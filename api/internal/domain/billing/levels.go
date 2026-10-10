package billing

import (
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// LevelRecord is one report of a level: the whole current count of something a
// customer has (spaces, people), never a delta (spec § 6.1). It belongs to the
// customer reference and the meter, not to a subscription item, so it is known
// before the customer subscribes to a plan that bills it.
type LevelRecord struct {
	OrganizationID string    `dynamodbav:"organization_id" json:"-"`
	Livemode       bool      `dynamodbav:"livemode"        json:"-"`
	CustomerRef    string    `dynamodbav:"customer_ref"    json:"customer_ref"`
	Meter          string    `dynamodbav:"meter"           json:"meter"`
	Value          int64     `dynamodbav:"value"           json:"value"`
	OccurredAt     time.Time `dynamodbav:"occurred_at"     json:"occurred_at"`
	IdempotencyKey string    `dynamodbav:"idempotency_key" json:"idempotency_key"`
	// Previous is the level held just before this one, as the store knew it
	// when the report was written. Filled by the repository, never by a caller:
	// it is how the close recovers a carried-in level whose own report expired.
	Previous int64 `dynamodbav:"previous" json:"-"`
}

// Validate checks the record can be stored. A reference with "#" would reach
// into another key, so it is refused like a bad meter.
func (l *LevelRecord) Validate() error {
	switch {
	case l.CustomerRef == "" || strings.Contains(l.CustomerRef, "#"):
		return fmt.Errorf("%w: customer_ref is required and may not contain '#'", ErrInvalidUsage)
	case !ValidMeter(l.Meter):
		return fmt.Errorf("%w: meter %q is not a meter name", ErrInvalidUsage, l.Meter)
	case l.Value < 0:
		return fmt.Errorf("%w: level %d is negative", ErrInvalidUsage, l.Value)
	case l.OccurredAt.IsZero():
		return fmt.Errorf("%w: missing occurred_at", ErrInvalidUsage)
	case l.IdempotencyKey == "":
		return fmt.Errorf("%w: missing idempotency key", ErrInvalidUsage)
	}
	return nil
}

// Date is the São Paulo civil date the level was reached on.
func (l *LevelRecord) Date() brcal.Date { return brcal.FromTime(l.OccurredAt) }

// MaxLevel is the highest level held during period: the level carried in from
// before its start, or any level reported inside it (spec § 6.3). A month with
// no change bills the carried-in level; a peak mid-month is billed even if the
// level dropped again. Records outside the period are ignored.
func MaxLevel(records []LevelRecord, carriedIn int64, period Period) int64 {
	level := carriedIn
	for i := range records {
		if period.Contains(records[i].Date()) && records[i].Value > level {
			level = records[i].Value
		}
	}
	return level
}
