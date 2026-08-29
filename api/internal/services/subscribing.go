package services

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Subscriber creates and ends subscriptions.
type Subscriber struct {
	subs     *repositories.SubscriptionRepository
	catalog  *repositories.CatalogRepository
	invoicer *Invoicer
}

func NewSubscriber(
	subs *repositories.SubscriptionRepository,
	catalog *repositories.CatalogRepository,
	invoicer *Invoicer,
) *Subscriber {
	return &Subscriber{subs: subs, catalog: catalog, invoicer: invoicer}
}

// SubscribeItem is one price the subscription bills for.
type SubscribeItem struct {
	PriceID string
	// Quantity multiplies a fixed price. It is ignored for a metered one, where
	// the quantity is the reported usage. Below 1 it is read as 1.
	Quantity int64
}

// quantity applies the "below 1 reads as 1" rule.
//
// A method rather than an inline clamp because two things now need the answer —
// the item that gets written and the cost that decides the starting status — and
// two copies of a normalization rule is how they come to disagree about what a
// zero means.
func (it SubscribeItem) quantity() int64 {
	if it.Quantity < 1 {
		return 1
	}
	return it.Quantity
}

// SubscribeInput is what a caller must supply to start a subscription.
type SubscribeInput struct {
	OrganizationID string
	Livemode       bool
	CustomerID     string
	// Items is one or more prices billed as one agreement. Several is what a
	// usage-based plan needs — one meter per document type, one monthly bill.
	Items []SubscribeItem
	// Anchor is the day every future period is derived from. Defaults to today
	// in America/Sao_Paulo.
	Anchor   brcal.Date
	NetDays  int
	Metadata billing.Metadata
	Actor    string
}

// Subscribe creates a subscription, its items, and — when the prices are billed
// in advance — the invoice for its first period.
//
// The first invoice cannot come from the daily sweep: the sweep fires at period
// boundaries, and the first period has none behind it (see
// billing.PeriodToInvoice). So it is created here or never.
//
// **A paid plan starts INCOMPLETE**, not ACTIVE (assessment § 6.2). Service the
// customer has not paid for is not service they have, and INCOMPLETE is the state
// that says so — it is not PAST_DUE, because nothing is being taken away from
// somebody who never had it, and its expiry is therefore silent.
//
// This was deliberately deferred while nothing could collect money: shipping a
// state with no way out would have been worse than granting service early. Both
// halves of the way out now exist — Collector.activateSubscription walks
// INCOMPLETE -> ACTIVE when the first payment lands, and the activation-expiry
// sweep cancels the ones where it never does.
func (s *Subscriber) Subscribe(ctx context.Context, in SubscribeInput, now time.Time) (*billing.Subscription, *billing.Invoice, error) {
	if len(in.Items) == 0 {
		return nil, nil, fmt.Errorf("%w: a subscription needs at least one item", billing.ErrInvalidSubscriptionItem)
	}
	if in.Anchor.IsZero() {
		in.Anchor = brcal.FromTime(now)
	}
	if err := in.Metadata.Validate(); err != nil {
		return nil, nil, err
	}

	prices, ownerKey, err := s.resolveItemPrices(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	// Every price agrees on these two by the check above, so the first one speaks
	// for all of them.
	cycle := prices[0]

	// Decided before the row is written rather than corrected after it. A
	// subscription that exists as ACTIVE for even an instant is one an entitlement
	// check can see, and the whole point of INCOMPLETE is that the window does not
	// exist.
	//
	// Two ways to start ACTIVE, and they are different facts rather than one rule
	// with an exception. A **free** plan's first period costs nothing, so there is
	// no payment to wait for. An **arrears** plan has not yet served the period it
	// will bill for, so nothing is owed yet either — its first invoice comes from
	// the sweep when that period closes.
	status := billing.SubscriptionActive
	if cycle.Timing == billing.BillAdvance && firstPeriodCost(prices, in.Items) > 0 {
		status = billing.SubscriptionIncomplete
	}

	sub := &billing.Subscription{
		ID:             id.NewWithPrefix(id.PrefixSubscription),
		OrganizationID: in.OrganizationID,
		Livemode:       in.Livemode,
		CustomerID:     in.CustomerID,
		Status:         status,
		// The recurrence comes from the prices, not from the request: a
		// subscription whose cycle disagrees with the prices it is on would bill
		// the wrong amount for the wrong window, and nothing would flag it.
		Recurrence: cycle.Recurrence,
		Timing:     cycle.Timing,
		Anchor:     in.Anchor,
		NetDays:    in.NetDays,
		OwnerKey:   ownerKey,
		Metadata:   in.Metadata,
	}

	items := make([]billing.SubscriptionItem, len(in.Items))
	for i, want := range in.Items {
		quantity := want.quantity()
		items[i] = billing.SubscriptionItem{
			ID:             id.NewWithPrefix(id.PrefixSubscriptionItm),
			OrganizationID: in.OrganizationID,
			Livemode:       in.Livemode,
			SubscriptionID: sub.ID,
			PriceID:        prices[i].ID,
			Quantity:       quantity,
		}
	}
	if err := s.subs.Create(ctx, sub, items, now); err != nil {
		return nil, nil, err
	}

	if cycle.Timing != billing.BillAdvance {
		// Billed in arrears: nothing is owed until the period closes, and the
		// sweep will produce that invoice.
		return sub, nil, nil
	}
	inv, err := s.invoicer.GenerateForPeriod(ctx, sub, items, sub.CurrentPeriod(), in.Actor, now)
	if err != nil {
		// The subscription exists and is billable; only its first invoice is
		// missing. Returning it alongside the error lets the caller report a
		// partial success instead of implying nothing happened — and the
		// generation key means a retry produces exactly one invoice.
		return sub, nil, err
	}
	return sub, inv, nil
}

// ChangeInput is a mid-period plan change.
type ChangeInput struct {
	// Items is the **complete** new set, not a delta. A partial update would make
	// "remove the CT-e meter" and "forgot to send the CT-e meter" the same
	// request, and one of them silently stops billing something.
	Items []SubscribeItem
	// Cause separates an operator's change from the customer's own, in the audit
	// trail six months later.
	Cause     billing.Cause
	Actor     string
	RequestID string
}

// ChangePlan swaps a subscription onto a different set of prices, effective now,
// and bills the difference for the remainder of the current period.
//
// Four decisions worth naming, because each rules out something a caller might
// reasonably expect:
//
//   - **The anchor and the period index do not move.** Changing plan does not
//     change the day the customer is billed on. Moving the anchor would give them
//     a short or long month nobody asked for and shift every future invoice.
//   - **The recurrence may not change.** A monthly plan becoming annual is not a
//     swap; the period this subscription is in would stop meaning what the anchor
//     and index say it means. That is a new subscription, and refusing it here is
//     cheaper than a period-arithmetic bug found on an invoice.
//   - **Metered prices are never prorated.** Usage is billed for what was used.
//     There is no unearned remainder to credit and nothing to charge in advance,
//     and the closed period's usage still arrives on the normal sweep.
//   - **A change that nets zero or negative issues no invoice.** The plan called
//     for a zero-total document settled on issue (ADR 0019); what a negative net
//     actually is, is money owed back, and that is a CreditNote — a different
//     document, which this service does not yet issue. Capping the credit to make
//     the total zero would put a line on the invoice whose stated fraction is not
//     the amount charged, which is the one thing the two-line rule exists to
//     prevent. So a downgrade mid-period keeps the plan it was paid for until the
//     boundary, and the unused remainder is forfeited. **When credit notes exist,
//     this is the branch that grants one.**
//
// The items are swapped before the invoice is generated. If generation then
// fails the caller gets the error with the change already applied — the same
// shape Subscribe has, and the right way round: a customer on the new plan with
// a missing bill is an operator's afternoon, a customer billed for a plan they
// were never moved onto is a refund.
func (s *Subscriber) ChangePlan(
	ctx context.Context,
	sub *billing.Subscription,
	in ChangeInput,
	now time.Time,
) (*billing.Invoice, error) {
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("%w: a subscription needs at least one item", billing.ErrInvalidSubscriptionItem)
	}

	oldItems, err := s.subs.ListItems(ctx, sub.OrganizationID, sub.Livemode, sub.ID)
	if err != nil {
		return nil, err
	}

	// The same three rules that gate a new subscription — one recurrence, one
	// timing, one owner — applied to the new set by the same function rather than
	// a second copy of the checks.
	prices, ownerKey, err := s.resolveItemPrices(ctx, SubscribeInput{
		OrganizationID: sub.OrganizationID,
		Livemode:       sub.Livemode,
		Items:          in.Items,
	})
	if err != nil {
		return nil, err
	}
	cycle := prices[0]
	if cycle.Recurrence != sub.Recurrence {
		return nil, fmt.Errorf(
			"%w: this subscription bills every %d %s and the new prices bill every %d %s; changing the cycle means a new subscription",
			billing.ErrInvalidSubscriptionItem,
			sub.Recurrence.Count, sub.Recurrence.Interval, cycle.Recurrence.Count, cycle.Recurrence.Interval)
	}

	oldSide, err := s.swapSide(ctx, sub.OrganizationID, sub.Livemode, oldItems, sub.Timing)
	if err != nil {
		return nil, err
	}
	newSide, err := s.sideOf(ctx, sub.OrganizationID, sub.Livemode, prices, in.Items, cycle.Timing)
	if err != nil {
		return nil, err
	}

	items := make([]billing.SubscriptionItem, len(in.Items))
	for i, want := range in.Items {
		items[i] = billing.SubscriptionItem{
			ID:             id.NewWithPrefix(id.PrefixSubscriptionItm),
			OrganizationID: sub.OrganizationID,
			Livemode:       sub.Livemode,
			SubscriptionID: sub.ID,
			PriceID:        prices[i].ID,
			Quantity:       want.quantity(),
		}
	}
	if err := s.subs.ChangeItems(
		ctx, sub, oldItems, items, cycle.Timing, ownerKey, in.Cause, in.Actor, in.RequestID, now,
	); err != nil {
		return nil, err
	}

	return s.invoiceChange(ctx, sub, oldSide, newSide, in.Actor, now)
}

// swapSide totals the fixed prices a subscription is currently on.
//
// A side billed in arrears contributes nothing: nothing was paid in advance, so
// there is no unearned remainder to credit back.
func (s *Subscriber) swapSide(
	ctx context.Context,
	organizationID string,
	livemode bool,
	items []billing.SubscriptionItem,
	timing billing.BillingTiming,
) (billing.SwapSide, error) {
	prices := make([]*billing.Price, len(items))
	quantities := make([]SubscribeItem, len(items))
	for i, it := range items {
		price, err := s.catalog.GetPrice(ctx, organizationID, livemode, it.PriceID)
		if err != nil {
			return billing.SwapSide{}, err
		}
		prices[i] = price
		quantities[i] = SubscribeItem{PriceID: it.PriceID, Quantity: it.Quantity}
	}
	return s.sideOf(ctx, organizationID, livemode, prices, quantities, timing)
}

// sideOf builds one half of the swap: the full-period cost of the fixed items,
// and a name for the line.
func (s *Subscriber) sideOf(
	ctx context.Context,
	organizationID string,
	livemode bool,
	prices []*billing.Price,
	items []SubscribeItem,
	timing billing.BillingTiming,
) (billing.SwapSide, error) {
	side := billing.SwapSide{}
	fixed := 0
	for i, price := range prices {
		product, err := s.catalog.GetProduct(ctx, organizationID, livemode, price.ProductID)
		if err != nil {
			return billing.SwapSide{}, err
		}
		// The name comes from the first product either way, so a metered-only plan
		// still has something to call itself on the credit line.
		if side.Name == "" {
			side.Name = product.Name
		}
		if price.Type != billing.PriceFixed || timing != billing.BillAdvance {
			continue
		}
		fixed++
		side.Total += price.UnitAmount * billing.Cents(items[i].quantity())
		side.Name = product.Name
		side.PriceID = price.ID
	}
	// PriceID is only meaningful when the side is a single price; an aggregate
	// line pointing at one of several would be a worse answer than none.
	if fixed != 1 {
		side.PriceID = ""
	}
	return side, nil
}

// invoiceChange issues the proration document for a swap, or nothing when the
// swap costs the customer nothing.
func (s *Subscriber) invoiceChange(
	ctx context.Context,
	sub *billing.Subscription,
	oldSide, newSide billing.SwapSide,
	actor string,
	now time.Time,
) (*billing.Invoice, error) {
	period := sub.CurrentPeriod()
	at := brcal.FromTime(now)
	swap := billing.ProrateSwap(oldSide.Total, newSide.Total, period, at)
	if swap.Net() <= 0 {
		return nil, nil
	}

	credit, charge := billing.SwapSideLines(oldSide, newSide, period, at)
	lines := make([]billing.InvoiceItem, 0, 2)
	// A zero credit is the free-plan side of an upgrade. Printing "Crédito
	// proporcional — DF-e Free: R$ 0,00" explains nothing and invites the question
	// it looks like it is answering.
	if credit.Amount != 0 {
		lines = append(lines, credit)
	}
	lines = append(lines, charge)

	inv := &billing.Invoice{
		ID:             id.NewWithPrefix(id.PrefixInvoice),
		OrganizationID: sub.OrganizationID,
		Livemode:       sub.Livemode,
		CustomerID:     sub.CustomerID,
		SubscriptionID: sub.ID,
		OwnerKey:       sub.OwnerKey,
		Status:         billing.InvoiceDraft,
		Period:         billing.Period{Start: at, End: period.End},
		Currency:       billing.CurrencyBRL,
		Metadata:       sub.Metadata.Clone(),
	}
	if err := billing.ApplyToInvoice(inv, lines, 0); err != nil {
		return nil, err
	}

	// **No generation key.** That key claims a subscription's period, and this
	// invoice does not bill a period — the period's own invoice already exists or
	// is still coming. Two plan changes in one month are two real documents, and a
	// key would let the second one silently vanish. Retries are the HTTP
	// Idempotency-Key's job, which every route reaching here is behind.
	if err := s.invoicer.invoices.Create(ctx, inv, lines, "", now); err != nil {
		return nil, err
	}
	// Due immediately: the customer is already on the new plan. Dunning is armed
	// the same way the sweep arms it, because an unpaid upgrade is an unpaid bill.
	dueDate := billing.DueDate(inv.Period, billing.BillAdvance, sub.NetDays)
	if _, err := s.invoicer.invoices.Finalize(
		ctx, inv, dueDate, inv.Schedule().FirstDunningDate(dueDate), billing.CauseScheduler, actor, "", now,
	); err != nil {
		return nil, err
	}
	return inv, nil
}

// firstPeriodCost is what the first advance period will cost, computed from the
// prices alone.
//
// It has to be answered before the invoice exists, because the invoice is
// written against a subscription whose status is already decided. That makes it
// the same arithmetic as billing.FixedLine — the one GenerateForPeriod runs a
// moment later — in a second place, which is normally the thing to avoid.
//
// What keeps the two honest is a test rather than a comment:
// TestSubscribingToAPaidPlanStartsIncomplete asserts the status Subscribe chose
// against the invoice Subscribe produced, so they cannot silently disagree. A
// discount, a trial or a mid-period proration would land in the invoice and not
// here — that test is what fails first, and it is the thing to fix rather than
// delete when one of those arrives.
//
// Metered prices contribute nothing, and that is correct rather than a rounding
// of the problem: a metered price cannot be billed in advance at all
// (Price.Validate), so one cannot reach this path — and usage for a period that
// has not started is genuinely zero.
func firstPeriodCost(prices []*billing.Price, items []SubscribeItem) billing.Cents {
	total := billing.Cents(0)
	for i, price := range prices {
		if price.Type != billing.PriceFixed {
			continue
		}
		total += price.UnitAmount * billing.Cents(items[i].quantity())
	}
	return total
}

// resolveItemPrices reads the requested prices and rejects a set that cannot be
// one subscription. It returns them in request order, plus the owner key they
// agree on.
//
// The three rules are not style. A subscription carries exactly one Recurrence
// and one BillingTiming, so items that disagree on either would be billed for a
// period the subscription does not have — an advance item and an arrears one on
// the same document cover two different windows, and nothing downstream could
// tell which. One OwnerKey routes the events (ADR 0016), so items owned by
// different services would send each service the other's invoices, which is the
// exact failure that key exists to prevent.
func (s *Subscriber) resolveItemPrices(ctx context.Context, in SubscribeInput) ([]*billing.Price, string, error) {
	prices := make([]*billing.Price, len(in.Items))
	seen := make(map[string]bool, len(in.Items))
	ownerKey := ""

	for i, want := range in.Items {
		if seen[want.PriceID] {
			// Two items on the same price are two lines charging the same thing.
			// Whoever means "twice as much" means quantity.
			return nil, "", fmt.Errorf("%w: price %s appears twice; use quantity instead",
				billing.ErrInvalidSubscriptionItem, want.PriceID)
		}
		seen[want.PriceID] = true

		price, err := s.catalog.GetPrice(ctx, in.OrganizationID, in.Livemode, want.PriceID)
		if err != nil {
			return nil, "", err
		}
		if price.Archived {
			return nil, "", fmt.Errorf("%w: price %s is archived", billing.ErrInvalidPrice, price.ID)
		}
		if err := price.Validate(); err != nil {
			return nil, "", err
		}
		if i > 0 {
			if price.Recurrence != prices[0].Recurrence {
				return nil, "", fmt.Errorf("%w: price %s bills every %d %s but %s bills every %d %s; one subscription has one cycle",
					billing.ErrInvalidSubscriptionItem,
					price.ID, price.Recurrence.Count, price.Recurrence.Interval,
					prices[0].ID, prices[0].Recurrence.Count, prices[0].Recurrence.Interval)
			}
			if price.Timing != prices[0].Timing {
				return nil, "", fmt.Errorf("%w: price %s is billed %s but %s is billed %s; one subscription has one timing",
					billing.ErrInvalidSubscriptionItem, price.ID, price.Timing, prices[0].ID, prices[0].Timing)
			}
		}
		prices[i] = price

		// One extra read per item, once per subscription, to learn which service
		// this belongs to. It is done here rather than at each event because the
		// answer is fixed for the life of the subscription — and doing it here
		// means an event emitted inside a status-change transaction needs no
		// lookups at all.
		product, err := s.catalog.GetProduct(ctx, in.OrganizationID, in.Livemode, price.ProductID)
		if err != nil {
			return nil, "", fmt.Errorf("resolving the product behind price %s: %w", price.ID, err)
		}
		if i > 0 && product.OwnerKey != ownerKey {
			return nil, "", fmt.Errorf("%w: price %s belongs to owner %q but the first item belongs to %q; one subscription has one owner",
				billing.ErrInvalidSubscriptionItem, price.ID, product.OwnerKey, ownerKey)
		}
		ownerKey = product.OwnerKey
	}
	return prices, ownerKey, nil
}

// Cancel ends a subscription, immediately or at the end of the current period.
//
// These are **two distinct operations**, never a checkbox on one: cancelling now
// revokes service and may owe a proration credit; cancelling at period end
// changes nothing today. Collapsing them into one call with a flag is how a
// customer loses access on the day they meant to keep it until.
func (s *Subscriber) Cancel(ctx context.Context, sub *billing.Subscription, atPeriodEnd bool, cause billing.Cause, actor, requestID string, now time.Time) error {
	// "At the end of the period" means "keep what you already paid for until it
	// runs out", and a subscription that never activated has no such thing: no
	// payment landed and no service was granted, so there is nothing for the
	// deferral to protect. Honouring the flag would leave a row alive for a month
	// guarding an entitlement that does not exist — and the domain would refuse
	// it anyway, since INCOMPLETE has no self-edge to schedule against.
	if atPeriodEnd && sub.Status != billing.SubscriptionIncomplete {
		return s.subs.ScheduleCancellation(ctx, sub, cause, actor, requestID, now)
	}
	_, err := s.subs.Transition(ctx, sub, billing.SubscriptionCanceled, cause, actor, requestID, now)
	return err
}
