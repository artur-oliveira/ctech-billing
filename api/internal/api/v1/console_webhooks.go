package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// webhookEndpointResponse never has a secret field: the secret is only in
// webhookSecretResponse, which only create and rotate answer with.
type webhookEndpointResponse struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"` // [] is every event
	Status      string   `json:"status"`
	FailureRun  int      `json:"failure_run"`
	DisabledAt  string   `json:"disabled_at,omitempty"`
	Provisioned bool     `json:"provisioned"`
	AddedAt     string   `json:"added_at,omitempty"`
	OwnerKey    string   `json:"owner_key,omitempty"`
}

type webhookSecretResponse struct {
	webhookEndpointResponse
	Secret string `json:"secret"`
}

type webhookListResponse struct {
	Data         []webhookEndpointResponse `json:"data"`
	EventTypes   []billing.EventType       `json:"event_types"`
	MaxEndpoints int                       `json:"max_endpoints"`
}

type createWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

func newWebhookEndpointResponse(e *billing.WebhookEndpoint) webhookEndpointResponse {
	events := make([]string, 0, len(e.Events))
	for _, t := range e.Events {
		events = append(events, string(t))
	}
	return webhookEndpointResponse{
		ID: e.ID, URL: e.URL, Events: events, Status: string(e.Status), FailureRun: e.FailureRun,
		DisabledAt: e.DisabledAt, Provisioned: e.Provisioned(), AddedAt: e.AddedAt, OwnerKey: e.OwnerKey,
	}
}

func (h *consoleHandlers) listWebhooks(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	endpoints, err := h.hooks.ListEndpoints(c.Context(), t.OrganizationID, t.Livemode)
	if err != nil {
		return fail(c, err)
	}
	out := webhookListResponse{
		Data:         make([]webhookEndpointResponse, 0, len(endpoints)),
		EventTypes:   billing.WebhookEventTypes(),
		MaxEndpoints: limits.MaxWebhookEndpoints,
	}
	for i := range endpoints {
		out.Data = append(out.Data, newWebhookEndpointResponse(&endpoints[i]))
	}
	return c.JSON(out)
}

func (h *consoleHandlers) createWebhook(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	var req createWebhookRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.webhookURL("url", req.URL)
	events := ch.webhookEvents("events", req.Events)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	t := middleware.GetTenant(c)
	now := h.now()
	e := &billing.WebhookEndpoint{
		ID: id.NewWithPrefix(id.PrefixWebhookEndpoint), OrganizationID: t.OrganizationID, Livemode: t.Livemode,
		URL: req.URL, Secret: billing.NewWebhookSecret(), Events: events, Status: billing.EndpointActive,
		AddedBy: me(c), AddedAt: now.UTC().Format(time.RFC3339),
	}
	err := h.hooks.AddEndpoint(c.Context(), e, limits.MaxWebhookEndpoints, actorOfUser(c), middleware.GetRequestID(c), now)
	switch {
	case errors.Is(err, repositories.ErrEndpointLimit):
		return problem.Unprocessable("this mode already has the most webhook endpoints allowed").WithCode("webhook_endpoint_limit").Send(c)
	case errors.Is(err, repositories.ErrEndpointsChanged):
		return problem.New(http.StatusConflict, problem.TypeConcurrentUpdate, "Concurrent Update",
			"the endpoints changed while saving; try again").WithCode("concurrent_update").Send(c)
	case err != nil:
		return fail(c, err)
	}
	return c.Status(http.StatusCreated).JSON(webhookSecretResponse{newWebhookEndpointResponse(e), e.Secret})
}

// consoleEndpoint reads the endpoint named in the path and refuses a
// provisioned one: every write below is for merchant endpoints only.
func (h *consoleHandlers) consoleEndpoint(c fiber.Ctx) (*billing.WebhookEndpoint, error) {
	t := middleware.GetTenant(c)
	e, err := h.hooks.GetEndpoint(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return nil, fail(c, err)
	}
	if e.Provisioned() {
		return nil, problem.Conflict("this endpoint is managed by CTech").WithCode("webhook_endpoint_provisioned").Send(c)
	}
	return e, nil
}

// deleteWebhook is allowed on a suspended tenant (scope decision 1).
func (h *consoleHandlers) deleteWebhook(c fiber.Ctx) error {
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	if err := h.hooks.RemoveEndpoint(c.Context(), e, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

// rotateWebhookSecret is allowed on a suspended tenant: a leaked secret must be
// replaceable whatever the account's standing.
func (h *consoleHandlers) rotateWebhookSecret(c fiber.Ctx) error {
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	secret := billing.NewWebhookSecret()
	if err := h.hooks.RotateEndpointSecret(c.Context(), e, secret, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(webhookSecretResponse{newWebhookEndpointResponse(e), secret})
}

func (h *consoleHandlers) testWebhook(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	if e.Status != billing.EndpointActive {
		return problem.Conflict("this endpoint is disabled").WithCode("webhook_endpoint_disabled").Send(c)
	}
	ev, err := h.hooks.EmitPing(c.Context(), e, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(http.StatusAccepted).JSON(fiber.Map{"event_id": ev.ID})
}
