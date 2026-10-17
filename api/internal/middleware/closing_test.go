package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type fakeFlags struct {
	ro  map[string]bool
	err error
}

func (f fakeFlags) ReadOnly(_ context.Context, id string) (bool, error) { return f.ro[id], f.err }

func closingApp(flags ClosingFlags) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(TenantKey, Tenant{OrganizationID: c.Get("X-Tenant"), Livemode: c.Get("X-Live") == "1"})
		return c.Next()
	})
	app.Use(RefuseClosingWrites(flags))
	app.All("/", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	return app
}

func TestAClosingTenantRefusesWritesInEitherModeAndServesReads(t *testing.T) {
	app := closingApp(fakeFlags{ro: map[string]bool{"closing": true}})
	for _, tc := range []struct {
		name, method, tenant, live string
		want                       int
	}{
		{"a read of a closing tenant", "GET", "closing", "1", 200},
		{"a live write", "POST", "closing", "1", 403},
		{"a test write", "POST", "closing", "0", 403},
		{"a write of another tenant", "POST", "other", "1", 200},
		{"a delete", "DELETE", "closing", "1", 403},
	} {
		req := httptest.NewRequest(tc.method, "/", nil)
		req.Header.Set("X-Tenant", tc.tenant)
		req.Header.Set("X-Live", tc.live)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, resp.StatusCode, tc.want, body)
		}
		if tc.want == 403 && !strings.Contains(string(body), "tenant_closing") {
			t.Errorf("%s: body %s does not carry tenant_closing", tc.name, body)
		}
	}
}

func TestTheClosingGateFailsClosedAndIsOffWithoutFlags(t *testing.T) {
	broken := closingApp(fakeFlags{err: errors.New("dynamodb: boom")})
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Tenant", "t")
	if resp, _ := broken.Test(req); resp.StatusCode != 500 {
		t.Fatalf("an unreadable flag: %d, want 500", resp.StatusCode)
	}
	off := closingApp(nil)
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Tenant", "t")
	if resp, _ := off.Test(req); resp.StatusCode != 200 {
		t.Fatalf("feature off: %d, want 200", resp.StatusCode)
	}
}
