package repositories

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// SubscriptionRepository stores recurring agreements.
type SubscriptionRepository struct {
	base   Base
	audit  Base
	events Base
}

func NewSubscriptionRepository(db *dynamodb.Client, cfg *config.Config) *SubscriptionRepository {
	return &SubscriptionRepository{
		base:   NewBase(db, cfg, TableSubscriptions),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
	}
}

// nextSweepDate is when the daily job should next look at this subscription: the
// start of the period after the current one.
func nextSweepDate(s *billing.Subscription) brcal.Date { return s.NextPeriod().Start }

// sweepable reports whether the subscription belongs in the daily renewal sweep.
//
// **ACTIVE only**, and the exclusions are deliberate rather than an oversight:
//
//   - TRIALING has no renewal to perform; what it needs is a trial-end job, and
//     that arrives with trials. Leaving it in this sweep would find rows the
//     renewal transition cannot legally move, which is a queue that never drains.
//   - PAST_DUE is dunning's problem. Generating another invoice for a customer
//     who has not paid the last one is a decision the dunning policy makes, not a
//     side effect of a date arriving.
//   - PAUSED, CANCELED and INCOMPLETE have nothing to bill.
//
// Everything this returns true for, the sweep must be able to renew. That
// invariant is what keeps the index a work queue instead of a backlog.
//
// **Known gap, for dunning (Phase 3) to close:** a subscription that recovers
// from PAST_DUE re-enters the sweep at its next period boundary, which may
// already be in the past — so the missed period is not picked up automatically.
// It needs an explicit catch-up, not a wider sweep.
func sweepable(s *billing.Subscription) bool {
	return s.Status == billing.SubscriptionActive
}

func subscriptionRowOf(s *billing.Subscription, now time.Time) subscriptionRow {
	row := subscriptionRow{
		keys:         newKeys(TenantPK(s.OrganizationID, s.Livemode), SubscriptionSK(s.ID), RetentionSubscription, now),
		PeriodAttrs:  NewPeriodAttrs(s.OrganizationID, s.Livemode, EntitySubscription, brcal.FromTime(now), s.ID),
		Subscription: *s,
	}
	row.SchedulePK, row.ScheduleSK = scheduleKeys(
		s.Livemode, JobSubscriptionDue, nextSweepDate(s), s.ID, sweepable(s),
	)
	return row
}

// Create writes a subscription and its items in one transaction.
//
// A subscription with no item cannot be billed, so writing them separately would
// leave a window in which the sweep finds a subscription it cannot invoice — and
// that window is a row that stays broken until someone notices.
func (r *SubscriptionRepository) Create(ctx context.Context, s *billing.Subscription, items []billing.SubscriptionItem, now time.Time) error {
	if err := s.Recurrence.Validate(); err != nil {
		return err
	}
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%w: a subscription needs at least one item", billing.ErrInvalidSubscriptionItem)
	}
	// There is no upper bound here on purpose. What a subscription may hold is a
	// question about prices agreeing on a cycle, which needs the prices — so it is
	// answered in Subscriber.Subscribe, where they are already read, rather than
	// re-read here to enforce a count that would mean nothing on its own.

	pk := TenantPK(s.OrganizationID, s.Livemode)
	subItem, err := Encode(subscriptionRowOf(s, now))
	if err != nil {
		return err
	}
	writes := txItems(r.base.BuildPutTxItemIfAbsent(subItem))

	for _, it := range items {
		if err := it.Validate(); err != nil {
			return err
		}
		encoded, err := Encode(subscriptionItemRow{
			keys:             newKeys(pk, SubscriptionItemSK(s.ID, it.ID), RetentionSubscription, now),
			SubscriptionItem: it,
		})
		if err != nil {
			return err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(encoded))
	}

	// `subscription.created`, in the same transaction as the subscription. A
	// creation is not a status change, so it does not pass through
	// CommitStatusChange — which means this is the one emission that has to be
	// written by hand, and the one worth checking in review.
	event, err := buildCreationEvent(r.events, s.OrganizationID, s.Livemode,
		billing.EventSubscriptionCreated, subscriptionSubject(s), now)
	if err != nil {
		return err
	}
	writes = append(writes, event)

	err = r.base.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		return fmt.Errorf("subscription %s already exists", s.ID)
	}
	return err
}

// ListItems returns a subscription's items.
//
// Items are nested under the subscription's sort key, so this is one Query on
// one partition rather than a round trip per item.
func (r *SubscriptionRepository) ListItems(ctx context.Context, organizationID string, livemode bool, subscriptionID string) ([]billing.SubscriptionItem, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:               TenantPK(organizationID, livemode),
		SKPrefix:         SubscriptionSK(subscriptionID) + "#ITEM#",
		ScanIndexForward: true,
		Limit:            50,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[subscriptionItemRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.SubscriptionItem, len(rows))
	for i, row := range rows {
		out[i] = row.SubscriptionItem
	}
	return out, nil
}

// Get reads a subscription.
func (r *SubscriptionRepository) Get(ctx context.Context, organizationID string, livemode bool, subscriptionID string) (*billing.Subscription, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), SubscriptionSK(subscriptionID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: subscription %s", ErrNotFound, subscriptionID)
	}
	row, err := Decode[subscriptionRow](item)
	if err != nil {
		return nil, err
	}
	sub := row.entity()
	return &sub, nil
}

// Transition applies a state change, writing the audit entry in the same
// transaction and keeping the sweep index in step.
//
// The domain decides whether the move is legal; this function only persists it.
// Splitting it that way is what lets every transition rule be tested without a
// database, and it means an illegal transition never reaches DynamoDB at all.
func (r *SubscriptionRepository) Transition(
	ctx context.Context,
	s *billing.Subscription,
	to billing.SubscriptionStatus,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) ([]billing.EventType, error) {
	updated := *s
	events, err := updated.Transition(to, cause)
	if err != nil {
		return nil, err
	}

	set := map[string]types.AttributeValue{}
	var remove []string

	// A renewal advances the period, so the row's next sweep date moves with it.
	if updated.PeriodIndex != s.PeriodIndex {
		set["period_index"] = &types.AttributeValueMemberN{Value: strconv.Itoa(updated.PeriodIndex)}
	}
	if sweepable(&updated) {
		set["schedule_pk"] = &types.AttributeValueMemberS{Value: SchedulePK(updated.Livemode, JobSubscriptionDue, nextSweepDate(&updated))}
		set["schedule_sk"] = &types.AttributeValueMemberS{Value: updated.ID}
	} else {
		// Leaving the sweep is a REMOVE, not a flag. A canceled subscription that
		// merely carried status=CANCELED in the index would still be read every
		// morning, forever.
		remove = append(remove, "schedule_pk", "schedule_sk")
	}

	change := StatusChange{
		OrganizationID: s.OrganizationID,
		Livemode:       s.Livemode,
		PK:             TenantPK(s.OrganizationID, s.Livemode),
		SK:             SubscriptionSK(s.ID),
		From:           string(s.Status),
		To:             string(to),
		Set:            set,
		Remove:         remove,
		Audit: AuditEntry{
			Entity:    EntitySubscription,
			EntityID:  s.ID,
			Action:    string(events[0]),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
		},
		Emit:    events,
		Subject: subscriptionSubject(&updated),
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return nil, err
	}
	*s = updated
	return events, nil
}

// ChangeItems replaces a subscription's items, in the same transaction as the
// audit row and the subscription.updated event.
//
// One transaction and not three writes, for the reason every other change here
// is one: a subscription whose items were swapped without the audit row cannot
// answer "what were they paying before this invoice", and that is the question
// asked during the argument about the invoice. The old rows are deleted rather
// than left archived — an item is what the sweep bills from, and a stale one is
// a second line on next month's invoice.
//
// `timing` and `ownerKey` come from the new prices. OwnerKey especially: it is
// copied onto the subscription at creation and routes every event it emits
// (ADR 0016), and this is the code the Subscription.OwnerKey comment names as
// the place that must recompute it.
//
// The move itself is a self-edge on the current status, so the domain decides
// whether this subscription may be changed at all: ACTIVE has the edge, and
// INCOMPLETE, PAST_DUE, PAUSED and CANCELED do not — a plan change on a
// subscription that never activated, or one being dunned, is a decision with
// consequences for money already owed, and it is refused here rather than
// half-handled.
func (r *SubscriptionRepository) ChangeItems(
	ctx context.Context,
	s *billing.Subscription,
	oldItems, newItems []billing.SubscriptionItem,
	timing billing.BillingTiming,
	ownerKey string,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) error {
	if len(newItems) == 0 {
		return fmt.Errorf("%w: a subscription needs at least one item", billing.ErrInvalidSubscriptionItem)
	}

	updated := *s
	updated.Timing = timing
	updated.OwnerKey = ownerKey
	events, err := updated.Transition(s.Status, cause)
	if err != nil {
		return err
	}

	pk := TenantPK(s.OrganizationID, s.Livemode)
	extra := make([]types.TransactWriteItem, 0, len(oldItems)+len(newItems))
	for _, it := range oldItems {
		extra = append(extra, r.base.BuildDeleteTxItem(pk, SubscriptionItemSK(s.ID, it.ID)))
	}
	for _, it := range newItems {
		if err := it.Validate(); err != nil {
			return err
		}
		encoded, err := Encode(subscriptionItemRow{
			keys:             newKeys(pk, SubscriptionItemSK(s.ID, it.ID), RetentionSubscription, now),
			SubscriptionItem: it,
		})
		if err != nil {
			return err
		}
		extra = append(extra, r.base.BuildPutTxItemIfAbsent(encoded))
	}

	change := StatusChange{
		OrganizationID: s.OrganizationID,
		Livemode:       s.Livemode,
		PK:             pk,
		SK:             SubscriptionSK(s.ID),
		From:           string(s.Status),
		To:             string(updated.Status),
		Set: map[string]types.AttributeValue{
			"billing_timing": &types.AttributeValueMemberS{Value: string(timing)},
			"owner_key":      &types.AttributeValueMemberS{Value: ownerKey},
			// Recomputed even though the period is unchanged: the timing may have
			// moved, and the sweep date is derived from the row rather than stored
			// independently of it.
			"schedule_pk": &types.AttributeValueMemberS{Value: SchedulePK(updated.Livemode, JobSubscriptionDue, nextSweepDate(&updated))},
			"schedule_sk": &types.AttributeValueMemberS{Value: updated.ID},
		},
		Audit: AuditEntry{
			Entity:    EntitySubscription,
			EntityID:  s.ID,
			Action:    string(events[0]),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
			// The status did not change, so the default before/after would record
			// "ACTIVE -> ACTIVE" and say nothing. What changed is the price set, and
			// that is what the trail has to carry.
			Before: "prices=" + strings.Join(priceIDsOf(oldItems), ","),
			After:  "prices=" + strings.Join(priceIDsOf(newItems), ","),
		},
		Emit:    events,
		Subject: subscriptionSubject(&updated),
	}
	if err := commitWithExtraWrites(ctx, r.tables(), change, now, extra...); err != nil {
		return err
	}
	*s = updated
	return nil
}

// priceIDsOf renders an item set for the audit trail.
func priceIDsOf(items []billing.SubscriptionItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.PriceID)
	}
	return out
}

// ListByCustomer returns a customer's subscriptions.
//
// It filters the tenant partition rather than adding an index. That is a bounded
// compromise, not a shortcut: the read is already confined to one organization,
// and the entitlement check is called per customer, not per page view. If it
// ever becomes a hot path the fix is an index — never a Scan.
//
// The `#ITEM#` rows share the SUB# prefix, so they come back too and are
// discarded by the decode filter below.
func (r *SubscriptionRepository) ListByCustomer(ctx context.Context, organizationID string, livemode bool, customerID string, limit int) ([]billing.Subscription, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:               TenantPK(organizationID, livemode),
		SKPrefix:         skSubscription,
		FilterField:      "customer_id",
		FilterValue:      customerID,
		Limit:            limit,
		ScanIndexForward: true,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[subscriptionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Subscription, 0, len(rows))
	for _, row := range rows {
		// Item rows carry no status; only subscriptions do.
		if row.Subscription.Status == "" {
			continue
		}
		out = append(out, row.entity())
	}
	return out, nil
}

// List pages the tenant's subscriptions, newest first.
//
// Unlike ListByCustomer this reads the period index, where only subscription
// rows are indexed — the `#ITEM#` rows are not, so nothing has to be filtered
// out of the page and the limit means what it says.
func (r *SubscriptionRepository) List(
	ctx context.Context,
	organizationID string,
	livemode bool,
	limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.Subscription], error) {
	return pagePeriod(ctx, r.base, organizationID, livemode, EntitySubscription,
		"", limit, startKey,
		func(row subscriptionRow) billing.Subscription { return row.entity() })
}

// ScheduleCancellation marks the subscription to end when its current period
// closes, without changing its status.
//
// It is a separate method rather than a flag on Transition because scheduling a
// cancellation is not a state change — that is precisely the design decision
// behind cancel_at_period_end existing at all (assessment § 6.2). Modelling it
// as a status is what produces a combinatorial state explosion.
func (r *SubscriptionRepository) ScheduleCancellation(
	ctx context.Context,
	s *billing.Subscription,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) error {
	if cause == "" {
		cause = billing.CauseScheduleCancel
	}
	if s.CancelAtPeriodEnd {
		return nil
	}
	change := StatusChange{
		OrganizationID: s.OrganizationID,
		Livemode:       s.Livemode,
		PK:             TenantPK(s.OrganizationID, s.Livemode),
		SK:             SubscriptionSK(s.ID),
		From:           string(s.Status),
		To:             string(s.Status),
		Set: map[string]types.AttributeValue{
			"cancel_at_period_end": &types.AttributeValueMemberBOOL{Value: true},
		},
		Audit: AuditEntry{
			Entity:    EntitySubscription,
			EntityID:  s.ID,
			Action:    string(billing.EventSubscriptionUpdated),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
			Before:    "cancel_at_period_end=false",
			After:     "cancel_at_period_end=true",
		},
		// A scheduled cancellation is not a status change, and it is still the
		// single most important thing a consuming product needs to hear: it is the
		// notice that entitlement ends at the period boundary. Emitting only on
		// the eventual CANCELED would tell them on the day it happens, which is
		// too late to email a customer about it.
		Emit:    []billing.EventType{billing.EventSubscriptionUpdated},
		Subject: subscriptionSubject(s),
	}
	if _, err := (&billing.Subscription{Status: s.Status}).Transition(s.Status, cause); err != nil {
		// The domain still decides whether this subscription may be scheduled for
		// cancellation at all — a canceled one may not.
		return err
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return err
	}
	s.CancelAtPeriodEnd = true
	return nil
}

// DueOn returns the subscriptions the daily sweep must invoice on a given date.
//
// This is the one query that is not tenant-scoped, because a daily sweep is
// inherently cross-tenant (ADR 0002). It must never be called from a
// request-scoped path — the mode is part of the key so a test sweep can never
// reach live rows.
func (r *SubscriptionRepository) DueOn(ctx context.Context, livemode bool, due brcal.Date, limit int, startKey map[string]types.AttributeValue) (*Page[billing.Subscription], error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName:         IndexSchedule,
		PKField:           "schedule_pk",
		SKField:           "schedule_sk",
		PK:                SchedulePK(livemode, JobSubscriptionDue, due),
		Limit:             limit,
		ExclusiveStartKey: startKey,
		ScanIndexForward:  true,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[subscriptionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Subscription, len(rows))
	for i, row := range rows {
		out[i] = row.entity()
	}
	return &Page[billing.Subscription]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// tables is the set every transition in this repository writes across.
func (r *SubscriptionRepository) tables() Tables {
	return Tables{Rows: r.base, Audit: r.audit, Events: r.events}
}
