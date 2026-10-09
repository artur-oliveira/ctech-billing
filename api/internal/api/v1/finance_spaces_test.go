package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

type fakeLister struct {
	orgs   []accountclient.Organization
	err    error
	askedF string
}

func (f *fakeLister) Organizations(_ context.Context, userID string) ([]accountclient.Organization, error) {
	f.askedF = userID
	return f.orgs, f.err
}

type spacesBody struct {
	Spaces []struct {
		Kind           string   `json:"kind"`
		OrganizationID string   `json:"organization_id"`
		Label          string   `json:"label"`
		Role           string   `json:"role"`
		Verbs          []string `json:"verbs"`
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

func TestSpacesListsPersonalFirstThenOrganizationsWithTheirVerbs(t *testing.T) {
	l := &fakeLister{orgs: []accountclient.Organization{
		{ID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", DisplayName: "Acme", Role: "admin"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff", DisplayName: "Beta", Role: "viewer"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-eeeeeeeeeeee", DisplayName: "Gone", Role: "auditor"}, // a role with no verbs
	}}
	code, b := getSpaces(t, spacesApp(l, userClaims))
	if code != 200 || len(b.Spaces) != 3 {
		t.Fatalf("status %d, %d spaces: %+v", code, len(b.Spaces), b)
	}
	if b.Spaces[0].Kind != "personal" || b.Spaces[0].Label != "Pessoal" || len(b.Spaces[0].Verbs) != 5 {
		t.Fatalf("first = %+v, want personal with every verb", b.Spaces[0])
	}
	if b.Spaces[1].Label != "Acme" || b.Spaces[1].Role != "admin" || len(b.Spaces[1].Verbs) != 5 {
		t.Fatalf("Acme = %+v", b.Spaces[1])
	}
	if b.Spaces[2].Label != "Beta" || len(b.Spaces[2].Verbs) != 1 || b.Spaces[2].Verbs[0] != "finance.read" {
		t.Fatalf("Beta = %+v, want only finance.read", b.Spaces[2])
	}
	if b.OrganizationsUnavailable {
		t.Fatal("organizations_unavailable on a healthy answer")
	}
}

func TestAnAccountOutageKeepsThePersonalSpaceAndSaysSo(t *testing.T) {
	code, b := getSpaces(t, spacesApp(&fakeLister{err: errors.New("timeout")}, userClaims))
	if code != 200 || len(b.Spaces) != 1 || b.Spaces[0].Kind != "personal" || !b.OrganizationsUnavailable {
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
	if l.askedF != "" {
		t.Fatalf("account was asked about %q for a refused caller", l.askedF)
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
	if l.askedF != "alice" {
		t.Fatalf("account was asked about %q, want the token's subject alice", l.askedF)
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
