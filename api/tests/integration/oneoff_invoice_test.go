//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type oneOffInvoice struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Number      int64  `json:"number"`
	Total       int64  `json:"total"`
	AmountDue   int64  `json:"amount_due"`
	CustomerID  string `json:"customer_id"`
	CheckoutURL string `json:"checkout_url"`
	DueDate     string `json:"due_date"`
	Lines       []struct {
		Description string `json:"description"`
		Amount      int64  `json:"amount"`
	} `json:"lines"`
}

func (e *apiEnv) oneOffToken(t *testing.T) string {
	t.Helper()
	return e.token(t, e.client, "", middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead, middleware.ScopeCustomersWrite)
}

func (e *apiEnv) customerID(t *testing.T, token string) string {
	t.Helper()
	r := e.do(t, http.MethodPost, "/v1.0/customers", token, "cust-"+t.Name(), `{"name":"Cliente Pagador","email":"cliente@example.com","tax_id":"39053344705"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("customer: %d %s", r.status, r.body)
	}
	var c struct {
		ID string `json:"id"`
	}
	r.decode(t, &c)
	return c.ID
}

func TestAOneOffInvoiceIsIssuedNumberedAndOpen(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	body := `{"customer_id":"` + cust + `","description":"Prova de coleta","amount_cents":10,"reference":"proof-1"}`

	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-proof-1", body)
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var inv oneOffInvoice
	r.decode(t, &inv)
	if inv.Status != "OPEN" || inv.Number == 0 || inv.Total != 10 || inv.AmountDue != 10 || inv.CustomerID != cust {
		t.Fatalf("issued, numbered, for R$ 0,10: %+v", inv)
	}
	if len(inv.Lines) != 1 || inv.Lines[0].Description != "Prova de coleta" || inv.Lines[0].Amount != 10 {
		t.Fatalf("one line: %+v", inv.Lines)
	}
	// The checkout link rides on newInvoiceResponse like on every invoice and is
	// tested with the checkout routes; this harness mounts none.
}

func TestTheSameReferenceReturnsTheSameInvoice(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	body := `{"customer_id":"` + cust + `","description":"Depósito","amount_cents":5000,"reference":"dep-1"}`

	var first, again oneOffInvoice
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-a", body)
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r.decode(t, &first)
	// A different Idempotency-Key, the same reference: still one invoice (the
	// reference is the caller's own identity for the thing, the key only guards a retry).
	r = e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-b", body)
	if r.status != http.StatusOK && r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r.decode(t, &again)
	if again.ID != first.ID {
		t.Fatalf("two invoices for one reference: %s %s", first.ID, again.ID)
	}
}

func TestAReferenceReusedForAnotherAmountIsAConflict(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	mk := func(amount string) string {
		return `{"customer_id":"` + cust + `","description":"Depósito","amount_cents":` + amount + `,"reference":"dep-2"}`
	}
	if r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-1", mk("5000")); r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-2", mk("6000"))
	if r.status != http.StatusConflict {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestAOneOffInvoiceIsRefusedForWhatItCannotBe(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	cases := map[string]string{
		"no amount":        `{"customer_id":"` + cust + `","description":"x","amount_cents":0,"reference":"r1"}`,
		"negative":         `{"customer_id":"` + cust + `","description":"x","amount_cents":-5,"reference":"r2"}`,
		"no description":   `{"customer_id":"` + cust + `","description":"","amount_cents":100,"reference":"r3"}`,
		"no reference":     `{"customer_id":"` + cust + `","description":"x","amount_cents":100}`,
		"no customer":      `{"description":"x","amount_cents":100,"reference":"r4"}`,
		"absurd amount":    `{"customer_id":"` + cust + `","description":"x","amount_cents":1000000000000,"reference":"r5"}`,
		"unknown customer": `{"customer_id":"cus_nope","description":"x","amount_cents":100,"reference":"r6"}`,
	}
	for name, body := range cases {
		r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-"+name, body)
		if r.status != http.StatusUnprocessableEntity && r.status != http.StatusNotFound && r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestOneOffInvoicesNeedTheirOwnScope(t *testing.T) {
	e := newAPI(t)
	readOnly := e.token(t, e.client, "", middleware.ScopeInvoicesRead)
	r := e.do(t, http.MethodPost, "/v1.0/invoices", readOnly, "k-x", `{"customer_id":"c","description":"x","amount_cents":100,"reference":"r"}`)
	if r.status != http.StatusForbidden {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// A service that sells through one-off invoices names its owner key, and the
// invoice carries it, so the paid event reaches the endpoint registered for that
// key and no other service's endpoint.
func TestAOneOffInvoiceCarriesTheCallersOwnerKey(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	body := func(ref, owner string) string {
		return `{"customer_id":"` + cust + `","description":"Depósito","amount_cents":5000,"reference":"` + ref + `","owner_key":"` + owner + `"}`
	}
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-owner", body("dep-owner", "wallet"))
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var made oneOffInvoice
	r.decode(t, &made)
	inv, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), e.org.ID, true, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inv.OwnerKey != "wallet" {
		t.Fatalf("owner key %q, want wallet", inv.OwnerKey)
	}
	if r = e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-owner-2", body("dep-owner-2", "not valid!")); r.status != http.StatusUnprocessableEntity && r.status != http.StatusBadRequest {
		t.Fatalf("an owner key with spaces is refused: %d", r.status)
	}
}

// A credential scoped to an owner issues for that owner without saying so, and
// cannot issue for another.
func TestAScopedCredentialIssuesOneOffInvoicesForItsOwnerOnly(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	creds := repositories.NewCredentialRepository(testDB, testCfg)
	cred, err := creds.Resolve(ctxT(t), e.client)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SetOwnerKey(ctxT(t), cred, "wallet", time.Now()); err != nil {
		t.Fatal(err)
	}
	body := func(ref, owner string) string {
		extra := ""
		if owner != "" {
			extra = `,"owner_key":"` + owner + `"`
		}
		return `{"customer_id":"` + cust + `","description":"Depósito","amount_cents":5000,"reference":"` + ref + `"` + extra + `}`
	}
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-scoped", body("dep-scoped", ""))
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var made oneOffInvoice
	r.decode(t, &made)
	inv, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), e.org.ID, true, made.ID)
	if err != nil || inv.OwnerKey != "wallet" {
		t.Fatalf("owner %q err %v", inv.OwnerKey, err)
	}
	if r = e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-scoped-2", body("dep-scoped-2", "poker")); r.status != http.StatusForbidden {
		t.Fatalf("another owner is refused: %d %s", r.status, r.body)
	}
}
