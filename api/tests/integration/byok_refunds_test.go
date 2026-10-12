//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// excessPaidAt is an invoice paid twice at paidAt: the second payment is excess.
func (e *collectEnv) excessPaidAt(t *testing.T, paidAt time.Time) billing.ExcessPayment {
	t.Helper()
	inv := e.finalizeInvoice(t, 1000)
	ref := e.chargeRef(t, inv)
	e.fake.Pay(ref.Ref, 1000, 0, paidAt)
	e.fake.Pay(ref.Ref, 1000, 0, paidAt)
	_, _ = e.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	ex := e.excess(t, inv)
	if len(ex) != 1 {
		t.Fatalf("%+v", ex)
	}
	return ex[0]
}

func TestAnExcessIsRefundedOnlyWhenTheMerchantAsks(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	ex := env.excessPaidAt(t, now())
	if env.fake.Calls(byok.OpRefund) != 0 {
		t.Fatal("nothing is refunded automatically (D19)")
	}
	r := env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}")
	if r.status != 200 || env.fake.Calls(byok.OpRefund) != 1 || !bodyHas(r.body, `"status":"refunding"`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}")
	if env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatal("a second press does not refund twice")
	}
	// Cross-tenant by design: other tests' refunds in the shared table may
	// answer errors here (their fake bank is not this one); this one settles.
	_ = env.byok.FollowRefunds(ctxT(t), false, now())
	if got, _ := env.payments.GetExcess(ctxT(t), env.tenant.ID, false, ex.ID); got.Status != billing.ExcessRefunded {
		t.Fatalf("the sweep follows the refund: %s", got.Status)
	}
}

func TestRefundWindowClosesAfterNinetyDays(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	ex := env.excessPaidAt(t, now().AddDate(0, 0, -91))
	if r := env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}"); r.code() != "refund_window_closed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestAnotherTenantCannotRefundAnExcess(t *testing.T) {
	a := newCollectEnv(t, collectTest)
	ex := a.excessPaidAt(t, now())
	b := newCollectEnv(t, collectTest)
	if r := b.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}"); r.status != 404 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestACreditNoteRefundsAPaidInvoiceViaPix(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/credit-notes", `{"amount":2000,"reason":"devolução parcial","refund_via_pix":true}`)
	if r.status != 201 || env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if rows := env.excess(t, inv); len(rows) != 1 || rows[0].Kind != billing.RefundOfCredit || rows[0].Amount != 2000 {
		t.Fatalf("%+v", rows)
	}
}

func TestARefundViaPixThatCannotHappenIssuesNoCreditNote(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now().AddDate(0, 0, -91))
	setPaidAt(t, env, inv, now().AddDate(0, 0, -91))
	r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/credit-notes", `{"amount":2000,"reason":"devolução","refund_via_pix":true}`)
	if r.code() != "refund_window_closed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	notes, _ := repositories.NewCreditNoteRepository(testDB, testCfg).ListByInvoice(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID)
	if len(notes) != 0 {
		t.Fatal("no credit note when its refund cannot happen")
	}
}

// setPaidAt backdates an invoice's paid_at (a payment 91 days ago).
func setPaidAt(t *testing.T, env *collectEnv, inv *billing.Invoice, at time.Time) {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableInvoices)
	sk := repositories.InvoiceSK(inv.ID)
	if _, err := base.UpdateItem(ctxT(t), repositories.TenantPK(inv.OrganizationID, inv.Livemode), &sk, map[string]any{"paid_at": at.UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
}
