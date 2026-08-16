package billing

import (
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrInvalidUsage reports a usage record that cannot be accepted.
var ErrInvalidUsage = errors.New("invalid usage record")

// UsageRecord is one reported unit of consumption against a metered
// subscription item.
//
// Idempotency is mandatory, not advisory: a product reporting usage will retry
// on a timeout, and a double-counted report is an overcharge the customer finds
// before we do.
type UsageRecord struct {
	ID                 string `dynamodbav:"id"                   json:"id"`
	OrganizationID     string `dynamodbav:"organization_id"      json:"organization_id"`
	Livemode           bool   `dynamodbav:"livemode"             json:"livemode"`
	SubscriptionItemID string `dynamodbav:"subscription_item_id" json:"subscription_item_id"`

	Quantity int64 `dynamodbav:"quantity" json:"quantity"`
	// OccurredAt is when the consumption happened, in UTC. Which period it falls
	// into is decided by the civil date in America/Sao_Paulo, never by the UTC
	// day — see brcal.
	OccurredAt time.Time `dynamodbav:"occurred_at" json:"occurred_at"`

	// IdempotencyKey is supplied by the reporting product. Two records with the
	// same key are the same event, however many times it was sent.
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"idempotency_key"`
}

// Validate checks the record can be counted.
func (u *UsageRecord) Validate() error {
	if u.SubscriptionItemID == "" {
		return fmt.Errorf("%w: missing subscription item", ErrInvalidUsage)
	}
	if u.IdempotencyKey == "" {
		return fmt.Errorf("%w: missing idempotency key", ErrInvalidUsage)
	}
	// Negative usage would be a correction, and a correction to a closed period is
	// a CreditNote, not a negative record. Allowing it here would let a caller
	// silently rewrite a period that has already been invoiced.
	if u.Quantity < 0 {
		return fmt.Errorf("%w: quantity %d is negative", ErrInvalidUsage, u.Quantity)
	}
	if u.OccurredAt.IsZero() {
		return fmt.Errorf("%w: missing occurred_at", ErrInvalidUsage)
	}
	return nil
}

// Date is the civil date the record counts towards.
func (u *UsageRecord) Date() brcal.Date { return brcal.FromTime(u.OccurredAt) }

// SumUsage totals the records that fall inside period.
//
// Simple summation is the only aggregation in the MVP. Max, last-value, unique
// counts and sliding windows are real features of mature metered billing and
// none of them is needed by the first consumer, so building them now would be
// guessing at semantics nobody has asked for (assessment § 13).
//
// Records outside the period are ignored rather than rejected: a late report for
// a closed period is a normal occurrence, and the caller decides whether to
// re-invoice it or drop it — silently folding it into the current period would
// bill the customer in the wrong month.
func SumUsage(records []UsageRecord, period Period) int64 {
	var total int64
	for _, r := range records {
		if period.Contains(r.Date()) {
			total += r.Quantity
		}
	}
	return total
}

// DeduplicateUsage returns the records with duplicate idempotency keys removed,
// keeping the first occurrence of each key.
//
// The repository is the real enforcement point (a conditional write on the key);
// this exists for the period-close path, which reads a partition and must not
// double-count if a duplicate ever got through.
func DeduplicateUsage(records []UsageRecord) []UsageRecord {
	seen := make(map[string]struct{}, len(records))
	out := make([]UsageRecord, 0, len(records))
	for _, r := range records {
		if _, dup := seen[r.IdempotencyKey]; dup {
			continue
		}
		seen[r.IdempotencyKey] = struct{}{}
		out = append(out, r)
	}
	return out
}
