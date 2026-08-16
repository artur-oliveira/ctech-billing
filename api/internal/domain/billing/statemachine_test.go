package billing

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func mustDate(s string) brcal.Date {
	d, err := brcal.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestInvoiceHappyPath(t *testing.T) {
	inv := &Invoice{Status: InvoiceDraft}

	events, err := inv.Transition(InvoiceOpen, CauseScheduler)
	if err != nil || len(events) != 1 || events[0] != EventInvoiceFinalized {
		t.Fatalf("finalize: events %v, err %v", events, err)
	}
	events, err = inv.Transition(InvoicePaid, CauseWalletWebhook)
	if err != nil || events[0] != EventInvoicePaid {
		t.Fatalf("pay: events %v, err %v", events, err)
	}
	if inv.Status != InvoicePaid {
		t.Fatalf("status = %s", inv.Status)
	}
}

func TestInvoicePaidAndVoidAreTerminal(t *testing.T) {
	for _, terminal := range []InvoiceStatus{InvoicePaid, InvoiceVoid} {
		for _, to := range []InvoiceStatus{InvoiceDraft, InvoiceOpen, InvoicePaid, InvoiceVoid, InvoiceUncollectible} {
			inv := &Invoice{Status: terminal}
			if _, err := inv.Transition(to, CauseManual); !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s -> %s must be rejected, got %v", terminal, to, err)
			}
			if inv.Status != terminal {
				t.Errorf("a rejected transition must not mutate the invoice (%s became %s)", terminal, inv.Status)
			}
		}
	}
}

func TestInvoiceUncollectibleToPaidRequiresARealPayment(t *testing.T) {
	// A customer paying a long-overdue charge is real and must work. An operator
	// clicking a status with no money behind it must not.
	for _, cause := range []Cause{CauseWalletWebhook, CauseReconciliation, CauseManualPayment} {
		inv := &Invoice{Status: InvoiceUncollectible}
		if _, err := inv.Transition(InvoicePaid, cause); err != nil {
			t.Errorf("cause %q must be allowed: %v", cause, err)
		}
	}
	inv := &Invoice{Status: InvoiceUncollectible}
	if _, err := inv.Transition(InvoicePaid, CauseManual); !errors.Is(err, ErrCauseNotAllowed) {
		t.Fatalf("a bare operator action must not resurrect a write-off, got %v", err)
	}
	if inv.Status != InvoiceUncollectible {
		t.Fatal("the rejected transition mutated the invoice")
	}
}

func TestInvoicePaymentFailureAdvancesTheDunningCounter(t *testing.T) {
	inv := &Invoice{Status: InvoiceOpen}
	for i := 1; i <= 3; i++ {
		events, err := inv.Transition(InvoiceOpen, CausePaymentFailed)
		if err != nil || events[0] != EventInvoicePaymentFailed {
			t.Fatalf("attempt %d: events %v, err %v", i, events, err)
		}
		if inv.AttemptCount != i {
			t.Fatalf("attempt count = %d, want %d", inv.AttemptCount, i)
		}
		if inv.Status != InvoiceOpen {
			t.Fatalf("a failed attempt must not change the status, got %s", inv.Status)
		}
	}
	if _, err := inv.Transition(InvoiceUncollectible, CauseDunningExhausted); err != nil {
		t.Fatal(err)
	}
}

func TestInvoiceOverdueIsDerivedNotStored(t *testing.T) {
	due := mustDate("2026-03-10")
	inv := &Invoice{Status: InvoiceOpen, DueDate: due}
	if inv.IsOverdue(due) {
		t.Error("an invoice is not overdue on its due date")
	}
	if !inv.IsOverdue(due.AddDays(1)) {
		t.Error("an invoice is overdue the day after its due date")
	}
	inv.Status = InvoicePaid
	if inv.IsOverdue(due.AddDays(30)) {
		t.Error("a paid invoice is never overdue")
	}
}

func TestInvoiceAmountDue(t *testing.T) {
	inv := &Invoice{Total: 5000, AmountPaid: 1500}
	if got := inv.AmountDue(); got != 3500 {
		t.Fatalf("AmountDue = %s, want R$ 35,00", got)
	}
}

func TestSubscriptionRenewalAdvancesThePeriod(t *testing.T) {
	sub := &Subscription{
		Status:      SubscriptionActive,
		Recurrence:  monthly(),
		Anchor:      mustDate("2026-01-31"),
		PeriodIndex: 0,
	}
	if got := sub.CurrentPeriod().Start; got != mustDate("2026-01-31") {
		t.Fatalf("current period starts %s", got)
	}
	events, err := sub.Transition(SubscriptionActive, CauseRenewal)
	if err != nil || events[0] != EventSubscriptionRenewed {
		t.Fatalf("renew: events %v, err %v", events, err)
	}
	if sub.PeriodIndex != 1 {
		t.Fatalf("period index = %d, want 1", sub.PeriodIndex)
	}
	if got := sub.CurrentPeriod().Start; got != mustDate("2026-02-28") {
		t.Fatalf("after renewal the period starts %s, want 2026-02-28", got)
	}
}

func TestSubscriptionScheduledCancelIsNotAStateChange(t *testing.T) {
	sub := &Subscription{Status: SubscriptionActive, Recurrence: monthly(), Anchor: mustDate("2026-01-01")}
	events, err := sub.Transition(SubscriptionActive, CauseScheduleCancel)
	if err != nil || events[0] != EventSubscriptionUpdated {
		t.Fatalf("schedule cancel: events %v, err %v", events, err)
	}
	if sub.Status != SubscriptionActive {
		t.Fatalf("status changed to %s", sub.Status)
	}
	if sub.PeriodIndex != 0 {
		t.Fatal("scheduling a cancellation must not advance the period")
	}
}

func TestSubscriptionCanceledIsTerminal(t *testing.T) {
	for _, to := range []SubscriptionStatus{
		SubscriptionActive, SubscriptionTrialing, SubscriptionPastDue, SubscriptionPaused, SubscriptionIncomplete,
	} {
		sub := &Subscription{Status: SubscriptionCanceled}
		if _, err := sub.Transition(to, CauseManual); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("CANCELED -> %s must be rejected (reactivating is a new subscription), got %v", to, err)
		}
	}
}

func TestSubscriptionIncompleteIsNotPastDue(t *testing.T) {
	// INCOMPLETE means the service was never granted, so there is nothing to
	// revoke and no entitlement to claim on day one.
	sub := &Subscription{Status: SubscriptionIncomplete}
	if sub.IsEntitled() {
		t.Fatal("an INCOMPLETE subscription must not grant entitlement")
	}
	if _, err := sub.Transition(SubscriptionPastDue, CausePaymentFailed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("INCOMPLETE -> PAST_DUE must not exist, got %v", err)
	}
	if _, err := sub.Transition(SubscriptionActive, CauseInvoicePaid); err != nil {
		t.Fatalf("the first payment must activate it: %v", err)
	}
}

func TestSubscriptionEntitlement(t *testing.T) {
	entitled := map[SubscriptionStatus]bool{
		SubscriptionTrialing:   true,
		SubscriptionActive:     true,
		SubscriptionPastDue:    true, // dunning has not given up yet
		SubscriptionPaused:     false,
		SubscriptionCanceled:   false,
		SubscriptionIncomplete: false,
	}
	for status, want := range entitled {
		if got := (&Subscription{Status: status}).IsEntitled(); got != want {
			t.Errorf("%s: IsEntitled = %v, want %v", status, got, want)
		}
	}
}

func TestPausedSubscriptionCanBeCanceledDirectly(t *testing.T) {
	// Without this edge the operator has to resume just to cancel, producing a
	// spurious resume event and a billing period nobody wanted.
	sub := &Subscription{Status: SubscriptionPaused}
	if _, err := sub.Transition(SubscriptionCanceled, CauseManual); err != nil {
		t.Fatal(err)
	}
}

func TestPaymentAttemptSucceededRequiresAConfirmedPayment(t *testing.T) {
	for _, cause := range []Cause{CauseWalletWebhook, CauseReconciliation, CauseManualPayment} {
		a := &PaymentAttempt{Status: AttemptPending}
		if _, err := a.Transition(AttemptSucceeded, cause); err != nil {
			t.Errorf("cause %q must be allowed: %v", cause, err)
		}
	}
	a := &PaymentAttempt{Status: AttemptPending}
	if _, err := a.Transition(AttemptSucceeded, CauseManual); !errors.Is(err, ErrCauseNotAllowed) {
		t.Fatalf("a UI action must never mark an attempt succeeded, got %v", err)
	}
	// SUCCEEDED is terminal.
	a = &PaymentAttempt{Status: AttemptSucceeded}
	if _, err := a.Transition(AttemptFailed, CauseWalletWebhook); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SUCCEEDED is terminal, got %v", err)
	}
}

// FAILED and ABANDONED are two different findings and reconciliation writes
// both. Collapsing them would report a customer who walked away from a QR code
// as an integration fault, which is how a real alarm gets ignored.
func TestReconciliationCanBothFailAndAbandonAnAttempt(t *testing.T) {
	a := &PaymentAttempt{Status: AttemptPending}
	if _, err := a.Transition(AttemptFailed, CauseReconciliation); err != nil {
		t.Fatalf("a charge found expired unpaid must fail: %v", err)
	}

	a = &PaymentAttempt{Status: AttemptPending}
	if _, err := a.Transition(AttemptAbandoned, CauseReconciliation); err != nil {
		t.Fatalf("a charge wallet does not know must be abandoned: %v", err)
	}

	// ABANDONED belongs to the job alone: the rail cannot report a charge it has
	// no record of, so a webhook claiming it is a message to distrust.
	a = &PaymentAttempt{Status: AttemptPending}
	if _, err := a.Transition(AttemptAbandoned, CauseWalletWebhook); !errors.Is(err, ErrCauseNotAllowed) {
		t.Fatalf("only reconciliation abandons an attempt, got %v", err)
	}
}

func TestPaymentAttemptIdempotencyKeyIncludesTheAttemptNumber(t *testing.T) {
	// Keying on the invoice alone would make the second dunning attempt a silent
	// no-op that replays the first attempt's failure.
	first := (&PaymentAttempt{InvoiceID: "in_123", AttemptNumber: 1}).IdempotencyKey()
	second := (&PaymentAttempt{InvoiceID: "in_123", AttemptNumber: 2}).IdempotencyKey()
	if first == second {
		t.Fatalf("attempts 1 and 2 share the idempotency key %q", first)
	}
	if first != "in_123:1" {
		t.Fatalf("key = %q", first)
	}
}

func TestCheckoutSessionCannotLeaveCompleted(t *testing.T) {
	// The race this guards: a PIX confirmation landing in the same second the TTL
	// elapses must never let an expiring session undo a paid invoice.
	c := &CheckoutSession{Status: CheckoutCompleted}
	for _, to := range []CheckoutSessionStatus{CheckoutOpen, CheckoutExpired, CheckoutCanceled} {
		if _, err := c.Transition(to, CauseExpired); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("COMPLETED -> %s must be rejected, got %v", to, err)
		}
	}
}

func TestCheckoutSessionExpiredCannotReopen(t *testing.T) {
	c := &CheckoutSession{Status: CheckoutExpired}
	if _, err := c.Transition(CheckoutOpen, CauseCustomer); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("an expired session is replaced by a new one, never reopened, got %v", err)
	}
}

func TestOrganizationChargeGate(t *testing.T) {
	for _, status := range []PayoutStatus{PayoutNotConfigured, PayoutPendingCustody} {
		org := &Organization{PayoutStatus: status}
		if err := org.AuthorizeCharge(); !errors.Is(err, ErrPayoutNotEnabled) {
			t.Errorf("payout_status %q must block charges, got %v", status, err)
		}
	}
	if err := (&Organization{PayoutStatus: PayoutEnabled}).AuthorizeCharge(); err != nil {
		t.Fatalf("an enabled organization must be allowed to charge: %v", err)
	}
	// The zero value must be closed, not open: a new organization that nobody has
	// configured cannot be allowed to collect money by default.
	if err := (&Organization{}).AuthorizeCharge(); err == nil {
		t.Fatal("the zero-value organization must not be able to charge")
	}
}
