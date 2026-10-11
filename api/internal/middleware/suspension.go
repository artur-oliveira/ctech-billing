package middleware

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// TenantRows reads a tenant row (OrganizationRepository satisfies it).
type TenantRows interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

// RefuseSuspendedWrites answers 403 tenant_suspended to a write of a suspended
// live tenant (spec § 4.3). After ResolveTenant. Reads pass untouched and test
// mode is never suspended, so neither reads the tenant row: the cost is one
// GetItem per live write (plan decision 3). A read failure refuses the write.
func RefuseSuspendedWrites(orgs TenantRows) fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		t := GetTenant(c)
		if !t.Livemode {
			return c.Next()
		}
		org, err := orgs.Get(c.Context(), t.OrganizationID, true)
		if err != nil {
			slog.Error("m2m: reading the tenant for the suspension gate failed", "tenant", t.OrganizationID, "error", err)
			return problem.Internal("could not resolve organization").WithCode("organization_resolve_failed").Send(c)
		}
		if org.Suspended() {
			return problem.New(fiber.StatusForbidden, problem.TypeTenantSuspended, "Tenant suspended",
				"this organization's live billing is suspended; reads still work").WithCode("tenant_suspended").Send(c)
		}
		c.Locals(OrganizationKey, org)
		return c.Next()
	}
}
