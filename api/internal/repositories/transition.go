package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// ErrConcurrentModification reports that the row was not in the state the caller
// believed it was in. It is the expected outcome of two operators, or an
// operator and a webhook, acting on the same invoice at the same time — not an
// internal error, and the caller should re-read and decide.
var ErrConcurrentModification = errors.New("row changed since it was read")

// AuditEntry is the record written alongside a state change.
type AuditEntry struct {
	Entity    Entity
	EntityID  string
	Action    string // normally the emitted event type
	Cause     billing.Cause
	Actor     string
	RequestID string
	IP        string
	Before    string
	After     string
}

// StatusChange is one state transition, ready to be committed.
type StatusChange struct {
	OrganizationID string
	Livemode       bool
	PK, SK         string
	From, To       string

	// Field is the attribute holding the state. Defaults to "status"; the
	// organization's charge gate lives in payout_status, which is the only row
	// whose state is not called that.
	Field string

	// Set holds attributes written alongside the status — a paid_at, an
	// incremented attempt counter, a new period index.
	Set map[string]types.AttributeValue
	// Remove holds attributes deleted by the transition. This is how a row
	// leaves a sparse index: a canceled subscription removes its schedule keys
	// and disappears from the sweep, rather than being filtered out of it.
	Remove []string

	Audit AuditEntry

	// Emit are the outbound events this transition produces (ADR 0016). Empty
	// means the transition is not something a consumer is told about — a payment
	// attempt moving between its own internal states, for instance.
	//
	// They are written here, in the transaction, for exactly the reason the audit
	// row is: an invoice that reaches PAID with no `invoice.paid` queued is a
	// consumer who never learns their customer paid, and "we usually emit the
	// event" is not a property. Emitting at the call site instead would put that
	// guarantee in eight places.
	Emit []billing.EventType
	// Subject describes what the events are about. Required when Emit is set.
	Subject EventSubject
}

// eventSubjectOf renders the routing and payload subject for the two entities
// that emit. It lives here, beside EventSubject, so the two objects a consumer
// can receive are named in one place rather than spelled out at each call.
func invoiceSubject(inv *billing.Invoice) EventSubject {
	return EventSubject{
		Object:         "invoice",
		ObjectID:       inv.ID,
		SubscriptionID: inv.SubscriptionID,
		OwnerKey:       inv.OwnerKey,
	}
}

func subscriptionSubject(sub *billing.Subscription) EventSubject {
	return EventSubject{
		Object:         "subscription",
		ObjectID:       sub.ID,
		SubscriptionID: sub.ID,
		OwnerKey:       sub.OwnerKey,
	}
}

// EventSubject is what an emitted event points at, and how it is routed.
type EventSubject struct {
	// Object is the payload's `data.object` — "invoice", "subscription".
	Object   string
	ObjectID string
	// SubscriptionID is the subscription this concerns, which for an invoice is
	// the subscription that produced it. Empty on a one-off invoice.
	SubscriptionID string
	// OwnerKey routes the event to one endpoint rather than all of them. It is
	// carried here rather than looked up because the caller has already read the
	// entity that knows it.
	OwnerKey string
}

// CommitStatusChange writes the new status and its audit entry **in one
// transaction**.
//
// Two properties, both of which are the reason this function exists rather than
// two calls at the call site:
//
//   - The audit entry cannot go missing. A status that changed with no record of
//     who changed it is unanswerable during an incident, and "we usually write
//     the audit row" is not a property.
//   - The update is conditional on the status the caller read. Without it, two
//     concurrent transitions both succeed and the last writer wins silently —
//     which, on an invoice, means a webhook marking PAID can be overwritten by
//     an operator voiding a row they read seconds earlier.
//
// `rows` is the table the entity lives in and `audit` is the audit table. They
// are two tables, and the transaction is still one: each TransactWriteItem
// carries its own table name, so TransactWriteItems commits across both or
// neither. Nothing about the guarantee depended on them sharing a table.
func CommitStatusChange(ctx context.Context, t Tables, c StatusChange, now time.Time) error {
	return commitWithExtraWrites(ctx, t, c, now)
}

// Tables is the set a transition writes across: the entity's own table, the
// audit table, and — when the transition emits — the webhook outbox.
//
// It is a struct rather than three positional arguments because three bare
// `Base` values at a call site are three chances to pass them in the wrong
// order, and the compiler would accept every one of them.
type Tables struct {
	Rows   Base
	Audit  Base
	Events Base
}

// commitWithExtraWrites commits a status change together with additional writes
// that must succeed or fail with it — invoice numbering being the case that
// needs it, because a counter advanced without its invoice is a burnt number.
func commitWithExtraWrites(ctx context.Context, t Tables, c StatusChange, now time.Time, extra ...types.TransactWriteItem) error {
	writes, err := buildStatusChangeWrites(t.Rows, t.Audit, c, now)
	if err != nil {
		return err
	}
	events, err := buildEventWrites(t.Events, c, now)
	if err != nil {
		return err
	}
	writes = append(writes, events...)
	writes = append(writes, extra...)

	err = t.Rows.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s expected to be %s", ErrConcurrentModification, c.SK, c.From)
	}
	return err
}

// buildCreationEvent renders the outbox row for something that came into
// existence, which is not a status change and therefore not a transition.
//
// It exists so creation and transition produce the same kind of row through the
// same code, rather than a second hand-rolled event shape that drifts.
func buildCreationEvent(events Base, organizationID string, livemode bool, t billing.EventType, subject EventSubject, now time.Time) (types.TransactWriteItem, error) {
	items, err := buildEventWrites(events, StatusChange{
		OrganizationID: organizationID,
		Livemode:       livemode,
		Emit:           []billing.EventType{t},
		Subject:        subject,
	}, now)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return items[0], nil
}

// buildEventWrites renders the outbox rows for a transition.
//
// A transition that names events without a subject is a programming error, not
// a row to write with empty fields: an event pointing at nothing is delivered,
// and the consumer that receives it cannot look anything up.
func buildEventWrites(events Base, c StatusChange, now time.Time) ([]types.TransactWriteItem, error) {
	if len(c.Emit) == 0 {
		return nil, nil
	}
	if c.Subject.Object == "" || c.Subject.ObjectID == "" {
		return nil, fmt.Errorf("repositories: %s emits %v with no subject", c.SK, c.Emit)
	}

	out := make([]types.TransactWriteItem, 0, len(c.Emit))
	for _, t := range c.Emit {
		row := eventRow{
			Event: billing.Event{
				ID:             id.NewWithPrefix(id.PrefixEvent),
				OrganizationID: c.OrganizationID,
				Livemode:       c.Livemode,
				Type:           t,
				Entity:         c.Subject.Object,
				EntityID:       c.Subject.ObjectID,
				SubscriptionID: c.Subject.SubscriptionID,
				OwnerKey:       c.Subject.OwnerKey,
				OccurredAt:     now.UTC().Format(time.RFC3339Nano),
			},
			SchedulePK: WebhookQueuePK(c.Livemode, JobWebhookFanout),
		}
		row.keys = newKeys(
			TenantPK(c.OrganizationID, c.Livemode),
			EventSK(row.Event.ID),
			RetentionEvent,
			now,
		)
		row.ScheduleSK = WebhookQueueSK(now, row.Event.ID)

		item, err := Encode(row)
		if err != nil {
			return nil, err
		}
		out = append(out, events.BuildPutTxItemIfAbsent(item))
	}
	return out, nil
}

func buildStatusChangeWrites(base, audit Base, c StatusChange, now time.Time) ([]types.TransactWriteItem, error) {
	if c.From == "" || c.To == "" {
		return nil, fmt.Errorf("repositories: status change needs both states")
	}
	if c.Audit.Actor == "" {
		// "The system did it" is not an answer during an incident, so an unset
		// actor is a programming error rather than a default.
		return nil, fmt.Errorf("repositories: status change on %s needs an actor", c.SK)
	}

	field := c.Field
	if field == "" {
		field = "status"
	}
	names := map[string]string{"#st": field, "#ua": "updated_at"}
	values := map[string]types.AttributeValue{
		":to":   &types.AttributeValueMemberS{Value: c.To},
		":from": &types.AttributeValueMemberS{Value: c.From},
		":now":  &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	sets := []string{"#st = :to", "#ua = :now"}

	// Sorted so the generated expression is deterministic and diffable in a log.
	for i, key := range sortedKeys(c.Set) {
		n, v := fmt.Sprintf("#s%d", i), fmt.Sprintf(":s%d", i)
		names[n] = key
		values[v] = c.Set[key]
		sets = append(sets, n+" = "+v)
	}

	expr := "SET " + strings.Join(sets, ", ")
	if len(c.Remove) > 0 {
		removes := make([]string, 0, len(c.Remove))
		for i, attr := range c.Remove {
			n := fmt.Sprintf("#r%d", i)
			names[n] = attr
			removes = append(removes, n)
		}
		expr += " REMOVE " + strings.Join(removes, ", ")
	}

	sk := c.SK
	update := base.BuildRawUpdateTxItem(
		c.PK, &sk, expr,
		"attribute_exists(pk) AND #st = :from",
		names, values,
	)

	auditItem, err := buildAuditItem(c.OrganizationID, c.Livemode, c.Audit, c.From, c.To, now)
	if err != nil {
		return nil, err
	}
	return []types.TransactWriteItem{update, audit.BuildPutTxItemIfAbsent(auditItem)}, nil
}

// buildAuditItem renders an audit row. Before/After default to the status pair,
// which is what the caller almost always wants and forgets to fill in.
func buildAuditItem(organizationID string, livemode bool, a AuditEntry, from, to string, now time.Time) (map[string]types.AttributeValue, error) {
	if a.Before == "" {
		a.Before = from
	}
	if a.After == "" {
		a.After = to
	}
	auditID := id.NewWithPrefix(id.PrefixAuditLog)
	row := auditRow{
		keys:        newKeys(TenantPK(organizationID, livemode), AuditSK(auditID), RetentionAuditLog, now),
		PeriodAttrs: NewPeriodAttrs(organizationID, livemode, EntityAudit, brcal.FromTime(now), auditID),
		AuditLog: billing.AuditLog{
			ID:             auditID,
			OrganizationID: organizationID,
			Livemode:       livemode,
			EntityType:     string(a.Entity),
			EntityID:       a.EntityID,
			Action:         a.Action,
			Cause:          a.Cause,
			Actor:          a.Actor,
			Before:         a.Before,
			After:          a.After,
			RequestID:      a.RequestID,
			IP:             a.IP,
			CreatedAt:      now.UTC(),
		},
	}
	return Encode(row)
}

func sortedKeys(m map[string]types.AttributeValue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
