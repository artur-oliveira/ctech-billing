package repositories

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrAlreadyGenerated reports that the period already has an invoice. The
// scheduler treats it as success: re-running the daily sweep must be a no-op,
// not an error page.
var ErrAlreadyGenerated = errors.New("invoice already generated for this period")

// numberingAttempts bounds the optimistic retry on invoice numbering.
//
// Contention only happens when several invoices of the same organization are
// finalized in the same instant — the daily sweep doing exactly that is the
// realistic case, which is why the bound is generous and the retries back off.
// Without a backoff, contending writers re-read the same value in lockstep and
// keep colliding; the jitter is what breaks the convoy.
const (
	numberingAttempts = 12
	numberingBackoff  = 3 * time.Millisecond
)

// InvoiceRepository stores invoices, their lines and their payment attempts.
type InvoiceRepository struct {
	base   Base
	audit  Base
	events Base
}

func NewInvoiceRepository(db *dynamodb.Client, cfg *config.Config) *InvoiceRepository {
	return &InvoiceRepository{
		base:   NewBase(db, cfg, TableInvoices),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
	}
}

// Create writes a DRAFT invoice, its lines and the generation marker **in one
// transaction**.
//
// The generation marker is what makes the daily sweep safe to re-run: it is
// written create-only under the {subscription_item_id}:{period_start} key, so a
// second sweep on the same day fails the condition and creates nothing. Putting
// it in the same transaction as the invoice is the point — a marker written
// separately could survive a failed invoice write and block the period forever.
func (r *InvoiceRepository) Create(
	ctx context.Context,
	inv *billing.Invoice,
	items []billing.InvoiceItem,
	generationKey string,
	now time.Time,
) error {
	if inv.Status != billing.InvoiceDraft {
		return fmt.Errorf("an invoice is created as DRAFT, not %s", inv.Status)
	}
	if err := inv.Metadata.Validate(); err != nil {
		return err
	}

	pk := TenantPK(inv.OrganizationID, inv.Livemode)
	writes := make([]types.TransactWriteItem, 0, len(items)+2)

	subscriptionLookup := ""
	if inv.SubscriptionID != "" {
		subscriptionLookup = LookupSubscriptionInvoicesPK(inv.OrganizationID, inv.Livemode, inv.SubscriptionID)
	}

	invItem, err := Encode(invoiceRow{
		keys:          newKeys(pk, InvoiceSK(inv.ID), RetentionInvoice, now),
		PeriodAttrs:   NewPeriodAttrs(inv.OrganizationID, inv.Livemode, EntityInvoice, inv.Period.Start, inv.ID),
		Invoice:       *inv,
		GenerationKey: generationKey,
		LookupPK:      subscriptionLookup,
	})
	if err != nil {
		return err
	}
	writes = append(writes, r.base.BuildPutTxItemIfAbsent(invItem))

	for i, line := range items {
		lineItem, err := Encode(invoiceItemRow{
			keys:        newKeys(pk, InvoiceItemSK(inv.ID, i), RetentionInvoiceItem, now),
			InvoiceItem: line,
			InvoiceID:   inv.ID,
			Line:        i,
		})
		if err != nil {
			return err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(lineItem))
	}

	if generationKey != "" {
		marker, err := Encode(generationRow{
			keys:          newKeys(LookupInvoiceGenerationPK(inv.OrganizationID, inv.Livemode, generationKey), "GEN", RetentionInvoice, now),
			InvoiceID:     inv.ID,
			GenerationKey: generationKey,
		})
		if err != nil {
			return err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(marker))
	}

	err = r.base.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrAlreadyGenerated, generationKey)
	}
	return err
}

// Get reads an invoice.
func (r *InvoiceRepository) Get(ctx context.Context, organizationID string, livemode bool, invoiceID string) (*billing.Invoice, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), InvoiceSK(invoiceID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: invoice %s", ErrNotFound, invoiceID)
	}
	row, err := Decode[invoiceRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Invoice, nil
}

// ListItems returns an invoice's lines in order.
func (r *InvoiceRepository) ListItems(ctx context.Context, organizationID string, livemode bool, invoiceID string) ([]billing.InvoiceItem, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:               TenantPK(organizationID, livemode),
		SKPrefix:         InvoiceSK(invoiceID) + "#LINE#",
		ScanIndexForward: true,
		Limit:            100,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[invoiceItemRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.InvoiceItem, len(rows))
	for i, row := range rows {
		out[i] = row.InvoiceItem
	}
	return out, nil
}

// Finalize assigns the sequential number and moves DRAFT to OPEN.
//
// Numbering is **gapless**, not merely unique: the counter is advanced in the
// same transaction that finalizes the invoice, guarded by the value that was
// read. Two simultaneous finalizations make one of them fail its condition and
// retry with the next number, rather than both taking the same one or one of
// them burning a number that no invoice ever carries.
//
// The cheaper alternative — increment the counter, then write the invoice — is
// what most implementations do, and it produces gaps whenever the second write
// fails. Gaps in commercial numbering are the kind of thing an accountant asks
// about years later, and by then the answer is unrecoverable.
//
// A zero `settlementSweep` arms no dunning; see the comment on `set` below.
func (r *InvoiceRepository) Finalize(
	ctx context.Context,
	inv *billing.Invoice,
	dueDate brcal.Date,
	settlementSweep brcal.Date,
	actor, requestID string,
	now time.Time,
) ([]billing.EventType, error) {
	updated := *inv
	events, err := updated.Transition(billing.InvoiceOpen, billing.CauseScheduler)
	if err != nil {
		return nil, err
	}
	updated.DueDate = dueDate

	pk := TenantPK(inv.OrganizationID, inv.Livemode)
	counterSK := InvoiceCounterSK(inv.Period.Start.Year)

	for attempt := range numberingAttempts {
		if attempt > 0 {
			// Exponential backoff with jitter, so contending finalizers stop
			// re-reading the same counter value in lockstep.
			delay := numberingBackoff << min(attempt, 6)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay/2 + time.Duration(rand.Int64N(int64(delay)))):
			}
		}
		current, err := r.currentNumber(ctx, pk, counterSK)
		if err != nil {
			return nil, err
		}
		next := current + 1
		updated.Number = next

		counterUpdate := r.buildCounterAdvance(pk, counterSK, current, next, now)

		set := map[string]types.AttributeValue{
			"number":   &types.AttributeValueMemberN{Value: strconv.FormatInt(next, 10)},
			"due_date": &types.AttributeValueMemberS{Value: dueDate.String()},
		}
		// A zero settlement date means "do not chase this one" — a free plan's
		// invoice, which is PAID before anyone could be reminded of it. The keys
		// are simply not written, which keeps the schedule index sparse: an
		// invoice that owes nothing never appears in the dunning partition at all,
		// rather than appearing and being filtered out every morning.
		if !settlementSweep.IsZero() {
			set["schedule_pk"] = &types.AttributeValueMemberS{Value: SchedulePK(inv.Livemode, JobInvoiceSettlement, settlementSweep)}
			set["schedule_sk"] = &types.AttributeValueMemberS{Value: inv.ID}
		}

		change := StatusChange{
			OrganizationID: inv.OrganizationID,
			Livemode:       inv.Livemode,
			PK:             pk,
			SK:             InvoiceSK(inv.ID),
			From:           string(billing.InvoiceDraft),
			To:             string(billing.InvoiceOpen),
			Set:            set,
			Audit: AuditEntry{
				Entity:    EntityInvoice,
				EntityID:  inv.ID,
				Action:    string(events[0]),
				Cause:     billing.CauseScheduler,
				Actor:     actor,
				RequestID: requestID,
			},
			// The events the domain says this transition produced, queued in the
			// same transaction. Not a second list maintained here: a state machine
			// that names its events and a repository that re-derives them are two
			// answers to one question.
			Emit:    events,
			Subject: invoiceSubject(inv),
		}

		err = commitWithExtraWrites(ctx, r.tables(), change, now, counterUpdate)
		if err == nil {
			*inv = updated
			return events, nil
		}
		if !errors.Is(err, ErrConcurrentModification) {
			return nil, err
		}
		// The condition that failed was either the counter or the invoice's
		// status, and DynamoDB does not say which through this API. Re-reading the
		// invoice separates them: if it is no longer DRAFT, someone else finalized
		// or voided it and retrying would be wrong.
		fresh, getErr := r.Get(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
		if getErr != nil {
			return nil, getErr
		}
		if fresh.Status != billing.InvoiceDraft {
			return nil, fmt.Errorf("%w: invoice %s is %s, not DRAFT", ErrConcurrentModification, inv.ID, fresh.Status)
		}
	}
	return nil, fmt.Errorf("%w: invoice numbering contended %d times", ErrConcurrentModification, numberingAttempts)
}

// currentNumber reads the organization's counter for a year. An absent counter
// is zero, so the first invoice of a year is number 1 with no bootstrap step.
//
// The attribute is last_number, deliberately not `seq`: `seq` is a key attribute
// of the period index and is declared as a string there, so writing a number
// into it makes DynamoDB reject the whole transaction with a ValidationError.
// That collision is invisible in a unit test with a fake — it is the reason this
// layer has integration tests at all.
func (r *InvoiceRepository) currentNumber(ctx context.Context, pk, counterSK string) (int64, error) {
	item, err := r.base.GetItem(ctx, pk, counterSK)
	if err != nil {
		return 0, err
	}
	if item == nil {
		return 0, nil
	}
	av, ok := item["last_number"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	return strconv.ParseInt(av.Value, 10, 64)
}

// buildCounterAdvance moves the counter from current to next, but only if it is
// still at current.
func (r *InvoiceRepository) buildCounterAdvance(pk, counterSK string, current, next int64, now time.Time) types.TransactWriteItem {
	sk := counterSK
	return r.base.BuildRawUpdateTxItem(
		pk, &sk,
		"SET #seq = :next, #ua = :now",
		"attribute_not_exists(#seq) OR #seq = :current",
		map[string]string{"#seq": "last_number", "#ua": "updated_at"},
		map[string]types.AttributeValue{
			":next":    &types.AttributeValueMemberN{Value: strconv.FormatInt(next, 10)},
			":current": &types.AttributeValueMemberN{Value: strconv.FormatInt(current, 10)},
			":now":     &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		},
	)
}

// Transition applies any other invoice state change, with its audit entry.
//
// An invoice that stops being collectable leaves the settlement sweep — the same
// sparse-index discipline as subscriptions, for the same reason.
func (r *InvoiceRepository) Transition(
	ctx context.Context,
	inv *billing.Invoice,
	to billing.InvoiceStatus,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) ([]billing.EventType, error) {
	updated := *inv
	events, err := updated.Transition(to, cause)
	if err != nil {
		return nil, err
	}

	set := map[string]types.AttributeValue{}
	var remove []string

	if updated.AttemptCount != inv.AttemptCount {
		set["attempt_count"] = &types.AttributeValueMemberN{Value: strconv.Itoa(updated.AttemptCount)}
	}
	if to == billing.InvoicePaid {
		set["amount_paid"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(int64(updated.Total), 10)}
		updated.AmountPaid = updated.Total
	}
	if to != billing.InvoiceOpen {
		remove = append(remove, "schedule_pk", "schedule_sk")
	}

	change := StatusChange{
		OrganizationID: inv.OrganizationID,
		Livemode:       inv.Livemode,
		PK:             TenantPK(inv.OrganizationID, inv.Livemode),
		SK:             InvoiceSK(inv.ID),
		From:           string(inv.Status),
		To:             string(to),
		Set:            set,
		Remove:         remove,
		Audit: AuditEntry{
			Entity:    EntityInvoice,
			EntityID:  inv.ID,
			Action:    string(events[0]),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
		},
		Emit:    events,
		Subject: invoiceSubject(inv),
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return nil, err
	}
	*inv = updated
	return events, nil
}

// ListByCustomer returns one customer's invoices, newest first.
//
// It reads the tenant partition and filters, rather than adding an index. The
// portal opens this for one person at a time and a person's own invoice history
// is small; the period index answers the tenant-wide question, which is the one
// that grows. If this ever becomes a hot path the fix is an index — never a Scan.
//
// The `#LINE#` and `#ATTEMPT#` rows share the INVOICE# prefix and carry no
// customer, so the filter already excludes them; the status check below is the
// belt to that braces.
func (r *InvoiceRepository) ListByCustomer(
	ctx context.Context,
	organizationID string,
	livemode bool,
	customerID string,
	limit int,
) ([]billing.Invoice, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:          TenantPK(organizationID, livemode),
		SKPrefix:    skInvoice,
		FilterField: "customer_id",
		FilterValue: customerID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[invoiceRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Invoice, 0, len(rows))
	for _, row := range rows {
		if row.Invoice.Status == "" {
			continue
		}
		out = append(out, row.Invoice)
	}
	return out, nil
}

// ListBySubscription returns a subscription's most recent invoices, newest
// first.
//
// It answers exactly one screen — the subscription's own detail — and it is
// capped rather than paginated on purpose. "The last few charges on this plan"
// is context for the plan; a customer who wants the whole history is asking for
// the invoice list, which already exists and already paginates.
//
// The lookup partition is sparse, so a subscription that has never been invoiced
// returns empty rather than scanning. Descending because the index sorts on the
// invoice's ULID and the newest is the one that matters.
func (r *InvoiceRepository) ListBySubscription(
	ctx context.Context,
	organizationID string,
	livemode bool,
	subscriptionID string,
	limit int,
) ([]billing.Invoice, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupSubscriptionInvoicesPK(organizationID, livemode, subscriptionID),
		SKPrefix:  skInvoice,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[invoiceRow](res.Items)
	if err != nil {
		return nil, err
	}
	// The `#LINE#` rows share the INVOICE# prefix but carry no lookup_pk, so
	// the sparse index already excludes them. The status check is the belt to
	// that braces, as it is in ListByCustomer.
	out := make([]billing.Invoice, 0, len(rows))
	for _, row := range rows {
		if row.Invoice.Status == "" {
			continue
		}
		out = append(out, row.Invoice)
	}
	return out, nil
}

// ListByMonth returns the tenant's invoices for a calendar month, newest first.
//
// This is the period index doing the job it exists for. The month is a prefix of
// the composite sort key, so "everything in March" and "everything in 2026" are
// the same query with one fewer condition — and neither is a scan.
func (r *InvoiceRepository) ListByMonth(
	ctx context.Context,
	organizationID string,
	livemode bool,
	year, month int,
	limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.Invoice], error) {
	return pagePeriod(ctx, r.base, organizationID, livemode, EntityInvoice,
		PeriodPrefix(year, month, 0), limit, startKey,
		func(row invoiceRow) billing.Invoice { return row.Invoice })
}

// DueForDunning returns the invoices whose next dunning step falls on a date.
//
// It reads the same sparse partition Finalize arms and Transition disarms, so
// the queue is "bills nobody has paid" rather than "every invoice ever issued".
// Cross-tenant, like every other sweep, and for the same reason: an unpaid
// invoice is unpaid in whichever tenant it belongs to.
func (r *InvoiceRepository) DueForDunning(
	ctx context.Context,
	livemode bool,
	due brcal.Date,
	limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.Invoice], error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName:         IndexSchedule,
		PKField:           "schedule_pk",
		SKField:           "schedule_sk",
		PK:                SchedulePK(livemode, JobInvoiceSettlement, due),
		Limit:             limit,
		ExclusiveStartKey: startKey,
		ScanIndexForward:  true,
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

// AdvanceDunning records that a step was performed and schedules the next one.
//
// The write is conditional on the step the caller acted on, so two jobs running
// the same day — which is what a second instance is — cannot both send the same
// reminder. The loser's condition fails and it moves on.
//
// With no next step the invoice leaves the queue. It stays OPEN: the policy has
// run out of things to do, not the customer out of the right to pay. Whoever
// wants it closed calls Transition, which is a decision with an actor.
func (r *InvoiceRepository) AdvanceDunning(ctx context.Context, inv *billing.Invoice, performed int, reminded bool, now time.Time) error {
	next := performed + 1
	names := map[string]string{"#ds": "dunning_step", "#ua": "updated_at"}
	values := map[string]types.AttributeValue{
		":next": &types.AttributeValueMemberN{Value: strconv.Itoa(next)},
		":cur":  &types.AttributeValueMemberN{Value: strconv.Itoa(performed)},
		":now":  &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	sets := []string{"#ds = :next", "#ua = :now"}
	var removes []string

	if reminded {
		names["#lr"] = "last_reminded_at"
		sets = append(sets, "#lr = :now")
	}

	if date, ok := billing.DunningDate(inv.DueDate, next); ok {
		names["#spk"], names["#ssk"] = "schedule_pk", "schedule_sk"
		values[":spk"] = &types.AttributeValueMemberS{Value: SchedulePK(inv.Livemode, JobInvoiceSettlement, date)}
		values[":ssk"] = &types.AttributeValueMemberS{Value: inv.ID}
		sets = append(sets, "#spk = :spk", "#ssk = :ssk")
	} else {
		names["#spk"], names["#ssk"] = "schedule_pk", "schedule_sk"
		removes = append(removes, "#spk", "#ssk")
	}

	expr := "SET " + strings.Join(sets, ", ")
	if len(removes) > 0 {
		expr += " REMOVE " + strings.Join(removes, ", ")
	}

	// `attribute_not_exists` covers an invoice finalized before dunning existed,
	// whose step attribute was never written. Without it every such invoice would
	// be skipped forever, silently.
	condition := "(attribute_not_exists(#ds) AND :cur = :zero) OR #ds = :cur"
	values[":zero"] = &types.AttributeValueMemberN{Value: "0"}

	sk := InvoiceSK(inv.ID)
	err := r.base.TransactWrite(ctx, txItems(r.base.BuildRawUpdateTxItem(
		TenantPK(inv.OrganizationID, inv.Livemode), &sk,
		expr, condition, names, values,
	)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: invoice %s is no longer at dunning step %d", ErrConcurrentModification, inv.ID, performed)
	}
	if err == nil {
		inv.DunningStep = next
	}
	return err
}

// tables is the set every transition in this repository writes across.
func (r *InvoiceRepository) tables() Tables {
	return Tables{Rows: r.base, Audit: r.audit, Events: r.events}
}
