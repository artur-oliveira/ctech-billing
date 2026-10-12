//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestSettlementComesOnlyFromTheProvider(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	if out, _ := env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now()); out != services.OutcomeOpen {
		t.Fatalf("open charge: %s", out)
	}
	if env.reloadInvoice(t, inv).Status != billing.InvoiceOpen || env.attempt(t, inv).CheckedAt.IsZero() {
		t.Fatal("still open, re-query recorded")
	}
	env.fake.Pay(ref.Ref, 5000, 0, now())
	if out, _ := env.byok.Confirm(ctxT(t), ref, services.WakeSweep, now()); out != services.OutcomeSettled {
		t.Fatalf("paid: %s", out)
	}
	if out, _ := env.byok.Confirm(ctxT(t), ref, services.WakeSweep, now()); out != services.OutcomeAlreadySettled {
		t.Fatalf("again: %s", out)
	}
	if a := env.attempt(t, inv); a.Status != billing.AttemptSucceeded || a.PaymentID == "" {
		t.Fatalf("%+v", a)
	}
}

func TestThreeConcurrentWakeUpsSettleOnce(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	var wg sync.WaitGroup
	outs := make([]services.Outcome, 3)
	for i, cause := range []services.WakeCause{services.WakeWebhook, services.WakeSweep, services.WakeStatement} {
		wg.Add(1)
		go func() { defer wg.Done(); outs[i], _ = env.byok.Confirm(ctxT(t), ref, cause, now()) }()
	}
	wg.Wait()
	settled := 0
	for _, o := range outs {
		if o == services.OutcomeSettled {
			settled++
		}
	}
	if settled != 1 || env.reloadInvoice(t, inv).Status != billing.InvoicePaid || env.auditCount(t, inv, "invoice.paid") != 1 {
		t.Fatalf("%v paid audits %d", outs, env.auditCount(t, inv, "invoice.paid"))
	}
}

func TestExcessAndLateChargesAreToldApart(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 10300, 300, now()) // R$ 3,00 of multa + juros
	env.fake.Pay(ref.Ref, 10000, 0, now())   // somebody paid the same QR again
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeSweep, now())
	ex := env.excess(t, inv)
	if len(ex) != 1 || ex[0].Amount != 10000 || ex[0].Status != billing.ExcessOpen {
		t.Fatalf("the late fee is not excess; the second payment is, once: %+v", ex)
	}
	if a := env.attempt(t, inv); a.LateCharges != 300 {
		t.Fatalf("late charges %d", a.LateCharges)
	}
}

func TestAnOverpaymentAboveTheLateChargesIsExcess(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 10500, 300, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	if ex := env.excess(t, inv); len(ex) != 1 || ex[0].Amount != 200 {
		t.Fatalf("%+v", ex)
	}
}

func TestAnUnderpaidChargeAlarmsAndSettlesNothing(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 9000, 0, now())
	if _, err := env.byok.Confirm(ctxT(t), ref, services.WakeSweep, now()); !errors.Is(err, services.ErrAmountMismatch) {
		t.Fatalf("%v", err)
	}
	if env.reloadInvoice(t, inv).Status == billing.InvoicePaid {
		t.Fatal("never a partial payment")
	}
}
