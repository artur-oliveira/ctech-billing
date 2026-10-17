package middleware

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// ClosingFlags answers whether a billing tenant row is read-only because its
// owner confirmed closing it (repositories.ClosureRepository satisfies it).
type ClosingFlags interface {
	ReadOnly(ctx context.Context, tenantOrgID string) (bool, error)
}

// RefuseClosingWrites answers 403 tenant_closing to a write of a tenant being
// closed, in either mode (spec T2). After ResolveTenant or ResolveMerchantTenant.
// Reads pass untouched: one GetItem per write, fail closed on a read error. A nil
// flags makes it a no-op, which is the feature being off.
func RefuseClosingWrites(flags ClosingFlags) fiber.Handler {
	return func(c fiber.Ctx) error {
		if flags == nil {
			return c.Next()
		}
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		t := GetTenant(c)
		if t.OrganizationID == "" {
			return c.Next()
		}
		ro, err := flags.ReadOnly(c.Context(), t.OrganizationID)
		if err != nil {
			slog.Error("closing gate: reading the flag failed", "tenant", t.OrganizationID, "error", err)
			return problem.Internal("could not resolve organization").WithCode("organization_resolve_failed").Send(c)
		}
		if ro {
			return problem.New(fiber.StatusForbidden, problem.TypeTenantClosing, "Tenant closing",
				"this billing account is being closed; reads still work").WithCode("tenant_closing").Send(c)
		}
		return c.Next()
	}
}
