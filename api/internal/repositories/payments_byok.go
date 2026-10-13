package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// LookupByokOpenPK is the sparse lookup partition of a tenant's open BYOK
// attempts (spec 2026-10-11 § 3.6, § 5 step 1): on the attempt row while it is
// PENDING, removed by its terminal transition. Disconnect counts it and the
// hourly sweep walks it, each with one key-condition query.
func LookupByokOpenPK(organizationID string, livemode bool) string {
	return Mode(livemode) + "#BYOKOPEN#" + organizationID
}

// LookupExcessPK resolves an excess payment row by its id (the refund route).
func LookupExcessPK(livemode bool, excessID string) string {
	return Mode(livemode) + "#EXCESS#" + excessID
}

// ExcessSK nests an excess payment under its invoice, keyed by the payment
// id so a second record of the same payment is refused.
func ExcessSK(invoiceID, paymentID string) string {
	return InvoiceSK(invoiceID) + "#EXCESS#" + paymentID
}

// ChargeRef is the CHARGEREF# pointer (spec § 12.2): which tenant, invoice
// and attempt a provider charge reference belongs to.
type ChargeRef struct {
	Ref                   string `dynamodbav:"ref"`
	Provider              string `dynamodbav:"provider"`
	Livemode              bool   `dynamodbav:"livemode"`
	TenantID              string `dynamodbav:"tenant_id"`
	AccountOrganizationID string `dynamodbav:"account_organization_id"`
	InvoiceID             string `dynamodbav:"invoice_id"`
	AttemptID             string `dynamodbav:"attempt_id"`
	AttemptNumber         int    `dynamodbav:"attempt_number"`
}

type chargeRefRow struct {
	keys
	ChargeRef
}

type excessRow struct {
	keys
	billing.ExcessPayment
	LookupPK string `dynamodbav:"lookup_pk"`
}

// CreateByokAttempt writes a BYOK attempt and its CHARGEREF# pointer in one
// transaction, before the provider is called (plan scope decision 2: the txid
// is ours, so a crash after the provider created the charge leaves a row the
// retry completes). Both are create-only; a taken attempt number is
// ErrAttemptExists.
//
// A live attempt also adds one to its tenant's charge counter of the month
// (part C). limit >= 0 is the plan's refusing quota: past it the whole write
// is refused with billing.ErrByokQuotaExceeded; limit < 0 only counts.
func (r *PaymentRepository) CreateByokAttempt(ctx context.Context, a *billing.PaymentAttempt, accountOrgID string, limit int64, now time.Time) error {
	if a.Method != billing.MethodBYOK || a.ChargeRef == "" || a.Provider == "" {
		return fmt.Errorf("repositories: a BYOK attempt is recorded with its provider and charge reference")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now.UTC()
	}
	item, err := Encode(paymentAttemptRow{
		keys:           newKeys(TenantPK(a.OrganizationID, a.Livemode), PaymentAttemptSK(a.InvoiceID, a.AttemptNumber), RetentionPaymentAttempt, now),
		PeriodAttrs:    NewPeriodAttrs(a.OrganizationID, a.Livemode, EntityPaymentAttempt, brcal.FromTime(now), a.ID),
		PaymentAttempt: *a,
		LookupPK:       LookupByokOpenPK(a.OrganizationID, a.Livemode),
	})
	if err != nil {
		return err
	}
	ptr, err := Encode(chargeRefRow{
		keys: newKeys(ChargeRefPK(a.Provider, a.Livemode, a.ChargeRef), "CHARGEREF", RetentionPaymentAttempt, now),
		ChargeRef: ChargeRef{Ref: a.ChargeRef, Provider: a.Provider, Livemode: a.Livemode, TenantID: a.OrganizationID,
			AccountOrganizationID: accountOrgID, InvoiceID: a.InvoiceID, AttemptID: a.ID, AttemptNumber: a.AttemptNumber},
	})
	if err != nil {
		return err
	}
	writes := txItems(r.base.BuildPutTxItemIfAbsent(item), r.byok.BuildPutTxItemIfAbsent(ptr))
	if a.Livemode {
		writes = append(writes, r.chargeCounterItem(a.OrganizationID, ChargeMonth(a.CreatedAt), 1, limit, now))
	}
	err = r.base.TransactWrite(ctx, writes)
	if codes := cancellationCodes(err); len(codes) > 2 && codes[2] == codeConditionFailed {
		return fmt.Errorf("%w: tenant %s", billing.ErrByokQuotaExceeded, a.OrganizationID)
	}
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s attempt %d", ErrAttemptExists, a.InvoiceID, a.AttemptNumber)
	}
	return conflictErr(err)
}

// ChargeRef reads the pointer of a provider charge reference.
func (r *PaymentRepository) ChargeRef(ctx context.Context, provider string, livemode bool, ref string) (*ChargeRef, error) {
	if ref == "" {
		return nil, fmt.Errorf("%w: empty charge reference", ErrNotFound)
	}
	item, err := r.byok.GetItem(ctx, ChargeRefPK(provider, livemode, ref), "CHARGEREF")
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: charge reference", ErrNotFound)
	}
	row, err := Decode[chargeRefRow](item)
	if err != nil {
		return nil, err
	}
	if row.TenantID == "" || row.Livemode != livemode {
		return nil, fmt.Errorf("%w: charge reference", ErrNotFound)
	}
	return &row.ChargeRef, nil
}

// GetAttempt reads one attempt, strongly consistent: the confirmation path
// decides on its status.
func (r *PaymentRepository) GetAttempt(ctx context.Context, organizationID string, livemode bool, invoiceID string, attemptNumber int) (*billing.PaymentAttempt, error) {
	out, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk = :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": str(TenantPK(organizationID, livemode)), ":sk": str(PaymentAttemptSK(invoiceID, attemptNumber)),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, fmt.Errorf("%w: attempt %s #%d", ErrNotFound, invoiceID, attemptNumber)
	}
	row, err := Decode[paymentAttemptRow](out.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.PaymentAttempt, nil
}

// SetByokCharge records the provider's artefact once the charge exists.
func (r *PaymentRepository) SetByokCharge(ctx context.Context, a *billing.PaymentAttempt, payable string, due, until brcal.Date, now time.Time) error {
	sk := PaymentAttemptSK(a.InvoiceID, a.AttemptNumber)
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(TenantPK(a.OrganizationID, a.Livemode), &sk,
		"SET payable = :p, due_date = :d, payable_until = :u, updated_at = :now", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":p": str(payable), ":d": str(due.String()), ":u": str(until.String()),
			":now": str(now.UTC().Format(time.RFC3339Nano))})))
	if err != nil {
		return conflictErr(err)
	}
	a.Payable, a.DueDate, a.PayableUntil = payable, due, until
	return nil
}

// TouchChecked stamps the last re-query (the sweep's cadence reads it).
func (r *PaymentRepository) TouchChecked(ctx context.Context, a *billing.PaymentAttempt, now time.Time) error {
	sk := PaymentAttemptSK(a.InvoiceID, a.AttemptNumber)
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": str(TenantPK(a.OrganizationID, a.Livemode)), "sk": str(sk)},
		UpdateExpression:          aws.String("SET checked_at = :c"),
		ConditionExpression:       aws.String("attribute_exists(pk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":c": str(now.UTC().Format(time.RFC3339Nano))},
	})
	if err == nil {
		a.CheckedAt = now.UTC()
	}
	return err
}

// OpenByok pages through a tenant's PENDING BYOK attempts (the sparse lookup
// partition), oldest first.
func (r *PaymentRepository) OpenByok(ctx context.Context, organizationID string, livemode bool, start map[string]types.AttributeValue, limit int) (*Page[billing.PaymentAttempt], error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexLookup, PKField: "lookup_pk", PK: LookupByokOpenPK(organizationID, livemode),
		Limit: limit, ExclusiveStartKey: start, ScanIndexForward: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[paymentAttemptRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.PaymentAttempt, 0, len(rows))
	for _, row := range rows {
		if row.OrganizationID == organizationID && row.Status == billing.AttemptPending {
			out = append(out, row.PaymentAttempt)
		}
	}
	return &Page[billing.PaymentAttempt]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// RecordExcess writes an excess payment row, create-only per payment id:
// created=false when this payment was recorded before.
func (r *PaymentRepository) RecordExcess(ctx context.Context, e *billing.ExcessPayment, now time.Time) (bool, error) {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now.UTC()
	}
	item, err := Encode(excessRow{keys: newKeys(TenantPK(e.OrganizationID, e.Livemode), ExcessSK(e.InvoiceID, e.PaymentID), RetentionPaymentAttempt, now),
		ExcessPayment: *e, LookupPK: LookupExcessPK(e.Livemode, e.ID)})
	if err != nil {
		return false, err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, conflictErr(err)
}

// ListExcess returns an invoice's excess payments.
func (r *PaymentRepository) ListExcess(ctx context.Context, organizationID string, livemode bool, invoiceID string) ([]billing.ExcessPayment, error) {
	res, err := r.base.Query(ctx, QueryOpts{PK: TenantPK(organizationID, livemode), SKPrefix: InvoiceSK(invoiceID) + "#EXCESS#",
		ScanIndexForward: true, Limit: 100})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[excessRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.ExcessPayment, len(rows))
	for i := range rows {
		out[i] = rows[i].ExcessPayment
	}
	return out, nil
}

// GetExcess reads an excess payment of this tenant by id (another tenant's
// row is not found).
func (r *PaymentRepository) GetExcess(ctx context.Context, organizationID string, livemode bool, excessID string) (*billing.ExcessPayment, error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexLookup, PKField: "lookup_pk", PK: LookupExcessPK(livemode, excessID), Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, fmt.Errorf("%w: excess payment %s", ErrNotFound, excessID)
	}
	row, err := Decode[excessRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	if row.OrganizationID != organizationID {
		return nil, fmt.Errorf("%w: excess payment %s", ErrNotFound, excessID)
	}
	// The index is eventually consistent: the decision reads the base row.
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), ExcessSK(row.InvoiceID, row.PaymentID))
	if err != nil || item == nil {
		return &row.ExcessPayment, err
	}
	fresh, err := Decode[excessRow](item)
	if err != nil {
		return nil, err
	}
	return &fresh.ExcessPayment, nil
}

// SetExcessStatus moves an excess row, conditional on the status it was read
// with (two presses of "Devolver" refund once).
func (r *PaymentRepository) SetExcessStatus(ctx context.Context, e *billing.ExcessPayment, status, refundID string, now time.Time) error {
	sk := ExcessSK(e.InvoiceID, e.PaymentID)
	expr := "SET #st = :to, updated_at = :now"
	vals := map[string]types.AttributeValue{":to": str(status), ":from": str(e.Status), ":now": str(now.UTC().Format(time.RFC3339Nano))}
	remove := ""
	if status == billing.ExcessRefunding {
		// The sweep follows refunds in flight through a sparse cross-tenant
		// partition (ADR 0002: cmd/byok only), left on a terminal status.
		expr += ", schedule_pk = :spk, schedule_sk = :ssk"
		vals[":spk"], vals[":ssk"] = str(refundingPK(e.Livemode)), str(e.ID)
	} else {
		remove = " REMOVE schedule_pk, schedule_sk"
	}
	if refundID != "" {
		expr += ", refund_id = :rf"
		vals[":rf"] = str(refundID)
	}
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(TenantPK(e.OrganizationID, e.Livemode), &sk, expr+remove,
		"attribute_exists(pk) AND #st = :from", map[string]string{"#st": "status"}, vals)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: excess payment %s is no longer %s", ErrConcurrentModification, e.ID, e.Status)
	}
	if err != nil {
		return conflictErr(err)
	}
	e.Status = status
	if refundID != "" {
		e.RefundID = refundID
	}
	return nil
}

// SetByokAmount records a credit note's new amount on the open charge's
// attempt, after the provider accepted it (spec § 7.3).
func (r *PaymentRepository) SetByokAmount(ctx context.Context, a *billing.PaymentAttempt, amount billing.Cents, now time.Time) error {
	sk := PaymentAttemptSK(a.InvoiceID, a.AttemptNumber)
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(TenantPK(a.OrganizationID, a.Livemode), &sk,
		"SET amount = :a, updated_at = :now", "attribute_exists(pk) AND #st = :pending", map[string]string{"#st": "status"},
		map[string]types.AttributeValue{":a": numberValue(int64(amount)), ":pending": str(string(billing.AttemptPending)),
			":now": str(now.UTC().Format(time.RFC3339Nano))})))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: attempt %s is no longer pending", ErrConcurrentModification, a.ID)
	}
	if err != nil {
		return conflictErr(err)
	}
	a.Amount = amount
	return nil
}

func refundingPK(livemode bool) string { return Mode(livemode) + "#" + JobByokRefunding }

// JobByokRefunding names the partition of refunds still in flight.
const JobByokRefunding = "BYOK_REFUNDING"

// Refunding pages through every tenant's refunds in flight (cmd/byok only).
func (r *PaymentRepository) Refunding(ctx context.Context, livemode bool, start map[string]types.AttributeValue, limit int) (*Page[billing.ExcessPayment], error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: refundingPK(livemode), Limit: limit, ExclusiveStartKey: start, ScanIndexForward: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[excessRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.ExcessPayment, len(rows))
	for i := range rows {
		out[i] = rows[i].ExcessPayment
	}
	return &Page[billing.ExcessPayment]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// GetExcessByPayment reads an invoice's excess row by its payment id (for a
// credit refund, the credit note's id), strongly consistent.
func (r *PaymentRepository) GetExcessByPayment(ctx context.Context, organizationID string, livemode bool, invoiceID, paymentID string) (*billing.ExcessPayment, error) {
	out, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("pk = :pk AND sk = :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": str(TenantPK(organizationID, livemode)), ":sk": str(ExcessSK(invoiceID, paymentID))},
		ConsistentRead:            aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, fmt.Errorf("%w: excess payment", ErrNotFound)
	}
	row, err := Decode[excessRow](out.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.ExcessPayment, nil
}
