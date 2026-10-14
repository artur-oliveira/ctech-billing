//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// The round trip the wallet will make through Billing, end to end and over the
// public routes: a deposit invoice by reference, its payment (the bank's word),
// the payer from the statement, the money given back, and a withdrawal paid out
// and settled by the sweep.
func TestTheDepositAndWithdrawalRoundTrip(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.issueHook(t)
	payouts := env.payoutEnv(t)
	tok := env.token(t, env.client, "", middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead,
		middleware.ScopePayoutsWrite, middleware.ScopePayoutsRead)

	// 1. A deposit invoice by the wallet's own reference.
	body := `{"customer_id":"` + env.customer.ID + `","description":"Depósito na carteira","amount_cents":5000,"reference":"dep-e2e"}`
	r := env.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-dep", body)
	if r.status != http.StatusCreated {
		t.Fatalf("issue: %d %s", r.status, r.body)
	}
	var made oneOffInvoice
	r.decode(t, &made)
	inv := env.reloadInvoice(t, &billing.Invoice{ID: made.ID, OrganizationID: env.tenant.ID, Livemode: true})
	if inv.Status != billing.InvoiceOpen {
		t.Fatalf("the invoice is open and collectable: %s", inv.Status)
	}

	// 2. The payer pays at the bank; the bank's statement names them; the webhook wakes Billing.
	env.fake.AddLines(env.payLine(t, inv, "L-e2e", &byok.Payer{Name: "Fulano de Tal", Document: env.customer.TaxID}))
	ref := env.chargeRef(t, inv)
	hooks := env.webhooks.WithStatement(env.statement)
	if status := hooks.Notify(ctxT(t), services.NotifyRequest{Provider: "inter", HookID: env.hookID, Secret: env.secret, Product: "pix",
		ChargeRefs: []string{ref.Ref}}, now()); status != 200 {
		t.Fatalf("notify = %d", status)
	}

	// 3. By reference, the wallet sees it paid, and who paid, and that it is the customer.
	var seen struct {
		Status     string `json:"status"`
		PayerMatch string `json:"payer_match"`
		Payer      struct {
			Name string `json:"name"`
		} `json:"payer"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r = env.do(t, http.MethodGet, "/v1.0/invoices/by-reference/dep-e2e", tok, "", "")
		r.decode(t, &seen)
		if seen.PayerMatch != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if seen.Status != "PAID" || seen.PayerMatch != "match" || seen.Payer.Name != "Fulano de Tal" {
		t.Fatalf("by reference: %+v (%s)", seen, r.body)
	}

	// 4. Part of it goes back to the payer.
	if r = env.do(t, http.MethodPost, "/v1.0/invoices/"+inv.ID+"/credit-notes", tok, "k-back",
		`{"amount":1000,"reason":"Devolução do depósito","refund_via_pix":true}`); r.status != http.StatusCreated || env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatalf("refund: %d %s", r.status, r.body)
	}

	// 5. A withdrawal is paid out, and the sweep finishes it on the bank's word.
	r = env.do(t, http.MethodPost, "/v1.0/payouts", tok, "k-wd", `{"reference":"wd-e2e","cpf":"`+validCPF+`","amount_cents":3000,"message":"Saque"}`)
	var po payoutBody
	r.decode(t, &po)
	if r.status != http.StatusCreated || po.Status != "processing" {
		t.Fatalf("payout: %d %s", r.status, r.body)
	}
	env.fake.SettlePayout(env.payout(t, po.ID).Ref, byok.PayoutDone, "E-e2e", "")
	if errs := ours(env.sweep().WithPayouts(payouts).FollowPayouts(ctxT(t), true, now().Add(2*time.Minute)), po.ID); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	r = env.do(t, http.MethodGet, "/v1.0/payouts/by-reference/wd-e2e", tok, "", "")
	r.decode(t, &po)
	if po.Status != "done" || po.PaymentID != "E-e2e" {
		t.Fatalf("settled by the sweep: %+v", po)
	}
}
