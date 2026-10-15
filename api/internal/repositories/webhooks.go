package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// WebhookRepository stores endpoints, the events emitted for them, and the
// deliveries in between (ADR 0016).
//
// One repository for three row types because they are one aggregate in use: the
// fan-out pass reads an event and the tenant's endpoints together, and a retry
// reads a delivery beside the event it carries.
type WebhookRepository struct {
	base Base
	// audit is where the console's endpoint writes record who did what.
	audit Base
	// seal protects the signing secret at rest, the same way a tax id is
	// protected. A leaked endpoint secret lets somebody forge a delivery to a
	// consumer that trusts this service's signature, which is a worse outcome
	// than reading one.
	seal *crypto.Sealer
}

func NewWebhookRepository(db *dynamodb.Client, cfg *config.Config) *WebhookRepository {
	return &WebhookRepository{
		base:  NewBase(db, cfg, TableWebhooks),
		audit: NewBase(db, cfg, TableAudit),
		seal:  crypto.NewSealer(cfg.FieldEncryptionKey),
	}
}

// ── Endpoints ────────────────────────────────────────────────────────────────

// CreateEndpoint registers a destination.
func (r *WebhookRepository) CreateEndpoint(ctx context.Context, e *billing.WebhookEndpoint, now time.Time) error {
	if e.Status == "" {
		e.Status = billing.EndpointActive
	}
	if err := e.Validate(); err != nil {
		return err
	}
	row := endpointRow{
		keys:            newKeys(TenantPK(e.OrganizationID, e.Livemode), EndpointSK(e.ID), RetentionCredential, now),
		WebhookEndpoint: *e,
	}
	sealed, err := r.seal.Seal(e.Secret)
	if err != nil {
		return fmt.Errorf("sealing endpoint secret: %w", err)
	}
	row.WebhookEndpoint.Secret = sealed

	item, err := Encode(row)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("webhook endpoint %s already exists", e.ID)
	}
	return err
}

// GetEndpoint reads one endpoint, secret included — it is what signs a delivery.
func (r *WebhookRepository) GetEndpoint(ctx context.Context, organizationID string, livemode bool, endpointID string) (*billing.WebhookEndpoint, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), EndpointSK(endpointID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: webhook endpoint %s", ErrNotFound, endpointID)
	}
	row, err := Decode[endpointRow](item)
	if err != nil {
		return nil, err
	}
	return r.openEndpoint(row)
}

// ListEndpoints returns every endpoint a tenant registered, disabled ones
// included: an operator asking why nothing arrives needs to see the disabled
// row, not an empty list.
func (r *WebhookRepository) ListEndpoints(ctx context.Context, organizationID string, livemode bool) ([]billing.WebhookEndpoint, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:       TenantPK(organizationID, livemode),
		SKPrefix: skEndpoint,
		Limit:    100,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[endpointRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.WebhookEndpoint, 0, len(rows))
	for i := range rows {
		e, err := r.openEndpoint(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, nil
}

func (r *WebhookRepository) openEndpoint(row *endpointRow) (*billing.WebhookEndpoint, error) {
	e := row.WebhookEndpoint
	secret, err := r.seal.Open(e.Secret)
	if err != nil {
		return nil, fmt.Errorf("opening secret of endpoint %s: %w", e.ID, err)
	}
	e.Secret = secret
	return &e, nil
}

// RecordEndpointHealth updates the consecutive-failure run, disabling the
// endpoint once it is plainly gone.
//
// The run resets on any success rather than counting total failures. An
// endpoint that fails one delivery in a hundred is a working endpoint, and
// disabling it after twelve such failures spread over a year would be the job
// breaking a healthy integration.
//
// detail is what the endpoint said when it failed ("HTTP 404", "timeout"),
// kept so the console can show why; it is ignored on success.
func (r *WebhookRepository) RecordEndpointHealth(ctx context.Context, e *billing.WebhookEndpoint, ok bool, detail string, now time.Time) error {
	updates := map[string]any{"updated_at": now.UTC().Format(time.RFC3339Nano)}
	stamp := now.UTC().Format(time.RFC3339)
	switch {
	case ok:
		// Nothing to say when a healthy endpoint stays healthy.
		if e.FailureRun == 0 && e.LastDeliveryOK {
			return nil
		}
		updates["failure_run"] = 0
		updates["last_delivery_ok"] = true
		updates["last_delivery_at"] = stamp
		updates["last_delivery_detail"] = ""
	default:
		run := e.FailureRun + 1
		updates["failure_run"] = run
		updates["last_delivery_ok"] = false
		updates["last_delivery_at"] = stamp
		updates["last_delivery_detail"] = truncate(detail, 200)
		if run >= billing.MaxFailureRun {
			updates["status"] = string(billing.EndpointDisabled)
			updates["disabled_at"] = now.UTC().Format(time.RFC3339Nano)
		}
	}
	_, err := r.base.UpdateItem(ctx,
		TenantPK(e.OrganizationID, e.Livemode),
		new(EndpointSK(e.ID)),
		updates,
	)
	return err
}

// ── Events ───────────────────────────────────────────────────────────────────

// BuildEventTxItem renders an event as a transaction item, so it can be written
// with the change it describes rather than after it.
//
// After would be a second call that can fail on its own, which is how a paid
// invoice ends up with no `invoice.paid` event and a consumer that never learns
// its customer paid. This is the same reasoning that puts the audit row inside
// the transaction, applied to the other record a state change must not lose.
func (r *WebhookRepository) BuildEventTxItem(e *billing.Event, now time.Time) (types.TransactWriteItem, error) {
	item, _, err := r.BuildEventTxItems(e, now)
	return item, err
}

// BuildEventTxItems is BuildEventTxItem plus the pointers that make the event
// findable from the thing it concerns.
func (r *WebhookRepository) BuildEventTxItems(e *billing.Event, now time.Time) (types.TransactWriteItem, []types.TransactWriteItem, error) {
	if e.OccurredAt == "" {
		e.OccurredAt = now.UTC().Format(time.RFC3339Nano)
	}
	row := eventRow{
		keys:  newKeys(TenantPK(e.OrganizationID, e.Livemode), EventSK(e.ID), RetentionEvent, now),
		Event: *e,
		// Due immediately: fan-out is not scheduled work, it is the next thing
		// that should happen. The queue exists to survive a job that is not
		// running, not to delay one that is.
		SchedulePK: WebhookQueuePK(e.Livemode, JobWebhookFanout),
		ScheduleSK: WebhookQueueSK(now, e.ID),
	}
	item, err := Encode(row)
	if err != nil {
		return types.TransactWriteItem{}, nil, err
	}
	refs, err := eventRefWrites(r.base, *e, now)
	if err != nil {
		return types.TransactWriteItem{}, nil, err
	}
	return r.base.BuildPutTxItemIfAbsent(item), refs, nil
}

// PendingEvents returns events waiting to be matched to endpoints, oldest first.
//
// The query is ascending with a limit and no time condition, which is the whole
// trick: the sort key is the due time, so the head of the partition is the work
// that is due. The caller stops at the first row that is not.
func (r *WebhookRepository) PendingEvents(ctx context.Context, livemode bool, limit int) ([]billing.Event, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName:        IndexSchedule,
		PKField:          "schedule_pk",
		SKField:          "schedule_sk",
		PK:               WebhookQueuePK(livemode, JobWebhookFanout),
		ScanIndexForward: true,
		Limit:            limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[eventRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Event, len(rows))
	for i, row := range rows {
		out[i] = row.Event
	}
	return out, nil
}

// FanOut writes one delivery per matching endpoint and takes the event out of
// the fan-out queue, in a single transaction.
//
// Both halves together, because either alone is a defect: writing deliveries
// without clearing the event fans it out again on the next pass and delivers
// everything twice, and clearing it without the deliveries loses the event
// entirely. An event that matches nothing still leaves the queue — "no endpoint
// wanted this" is a decision, not a pending job.
func (r *WebhookRepository) FanOut(ctx context.Context, ev *billing.Event, endpoints []billing.WebhookEndpoint, now time.Time) (int, error) {
	pk := TenantPK(ev.OrganizationID, ev.Livemode)

	writes := make([]types.TransactWriteItem, 0, 2*len(endpoints)+1)
	for _, e := range endpoints {
		row := deliveryRow{
			keys: newKeys(pk, DeliverySK(ev.ID, e.ID), RetentionWebhookDelivery, now),
			Delivery: billing.Delivery{
				EventID:        ev.ID,
				EndpointID:     e.ID,
				OrganizationID: ev.OrganizationID,
				Livemode:       ev.Livemode,
				Status:         billing.DeliveryPending,
				NextAttemptAt:  now.UTC().Format(time.RFC3339Nano),
			},
			SchedulePK: WebhookQueuePK(ev.Livemode, JobWebhookDelivery),
			ScheduleSK: WebhookQueueSK(now, ev.ID+"#"+e.ID),
		}
		item, err := Encode(row)
		if err != nil {
			return 0, err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(item))
		ref, err := r.EndpointRefItem(*ev, e.ID, now)
		if err != nil {
			return 0, err
		}
		writes = append(writes, ref)
	}

	writes = append(writes, r.base.BuildRawUpdateTxItem(
		pk, new(EventSK(ev.ID)),
		"SET #ua = :now REMOVE #spk, #ssk",
		// Conditional on the event still being queued. Two delivery jobs running
		// at once — which is what a second instance is — would otherwise both fan
		// the same event out and double every delivery.
		"attribute_exists(schedule_pk)",
		map[string]string{"#ua": "updated_at", "#spk": "schedule_pk", "#ssk": "schedule_sk"},
		map[string]types.AttributeValue{":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)}},
	))

	err := r.base.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		// Another pass got there first. Not an error: the work is done.
		return 0, nil
	}
	return len(endpoints), err
}

// ── Deliveries ───────────────────────────────────────────────────────────────

// DueDelivery pairs a delivery with the event it carries, which is what an
// attempt actually needs.
type DueDelivery struct {
	Delivery billing.Delivery
	Event    billing.Event
}

// DueDeliveries returns attempts that are ready, oldest due first.
//
// Same ascending-with-limit shape as PendingEvents. The event is read alongside
// each delivery because the payload comes from it — one extra GetItem per
// delivery, inside a partition the caller already holds.
func (r *WebhookRepository) DueDeliveries(ctx context.Context, livemode bool, limit int) ([]DueDelivery, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName:        IndexSchedule,
		PKField:          "schedule_pk",
		SKField:          "schedule_sk",
		PK:               WebhookQueuePK(livemode, JobWebhookDelivery),
		ScanIndexForward: true,
		Limit:            limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[deliveryRow](res.Items)
	if err != nil {
		return nil, err
	}

	out := make([]DueDelivery, 0, len(rows))
	for _, row := range rows {
		ev, err := r.GetEvent(ctx, row.OrganizationID, row.Livemode, row.EventID)
		if err != nil {
			// An event whose retention expired under a delivery that never
			// succeeded. Skipping is right — there is nothing left to send — and
			// the row is left in the queue rather than silently dropped, because a
			// delivery outliving its event is a retention bug worth seeing.
			continue
		}
		out = append(out, DueDelivery{Delivery: row.Delivery, Event: *ev})
	}
	return out, nil
}

// GetEvent reads one event.
func (r *WebhookRepository) GetEvent(ctx context.Context, organizationID string, livemode bool, eventID string) (*billing.Event, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), EventSK(eventID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: event %s", ErrNotFound, eventID)
	}
	row, err := Decode[eventRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Event, nil
}

// MarkDelivered ends a delivery successfully and takes it out of the queue.
func (r *WebhookRepository) MarkDelivered(ctx context.Context, d *billing.Delivery, statusCode int, now time.Time) error {
	updates := map[string]any{
		"status":           string(billing.DeliveryDelivered),
		"attempts":         d.Attempts + 1,
		"last_status_code": statusCode,
		"delivered_at":     now.UTC().Format(time.RFC3339Nano),
		"updated_at":       now.UTC().Format(time.RFC3339Nano),
		// Leaving the schedule index is what "done" means here. A delivered row
		// that stayed would be retried forever.
		"schedule_pk":     nil,
		"schedule_sk":     nil,
		"next_attempt_at": nil,
		"last_error":      nil,
	}
	_, err := r.base.UpdateItem(ctx,
		TenantPK(d.OrganizationID, d.Livemode),
		new(DeliverySK(d.EventID, d.EndpointID)),
		updates,
	)
	return err
}

// MarkAttemptFailed schedules the next attempt, or gives up.
//
// Giving up is a terminal status and not a deletion: "we tried eight times over
// two days and the endpoint never answered" is the answer to a support question
// that will be asked, and a missing row answers it with silence.
func (r *WebhookRepository) MarkAttemptFailed(ctx context.Context, d *billing.Delivery, statusCode int, cause string, now time.Time) error {
	attempts := d.Attempts + 1
	updates := map[string]any{
		"attempts":   attempts,
		"last_error": truncate(cause, 500),
		"updated_at": now.UTC().Format(time.RFC3339Nano),
	}
	if statusCode > 0 {
		updates["last_status_code"] = statusCode
	}

	if attempts >= billing.MaxDeliveryAttempts {
		updates["status"] = string(billing.DeliveryFailed)
		updates["schedule_pk"] = nil
		updates["schedule_sk"] = nil
		updates["next_attempt_at"] = nil
	} else {
		next := now.Add(billing.Backoff(attempts))
		updates["next_attempt_at"] = next.UTC().Format(time.RFC3339Nano)
		updates["schedule_pk"] = WebhookQueuePK(d.Livemode, JobWebhookDelivery)
		// Rewriting the sort key is what moves the row down the queue. Without it
		// a failing delivery stays at the head and is retried on every pass,
		// starving everything behind it.
		updates["schedule_sk"] = WebhookQueueSK(next, d.EventID+"#"+d.EndpointID)
	}

	_, err := r.base.UpdateItem(ctx,
		TenantPK(d.OrganizationID, d.Livemode),
		new(DeliverySK(d.EventID, d.EndpointID)),
		updates,
	)
	return err
}

// DeferDelivery moves a delivery down the queue to `until` without counting an
// attempt: nothing was sent. Used for an endpoint that is disabled but may be
// re-enabled (a provisioned one), so its rows keep what it missed without
// sitting at the head of the shared queue, where they would starve every
// other tenant's deliveries.
func (r *WebhookRepository) DeferDelivery(ctx context.Context, d *billing.Delivery, until time.Time, now time.Time) error {
	_, err := r.base.UpdateItem(ctx,
		TenantPK(d.OrganizationID, d.Livemode),
		new(DeliverySK(d.EventID, d.EndpointID)),
		map[string]any{
			"next_attempt_at": until.UTC().Format(time.RFC3339Nano),
			"schedule_pk":     WebhookQueuePK(d.Livemode, JobWebhookDelivery),
			"schedule_sk":     WebhookQueueSK(until, d.EventID+"#"+d.EndpointID),
			"updated_at":      now.UTC().Format(time.RFC3339Nano),
		},
	)
	return err
}

// CloseDelivery ends a delivery as failed without an attempt, and takes it off
// the queue: there is nowhere it can ever be sent (a console endpoint that was
// disabled; the console has no re-enable, only delete and add again).
func (r *WebhookRepository) CloseDelivery(ctx context.Context, d *billing.Delivery, cause string, now time.Time) error {
	_, err := r.base.UpdateItem(ctx,
		TenantPK(d.OrganizationID, d.Livemode),
		new(DeliverySK(d.EventID, d.EndpointID)),
		map[string]any{
			"status":          string(billing.DeliveryFailed),
			"last_error":      truncate(cause, 500),
			"schedule_pk":     nil,
			"schedule_sk":     nil,
			"next_attempt_at": nil,
			"updated_at":      now.UTC().Format(time.RFC3339Nano),
		},
	)
	return err
}

// ListDeliveries returns every attempt made for one event, for the console and
// for answering "did they get it?".
func (r *WebhookRepository) ListDeliveries(ctx context.Context, organizationID string, livemode bool, eventID string) ([]billing.Delivery, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:       TenantPK(organizationID, livemode),
		SKPrefix: EventSK(eventID) + "#" + skDelivery,
		Limit:    100,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[deliveryRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Delivery, len(rows))
	for i, row := range rows {
		out[i] = row.Delivery
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ── Console writes (spec § 5.2) ─────────────────────────────────────────────

// skEndpointRev is one row per tenant and mode whose `rev` every console add
// and remove moves. An add conditions on the rev it read before counting, so
// two concurrent adds cannot both see four endpoints and both write a fifth.
// Not "ENDPOINT#…": ListEndpoints reads that prefix.
const skEndpointRev = "ENDPOINT_REV"

// PingInterval is the least time between two console tests of one endpoint.
const PingInterval = 30 * time.Second

var (
	// ErrEndpointLimit: the tenant already has the most endpoints a mode may.
	ErrEndpointLimit = errors.New("webhook endpoint limit reached")
	// ErrEndpointProvisioned: the endpoint came from the tenant plan.
	ErrEndpointProvisioned = errors.New("webhook endpoint provisioned by the tenant plan")
	// ErrPingTooSoon: the endpoint was tested less than PingInterval ago.
	ErrPingTooSoon = errors.New("webhook endpoint tested too recently")
	// ErrEndpointsChanged: other adds kept moving the count; the caller retries later.
	ErrEndpointsChanged = errors.New("the tenant's webhook endpoints changed during the request")
)

type endpointRevRow struct {
	Rev int64 `dynamodbav:"rev"`
}

// endpointsAndRev reads the tenant's endpoints and the revision row in one
// strongly consistent query (both sort keys start with "ENDPOINT"). Two
// eventually consistent reads could pair a fresh revision with a stale list,
// and the add would pass the limit with its condition still holding.
func (r *WebhookRepository) endpointsAndRev(ctx context.Context, organizationID string, livemode bool) (int, int64, error) {
	count, rev := 0, int64(0)
	var start map[string]types.AttributeValue
	for {
		out, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
			KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :sk)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": str(TenantPK(organizationID, livemode)), ":sk": str("ENDPOINT"),
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return 0, 0, err
		}
		for _, item := range out.Items {
			sk, _ := item["sk"].(*types.AttributeValueMemberS)
			switch {
			case sk != nil && sk.Value == skEndpointRev:
				row, err := Decode[endpointRevRow](item)
				if err != nil {
					return 0, 0, err
				}
				rev = row.Rev
			case sk != nil && strings.HasPrefix(sk.Value, skEndpoint):
				count++
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return count, rev, nil
		}
		start = out.LastEvaluatedKey
	}
}

// bumpRev moves the revision. seen < 0 means unconditional (a removal only
// makes room, so it need not win a race; it must only be seen by one).
func (r *WebhookRepository) bumpRev(organizationID string, livemode bool, seen int64, now time.Time) types.TransactWriteItem {
	sk := skEndpointRev
	values := map[string]types.AttributeValue{":one": numberValue(1), ":now": str(now.UTC().Format(time.RFC3339Nano))}
	cond := ""
	switch {
	case seen == 0:
		cond = "attribute_not_exists(rev)"
	case seen > 0:
		cond = "rev = :seen"
		values[":seen"] = numberValue(seen)
	}
	return r.base.BuildRawUpdateTxItem(TenantPK(organizationID, livemode), &sk,
		"SET updated_at = :now ADD rev :one", cond, nil, values)
}

func (r *WebhookRepository) endpointAudit(e *billing.WebhookEndpoint, action, before, after, actor, requestID string, now time.Time) (types.TransactWriteItem, error) {
	item, err := buildAuditItem(e.OrganizationID, e.Livemode, AuditEntry{
		Entity: EntityWebhookEndpoint, EntityID: e.ID, Action: action,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, before, after, now)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.audit.BuildPutTxItemIfAbsent(item), nil
}

// AddEndpoint registers a merchant's endpoint from the console: at most max per
// tenant and mode, counting every endpoint there (provisioned ones too), with
// the audit row. Up to three attempts when concurrent adds move the count.
func (r *WebhookRepository) AddEndpoint(ctx context.Context, e *billing.WebhookEndpoint, max int, actor, requestID string, now time.Time) error {
	if e.AddedBy == "" || actor == "" {
		return fmt.Errorf("repositories: adding endpoint %s needs who added it", e.ID)
	}
	if e.Status == "" {
		e.Status = billing.EndpointActive
	}
	if err := e.Validate(); err != nil {
		return err
	}
	sealed, err := r.seal.Seal(e.Secret)
	if err != nil {
		return fmt.Errorf("sealing endpoint secret: %w", err)
	}
	row := endpointRow{
		keys:            newKeys(TenantPK(e.OrganizationID, e.Livemode), EndpointSK(e.ID), RetentionCredential, now),
		WebhookEndpoint: *e,
	}
	row.WebhookEndpoint.Secret = sealed
	item, err := Encode(row)
	if err != nil {
		return err
	}
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointCreated, "", e.URL, actor, requestID, now)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		count, rev, err := r.endpointsAndRev(ctx, e.OrganizationID, e.Livemode)
		if err != nil {
			return err
		}
		if count >= max {
			return fmt.Errorf("%w: %d", ErrEndpointLimit, max)
		}
		err = r.base.TransactWrite(ctx, txItems(
			r.base.BuildPutTxItemIfAbsent(item),
			r.bumpRev(e.OrganizationID, e.Livemode, rev, now),
			audit,
		))
		// Lost to a concurrent add or remove (the rev moved, or the two
		// transactions collided): count again.
		if !IsConditionFailed(err) && !IsTransactionConflict(err) {
			return err
		}
	}
	return ErrEndpointsChanged
}

// RemoveEndpoint deletes a console endpoint. Its queued deliveries close as
// "endpoint no longer exists" on the delivery job's next pass.
func (r *WebhookRepository) RemoveEndpoint(ctx context.Context, e *billing.WebhookEndpoint, actor, requestID string, now time.Time) error {
	if e.Provisioned() {
		return fmt.Errorf("%w: %s", ErrEndpointProvisioned, e.ID)
	}
	del := types.TransactWriteItem{Delete: &types.Delete{
		TableName: aws.String(r.base.TableName),
		Key: map[string]types.AttributeValue{
			"pk": str(TenantPK(e.OrganizationID, e.Livemode)),
			"sk": str(EndpointSK(e.ID)),
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(added_by)"),
	}}
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointDeleted, e.URL, "", actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(del, r.bumpRev(e.OrganizationID, e.Livemode, -1, now), audit))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: webhook endpoint %s", ErrNotFound, e.ID)
	}
	return conflictErr(err)
}

// RotateEndpointSecret replaces the signing secret at once (scope decision 10).
func (r *WebhookRepository) RotateEndpointSecret(ctx context.Context, e *billing.WebhookEndpoint, secret, actor, requestID string, now time.Time) error {
	if e.Provisioned() {
		return fmt.Errorf("%w: %s", ErrEndpointProvisioned, e.ID)
	}
	if len(secret) < 32 {
		return fmt.Errorf("%w: secret must be at least 32 characters", billing.ErrInvalidEndpoint)
	}
	sealed, err := r.seal.Seal(secret)
	if err != nil {
		return fmt.Errorf("sealing endpoint secret: %w", err)
	}
	sk := EndpointSK(e.ID)
	update := r.base.BuildRawUpdateTxItem(TenantPK(e.OrganizationID, e.Livemode), &sk,
		"SET secret = :s, updated_at = :now", "attribute_exists(pk) AND attribute_exists(added_by)", nil,
		map[string]types.AttributeValue{":s": str(sealed), ":now": str(now.UTC().Format(time.RFC3339Nano))})
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointSecretRotated, "", "", actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(update, audit))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: webhook endpoint %s", ErrNotFound, e.ID)
	}
	if err == nil {
		e.Secret = secret
	}
	return conflictErr(err)
}

// EmitPing queues the console's test event for one endpoint. It goes through
// the same fan-out and delivery as any event (spec § 5.2), signed with the
// endpoint's secret, retried on failure, counted in the endpoint's health.
func (r *WebhookRepository) EmitPing(ctx context.Context, e *billing.WebhookEndpoint, now time.Time) (*billing.Event, error) {
	ev := &billing.Event{
		ID: id.NewWithPrefix(id.PrefixEvent), OrganizationID: e.OrganizationID, Livemode: e.Livemode,
		Type: billing.EventPing, Entity: "webhook_endpoint", EntityID: e.ID, EndpointID: e.ID,
	}
	item, err := r.BuildEventTxItem(ev, now)
	if err != nil {
		return nil, err
	}
	// The event goes into the queue every tenant shares; one ping per endpoint
	// per PingInterval, stamped on the endpoint row in the same transaction.
	sk := EndpointSK(e.ID)
	stamp := r.base.BuildRawUpdateTxItem(TenantPK(e.OrganizationID, e.Livemode), &sk,
		"SET last_ping_at = :now",
		"attribute_exists(pk) AND (attribute_not_exists(last_ping_at) OR last_ping_at < :cutoff)", nil,
		map[string]types.AttributeValue{
			":now":    str(now.UTC().Format(time.RFC3339Nano)),
			":cutoff": str(now.Add(-PingInterval).UTC().Format(time.RFC3339Nano)),
		})
	err = r.base.TransactWrite(ctx, txItems(stamp, item))
	switch {
	case IsConditionFailed(err):
		return nil, fmt.Errorf("%w: %s", ErrPingTooSoon, e.ID)
	case err != nil:
		return nil, conflictErr(err)
	}
	return ev, nil
}
