package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// FinanceOwnerKey is CTech Finanças' owner in tenant zero's catalogue.
const FinanceOwnerKey = "finance"

var (
	ErrNotAPlanPrice       = errors.New("not a Finanças plan price")
	ErrCustomerRefMismatch = errors.New("the person's customer is not USER_{sub}")
	ErrPlanPaymentPending  = errors.New("the plan's first invoice is not paid yet")
	ErrNoFinancePlan       = errors.New("no Finanças subscription")
)

// PlanPaymentPending carries the unpaid first invoice, so the screen can send
// the person to pay it.
type PlanPaymentPending struct{ Invoice *billing.Invoice }

func (e *PlanPaymentPending) Error() string { return ErrPlanPaymentPending.Error() }
func (e *PlanPaymentPending) Unwrap() error { return ErrPlanPaymentPending }

// FinancePlan is the person's Finanças plan (spec § 7), always in tenant zero,
// live (scope decision 2).
type FinancePlan struct {
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	invoices   *repositories.InvoiceRepository
	catalog    *repositories.CatalogRepository
	levels     *repositories.LevelRepository
	subscriber *Subscriber
	tenant     string
}

func NewFinancePlan(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository, catalog *repositories.CatalogRepository,
	levels *repositories.LevelRepository, subscriber *Subscriber, tenantZero string) *FinancePlan {
	return &FinancePlan{customers: customers, subs: subs, invoices: invoices, catalog: catalog, levels: levels, subscriber: subscriber, tenant: tenantZero}
}

// Enabled is false where no tenant zero is configured.
func (s *FinancePlan) Enabled() bool { return s != nil && s.tenant != "" }

type LevelView struct {
	Current int64 `json:"current"`
	Peak    int64 `json:"peak"`
}

type PlanState struct {
	Subscription   *billing.Subscription
	PriceIDs       []string
	PendingChange  *billing.PendingChange
	OpenInvoice    *billing.Invoice
	Prices         []billing.Price
	DefaultPriceID string
	Period         billing.Period
	Levels         map[string]LevelView
	Projected      billing.Cents
}

type ChooseInput struct {
	UserID, Name, Email string
	PriceIDs            []string
	RequestID           string
}

func userRef(userID string) string { return "USER_" + userID }

// catalogue returns the active prices of finance products and the default.
func (s *FinancePlan) catalogue(ctx context.Context) ([]billing.Price, string, error) {
	products, err := s.catalog.ListProducts(ctx, s.tenant, true, 100)
	if err != nil {
		return nil, "", err
	}
	finance := map[string]bool{}
	def := ""
	for _, p := range products {
		if p.OwnerKey == FinanceOwnerKey && p.Active {
			finance[p.ID] = true
			if p.DefaultPriceID != "" {
				def = p.DefaultPriceID
			}
		}
	}
	all, err := s.catalog.ListPrices(ctx, s.tenant, true, 100)
	if err != nil {
		return nil, "", err
	}
	var out []billing.Price
	for _, p := range all {
		if finance[p.ProductID] && !p.Archived {
			out = append(out, p)
		}
	}
	return out, def, nil
}

// current is the person's Finanças subscription that is not canceled.
func (s *FinancePlan) current(ctx context.Context, customerID string) (*billing.Subscription, error) {
	subs, err := s.subs.ListByCustomer(ctx, s.tenant, true, customerID, 100)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].OwnerKey == FinanceOwnerKey && subs[i].Status != billing.SubscriptionCanceled {
			return &subs[i], nil
		}
	}
	return nil, nil
}

func (s *FinancePlan) openInvoice(ctx context.Context, sub *billing.Subscription) (*billing.Invoice, error) {
	invs, err := s.invoices.ListBySubscription(ctx, s.tenant, true, sub.ID, 10)
	if err != nil {
		return nil, err
	}
	for i := range invs {
		if invs[i].Status == billing.InvoiceOpen {
			return &invs[i], nil
		}
	}
	return nil, nil
}

func (s *FinancePlan) State(ctx context.Context, userID string, now time.Time) (*PlanState, error) {
	prices, def, err := s.catalogue(ctx)
	if err != nil {
		return nil, err
	}
	st := &PlanState{Prices: prices, DefaultPriceID: def, Levels: map[string]LevelView{}}
	today := brcal.FromTime(now)
	st.Period = billing.Period{Start: brcal.New(today.Year, today.Month, 1), End: brcal.New(today.Year, today.Month+1, 1)}

	customer, err := s.customers.GetByUser(ctx, s.tenant, true, userID)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		sub, err := s.current(ctx, customer.ID)
		if err != nil {
			return nil, err
		}
		if sub != nil {
			st.Subscription, st.Period, st.PendingChange = sub, sub.CurrentPeriod(), sub.PendingChange
			items, err := s.subs.ListItems(ctx, s.tenant, true, sub.ID)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				st.PriceIDs = append(st.PriceIDs, it.PriceID)
			}
			if st.OpenInvoice, err = s.openInvoice(ctx, sub); err != nil {
				return nil, err
			}
		}
	}
	return st, s.project(ctx, userID, st)
}

// project fills Levels and Projected from billing's own level reports — the
// numbers the close bills (scope decision 7).
func (s *FinancePlan) project(ctx context.Context, userID string, st *PlanState) error {
	for _, p := range st.Prices {
		if p.Aggregation != billing.AggregationMax {
			continue
		}
		carried := int64(0)
		before, err := s.levels.LatestBefore(ctx, s.tenant, true, userRef(userID), p.Meter, st.Period.Start.Time())
		if err != nil {
			return err
		}
		if before != nil {
			carried = before.Value
		}
		records, err := s.levels.InPeriod(ctx, s.tenant, true, userRef(userID), p.Meter, st.Period.Start.Time(), st.Period.End.Time())
		if err != nil {
			return err
		}
		current := carried
		if len(records) > 0 {
			current = records[len(records)-1].Value
		}
		peak := billing.MaxLevel(records, carried, st.Period)
		st.Levels[p.Meter] = LevelView{Current: current, Peak: peak}
		st.Projected += p.UnitAmount * billing.Cents(billing.BillableUnits(peak, p.IncludedQuantity))
	}
	return nil
}

// customerFor finds or creates USER_{sub} with its portal pointer.
func (s *FinancePlan) customerFor(ctx context.Context, in ChooseInput, now time.Time) (*billing.Customer, error) {
	c, err := s.customers.GetByUser(ctx, s.tenant, true, in.UserID)
	if err == nil {
		if c.ExternalRef != userRef(in.UserID) {
			return nil, fmt.Errorf("%w: customer %s has external ref %q", ErrCustomerRefMismatch, c.ID, c.ExternalRef)
		}
		return c, nil
	}
	if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	c = &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: s.tenant, Livemode: true,
		ExternalRef: userRef(in.UserID), UserID: in.UserID, Name: in.Name, Email: in.Email,
	}
	err = s.customers.Create(ctx, c, "user:"+in.UserID, in.RequestID, now)
	if errors.Is(err, repositories.ErrUserAlreadyCustomer) {
		// The other tab won (Review Focus 1): use its customer.
		return s.customerFor(ctx, ChooseInput{UserID: in.UserID}, now)
	}
	return c, err
}

func (s *FinancePlan) validPrices(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: choose at least one price", ErrNotAPlanPrice)
	}
	prices, def, err := s.catalogue(ctx)
	if err != nil {
		return err
	}
	for _, want := range ids {
		ok := want != def && slices.ContainsFunc(prices, func(p billing.Price) bool { return p.ID == want })
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotAPlanPrice, want)
		}
	}
	return nil
}

func (s *FinancePlan) Choose(ctx context.Context, in ChooseInput, now time.Time) (*billing.Subscription, *billing.Invoice, error) {
	if err := s.validPrices(ctx, in.PriceIDs); err != nil {
		return nil, nil, err
	}
	customer, err := s.customerFor(ctx, in, now)
	if err != nil {
		return nil, nil, err
	}
	items := make([]SubscribeItem, len(in.PriceIDs))
	for i, p := range in.PriceIDs {
		items[i] = SubscribeItem{PriceID: p}
	}
	actor := "user:" + in.UserID

	sub, err := s.current(ctx, customer.ID)
	if err != nil {
		return nil, nil, err
	}
	if sub == nil {
		return s.subscriber.Subscribe(ctx, SubscribeInput{
			OrganizationID: s.tenant, Livemode: true, CustomerID: customer.ID, Items: items, Actor: actor,
		}, now)
	}
	open, err := s.openInvoice(ctx, sub)
	if err != nil {
		return nil, nil, err
	}
	current, err := s.subs.ListItems(ctx, s.tenant, true, sub.ID)
	if err != nil {
		return nil, nil, err
	}
	change := ChangeInput{Items: items, Cause: billing.CauseCustomer, Actor: actor, RequestID: in.RequestID}
	if samePrices(current, in.PriceIDs) {
		// The current plan again — a double click, or a retry while the first
		// invoice is still open — is the current state, never an error; it also
		// drops a change scheduled away from it.
		if sub.Status == billing.SubscriptionIncomplete {
			return sub, open, nil
		}
		return sub, open, s.subscriber.ClearPlanChange(ctx, sub, change, now)
	}
	if sub.Status == billing.SubscriptionIncomplete {
		return nil, nil, &PlanPaymentPending{Invoice: open}
	}
	newTiming, err := s.timingOf(ctx, in.PriceIDs[0])
	if err != nil {
		return nil, nil, err
	}
	if sub.Timing == billing.BillAdvance && newTiming == billing.BillArrears {
		// Decision 5a: never bill the paid period twice; the limits hold until then.
		return sub, nil, s.subscriber.SchedulePlanChange(ctx, sub, change, now)
	}
	inv, err := s.subscriber.ChangePlan(ctx, sub, change, now)
	return sub, inv, err
}

func (s *FinancePlan) timingOf(ctx context.Context, priceID string) (billing.BillingTiming, error) {
	p, err := s.catalog.GetPrice(ctx, s.tenant, true, priceID)
	if err != nil {
		return "", err
	}
	return p.Timing, nil
}

func samePrices(items []billing.SubscriptionItem, ids []string) bool {
	if len(items) != len(ids) {
		return false
	}
	for _, it := range items {
		if !slices.Contains(ids, it.PriceID) {
			return false
		}
	}
	return true
}

// Cancel goes back to Free at the end of the period (scope decision 8).
func (s *FinancePlan) Cancel(ctx context.Context, userID, requestID string, now time.Time) (*billing.Subscription, error) {
	customer, err := s.customers.GetByUser(ctx, s.tenant, true, userID)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, ErrNoFinancePlan
	}
	if err != nil {
		return nil, err
	}
	sub, err := s.current(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrNoFinancePlan
	}
	if sub.CancelAtPeriodEnd {
		return sub, nil
	}
	return sub, s.subscriber.Cancel(ctx, sub, true, billing.CauseCustomer, "user:"+userID, requestID, now)
}
