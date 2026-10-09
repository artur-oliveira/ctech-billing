package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// SpaceHeader carries the console's space selector: "personal" or "org:{id}".
// It is a request, never a permission (ADR 0025).
//
// MUST match the constant in ui/src/lib/api/client.ts — never rename.
const SpaceHeader = "X-Billing-Space"

// SpaceKey is the Fiber locals key of the ResolvedSpace.
const SpaceKey = "space"

func spaceNotFound() *problem.Problem {
	return problem.New(fiber.StatusNotFound, problem.TypeSpaceNotFound, "Space not found", "espaço não encontrado")
}

// ResolveSpace turns the token's subject plus the X-Billing-Space and mode headers
// into a ResolvedSpace, or refuses. It runs before any table read.
//
// Every "no" about an organization — not a member, no such organization, an id
// that is not even shaped like one — is the same 404, byte for byte. The one
// different answer is ctech-account being unreachable, which is a 503 because
// pretending it was a 404 would hide an outage as a refusal, and pretending it
// was a 200 would be an authorization bypass.
func ResolveSpace(r *space.Resolver) fiber.Handler {
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

		sel, err := space.ParseSelector(c.Get(SpaceHeader))
		if err != nil {
			if errors.Is(err, space.ErrSpaceNotFound) {
				return spaceNotFound().Send(c)
			}
			return problem.BadRequest("informe " + SpaceHeader + ": personal ou org:{id}").Send(c)
		}

		sp, err := r.Resolve(c.Context(), cl.Sub, sel, livemode)
		switch {
		case err == nil:
			c.Locals(SpaceKey, sp)
			return c.Next()
		case errors.Is(err, space.ErrSpaceNotFound):
			return spaceNotFound().Send(c)
		case errors.Is(err, space.ErrInvalidSubject):
			return problem.Unauthorized("credenciais inválidas").Send(c)
		default: // ErrSpaceUnavailable and anything unforeseen: fail closed
			return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable,
				"Space unavailable", "não foi possível verificar o acesso agora").Send(c)
		}
	}
}

// GetSpace returns the resolved space. The zero value (no ResolveSpace ran) is
// refused by every repository, so forgetting the middleware cannot read data.
func GetSpace(c fiber.Ctx) space.ResolvedSpace {
	s, _ := c.Locals(SpaceKey).(space.ResolvedSpace)
	return s
}

// RequireVerb gates a route on a verb the resolved space holds.
func RequireVerb(v space.Verbs) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !GetSpace(c).Can(v) {
			return problem.Forbidden("seu papel não permite esta operação").Send(c)
		}
		return c.Next()
	}
}
