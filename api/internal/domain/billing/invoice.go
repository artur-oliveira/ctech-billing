package billing

import (
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// InvoiceStatus is the lifecycle of a commercial document.
//
// There are exactly five. Three states people ask for are deliberately absent
// (assessment § 6.1), because each one that exists multiplies test and UI paths:
//
//   - "pending" is OPEN.
//   - "overdue" is OPEN with a due date in the past — derived on read, shown in
//     the UI, never persisted. Persisting it means a nightly job that can be
//     wrong, and two sources of truth about lateness.
//   - "refunded" is the existence of a CreditNote covering the total.
type InvoiceStatus string

const (
	InvoiceDraft         InvoiceStatus = "DRAFT"
	InvoiceOpen          InvoiceStatus = "OPEN"
	InvoicePaid          InvoiceStatus = "PAID"
	InvoiceVoid          InvoiceStatus = "VOID"
	InvoiceUncollectible InvoiceStatus = "UNCOLLECTIBLE"
)

// Invoice events.
const (
	EventInvoiceCreated       EventType = "invoice.created"
	EventInvoiceFinalized     EventType = "invoice.finalized"
	EventInvoicePaid          EventType = "invoice.paid"
	EventInvoicePaymentFailed EventType = "invoice.payment_failed"
	EventInvoiceUncollectible EventType = "invoice.uncollectible"
	EventInvoiceVoided        EventType = "invoice.voided"
)

// paymentCauses are the causes that constitute an actual, reconciled payment.
// CauseManual is absent on purpose: an operator changing a status is not a
// payment. CauseManualPayment is present because it records a real receipt with
// an actor and its own permission (assessment § 6.4).
var paymentCauses = []Cause{CauseWalletWebhook, CauseReconciliation, CauseManualPayment}

var invoiceTransitions = map[edge[InvoiceStatus]][]rule{
	{InvoiceDraft, InvoiceOpen}: {{
		event:  EventInvoiceFinalized,
		causes: []Cause{CauseScheduler, CauseManual},
	}},
	{InvoiceDraft, InvoiceVoid}: {{
		event:  EventInvoiceVoided,
		causes: []Cause{CauseManual},
	}},
	{InvoiceOpen, InvoicePaid}: {
		{
			event:  EventInvoicePaid,
			causes: paymentCauses,
		},
		// A zero-total invoice is settled the moment it is issued: there is
		// nothing to collect, so there is nothing to wait for. It emits the same
		// invoice.paid a real payment does, on purpose — a consumer that grants
		// service on that event must grant it to a free plan too, and the only
		// alternative is every consumer learning a second event that means the
		// same thing. What separates them is the cause, which is recorded, and
		// the guard in Transition, which refuses this one when money is owed.
		{
			event:  EventInvoicePaid,
			causes: []Cause{CauseNothingDue},
		},
	},
	// Self-edge: a failed attempt does not change the status, but it is a real
	// event, it advances the dunning counter, and the timeline has to show it.
	{InvoiceOpen, InvoiceOpen}: {{
		event:  EventInvoicePaymentFailed,
		causes: []Cause{CausePaymentFailed},
	}},
	{InvoiceOpen, InvoiceUncollectible}: {{
		event:  EventInvoiceUncollectible,
		causes: []Cause{CauseDunningExhausted},
	}},
	{InvoiceOpen, InvoiceVoid}: {{
		event:  EventInvoiceVoided,
		causes: []Cause{CauseManual},
	}},
	// A customer paying a long-overdue charge is a real, common event, so this
	// edge exists — but only for a reconciled payment. Reached by CauseManual it
	// would be an operator undoing a write-off with a click, with no money behind
	// it, which is the one thing this table exists to prevent.
	{InvoiceUncollectible, InvoicePaid}: {{
		event:  EventInvoicePaid,
		causes: paymentCauses,
	}},
}

// Invoice is a commercial document. Once finalized its lines are frozen: the
// only way to correct a PAID invoice is a CreditNote.
type Invoice struct {
	ID             string        `dynamodbav:"id"              json:"id"`
	OrganizationID string        `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool          `dynamodbav:"livemode"        json:"livemode"`
	CustomerID     string        `dynamodbav:"customer_id"     json:"customer_id"`
	SubscriptionID string        `dynamodbav:"subscription_id,omitempty" json:"subscription_id,omitempty"`
	Status         InvoiceStatus `dynamodbav:"status"          json:"status"`

	// OwnerKey routes this invoice's events (ADR 0016), copied from the
	// subscription that produced it. Empty on a one-off invoice, which therefore
	// only reaches endpoints that asked for everything.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"-"`

	// Policy is the dunning schedule this invoice was issued under, copied at
	// finalization and never shared with the product or organization it came
	// from (the same rule Metadata follows — ADR 0008).
	//
	// Copied rather than looked up because DunningStep is an **index into it**.
	// If the schedule could be edited underneath an invoice in flight, a stored
	// step would silently come to mean a different day and a different action —
	// an invoice at step 4 could jump from "third reminder" to "give up".
	//
	// Empty on an invoice finalized before per-plan policies existed, which
	// Schedule() reads as the default.
	Policy DunningSchedule `dynamodbav:"dunning_policy,omitempty" json:"-"`

	// DunningStep is which entry of Policy this invoice has reached. It
	// is stored rather than derived from today's date, which is what makes the
	// job re-runnable: a missed day replayed twice performs each step once,
	// because the invoice has already moved past it.
	DunningStep int `dynamodbav:"dunning_step,omitempty" json:"-"`
	// LastRemindedAt is when a reminder was last sent, for support answering
	// "did you tell them?".
	LastRemindedAt string `dynamodbav:"last_reminded_at,omitempty" json:"-"`

	// Number is the per-organization sequential invoice number, assigned at
	// finalization and never reused. Sequential numbering cannot be applied
	// retroactively, which is why it is in the MVP (assessment § 13).
	Number int64 `dynamodbav:"number,omitempty" json:"number,omitempty"`

	Period   Period     `dynamodbav:"period"   json:"period"`
	DueDate  brcal.Date `dynamodbav:"due_date" json:"due_date"`
	Currency string     `dynamodbav:"currency" json:"currency"`

	Subtotal   Cents `dynamodbav:"subtotal"    json:"subtotal"`
	Discount   Cents `dynamodbav:"discount"    json:"discount"`
	Total      Cents `dynamodbav:"total"       json:"total"`
	AmountPaid Cents `dynamodbav:"amount_paid" json:"amount_paid"`

	// PaidAt is when this invoice was settled, RFC 3339 in UTC. It is written by
	// the repository on the transition to PAID, not by whoever caused it, so the
	// webhook path and the reconciler cannot disagree about it — and a
	// zero-total invoice, settled the moment it is issued, carries one too.
	//
	// Stored rather than derived from the audit trail: "quando foi pago" is on
	// the customer's own receipt, and reading an audit log to render a receipt
	// would make the portal depend on a table it is otherwise not allowed to
	// see.
	PaidAt string `dynamodbav:"paid_at,omitempty" json:"paid_at,omitempty"`

	// AttemptCount is how many collection attempts have been made. It drives the
	// dunning policy and is why PaymentAttempt is a separate entity: folding the
	// attempt into the invoice makes dunning and diagnosis impossible.
	AttemptCount int `dynamodbav:"attempt_count" json:"attempt_count"`

	// Metadata is copied from the Subscription at generation time, never shared
	// with it (ADR 0008).
	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// Schedule is the policy this invoice follows.
//
// An invoice with none stored follows the default, which is what every invoice
// finalized before this field existed does — and is why no backfill is needed.
func (i *Invoice) Schedule() DunningSchedule {
	if len(i.Policy) > 0 {
		return i.Policy
	}
	return DefaultDunningPolicy
}

// AmountDue is what is still owed. Derived, never stored — a stored copy is one
// more thing that can disagree with the lines.
func (i *Invoice) AmountDue() Cents { return i.Total - i.AmountPaid }

// NothingDue reports an invoice that costs the customer nothing.
//
// It is a real invoice and not a skipped one: the free plan's period was served
// and the document says so, the sequential numbering stays gapless, and the
// subscription renews through the same path every other plan renews through. It
// is only what happens *after* finalization that differs — no charge is opened
// and no reminder is scheduled, because both would be about an amount of zero.
func (i *Invoice) NothingDue() bool { return i.Total == 0 }

// Payable reports an invoice a customer can actually pay right now. It is the
// one answer behind every "pagar" button, every `payable` flag on the wire, and
// every checkout link handed out — four places that were computing it
// separately and would eventually have disagreed.
//
// Three conditions, and the third is the surprising one:
//
//   - OPEN. A draft is not a bill yet and a paid or void one is not a bill any
//     more.
//   - Something is still owed. A zero-total invoice is closed on issue
//     (NothingDue), and a page offering to collect R$ 0,00 is a dead end.
//   - **Livemode.** Collection runs through ctech-wallet, and wallet has no test
//     rail for this charge kind — `POST /v1.0/internal/wallet/charge` opens a
//     real PIX charge against a real provider whatever billing's mode is. So a
//     test-mode invoice offered for payment collects real money against a
//     document that, by design, is not real. Until a sandbox rail exists this
//     is the honest answer, and it is one line to relax when it does.
//
// Collector.Pay checks the same three separately rather than calling this,
// because it must say *which* one failed: "not open" and "test mode" are
// different problems with different fixes, and one error for both makes an
// operator guess.
func (i *Invoice) Payable() bool {
	return i.Livemode && i.Status == InvoiceOpen && i.AmountDue() > 0
}

// IsOverdue reports whether the invoice is OPEN past its due date, as of today.
// This is the derived attribute that "overdue" would otherwise be a state for.
func (i *Invoice) IsOverdue(today brcal.Date) bool {
	return i.Status == InvoiceOpen && i.DueDate.Before(today)
}

// Transition moves the invoice to `to`, returning the events to emit.
//
// The invoice is only mutated when the move is legal, so a rejected transition
// leaves a caller's in-memory copy untouched and safe to reuse.
func (i *Invoice) Transition(to InvoiceStatus, cause Cause) ([]EventType, error) {
	// The guard that makes CauseNothingDue safe to have at all. Without it the
	// cause is a way to mark any invoice PAID with no money behind it and no
	// operator named — exactly what CauseManual is kept out of paymentCauses to
	// prevent.
	if cause == CauseNothingDue && !i.NothingDue() {
		return nil, fmt.Errorf("%w: invoice %s owes %s, so it cannot be closed as nothing due",
			ErrCauseNotAllowed, i.ID, i.AmountDue())
	}
	event, err := apply(invoiceTransitions, i.Status, to, cause)
	if err != nil {
		return nil, err
	}
	i.Status = to
	if event == EventInvoicePaymentFailed {
		i.AttemptCount++
	}
	return []EventType{event}, nil
}
