package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
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
			return problem.Forbidden("esta rota exige sessão de usuário").Send(c)
		}
		if !cl.HasScope(scope) {
			return problem.Forbidden("escopo insuficiente: " + scope).Send(c)
		}
		return c.Next()
	}
}

// ResolveConsoleTenant turns a signed-in person into the organization and mode
// their request acts for (ADR 0011).
//
// The organization comes from **who the token says they are**, never from the
// request: there is still no organization_id parameter, exactly as on the M2M
// surface. What does come from the request is the mode, and only here — a person
// legitimately works in both test and live within one session, which an
// integration never does. It is required rather than defaulted: a console that
// forgets the header should fail loudly, not quietly show the wrong world.
func ResolveConsoleTenant(orgs *repositories.OrganizationRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}

		var livemode bool
		switch c.Get(ModeHeader) {
		case "live":
			livemode = true
		case "test":
			livemode = false
		default:
			return problem.BadRequest("informe " + ModeHeader + ": live ou test").Send(c)
		}

		org, err := orgs.GetByOwner(c.Context(), cl.Sub, livemode)
		if err != nil {
			if errors.Is(err, repositories.ErrNotFound) {
				// The same answer whether the person owns no organization or owns
				// one only in the other mode. Distinguishing the two would let a
				// signed-in stranger probe which organizations exist.
				return problem.Forbidden("nenhuma organização para este usuário").Send(c)
			}
			return problem.Internal("erro ao resolver organização").Send(c)
		}

		c.Locals(OrganizationKey, org)
		c.Locals(TenantKey, Tenant{OrganizationID: org.ID, Livemode: org.Livemode})
		return c.Next()
	}
}

// GetOrganization returns the console session's organization.
func GetOrganization(c fiber.Ctx) *billing.Organization {
	org, _ := c.Locals(OrganizationKey).(*billing.Organization)
	return org
}
