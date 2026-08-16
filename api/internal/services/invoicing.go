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
}

func NewInvoicer(
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	catalog *repositories.CatalogRepository,
	usage *repositories.UsageRepository,
) *Invoicer {
	return &Invoicer{subs: subs, invoices: invoices, catalog: catalog, usage: usage}
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
		if _, err := s.invoices.Finalize(ctx, inv, dueDate, brcal.Date{}, actor, "", now); err != nil {
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
	settlement := billing.FirstDunningDate(dueDate)
	if _, err := s.invoices.Finalize(ctx, inv, dueDate, settlement, actor, "", now); err != nil {
		return nil, err
	}
	return inv, nil
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
