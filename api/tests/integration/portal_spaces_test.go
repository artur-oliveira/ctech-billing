//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type portalSpacesEnv struct {
	*portalEnv
	account *fakeAccount
}

func newPortalSpaces(t *testing.T) portalSpacesEnv {
	t.Helper()
	p := newPortal(t)
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}}
	srv := acct.serve(t)
	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL, cfg.CtechJWKSURL, cfg.ServiceAudience = testIssuer, p.jwksURL, testAudience
	cfg.PortalOrganizationID = p.org.ID
	cfg.AccountBaseURL, cfg.AccountTokenURL = srv.URL, srv.URL+"/token"
	cfg.AccountClientID, cfg.AccountClientSecret = "billing-test", "secret"
	server, err := app.Build(ctxT(t), &cfg, func() time.Time { return now() })
	if err != nil {
		t.Fatal(err)
	}
	p.app = server
	return portalSpacesEnv{portalEnv: p, account: acct}
}

func (e portalSpacesEnv) get(t *testing.T, path, token, selector string) apiResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if selector != "" {
		req.Header.Set(middleware.SpaceHeader, selector)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return apiResponse{status: resp.StatusCode, body: []byte(b.String())}
}

// Review Focus 5.
func TestAnAdminWhoIsNotACustomerCanListAndPick(t *testing.T) {
	e := newPortalSpaces(t)
	ctx := ctxT(t)
	accountOrg := newSpaceOrgID()
	admin := "usr_" + id.New()
	e.account.set(accountOrg, admin, "organization", "admin")
	e.account.names[accountOrg] = "Acme"
	orgCustomer := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true, Name: "Acme LTDA", ExternalRef: "ORG_" + accountOrg}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, orgCustomer, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	inv := newInvoiceFor(t, e.org, orgCustomer.ID)
	due := brcal.New(2026, time.March, 10)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Finalize(ctx, inv, due, due, billing.CauseScheduler, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	tok := e.token(t, admin, "sess_"+id.New(), middleware.ScopeMySubscriptionsRead, middleware.ScopeMyInvoicesRead)
	list := e.get(t, "/v1.0/portal/spaces", tok, "")
	if list.status != 200 || !strings.Contains(string(list.body), "org:"+accountOrg) {
		t.Fatalf("spaces: %d %s", list.status, list.body)
	}
	if r := e.get(t, "/v1.0/portal/invoices", tok, "org:"+accountOrg); r.status != 200 || !strings.Contains(string(r.body), inv.ID) {
		t.Fatalf("invoices: %d %s", r.status, r.body)
	}
	if r := e.get(t, "/v1.0/portal/invoices", tok, ""); r.status != http.StatusForbidden {
		t.Fatalf("personal, no customer: %d", r.status)
	}
}

func TestPortalSpacesListOnlyOrganizationsTheyManage(t *testing.T) {
	e := newPortalSpaces(t)
	user := "usr_" + id.New()
	managed, member, personal := newSpaceOrgID(), newSpaceOrgID(), newSpaceOrgID()
	e.account.set(managed, user, "organization", "owner")
	e.account.set(member, user, "organization", "member")
	e.account.set(personal, user, "personal", "owner")
	r := e.get(t, "/v1.0/portal/spaces", e.token(t, user, "s", middleware.ScopeMySubscriptionsRead), "")
	body := string(r.body)
	if r.status != 200 || !strings.Contains(body, managed) || strings.Contains(body, member) || strings.Contains(body, personal) {
		t.Fatalf("%d %s", r.status, body)
	}
}

func TestAForgedPersonalWorkspaceIsA404(t *testing.T) {
	e := newPortalSpaces(t)
	user := "usr_" + id.New()
	ws := newSpaceOrgID()
	e.account.set(ws, user, "personal", "owner")
	if r := e.get(t, "/v1.0/portal/invoices", e.token(t, user, "s", middleware.ScopeMyInvoicesRead), "org:"+ws); r.status != 404 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
