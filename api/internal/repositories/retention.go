package repositories

import "time"

// Retention implements ADR 0009.
//
// In DynamoDB the TTL is an attribute written when the item is created, so
// changing the policy later reaches only new items. That has one practical
// consequence that drives this whole file: **every write sets the attribute**,
// even for records that never expire — for those it is explicitly absent rather
// than accidentally missing, and the difference is visible in review.
//
// The caveat that must not be lost: AWS deletes typically within ~48 h *after*
// expiry. This is data hygiene. It is **not** a guarantee of "deleted on day X"
// for a hard legal deadline; if such a requirement appears, the purge becomes an
// explicit job and this file is not the answer.

// Retention is how long a record is kept.
type Retention int

const (
	// RetentionPermanent writes no TTL. Used for commercial documents, whose
	// disposal is a reviewed process rather than an attribute.
	RetentionPermanent Retention = iota
	// RetentionFiveYears matches the legal floor of the document a record
	// explains, so evidence never outlives — or predeceases — the invoice.
	RetentionFiveYears
	// RetentionTwentyFourMonths is for raw usage: the aggregate already lives on
	// the invoice, and the raw record exists to settle a consumption dispute.
	RetentionTwentyFourMonths
	// RetentionNinetyDays is the redelivery/debugging window.
	RetentionNinetyDays
)

// Per-record-type retention. Kept as one table so the policy is readable in one
// place rather than inferred from scattered call sites.
const (
	RetentionOrganization    = RetentionPermanent
	RetentionCustomer        = RetentionPermanent // anonymized in place, never deleted
	RetentionProduct         = RetentionPermanent
	RetentionPrice           = RetentionPermanent // immutable and referenced by old subscriptions
	RetentionSubscription    = RetentionPermanent // a canceled one explains invoices that still exist
	RetentionInvoice         = RetentionPermanent
	RetentionInvoiceItem     = RetentionPermanent
	RetentionCreditNote      = RetentionPermanent
	RetentionInvoiceCounter  = RetentionPermanent // losing it restarts invoice numbering
	RetentionAuditLog        = RetentionFiveYears
	RetentionPaymentAttempt  = RetentionFiveYears
	RetentionUsageRecord     = RetentionTwentyFourMonths
	RetentionCredential      = RetentionPermanent // revoking is a flag, not an expiry
	RetentionEvent           = RetentionNinetyDays
	RetentionWebhookDelivery = RetentionNinetyDays
	// RetentionCheckoutSession applies to every session, not only expired and
	// canceled ones. A completed session is support data too — the evidence of
	// payment is the PaymentAttempt, which is kept for five years.
	RetentionCheckoutSession = RetentionNinetyDays
)

// ExpiresAt returns the Unix timestamp DynamoDB should expire the item at, or
// nil for a record that is kept.
//
// It takes `now` rather than reading the clock so that a repository's write path
// stays testable without freezing global time.
func (r Retention) ExpiresAt(now time.Time) *int64 {
	var d time.Duration
	switch r {
	case RetentionPermanent:
		return nil
	case RetentionFiveYears:
		return unixPtr(now.AddDate(5, 0, 0))
	case RetentionTwentyFourMonths:
		return unixPtr(now.AddDate(0, 24, 0))
	case RetentionNinetyDays:
		d = 90 * 24 * time.Hour
	default:
		// An unknown class must not silently mean "keep forever": that is how a
		// new record type quietly acquires unlimited retention.
		panic("repositories: unknown retention class")
	}
	return unixPtr(now.Add(d))
}

func unixPtr(t time.Time) *int64 {
	v := t.Unix()
	return &v
}
