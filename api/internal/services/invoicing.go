// Package services holds the orchestration between the pure domain and the
// repositories: the steps that need both, and nothing that needs neither.
//
// The arithmetic and the transition rules do not live here — they live in
// internal/domain/billing, where they are tested without a database. What lives
// here is the ordering: which period to bill, what to read before writing, and
// what must happen even when one subscription is broken.
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Invoicer turns a due subscription period into a finalized invoice.
type Invoicer struct {
	subs     *repositories.SubscriptionRepository
	invoices *repositories.InvoiceRepository
	catalog  *repositories.CatalogRepository
	usage    *repositories.UsageRepository
	// orgs answers "what is this tenant's default dunning policy". Optional: a
	// nil one resolves every invoice to the product's policy or the built-in
	// default, which is what a test that does not care about dunning wants.
	orgs *repositories.OrganizationRepository
}

func NewInvoicer(
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	catalog *repositories.CatalogRepository,
	usage *repositories.UsageRepository,
) *Invoicer {
	return &Invoicer{subs: subs, invoices: invoices, catalog: catalog, usage: usage}
}

// WithOrganizations supplies the tenant's default dunning policy.
//
// A setter rather than a constructor argument, unlike Collector's `subs`: a nil
// organizations repository here degrades to the built-in schedule, which is
// exactly what an invoice with no configured policy follows anyway. Nothing is
// silently wrong, so nothing needs to be a compile error.
func (s *Invoicer) WithOrganizations(orgs *repositories.OrganizationRepository) *Invoicer {
	s.orgs = orgs
	return s
}

// NetDaysDefault is how many days after the period boundary an invoice falls
// due, before the business-day roll-forward.
//
// Zero for now: the customer is billed on the boundary itself. It is a named
// constant rather than a literal because it is the first thing a merchant will
// want to configure, and when that happens the field belongs on the
// organization — not on every call site that had a 0 in it.
const NetDaysDefault = 0

// GenerateForPeriod builds and finalizes the invoice covering period.
//
// One invoice, one line per item. A subscription that meters NF-e, NFC-e, CT-e
// and MDF-e separately produces four lines on one document, not four documents:
// the customer agreed to one plan and pays one PIX.
//
// It is idempotent by construction: the invoice is written under the
// {subscription_id}:{period_start} generation key, so a second call for the same
// period returns ErrAlreadyGenerated without writing anything. That is what makes
// the daily sweep safe to re-run, and re-running it is not an edge case — it is
// what happens every time the job is retried after a timeout.
//
// **The payout gate is deliberately not applied here.** ADR 0005 gates *opening
// a charge*, and issuing an invoice is not one. Blocking generation for a
// merchant whose payout is not yet enabled would permanently lose the periods
// they were onboarding through; the charge attempt is where the 409 belongs.
func (s *Invoicer) GenerateForPeriod(
	ctx context.Context,
	sub *billing.Subscription,
	items []billing.SubscriptionItem,
	period billing.Period,
	actor string,
	now time.Time,
) (*billing.Invoice, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: subscription %s has no items", billing.ErrInvalidSubscriptionItem, sub.ID)
	}

	lines := make([]billing.InvoiceItem, 0, len(items))
	// The products' policies, collected while the lines are built rather than in
	// a second pass: every product on this invoice is already being read to name
	// its line.
	policies := make([]billing.DunningSchedule, 0, len(items))
	for _, item := range items {
		price, err := s.catalog.GetPrice(ctx, sub.OrganizationID, sub.Livemode, item.PriceID)
		if err != nil {
			return nil, fmt.Errorf("price for item %s: %w", item.ID, err)
		}
		product, err := s.catalog.GetProduct(ctx, sub.OrganizationID, sub.Livemode, price.ProductID)
		if err != nil {
			return nil, fmt.Errorf("product for price %s: %w", price.ID, err)
		}
		line, err := s.buildLine(ctx, sub, item, price, product, period)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
		policies = append(policies, product.DunningPolicy)
	}

	policy, err := s.resolvePolicy(ctx, sub.OrganizationID, sub.Livemode, policies)
	if err != nil {
		return nil, err
	}

	inv := &billing.Invoice{
		ID:             id.NewWithPrefix(id.PrefixInvoice),
		OrganizationID: sub.OrganizationID,
		Livemode:       sub.Livemode,
		CustomerID:     sub.CustomerID,
		SubscriptionID: sub.ID,
		OwnerKey:       sub.OwnerKey,
		Status:         billing.InvoiceDraft,
		Period:         period,
		Currency:       billing.CurrencyBRL,
		// Copied, never shared: an invoice is a historical record, and pointing at
		// the subscription's live metadata would let a later edit rewrite the past
		// of closed invoices (ADR 0008).
		Metadata: sub.Metadata.Clone(),
		// Stamped at creation, so the schedule an invoice follows is decided once
		// and cannot be rewritten under an invoice already being chased.
		Policy: policy,
	}
	if err := billing.ApplyToInvoice(inv, lines, 0); err != nil {
		return nil, err
	}

	generationKey := billing.GenerationKey(sub.ID, period.Start)
	if err := s.invoices.Create(ctx, inv, lines, generationKey, now); err != nil {
		return nil, err
	}

	dueDate := billing.DueDate(period, sub.Timing, sub.NetDays)

	// A free plan, or a period whose metered usage was zero. The invoice is
	// issued — the period was served and the document says so, with a number in
	// the same gapless sequence — and then closed immediately, because there is
	// nothing to collect and therefore nothing to wait for.
	//
	// The dunning queue is not armed: a reminder about R$ 0,00 is worse than no
	// reminder, and a customer chased for a free plan learns to ignore the next
	// message, which will be about a real one.
	if inv.NothingDue() {
		if _, err := s.invoices.Finalize(ctx, inv, dueDate, brcal.Date{}, billing.CauseScheduler, actor, "", now); err != nil {
			return nil, err
		}
		if _, err := s.invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseNothingDue, actor, "", now); err != nil {
			// The invoice exists and is OPEN with nothing owed. That is visible and
			// harmless — nothing will chase it and nothing can charge for it — and
			// the next attempt closes it, so it is reported rather than papered over.
			return inv, fmt.Errorf("closing zero-total invoice %s: %w", inv.ID, err)
		}
		return inv, nil
	}

	// The dunning queue, armed at the first step of the policy — which is three
	// days *before* the due date, because the only message that prevents a late
	// invoice is one that arrives while it can still be paid on time.
	settlement := inv.Schedule().FirstDunningDate(dueDate)
	if _, err := s.invoices.Finalize(ctx, inv, dueDate, settlement, billing.CauseScheduler, actor, "", now); err != nil {
		return nil, err
	}
	return inv, nil
}

// resolvePolicy picks the schedule for an invoice: the product's when the
// products agree, then the organization's, then the built-in default.
//
// The organization is read on every generation rather than cached, and that is
// affordable — one point read per invoice, against a row the sweep is already
// tenant-scoped to. A cache here would be a policy change that takes effect on
// some invoices and not others in the same run.
func (s *Invoicer) resolvePolicy(
	ctx context.Context,
	organizationID string,
	livemode bool,
	products []billing.DunningSchedule,
) (billing.DunningSchedule, error) {
	var orgPolicy billing.DunningSchedule
	if s.orgs != nil {
		org, err := s.orgs.Get(ctx, organizationID, livemode)
		if err != nil {
			return nil, fmt.Errorf("reading the organization's dunning policy: %w", err)
		}
		orgPolicy = org.DunningPolicy
	}
	return billing.ResolveDunningPolicy(orgPolicy, products), nil
}

// buildLine produces one item's invoice line for the period.
//
// A metered line reads the closed period's usage from one partition and
// deduplicates it before summing. The repository already rejects a duplicate
// idempotency key on write, so this second pass only matters if one ever got
// through — and double-counting consumption is an overcharge the customer finds
// before we do.
func (s *Invoicer) buildLine(
	ctx context.Context,
	sub *billing.Subscription,
	item billing.SubscriptionItem,
	price *billing.Price,
	product *billing.Product,
	period billing.Period,
) (billing.InvoiceItem, error) {
	if price.Type != billing.PriceMetered {
		return billing.FixedLine(price, product.Name, period, item.Quantity), nil
	}

	records, err := s.usage.ListForPeriod(ctx, sub.OrganizationID, sub.Livemode, item.ID, period.Start)
	if err != nil {
		return billing.InvoiceItem{}, fmt.Errorf("usage for item %s: %w", item.ID, err)
	}
	units := billing.SumUsage(billing.DeduplicateUsage(records), period)
	return billing.MeteredLine(price, product.Name, period, units), nil
}

// SweepResult reports what a daily run did. Counts rather than a bare error,
// because "the sweep ran and 3 of 400 subscriptions failed" and "the sweep did
// not run" are different incidents with different responses.
type SweepResult struct {
	Examined int
	Invoiced int
	Skipped  int // already invoiced for the period — a re-run, which is normal
	Failed   int
	Errors   []error
}

// Issue finalizes a draft invoice that the sweep left behind (C3).
//
// A DRAFT is normally transient: GenerateForPeriod creates one and finalizes it
// in the same call. One that outlives that call is the residue of a half-failed
// run — the invoice exists, nobody has been billed for it, and nothing will ever
// pick it up again, because the sweep skips a period it has already written.
// This is the route that finishes it, and it is why the console needs a write at
// all rather than an operator being told to wait.
//
// It is the sweep's own path, not a second one: the same due date, the same
// dunning arming, the same zero-total settlement. What differs is the cause —
// CauseManual, so the trail says a person issued this — and that is exactly the
// distinction a second implementation would eventually lose.
func (s *Invoicer) Issue(
	ctx context.Context,
	inv *billing.Invoice,
	actor, requestID string,
	now time.Time,
) error {
	timing, netDays := billing.BillArrears, 0
	if inv.SubscriptionID != "" {
		sub, err := s.subs.Get(ctx, inv.OrganizationID, inv.Livemode, inv.SubscriptionID)
		if err != nil {
			return err
		}
		timing, netDays = sub.Timing, sub.NetDays
	}
	// A one-off invoice with no subscription has no timing of its own, so it
	// falls due at the end of the period it bills — arrears, which is the only
	// reading that cannot bill somebody before the service existed.
	dueDate := billing.DueDate(inv.Period, timing, netDays)

	settlement := inv.Schedule().FirstDunningDate(dueDate)
	if inv.NothingDue() {
		settlement = brcal.Date{}
	}
	if _, err := s.invoices.Finalize(ctx, inv, dueDate, settlement, billing.CauseManual, actor, requestID, now); err != nil {
		return err
	}
	if inv.NothingDue() {
		// Same close as the sweep's, and for the same reason: there is nothing to
		// collect, so there is nothing to wait for (ADR 0019).
		if _, err := s.invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseNothingDue, actor, requestID, now); err != nil {
			return fmt.Errorf("closing zero-total invoice %s: %w", inv.ID, err)
		}
	}
	return nil
}

// RunDailySweep invoices every subscription due on `date`.
//
// One broken subscription must not stop billing everyone else, so a failure is
// counted and the sweep continues. The alternative — abort on first error — is
// how a single bad price reference stops a company's entire revenue for a day,
// and it is discovered by the customers who were not billed.
//
// It is cross-tenant by design (ADR 0002) and must only ever be called from a
// scheduled job, never from a request.
func (s *Invoicer) RunDailySweep(ctx context.Context, livemode bool, date brcal.Date, actor string, now time.Time) SweepResult {
	var result SweepResult
	var startKey map[string]types.AttributeValue

	for {
		page, err := s.subs.DueOn(ctx, livemode, date, 100, startKey)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read due subscriptions for %s: %w", date, err))
			return result
		}
		for i := range page.Items {
			sub := page.Items[i]
			result.Examined++
			switch err := s.invoiceOne(ctx, &sub, actor, now); {
			case err == nil:
				result.Invoiced++
			case errors.Is(err, repositories.ErrAlreadyGenerated):
				result.Skipped++
			default:
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("subscription %s: %w", sub.ID, err))
			}
		}
		if page.LastEvaluatedKey == nil {
			return result
		}
		startKey = page.LastEvaluatedKey
	}
}

// invoiceOne bills a subscription and then advances it to the next period.
//
// The order matters and is not interchangeable: the invoice is written first,
// under its generation key, and only then is the subscription renewed. If the
// process dies between the two, the next run regenerates nothing (the key
// blocks it) and completes the renewal. Renewing first would move the sweep date
// forward past a period that was never invoiced — revenue lost silently, which
// is the failure mode nobody notices.
func (s *Invoicer) invoiceOne(ctx context.Context, sub *billing.Subscription, actor string, now time.Time) error {
	items, err := s.subs.ListItems(ctx, sub.OrganizationID, sub.Livemode, sub.ID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%w: subscription %s has no items", billing.ErrInvalidSubscriptionItem, sub.ID)
	}

	period := billing.PeriodToInvoice(sub)
	genErr := error(nil)
	if _, err := s.GenerateForPeriod(ctx, sub, items, period, actor, now); err != nil {
		if !errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
		// Already invoiced: this is a re-run. The renewal below may still be
		// outstanding, so it is not skipped.
		genErr = err
	}

	if _, err := s.subs.Transition(ctx, sub, billing.SubscriptionActive, billing.CauseRenewal, actor, "", now); err != nil {
		// A renewal that cannot happen because the subscription already moved on
		// is not a failure of this run.
		if !errors.Is(err, repositories.ErrConcurrentModification) && !errors.Is(err, billing.ErrInvalidTransition) {
			return err
		}
	}
	return genErr
}
