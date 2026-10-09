package v1

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/space"
)

const (
	gateOrg      = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	gatePersonal = "0190a1b2-c3d4-7e5f-8a9b-aaaaaaaaaaaa" // a personal workspace (ADR 0027)
	gateOddKind  = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb" // a kind billing does not know
)

// roleSource answers membership by user name: the user IS the role ("viewer"
// holds role viewer, and so on). The workspace's kind comes from the id asked.
type roleSource struct{}

func (roleSource) Membership(_ context.Context, org, user string) (string, string, bool, error) {
	switch org {
	case gateOrg:
		return "organization", user, true, nil
	case gatePersonal:
		return "personal", user, true, nil
	case gateOddKind:
		return "team", user, true, nil
	}
	return "", "", false, nil
}

// gateApp mounts every finance route with the REAL chain (financeChain) and a
// probe in place of each handler, so the test proves the gate, not the handler.
func gateApp(t *testing.T) (*fiber.App, map[string]bool) {
	t.Helper()
	idemRan := map[string]bool{}
	resolver := space.NewResolver(roleSource{}, cache.NewMemoryBackend(100))
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		scope := c.Get("X-Test-Scope")
		c.Locals(middleware.ClaimsKey, &middleware.Claims{Sub: c.Get("X-Test-User"), SID: "s", Scope: scope})
		return c.Next()
	})
	idem := func(c fiber.Ctx) error {
		idemRan[c.Method()+" "+c.Route().Path[len("/console/finance"):]] = true
		return c.Next()
	}
	mountFinance(app.Group("/console/finance"), resolver, idem, func(financeRoute) fiber.Handler {
		return func(c fiber.Ctx) error { return c.SendStatus(200) }
	})
	return app, idemRan
}

func gateCall(t *testing.T, app *fiber.App, r financeRoute, user, scope string) int {
	t.Helper()
	return gateCallIn(t, app, r, gateOrg, user, scope)
}

// gateCallIn is gateCall in workspace ws.
func gateCallIn(t *testing.T, app *fiber.App, r financeRoute, ws, user, scope string) int {
	t.Helper()
	path := strings.ReplaceAll("/console/finance"+r.Path, ":id", "x")
	req := httptest.NewRequest(r.Method, path, nil)
	req.Header.Set(middleware.ModeHeader, "live")
	req.Header.Set(middleware.SpaceHeader, "org:"+ws)
	req.Header.Set("X-Test-User", user)
	req.Header.Set("X-Test-Scope", scope)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestEveryFinanceRouteIsGatedByItsVerb(t *testing.T) {
	app, _ := gateApp(t)
	scopes := middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite
	for _, r := range financeRoutes() {
		for ws, kind := range map[string]space.Kind{gateOrg: space.KindOrganization, gatePersonal: space.KindPersonal} {
			for _, role := range []string{"viewer", "member", "admin", "owner"} {
				verbs := space.VerbsFor(kind, role)
				want := 200
				switch {
				case verbs == 0:
					want = 404 // no verbs is no space
				case !verbs.Has(r.Verb):
					want = 403
				}
				if got := gateCallIn(t, app, r, ws, role, scopes); got != want {
					t.Errorf("%s %s as %s of a %s: status %d, want %d", r.Method, r.Path, role, kind, got, want)
				}
			}
		}
	}
}

// The specific promises of spec § 6.1, stated outright rather than only through
// the table: a member configures nothing, a viewer writes nothing.
func TestTheRolesMatchTheSpecTable(t *testing.T) {
	app, _ := gateApp(t)
	scopes := middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite
	find := func(method, path string) financeRoute {
		for _, r := range financeRoutes() {
			if r.Method == method && r.Path == path {
				return r
			}
		}
		t.Fatalf("no route %s %s", method, path)
		return financeRoute{}
	}
	for _, c := range []struct {
		method, path, role string
		want               int
	}{
		{"POST", "/accounts", "member", 403},
		{"POST", "/accounts/:id/archive", "member", 403},
		{"PUT", "/settings/default-receiving-account", "member", 403},
		{"PUT", "/settings/post-ctech-invoices", "member", 403},
		{"PUT", "/settings/post-ctech-invoices", "admin", 200},
		{"POST", "/accounts", "admin", 200},
		{"POST", "/bills", "viewer", 403},
		{"POST", "/bills/:id/settle", "viewer", 403},
		{"POST", "/bills/:id/settle", "member", 200}, // member holds finance.settle
		{"GET", "/bills", "viewer", 200},
		{"GET", "/projection", "viewer", 200},
		{"POST", "/recurrences/preview", "viewer", 200}, // stateless: a read
		{"GET", "/recurrences/:id/occurrences", "viewer", 200},
		{"PATCH", "/recurrences/:id", "viewer", 403},
		{"POST", "/imports", "viewer", 403},
		{"POST", "/imports", "member", 200}, // member holds finance.import
		{"GET", "/imports/:id", "viewer", 200},
		{"POST", "/imports/:id/lines/:n/new", "member", 200},
		{"POST", "/imports/:id/lines/:n/match", "viewer", 403},
		{"POST", "/imports/:id/lines/:n/ignore", "viewer", 403},
		{"PUT", "/accounts/:id/csv-mapping", "viewer", 403},
		{"PUT", "/accounts/:id/csv-mapping", "member", 200},
	} {
		if got := gateCall(t, app, find(c.method, c.path), c.role, scopes); got != c.want {
			t.Errorf("%s %s as %s: %d, want %d", c.method, c.path, c.role, got, c.want)
		}
	}
}

func TestWriteRoutesNeedTheWriteScopeAndReadsOnlyTheReadScope(t *testing.T) {
	app, _ := gateApp(t)
	for _, r := range financeRoutes() {
		got := gateCall(t, app, r, "owner", middleware.ScopeFinanceRead)
		if r.Write && got != 403 {
			t.Errorf("%s %s ran with only the read scope: %d", r.Method, r.Path, got)
		}
		if !r.Write && got != 200 {
			t.Errorf("%s %s refused the read scope: %d", r.Method, r.Path, got)
		}
	}
}

func TestOnlyWriteRoutesRunTheIdempotencyLayer(t *testing.T) {
	app, ran := gateApp(t)
	scopes := middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite
	for _, r := range financeRoutes() {
		gateCall(t, app, r, "owner", scopes)
		if got := ran[r.Method+" "+r.Path]; got != r.Write {
			t.Errorf("%s %s: idempotency ran = %v, want %v", r.Method, r.Path, got, r.Write)
		}
	}
}

// A refused space must stop before the verb, the idempotency layer and the handler.
func TestARefusedSpaceNeverReachesTheHandlerOrTheStore(t *testing.T) {
	app, ran := gateApp(t)
	for _, r := range financeRoutes() {
		path := strings.ReplaceAll("/console/finance"+r.Path, ":id", "x")
		req := httptest.NewRequest(r.Method, path, nil)
		req.Header.Set(middleware.ModeHeader, "live")
		req.Header.Set(middleware.SpaceHeader, "org:0190a1b2-c3d4-7e5f-8a9b-ffffffffffff") // not the role source's org
		req.Header.Set("X-Test-User", "owner")
		req.Header.Set("X-Test-Scope", middleware.ScopeFinanceRead+" "+middleware.ScopeFinanceWrite)
		resp, err := app.Test(req)
		if err != nil || resp.StatusCode != 404 {
			t.Errorf("%s %s: %d %v, want 404", r.Method, r.Path, resp.StatusCode, err)
		}
		if ran[r.Method+" "+r.Path] {
			t.Errorf("%s %s reached the idempotency layer for a refused space", r.Method, r.Path)
		}
	}
}

// § 9.2: Leitura in a personal workspace writes nothing — every write route in
// the table, not a sample — and still reads everything.
func TestAViewerInAPersonalWorkspaceIsRefusedOnEveryWriteRoute(t *testing.T) {
	app, _ := gateApp(t)
	scopes := middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite
	writes := 0
	for _, r := range financeRoutes() {
		got := gateCallIn(t, app, r, gatePersonal, "viewer", scopes)
		if r.Verb != space.Read && got != 403 {
			t.Errorf("%s %s as a personal-workspace viewer: %d, want 403", r.Method, r.Path, got)
		}
		if r.Verb == space.Read && got != 200 {
			t.Errorf("%s %s (a read) as a personal-workspace viewer: %d, want 200", r.Method, r.Path, got)
		}
		if r.Write {
			writes++
			if r.Verb == space.Read {
				t.Errorf("%s %s writes but is gated by finance.read", r.Method, r.Path)
			}
		}
	}
	if writes < 20 {
		t.Fatalf("only %d write routes walked: the table shrank or the test stopped reading it", writes)
	}
}

// Acesso total is full access, configure included — unlike an organization member.
func TestAMemberOfAPersonalWorkspaceConfigures(t *testing.T) {
	app, _ := gateApp(t)
	scopes := middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite
	for _, r := range financeRoutes() {
		if got := gateCallIn(t, app, r, gatePersonal, "member", scopes); got != 200 {
			t.Errorf("%s %s as a personal-workspace member: %d, want 200", r.Method, r.Path, got)
		}
	}
}

// § 9.3 through the real chain: admin on personal, or a kind billing does not
// know, is the same 404 as a space that does not exist — byte for byte.
func TestAPersonalWorkspaceAdminOrUnknownKindIsTheOrdinary404(t *testing.T) {
	app, _ := gateApp(t)
	get := func(ws, user string) (int, string) {
		req := httptest.NewRequest("GET", "/console/finance/accounts", nil)
		req.Header.Set(middleware.ModeHeader, "live")
		req.Header.Set(middleware.SpaceHeader, "org:"+ws)
		req.Header.Set("X-Test-User", user)
		req.Header.Set("X-Test-Scope", middleware.ScopeFinanceRead)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	wantCode, wantBody := get("0190a1b2-c3d4-7e5f-8a9b-999999999999", "owner") // nobody's
	if wantCode != 404 {
		t.Fatalf("a space nobody holds answered %d", wantCode)
	}
	for _, c := range [][2]string{{gatePersonal, "admin"}, {gateOddKind, "owner"}} {
		code, got := get(c[0], c[1])
		if code != wantCode || got != wantBody {
			t.Errorf("%s as %s: %d %s, want %d %s", c[0], c[1], code, got, wantCode, wantBody)
		}
	}
}
