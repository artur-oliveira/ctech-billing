package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// WakeCause is what woke a confirmation (spec § 7.4: three wake-ups, one
// decision). It names the audit actor; it never decides anything.
type WakeCause string

const (
	WakeWebhook   WakeCause = "webhook"
	WakeSweep     WakeCause = "sweep"
	WakeStatement WakeCause = "statement"
	WakeManual    WakeCause = "manual"
)

// Outcome is what a confirmation found.
type Outcome string

const (
	OutcomeSettled        Outcome = "settled"
	OutcomeAlreadySettled Outcome = "already_settled"
	OutcomeOpen           Outcome = "open"
	OutcomeExpired        Outcome = "expired"
	OutcomeIgnored        Outcome = "ignored"
)

func actorByok(cause WakeCause) string { return "service:byok-" + string(cause) }

// Confirm re-queries the provider for a charge reference and acts on its
// answer only (spec § 7.4). Called by the notify route, the sweep and the
// statement sync; settles once whatever the number of wake-ups.
func (c *ByokCollector) Confirm(ctx context.Context, ref repositories.ChargeRef, cause WakeCause, now time.Time) (Outcome, error) {
	a, err := c.payments.GetAttempt(ctx, ref.TenantID, ref.Livemode, ref.InvoiceID, ref.AttemptNumber)
	if err != nil {
		return "", err
	}
	if a.Method != billing.MethodBYOK || a.ChargeRef != ref.Ref {
		return OutcomeIgnored, nil
	}
	org, err := c.orgs.Get(ctx, ref.TenantID, ref.Livemode)
	if err != nil {
		return "", err
	}
	return c.confirmAttempt(ctx, org, a, cause, now)
}

func (c *ByokCollector) confirmAttempt(ctx context.Context, org *billing.Organization, a *billing.PaymentAttempt, cause WakeCause, now time.Time) (Outcome, error) {
	conn, err := c.connection(ctx, org)
	if err != nil {
		return "", err
	}
	sess := c.sessions(conn)
	if err := pix(ctx, sess); err != nil {
		return "", err
	}
	ch, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (byok.Charge, error) {
		return sess.P.GetCharge(ctx, cr, t, a.ChargeRef)
	})
	if err != nil {
		return "", err // never inferred from a failure (spec § 14)
	}
	if a.Status == billing.AttemptPending {
		_ = c.payments.TouchChecked(ctx, a, now)
	}
	actor := actorByok(cause)
	if len(ch.Payments) == 0 {
		switch ch.Status {
		case byok.ChargeExpired, byok.ChargeRemoved:
			if a.Status == billing.AttemptPending {
				a.FailureReason = string(ch.Status)
				if _, err := c.payments.TransitionAttempt(ctx, a, billing.AttemptFailed, billing.CausePaymentFailed, actor, "", now); err != nil &&
					!errors.Is(err, repositories.ErrConcurrentModification) {
					return "", err
				}
			}
			return OutcomeExpired, nil
		}
		return OutcomeOpen, nil // still payable: nothing to do
	}
	first := ch.Payments[0]
	if first.Amount < int64(a.Amount) {
		slog.ErrorContext(ctx, "byok charge paid below the amount due", "attempt_id", a.ID, "expected", int64(a.Amount), "paid", first.Amount)
		return "", fmt.Errorf("%w: attempt %s paid %d, expected %d", ErrAmountMismatch, a.ID, first.Amount, int64(a.Amount))
	}
	outcome := OutcomeAlreadySettled
	switch a.Status {
	case billing.AttemptPending:
		a.PaymentID, a.LateCharges = first.ID, billing.Cents(first.Amount-int64(a.Amount))
		won, err := c.settler.SettleAttempt(ctx, a, billing.CauseByokConfirmed, actor, "", now)
		if err != nil {
			return "", err
		}
		if won {
			outcome = OutcomeSettled
		}
	case billing.AttemptSucceeded:
		// Finishes a half-done settlement (invoice, subscription, Finanças).
		if _, err := c.settler.SettleAttempt(ctx, a, billing.CauseByokConfirmed, actor, "", now); err != nil {
			return "", err
		}
	default:
		// FAILED (voided, disconnected) and then paid: the money is in the
		// merchant's account. Recorded as excess so it is never lost.
		slog.WarnContext(ctx, "byok charge paid after its attempt ended", "attempt_id", a.ID, "status", a.Status)
		for _, p := range ch.Payments {
			c.excess(ctx, conn, a, p.ID, p.ID, billing.Cents(p.Amount), p.PaidAt, now)
		}
		return OutcomeIgnored, nil
	}
	// Excess (D19): anything above amount + the provider's computed late
	// charges, and every further payment on the same charge.
	if allowed := int64(a.Amount) + first.LateCharges; first.Amount > allowed {
		c.excess(ctx, conn, a, first.ID+"X", first.ID, billing.Cents(first.Amount-allowed), first.PaidAt, now)
	}
	for _, p := range ch.Payments[1:] {
		c.excess(ctx, conn, a, p.ID, p.ID, billing.Cents(p.Amount), p.PaidAt, now)
	}
	return outcome, nil
}

// excess records one excess payment (create-only by payment id) and tells
// the merchant once. Never refunded here (D19).
func (c *ByokCollector) excess(ctx context.Context, conn *byokconn.Connection, a *billing.PaymentAttempt, paymentID, sourcePaymentID string, amount billing.Cents, paidAt, now time.Time) {
	if amount <= 0 {
		return
	}
	e := &billing.ExcessPayment{ID: "ex_" + id.New(), OrganizationID: a.OrganizationID, Livemode: a.Livemode, InvoiceID: a.InvoiceID,
		AttemptID: a.ID, AttemptNumber: a.AttemptNumber, Provider: a.Provider, PaymentID: paymentID, Amount: amount,
		SourcePaymentID: sourcePaymentID, Kind: billing.KindExcess,
		Status: billing.ExcessOpen, PaidAt: paidAt.UTC()}
	if e.PaidAt.IsZero() {
		e.PaidAt = now.UTC()
	}
	created, err := c.payments.RecordExcess(ctx, e, now)
	if err != nil {
		slog.ErrorContext(ctx, "byok excess payment not recorded", "attempt_id", a.ID, "error", err)
		return
	}
	if !created {
		return
	}
	slog.WarnContext(ctx, "byok excess payment", "attempt_id", a.ID, "amount", int64(amount))
	inv, _ := c.invoices.Get(ctx, a.OrganizationID, a.Livemode, a.InvoiceID)
	c.excessNotice(ctx, conn, inv, amount)
}
