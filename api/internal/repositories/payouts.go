package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// Payouts live in the tenant's partition beside its payment attempts. A marker
// item per reference, written in the same transaction as the payout, is what
// makes a repeated reference return the first payout (create-only both).

func PayoutSK(id string) string { return "PAYOUT#" + id }

func LookupPayoutReferencePK(organizationID string, livemode bool, reference string) string {
	return TenantPK(organizationID, livemode) + "#PAYREF#" + reference
}

// JobByokPayouts names the partition of payouts still processing (cmd/byok).
const JobByokPayouts = "BYOK_PAYOUTS"

func payoutsProcessingPK(livemode bool) string { return Mode(livemode) + "#" + JobByokPayouts }

type payoutRow struct {
	keys
	billing.Payout
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

type payoutRefRow struct {
	keys
	PayoutID    string `dynamodbav:"payout_id"`
	RequestHash string `dynamodbav:"request_hash"`
}

// CreatePayout reserves a payout. created=false returns the payout this
// reference already named (the caller compares its request hash).
func (r *PaymentRepository) CreatePayout(ctx context.Context, p *billing.Payout, now time.Time) (*billing.Payout, bool, error) {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now.UTC()
	}
	p.UpdatedAt = p.CreatedAt
	item, err := Encode(payoutRow{keys: newKeys(TenantPK(p.OrganizationID, p.Livemode), PayoutSK(p.ID), RetentionPaymentAttempt, now),
		Payout: *p, SchedulePK: payoutsProcessingPK(p.Livemode), ScheduleSK: p.ID})
	if err != nil {
		return nil, false, err
	}
	marker, err := Encode(payoutRefRow{keys: newKeys(LookupPayoutReferencePK(p.OrganizationID, p.Livemode, p.Reference), "REF", RetentionPaymentAttempt, now),
		PayoutID: p.ID, RequestHash: p.RequestHash})
	if err != nil {
		return nil, false, err
	}
	err = r.base.TransactWrite(ctx, []types.TransactWriteItem{r.base.BuildPutTxItemIfAbsent(item), r.base.BuildPutTxItemIfAbsent(marker)})
	if IsConditionFailed(err) {
		existing, gerr := r.GetPayoutByReference(ctx, p.OrganizationID, p.Livemode, p.Reference)
		return existing, false, gerr
	}
	if err != nil {
		return nil, false, conflictErr(err)
	}
	return p, true, nil
}

func (r *PaymentRepository) GetPayout(ctx context.Context, organizationID string, livemode bool, id string) (*billing.Payout, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), PayoutSK(id))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: payout %s", ErrNotFound, id)
	}
	row, err := Decode[payoutRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Payout, nil
}

func (r *PaymentRepository) GetPayoutByReference(ctx context.Context, organizationID string, livemode bool, reference string) (*billing.Payout, error) {
	item, err := r.base.GetItem(ctx, LookupPayoutReferencePK(organizationID, livemode, reference), "REF")
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: payout reference %s", ErrNotFound, reference)
	}
	marker, err := Decode[payoutRefRow](item)
	if err != nil {
		return nil, err
	}
	return r.GetPayout(ctx, organizationID, livemode, marker.PayoutID)
}

// SetPayoutSent records the bank's own id for a payout it has taken. Idempotent.
func (r *PaymentRepository) SetPayoutSent(ctx context.Context, p *billing.Payout, providerRef string, now time.Time) error {
	sk := PayoutSK(p.ID)
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(TenantPK(p.OrganizationID, p.Livemode), &sk,
		"SET provider_ref = :r, updated_at_p = :now", "attribute_exists(pk) AND #st = :processing", map[string]string{"#st": "status"},
		map[string]types.AttributeValue{":r": str(providerRef), ":processing": str(string(billing.PayoutProcessing)),
			":now": str(now.UTC().Format(time.RFC3339Nano))})))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: payout %s is no longer processing", ErrConcurrentModification, p.ID)
	}
	if err != nil {
		return conflictErr(err)
	}
	p.ProviderRef = providerRef
	return nil
}

// SettlePayout moves a processing payout to done or failed, once: a second
// writer finds it no longer processing. The sweep stops following it.
func (r *PaymentRepository) SettlePayout(ctx context.Context, p *billing.Payout, status billing.PayoutState, paymentID, reason string, now time.Time) error {
	sk := PayoutSK(p.ID)
	expr := "SET #st = :to, updated_at_p = :now"
	vals := map[string]types.AttributeValue{":to": str(string(status)), ":processing": str(string(billing.PayoutProcessing)),
		":now": str(now.UTC().Format(time.RFC3339Nano))}
	if paymentID != "" {
		expr += ", payment_id = :pid"
		vals[":pid"] = str(paymentID)
	}
	if reason != "" {
		expr += ", failure_reason = :why"
		vals[":why"] = str(reason)
	}
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(TenantPK(p.OrganizationID, p.Livemode), &sk,
		expr+" REMOVE schedule_pk, schedule_sk", "attribute_exists(pk) AND #st = :processing", map[string]string{"#st": "status"}, vals)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: payout %s is no longer processing", ErrConcurrentModification, p.ID)
	}
	if err != nil {
		return conflictErr(err)
	}
	p.Status, p.PaymentID, p.FailureReason = status, paymentID, reason
	return nil
}

// ProcessingPayouts pages through every tenant's payouts still processing
// (cmd/byok only, ADR 0002).
func (r *PaymentRepository) ProcessingPayouts(ctx context.Context, livemode bool, start map[string]types.AttributeValue, limit int) (*Page[billing.Payout], error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: payoutsProcessingPK(livemode), Limit: limit, ExclusiveStartKey: start, ScanIndexForward: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[payoutRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Payout, len(rows))
	for i := range rows {
		out[i] = rows[i].Payout
	}
	return &Page[billing.Payout]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}
