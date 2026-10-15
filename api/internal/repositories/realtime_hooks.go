package repositories

import (
	"context"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// ChangeEmitter builds the Change that tells the live screens an entity moved,
// resolving who may hear it. It is an interface here because the implementation
// (internal/realtime) imports this package; a repository cannot import it back.
//
// Every method returns nil when nobody should be told, and never fails: a
// change that cannot be built is a screen that refreshes a little later, not a
// mutation that does not happen.
type ChangeEmitter interface {
	ForInvoice(ctx context.Context, inv *billing.Invoice, typ billing.EventType) *Change
	ForAttempt(ctx context.Context, a *billing.PaymentAttempt, typ billing.EventType) *Change
	ForSubscription(ctx context.Context, sub *billing.Subscription, typ billing.EventType) *Change
	ForCheckout(ctx context.Context, cs *billing.CheckoutSession, typ billing.EventType) *Change
	ForCreditNote(ctx context.Context, cn *billing.CreditNote, inv *billing.Invoice) *Change

	// The rest are not transitions of an entity the emitter can read: a payer
	// recorded, a payout settled, a connection saved, a bank line posted, a bill
	// settled. The caller says what happened and to whom; the emitter owns the
	// scope keys, so their format lives in one place.
	ForInvoiceRef(org string, live bool, invoiceID, typ, status string) *Change
	ForTenant(org string, live bool, entity, entityID, typ, status string) *Change
	ForSpace(spaceKey, entity, entityID, typ, status string) *Change
}

// ChangePublisher sends committed changes to the connections that are listening.
// It runs after the commit and never returns an error: a publish that is lost is
// found again by the reconnecting client's replay from the log.
type ChangePublisher interface {
	Publish(ctx context.Context, changes ...*Change)
}

// Realtime is what a repository holds to tell the live screens. The zero value
// is off: the emitter is nil, so no change is built, none is written to the log
// and nothing is published (REALTIME_ENABLED=false behaves as before).
type Realtime struct {
	Emitter   ChangeEmitter
	Publisher ChangePublisher
}

func (rt Realtime) invoice(ctx context.Context, inv *billing.Invoice, typ billing.EventType) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForInvoice(ctx, inv, typ)
}

func (rt Realtime) attempt(ctx context.Context, a *billing.PaymentAttempt, typ billing.EventType) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForAttempt(ctx, a, typ)
}

func (rt Realtime) subscription(ctx context.Context, sub *billing.Subscription, typ billing.EventType) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForSubscription(ctx, sub, typ)
}

func (rt Realtime) checkout(ctx context.Context, cs *billing.CheckoutSession, typ billing.EventType) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForCheckout(ctx, cs, typ)
}

func (rt Realtime) creditNote(ctx context.Context, cn *billing.CreditNote, inv *billing.Invoice) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForCreditNote(ctx, cn, inv)
}

func (rt Realtime) invoiceRef(org string, live bool, invoiceID, typ, status string) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForInvoiceRef(org, live, invoiceID, typ, status)
}

func (rt Realtime) tenant(org string, live bool, entity, entityID, typ, status string) *Change {
	if rt.Emitter == nil {
		return nil
	}
	return rt.Emitter.ForTenant(org, live, entity, entityID, typ, status)
}

func (rt Realtime) space(spaceKey, entity, entityID, typ, status string) *Change {
	if rt.Emitter == nil || spaceKey == "" {
		return nil
	}
	return rt.Emitter.ForSpace(spaceKey, entity, entityID, typ, status)
}

// announce is for a mutation that has no transaction of its own (a plain update
// followed by a separate audit write): the change is written to the log and then
// published. A failed log write is logged and swallowed. It is the weaker of the
// two, and it is used only where there was no transaction to join; the screens'
// own refetch on focus covers the rare lost one.
func (rt Realtime) announce(ctx context.Context, events Base, c *Change, now time.Time) {
	if c == nil {
		return
	}
	writes, err := changeWrites(events, c, now)
	if err == nil && len(writes) > 0 {
		err = events.TransactWrite(ctx, writes)
	}
	if err != nil {
		slog.WarnContext(ctx, "realtime: change not logged", "type", c.Type, "entity_id", c.EntityID, "error", err)
		return
	}
	rt.publish(ctx, c)
}

// publish is called after the transaction that wrote the changes returned nil.
func (rt Realtime) publish(ctx context.Context, changes ...*Change) {
	if rt.Publisher == nil {
		return
	}
	live := make([]*Change, 0, len(changes))
	for _, c := range changes {
		if c != nil && c.ID != "" {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return
	}
	defer func() {
		// A publisher that panics must not turn a committed mutation into a 500.
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "realtime publish panicked", "panic", r)
		}
	}()
	rt.Publisher.Publish(ctx, live...)
}
