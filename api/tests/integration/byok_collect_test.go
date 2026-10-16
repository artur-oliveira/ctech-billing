//go:build integration

package integration

import (
	"errors"
	"strconv"
	"testing"
	"time"

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

func TestOnlyABYOKTenantCanCollect(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	if s := env.checkout(t, env.finalizeInvoice(t, 100)); s.PixCode == "" {
		t.Fatal("a test BYOK invoice is payable in the sandbox")
	}
	w := newCollectEnv(t, collectTestWallet)
	if _, err := w.checkoutErr(t, w.finalizeInvoice(t, 100)); !errors.Is(err, billing.ErrByokNotConnected) {
		t.Fatalf("a tenant with no BYOK connection: %v", err)
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
	if r.status != 200 || !bodyHas(r.body, `"payout_rail":"byok"`) || !bodyHas(r.body, `"can_charge":true`) || !bodyHas(r.body, `"collection":{"fine_bp":0`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestACreditNoteTheBankMissedIsAppliedByTheSweep(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.FailNext(byok.OpUpdateCharge, byok.ErrUnavailable)
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/credit-notes", `{"amount":1000,"reason":"desconto"}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if ch, _ := env.fake.Charge(env.attempt(t, inv).ChargeRef); ch.Amount != 5000 {
		t.Fatal("the bank was down")
	}
	env.runJob(t, now().Add(10*time.Minute))
	if ch, _ := env.fake.Charge(env.attempt(t, inv).ChargeRef); ch.Amount != 4000 || env.attempt(t, inv).Amount != 4000 {
		t.Fatalf("the sweep applies the credit: %d", ch.Amount)
	}
}

func TestAPaymentOfAStaleAmountAfterACreditIsPartlyExcess(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.FailNext(byok.OpUpdateCharge, byok.ErrUnavailable)
	env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/credit-notes", `{"amount":1000,"reason":"desconto"}`)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	if out, err := env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now()); out != services.OutcomeSettled || err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if ex := env.excess(t, inv); len(ex) != 1 || ex[0].Amount != 1000 {
		t.Fatalf("the credited part paid anyway is excess: %+v", ex)
	}
	if a := env.attempt(t, inv); a.Amount != 4000 {
		t.Fatalf("the attempt settles what was owed: %d", a.Amount)
	}
}

// A service that asked for a short-lived Pix gets an immediate charge (Inter cob) that expires
// when it said, with the text it wrote; every other invoice keeps its due-date charge.
func TestAnInvoiceThatAsksForAShortPixGetsAnImmediateChargeWithItsMessage(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.finalizeOneOff(t, 5000, "Pacote de 10.000 fichas", 3600)
	a := env.attempt(t, inv)
	in, ok := env.fake.Created(a.ChargeRef)
	if !ok || !byok.IsImmediateRef(a.ChargeRef) || len(a.ChargeRef) != 29 {
		t.Fatalf("not an immediate charge: %q %+v", a.ChargeRef, in)
	}
	if in.ExpiresInSeconds != 3600 || in.Message != "Pacote de 10.000 fichas" {
		t.Fatalf("created from %+v", in)
	}
	if a.PixExpiresAt.IsZero() || a.PixExpiresAt.Sub(now()) != time.Hour {
		t.Fatalf("the attempt remembers the exact end: %v", a.PixExpiresAt)
	}
	if s := env.checkout(t, inv); !s.ExpiresAt.Equal(a.PixExpiresAt) {
		t.Fatalf("the page is offered only while the Pix lives: %v vs %v", s.ExpiresAt, a.PixExpiresAt)
	}

	plain := env.finalizeInvoice(t, 5000)
	pa := env.attempt(t, plain)
	pin, _ := env.fake.Created(pa.ChargeRef)
	if byok.IsImmediateRef(pa.ChargeRef) || pin.ExpiresInSeconds != 0 || pin.Message != "Fatura #"+strconv.FormatInt(plain.Number, 10) || !pa.PixExpiresAt.IsZero() {
		t.Fatalf("an invoice that asked for nothing stays due-date: %q %+v", pa.ChargeRef, pin)
	}
}

func TestACheckoutNeverShowsAnImmediatePixThatHasExpired(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.finalizeOneOff(t, 5000, "", 60)
	late := now().Add(2 * time.Minute)
	if _, err := env.byok.Checkout(ctxT(t), env.tenantNow(t), inv, "user:test", "req", late); !errors.Is(err, services.ErrInvoiceNotPayable) {
		t.Fatalf("an expired Pix must not be offered: %v", err)
	}
}
