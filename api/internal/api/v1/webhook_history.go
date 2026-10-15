package v1

import (
	"errors"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// What happened around an invoice or a subscription, and where it was sent.
//
// An event is the record that something happened (invoice.paid); a delivery is
// that event on its way to one webhook endpoint; an attempt is one try at it.
// These routes answer the support questions in that order: "what happened to
// this invoice?", "who was told?", "what did they answer, and when?".

// deliveryView is one event as one endpoint got it.
type deliveryView struct {
	EndpointID string `json:"endpoint_id"`
	// EndpointURL is where it was sent, without credentials or a query string:
	// an endpoint's URL may carry a token the reader of this history should not.
	EndpointURL    string                    `json:"endpoint_url,omitempty"`
	Status         billing.DeliveryStatus    `json:"status"`
	Attempts       int                       `json:"attempts"`
	LastStatusCode int                       `json:"last_status_code,omitempty"`
	LastError      string                    `json:"last_error,omitempty"`
	DeliveredAt    string                    `json:"delivered_at,omitempty"`
	NextAttemptAt  string                    `json:"next_attempt_at,omitempty"`
	AttemptLog     []billing.DeliveryAttempt `json:"attempt_log,omitempty"`
}

// eventView is an event with the deliveries it caused.
type eventView struct {
	ID             string            `json:"id"`
	Type           billing.EventType `json:"type"`
	Object         string            `json:"object"`
	ObjectID       string            `json:"object_id"`
	SubscriptionID string            `json:"subscription_id,omitempty"`
	OwnerKey       string            `json:"owner_key,omitempty"`
	OccurredAt     string            `json:"occurred_at"`
	Livemode       bool              `json:"livemode"`
	Deliveries     []deliveryView    `json:"deliveries"`
	Payload        *billing.Payload  `json:"payload,omitempty"`
}

// safeURL keeps scheme, host and path.
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func newDeliveryView(d billing.Delivery, endpointURL string, attempts []billing.DeliveryAttempt) deliveryView {
	return deliveryView{
		EndpointID: d.EndpointID, EndpointURL: endpointURL, Status: d.Status, Attempts: d.Attempts,
		LastStatusCode: d.LastStatusCode, LastError: d.LastError, DeliveredAt: d.DeliveredAt, NextAttemptAt: d.NextAttemptAt,
		AttemptLog: attempts,
	}
}

// historyQuery reads the paging and detail parameters shared by these routes.
type historyQuery struct {
	limit    int
	cursor   map[string]types.AttributeValue
	attempts bool
}

// historyLimit is the default and the ceiling of a page of history. It is a
// parameter here, unlike pageLimit, because a screen paging a long history may
// reasonably want fewer; the ceiling keeps one request cheap.
const (
	historyDefaultLimit = 20
	historyMaxLimit     = 50
)

func readHistoryQuery(c fiber.Ctx) (historyQuery, error) {
	var q historyQuery
	q.limit = fiber.Query(c, "limit", historyDefaultLimit)
	if q.limit < 1 || q.limit > historyMaxLimit {
		return q, errBadHistoryLimit
	}
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return q, errBadCursor
	}
	q.cursor = start
	q.attempts = c.Query("include") == "attempts"
	return q, nil
}

var (
	errBadHistoryLimit = errors.New("limit")
	errBadCursor       = errors.New("cursor")
)

func historyQueryProblem(c fiber.Ctx, err error) error {
	if errors.Is(err, errBadHistoryLimit) {
		return problem.Validation([]problem.FieldError{
			fieldErr("limit", "out_of_range", "between 1 and 50", "min", 1, "max", historyMaxLimit),
		}).Send(c)
	}
	return problem.BadRequest("invalid cursor").WithCode("invalid_cursor").Send(c)
}

// eventViews renders events with their deliveries. Endpoint URLs are read once
// per endpoint. A credential scoped to an owner sees the events of that owner.
func (h *handlers) eventViews(c fiber.Ctx, t middleware.Tenant, events []billing.Event, withAttempts, withPayload bool) ([]eventView, error) {
	urls := map[string]string{}
	owner := ""
	if cred := middleware.GetCredential(c); cred != nil {
		owner = cred.OwnerKey
	}
	out := make([]eventView, 0, len(events))
	for i := range events {
		ev := &events[i]
		if owner != "" && ev.OwnerKey != owner {
			continue
		}
		deliveries, err := h.webhooks.ListDeliveries(c.Context(), t.OrganizationID, t.Livemode, ev.ID)
		if err != nil {
			return nil, err
		}
		view := eventView{
			ID: ev.ID, Type: ev.Type, Object: ev.Entity, ObjectID: ev.EntityID, SubscriptionID: ev.SubscriptionID,
			OwnerKey: ev.OwnerKey, OccurredAt: ev.OccurredAt, Livemode: ev.Livemode, Deliveries: make([]deliveryView, 0, len(deliveries)),
		}
		if withPayload {
			p := ev.Payload()
			view.Payload = &p
		}
		for _, d := range deliveries {
			u, known := urls[d.EndpointID]
			if !known {
				if e, err := h.webhooks.GetEndpoint(c.Context(), t.OrganizationID, t.Livemode, d.EndpointID); err == nil {
					u = safeURL(e.URL)
				}
				urls[d.EndpointID] = u
			}
			var attempts []billing.DeliveryAttempt
			if withAttempts {
				if attempts, err = h.webhooks.Attempts(c.Context(), t.OrganizationID, t.Livemode, ev.ID, d.EndpointID); err != nil {
					return nil, err
				}
			}
			view.Deliveries = append(view.Deliveries, newDeliveryView(d, u, attempts))
		}
		out = append(out, view)
	}
	return out, nil
}

// objectEvents answers "what happened to this invoice / subscription".
func (h *handlers) objectEvents(c fiber.Ctx, object, objectID string) error {
	t := middleware.GetTenant(c)
	q, err := readHistoryQuery(c)
	if err != nil {
		return historyQueryProblem(c, err)
	}
	page, err := h.webhooks.EventsOf(c.Context(), t.OrganizationID, t.Livemode, object, objectID, q.cursor, q.limit)
	if err != nil {
		return fail(c, err)
	}
	views, err := h.eventViews(c, t, page.Items, q.attempts, false)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(listResponse[eventView]{Data: views, HasMore: page.LastEvaluatedKey != nil, Cursor: repositories.EncodeCursor(page.LastEvaluatedKey)})
}

// invoiceEvents is GET .../invoices/:id/events.
func (h *handlers) invoiceEvents(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return h.objectEvents(c, "invoice", inv.ID)
}

// subscriptionEvents is GET .../subscriptions/:id/events: the subscription's own
// events and those of the invoices it produced.
func (h *handlers) subscriptionEvents(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return h.objectEvents(c, "subscription", sub.ID)
}

// eventDetail is GET /console/events/:id: one event with its payload, its
// deliveries and every attempt.
func (h *handlers) eventDetail(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	ev, err := h.webhooks.GetEvent(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	views, err := h.eventViews(c, t, []billing.Event{*ev}, true, true)
	if err != nil {
		return fail(c, err)
	}
	if len(views) == 0 {
		return fail(c, repositories.ErrNotFound)
	}
	return c.JSON(views[0])
}

// endpointDeliveryView is one row of an endpoint's history.
type endpointDeliveryView struct {
	EventID    string            `json:"event_id"`
	Type       billing.EventType `json:"type"`
	Object     string            `json:"object"`
	ObjectID   string            `json:"object_id"`
	OccurredAt string            `json:"occurred_at"`
	Delivery   deliveryView      `json:"delivery"`
}

// endpointDeliveries is GET /console/webhooks/:id/deliveries: everything one
// webhook endpoint was sent, newest first, paged.
func (h *consoleHandlers) endpointDeliveries(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	q, err := readHistoryQuery(c)
	if err != nil {
		return historyQueryProblem(c, err)
	}
	endpoint, err := h.hooks.GetEndpoint(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	page, err := h.hooks.DeliveriesOfEndpoint(c.Context(), t.OrganizationID, t.Livemode, endpoint.ID, q.cursor, q.limit)
	if err != nil {
		return fail(c, err)
	}
	u := safeURL(endpoint.URL)
	out := make([]endpointDeliveryView, 0, len(page.Items))
	for _, e := range page.Items {
		var attempts []billing.DeliveryAttempt
		if q.attempts {
			if attempts, err = h.hooks.Attempts(c.Context(), t.OrganizationID, t.Livemode, e.Event.ID, endpoint.ID); err != nil {
				return fail(c, err)
			}
		}
		out = append(out, endpointDeliveryView{
			EventID: e.Event.ID, Type: e.Event.Type, Object: e.Event.Entity, ObjectID: e.Event.EntityID,
			OccurredAt: e.Event.OccurredAt, Delivery: newDeliveryView(e.Delivery, u, attempts),
		})
	}
	return c.JSON(listResponse[endpointDeliveryView]{Data: out, HasMore: page.LastEvaluatedKey != nil, Cursor: repositories.EncodeCursor(page.LastEvaluatedKey)})
}
