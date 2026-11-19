package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/api-commons/pushsvc"
	"gopkg.aoctech.app/api-commons/validation"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/pendingpush"
)

// PushJobSender hands a push job to the sender Lambda (pushsvc.Invoker).
type PushJobSender interface {
	Send(ctx context.Context, job pushsvc.Job) error
}

// PushDeps is what the push routes need; app wires it from config and the AWS clients. Nil unmounts the routes.
type PushDeps struct {
	Service *pushsvc.Service
	Prefs   *pendingpush.Service
	Sender  PushJobSender
}

// pushScope is the finance scope a push route needs: read for GET, write for anything that changes state. Push is
// personal (a device belongs to a person), so RequireUserScope also refuses a service token.
func pushScope(c fiber.Ctx) error {
	scope := middleware.ScopeFinanceWrite
	if c.Method() == fiber.MethodGet {
		scope = middleware.ScopeFinanceRead
	}
	return middleware.RequireUserScope(scope)(c)
}

// mountPush registers /push/* under router (the authenticated /console/finance group): the device routes of pushsvc
// (config, subscriptions), the person's reminder preferences, and a test push to the caller's own devices.
// docs/specs/2026-11-18-pending-reminder-web-push.md.
func mountPush(router fiber.Router, d *PushDeps) {
	userID := func(c fiber.Ctx) string { return middleware.GetClaims(c).Sub }
	// router is already behind auth; the extra middleware is the scope check, so pushsvc's own auth slot is a pass-through.
	pushsvc.Mount(router, d.Service, func(c fiber.Ctx) error { return c.Next() }, userID, pushScope)

	g := router.Group("/push", pushScope)
	g.Get("/preferences", func(c fiber.Ctx) error {
		p, err := d.Prefs.Get(c.Context(), userID(c))
		if err != nil {
			return problem.FromError(err).Send(c)
		}
		return c.JSON(p)
	})
	g.Put("/preferences", func(c fiber.Ctx) error {
		var body pendingpush.Preferences
		if p := validation.BindJSON(c.Body(), &body); p != nil {
			return p.Send(c)
		}
		p, err := d.Prefs.Set(c.Context(), userID(c), body)
		if err != nil {
			return problem.FromError(err).Send(c)
		}
		return c.JSON(p)
	})
	g.Post("/test", func(c fiber.Ctx) error {
		if !d.Service.Enabled() {
			return problem.ServiceUnavailable("notificações desativadas nesta instalação").Send(c)
		}
		id := make([]byte, 12)
		_, _ = rand.Read(id)
		err := d.Sender.Send(c.Context(), pushsvc.Job{
			EventID: hex.EncodeToString(id), Type: "test", Title: "Teste de notificação",
			Body: "Se você viu isto, as notificações estão funcionando.", Navigate: "/", Tag: "test", Topic: "test",
			TTLSeconds: 60, Urgency: "high", Recipients: []string{userID(c)},
		})
		if err != nil {
			return problem.FromError(err).Send(c)
		}
		return c.SendStatus(fiber.StatusAccepted)
	})
}
