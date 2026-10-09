package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/space"
)

const (
	orgA = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	orgB = "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff"
)

type members struct {
	calls int
	m     map[string]string // "org|user" -> role
	err   error
}

// Membership answers organizations ("" kind, read as organization).
func (f *members) Membership(_ context.Context, org, user string) (string, string, bool, error) {
	f.calls++
	if f.err != nil {
		return "", "", false, f.err
	}
	role, ok := f.m[org+"|"+user]
	return "", role, ok, nil
}

// spaceApp mounts the real chain: claims -> user scope -> ResolveSpace ->
// RequireVerb(Read) -> a handler that records that it ran.
func spaceApp(src *members, sub string) (*fiber.App, *bool) {
	ran := false
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, &Claims{Sub: sub, SID: "sess", Scope: ScopeFinanceRead + " " + ScopeFinanceWrite})
		return c.Next()
	})
	r := space.NewResolver(src, cache.NewMemoryBackend(100))
	app.Get("/space",
		RequireUserScope(ScopeFinanceRead), ResolveSpace(r), RequireVerb(space.Read),
		func(c fiber.Ctx) error {
			ran = true
			return c.SendString(GetSpace(c).PK())
		})
	app.Post("/write",
		RequireUserScope(ScopeFinanceWrite), ResolveSpace(r), RequireVerb(space.Write),
		func(c fiber.Ctx) error { ran = true; return c.SendString("ok") })
	return app, &ran
}

func call(t *testing.T, app *fiber.App, method, path, mode, sel string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if mode != "" {
		req.Header.Set(ModeHeader, mode)
	}
	if sel != "" {
		req.Header.Set(SpaceHeader, sel)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPersonalSpaceNeedsNoMembership(t *testing.T) {
	src := &members{}
	app, ran := spaceApp(src, "alice")
	code, body := call(t, app, "GET", "/space", "live", "personal")
	if code != 200 || body != "USER#alice#live" || !*ran || src.calls != 0 {
		t.Fatalf("%d %q ran=%v calls=%d", code, body, *ran, src.calls)
	}
}

// The three refusals a prober can cause must be indistinguishable.
func TestEveryRefusalOfAnOrganizationIsTheSame404(t *testing.T) {
	src := &members{m: map[string]string{orgB + "|bob": "owner"}}
	app, ran := spaceApp(src, "alice")
	var bodies []string
	for _, sel := range []string{
		"org:" + orgB,        // real organization, alice is not a member
		"org:" + orgA,        // organization that does not exist
		"org:USER#bob",       // key-shaped injection
		"org:" + orgA + "#x", // malformed id
	} {
		code, body := call(t, app, "GET", "/space", "live", sel)
		if code != 404 {
			t.Fatalf("%s: status %d", sel, code)
		}
		bodies = append(bodies, body)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("refusals differ:\n%s\n%s", bodies[0], b)
		}
	}
	if *ran {
		t.Fatal("the handler ran for a refused space")
	}
	if want := 2; src.calls != want { // injection and malformed ids never reach the source
		t.Fatalf("membership asked %d times, want %d", src.calls, want)
	}
}

func TestAnOrganizationMemberResolves(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "member"}}
	app, _ := spaceApp(src, "alice")
	code, body := call(t, app, "GET", "/space", "test", "org:"+orgA)
	if code != 200 || body != orgA+"#test" {
		t.Fatalf("%d %q", code, body)
	}
}

func TestAViewerCannotWrite(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "viewer"}}
	app, ran := spaceApp(src, "alice")
	if code, _ := call(t, app, "POST", "/write", "live", "org:"+orgA); code != 403 || *ran {
		t.Fatalf("status %d ran=%v", code, *ran)
	}
}

func TestAccountOutageIs503NotA404(t *testing.T) {
	src := &members{err: errors.New("timeout")}
	app, ran := spaceApp(src, "alice")
	code, body := call(t, app, "GET", "/space", "live", "org:"+orgA)
	if code != 503 || *ran || !strings.Contains(body, "/problems/space-unavailable") {
		t.Fatalf("%d %q ran=%v", code, body, *ran)
	}
	// Personal keeps working during the outage.
	if code, _ := call(t, app, "GET", "/space", "live", "personal"); code != 200 {
		t.Fatalf("personal during outage: %d", code)
	}
}

func TestMissingOrMalformedHeadersAre400(t *testing.T) {
	app, ran := spaceApp(&members{}, "alice")
	for _, c := range []struct{ mode, sel string }{
		{"", "personal"}, {"live", ""}, {"prod", "personal"}, {"live", "everyone"}, {"live", "personal:bob"}, {"live", "org:"},
	} {
		if code, _ := call(t, app, "GET", "/space", c.mode, c.sel); code != 400 {
			t.Errorf("mode %q selector %q: status %d, want 400", c.mode, c.sel, code)
		}
	}
	if *ran {
		t.Fatal("the handler ran")
	}
}
