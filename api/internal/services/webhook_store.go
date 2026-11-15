package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/webhooks"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// webhookStore is the billing side of the shared delivery engine
// (go-common/webhooks): the engine decides when and how to attempt, this maps
// its calls onto WebhookRepository, whose layout is unchanged. The billing rows
// travel in Delivery.Ref and Endpoint.Ref so nothing is parsed back out of an id.
type webhookStore struct {
	repo     *repositories.WebhookRepository
	livemode bool
}

var _ webhooks.Store = (*webhookStore)(nil)

// toEndpoint maps a stored endpoint. A provisioned one (no author: the tenant
// plan's, on CTech's private network) is Internal.
func toEndpoint(e *billing.WebhookEndpoint) webhooks.Endpoint {
	return webhooks.Endpoint{
		ID: e.ID, URL: e.URL, Secret: e.Secret,
		Active:   e.Status == billing.EndpointActive,
		Internal: e.Provisioned(),
		Ref:      *e,
	}
}

// toDue maps a queued delivery with its event. Group is one endpoint of one
// tenant and mode: its deliveries are made in order, one at a time.
func toDue(row repositories.DueDelivery) (webhooks.Due, error) {
	body, err := json.Marshal(row.Event.Payload())
	if err != nil {
		return webhooks.Due{}, fmt.Errorf("rendering payload of %s: %w", row.Event.ID, err)
	}
	d := row.Delivery
	return webhooks.Due{
		Delivery: webhooks.Delivery{
			EventID: d.EventID, EndpointID: d.EndpointID,
			Group:    fmt.Sprintf("%s|%t|%s", d.OrganizationID, d.Livemode, d.EndpointID),
			Attempts: d.Attempts, NextAttemptAt: d.NextAttemptAt,
			Ref: d,
		},
		Event: webhooks.Event{ID: row.Event.ID, Type: string(row.Event.Type), Body: body},
	}, nil
}

func toAttempt(a webhooks.Attempt) billing.DeliveryAttempt {
	return billing.DeliveryAttempt{
		Number: a.Number, StatusCode: a.StatusCode, DurationMs: a.DurationMs,
		Response: a.Response, Error: a.Error,
	}
}

var errNoRow = errors.New("webhook store: delivery without its stored row")

func rowOf(d webhooks.Delivery) (*billing.Delivery, error) {
	row, ok := d.Ref.(billing.Delivery)
	if !ok {
		return nil, errNoRow
	}
	return &row, nil
}

func (s *webhookStore) DueDeliveries(ctx context.Context, limit int) ([]webhooks.Due, error) {
	rows, err := s.repo.DueDeliveries(ctx, s.livemode, limit)
	if err != nil {
		return nil, err
	}
	out := make([]webhooks.Due, 0, len(rows))
	for _, r := range rows {
		due, err := toDue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, due)
	}
	return out, nil
}

func (s *webhookStore) GetEndpoint(ctx context.Context, d webhooks.Delivery) (*webhooks.Endpoint, error) {
	row, err := rowOf(d)
	if err != nil {
		return nil, err
	}
	e, err := s.repo.GetEndpoint(ctx, row.OrganizationID, row.Livemode, row.EndpointID)
	if err != nil {
		// As before the move, any failure reading the endpoint is "gone": the
		// delivery is closed as failed rather than retried.
		return nil, fmt.Errorf("%w: %v", webhooks.ErrEndpointGone, err)
	}
	out := toEndpoint(e)
	return &out, nil
}

func (s *webhookStore) RecordAttempt(ctx context.Context, d webhooks.Delivery, a webhooks.Attempt, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.RecordAttempt(ctx, row, toAttempt(a), now)
}

func (s *webhookStore) MarkDelivered(ctx context.Context, d webhooks.Delivery, status int, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.MarkDelivered(ctx, row, status, now)
}

func (s *webhookStore) MarkAttemptFailed(ctx context.Context, d webhooks.Delivery, status int, cause string, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.MarkAttemptFailed(ctx, row, status, cause, now)
}

func (s *webhookStore) Defer(ctx context.Context, d webhooks.Delivery, until, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.DeferDelivery(ctx, row, until, now)
}

func (s *webhookStore) Close(ctx context.Context, d webhooks.Delivery, cause string, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.CloseDelivery(ctx, row, cause, now)
}

func (s *webhookStore) RecordHealth(ctx context.Context, e webhooks.Endpoint, ok bool, detail string, now time.Time) error {
	row, isRow := e.Ref.(billing.WebhookEndpoint)
	if !isRow {
		return errNoRow
	}
	return s.repo.RecordEndpointHealth(ctx, &row, ok, detail, now)
}

func (s *webhookStore) Requeue(ctx context.Context, d webhooks.Delivery, now time.Time) error {
	row, err := rowOf(d)
	if err != nil {
		return err
	}
	return s.repo.RequeueDelivery(ctx, row, now)
}
