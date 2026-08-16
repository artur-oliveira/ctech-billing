package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// actorReconciler names the job in the audit trail. It is not actorWallet, and
// the difference is the whole value of the field: "the rail told us" and "we
// went and asked because the rail never told us" are the two facts an incident
// review needs to separate.
const actorReconciler = "reconciler"

// abandonAfter is how long an attempt may stay unanswered before the job stops
// asking and raises an alarm.
//
// It is long on purpose. Every shorter deadline is a bet that wallet is not
// merely slow, and losing that bet marks a real payment ABANDONED — which reads
// as an integration fault and sends somebody hunting a bug that does not exist.
// A charge nobody paid costs nothing by staying PENDING for a day; a charge
// somebody paid costs a customer their money.
const abandonAfter = 24 * time.Hour

// ReconcileResult is one run's tally.
type ReconcileResult struct {
	Examined  int
	Settled   int // the webhook never arrived and this run collected the money
	Failed    int // the charge expired unpaid
	Abandoned int // wallet does not know the charge — an integration fault
	Waiting   int // still live, correctly nothing to do
	Errors    []error
}

// Reconcile answers the charges whose webhook never arrived.
//
// The webhook is the fast path and this is the correct one. A notify-back can be
// lost by a deploy, a network partition, a 502 from a healthy-looking instance,
// or a signature rotated mid-flight; none of those are visible from here, and
// all of them end the same way — a customer who paid looking at an invoice that
// says they did not. So the job never asks "did the webhook arrive?" It asks
// wallet what happened, which is the only question with an authoritative answer.
//
// Cross-tenant by design (ADR 0002), and therefore reachable only from
// cmd/reconcile — never from a route.
//
// Every branch is idempotent and safe to re-run: it settles through Confirm,
// which re-reads the charge and tolerates an attempt that is already SUCCEEDED.
func (c *Collector) Reconcile(ctx context.Context, livemode bool, opened brcal.Date, now time.Time) ReconcileResult {
	var result ReconcileResult
	var startKey map[string]types.AttributeValue

	for {
		page, err := c.payments.PendingOn(ctx, livemode, opened, 100, startKey)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read pending attempts for %s: %w", opened, err))
			return result
		}
		for i := range page.Items {
			attempt := page.Items[i]
			result.Examined++
			if err := c.reconcileOne(ctx, &attempt, opened, now, &result); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("attempt %s (charge %s): %w", attempt.ID, attempt.WalletChargeID, err))
			}
		}
		if page.LastEvaluatedKey == nil {
			return result
		}
		startKey = page.LastEvaluatedKey
	}
}

// reconcileOne decides one attempt against what wallet says about its charge.
//
// The order matters. Paid is checked before expired, because a charge can be
// both — the customer paid in the last minute of the window — and reading them
// the other way round fails an attempt that actually collected.
func (c *Collector) reconcileOne(
	ctx context.Context,
	attempt *billing.PaymentAttempt,
	opened brcal.Date,
	now time.Time,
	result *ReconcileResult,
) error {
	charge, err := c.charges.GetCharge(ctx, attempt.WalletChargeID)
	switch {
	case errors.Is(err, wallet.ErrChargeNotFound):
		// Billing holds a charge id wallet cannot account for. Nobody declined to
		// pay; something in the integration wrote an id that does not exist, or
		// wallet lost a row. Either way it is not a billing decision to make
		// quietly, which is what ABANDONED is for.
		slog.ErrorContext(ctx, "wallet does not know a charge billing opened",
			"charge_id", attempt.WalletChargeID, "invoice_id", attempt.InvoiceID,
			"organization_id", attempt.OrganizationID)
		if err := c.terminate(ctx, attempt, billing.AttemptAbandoned, "wallet does not know this charge", now); err != nil {
			return err
		}
		result.Abandoned++
		return nil
	case err != nil:
		// An outage. Leave the attempt exactly where it is: it stays in the sweep
		// partition, and the next run asks again. Terminating an attempt because
		// wallet was unreachable would invent a payment outcome out of a network
		// error.
		return err
	}

	if charge.Paid() {
		// The reason this job exists. Confirm re-reads the charge itself and does
		// the whole settlement, so a webhook that arrives late finds the work done
		// rather than done twice.
		if err := c.Confirm(ctx, attempt.Livemode, attempt.WalletChargeID, billing.CauseReconciliation, "", now); err != nil {
			return err
		}
		slog.InfoContext(ctx, "reconciliation settled an invoice the webhook never reported",
			"charge_id", attempt.WalletChargeID, "invoice_id", attempt.InvoiceID)
		result.Settled++
		return nil
	}

	if !chargeExpiry(charge, opened.Time().Add(abandonAfter)).Before(now) {
		// Still inside its window, or wallet gave no expiry and the attempt is not
		// yet old enough to give up on. Nothing has gone wrong.
		result.Waiting++
		return nil
	}

	// The window closed and wallet never saw the money. This is the ordinary
	// outcome of a customer who opened a checkout and walked away, and it is a
	// failed attempt, not an abandoned one — the invoice stays OPEN and payable,
	// and the next attempt opens a fresh charge.
	if err := c.terminate(ctx, attempt, billing.AttemptFailed, "charge expired without payment", now); err != nil {
		return err
	}
	result.Failed++
	return nil
}

// terminate moves an attempt to a terminal status with the reconciliation cause.
//
// It writes the reason onto the row rather than only into a log line: the
// invoice detail screen shows attempts, and "FAILED" with no reason is the
// answer that starts a support conversation instead of ending one.
func (c *Collector) terminate(
	ctx context.Context,
	attempt *billing.PaymentAttempt,
	to billing.PaymentAttemptStatus,
	reason string,
	now time.Time,
) error {
	attempt.FailureReason = reason
	_, err := c.payments.TransitionAttempt(
		ctx, attempt, to, billing.CauseReconciliation, actorReconciler, "", now,
	)
	return err
}
