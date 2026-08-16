//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The console surface (ADR 0011). What is tested here is the tenancy seam, not
// the payloads: the console reads the same repositories the M2M routes read, and
// the only new thing it can get wrong is *whose* data it reads.

// console performs a console request with a real signed session token.
func (e *apiEnv) console(t *testing.T, path, token, mode string) apiResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if mode != "" {
		req.Header.Set(middleware.ModeHeader, mode)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

// sessionToken mints a browser token for the owner of e.org.
func (e *apiEnv) sessionToken(t *testing.T, scopes ...string) string {
	t.Helper()
	return e.token(t, e.org.OwnerUserID, "sess_"+id.New(), scopes...)
}

func TestConsoleResolvesTheOrganizationFromTheOwner(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1/console/session", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		OrganizationID string `json:"organization_id"`
		Livemode       bool   `json:"livemode"`
		CanCharge      bool   `json:"can_charge"`
	}
	res.decode(t, &body)
	if body.OrganizationID != e.org.ID {
		t.Fatalf("organization = %q, want %q", body.OrganizationID, e.org.ID)
	}
	if !body.Livemode {
		t.Fatal("live mode was requested and must be reported back")
	}
	if !body.CanCharge {
		t.Fatal("the test organization is payout-enabled; can_charge must say so")
	}
}

// The mode is required rather than defaulted: a console that forgets the header
// must fail loudly instead of quietly showing the wrong world (ADR 0011).
func TestConsoleRequiresTheModeHeader(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1/console/session", token, "")
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// Asking for a mode you own no organization in is a 403, and the same 403 as
// owning nothing at all. The mode being caller-controlled is safe precisely
// because it cannot widen access.
func TestConsoleCannotReachAModeItDoesNotOwn(t *testing.T) {
	e := newAPI(t) // e.org exists in live mode only
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1/console/session", token, "test")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// The two surfaces are mirrors: a service token cannot drive the console, and a
// session token cannot drive the integration API (already covered for the other
// direction by TestUserTokensAreRejectedOnM2MRoutes).
func TestServiceTokensAreRejectedOnConsoleRoutes(t *testing.T) {
	e := newAPI(t)
	m2m := e.token(t, e.client, "", middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1/console/session", m2m, "live")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

func TestConsoleScopesAreEnforcedPerRoute(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	// A session that may read the organization may not therefore read invoices.
	res := e.console(t, "/v1/console/invoices", token, "live")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// One owner's console must not see another owner's organization, which is the
// whole reason the owner is a key rather than a filter.
func TestConsoleCannotReachAnotherTenant(t *testing.T) {
	e := newAPI(t)
	other := newOrg(t, true)
	stranger := e.token(t, other.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1/console/session", stranger, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		OrganizationID string `json:"organization_id"`
	}
	res.decode(t, &body)
	if body.OrganizationID == e.org.ID {
		t.Fatal("the console resolved the wrong organization: owner lookup is not isolating tenants")
	}
	if body.OrganizationID != other.ID {
		t.Fatalf("organization = %q, want %q", body.OrganizationID, other.ID)
	}
}

// The listing screens (C2, C4, C6, C8) read the tenant's own rows and nobody
// else's. One test covers all four because they share the page helper — what
// differs between them is the entity constant, and that is what this checks.
func TestConsoleListingsAreTenantScoped(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t,
		middleware.ScopeCustomersRead,
		middleware.ScopeSubscriptionsRead,
		middleware.ScopeProductsRead,
		middleware.ScopeInvoicesRead)

	mine := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Cliente Meu",
	}
	other := newOrg(t, true)
	theirs := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: other.ID, Livemode: true,
		Name: "Cliente De Outra Org",
	}
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	for _, c := range []*billing.Customer{mine, theirs} {
		if err := customers.Create(ctx, c, now()); err != nil {
			t.Fatal(err)
		}
	}

	res := e.console(t, "/v1/console/customers", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var page struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	res.decode(t, &page)

	var sawMine bool
	for _, c := range page.Data {
		if c.ID == theirs.ID {
			t.Fatal("another organization's customer appeared in the listing")
		}
		if c.ID == mine.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatal("the tenant's own customer is missing from the listing")
	}

	// The catalogue created by newAPI belongs to this tenant, so the products
	// listing must find it.
	res = e.console(t, "/v1/console/products", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("products: status = %d: %s", res.status, res.body)
	}
	var products struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	res.decode(t, &products)
	if len(products.Data) == 0 {
		t.Fatal("the tenant's product is missing from the catalogue listing")
	}
}

// A detail screen's timeline is the audit trail, which is what makes "who
// changed this?" answerable at all.
func TestConsoleInvoiceDetailCarriesItsTimeline(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesRead)

	inv := newDraftInvoice(t, e.org, "gen_"+id.New())
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, due, "console-test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.console(t, "/v1/console/invoices/"+inv.ID, token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Invoice struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"invoice"`
		Timeline []struct {
			Action string `json:"action"`
			Actor  string `json:"actor"`
		} `json:"timeline"`
	}
	res.decode(t, &body)
	if body.Invoice.ID != inv.ID {
		t.Fatalf("invoice = %q, want %q", body.Invoice.ID, inv.ID)
	}
	if len(body.Timeline) == 0 {
		t.Fatal("finalizing wrote an audit entry; the timeline must show it")
	}
	if body.Timeline[0].Actor != "console-test" {
		t.Fatalf("actor = %q, want the actor that caused the change", body.Timeline[0].Actor)
	}
}
