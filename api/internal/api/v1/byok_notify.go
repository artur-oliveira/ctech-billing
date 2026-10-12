package v1

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
)

const maxNotifyBody = 64 << 10

// registerByokNotify mounts the webhook Lambda's call (spec 2026-10-11 § 6.3).
// No tenant resolver: the hook row decides the organization. Only a service
// token with internal:billing:byok-notify (the billing-byok-webhook client)
// reaches it.
func registerByokNotify(v1 fiber.Router, d Deps, auth fiber.Handler, clock func() time.Time) {
	if d.ByokWebhooks == nil {
		return
	}
	hooks := d.ByokWebhooks
	v1.Post("/internal/byok/notify", auth, middleware.RequireM2MScope(middleware.ScopeByokNotify), func(c fiber.Ctx) error {
		body := c.Body()
		if len(body) > maxNotifyBody {
			return problem.New(fiber.StatusRequestEntityTooLarge, "/problems/payload-too-large", "Payload Too Large", "the notice is too large").
				WithCode("payload_too_large").Send(c)
		}
		var in services.NotifyRequest
		if err := json.Unmarshal(body, &in); err != nil || in.HookID == "" || in.Provider == "" || len(in.ChargeRefs) > services.MaxNotifyRefs {
			return problem.BadRequest("invalid notice").WithCode("invalid_body").Send(c)
		}
		status := hooks.Notify(c.Context(), in, clock())
		if status == fiber.StatusOK {
			return c.SendStatus(status)
		}
		if status == fiber.StatusNotFound {
			return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
		}
		return problem.New(status, problem.TypeProviderUnavailable, "Provider Unavailable", "the bank could not be re-queried; retry").
			WithCode("byok_provider_unavailable").Send(c)
	})
}
