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
		state, tone := invoiceState(inv, today)

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
	}{
		{"hoje", open(today), "Vence hoje", toneUrgent},
		{"amanhã", open(today.AddDays(1)), "Vence amanhã", toneAttention},
		{"esta semana", open(today.AddDays(3)), "Vence em 3 dias", toneAttention},
		{"um dia vencida", open(today.AddDays(-1)), "Vencida há 1 dia", toneUrgent},
		{"vencida", open(today.AddDays(-5)), "Vencida há 5 dias", toneUrgent},
		{"longe", open(brcal.New(2026, time.April, 20)), "Vence em 20/04/2026", toneNeutral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, tone := invoiceState(tc.inv, today)
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

	_, overdue := invoiceState(&billing.Invoice{Status: billing.InvoiceOpen, DueDate: today.AddDays(-1)}, today)
	if overdue != toneUrgent {
		t.Errorf("an overdue invoice reads as %q", overdue)
	}
	_, paid := invoiceState(&billing.Invoice{Status: billing.InvoicePaid, DueDate: today.AddDays(-30)}, today)
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
	if state == "Ativa" {
		t.Error("a subscription ending at period end must not read as plain Ativa")
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

	if got := describeLines(nil); got != "Fatura" {
		t.Errorf("empty = %q", got)
	}
	if got := describeLines(one); got != "DF-e Basic" {
		t.Errorf("single = %q", got)
	}
	if got := describeLines(three); got != "DF-e Basic e mais 2" {
		t.Errorf("many = %q", got)
	}
}
