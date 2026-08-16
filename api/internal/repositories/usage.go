package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrDuplicateUsage reports a usage record whose idempotency key was already
// used. Callers should treat it as success: the reporting product retried, and
// the consumption is already counted.
var ErrDuplicateUsage = fmt.Errorf("usage record already reported")

// UsageRepository stores metered consumption.
//
// It is the one entity with unbounded per-tenant write volume, so it lives in a
// sub-partition keyed by subscription item and period (ADR 0002): closing a
// period reads exactly one partition instead of sweeping the tenant's.
type UsageRepository struct {
	base Base
}

func NewUsageRepository(db *dynamodb.Client, cfg *config.Config) *UsageRepository {
	return &UsageRepository{base: NewBase(db, cfg, TableUsage)}
}

// Append records consumption, exactly once per idempotency key.
//
// The sort key **is** the idempotency key, so deduplication is a property of the
// primary key rather than a check that can race. A retried report fails its
// create-only condition; it does not add a second unit of consumption. This is
// the difference between an overcharge the customer finds and a retry that costs
// nothing.
func (r *UsageRepository) Append(ctx context.Context, u *billing.UsageRecord, periodStart brcal.Date, now time.Time) error {
	if err := u.Validate(); err != nil {
		return err
	}
	item, err := Encode(usageRow{
		keys: newKeys(
			UsagePK(u.OrganizationID, u.Livemode, u.SubscriptionItemID, periodStart),
			u.IdempotencyKey,
			RetentionUsageRecord,
			now,
		),
		UsageRecord: *u,
	})
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrDuplicateUsage, u.IdempotencyKey)
	}
	return err
}

// ListForPeriod returns every record in a closed period, reading one partition.
//
// It pages internally rather than exposing a cursor: the caller is the period
// close, which needs the whole period or none of it — a partial total would
// produce an invoice that undercharges and looks correct.
func (r *UsageRepository) ListForPeriod(
	ctx context.Context,
	organizationID string,
	livemode bool,
	subscriptionItemID string,
	periodStart brcal.Date,
) ([]billing.UsageRecord, error) {
	pk := UsagePK(organizationID, livemode, subscriptionItemID, periodStart)
	var out []billing.UsageRecord
	var startKey map[string]types.AttributeValue

	for {
		res, err := r.base.Query(ctx, QueryOpts{
			PK:                pk,
			Limit:             1000,
			ScanIndexForward:  true,
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[usageRow](res.Items)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.UsageRecord)
		}
		if res.LastEvaluatedKey == nil {
			return out, nil
		}
		startKey = res.LastEvaluatedKey
	}
}
