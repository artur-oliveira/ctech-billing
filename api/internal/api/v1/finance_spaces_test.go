package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// fakeLister answers per user, as ctech-account does: each person's own list.
type fakeLister struct {
	orgs  map[string][]accountclient.Organization
	err   error
	asked []string
}

func (f *fakeLister) Organizations(_ context.Context, userID string) ([]accountclient.Organization, error) {
	f.asked = append(f.asked, userID)
	return f.orgs[userID], f.err
}

type spacesBody struct {
	Spaces []struct {
		Selector     string   `json:"selector"`
		Kind         string   `json:"kind"`
		DisplayName  string   `json:"display_name"`
		Role         string   `json:"role"`
		Verbs        []string `json:"verbs"`
		ManagePeople bool     `json:"manage_people"`
	} `json:"spaces"`
	OrganizationsUnavailable bool `json:"organizations_unavailable"`
}

func spacesApp(l spaceLister, claims *middleware.Claims) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals(middleware.ClaimsKey, claims)
		}
		return c.Next()
	})
	mountSpaces(app.Group("/console/finance"), &financeHandlers{spaces: l})
	return app
}

func getSpaces(t *testing.T, app *fiber.App) (int, spacesBody) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/console/finance/spaces", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var b spacesBody
	_ = json.Unmarshal(raw, &b)
	return resp.StatusCode, b
}

var userClaims = &middleware.Claims{Sub: "alice", SID: "s", Scope: middleware.ScopeFinanceRead}

func TestSpacesListPessoalThenPersonalWorkspacesThenOrganizations(t *testing.T) {
	l := &fakeLister{orgs: map[string][]accountclient.Organization{"alice": {
		{ID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", DisplayName: "acme", Role: "admin", Kind: "organization"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-dddddddddddd", DisplayName: "Viagem", Role: "viewer", Kind: "personal"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff", DisplayName: "Beta", Role: "viewer"}, // no kind: an organization
		{ID: "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc", DisplayName: "casa", Role: "owner", Kind: "personal"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-eeeeeeeeeeee", DisplayName: "Gone", Role: "auditor", Kind: "organization"}, // no verbs
		{ID: "0190a1b2-c3d4-7e5f-8a9b-999999999999", DisplayName: "Odd", Role: "admin", Kind: "personal"},        // admin on personal: none
		{ID: "0190a1b2-c3d4-7e5f-8a9b-888888888888", DisplayName: "Team", Role: "owner", Kind: "team"},           // unknown kind: none
		{ID: "USER#bob", DisplayName: "Bad id", Role: "owner", Kind: "personal"},                                 // not a workspace id
	}}}
	code, b := getSpaces(t, spacesApp(l, userClaims))
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	type row struct {
		sel, kind, name, role string
		verbs                 int
		manage                bool
	}
	want := []row{
		{"personal", "personal_default", "Pessoal", "", 5, false},
		{"org:0190a1b2-c3d4-7e5f-8a9b-cccccccccccc", "personal", "casa", "owner", 5, true},
		{"org:0190a1b2-c3d4-7e5f-8a9b-dddddddddddd", "personal", "Viagem", "viewer", 1, false},
		{"org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "organization", "acme", "admin", 5, false},
		{"org:0190a1b2-c3d4-7e5f-8a9b-ffffffffffff", "organization", "Beta", "viewer", 1, false},
	}
	if len(b.Spaces) != len(want) {
		t.Fatalf("%d spaces, want %d: %+v", len(b.Spaces), len(want), b.Spaces)
	}
	for i, w := range want {
		g := b.Spaces[i]
		if g.Selector != w.sel || g.Kind != w.kind || g.DisplayName != w.name || g.Role != w.role || len(g.Verbs) != w.verbs || g.ManagePeople != w.manage {
			t.Errorf("[%d] = %+v, want %+v", i, g, w)
		}
	}
	if b.OrganizationsUnavailable {
		t.Fatal("organizations_unavailable on a healthy answer")
	}
}

// § 9.9: the list is asked for the token's subject only, and holds only what
// ctech-account answered for that subject — never another person's workspace,
// whatever the request carries.
func TestTheSpacesListHoldsOnlyTheTokensOwnWorkspaces(t *testing.T) {
	bobs := "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
	l := &fakeLister{orgs: map[string][]accountclient.Organization{
		"alice": {{ID: "0190a1b2-c3d4-7e5f-8a9b-aaaaaaaaaaaa", DisplayName: "Casa", Role: "owner", Kind: "personal"}},
		"bob":   {{ID: bobs, DisplayName: "Bob", Role: "owner", Kind: "personal"}},
	}}
	app := spacesApp(l, userClaims)
	req := httptest.NewRequest("GET", "/console/finance/spaces?user_id=bob&sub=bob", nil)
	req.Header.Set("X-User-Id", "bob")
	req.Header.Set("X-Billing-Space", "org:"+bobs)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || strings.Contains(string(raw), bobs) || !strings.Contains(string(raw), "Casa") {
		t.Fatalf("%d %s: the list must be alice's alone", resp.StatusCode, raw)
	}
	if len(l.asked) != 1 || l.asked[0] != "alice" {
		t.Fatalf("ctech-account was asked about %v, want only alice", l.asked)
	}
}

func TestAnAccountOutageKeepsThePersonalSpaceAndSaysSo(t *testing.T) {
	code, b := getSpaces(t, spacesApp(&fakeLister{err: errors.New("timeout")}, userClaims))
	if code != 200 || len(b.Spaces) != 1 || b.Spaces[0].Kind != "personal_default" || b.Spaces[0].Selector != "personal" || !b.OrganizationsUnavailable {
		t.Fatalf("status %d body %+v, want 200 with only the personal space and organizations_unavailable", code, b)
	}
}

func TestSpacesNeedsTheReadScopeAndASignedInUser(t *testing.T) {
	l := &fakeLister{}
	for name, claims := range map[string]*middleware.Claims{
		"no claims":        nil,
		"no finance scope": {Sub: "alice", SID: "s", Scope: "openid"},
		"a service token":  {Sub: "svc", SID: "", Scope: middleware.ScopeFinanceRead},
	} {
		code, _ := getSpaces(t, spacesApp(l, claims))
		if code != 401 && code != 403 {
			t.Errorf("%s: status %d, want 401 or 403", name, code)
		}
	}
	if len(l.asked) != 0 {
		t.Fatalf("account was asked about %v for a refused caller", l.asked)
	}
}

// The list is asked for the token's own subject and nothing a request supplies.
func TestTheListIsAskedForTheTokensSubjectOnly(t *testing.T) {
	l := &fakeLister{}
	app := spacesApp(l, userClaims)
	req := httptest.NewRequest("GET", "/console/finance/spaces?user_id=bob&sub=bob", nil)
	req.Header.Set("X-User-Id", "bob")
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	if len(l.asked) != 1 || l.asked[0] != "alice" {
		t.Fatalf("account was asked about %v, want the token's subject alice", l.asked)
	}
}

// Every route in the table still runs the space chain; /spaces, which precedes
// space resolution, is the one deliberate exception and is not in the table.
func TestTheSpacesRouteIsNotInTheRouteTable(t *testing.T) {
	for _, r := range financeRoutes() {
		if r.Path == "/spaces" {
			t.Fatalf("%s %s is in the table: it would run ResolveSpace before any space exists", r.Method, r.Path)
		}
	}
}
