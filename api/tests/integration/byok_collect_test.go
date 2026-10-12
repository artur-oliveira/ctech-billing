//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestFinalizationCreatesOneCobvWithTheInvoiceTerms(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.setCollection(t, billing.CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 100})
	inv := env.finalizeInvoice(t, 9990)
	a := env.attempt(t, inv)
	ch, _ := env.fake.Charge(a.ChargeRef)
	if a.Method != billing.MethodBYOK || ch.Amount != 9990 || a.Payable == "" || a.PayableUntil.IsZero() || len(a.ChargeRef) != 29 {
		t.Fatalf("%+v %+v", a, ch)
	}
	if got := env.reloadInvoice(t, inv).Collection; got.FineBasisPoints != 200 || got.MonthlyInterestBasisPoints != 100 {
		t.Fatalf("the invoice copies the terms: %+v", got)
	}
	env.setCollection(t, billing.CollectionSettings{FineBasisPoints: 900})
	if got := env.reloadInvoice(t, inv).Collection.FineBasisPoints; got != 200 {
		t.Fatalf("an issued invoice keeps its terms: %d", got)
	}
	if env.fake.Calls(byok.OpCreateDueCharge) != 1 {
		t.Fatal("one creation")
	}
}

func TestAFailedCreationLeavesTheInvoiceIssuedAndCheckoutRetries(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.fake.FailNext(byok.OpCreateDueCharge, byok.ErrUnavailable)
	inv := env.finalizeInvoice(t, 5000)
	if inv.Status != billing.InvoiceOpen || env.attempt(t, inv).Payable != "" {
		t.Fatal("issued, attempt pending without artefact")
	}
	s := env.checkout(t, inv)
	if s.PixCode == "" || s.PayableUntil.IsZero() || env.fake.Calls(byok.OpCreateDueCharge) != 2 {
		t.Fatalf("checkout completes the same attempt: %+v", s)
	}
	if env.attempts(t, inv) != 1 {
		t.Fatal("one attempt per invoice")
	}
	if again := env.checkout(t, inv); again.ID != s.ID {
		t.Fatal("a second press shows the same session")
	}
}

func TestACustomerWithoutTaxIDGetsNoCharge(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.finalizeInvoiceFor(t, env.customerWithoutTaxID(t), 5000)
	if env.attempts(t, inv) != 0 {
		t.Fatal("no attempt")
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/charge", "{}"); r.code() != "customer_tax_id_required" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestVoidRemovesTheChargeAndAPaidChargeWinsTheRace(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	open := env.finalizeInvoice(t, 3000)
	env.void(t, open)
	if ch, _ := env.fake.Charge(env.attempt(t, open).ChargeRef); ch.Status != byok.ChargeRemoved || env.attempt(t, open).Status != billing.AttemptFailed {
		t.Fatal("a void removes the open charge and fails its attempt")
	}
	if env.reloadInvoice(t, open).Status != billing.InvoiceVoid {
		t.Fatal("voided")
	}

	inv := env.finalizeInvoice(t, 5000)
	env.fake.Pay(env.attempt(t, inv).ChargeRef, 5000, 0, now())
	env.void(t, inv)
	if got := env.reloadInvoice(t, inv); got.Status != billing.InvoicePaid {
		t.Fatalf("the payment wins over the void: %s", got.Status)
	}
}

func TestACreditNoteUpdatesTheOpenCharge(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/credit-notes", `{"amount":1000,"reason":"desconto"}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	ch, _ := env.fake.Charge(env.attempt(t, inv).ChargeRef)
	if ch.Amount != 4000 || env.attempt(t, inv).Amount != 4000 {
		t.Fatalf("the charge follows the amount due: %d %d", ch.Amount, env.attempt(t, inv).Amount)
	}
}

func TestANewPixAfterTheOldOneExpired(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/charge", "{}"); r.code() != "byok_charge_open" {
		t.Fatalf("still open: %d %s", r.status, r.body)
	}
	if err := env.fake.CancelCharge(ctxT(t), byok.Credentials{}, byok.Token{}, env.attempt(t, inv).ChargeRef); err != nil {
		t.Fatal(err)
	}
	if out, err := env.byok.Confirm(ctxT(t), env.chargeRef(t, inv), services.WakeSweep, now()); out != services.OutcomeExpired || err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/charge", "{}"); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	a := env.attempt(t, inv)
	if env.attempts(t, inv) != 2 || a.Payable == "" || a.DueDate != todayPlus(3) {
		t.Fatalf("a second attempt, due in 3 days: %+v", a)
	}
}

func TestTestModeIsCollectableOnTheBYOKRailOnly(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	if s := env.checkout(t, env.finalizeInvoice(t, 100)); s.PixCode == "" {
		t.Fatal("a test BYOK invoice is payable in the sandbox")
	}
	w := newCollectEnv(t, collectTestWallet)
	if _, err := w.checkoutErr(t, w.finalizeInvoice(t, 100)); !errors.Is(err, services.ErrTestModeNotPayable) {
		t.Fatalf("wallet rail in test: %v", err)
	}
}

func TestTheGateRefusesALiveTenantWithoutEntitlementOrConnection(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	org := env.tenantNow(t)
	if err := env.gates.Authorize(ctxT(t), org); err != nil {
		t.Fatalf("entitled, connected: %v", err)
	}
	stranger := *org
	stranger.AccountOrganizationID = newSpaceOrgID()
	if err := env.gates.Authorize(ctxT(t), &stranger); !errors.Is(err, billing.ErrByokNotConnected) {
		t.Fatalf("no connection: %v", err)
	}
	r := env.consoleAs(t, "GET", "/v1.0/console/settings", env.sessionToken(t, collectScopes...), "live", "org:"+env.accountOrg, "")
	if r.status != 200 || !bodyHas(r.body, `"payout_rail":"byok"`) || !bodyHas(r.body, `"can_charge":true`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
