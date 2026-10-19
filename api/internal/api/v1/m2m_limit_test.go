package v1

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

func limitKey(t *testing.T, tenantZero string, tenant middleware.Tenant) string {
	t.Helper()
	var got string
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.Locals(middleware.TenantKey, tenant)
		got = m2mLimitKey(tenantZero)(c)
		return nil
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheM2MLimitCountsMerchantsPerModeAndNeverTenantZero(t *testing.T) {
	if k := limitKey(t, "org_zero", middleware.Tenant{OrganizationID: "org_a", Livemode: true}); k != "org_a|true" {
		t.Fatalf("live key = %q", k)
	}
	if k := limitKey(t, "org_zero", middleware.Tenant{OrganizationID: "org_a"}); k != "org_a|false" {
		t.Fatalf("test traffic must have its own budget: %q", k)
	}
	if k := limitKey(t, "org_zero", middleware.Tenant{OrganizationID: "org_zero", Livemode: true}); k != "" {
		t.Fatalf("the platform's own traffic must not be limited: %q", k)
	}
	if k := limitKey(t, "", middleware.Tenant{}); k != "" {
		t.Fatalf("no tenant, no key: %q", k)
	}
}
