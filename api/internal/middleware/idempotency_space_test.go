package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// scopeProbe runs a scope function inside a real request so it sees real locals.
func scopeProbe(t *testing.T, locals map[string]any, scope func(fiber.Ctx) (string, bool, bool)) (owner string, live, ok bool) {
	t.Helper()
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		for k, v := range locals {
			c.Locals(k, v)
		}
		owner, live, ok = scope(c)
		return c.SendStatus(204)
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	return
}

func TestTheSpaceScopeReadsTheResolvedSpaceOnly(t *testing.T) {
	sp, _ := space.ForJob("USER#alice", true)
	// A credential in the locals (an M2M path) must be ignored by the space scope.
	cred := &billing.APICredential{OrganizationID: "someone-else", Livemode: false}
	owner, live, ok := scopeProbe(t, map[string]any{SpaceKey: sp, CredentialKey: cred}, spaceScope)
	if !ok || owner != "USER#alice" || !live {
		t.Fatalf("scope = %q %v %v, want USER#alice live", owner, live, ok)
	}
	if _, _, ok := scopeProbe(t, nil, spaceScope); ok {
		t.Fatal("an unresolved space produced a scope")
	}
}

func TestTheCredentialScopeStillReadsTheCredential(t *testing.T) {
	cred := &billing.APICredential{OrganizationID: "org-1", Livemode: true}
	sp, _ := space.ForJob("USER#alice", false)
	owner, live, ok := scopeProbe(t, map[string]any{CredentialKey: cred, SpaceKey: sp}, credentialScope)
	if !ok || owner != "org-1" || !live {
		t.Fatalf("scope = %q %v %v", owner, live, ok)
	}
	if _, _, ok := scopeProbe(t, nil, credentialScope); ok {
		t.Fatal("no credential produced a scope")
	}
}

func idemApp(locals map[string]any) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		for k, v := range locals {
			c.Locals(k, v)
		}
		return c.Next()
	})
	// A nil store is fine for the refusals below: they happen before any lookup.
	app.Post("/w", SpaceIdempotency(nil, time.Now), func(c fiber.Ctx) error { return c.SendStatus(201) })
	return app
}

func TestSpaceIdempotencyRefusesAMissingKey(t *testing.T) {
	sp, _ := space.ForJob("USER#alice", true)
	resp, err := idemApp(map[string]any{SpaceKey: sp}).Test(httptest.NewRequest("POST", "/w", nil))
	if err != nil || resp.StatusCode != 400 {
		t.Fatalf("status %d, %v; want 400 before the handler runs", resp.StatusCode, err)
	}
}

func TestSpaceIdempotencyRefusesAnUnresolvedSpace(t *testing.T) {
	req := httptest.NewRequest("POST", "/w", nil)
	req.Header.Set(IdempotencyHeader, "k1")
	resp, err := idemApp(nil).Test(req)
	if err != nil || resp.StatusCode != 500 {
		t.Fatalf("status %d, %v; want 500: ResolveSpace must run first", resp.StatusCode, err)
	}
}
