//go:build integration

package integration

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func (e *collectEnv) paidInvoice(t *testing.T, cents billing.Cents) *billing.Invoice {
	t.Helper()
	inv := e.finalizeInvoice(t, cents)
	ref := e.chargeRef(t, inv)
	e.fake.Pay(ref.Ref, int64(cents), 0, now())
	if _, err := e.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

// A CTech service gives a paid invoice's money back to its payer: the same
// credit note + "Devolver via Pix" the console issues, asked for by a client.
func TestAServiceClientRefundsAPaidInvoiceToItsPayer(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.paidInvoice(t, 5000)
	tok := env.token(t, env.client, "", middleware.ScopeInvoicesWrite)
	r := env.do(t, http.MethodPost, "/v1.0/invoices/"+inv.ID+"/credit-notes", tok, "k-refund-1",
		`{"amount":2000,"reason":"Depósito devolvido ao pagador","refund_via_pix":true}`)
	if r.status != http.StatusCreated || env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if rows := env.excess(t, inv); len(rows) != 1 || rows[0].Kind != billing.RefundOfCredit || rows[0].Amount != 2000 {
		t.Fatalf("%+v", rows)
	}
	// The same Idempotency-Key replays the answer; no second refund.
	if again := env.do(t, http.MethodPost, "/v1.0/invoices/"+inv.ID+"/credit-notes", tok, "k-refund-1",
		`{"amount":2000,"reason":"Depósito devolvido ao pagador","refund_via_pix":true}`); again.status != http.StatusCreated || env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatalf("replay: %d calls=%d", again.status, env.fake.Calls(byok.OpRefund))
	}
}

func TestARefundThatCannotHappenIssuesNoNoteAndNeedsTheWriteScope(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now().AddDate(0, 0, -91))
	setPaidAt(t, env, inv, now().AddDate(0, 0, -91))

	tok := env.token(t, env.client, "", middleware.ScopeInvoicesWrite)
	r := env.do(t, http.MethodPost, "/v1.0/invoices/"+inv.ID+"/credit-notes", tok, "k-refund-2", `{"amount":2000,"reason":"tarde demais","refund_via_pix":true}`)
	if r.code() != "refund_window_closed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if notes, _ := repositories.NewCreditNoteRepository(testDB, testCfg).ListByInvoice(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID); len(notes) != 0 {
		t.Fatal("no credit note when its refund cannot happen")
	}
	read := env.token(t, env.client, "", middleware.ScopeInvoicesRead)
	if r = env.do(t, http.MethodPost, "/v1.0/invoices/"+inv.ID+"/credit-notes", read, "k-refund-3", `{"amount":1,"reason":"x"}`); r.status != http.StatusForbidden {
		t.Fatalf("read-only scope: %d", r.status)
	}
}

func TestAnInvoiceIsFoundByTheCallersReference(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	var made oneOffInvoice
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-ref", `{"customer_id":"`+cust+`","description":"Depósito","amount_cents":5000,"reference":"dep-by-ref"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r.decode(t, &made)

	var found oneOffInvoice
	r = e.do(t, http.MethodGet, "/v1.0/invoices/by-reference/dep-by-ref", tok, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r.decode(t, &found)
	if found.ID != made.ID || found.Total != 5000 {
		t.Fatalf("%+v vs %+v", found, made)
	}
	// A consumer woken by invoice.paid reads the invoice by id and must find out whose
	// sale it is from the invoice alone (Poker routes its deliveries on this).
	var byID oneOffInvoice
	r = e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID, tok, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r.decode(t, &byID)
	if made.Reference != "dep-by-ref" || found.Reference != "dep-by-ref" || byID.Reference != "dep-by-ref" {
		t.Fatalf("reference: created %q, by reference %q, by id %q", made.Reference, found.Reference, byID.Reference)
	}
	if r = e.do(t, http.MethodGet, "/v1.0/invoices/by-reference/never-issued", tok, "", ""); r.status != http.StatusNotFound {
		t.Fatalf("an unknown reference is 404: %d", r.status)
	}
	// "by-reference" is a route, not an invoice id.
	if r = e.do(t, http.MethodGet, "/v1.0/invoices/by-reference", tok, "", ""); r.status == http.StatusOK {
		t.Fatal("not an invoice")
	}
}
