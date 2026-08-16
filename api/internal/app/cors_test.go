package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

const portalOrigin = "https://billing.aoctech.app"

// newFiber is the whole CORS surface, so these exercise it directly rather than
// building the app — no DynamoDB, no JWKS, no wallet.
func corsApp(t *testing.T, origins ...string) *fiber.App {
	t.Helper()
	app := newFiber(&config.Config{CorsAllowedOrigins: origins})
	app.Get("/v1.0/portal/session", func(c fiber.Ctx) error { return c.SendString("ok") })
	app.Post("/v1.0/customers", func(c fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func request(t *testing.T, app *fiber.App, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Credentials are allowed, matching ctech-wallet and ctech-poker. The pairing
// with an exact origin is not a style choice: the spec forbids credentials
// alongside a wildcard, and Fiber refuses the combination outright.
func TestConfiguredOriginsAllowCredentials(t *testing.T) {
	res := request(t, corsApp(t, portalOrigin), http.MethodGet, "/v1.0/portal/session",
		map[string]string{fiber.HeaderOrigin: portalOrigin})

	if got := res.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != portalOrigin {
		t.Errorf("allow-origin = %q, want %q", got, portalOrigin)
	}
	if got := res.Header.Get(fiber.HeaderAccessControlAllowCredentials); got != "true" {
		t.Errorf("allow-credentials = %q, want true", got)
	}
	if got := res.Header.Get(fiber.HeaderAccessControlAllowOrigin); got == "*" {
		t.Error("a wildcard alongside credentials — the browser rejects this and the spec forbids it")
	}
}

// The laptop default the siblings ship. config.Load refuses to boot production
// in this state, so the permissive branch cannot reach a customer — which is
// the only reason it is acceptable at all.
func TestUnconfiguredOriginsNeverAllowCredentials(t *testing.T) {
	res := request(t, corsApp(t), http.MethodGet, "/v1.0/portal/session",
		map[string]string{fiber.HeaderOrigin: "https://anything.example"})

	if got := res.Header.Get(fiber.HeaderAccessControlAllowCredentials); got == "true" {
		t.Fatal("credentials allowed with no configured origins: any page on the internet would carry the reader's cookies")
	}
}

// Every header any surface sends has to survive a preflight, or it fails in the
// browser and nowhere else — no test, no log, no server-side trace.
func TestPreflightAllowsEveryHeaderTheClientSends(t *testing.T) {
	res := request(t, corsApp(t, portalOrigin), http.MethodOptions, "/v1.0/customers", map[string]string{
		fiber.HeaderOrigin:                     portalOrigin,
		fiber.HeaderAccessControlRequestMethod: http.MethodPost,
	})
	allowed := strings.ToLower(res.Header.Get(fiber.HeaderAccessControlAllowHeaders))

	for _, h := range []string{
		fiber.HeaderAuthorization,
		fiber.HeaderContentType,
		middleware.IdempotencyHeader,
		middleware.ModeHeader,
	} {
		if !strings.Contains(allowed, strings.ToLower(h)) {
			t.Errorf("preflight does not allow %q (allows %q)", h, allowed)
		}
	}
}

// The browser hides every response header cross-origin unless it is exposed,
// and both of these are acted on by a client.
func TestReadableResponseHeadersAreExposed(t *testing.T) {
	res := request(t, corsApp(t, portalOrigin), http.MethodGet, "/v1.0/portal/session",
		map[string]string{fiber.HeaderOrigin: portalOrigin})
	exposed := strings.ToLower(res.Header.Get(fiber.HeaderAccessControlExposeHeaders))

	for _, h := range []string{middleware.RequestIDHeader, "Idempotent-Replay"} {
		if !strings.Contains(exposed, strings.ToLower(h)) {
			t.Errorf("%q is not exposed (exposes %q)", h, exposed)
		}
	}
}

// The failure a suffix or prefix check would have. `evil-aoctech.app` ends with
// nothing on the list; `billing.aoctech.app.evil.com` *starts* with an allowed
// origin. Only whole-string comparison refuses both — and with credentials
// allowed, one of these getting through is a cross-site read of a signed-in
// reader's data.
func TestLookalikeOriginsAreRefused(t *testing.T) {
	app := corsApp(t, portalOrigin)

	for _, origin := range []string{
		"https://evil.com",
		"https://billing.aoctech.app.evil.com",
		"https://evil-aoctech.app",
		"http://billing.aoctech.app",
		"https://billing.aoctech.app:8443",
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			res := request(t, app, http.MethodGet, "/v1.0/portal/session",
				map[string]string{fiber.HeaderOrigin: origin})
			if got := res.Header.Get(fiber.HeaderAccessControlAllowOrigin); got == origin {
				t.Errorf("allow-origin echoed %q", origin)
			}
			if got := res.Header.Get(fiber.HeaderAccessControlAllowCredentials); got == "true" &&
				res.Header.Get(fiber.HeaderAccessControlAllowOrigin) == origin {
				t.Errorf("%q was granted credentials", origin)
			}
		})
	}
}

// Every M2M integration, curl and the health probe sends no Origin. Refusing
// those would break the surface that carries all the traffic.
func TestRequestsWithoutAnOriginStillSucceed(t *testing.T) {
	res := request(t, corsApp(t, portalOrigin), http.MethodGet, "/v1.0/portal/session", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}
