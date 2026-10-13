package services

import (
	"context"
	"errors"
	"slices"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ErrUnknownPlan is a plan key that is not in the public catalogue.
var ErrUnknownPlan = errors.New("billing: not a public plan")

// ErrNoBillingPlan: the tenant holds no prod_billing subscription to change.
var ErrNoBillingPlan = errors.New("billing: the organization has no plan to change")

// BillingPlanOption is one public BYOK plan (spec § 12.5).
type BillingPlanOption struct {
	Plan            string   `json:"plan"`
	Fee             int64    `json:"fee"`
	Included        int64    `json:"included"`
	OverageUnit     int64    `json:"overage_unit"`
	Refuse          bool     `json:"refuse"`
	BankConnections int64    `json:"bank_connections"`
	Users           int64    `json:"users"`
	APIAccess       bool     `json:"api_access"`
	PriceIDs        []string `json:"-"`
}

// BillingPlanUsage is this month's use against the plan; -1 is unlimited.
type BillingPlanUsage struct {
	ChargesThisMonth     int64 `json:"charges_this_month"`
	Quota                int64 `json:"quota"`
	SubscriptionsActive  int64 `json:"subscriptions_active"`
	SubscriptionsQuota   int64 `json:"subscriptions_quota"`
	BankConnections      int64 `json:"bank_connections"`
	BankConnectionsQuota int64 `json:"bank_connections_quota"`
}

// BillingPlanState is the console's plan screen.
type BillingPlanState struct {
	Current struct {
		Plan           string `json:"plan"`
		SubscriptionID string `json:"subscription_id"`
	} `json:"current"`
	Usage BillingPlanUsage    `json:"usage"`
	Plans []BillingPlanOption `json:"plans"`
}

// BillingPlanScreen is the console's plan screen (part C Task 6): the public
// plans, the tenant's plan and its use, and the change.
type BillingPlanScreen struct {
	plans      *BillingPlans
	byok       *repositories.ByokRepository
	subscriber *Subscriber
}

func NewBillingPlanScreen(plans *BillingPlans, byok *repositories.ByokRepository, subscriber *Subscriber) *BillingPlanScreen {
	return &BillingPlanScreen{plans: plans, byok: byok, subscriber: subscriber}
}

// Catalogue lists the public plans from tenant zero's catalogue: prod_billing
// prices with a plan key, neither internal nor early access, cheapest first.
func (s *BillingPlanScreen) Catalogue(ctx context.Context) ([]BillingPlanOption, error) {
	prices, err := s.plans.catalog.ListPrices(ctx, s.plans.tenantZero, true, 500)
	if err != nil {
		return nil, err
	}
	byPlan := map[string]*BillingPlanOption{}
	var out []*BillingPlanOption
	for _, p := range prices {
		key := p.Metadata["plan"]
		if p.ProductID != "prod_billing" || p.Archived || key == "" || key == "early_access" || p.Metadata["visibility"] == "internal" {
			continue
		}
		o := byPlan[key]
		if o == nil {
			o = &BillingPlanOption{Plan: key}
			byPlan[key] = o
			out = append(out, o)
		}
		o.PriceIDs = append(o.PriceIDs, p.ID)
		if p.Type == billing.PriceMetered {
			o.OverageUnit, o.Included = int64(p.UnitAmount), p.IncludedQuantity
			continue
		}
		l := limitsOf(p.Metadata)
		o.Fee, o.Refuse, o.BankConnections, o.Users, o.APIAccess = int64(p.UnitAmount), l.RefuseOverage, l.BankConnections, l.Users, l.APIAccess
		if o.Included == 0 {
			o.Included = l.ChargesPerMonth
		}
	}
	slices.SortStableFunc(out, func(a, b *BillingPlanOption) int { return int(a.Fee - b.Fee) })
	list := make([]BillingPlanOption, len(out))
	for i, o := range out {
		list[i] = *o
	}
	return list, nil
}

// State is the live tenant's plan and use (test mode reads the live plan).
func (s *BillingPlanScreen) State(ctx context.Context, org *billing.Organization, now time.Time) (*BillingPlanState, error) {
	var st BillingPlanState
	var err error
	if st.Plans, err = s.Catalogue(ctx); err != nil {
		return nil, err
	}
	limits, _, err := s.plans.For(ctx, org)
	if err != nil {
		return nil, err
	}
	st.Current.Plan, st.Current.SubscriptionID = limits.Plan, limits.SubscriptionID
	u := &st.Usage
	u.Quota, u.SubscriptionsQuota, u.BankConnectionsQuota = limits.ChargesPerMonth, limits.ChargesPerMonth, limits.BankConnections
	if u.ChargesThisMonth, err = s.byok.ChargesThisMonth(ctx, org.ID, repositories.ChargeMonth(now)); err != nil {
		return nil, err
	}
	if org.Rail() == billing.RailBYOK {
		if u.SubscriptionsActive, err = s.byok.SubscriptionCount(ctx, org.ID); err != nil {
			return nil, err
		}
	}
	conns, err := s.byok.ListConnections(ctx, org.AccountOrganizationID, true)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.State != "" {
			u.BankConnections++
		}
	}
	return &st, nil
}

// Change moves the tenant's prod_billing subscription onto a public plan's
// prices (Subscriber.ChangePlan: proration as today). A downgrade below the
// current use is allowed: caps apply to new creations only.
func (s *BillingPlanScreen) Change(ctx context.Context, org *billing.Organization, plan, actor, requestID string, now time.Time) error {
	options, err := s.Catalogue(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(options, func(o BillingPlanOption) bool { return o.Plan == plan })
	if i < 0 {
		return ErrUnknownPlan
	}
	sub, err := s.plans.Subscription(ctx, org)
	if err != nil {
		return err
	}
	if sub == nil {
		return ErrNoBillingPlan
	}
	limits, _, err := s.plans.For(ctx, org)
	if err != nil || limits.Plan == plan {
		return err
	}
	items := make([]SubscribeItem, len(options[i].PriceIDs))
	for j, id := range options[i].PriceIDs {
		items[j] = SubscribeItem{PriceID: id}
	}
	_, err = s.subscriber.ChangePlan(ctx, sub, ChangeInput{Items: items, Cause: billing.CauseCustomer, Actor: actor, RequestID: requestID}, now)
	return err
}
