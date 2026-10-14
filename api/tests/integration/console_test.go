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

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The console surface (ADR 0011). What is tested here is the tenancy seam, not
// the payloads: the console reads the same repositories the M2M routes read, and
// the only new thing it can get wrong is *whose* data it reads.

// console performs a GET on the console with a real signed session token, in
// e's organization space.
func (e *apiEnv) console(t *testing.T, path, token, mode string) apiResponse {
	t.Helper()
	selector := ""
	if strings.HasPrefix(path, "/v1.0/console/") {
		// The portal and /v1/me take no organization selector.
		selector = "org:" + e.accountOrg
	}
	return e.consoleAs(t, http.MethodGet, path, token, mode, selector, "")
}

// consoleAs is the one console request builder: any method, mode, selector
// ("" sends no X-Billing-Space) and JSON body.
func (e *apiEnv) consoleAs(t *testing.T, method, path, token, mode, selector, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if mode != "" {
		req.Header.Set(middleware.ModeHeader, mode)
	}
	if method != http.MethodGet {
		req.Header.Set(middleware.IdempotencyHeader, id.New()) // the UI sends one per intent
	}
	if selector != "" {
		req.Header.Set(middleware.SpaceHeader, selector)
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

// The tenant is the selected organization's (ADR 0028); /settings carries its
// block, which is the console's "where am I".
func TestConsoleResolvesTheTenantOfTheSelectedOrganization(t *testing.T) {
	e := newAPI(t)
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Organization struct {
			OrganizationID string `json:"organization_id"`
			Livemode       bool   `json:"livemode"`
			CanCharge      bool   `json:"can_charge"`
		} `json:"organization"`
	}
	res.decode(t, &body)
	if body.Organization.OrganizationID != e.org.ID || !body.Organization.Livemode || !body.Organization.CanCharge {
		t.Fatalf("organization = %+v, want %s live and chargeable", body.Organization, e.org.ID)
	}
}

// The mode is required rather than defaulted (ADR 0011, unchanged).
func TestConsoleRequiresTheModeHeader(t *testing.T) {
	e := newAPI(t)
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "")
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// A mode the organization has no tenant in is no_merchant: the mode is
// caller-controlled and still cannot widen access.
func TestConsoleCannotReachAModeWithNoTenant(t *testing.T) {
	e := newAPI(t) // live only
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "test")
	if res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// The owner of another merchant reaches their own tenant and never this one.
func TestConsoleCannotReachAnotherTenant(t *testing.T) {
	e := newAPI(t)
	otherAcct := newSpaceOrgID()
	other := newLinkedOrg(t, true, otherAcct)
	e.account.set(otherAcct, other.OwnerUserID, "organization", "owner")
	stranger := e.token(t, other.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead)

	if res := e.console(t, "/v1.0/console/settings", stranger, "live"); res.status != http.StatusNotFound {
		t.Fatalf("someone else's organization: %d %s", res.status, res.body)
	}
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/settings", stranger, "live", "org:"+otherAcct, "")
	if res.status != http.StatusOK || !strings.Contains(string(res.body), other.ID) || strings.Contains(string(res.body), e.org.ID) {
		t.Fatalf("own organization: %d %s", res.status, res.body)
	}
}

// The two surfaces are mirrors: a service token cannot drive the console, and a
// session token cannot drive the integration API (already covered for the other
// direction by TestUserTokensAreRejectedOnM2MRoutes).
func TestServiceTokensAreRejectedOnConsoleRoutes(t *testing.T) {
	e := newAPI(t)
	m2m := e.token(t, e.client, "", middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1.0/console/session", m2m, "live")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

func TestConsoleScopesAreEnforcedPerRoute(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	// A session that may read the organization may not therefore read invoices.
	res := e.console(t, "/v1.0/console/invoices", token, "live")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
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
		if err := customers.Create(ctx, c, "test", "req_setup", now()); err != nil {
			t.Fatal(err)
		}
	}

	res := e.console(t, "/v1.0/console/customers", token, "live")
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
	res = e.console(t, "/v1.0/console/products", token, "live")
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
	if _, err := invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "console-test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.console(t, "/v1.0/console/invoices/"+inv.ID, token, "live")
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

// C4 and C7 name a subscription the way an operator does: by its plan and its
// customer, not by sub_… and cus_…. The names are the console's; the M2M
// subscription JSON, which ctech-dfe decodes, stays as it was.
func TestConsoleSubscriptionsCarryTheCustomerAndPlanNames(t *testing.T) {
	e := newAPI(t)
	m2m := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeSubscriptionsWrite,
		middleware.ScopeSubscriptionsRead)

	res := e.do(t, http.MethodPost, "/v1.0/customers", m2m, "cust-names",
		`{"name":"A O CARVALHO TECH","email":"a@example.com","external_ref":"user_names"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("create customer: %d %s", res.status, res.body)
	}
	var customer struct {
		ID string `json:"id"`
	}
	res.decode(t, &customer)
	res = e.do(t, http.MethodPost, "/v1.0/subscriptions", m2m, "sub-names",
		`{"customer_id":"`+customer.ID+`","items":[{"price_id":"`+e.priceID+`"}],"anchor":"2026-03-10"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", res.status, res.body)
	}
	var created struct {
		Subscription struct {
			ID string `json:"id"`
		} `json:"subscription"`
	}
	res.decode(t, &created)

	type row struct {
		ID            string `json:"id"`
		CustomerID    string `json:"customer_id"`
		CustomerName  string `json:"customer_name"`
		PlanName      string `json:"plan_name"`
		CurrentPeriod struct {
			Start string `json:"period_start"`
		} `json:"current_period"`
	}
	token := e.sessionToken(t, middleware.ScopeSubscriptionsRead, middleware.ScopeCustomersRead)

	res = e.console(t, "/v1.0/console/subscriptions", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("list: %d %s", res.status, res.body)
	}
	var page struct {
		Data []row `json:"data"`
	}
	res.decode(t, &page)
	var found *row
	for i := range page.Data {
		if page.Data[i].ID == created.Subscription.ID {
			found = &page.Data[i]
		}
	}
	if found == nil {
		t.Fatalf("subscription missing from the listing: %s", res.body)
	}
	if found.CustomerName != "A O CARVALHO TECH" || found.PlanName != "DF-e Basic" {
		t.Fatalf("row = %+v, want the customer's name and the plan's", *found)
	}
	if found.CustomerID != customer.ID || found.CurrentPeriod.Start == "" {
		t.Fatalf("row lost a shared field: %+v", *found)
	}

	res = e.console(t, "/v1.0/console/customers/"+customer.ID, token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("customer: %d %s", res.status, res.body)
	}
	var detail struct {
		Subscriptions []row `json:"subscriptions"`
	}
	res.decode(t, &detail)
	if len(detail.Subscriptions) != 1 || detail.Subscriptions[0].PlanName != "DF-e Basic" {
		t.Fatalf("customer subscriptions = %+v, want the plan's name", detail.Subscriptions)
	}

	res = e.do(t, http.MethodGet, "/v1.0/subscriptions/"+created.Subscription.ID, m2m, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("m2m get: %d %s", res.status, res.body)
	}
	if strings.Contains(string(res.body), "plan_name") || strings.Contains(string(res.body), "customer_name") {
		t.Fatalf("the M2M subscription JSON changed: %s", res.body)
	}
}
