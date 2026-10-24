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

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// The history of events and deliveries, queryable without a scan.
//
//   - ENTITY#<object>#<id>#<event>   one per event, per thing it concerns: the
//     object itself, and the subscription a subscription's invoices belong to.
//   - ENDPOINT#<endpoint>#<event>    one per delivery, written when the event is
//     fanned out to the endpoint.
//   - ATTEMPT#<event>#<endpoint>#NN  one per try, written as it happens.
//
// All of them sit in the tenant's partition of the webhook table, next to the
// event and delivery they point at, and expire with the same retention. The
// pointers are written in the transaction that writes the row they point at, so
// there is no event without its pointers and no pointer without its event.
type refRow struct {
	keys
	EventID    string `dynamodbav:"event_id"`
	EndpointID string `dynamodbav:"endpoint_id,omitempty"`
}

type attemptRow struct {
	keys
	billing.DeliveryAttempt
}

// eventRefWrites are the pointers of one event: its own object and, when it
// belongs to a subscription, that subscription.
func eventRefWrites(events Base, ev billing.Event, now time.Time) ([]types.TransactWriteItem, error) {
	if ev.Entity == "" || ev.EntityID == "" {
		return nil, nil // a ping concerns no invoice or subscription
	}
	pk := TenantPK(ev.OrganizationID, ev.Livemode)
	targets := [][2]string{{ev.Entity, ev.EntityID}}
	if ev.SubscriptionID != "" && !(ev.Entity == "subscription" && ev.EntityID == ev.SubscriptionID) {
		targets = append(targets, [2]string{"subscription", ev.SubscriptionID})
	}
	out := make([]types.TransactWriteItem, 0, len(targets))
	for _, t := range targets {
		item, err := Encode(refRow{keys: newKeys(pk, EntityEventSK(t[0], t[1], ev.ID), RetentionEvent, now), EventID: ev.ID})
		if err != nil {
			return nil, err
		}
		out = append(out, events.BuildPutTxItemIfAbsent(item))
	}
	return out, nil
}

// EventRefItem is the pointer from an entity to an event, for the backfill.
func (r *WebhookRepository) EventRefItems(ev billing.Event, now time.Time) ([]types.TransactWriteItem, error) {
	return eventRefWrites(r.base, ev, now)
}

// EndpointRefItem is the pointer from an endpoint to an event delivered to it.
func (r *WebhookRepository) EndpointRefItem(ev billing.Event, endpointID string, now time.Time) (types.TransactWriteItem, error) {
	item, err := Encode(refRow{
		keys:    newKeys(TenantPK(ev.OrganizationID, ev.Livemode), EndpointEventSK(endpointID, ev.ID), RetentionWebhookDelivery, now),
		EventID: ev.ID, EndpointID: endpointID,
	})
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.base.BuildPutTxItemIfAbsent(item), nil
}

// pointerPage asks for one pointer more than the page and, when it gets it, cuts
// the page there and returns the key of its last row as the continuation. DynamoDB
// reports a continuation whenever the limit is reached, even when nothing follows,
// and a history that says "more" and then shows an empty page is worse than one
// extra read.
func (r *WebhookRepository) pointerPage(ctx context.Context, organizationID string, livemode bool, prefix string, start map[string]types.AttributeValue, limit int) ([]refRow, map[string]types.AttributeValue, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK: TenantPK(organizationID, livemode), SKPrefix: prefix,
		ScanIndexForward: false, Limit: limit + 1, ExclusiveStartKey: start,
	})
	if err != nil {
		return nil, nil, err
	}
	refs, err := DecodeItems[refRow](res.Items)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) <= limit {
		return refs, nil, nil
	}
	last := refs[limit-1]
	return refs[:limit], map[string]types.AttributeValue{"pk": str(last.PK), "sk": str(last.SK)}, nil
}

// EventsOf pages the events that concern one invoice or subscription, newest
// first. The cursor is the continuation key of the pointer query.
func (r *WebhookRepository) EventsOf(ctx context.Context, organizationID string, livemode bool, object, objectID string, start map[string]types.AttributeValue, limit int) (*Page[billing.Event], error) {
	refs, next, err := r.pointerPage(ctx, organizationID, livemode, EntityEventPrefix(object, objectID), start, limit)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Event, 0, len(refs))
	for _, ref := range refs {
		ev, err := r.GetEvent(ctx, organizationID, livemode, ref.EventID)
		if errors.Is(err, ErrNotFound) {
			continue // expired between the two reads
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *ev)
	}
	return &Page[billing.Event]{Items: out, LastEvaluatedKey: next}, nil
}

// EndpointEntry is one event as one endpoint got it.
type EndpointEntry struct {
	Event    billing.Event
	Delivery billing.Delivery
}

// DeliveriesOfEndpoint pages what one endpoint was sent, newest first.
func (r *WebhookRepository) DeliveriesOfEndpoint(ctx context.Context, organizationID string, livemode bool, endpointID string, start map[string]types.AttributeValue, limit int) (*Page[EndpointEntry], error) {
	refs, next, err := r.pointerPage(ctx, organizationID, livemode, EndpointEventPrefix(endpointID), start, limit)
	if err != nil {
		return nil, err
	}
	out := make([]EndpointEntry, 0, len(refs))
	for _, ref := range refs {
		ev, err := r.GetEvent(ctx, organizationID, livemode, ref.EventID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d, err := r.GetDelivery(ctx, organizationID, livemode, ref.EventID, endpointID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, EndpointEntry{Event: *ev, Delivery: *d})
	}
	return &Page[EndpointEntry]{Items: out, LastEvaluatedKey: next}, nil
}

// GetDelivery reads one event's delivery to one endpoint.
func (r *WebhookRepository) GetDelivery(ctx context.Context, organizationID string, livemode bool, eventID, endpointID string) (*billing.Delivery, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), DeliverySK(eventID, endpointID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}
	row, err := Decode[deliveryRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Delivery, nil
}

// RecordAttempt keeps one try as it happened. Idempotent on the attempt number:
// a repeat of the same number is ignored, so a crash between this write and the
// delivery row's update loses nothing and doubles nothing.
func (r *WebhookRepository) RecordAttempt(ctx context.Context, d *billing.Delivery, a billing.DeliveryAttempt, now time.Time) error {
	a.EventID, a.EndpointID = d.EventID, d.EndpointID
	if a.At == "" {
		a.At = now.UTC().Format(time.RFC3339Nano)
	}
	a.Error = truncate(a.Error, 500)
	a.Response = truncate(a.Response, 512)
	item, err := Encode(attemptRow{
		keys:            newKeys(TenantPK(d.OrganizationID, d.Livemode), AttemptSK(d.EventID, d.EndpointID, a.Number), RetentionWebhookDelivery, now),
		DeliveryAttempt: a,
	})
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, []types.TransactWriteItem{r.base.BuildPutTxItemIfAbsent(item)})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// Attempts lists the tries made to deliver one event to one endpoint, oldest first.
func (r *WebhookRepository) Attempts(ctx context.Context, organizationID string, livemode bool, eventID, endpointID string) ([]billing.DeliveryAttempt, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK: TenantPK(organizationID, livemode), SKPrefix: AttemptPrefix(eventID, endpointID),
		ScanIndexForward: true, Limit: billing.MaxDeliveryAttempts + 2,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[attemptRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.DeliveryAttempt, len(rows))
	for i, row := range rows {
		out[i] = row.DeliveryAttempt
	}
	return out, nil
}

// HistoryBackfillReport counts what BackfillHistoryLookups found and wrote.
type HistoryBackfillReport struct {
	Events, Deliveries int // rows scanned
	Written            int // pointers written (a repeat writes none)
}

// BackfillHistoryLookups gives the events and deliveries written before the
// history pointers existed theirs. It Scans the webhook table, which the service
// role is denied, so it runs with an operator's credentials; idempotent, since a
// pointer already there is left alone. Without apply it only counts.
func (r *WebhookRepository) BackfillHistoryLookups(ctx context.Context, apply bool) (HistoryBackfillReport, error) {
	var rep HistoryBackfillReport
	var start map[string]types.AttributeValue
	now := time.Now()
	for {
		out, err := r.base.ScanRaw(ctx, &dynamodb.ScanInput{ // standards:allow one-shot/admin scan
			FilterExpression:  aws.String("begins_with(sk, :ev)"),
			ExclusiveStartKey: start,
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":ev": &types.AttributeValueMemberS{Value: skEvent},
			},
		})
		if err != nil {
			return rep, fmt.Errorf("scan events: %w", err)
		}
		for _, item := range out.Items {
			sk, _ := item["sk"].(*types.AttributeValueMemberS)
			if sk == nil {
				continue
			}
			var writes []types.TransactWriteItem
			if strings.Contains(sk.Value, "#"+skDelivery) {
				rep.Deliveries++
				row, err := Decode[deliveryRow](item)
				if err != nil {
					return rep, err
				}
				ev, err := r.GetEvent(ctx, row.OrganizationID, row.Livemode, row.EventID)
				if err != nil {
					continue // the event expired before its delivery did
				}
				ref, err := r.EndpointRefItem(*ev, row.EndpointID, now)
				if err != nil {
					return rep, err
				}
				writes = append(writes, ref)
			} else {
				rep.Events++
				row, err := Decode[eventRow](item)
				if err != nil {
					return rep, err
				}
				if writes, err = eventRefWrites(r.base, row.Event, now); err != nil {
					return rep, err
				}
			}
			if !apply {
				continue
			}
			for _, w := range writes { // one at a time: a pointer that exists must not fail the others
				err := r.base.TransactWrite(ctx, []types.TransactWriteItem{w})
				switch {
				case err == nil:
					rep.Written++
				case IsConditionFailed(err):
				default:
					return rep, err
				}
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return rep, nil
		}
		start = out.LastEvaluatedKey
	}
}
