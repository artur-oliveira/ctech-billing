package billing

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidCreditNote reports a credit note that cannot be issued.
var ErrInvalidCreditNote = errors.New("invalid credit note")

// CreditNote event.
const EventCreditNoteCreated EventType = "credit_note.created"

// CreditNote is the only way to correct an invoice that has been issued.
//
// It exists because an issued invoice is immutable. Editing one — even to fix a
// genuine mistake — destroys the record of what the customer was actually asked
// to pay, which is the one thing an invoice is for.
//
// A credit note **never moves money**. If the customer is owed cash back, wallet
// or the PSP performs the refund and billing records that it happened. Billing
// issuing money is how this service would start becoming a wallet
// (assessment § 14).
type CreditNote struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`
	InvoiceID      string `dynamodbav:"invoice_id"      json:"invoice_id"`
	CustomerID     string `dynamodbav:"customer_id"     json:"customer_id"`

	// Amount is positive: it is the amount being credited, not a negative charge.
	Amount   Cents  `dynamodbav:"amount"   json:"amount"`
	Currency string `dynamodbav:"currency" json:"currency"`
	Reason   string `dynamodbav:"reason"   json:"reason"`

	// RefundedExternally records that wallet or the PSP actually returned money
	// for this credit, and its reference there. Empty means the credit exists only
	// as a document — which is the common case.
	RefundedExternally bool   `dynamodbav:"refunded_externally"        json:"refunded_externally"`
	ExternalRefundRef  string `dynamodbav:"external_refund_ref,omitempty" json:"external_refund_ref,omitempty"`

	CreatedBy string    `dynamodbav:"created_by" json:"created_by"`
	CreatedAt time.Time `dynamodbav:"created_at" json:"created_at"`

	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// ValidateAgainst checks the note can be issued for inv, given the credit
// already issued against it.
//
// The rule that matters: total credits may never exceed the invoice total.
// Over-crediting turns a document that says "you owe us X" into one that says
// "we owe you Y" without anyone deciding to, and it is trivially reachable by
// two operators issuing credits at the same time — which is why the check lives
// here and is re-run against freshly read totals at write time, not only in a
// form.
func (cn *CreditNote) ValidateAgainst(inv *Invoice, alreadyCredited Cents) error {
	if cn.Amount <= 0 {
		return fmt.Errorf("%w: amount %d must be positive", ErrInvalidCreditNote, cn.Amount)
	}
	if cn.InvoiceID != inv.ID {
		return fmt.Errorf("%w: note is for invoice %q, not %q", ErrInvalidCreditNote, cn.InvoiceID, inv.ID)
	}
	// A DRAFT invoice has not been issued, so there is nothing to correct: change
	// the lines instead. A VOID one was cancelled and owes nothing.
	switch inv.Status {
	case InvoiceOpen, InvoicePaid, InvoiceUncollectible:
	default:
		return fmt.Errorf("%w: cannot credit an invoice in status %s", ErrInvalidCreditNote, inv.Status)
	}
	if alreadyCredited < 0 {
		return fmt.Errorf("%w: already-credited total %d is negative", ErrInvalidCreditNote, alreadyCredited)
	}
	if alreadyCredited+cn.Amount > inv.Total {
		return fmt.Errorf("%w: crediting %s on top of %s exceeds the invoice total of %s",
			ErrInvalidCreditNote, cn.Amount, alreadyCredited, inv.Total)
	}
	if err := cn.Metadata.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidCreditNote, err)
	}
	return nil
}

// FullyCredited reports whether the credits issued cover the whole invoice —
// the condition the UI renders as "refunded", which is why "refunded" is not a
// status (assessment § 6.1).
func FullyCredited(inv *Invoice, totalCredited Cents) bool {
	return inv.Total > 0 && totalCredited >= inv.Total
}
