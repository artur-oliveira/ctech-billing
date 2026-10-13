package repositories

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ChargeCounterKey is a live tenant's BYOK charge counter of one São Paulo
// month (BYOK part C, scope decision 2): a gate counter written inside the
// attempt's transaction, never a Query. What the merchant pays comes from
// usage records, not from here.
func ChargeCounterKey(tenantID, month string) (pk, sk string) {
	return "BYOKCOUNT#" + tenantID, month
}

// ChargeMonth is the counter's month ("yyyy-mm") of an instant.
func ChargeMonth(at time.Time) string { return brcal.FromTime(at).String()[:7] }

// ChargesThisMonth reads a counter; a missing row is zero.
func (r *ByokRepository) ChargesThisMonth(ctx context.Context, tenantID, month string) (int64, error) {
	pk, sk := ChargeCounterKey(tenantID, month)
	return r.counter(ctx, pk, sk)
}

// SubscriptionCounterKey counts a live BYOK-rail tenant's subscriptions that
// are not CANCELED (part C, scope decision 3).
func SubscriptionCounterKey(tenantID string) (pk, sk string) { return "BYOKSUBS#" + tenantID, "COUNT" }

// SubscriptionCount reads the counter; a missing row is zero.
func (r *ByokRepository) SubscriptionCount(ctx context.Context, tenantID string) (int64, error) {
	pk, sk := SubscriptionCounterKey(tenantID)
	return r.counter(ctx, pk, sk)
}

// InitSubscriptionCount sets the counter when a tenant moves onto the BYOK
// rail (the one count by Query in this design). It overwrites: a tenant that
// left the rail and came back is recounted.
func (r *ByokRepository) InitSubscriptionCount(ctx context.Context, tenantID string, n int64) error {
	pk, sk := SubscriptionCounterKey(tenantID)
	return r.base.PutItem(ctx, map[string]types.AttributeValue{"pk": str(pk), "sk": str(sk), "n": numberValue(n)})
}

func (r *ByokRepository) counter(ctx context.Context, pk, sk string) (int64, error) {
	item, err := r.base.GetItem(ctx, pk, sk)
	if err != nil || item == nil {
		return 0, err
	}
	n, ok := item["n"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	return strconv.ParseInt(n.Value, 10, 64)
}

// counterItem adds delta to a counter row; limit >= 0 keeps n below limit.
func counterItem(b Base, pk, sk string, delta, limit int64) types.TransactWriteItem {
	values := map[string]types.AttributeValue{":d": numberValue(delta)}
	cond := ""
	if limit >= 0 {
		cond = "attribute_not_exists(#n) OR #n < :limit"
		values[":limit"] = numberValue(limit)
	}
	return b.BuildRawUpdateTxItem(pk, &sk, "ADD #n :d", cond, map[string]string{"#n": "n"}, values)
}

// chargeCounterItem adds delta to a counter. limit >= 0 makes it conditional:
// the row's n must stay below limit, so the limit holds under concurrency.
// The row expires three months after its last write.
func (r *PaymentRepository) chargeCounterItem(tenantID, month string, delta, limit int64, now time.Time) types.TransactWriteItem {
	pk, sk := ChargeCounterKey(tenantID, month)
	values := map[string]types.AttributeValue{":d": numberValue(delta), ":ttl": numberValue(now.AddDate(0, 3, 0).Unix())}
	cond := ""
	if limit >= 0 {
		cond = "attribute_not_exists(#n) OR #n < :limit"
		values[":limit"] = numberValue(limit)
	}
	return r.byok.BuildRawUpdateTxItem(pk, &sk, "ADD #n :d SET #ttl = :ttl", cond, map[string]string{"#n": "n", "#ttl": "ttl"}, values)
}
