package middleware

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// ModeHeader carries test-vs-live for a console session.
//
// MUST match the constant in ui/src/lib/api/client.ts — never rename.
const ModeHeader = "X-Billing-Mode"

// RequireUserScope gates a route on a browser session token carrying the scope.
//
// It is the exact mirror of RequireM2MScope: that one rejects session tokens,
// this one rejects service tokens. An integration's credential must not be able
// to drive the console's routes, because the console's routes are where a human
// switches between test and live — and a token that chooses its own mode is the
// one thing ADR 0003 exists to prevent.
func RequireUserScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
		}
		if !cl.HasScope(scope) {
			return problem.Forbidden("escopo insuficiente: " + scope).Send(c)
		}
		return c.Next()
	}
}

// GetOrganization returns the console session's organization.
func GetOrganization(c fiber.Ctx) *billing.Organization {
	org, _ := c.Locals(OrganizationKey).(*billing.Organization)
	return org
}
