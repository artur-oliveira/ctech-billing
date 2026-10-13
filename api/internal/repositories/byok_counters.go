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
