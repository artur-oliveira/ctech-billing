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

// CreditNoteRepository stores the corrections issued against an invoice.
//
// Credit notes hang off the invoice partition (see CreditNoteSK), so every read
// here is a prefix Query inside a tenant partition the caller already holds, and
// there is no index and no id-addressable route.
type CreditNoteRepository struct {
	base   Base
	audit  Base
	events Base
}

func NewCreditNoteRepository(db *dynamodb.Client, cfg *config.Config) *CreditNoteRepository {
	return &CreditNoteRepository{
		base:   NewBase(db, cfg, TableInvoices),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
	}
}

// Issue writes a credit note against inv, with its audit row and its event, in
// one transaction.
//
// Two things are enforced here rather than in a handler, and both are only
// enforceable at write time:
//
//   - **The total credited may never exceed the invoice total.** It is checked
//     against the notes read moments ago, and the write is conditional on the
//     invoice still being in the status that was read — so two operators
//     crediting the same invoice at once cannot both pass a check that was true
//     for each of them separately.
//   - **A credit note cannot be issued against a moving invoice.** The condition
//     on the invoice row is what makes crediting and voiding mutually exclusive:
//     whichever commits second is refused and re-reads, instead of leaving a
//     credit note attached to an invoice that says nothing is owed.
//
// It never moves money. If the customer is owed cash, wallet performs the refund
// and RefundedExternally records that it happened — billing issuing money is how
// this service would start becoming a wallet.
func (r *CreditNoteRepository) Issue(
	ctx context.Context,
	cn *billing.CreditNote,
	inv *billing.Invoice,
	actor, requestID string,
	now time.Time,
) error {
	if actor == "" {
		// Same rule as a status change: "the system did it" is not an answer
		// during a dispute, and a credit note is the document a dispute is about.
		return fmt.Errorf("repositories: a credit note needs an actor")
	}

	credited, err := r.TotalCredited(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		return err
	}
	if err := cn.ValidateAgainst(inv, credited); err != nil {
		return err
	}

	cn.OrganizationID = inv.OrganizationID
	cn.Livemode = inv.Livemode
	cn.CustomerID = inv.CustomerID
	cn.Currency = inv.Currency
	cn.CreatedBy = actor
	cn.CreatedAt = now.UTC()

	pk := TenantPK(inv.OrganizationID, inv.Livemode)
	item, err := Encode(creditNoteRow{
		keys:        newKeys(pk, CreditNoteSK(inv.ID, cn.ID), RetentionCreditNote, now),
		PeriodAttrs: NewPeriodAttrs(inv.OrganizationID, inv.Livemode, EntityCreditNote, brcal.FromTime(now), cn.ID),
		CreditNote:  *cn,
	})
	if err != nil {
		return err
	}

	// The audit row carries the credited totals rather than a status pair,
	// because nothing changed status: what a reader needs later is how much had
	// been credited before this note and how much after it.
	auditItem, err := buildAuditItem(inv.OrganizationID, inv.Livemode, AuditEntry{
		Entity:    EntityCreditNote,
		EntityID:  cn.ID,
		Action:    string(billing.EventCreditNoteCreated),
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		Before:    credited.String(),
		After:     (credited + cn.Amount).String(),
	}, "", "", now)
	if err != nil {
		return err
	}

	// The event points at the **invoice**, not at the note. A consumer reads the
	// subject back with its own credential (ADR 0016) and there is no route that
	// serves a credit note on its own — the invoice is where the correction is
	// visible, and pointing at an id nothing can fetch is a delivery that teaches
	// a consumer to ignore the type.
	event, err := buildCreationEvent(r.events, inv.OrganizationID, inv.Livemode,
		billing.EventCreditNoteCreated, invoiceSubject(inv), now)
	if err != nil {
		return err
	}

	err = r.base.TransactWrite(ctx, []types.TransactWriteItem{
		r.base.BuildPutTxItemIfAbsent(item),
		r.guardInvoice(pk, inv, now),
		r.audit.BuildPutTxItemIfAbsent(auditItem),
		event,
	})
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: invoice %s expected to be %s", ErrConcurrentModification, inv.ID, inv.Status)
	}
	return err
}

// guardInvoice is the condition that makes the credit note safe, expressed as
// the smallest write that can carry one: the invoice's updated_at moves, and the
// item is refused unless the status is still what the caller validated against.
//
// A bare ConditionCheck would say the same thing, and touching updated_at is
// worth the difference — an invoice that has been credited has changed, and a
// screen ordering by updated_at should see it.
func (r *CreditNoteRepository) guardInvoice(pk string, inv *billing.Invoice, now time.Time) types.TransactWriteItem {
	return r.base.BuildRawUpdateTxItem(
		pk, new(InvoiceSK(inv.ID)),
		"SET #ua = :now",
		"attribute_exists(pk) AND #st = :status",
		map[string]string{"#ua": "updated_at", "#st": "status"},
		map[string]types.AttributeValue{
			":now":    &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
			":status": &types.AttributeValueMemberS{Value: string(inv.Status)},
		},
	)
}

// ListByInvoice returns the credit notes issued against one invoice, oldest
// first — the order they were decided in, which is the order a timeline reads.
func (r *CreditNoteRepository) ListByInvoice(
	ctx context.Context,
	organizationID string,
	livemode bool,
	invoiceID string,
) ([]billing.CreditNote, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:               TenantPK(organizationID, livemode),
		SKPrefix:         InvoiceSK(invoiceID) + "#" + skCreditNote,
		ScanIndexForward: true,
		Limit:            100,
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
	}
	return out, nil
}

// TotalCredited is how much has already been credited against an invoice.
//
// Summed on read rather than stored on the invoice. A stored total is a second
// copy of a fact the notes already hold, and the two disagree the first time a
// write half-succeeds — while the sum is over a handful of rows in a partition
// the caller is reading anyway.
func (r *CreditNoteRepository) TotalCredited(
	ctx context.Context,
	organizationID string,
	livemode bool,
	invoiceID string,
) (billing.Cents, error) {
	notes, err := r.ListByInvoice(ctx, organizationID, livemode, invoiceID)
	if err != nil {
		return 0, err
	}
	var total billing.Cents
	for _, cn := range notes {
		total += cn.Amount
	}
	return total, nil
}
