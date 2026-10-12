package billing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// MethodBYOK is an attempt collected through the organization's own bank
// connection (spec 2026-10-11 § 7.2): a due-date charge whose money never
// passes through CTech.
const MethodBYOK PaymentMethod = "byok"

// ProductPixDue is Inter's cobv, the phase 1 BYOK product.
const ProductPixDue = "pix_due"

const (
	// CauseByokConfirmed is a BYOK charge re-queried at the provider and found
	// paid (webhook, sweep or statement line woke it; only the re-query counts).
	CauseByokConfirmed Cause = "byok_confirmed"
	// CauseByokDisconnected fails an open BYOK attempt whose charge was removed
	// by a disconnect (spec § 3.6 step 1).
	CauseByokDisconnected Cause = "byok_disconnected"
)

// InterTxid is the Inter txid of an attempt: "ctb" + the attempt's ULID, 29
// characters of [A-Za-z0-9], inside Bacen's 26-35 rule.
func InterTxid(attemptID string) string { return "ctb" + strings.TrimPrefix(attemptID, "pa_") }

// NewRefundID is a provider refund id: "rf" + a ULID (28 characters, inside
// Inter's [a-zA-Z0-9]{1,35}), stored before the call so a retry is idempotent.
func NewRefundID() string { return "rf" + id.New() }

// CollectionSettings are a tenant's late fees, in basis points (spec § 7.2):
// a fine once (multa, 200 = 2 %) and monthly interest (juros, calendar days).
// Copied onto an invoice at finalization, so a later change never alters an
// issued charge.
type CollectionSettings struct {
	FineBasisPoints            int `dynamodbav:"fine_bp,omitempty"             json:"fine_bp"`
	MonthlyInterestBasisPoints int `dynamodbav:"monthly_interest_bp,omitempty" json:"monthly_interest_bp"`
}

var ErrInvalidCollectionSettings = errors.New("invalid collection settings")

// Validate keeps the fine within 0-20 % and interest within 0-10 % a month.
func (s CollectionSettings) Validate() error {
	if s.FineBasisPoints < 0 || s.FineBasisPoints > 2000 {
		return fmt.Errorf("%w: fine must be 0 to 20%%", ErrInvalidCollectionSettings)
	}
	if s.MonthlyInterestBasisPoints < 0 || s.MonthlyInterestBasisPoints > 1000 {
		return fmt.Errorf("%w: monthly interest must be 0 to 10%%", ErrInvalidCollectionSettings)
	}
	return nil
}

func (s CollectionSettings) IsZero() bool { return s == CollectionSettings{} }

// Excess payment statuses (D19: never refunded automatically).
const (
	ExcessOpen         = "open"
	ExcessRefunding    = "refunding"
	ExcessRefunded     = "refunded"
	ExcessRefundFailed = "refund_failed"
)

// RefundWindow is Inter's limit for a Pix refund after the payment.
const RefundWindow = 90 * 24 * time.Hour

// ExcessPayment is money above what an invoice owed, or a second payment on
// the same charge (spec § 7.4 item 3). The money is the merchant's, in the
// merchant's account; the row only says so and lets the merchant refund it.
type ExcessPayment struct {
	ID             string    `dynamodbav:"id"              json:"id"`
	OrganizationID string    `dynamodbav:"organization_id" json:"-"`
	Livemode       bool      `dynamodbav:"livemode"        json:"livemode"`
	InvoiceID      string    `dynamodbav:"invoice_id"      json:"invoice_id"`
	AttemptID      string    `dynamodbav:"attempt_id"      json:"attempt_id"`
	AttemptNumber  int       `dynamodbav:"attempt_number"  json:"-"`
	Provider       string    `dynamodbav:"provider"        json:"provider"`
	PaymentID      string    `dynamodbav:"payment_id"      json:"-"`
	Amount         Cents     `dynamodbav:"amount"          json:"amount"`
	Status         string    `dynamodbav:"status"          json:"status"`
	RefundID       string    `dynamodbav:"refund_id,omitempty" json:"-"`
	PaidAt         time.Time `dynamodbav:"paid_at"         json:"paid_at"`
	CreatedAt      time.Time `dynamodbav:"created_at"      json:"created_at"`
}

// Refundable reports whether the provider still accepts a refund at now.
func (e ExcessPayment) Refundable(now time.Time) bool { return !now.After(e.PaidAt.Add(RefundWindow)) }

// PayableUntil is the last day a due-date charge is payable: the due date
// plus the validity after it (at least one day).
func PayableUntil(due brcal.Date, validityDays int) brcal.Date {
	return due.AddDays(max(1, validityDays))
}

// Collectable is what a BYOK charge asks for: the amount due net of the credit
// notes already issued (spec § 7.3: a credit note on an open invoice updates
// the charge), never below zero.
func (i *Invoice) Collectable() Cents { return max(0, i.AmountDue()-i.CreditedTotal) }

// ValidityDays is how long after its due date an invoice's BYOK charge stays
// payable: its copied dunning schedule's last offset, at least one day (plan
// scope decision 4), so the Pix is payable exactly as long as billing chases.
func (i *Invoice) ValidityDays() int {
	last := 0
	for _, step := range i.Schedule() {
		last = max(last, step.Offset)
	}
	return max(1, last)
}
