package repositories

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
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

// counterItem adds delta to a counter row; limit >= 0 keeps n below limit
// (so the limit holds under concurrency); ttl > 0 sets the row's expiry.
func counterItem(b Base, pk, sk string, delta, limit, ttl int64) types.TransactWriteItem {
	update := "ADD #n :d"
	names := map[string]string{"#n": "n"}
	values := map[string]types.AttributeValue{":d": numberValue(delta)}
	cond := ""
	if limit >= 0 {
		cond = "attribute_not_exists(#n) OR #n < :limit"
		values[":limit"] = numberValue(limit)
	}
	if ttl > 0 {
		update += " SET #ttl = :ttl"
		names["#ttl"], values[":ttl"] = "ttl", numberValue(ttl)
	}
	return b.BuildRawUpdateTxItem(pk, &sk, update, cond, names, values)
}

// chargeCounterItem moves a month's charge counter; the row expires three
// months after its last write.
func (r *PaymentRepository) chargeCounterItem(tenantID, month string, delta, limit int64, now time.Time) types.TransactWriteItem {
	pk, sk := ChargeCounterKey(tenantID, month)
	return counterItem(r.byok, pk, sk, delta, limit, now.AddDate(0, 3, 0).Unix())
}

// ByokUsagePK is the partition of the live BYOK charges created on one São
// Paulo day that are not reported yet (spec § 5 step 6). A row is written in
// the attempt's transaction and deleted once reported or skipped, so the job
// reads only what is left, with one key-condition Query on the base table.
func ByokUsagePK(day brcal.Date) string { return "live#BYOK_USAGE#" + day.String() }

// ByokUsageRow points at one attempt to report.
type ByokUsageRow struct {
	TenantID      string    `dynamodbav:"tenant_id"`
	InvoiceID     string    `dynamodbav:"invoice_id"`
	AttemptID     string    `dynamodbav:"attempt_id"`
	AttemptNumber int       `dynamodbav:"attempt_number"`
	CreatedAt     time.Time `dynamodbav:"attempt_created_at"`
}

type byokUsageRow struct {
	keys
	ByokUsageRow
}

func (r *PaymentRepository) usageItem(a *billing.PaymentAttempt, now time.Time) (map[string]types.AttributeValue, error) {
	// Ninety days: an unreported row outlives any catch-up, then goes.
	return Encode(byokUsageRow{keys: newKeys(ByokUsagePK(brcal.FromTime(a.CreatedAt)), a.ID, RetentionNinetyDays, now), ByokUsageRow: ByokUsageRow{TenantID: a.OrganizationID, InvoiceID: a.InvoiceID,
		AttemptID: a.ID, AttemptNumber: a.AttemptNumber, CreatedAt: a.CreatedAt.UTC()}})
}

// PendingUsage pages one day's unreported charges.
func (r *ByokRepository) PendingUsage(ctx context.Context, day brcal.Date, startKey map[string]types.AttributeValue) (*Page[ByokUsageRow], error) {
	res, err := r.base.Query(ctx, QueryOpts{PK: ByokUsagePK(day), Limit: 200, ScanIndexForward: true, ExclusiveStartKey: startKey})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[byokUsageRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := &Page[ByokUsageRow]{LastEvaluatedKey: res.LastEvaluatedKey}
	for _, row := range rows {
		out.Items = append(out.Items, row.ByokUsageRow)
	}
	return out, nil
}

// UsageDone removes a reported (or skipped) charge from its day.
func (r *ByokRepository) UsageDone(ctx context.Context, row ByokUsageRow) error {
	_, err := r.base.DeleteItem(ctx, ByokUsagePK(brcal.FromTime(row.CreatedAt)), row.AttemptID)
	return err
}
