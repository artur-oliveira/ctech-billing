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
// **Known interim behaviour, to change with the payment leg (Phase 3):** the
// subscription is created ACTIVE, so service is granted before the first invoice
// is paid. The designed behaviour is INCOMPLETE until that payment lands
// (assessment § 6.2) — but INCOMPLETE has no way out until something can
// actually collect money, and shipping a state nothing can leave would be worse
// than shipping this. It is acceptable only while the sole tenant is CTech's own
// products; it must not survive the first external merchant.
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

	sub := &billing.Subscription{
		ID:             id.NewWithPrefix(id.PrefixSubscription),
		OrganizationID: in.OrganizationID,
		Livemode:       in.Livemode,
		CustomerID:     in.CustomerID,
		Status:         billing.SubscriptionActive,
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
		quantity := want.Quantity
		if quantity < 1 {
			quantity = 1
		}
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
	if atPeriodEnd {
		return s.subs.ScheduleCancellation(ctx, sub, cause, actor, requestID, now)
	}
	_, err := s.subs.Transition(ctx, sub, billing.SubscriptionCanceled, cause, actor, requestID, now)
	return err
}
