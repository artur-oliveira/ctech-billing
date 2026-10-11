package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/accountorgs"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const (
	acctA = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	acctB = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
	acctC = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"
)

// fakeMerchants answers by "account org|mode".
type fakeMerchants map[string]*billing.Organization

func (f fakeMerchants) GetByAccountOrganization(_ context.Context, acct string, live bool) (*billing.Organization, error) {
	if o, ok := f[acct+"|"+repositories.Mode(live)]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

type sessionBody struct {
	Organizations []struct {
		OrganizationID string `json:"organization_id"`
		Selector       string `json:"selector"`
		DisplayName    string `json:"display_name"`
		Role           string `json:"role"`
		MerchantStatus string `json:"merchant_status"`
	} `json:"organizations"`
	OrganizationsUnavailable bool `json:"organizations_unavailable"`
}

func sessionApp(h *consoleHandlers, claims *middleware.Claims) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals(middleware.ClaimsKey, claims)
		}
		return c.Next()
	})
	app.Get("/console/session", middleware.RequireUserScope(middleware.ScopeOrganizationRead), h.session)
	return app
}

func getSession(t *testing.T, app *fiber.App) (int, string, sessionBody) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/console/session", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var b sessionBody
	_ = json.Unmarshal(raw, &b)
	return resp.StatusCode, string(raw), b
}

var sessionClaims = &middleware.Claims{Sub: "alice", SID: "s", Scope: middleware.ScopeOrganizationRead}

func TestSessionListsOwnerAndAdminOrganizationsWithTheirMerchantStatus(t *testing.T) {
	l := &fakeLister{orgs: map[string][]accountorgs.Organization{"alice": {
		{ID: acctC, DisplayName: "zeta", Role: "admin", Kind: "organization"},
		{ID: acctA, DisplayName: "CTech", Role: "owner", Kind: "organization"},
		{ID: acctB, DisplayName: "beta", Role: "owner"}, // no kind: an organization
		{ID: "0190a1b2-c3d4-7e5f-8a9b-dddddddddddd", DisplayName: "Member here", Role: "member", Kind: "organization"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-eeeeeeeeeeee", DisplayName: "Viewer here", Role: "viewer", Kind: "organization"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff", DisplayName: "Casa", Role: "owner", Kind: "personal"},
		{ID: "USER#bob", DisplayName: "Bad id", Role: "owner", Kind: "organization"},
	}}}
	m := fakeMerchants{
		acctA + "|live": {ID: "ctech", Livemode: true}, acctA + "|test": {ID: "ctech"},
		acctB + "|test": {ID: "org_b"},
	}
	code, raw, b := getSession(t, sessionApp(&consoleHandlers{spaces: l, merchants: m}, sessionClaims))
	if code != 200 || b.OrganizationsUnavailable {
		t.Fatalf("%d %s", code, raw)
	}
	got := []string{}
	for _, o := range b.Organizations {
		got = append(got, o.DisplayName+"/"+o.Role+"/"+o.MerchantStatus+"/"+o.Selector)
	}
	want := []string{
		"beta/owner/test/org:" + acctB,
		"CTech/owner/live/org:" + acctA,
		"zeta/admin/none/org:" + acctC,
	}
	if len(got) != len(want) {
		t.Fatalf("organizations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("organizations = %v, want %v", got, want)
		}
	}
	if len(l.asked) != 1 || l.asked[0] != "alice" {
		t.Fatalf("asked ctech-account about %v, want only the subject", l.asked)
	}
}

func TestSessionWithNoOrganizationsIsAnEmptyListNotNull(t *testing.T) {
	_, raw, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{}, merchants: fakeMerchants{}}, sessionClaims))
	if raw != `{"organizations":[],"organizations_unavailable":false}` {
		t.Fatalf("body = %s", raw)
	}
}

func TestSessionSaysOrganizationsAreUnavailableWhenCtechAccountIsDown(t *testing.T) {
	code, raw, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{err: errors.New("timeout")}, merchants: fakeMerchants{}}, sessionClaims))
	if code != 200 || raw != `{"organizations":[],"organizations_unavailable":true}` {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestSessionRefusesServiceTokens(t *testing.T) {
	code, _, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{}, merchants: fakeMerchants{}},
		&middleware.Claims{Sub: "cli", Scope: middleware.ScopeOrganizationRead}))
	if code != 403 {
		t.Fatalf("status %d", code)
	}
}

// The wire shape parts 2 and 3 and the console read.
func TestMerchantOrganizationWireShape(t *testing.T) {
	raw, err := json.Marshal(consoleSessionResponse{Organizations: []merchantOrganizationDTO{{
		OrganizationID: acctA, Selector: "org:" + acctA, DisplayName: "CTech", Role: "owner", MerchantStatus: billing.MerchantLive,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"organizations":[{"organization_id":"` + acctA + `","selector":"org:` + acctA +
		`","display_name":"CTech","role":"owner","merchant_status":"live"}],"organizations_unavailable":false}`
	if string(raw) != want {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
}
