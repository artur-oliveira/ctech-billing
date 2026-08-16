//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Reconciliation, against the same fake wallet the checkout tests use.
//
// These exist because the webhook is a delivery and deliveries fail. Every test
// here is the same shape: wallet knows the truth, billing never heard it, and
// the job is what closes the gap. The one thing none of them do is tell billing
// the answer — the fake is always asked.
//
// Assertions are per-invoice rather than on the run's totals, and that is not
// squeamishness about shared state: the sweep is cross-tenant by design (ADR
// 0002), so a run in one test legitimately sees the attempts every other test
// left behind. A total is not this test's to own; the invoice in front of it is.

// sweepDay is the civil date the fake clock writes attempts under, so a test
// reconciles the same partition the checkout wrote into.
func sweepDay() brcal.Date { return brcal.FromTime(now()) }

// The reason this job exists: the customer paid, wallet knows, and the
// notify-back never arrived.
func TestReconciliationSettlesAPaymentWhoseWebhookNeverArrived(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	charge := e.openCharge(t, inv)

	// Paid at the rail. No webhook is posted — that is the failure being modelled.
	e.wallet.settle(charge, int64(inv.Total))

	if res := e.collector.Reconcile(ctx, true, sweepDay(), now().Add(time.Hour)); len(res.Errors) > 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
	if got := e.attemptFor(t, inv.ID).Status; got != billing.AttemptSucceeded {
		t.Fatalf("attempt is %s, want SUCCEEDED", got)
	}

	// The trail must say the job did it, not the rail. During an incident, "the
	// webhook settled this" and "we found it hours later by polling" are the two
	// facts worth telling apart — the second one is the bug report.
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, inv.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range trail {
		if entry.After == string(billing.InvoicePaid) {
			if entry.Actor != "reconciler" || entry.Cause != billing.CauseReconciliation {
				t.Fatalf("payment attributed to %q/%q, want reconciler/reconciliation", entry.Actor, entry.Cause)
			}
			return
		}
	}
	t.Fatalf("no audit entry for the payment: %+v", trail)
}

// Re-running must be free. The job runs hourly and will meet the same charges
// over and over until their rows age out of the sweep window.
func TestReconciliationIsSafeToRunTwice(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	charge := e.openCharge(t, inv)
	e.wallet.settle(charge, int64(inv.Total))

	e.collector.Reconcile(ctx, true, sweepDay(), now().Add(time.Hour))
	if res := e.collector.Reconcile(ctx, true, sweepDay(), now().Add(2*time.Hour)); len(res.Errors) > 0 {
		t.Fatalf("second run errored: %v", res.Errors)
	}

	// The invoice is paid once and stays paid — a second settlement of an invoice
	// already PAID must be a no-op, not a failed transition.
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
	// And a decided attempt leaves the sweep, which is what keeps an hourly job
	// from re-asking wallet about every charge it has ever settled.
	if e.stillInSweep(t, inv.ID) {
		t.Fatal("a settled attempt is still in the reconciliation sweep")
	}
}

// A charge still inside its window is not a problem. The customer is probably
// in their bank app right now, and a job that failed the attempt here would kill
// a live QR code.
func TestReconciliationLeavesALiveChargeAlone(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	e.openCharge(t, inv)

	e.collector.Reconcile(ctx, true, sweepDay(), now().Add(time.Minute))

	if got := e.attemptFor(t, inv.ID).Status; got != billing.AttemptPending {
		t.Fatalf("attempt is %s, want PENDING", got)
	}
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoiceOpen {
		t.Fatalf("invoice is %s, want OPEN", got)
	}
}

// The ordinary ending: somebody opened a checkout and walked away. That is a
// FAILED attempt and an invoice that is still owed — never an ABANDONED one,
// which would report a customer's choice as an integration fault.
func TestReconciliationFailsAChargeThatExpiredUnpaid(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	e.openCharge(t, inv)

	// Past the 30 minutes the fake stamps on the charge, and wallet still reports
	// it pending.
	e.collector.Reconcile(ctx, true, sweepDay(), now().Add(2*time.Hour))

	attempt := e.attemptFor(t, inv.ID)
	if attempt.Status != billing.AttemptFailed {
		t.Fatalf("attempt is %s, want FAILED", attempt.Status)
	}
	if attempt.FailureReason == "" {
		t.Fatal("a failed attempt with no reason is what starts a support conversation")
	}
	// Still owed. Failing to collect is not forgiving the debt.
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoiceOpen {
		t.Fatalf("invoice is %s, want OPEN", got)
	}
}

// A charge wallet cannot account for is the one case that is billing's bug, not
// the customer's decision. It gets its own status precisely so it can be alarmed
// on separately.
func TestReconciliationAbandonsAChargeWalletDoesNotKnow(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	charge := e.openCharge(t, inv)
	e.wallet.forget(charge)

	// Inside the charge's window, so an expiry cannot be what decides this.
	e.collector.Reconcile(ctx, true, sweepDay(), now().Add(time.Minute))

	if got := e.attemptFor(t, inv.ID).Status; got != billing.AttemptAbandoned {
		t.Fatalf("attempt is %s, want ABANDONED", got)
	}
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoiceOpen {
		t.Fatalf("invoice is %s, want OPEN", got)
	}
}

// A webhook that did arrive must take the attempt out of the sweep, or the
// hourly job spends its life re-reading charges that are already settled.
func TestASettledWebhookRemovesTheAttemptFromTheSweep(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	charge := e.openCharge(t, inv)

	if !e.stillInSweep(t, inv.ID) {
		t.Fatal("a pending attempt is not in the reconciliation sweep")
	}

	e.wallet.settle(charge, int64(inv.Total))
	if res := e.notify(t, charge); res.status != 200 {
		t.Fatalf("webhook: %d", res.status)
	}

	if e.stillInSweep(t, inv.ID) {
		t.Fatal("a settled attempt is still in the sweep")
	}
}

// The sweep is cross-tenant, so it must not also be cross-*mode*. A test-mode
// run reading live rows would let sandbox data settle real invoices.
func TestReconciliationDoesNotCrossModes(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	charge := e.openCharge(t, inv)
	e.wallet.settle(charge, int64(inv.Total))

	e.collector.Reconcile(ctx, false, sweepDay(), now().Add(time.Hour))

	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoiceOpen {
		t.Fatalf("a test-mode run moved a live invoice to %s", got)
	}
	if got := e.attemptFor(t, inv.ID).Status; got != billing.AttemptPending {
		t.Fatalf("a test-mode run moved a live attempt to %s", got)
	}
}

// stillInSweep reports whether an invoice's attempt is in today's reconciliation
// partition.
func (e *payEnv) stillInSweep(t *testing.T, invoiceID string) bool {
	t.Helper()
	page, err := repositories.NewPaymentRepository(testDB, testCfg).
		PendingOn(ctxT(t), true, sweepDay(), 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range page.Items {
		if a.InvoiceID == invoiceID {
			return true
		}
	}
	return false
}
