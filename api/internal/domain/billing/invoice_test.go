package billing

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Payable is the single answer behind every pay button, every `payable` flag and
// every checkout link handed out. Each case below is a way it was computed
// somewhere before it was one function, so this table is the record of what the
// scattered copies would have disagreed about.
func TestPayableIsOpenLiveAndStillOwed(t *testing.T) {
	cases := []struct {
		name string
		inv  Invoice
		want bool
	}{
		{
			name: "open, live, money outstanding",
			inv:  Invoice{Livemode: true, Status: InvoiceOpen, Total: 35000},
			want: true,
		},
		{
			name: "partially paid still leaves something to collect",
			inv:  Invoice{Livemode: true, Status: InvoiceOpen, Total: 35000, AmountPaid: 10000},
			want: true,
		},
		{
			// A draft's amount can still change. A link to one 404s on purpose
			// (checkout.load), so publishing one would be publishing a dead URL.
			name: "draft is not a bill yet",
			inv:  Invoice{Livemode: true, Status: InvoiceDraft, Total: 35000},
			want: false,
		},
		{
			name: "paid owes nothing",
			inv:  Invoice{Livemode: true, Status: InvoicePaid, Total: 35000, AmountPaid: 35000},
			want: false,
		},
		{
			name: "void was withdrawn",
			inv:  Invoice{Livemode: true, Status: InvoiceVoid, Total: 35000},
			want: false,
		},
		{
			// The free plan. It is OPEN for no time at all — CauseNothingDue closes
			// it on issue — but the rule must not depend on that ordering.
			name: "zero total has nothing to collect",
			inv:  Invoice{Livemode: true, Status: InvoiceOpen, Total: 0},
			want: false,
		},
		{
			// The one that is not about the invoice at all: wallet has no sandbox
			// charge rail, so collecting here would take real money against a
			// document that is not real (ADR 0004, second amendment).
			name: "test mode has no rail to collect on",
			inv:  Invoice{Livemode: false, Status: InvoiceOpen, Total: 35000},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.inv.Payable(); got != tc.want {
				t.Errorf("Payable() = %v, want %v (status %s, livemode %v, due %s)",
					got, tc.want, tc.inv.Status, tc.inv.Livemode, tc.inv.AmountDue())
			}
		})
	}
}

// Payable and IsOverdue answer different questions, and an overdue invoice is
// still one somebody can pay. Asserting it here because the two are one word
// apart at a call site and collapsing them would silently stop collecting from
// exactly the customers dunning is chasing.
func TestAnOverdueInvoiceIsStillPayable(t *testing.T) {
	inv := Invoice{
		Livemode: true,
		Status:   InvoiceOpen,
		Total:    35000,
		DueDate:  brcal.New(2026, time.March, 1),
	}
	today := brcal.New(2026, time.March, 20)

	if !inv.IsOverdue(today) {
		t.Fatal("setup: the invoice should be overdue")
	}
	if !inv.Payable() {
		t.Error("an overdue invoice must stay payable; dunning exists to make it get paid")
	}
}
