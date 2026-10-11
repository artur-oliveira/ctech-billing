//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// Spec § 10.1: a member, a viewer, an admin of another organization, an
// invented org:{id} and personal each get 404/403 from every billing-half
// route, never data. The routes come from the running app's own table, so a
// route added later is covered without editing this file.

var allConsoleScopes = []string{
	middleware.ScopeOrganizationRead,
	middleware.ScopeInvoicesRead, middleware.ScopeInvoicesWrite,
	middleware.ScopeSubscriptionsRead, middleware.ScopeSubscriptionsWrite,
	middleware.ScopeCustomersRead, middleware.ScopeCustomersWrite,
	middleware.ScopeProductsRead, middleware.ScopeProductsWrite,
}

var routeParam = regexp.MustCompile(`:[A-Za-z_]+`)

// billingHalfRoutes is every /v1.0/console route outside finance and the
// session, with its parameters filled.
func billingHalfRoutes(t *testing.T, app *fiber.App) []fiber.Route {
	t.Helper()
	var out []fiber.Route
	for _, r := range app.GetRoutes(true) {
		if !strings.HasPrefix(r.Path, "/v1.0/console/") ||
			strings.HasPrefix(r.Path, "/v1.0/console/finance") ||
			r.Path == "/v1.0/console/session" {
			continue
		}
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
			r.Path = routeParam.ReplaceAllString(r.Path, "zz_"+id.New())
			out = append(out, r)
		}
	}
	if len(out) < 20 {
		t.Fatalf("found %d billing-half routes; the route table changed shape", len(out))
	}
	return out
}

func TestNoNonMerchantReachesAnyBillingHalfRoute(t *testing.T) {
	e := newAPI(t)
	member, viewer, otherAdmin := "usr_"+id.New(), "usr_"+id.New(), "usr_"+id.New()
	e.account.set(e.accountOrg, member, "organization", "member")
	e.account.set(e.accountOrg, viewer, "organization", "viewer")
	otherOrg := newSpaceOrgID() // has no tenant
	e.account.set(otherOrg, otherAdmin, "organization", "admin")
	personalWS := newSpaceOrgID()
	e.account.set(personalWS, e.org.OwnerUserID, "personal", "owner")
	owner := e.org.OwnerUserID

	cases := []struct {
		name, sub, selector string
		status              int
		code                string
	}{
		{"member", member, "org:" + e.accountOrg, 403, "merchant_role_required"},
		{"viewer", viewer, "org:" + e.accountOrg, 403, "merchant_role_required"},
		{"admin of another organization", otherAdmin, "org:" + e.accountOrg, 404, "space_not_found"},
		{"invented organization", owner, "org:" + newSpaceOrgID(), 404, "space_not_found"},
		{"personal", owner, "personal", 404, "space_not_found"},
		{"personal workspace", owner, "org:" + personalWS, 404, "space_not_found"},
		{"organization with no tenant", otherAdmin, "org:" + otherOrg, 404, "no_merchant"},
		{"owner in a mode with no tenant", owner, "org:" + e.accountOrg, 404, "no_merchant"},
	}
	routes := billingHalfRoutes(t, e.app)
	for _, c := range cases {
		token := e.token(t, c.sub, "sess_"+id.New(), allConsoleScopes...)
		mode := "live"
		if c.name == "owner in a mode with no tenant" {
			mode = "test" // e.org exists in live only
		}
		for _, r := range routes {
			res := e.consoleAs(t, r.Method, r.Path, token, mode, c.selector, `{}`)
			if res.status != c.status || !bytes.Contains(res.body, []byte(`"`+c.code+`"`)) {
				t.Errorf("%s: %s %s = %d %s, want %d %s", c.name, r.Method, r.Path, res.status, res.body, c.status, c.code)
			}
			if bytes.Contains(res.body, []byte(e.org.ID)) {
				t.Errorf("%s: %s %s leaked the tenant id", c.name, r.Method, r.Path)
			}
		}
	}
}

func TestServiceTokensAndMissingHeadersNeverReachTheBillingHalf(t *testing.T) {
	e := newAPI(t)
	m2m := e.token(t, e.client, "", allConsoleScopes...)
	owner := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), allConsoleScopes...)
	for _, r := range billingHalfRoutes(t, e.app) {
		if res := e.consoleAs(t, r.Method, r.Path, m2m, "live", "org:"+e.accountOrg, `{}`); res.status != 403 ||
			!bytes.Contains(res.body, []byte(`"user_session_required"`)) {
			t.Errorf("m2m %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
		if res := e.consoleAs(t, r.Method, r.Path, owner, "live", "", `{}`); res.status != 400 {
			t.Errorf("no selector %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
		if res := e.consoleAs(t, r.Method, r.Path, owner, "", "org:"+e.accountOrg, `{}`); res.status != 400 {
			t.Errorf("no mode %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
	}
}

// An admin who is not the stored owner reaches the tenant: the role decides.
func TestAnAdminWhoIsNotTheStoredOwnerReachesTheTenant(t *testing.T) {
	e := newAPI(t)
	admin := "usr_" + id.New()
	e.account.set(e.accountOrg, admin, "organization", "admin")
	res := e.console(t, "/v1.0/console/settings", e.token(t, admin, "sess_"+id.New(), middleware.ScopeOrganizationRead), "live")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(e.org.ID)) {
		t.Fatalf("admin: %d %s", res.status, res.body)
	}
}

func TestSessionNeedsNoModeAndNoSpaceAndListsTheEnvOrganization(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session", token, "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Organizations []struct {
			OrganizationID string `json:"organization_id"`
			Role           string `json:"role"`
			MerchantStatus string `json:"merchant_status"`
		} `json:"organizations"`
	}
	res.decode(t, &body)
	if len(body.Organizations) != 1 || body.Organizations[0].OrganizationID != e.accountOrg ||
		body.Organizations[0].Role != "owner" || body.Organizations[0].MerchantStatus != "live" {
		t.Fatalf("organizations = %+v", body.Organizations)
	}
}

func TestAMemberSeesNoOrganizationInTheSession(t *testing.T) {
	e := newAPI(t)
	member := "usr_" + id.New()
	e.account.set(e.accountOrg, member, "organization", "member")
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session",
		e.token(t, member, "sess_"+id.New(), middleware.ScopeOrganizationRead), "", "", "")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(`"organizations":[]`)) {
		t.Fatalf("%d %s", res.status, res.body)
	}
}

func TestAnOrganizationWithATestTenantOnlyIsTest(t *testing.T) {
	e := newAPI(t)
	acct := newSpaceOrgID()
	test := newLinkedOrg(t, false, acct)
	e.account.set(acct, test.OwnerUserID, "organization", "owner")
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session",
		e.token(t, test.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead), "", "", "")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(`"merchant_status":"test"`)) {
		t.Fatalf("%d %s", res.status, res.body)
	}
}
