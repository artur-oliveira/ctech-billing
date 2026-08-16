//go:build integration

package integration

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// TestCatalogIsReadableByAnIntegration is 1.1's contract: an M2M token with
// billing:products:read lists the catalogue, and a browser session token does
// not — even holding the same scope.
//
// The second half is the one worth having. The catalogue handler is shared with
// the console, and a shared handler mounted on the wrong group is how a
// user-session token comes to act as an integration.
func TestCatalogIsReadableByAnIntegration(t *testing.T) {
	e := newAPI(t)

	m2m := e.token(t, e.client, "", middleware.ScopeProductsRead)
	res := e.do(t, http.MethodGet, "/v1.0/products", m2m, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	res.decode(t, &list)
	if len(list.Data) == 0 {
		t.Fatalf("the tenant's catalogue came back empty: %s", res.body)
	}

	// The detail read carries the prices, which is where the quota metadata an
	// integration enforces actually lives.
	res = e.do(t, http.MethodGet, "/v1.0/products/"+list.Data[0].ID, m2m, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("get product: %d %s", res.status, res.body)
	}
	var product struct {
		Prices []struct {
			ID         string `json:"id"`
			UnitAmount int64  `json:"unit_amount"`
		} `json:"prices"`
	}
	res.decode(t, &product)
	if len(product.Prices) == 0 {
		t.Fatalf("a product must publish its prices: %s", res.body)
	}

	session := e.token(t, e.client, "sess_123", middleware.ScopeProductsRead)
	res = e.do(t, http.MethodGet, "/v1.0/products", session, "", "")
	if res.status != http.StatusForbidden {
		t.Fatalf("a session token on the M2M catalogue = %d, want 403: %s", res.status, res.body)
	}

	// And the scope is enforced, not merely the token kind.
	wrongScope := e.token(t, e.client, "", middleware.ScopeInvoicesRead)
	res = e.do(t, http.MethodGet, "/v1.0/products", wrongScope, "", "")
	if res.status != http.StatusForbidden {
		t.Fatalf("without billing:products:read = %d, want 403: %s", res.status, res.body)
	}
}

// TestEntitlementsCarryThePlanAndTheOpenInvoice is 1.2: the consuming product
// must be able to render its whole billing screen from this one call.
//
// The subscription here is a paid plan billed in advance, so it starts
// INCOMPLETE with an unpaid first invoice — which is exactly the state where
// `open_invoice.checkout_url` matters, because it is the link that turns "não
// entitled" into "pague aqui".
func TestEntitlementsCarryThePlanAndTheOpenInvoice(t *testing.T) {
	e := newAPI(t)
	token := e.token(t, e.client, "",
		middleware.ScopeCustomersWrite, middleware.ScopeSubscriptionsWrite, middleware.ScopeEntitlementsRead)

	res := e.do(t, http.MethodPost, "/v1.0/customers", token, "cust-ent",
		`{"name":"Fulana de Tal","external_ref":"user_ent"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("create customer: %d %s", res.status, res.body)
	}
	var customer struct {
		ID string `json:"id"`
	}
	res.decode(t, &customer)

	body := `{"customer_id":"` + customer.ID + `","items":[{"price_id":"` + e.priceID + `"}],"anchor":"2026-03-10"}`
	res = e.do(t, http.MethodPost, "/v1.0/subscriptions", token, "sub-ent", body)
	if res.status != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", res.status, res.body)
	}

	res = e.do(t, http.MethodGet, "/v1.0/entitlements?customer_ref=user_ent", token, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("entitlements: %d %s", res.status, res.body)
	}
	var ent struct {
		Subscriptions []struct {
			Status            string `json:"status"`
			Entitled          bool   `json:"entitled"`
			CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
			Items             []struct {
				PriceID    string `json:"price_id"`
				ProductID  string `json:"product_id"`
				Type       string `json:"type"`
				UnitAmount int64  `json:"unit_amount"`
			} `json:"items"`
			OpenInvoice *struct {
				ID         string `json:"id"`
				TotalCents int64  `json:"total_cents"`
				DueDate    string `json:"due_date"`
			} `json:"open_invoice"`
		} `json:"subscriptions"`
	}
	res.decode(t, &ent)

	if len(ent.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1: %s", len(ent.Subscriptions), res.body)
	}
	got := ent.Subscriptions[0]
	if got.Status != "INCOMPLETE" || got.Entitled || got.CancelAtPeriodEnd {
		t.Fatalf("subscription = %+v", got)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v, want the one price the subscription bills", got.Items)
	}
	if got.Items[0].PriceID != e.priceID || got.Items[0].Type != "fixed" || got.Items[0].UnitAmount != 4990 {
		t.Fatalf("item = %+v", got.Items[0])
	}
	if got.OpenInvoice == nil {
		t.Fatalf("a paid plan awaiting its first payment must publish the open invoice: %s", res.body)
	}
	if got.OpenInvoice.TotalCents != 4990 || got.OpenInvoice.DueDate == "" {
		t.Fatalf("open invoice = %+v", *got.OpenInvoice)
	}
}
