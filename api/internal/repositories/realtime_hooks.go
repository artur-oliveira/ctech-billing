package repositories

import (
	"context"
	"log/slog"

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
