package v1

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// The translation layer is the only place a consumer's words are decided, so it
// is tested directly rather than through HTTP. What matters is not the exact
// wording — that will be edited — but that no branch falls through to an
// internal name, and that the tone matches the urgency of the words.

func TestInvoiceStateNeverLeaksTheInternalStatus(t *testing.T) {
	today := brcal.New(2026, time.March, 10)
	statuses := []billing.InvoiceStatus{
		billing.InvoiceDraft,
		billing.InvoiceOpen,
		billing.InvoicePaid,
		billing.InvoiceVoid,
		billing.InvoiceUncollectible,
	}

	for _, status := range statuses {
		inv := &billing.Invoice{Status: status, DueDate: today.AddDays(30)}
		state, tone, _ := invoiceState(inv, today)

		if state == "" {
			t.Errorf("status %q produced no phrase", status)
		}
		if state == string(status) {
			t.Errorf("status %q leaked its internal name to the consumer", status)
		}
		switch tone {
		case toneNeutral, tonePositive, toneAttention, toneUrgent:
		default:
			t.Errorf("status %q produced tone %q, which is outside the closed set", status, tone)
		}
	}
}

func TestInvoiceStateReadsTheDueDateAsAPerson(t *testing.T) {
	today := brcal.New(2026, time.March, 10)
	open := func(due brcal.Date) *billing.Invoice {
		return &billing.Invoice{Status: billing.InvoiceOpen, DueDate: due}
	}

	cases := []struct {
		name string
		inv  *billing.Invoice
		want string
		tone string
		days int
	}{
		{"today", open(today), "due_today", toneUrgent, 0},
		{"tomorrow", open(today.AddDays(1)), "due_tomorrow", toneAttention, 1},
		{"this week", open(today.AddDays(3)), "due_soon", toneAttention, 3},
		{"seven days", open(today.AddDays(7)), "due_soon", toneAttention, 7},
		{"one day overdue", open(today.AddDays(-1)), "overdue", toneUrgent, -1},
		{"overdue", open(today.AddDays(-5)), "overdue", toneUrgent, -5},
		{"far", open(brcal.New(2026, time.April, 20)), "upcoming", toneNeutral, 41},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, tone, days := invoiceState(tc.inv, today)
			if days != tc.days {
				t.Errorf("days_until_due = %d, want %d", days, tc.days)
			}
			if state != tc.want {
				t.Errorf("state = %q, want %q", state, tc.want)
			}
			if tone != tc.tone {
				t.Errorf("tone = %q, want %q", tone, tc.tone)
			}
		})
	}
}

// An overdue invoice must never read as calm, and a paid one must never read as
// something to act on. This is the property the tone exists for.
func TestOverdueIsUrgentAndPaidIsNot(t *testing.T) {
	today := brcal.New(2026, time.March, 10)

	_, overdue, _ := invoiceState(&billing.Invoice{Status: billing.InvoiceOpen, DueDate: today.AddDays(-1)}, today)
	if overdue != toneUrgent {
		t.Errorf("an overdue invoice reads as %q", overdue)
	}
	_, paid, _ := invoiceState(&billing.Invoice{Status: billing.InvoicePaid, DueDate: today.AddDays(-30)}, today)
	if paid != tonePositive {
		t.Errorf("a paid invoice reads as %q", paid)
	}
}

func TestSubscriptionStateNeverLeaksTheInternalStatus(t *testing.T) {
	statuses := []billing.SubscriptionStatus{
		billing.SubscriptionIncomplete,
		billing.SubscriptionTrialing,
		billing.SubscriptionActive,
		billing.SubscriptionPastDue,
		billing.SubscriptionPaused,
		billing.SubscriptionCanceled,
	}
	for _, status := range statuses {
		state, tone := subscriptionState(&billing.Subscription{Status: status})
		if state == "" || state == string(status) {
			t.Errorf("status %q produced %q", status, state)
		}
		switch tone {
		case toneNeutral, tonePositive, toneAttention, toneUrgent:
		default:
			t.Errorf("status %q produced tone %q", status, tone)
		}
	}
}

// A subscription set to end still runs, and saying only "Ativa" would hide the
// one fact its owner needs.
func TestEndingSubscriptionSaysSo(t *testing.T) {
	state, tone := subscriptionState(&billing.Subscription{
		Status:            billing.SubscriptionActive,
		CancelAtPeriodEnd: true,
	})
	if state != "active_until_period_end" {
		t.Errorf("a subscription ending at period end read as %q", state)
	}
	if tone != toneAttention {
		t.Errorf("tone = %q, want attention", tone)
	}
}

func TestPayableIsDecidedByTheServer(t *testing.T) {
	today := brcal.New(2026, time.March, 10)
	cases := []struct {
		name string
		inv  billing.Invoice
		want bool
	}{
		{"aberta com saldo", billing.Invoice{Livemode: true, Status: billing.InvoiceOpen, Total: 4990, DueDate: today}, true},
		{"paga", billing.Invoice{Livemode: true, Status: billing.InvoicePaid, Total: 4990, AmountPaid: 4990, DueDate: today}, false},
		{"anulada", billing.Invoice{Livemode: true, Status: billing.InvoiceVoid, Total: 4990, DueDate: today}, false},
		{"rascunho", billing.Invoice{Livemode: true, Status: billing.InvoiceDraft, Total: 4990, DueDate: today}, false},
		// The portal only ever serves live mode (ADR 0012), so this case is
		// defensive rather than reachable — which is the reason to keep it. The day
		// somebody makes the portal mode-aware, this is what stops the pay button
		// from appearing over a rail that does not exist.
		{"modo de teste", billing.Invoice{Status: billing.InvoiceOpen, Total: 4990, DueDate: today}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newPortalInvoiceResponse(&tc.inv, nil, today)
			if got.Payable != tc.want {
				t.Errorf("payable = %v, want %v", got.Payable, tc.want)
			}
		})
	}
}

func TestDescribeLinesSaysWhatTheInvoiceIsFor(t *testing.T) {
	one := []billing.InvoiceItem{{Description: "DF-e Basic"}}
	three := []billing.InvoiceItem{{Description: "DF-e Basic"}, {Description: "Extra"}, {Description: "Ajuste"}}

	if d, n := describeLines(nil); d != "" || n != 0 {
		t.Errorf("empty = %q, %d", d, n)
	}
	if d, n := describeLines(one); d != "DF-e Basic" || n != 0 {
		t.Errorf("single = %q, %d", d, n)
	}
	if d, n := describeLines(three); d != "DF-e Basic" || n != 2 {
		t.Errorf("many = %q, %d", d, n)
	}
}

// A zero-total invoice is issued and settled on the spot (ADR 0019), so it is
// PAID with nothing ever paid. The screen must be able to tell that from a bill
// that has not been opened yet, and `amount_paid > 0` cannot: it was the bug
// that showed a Free-plan customer "esta fatura ainda não está aberta para
// pagamento" on an invoice that says Paga.
func TestZeroTotalInvoiceIsSettled(t *testing.T) {
	today := brcal.New(2026, time.August, 20)
	inv := &billing.Invoice{
		Status:   billing.InvoicePaid,
		DueDate:  brcal.New(2026, time.August, 17),
		Total:    0,
		PaidAt:   "2026-08-16T14:03:00Z",
		Currency: "BRL",
	}

	out := newPortalInvoiceResponse(inv, nil, today)

	if !out.Settled {
		t.Error("a PAID invoice must be settled whatever it cost")
	}
	if out.Payable {
		t.Error("a settled invoice is not payable")
	}
	if out.AmountPaid != 0 {
		t.Errorf("AmountPaid = %d, want 0 — nothing was paid", out.AmountPaid)
	}
	if out.PaidOn == nil || out.PaidOn.String() != "2026-08-16" {
		t.Errorf("PaidOn = %v, want the São Paulo civil date of the settlement", out.PaidOn)
	}
}

// An invoice nobody has paid carries no date, and the absence is what the
// screen branches on.
func TestOpenInvoiceIsNeitherSettledNorDated(t *testing.T) {
	today := brcal.New(2026, time.August, 20)
	inv := &billing.Invoice{Status: billing.InvoiceOpen, DueDate: today.AddDays(5), Total: 9900}

	out := newPortalInvoiceResponse(inv, nil, today)

	if out.Settled {
		t.Error("an OPEN invoice is not settled")
	}
	if out.PaidOn != nil {
		t.Errorf("PaidOn = %v, want nothing", out.PaidOn)
	}
}
