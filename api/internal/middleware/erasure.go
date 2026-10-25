package middleware

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// RefuseLocked refuses every request made with a session token whose subject is
// locked or erased in the account-deletion saga (ctech-account
// docs/specs/2026-10-06-account-deletion-saga-protocol.md § 4.2, § 4.5).
//
// It runs inside Verifier.Middleware, which every user-facing group already
// mounts, so it is the one place where a person's `sub` enters billing.
// Service tokens (empty SID) pass: their subject is a client, not a person.
// The public checkout and wallet's notify-back carry no token at all. Inbound
// money is accepted, and the next eligibility check reports it (§ 4.2).
//
// Every method, not only writes: one rule is easier to prove than a list of
// safe reads. A failed lookup is a 500 and never a pass. The lock is the
// second layer behind the revocation list, and failing open would make it no
// layer at all.
func RefuseLocked(blocked func(ctx context.Context, sub string) (bool, error)) fiber.Handler {
	return refuseLocked(blocked, nil)
}

// refuseLocked is RefuseLocked that hands a passing request to tail instead of
// c.Next (nil tail = c.Next), so Verifier.Middleware can chain the revocation
// guard behind it without a second handler at every route.
func refuseLocked(blocked func(ctx context.Context, sub string) (bool, error), tail fiber.Handler) fiber.Handler {
	next := func(c fiber.Ctx) error {
		if tail != nil {
			return tail(c)
		}
		return c.Next()
	}
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.SID == "" {
			return next(c)
		}
		locked, err := blocked(c.Context(), cl.Sub)
		if err != nil {
			return problem.Internal("erro ao verificar a conta").WithCause(err).Send(c)
		}
		if locked {
			return problem.New(fiber.StatusForbidden, problem.TypeAccountLocked, "Account locked",
				"conta em processo de exclusão").Send(c)
		}
		return next(c)
	}
}
