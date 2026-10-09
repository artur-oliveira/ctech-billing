package repositories

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// The finance replay queue (spec § 3.8, 6.7 review I1). A paid invoice, and a
// credit note issued on one, is queued in the same write that makes it so, and
// leaves the queue when the posting rule has settled both spaces one way or the
// other. Without it a posting that failed after the money arrived was lost:
// wallet does not redeliver a webhook that succeeded, and the reconciler only
// looks at attempts still pending.
//
// The queue is the invoices table's schedule-index, one partition per mode and
// job, ordered by when the fact happened. Cross-tenant by design (ADR 0002):
// read only by cmd/reconcile.

// PendingFinancePostings returns paid invoices whose posting is not complete,
// oldest first.
func (r *InvoiceRepository) PendingFinancePostings(ctx context.Context, livemode bool, limit int, startKey map[string]types.AttributeValue) (*Page[billing.Invoice], error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: FinanceQueuePK(livemode, JobFinancePosting), Limit: limit, ExclusiveStartKey: startKey, ScanIndexForward: true,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[invoiceRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Invoice, len(rows))
	for i, row := range rows {
		out[i] = row.Invoice
	}
	return &Page[billing.Invoice]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// FinancePosted takes a paid invoice off the queue. Conditional on it still
// being there, so it never removes another job's key; a second call is a no-op.
func (r *InvoiceRepository) FinancePosted(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	return dequeue(ctx, r.base, TenantPK(inv.OrganizationID, inv.Livemode), InvoiceSK(inv.ID), FinanceQueuePK(inv.Livemode, JobFinancePosting), now)
}

// PendingFinanceCredits returns credit notes on paid invoices whose posting is
// not complete, oldest first.
func (r *CreditNoteRepository) PendingFinanceCredits(ctx context.Context, livemode bool, limit int, startKey map[string]types.AttributeValue) (*Page[billing.CreditNote], error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: FinanceQueuePK(livemode, JobFinanceCredit), Limit: limit, ExclusiveStartKey: startKey, ScanIndexForward: true,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[creditNoteRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.CreditNote, len(rows))
	for i, row := range rows {
		out[i] = row.CreditNote
		// The note's created_at shares its attribute name with the row's own
		// (keys), so it does not survive a decode; the queue key carries the
		// note's time, which is what the replay window is measured from.
		if at, _, ok := strings.Cut(row.ScheduleSK, "#"); ok {
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
				out[i].CreatedAt = t
			}
		}
	}
	return &Page[billing.CreditNote]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// FinanceCredited takes a credit note off the queue.
func (r *CreditNoteRepository) FinanceCredited(ctx context.Context, cn *billing.CreditNote, now time.Time) error {
	return dequeue(ctx, r.base, TenantPK(cn.OrganizationID, cn.Livemode), CreditNoteSK(cn.InvoiceID, cn.ID), FinanceQueuePK(cn.Livemode, JobFinanceCredit), now)
}

func dequeue(ctx context.Context, b Base, pk, sk, queuePK string, now time.Time) error {
	err := b.TransactWrite(ctx, txItems(b.BuildRawUpdateTxItem(pk, &sk,
		"SET updated_at = :now REMOVE schedule_pk, schedule_sk",
		"schedule_pk = :q", nil,
		map[string]types.AttributeValue{
			":q":   &types.AttributeValueMemberS{Value: queuePK},
			":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		})))
	if err != nil && onlyConditionFailed(err) {
		return nil // already off the queue
	}
	return err
}
