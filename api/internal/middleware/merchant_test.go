package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// tenants answers by "account org|mode"; calls proves "no table read".
type tenants struct {
	byAccount map[string]*billing.Organization
	err       error
	calls     int
}

func (f *tenants) GetByAccountOrganization(_ context.Context, acct string, live bool) (*billing.Organization, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if o, ok := f.byAccount[acct+"|"+repositories.Mode(live)]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

var tenantA = &billing.Organization{ID: "org_a", Livemode: true, AccountOrganizationID: orgA}

// merchantApp mounts the production chain in front of a handler that echoes
// what it resolved. sid "" makes a service token.
func merchantApp(src *members, ts MerchantTenants, sub, sid string) (*fiber.App, *bool) {
	ran := false
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, &Claims{Sub: sub, SID: sid, Scope: ScopeInvoicesRead})
		return c.Next()
	})
	r := space.NewResolver(src, cache.NewMemoryBackend(100))
	app.Get("/console/invoices",
		RequireUserSession(), ResolveSpace(r), ResolveMerchantTenant(ts),
		func(c fiber.Ctx) error {
			ran = true
			a := GetMerchantAccess(c)
			return c.SendString(GetTenant(c).OrganizationID + "|" + GetOrganization(c).ID + "|" + a.AccountOrganizationID + "|" + a.Role + "|" + GetSpace(c).Role())
		})
	return app, &ran
}

func get(t *testing.T, app *fiber.App, mode, sel string) (int, string) {
	t.Helper()
	return call(t, app, "GET", "/console/invoices", mode, sel)
}

func TestOwnersAndAdminsReachTheTenantOfTheSelectedOrganization(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		src := &members{m: map[string]string{orgA + "|alice": role}}
		ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
		app, ran := merchantApp(src, ts, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if want := "org_a|org_a|" + orgA + "|" + role + "|" + role; code != 200 || body != want || !*ran {
			t.Fatalf("%s: %d %q, want 200 %q", role, code, body, want)
		}
	}
}

func TestMembersAndViewersAreRefusedBeforeAnyTableRead(t *testing.T) {
	for _, role := range []string{"member", "viewer"} {
		src := &members{m: map[string]string{orgA + "|alice": role}}
		ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
		app, ran := merchantApp(src, ts, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if code != 403 || !strings.Contains(body, `"merchant_role_required"`) || *ran || ts.calls != 0 {
			t.Fatalf("%s: %d %q ran=%v reads=%d", role, code, body, *ran, ts.calls)
		}
	}
}

// personal, a personal workspace, another organization and an invented one:
// one 404, byte for byte, and no tenant read.
func TestEveryNonMerchantSpaceIsTheSame404(t *testing.T) {
	src := &members{
		m:     map[string]string{orgB + "|bob": "owner", orgB + "|alice": "owner"},
		kinds: map[string]string{orgB + "|alice": "personal"},
	}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	var bodies []string
	for _, sel := range []string{
		"personal",
		"org:" + orgB, // alice's personal workspace
		"org:" + orgA, // an organization alice is not in (it has a tenant)
		"org:0190a1b2-c3d4-7e5f-8a9b-aaaaaaaaaaaa", // invented
	} {
		code, body := get(t, app, "live", sel)
		if code != 404 || !strings.Contains(body, "/problems/space-not-found") {
			t.Fatalf("%s: %d %q", sel, code, body)
		}
		bodies = append(bodies, body)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("refusals differ:\n%s\n%s", bodies[0], b)
		}
	}
	if *ran || ts.calls != 0 {
		t.Fatalf("ran=%v reads=%d", *ran, ts.calls)
	}
}

func TestAnOrganizationWithoutATenantInThisModeIsNoMerchant(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "owner"}}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	code, body := get(t, app, "test", "org:"+orgA) // live tenant only
	if code != 404 || !strings.Contains(body, `"no_merchant"`) || !strings.Contains(body, "/problems/no-merchant") || *ran {
		t.Fatalf("%d %q ran=%v", code, body, *ran)
	}
}

func TestServiceTokensNeverReachTheResolver(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|cli_x": "owner"}}
	ts := &tenants{}
	app, ran := merchantApp(src, ts, "cli_x", "")
	code, body := get(t, app, "live", "org:"+orgA)
	if code != 403 || !strings.Contains(body, `"user_session_required"`) || *ran || src.calls != 0 || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v membership=%d reads=%d", code, body, *ran, src.calls, ts.calls)
	}
}

func TestMembershipOutageIs503AndReadsNothing(t *testing.T) {
	src := &members{err: errors.New("timeout")}
	ts := &tenants{}
	app, ran := merchantApp(src, ts, "alice", "sess")
	code, body := get(t, app, "live", "org:"+orgA)
	if code != 503 || !strings.Contains(body, "/problems/space-unavailable") || *ran || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v reads=%d", code, body, *ran, ts.calls)
	}
}

func TestMissingHeadersAre400(t *testing.T) {
	app, ran := merchantApp(&members{}, &tenants{}, "alice", "sess")
	for _, c := range []struct{ mode, sel string }{{"", "org:" + orgA}, {"live", ""}, {"prod", "org:" + orgA}} {
		if code, _ := get(t, app, c.mode, c.sel); code != 400 {
			t.Errorf("mode %q selector %q: %d, want 400", c.mode, c.sel, code)
		}
	}
	if *ran {
		t.Fatal("the handler ran")
	}
}

func TestATenantReadFailureIs500NotAnAnswer(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "owner"}}
	for _, err := range []error{errors.New("throttled"), repositories.ErrAccountOrganizationAmbiguous} {
		app, ran := merchantApp(src, &tenants{err: err}, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if code != 500 || !strings.Contains(body, `"organization_resolve_failed"`) || *ran {
			t.Fatalf("%v: %d %q", err, code, body)
		}
	}
}

// A cached "not a member" is a plain 404 for its 60 s, never a 503 or 500.
func TestACachedRefusalIsAPlain404(t *testing.T) {
	src := &members{m: map[string]string{}}
	app, _ := merchantApp(src, &tenants{}, "alice", "sess")
	for i := 0; i < 2; i++ {
		if code, _ := get(t, app, "live", "org:"+orgA); code != 404 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if src.calls != 1 {
		t.Fatalf("membership asked %d times, want 1 (cached)", src.calls)
	}
}

func TestAMemberIsRefusedWhateverTheTenantRows(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "member"}}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	if code, body := get(t, app, "live", "org:"+orgA); code != 403 || *ran || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v reads=%d", code, body, *ran, ts.calls)
	}
}

func TestRequireMerchantRoleWithoutASpaceIsAServerFault(t *testing.T) {
	app := fiber.New()
	app.Get("/x", RequireMerchantRole(), func(c fiber.Ctx) error { return c.SendString("ran") })
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status %d: a chain that forgot ResolveSpace must fail closed", resp.StatusCode)
	}
}
