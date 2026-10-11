package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

type rows struct {
	org   *billing.Organization
	err   error
	reads int
}

func (r *rows) Get(context.Context, string, bool) (*billing.Organization, error) {
	r.reads++
	return r.org, r.err
}

func suspensionApp(r *rows, live bool) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(TenantKey, Tenant{OrganizationID: "org_m", Livemode: live})
		return c.Next()
	})
	app.All("/x", RefuseSuspendedWrites(r), func(c fiber.Ctx) error { return c.SendString("ran") })
	return app
}

func hit(t *testing.T, app *fiber.App, method string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return resp.StatusCode, b.String()
}

func TestASuspendedLiveTenantCannotWrite(t *testing.T) {
	r := &rows{org: &billing.Organization{ID: "org_m", Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		code, body := hit(t, suspensionApp(r, true), m)
		if code != 403 || !strings.Contains(body, `"tenant_suspended"`) || !strings.Contains(body, "/problems/tenant-suspended") {
			t.Fatalf("%s: %d %s", m, code, body)
		}
	}
}

func TestReadsAndTestModeNeverReadTheTenantRow(t *testing.T) {
	r := &rows{org: &billing.Organization{ID: "org_m", Livemode: true, SuspendedAt: "x"}}
	if code, _ := hit(t, suspensionApp(r, true), "GET"); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if code, _ := hit(t, suspensionApp(r, false), "POST"); code != 200 {
		t.Fatalf("test-mode POST: %d", code)
	}
	if r.reads != 0 {
		t.Fatalf("%d tenant reads, want 0", r.reads)
	}
}

func TestAnActiveTenantWritesAndAReadFailureFailsClosed(t *testing.T) {
	if code, _ := hit(t, suspensionApp(&rows{org: &billing.Organization{ID: "org_m", Livemode: true}}, true), "POST"); code != 200 {
		t.Fatalf("active: %d", code)
	}
	code, body := hit(t, suspensionApp(&rows{err: errors.New("throttled")}, true), "POST")
	if code != 500 || !strings.Contains(body, `"organization_resolve_failed"`) {
		t.Fatalf("read failure: %d %s", code, body)
	}
}
