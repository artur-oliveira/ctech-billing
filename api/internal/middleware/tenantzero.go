package middleware

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// RequireTenantZero admits only requests whose resolved tenant is tenant zero
// (spec § 4.2): the application review. Any other tenant gets the plain
// not-found body, so the route does not say it exists. After the merchant chain.
func RequireTenantZero(tenantZero string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if tenantZero == "" || GetTenant(c).OrganizationID != tenantZero {
			return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
		}
		return c.Next()
	}
}
