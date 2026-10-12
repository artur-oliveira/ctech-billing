//go:build integration

package integration

import (
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func (e *collectEnv) syncNow(t *testing.T) services.SyncResult {
	t.Helper()
	conn, err := e.repo.GetConnection(ctxT(t), e.accountOrg, e.livemode, "inter")
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.statement.Sync(ctxT(t), e.sp, conn, now())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *collectEnv) byokAccountBalance(t *testing.T) billing.Cents {
	t.Helper()
	acct, err := repositories.NewLedgerRepository(testDB, testCfg).GetAccount(ctxT(t), e.sp, e.conn.LedgerAccountID)
	if err != nil {
		t.Fatal(err)
	}
	return acct.Balance
}

func TestAPaidInvoiceAndItsLineMakeOneCashEntryWhicheverArrivesFirst(t *testing.T) {
	for _, order := range []string{"payment-first", "line-first"} {
		env := newCollectEnv(t, collectTest)
		inv := env.finalizeInvoice(t, 5000)
		ref := env.chargeRef(t, inv)
		env.fake.Pay(ref.Ref, 5000, 0, now())
		ch, _ := env.fake.Charge(ref.Ref)
		line := byok.StatementLine{ID: "L-" + order, Date: byokToday(), Direction: byok.Credit, Amount: 5000, ChargeRef: ref.Ref, PaymentID: ch.Payments[0].ID}
		if order == "payment-first" {
			_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
		}
		env.fake.AddLines(line)
		env.syncNow(t)
		if env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
			t.Fatalf("%s: the line woke the confirmation", order)
		}
		env.syncNow(t)
		if got := env.byokAccountBalance(t); got != 5000 {
			t.Fatalf("%s: balance %d, want one entry of 5000", order, got)
		}
	}
}

func TestLateChargesPostAsInterestReceived(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 10300, 300, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	if got := env.byokAccountBalance(t); got != 10300 {
		t.Fatalf("the bank received 10300: %d", got)
	}
	acct, err := repositories.NewLedgerRepository(testDB, testCfg).GetAccount(ctxT(t), env.sp, finance.CategoryLateChargesReceived)
	if err != nil || acct.Balance != -300 && acct.Balance != 300 {
		t.Fatalf("late charges category: %+v %v", acct, err)
	}
}

func TestAnUnpaidChargeLineWaitsAndAnExcessLinePostsToClassify(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.AddLines(byok.StatementLine{ID: "L-early", Date: byokToday(), Direction: byok.Credit, Amount: 5000, ChargeRef: ref.Ref})
	env.syncNow(t)
	if got := env.byokAccountBalance(t); got != 0 {
		t.Fatalf("a line for a charge the provider still says is open waits: %d", got)
	}
	env.fake.Pay(ref.Ref, 5000, 0, now())
	env.fake.Pay(ref.Ref, 5000, 0, now())
	ch, _ := env.fake.Charge(ref.Ref)
	env.fake.AddLines(byok.StatementLine{ID: "L-dup", Date: byokToday(), Direction: byok.Credit, Amount: 5000, ChargeRef: ref.Ref, PaymentID: ch.Payments[1].ID})
	env.syncNow(t)
	if got := env.byokAccountBalance(t); got != 10000 {
		t.Fatalf("the invoice's payment once and the duplicate as a bank line: %d", got)
	}
}

func TestAnOverpaidLineKeepsTheLedgerEqualToTheBank(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 10500, 300, now())
	ch, _ := env.fake.Charge(ref.Ref)
	_, _ = env.byok.Confirm(ctxT(t), ref, services.WakeWebhook, now())
	env.fake.AddLines(byok.StatementLine{ID: "L-over", Date: byokToday(), Direction: byok.Credit, Amount: 10500, ChargeRef: ref.Ref, PaymentID: ch.Payments[0].ID})
	env.syncNow(t)
	env.syncNow(t)
	if got := env.byokAccountBalance(t); got != 10500 {
		t.Fatalf("one line of 10500 in the bank, %d in the ledger", got)
	}
}
