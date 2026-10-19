package v1

import (
	"strconv"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// m2mLimitKey is what the M2M budget is counted on: the merchant tenant and its
// mode, so test traffic never eats the live budget. Tenant zero is the platform
// itself: wallet, dfe and poker call these routes for every one of their own
// users, so its traffic is the product's, not an integration to hold back. No
// resolved tenant is not limited (the resolver refuses it before this).
func m2mLimitKey(tenantZero string) func(fiber.Ctx) string {
	return func(c fiber.Ctx) string {
		t := middleware.GetTenant(c)
		if t.OrganizationID == "" || t.OrganizationID == tenantZero {
			return ""
		}
		return t.OrganizationID + "|" + strconv.FormatBool(t.Livemode)
	}
}
