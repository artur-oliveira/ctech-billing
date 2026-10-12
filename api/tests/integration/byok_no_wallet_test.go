//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// failingWallet fails the test on any call: the BYOK rail never reaches it.
type failingWallet struct{ t *testing.T }

func (f *failingWallet) OpenCharge(context.Context, wallet.OpenChargeInput) (*wallet.Charge, error) {
	f.t.Fatal("the BYOK rail opened a wallet charge")
	return nil, nil
}
func (f *failingWallet) GetCharge(context.Context, string) (*wallet.Charge, error) {
	f.t.Fatal("the BYOK rail read a wallet charge")
	return nil, nil
}
func (f *failingWallet) VerifySignature([]byte, string) bool {
	f.t.Fatal("the BYOK rail verified a wallet signature")
	return false
}

func TestABYOKInvoiceNeverTouchesTheWallet(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	env.useWallet(&failingWallet{t: t})
	inv := env.finalizeInvoice(t, 1000)
	env.checkout(t, inv)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 1000, 0, now())
	env.fake.Pay(ref.Ref, 1000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	if r := env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+env.excess(t, inv)[0].ID+"/refund", "{}"); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	inv2 := env.finalizeInvoice(t, 1000)
	env.void(t, inv2)
	env.runJob(t, now().Add(2*time.Hour))
	if env.reloadInvoice(t, inv).Status != billing.InvoicePaid || env.reloadInvoice(t, inv2).Status != billing.InvoiceVoid {
		t.Fatal("paid and voided")
	}
}

func TestSimulatePaymentIsTestModeOnly(t *testing.T) {
	live := newCollectEnv(t, collectLive)
	if r := live.consoleWrite(t, "POST", "/v1.0/console/invoices/"+live.finalizeInvoice(t, 100).ID+"/simulate-payment", "{}"); r.status != 404 {
		t.Fatalf("live: %d %s", r.status, r.body)
	}
	test := newCollectEnv(t, collectTest)
	inv := test.finalizeInvoice(t, 100)
	if r := test.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/simulate-payment", "{}"); r.status != 202 {
		t.Fatalf("test: %d %s", r.status, r.body)
	}
	test.runJob(t, now().Add(2*time.Hour))
	if test.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("simulated payment confirmed through the real path")
	}
}

func TestTheInvoiceDetailShowsThePixAndTheReasons(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 1000)
	r := env.consoleWrite(t, "GET", "/v1.0/console/invoices/"+inv.ID, "")
	if r.status != 200 || !bodyHas(r.body, `"payable":"00020126FAKE`) || !bodyHas(r.body, `"payable_until":"`) || !bodyHas(r.body, `"simulate":true`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	noTax := env.finalizeInvoiceFor(t, env.customerWithoutTaxID(t), 1000)
	if r := env.consoleWrite(t, "GET", "/v1.0/console/invoices/"+noTax.ID, ""); !bodyHas(r.body, `"reason":"customer_tax_id_required"`) {
		t.Fatalf("%s", r.body)
	}
}
