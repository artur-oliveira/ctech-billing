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
	"sync"
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
	// levels and customers bill prices aggregated by max (WithLevels).
	levels    *repositories.LevelRepository
	customers *repositories.CustomerRepository
	// tenantGuard skips the subscriptions of a tenant the sweep must leave alone
	// (WithTenantGuard). Nil: none.
	tenantGuard func(ctx context.Context, tenantOrgID string) (bool, error)
	// charges creates a BYOK tenant's due-date charge after an invoice is
	// issued (spec 2026-10-11 § 7.2). Nil: nothing to create.
	charges ChargeCreator
	// workers and perOrg bound the daily sweep's concurrency (WithConcurrency).
	workers, perOrg int
}

// WithCharges attaches the BYOK rail's hook after every issue.
// WithTenantGuard makes the daily sweep skip (and count as suspended) the
// subscriptions of a tenant for which skip answers true: a tenant being closed
// issues no new invoices during its read-only grace.
// WithConcurrency sets how many subscriptions the daily sweep bills at once
// overall, and at most how many of one tenant. The default is 1 and 1: serial, as
// the sweep always was. Raising it divides the run time of a renewal wave by about
// the number of workers; perOrg stays low because one tenant's invoices share a
// gapless counter whose compare-and-set retries do not scale with contenders.
func (s *Invoicer) WithConcurrency(workers, perOrg int) *Invoicer {
	s.workers, s.perOrg = max(workers, 1), max(perOrg, 1)
	return s
}

func (s *Invoicer) WithTenantGuard(skip func(ctx context.Context, tenantOrgID string) (bool, error)) *Invoicer {
	s.tenantGuard = skip
	return s
}

func (s *Invoicer) WithCharges(c ChargeCreator) *Invoicer {
	s.charges = c
	return s
}

// finalize is every Finalize of this package: a BYOK tenant's late fees are
// copied onto the invoice with it (spec § 7.2), and an issued invoice that
// owes something gets its charge after the write commits, never inside it.
func (s *Invoicer) finalize(ctx context.Context, inv *billing.Invoice, dueDate, settlement brcal.Date, cause billing.Cause, actor, requestID string, now time.Time) error {
	if s.orgs != nil && s.charges != nil {
		if org, err := s.orgs.Get(ctx, inv.OrganizationID, inv.Livemode); err == nil && org.Rail() == billing.RailBYOK {
			inv.Collection = org.Collection
		}
	}
	if _, err := s.invoices.Finalize(ctx, inv, dueDate, settlement, cause, actor, requestID, now); err != nil {
		return err
	}
	if s.charges != nil && !inv.NothingDue() {
		s.charges.Issued(ctx, inv, now)
	}
	return nil
}

// ErrLevelsNotWired is a max price closed by an Invoicer built without levels.
var ErrLevelsNotWired = errors.New("level metering is not wired into this invoicer")

// WithLevels lets the invoicer bill prices aggregated by max (spec § 6.3). A
// nil one refuses them rather than billing 0.
func (s *Invoicer) WithLevels(levels *repositories.LevelRepository, customers *repositories.CustomerRepository) *Invoicer {
	s.levels, s.customers = levels, customers
	return s
}

func NewInvoicer(
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	catalog *repositories.CatalogRepository,
	usage *repositories.UsageRepository,
) *Invoicer {
	return &Invoicer{subs: subs, invoices: invoices, catalog: catalog, usage: usage, workers: 1, perOrg: 1}
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

	return s.issue(ctx, inv, sub, period, actor, now)
}

// issue takes a DRAFT invoice of a period to its issued state: finalized and,
// when nothing is owed, closed. It is the second half of generating an invoice,
// split out so a re-run can finish one a previous run created and never issued.
func (s *Invoicer) issue(ctx context.Context, inv *billing.Invoice, sub *billing.Subscription, period billing.Period, actor string, now time.Time) (*billing.Invoice, error) {
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
		if err := s.finalize(ctx, inv, dueDate, brcal.Date{}, billing.CauseScheduler, actor, "", now); err != nil {
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
	if err := s.finalize(ctx, inv, dueDate, settlement, billing.CauseScheduler, actor, "", now); err != nil {
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
		return billing.FixedLine(price, price.Label(product.Name), period, item.Quantity), nil
	}

	if price.Aggregation == billing.AggregationMax {
		units, err := s.maxLevel(ctx, sub, price, period)
		if err != nil {
			return billing.InvoiceItem{}, fmt.Errorf("levels for item %s: %w", item.ID, err)
		}
		return billing.MeteredLine(price, price.Label(product.Name), period, units), nil
	}

	records, err := s.usage.ListForPeriod(ctx, sub.OrganizationID, sub.Livemode, item.ID, period.Start)
	if err != nil {
		return billing.InvoiceItem{}, fmt.Errorf("usage for item %s: %w", item.ID, err)
	}
	units := billing.SumUsage(billing.DeduplicateUsage(records), period)
	return billing.MeteredLine(price, price.Label(product.Name), period, units), nil
}

// maxLevel is the highest level of the price's meter for the subscription's
// customer reference during period (spec § 6.3). A customer with no external
// reference cannot have levels, and that is an error, not a free month.
func (s *Invoicer) maxLevel(ctx context.Context, sub *billing.Subscription, price *billing.Price, period billing.Period) (int64, error) {
	if s.levels == nil || s.customers == nil {
		return 0, ErrLevelsNotWired
	}
	customer, err := s.customers.Get(ctx, sub.OrganizationID, sub.Livemode, sub.CustomerID)
	if err != nil {
		return 0, err
	}
	if customer.ExternalRef == "" {
		return 0, fmt.Errorf("customer %s has no external reference to read %s levels under", customer.ID, price.Meter)
	}
	carried, err := s.carriedIn(ctx, sub, customer.ExternalRef, price.Meter, period.Start.Time())
	if err != nil {
		return 0, err
	}
	records, err := s.levels.InPeriod(ctx, sub.OrganizationID, sub.Livemode, customer.ExternalRef, price.Meter, period.Start.Time(), period.End.Time())
	if err != nil {
		return 0, err
	}
	return billing.MaxLevel(records, carried, period), nil
}

// carriedIn is the level held at start (scope decision 9): the newest in-TTL
// report before it; else the `previous` of the first report from it; else the
// no-TTL latest level, which is then before start; else 0.
func (s *Invoicer) carriedIn(ctx context.Context, sub *billing.Subscription, ref, meter string, start time.Time) (int64, error) {
	return s.levels.CarriedIn(ctx, sub.OrganizationID, sub.Livemode, ref, meter, start)
}

// SweepResult reports what a daily run did. Counts rather than a bare error,
// because "the sweep ran and 3 of 400 subscriptions failed" and "the sweep did
// not run" are different incidents with different responses.
type SweepResult struct {
	Examined int
	Invoiced int
	Skipped  int // already invoiced for the period — a re-run, which is normal
	Failed   int
	// Suspended counts subscriptions of a suspended live tenant, left for a
	// re-run after the lift (spec § 4.3).
	Suspended int
	Errors    []error
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
	if err := s.finalize(ctx, inv, dueDate, settlement, billing.CauseManual, actor, requestID, now); err != nil {
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
	var (
		mu     sync.Mutex
		result SweepResult
	)
	record := func(f func(*SweepResult)) {
		mu.Lock()
		f(&result)
		mu.Unlock()
	}

	suspended := newSuspensionCache(s.orgs)
	limit := newOrgLimiter(s.perOrg)
	work := make(chan billing.Subscription)
	var wg sync.WaitGroup
	for i := 0; i < max(s.workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sub := range work {
				s.sweepOne(ctx, &sub, actor, now, suspended, limit, record)
			}
		}()
	}

	var startKey map[string]types.AttributeValue
produce:
	for {
		page, err := s.subs.DueOn(ctx, livemode, date, 100, startKey)
		if err != nil {
			record(func(r *SweepResult) {
				r.Errors = append(r.Errors, fmt.Errorf("read due subscriptions for %s: %w", date, err))
			})
			break
		}
		for i := range page.Items {
			record(func(r *SweepResult) { r.Examined++ })
			select {
			case work <- page.Items[i]:
			case <-ctx.Done():
				record(func(r *SweepResult) {
					r.Errors = append(r.Errors, fmt.Errorf("sweep for %s interrupted: %w", date, ctx.Err()))
				})
				break produce
			}
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		startKey = page.LastEvaluatedKey
	}
	close(work)
	wg.Wait()
	return result
}

// sweepOne invoices one due subscription and records what happened. At most
// perOrg subscriptions of one tenant are in flight: they all number their invoices
// from one gapless counter, and more contenders than that only burn its retries.
func (s *Invoicer) sweepOne(ctx context.Context, sub *billing.Subscription, actor string, now time.Time,
	suspended *suspensionCache, limit *orgLimiter, record func(func(*SweepResult))) {
	fail := func(format string, err error) {
		record(func(r *SweepResult) {
			r.Failed++
			r.Errors = append(r.Errors, fmt.Errorf(format, sub.ID, err))
		})
	}
	if skip, err := suspended.skip(ctx, sub.OrganizationID, sub.Livemode); err != nil {
		fail("subscription %s: reading its tenant: %w", err)
		return
	} else if skip {
		record(func(r *SweepResult) { r.Suspended++ })
		return
	}
	if s.tenantGuard != nil {
		skip, err := s.tenantGuard(ctx, sub.OrganizationID)
		if err != nil {
			fail("subscription %s: reading the closure flag: %w", err)
			return
		}
		if skip {
			record(func(r *SweepResult) { r.Suspended++ })
			return
		}
	}
	release := limit.acquire(sub.OrganizationID)
	defer release()
	switch err := s.invoiceOne(ctx, sub, actor, now); {
	case err == nil:
		record(func(r *SweepResult) { r.Invoiced++ })
	case errors.Is(err, repositories.ErrAlreadyGenerated):
		record(func(r *SweepResult) { r.Skipped++ })
	default:
		fail("subscription %s: %w", err)
	}
}

// orgLimiter caps how many subscriptions of one tenant the sweep bills at once.
type orgLimiter struct {
	per  int
	mu   sync.Mutex
	sems map[string]chan struct{}
}

func newOrgLimiter(per int) *orgLimiter {
	return &orgLimiter{per: max(per, 1), sems: map[string]chan struct{}{}}
}

func (l *orgLimiter) acquire(org string) (release func()) {
	l.mu.Lock()
	sem, ok := l.sems[org]
	if !ok {
		sem = make(chan struct{}, l.per)
		l.sems[org] = sem
	}
	l.mu.Unlock()
	sem <- struct{}{}
	return func() { <-sem }
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
	if sub.CancelAtPeriodEnd {
		return s.endAtBoundary(ctx, sub, items, actor, now)
	}
	if pc := sub.PendingChange; pc != nil && pc.At == sub.CurrentPeriod().End {
		return s.changeAtBoundary(ctx, sub, items, pc, actor, now)
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
		// A previous run may have created the DRAFT and died, or failed to
		// finalize it (throttling, numbering contention). Renewing past it would
		// leave the period unbilled for good, so the invoice is issued first; a
		// failure here holds the renewal back and the next run tries again.
		if err := s.issueLeftoverDraft(ctx, sub, period, actor, now); err != nil {
			return err
		}
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

// issueLeftoverDraft finishes the invoice a previous run created for this
// period but never issued. Anything not a DRAFT is already past the point this
// guards.
func (s *Invoicer) issueLeftoverDraft(ctx context.Context, sub *billing.Subscription, period billing.Period, actor string, now time.Time) error {
	inv, err := s.invoices.GetByGenerationKey(ctx, sub.OrganizationID, sub.Livemode, billing.GenerationKey(sub.ID, period.Start))
	if err != nil {
		return fmt.Errorf("reading the invoice generated for %s: %w", period.Start, err)
	}
	if inv.Status != billing.InvoiceDraft {
		return nil
	}
	_, err = s.issue(ctx, inv, sub, period, actor, now)
	return err
}

// endAtBoundary executes a cancellation scheduled for the period end. In
// arrears the period that just ended was served and is billed now; in advance
// the period that would start is not billed. Re-runnable: an already generated
// period and an already canceled subscription are not failures.
func (s *Invoicer) endAtBoundary(ctx context.Context, sub *billing.Subscription, items []billing.SubscriptionItem, actor string, now time.Time) error {
	if sub.Timing == billing.BillArrears {
		if _, err := s.GenerateForPeriod(ctx, sub, items, billing.PeriodToInvoice(sub), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	if _, err := s.subs.Transition(ctx, sub, billing.SubscriptionCanceled, billing.CauseScheduler, actor, "", now); err != nil &&
		!errors.Is(err, repositories.ErrConcurrentModification) && !errors.Is(err, billing.ErrInvalidTransition) {
		return err
	}
	return nil
}

// changeAtBoundary applies a scheduled plan change as the period ends. The
// period that ends is billed under the old items only if they bill in arrears
// (an advance one was paid already); the change and the renewal are one write;
// the period that starts is billed now only if the new items bill in advance.
func (s *Invoicer) changeAtBoundary(ctx context.Context, sub *billing.Subscription, items []billing.SubscriptionItem,
	pc *billing.PendingChange, actor string, now time.Time) error {
	if sub.Timing == billing.BillArrears {
		if _, err := s.GenerateForPeriod(ctx, sub, items, billing.PeriodToInvoice(sub), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	newItems := make([]billing.SubscriptionItem, len(pc.PriceIDs))
	timing, owner := sub.Timing, sub.OwnerKey
	for i, priceID := range pc.PriceIDs {
		price, err := s.catalog.GetPrice(ctx, sub.OrganizationID, sub.Livemode, priceID)
		if err != nil {
			return err
		}
		product, err := s.catalog.GetProduct(ctx, sub.OrganizationID, sub.Livemode, price.ProductID)
		if err != nil {
			return err
		}
		timing, owner = price.Timing, product.OwnerKey
		newItems[i] = billing.SubscriptionItem{ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: sub.OrganizationID,
			Livemode: sub.Livemode, SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1}
	}
	if err := s.subs.ApplyScheduledChange(ctx, sub, items, newItems, timing, owner, actor, now); err != nil {
		return err
	}
	if timing == billing.BillAdvance {
		if _, err := s.GenerateForPeriod(ctx, sub, newItems, sub.CurrentPeriod(), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	return nil
}
