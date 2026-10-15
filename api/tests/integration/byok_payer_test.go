//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// payLine pays the invoice's charge at the fake bank and returns the statement
// line the bank would show for it, with the payer given.
func (e *collectEnv) payLine(t *testing.T, inv *billing.Invoice, id string, payer *byok.Payer) byok.StatementLine {
	t.Helper()
	ref := e.chargeRef(t, inv)
	e.fake.Pay(ref.Ref, int64(inv.Total), 0, now())
	ch, _ := e.fake.Charge(ref.Ref)
	return byok.StatementLine{ID: id, Date: byokToday(), Direction: byok.Credit, Amount: int64(inv.Total),
		ChargeRef: ref.Ref, PaymentID: ch.Payments[0].ID, Payer: payer}
}

func TestAReceivedLineRecordsItsPayerOnTheAttemptOnce(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	line := env.payLine(t, inv, "L-payer-1", &byok.Payer{Name: "Fulano de Tal", Document: env.customer.TaxID, Institution: "BANCO EXEMPLO"})
	env.fake.AddLines(line)
	env.syncNow(t)

	a := env.attempt(t, inv)
	if a.Payer == nil || a.Payer.Name != "Fulano de Tal" || a.Payer.Document != env.customer.TaxID || a.PayerMatch != billing.PayerMatchYes {
		t.Fatalf("payer %+v match %q", a.Payer, a.PayerMatch)
	}

	// Another line for the same payment, naming someone else: the first stays.
	other := line
	other.ID, other.Payer = "L-payer-2", &byok.Payer{Name: "Outra Pessoa", Document: "98765432100"}
	env.fake.AddLines(other)
	env.syncNow(t)
	if got := env.attempt(t, inv); got.Payer == nil || got.Payer.Name != "Fulano de Tal" || got.PayerMatch != billing.PayerMatchYes {
		t.Fatalf("write-once: %+v %q", got.Payer, got.PayerMatch)
	}
}

func TestAPayerWhoIsNotTheCustomerIsAMismatch(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.AddLines(env.payLine(t, inv, "L-other", &byok.Payer{Name: "Terceiro", Document: "98765432100"}))
	env.syncNow(t)
	if a := env.attempt(t, inv); a.Payer == nil || a.PayerMatch != billing.PayerMatchNo {
		t.Fatalf("%+v %q", a.Payer, a.PayerMatch)
	}
}

func TestALineWithoutAPayerSettlesAndLeavesTheAttemptWithoutOne(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.AddLines(env.payLine(t, inv, "L-nopayer", nil))
	env.syncNow(t)
	if env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("the invoice is paid by the re-query, payer or not")
	}
	if a := env.attempt(t, inv); a.Payer != nil || a.PayerMatch != "" {
		t.Fatalf("nothing to record: %+v %q", a.Payer, a.PayerMatch)
	}
}

// The webhook should not leave the payer to the hourly sync: once it settles an
// attempt, the statement is pulled for that connection, in the background.
func TestSettlingByWebhookPullsTheStatementForThePayer(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.issueHook(t)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.AddLines(env.payLine(t, inv, "L-pull", &byok.Payer{Name: "Fulano de Tal", Document: env.customer.TaxID}))

	hooks := env.webhooks.WithStatement(env.statement)
	ref := env.chargeRef(t, inv)
	status := hooks.Notify(ctxT(t), services.NotifyRequest{Provider: "inter", HookID: env.hookID, Secret: env.secret, Product: "pix",
		ChargeRefs: []string{ref.Ref}}, now())
	if status != 200 {
		t.Fatalf("notify = %d", status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a := env.attempt(t, inv); a.Payer != nil {
			if a.PayerMatch != billing.PayerMatchYes {
				t.Fatalf("match %q", a.PayerMatch)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the payer was not recorded within seconds of the webhook")
}

// The webhook names the payer too, masked. Once the re-query has settled that same
// payment it is recorded as provisional ("partial"), so a consumer can act at once;
// the statement line's complete payer then replaces it, and a webhook never replaces
// a statement payer.
func TestTheWebhooksMaskedPayerIsProvisionalAndTheStatementReplacesIt(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.issueHook(t)
	inv := env.finalizeInvoice(t, 5000)
	line := env.payLine(t, inv, "L-wh-1", &byok.Payer{Name: "Fulano de Tal", Document: env.customer.TaxID})
	ref := env.chargeRef(t, inv)

	masked := "***" + env.customer.TaxID[3:9] + "**"
	notify := func(claim byok.CallbackPayer) int {
		return env.webhooks.Notify(ctxT(t), services.NotifyRequest{Provider: "inter", HookID: env.hookID, Secret: env.secret, Product: "pix",
			ChargeRefs: []string{ref.Ref}, PaymentIDs: []string{line.PaymentID}, Payers: []byok.CallbackPayer{claim}}, now())
	}

	// A claim for another payment than the one the re-query settled is not recorded.
	if st := notify(byok.CallbackPayer{ChargeRef: ref.Ref, PaymentID: "E-someone-else", Name: "Outra Pessoa", Document: masked}); st != 200 {
		t.Fatalf("notify %d", st)
	}
	if a := env.attempt(t, inv); a.Payer != nil {
		t.Fatalf("a payer for another payment was recorded: %+v", a.Payer)
	}

	if st := notify(byok.CallbackPayer{ChargeRef: ref.Ref, PaymentID: line.PaymentID, Name: "Fulano de Tal", Document: masked}); st != 200 {
		t.Fatalf("notify %d", st)
	}
	a := env.attempt(t, inv)
	if a.Payer == nil || !a.Payer.Provisional || a.Payer.Document != masked || a.PayerMatch != billing.PayerMatchPartial {
		t.Fatalf("provisional payer %+v match %q", a.Payer, a.PayerMatch)
	}

	// A second webhook claim does not replace the first.
	notify(byok.CallbackPayer{ChargeRef: ref.Ref, PaymentID: line.PaymentID, Name: "Troca", Document: "***000000**"})
	if got := env.attempt(t, inv); got.Payer.Name != "Fulano de Tal" {
		t.Fatalf("a webhook replaced the first claim: %+v", got.Payer)
	}

	// The statement's complete payer replaces it.
	env.fake.AddLines(line)
	env.syncNow(t)
	a = env.attempt(t, inv)
	if a.Payer == nil || a.Payer.Provisional || a.Payer.Document != env.customer.TaxID || a.PayerMatch != billing.PayerMatchYes {
		t.Fatalf("after the statement: %+v match %q", a.Payer, a.PayerMatch)
	}

	// And nothing masked ever replaces it again.
	notify(byok.CallbackPayer{ChargeRef: ref.Ref, PaymentID: line.PaymentID, Name: "Fulano de Tal", Document: masked})
	if got := env.attempt(t, inv); got.Payer.Provisional || got.PayerMatch != billing.PayerMatchYes {
		t.Fatalf("a masked claim replaced the statement's payer: %+v %q", got.Payer, got.PayerMatch)
	}
}

// A settlement delivers its events at once, whichever path settled it first:
// the notice that settles the charge kicks, and so does one that finds it
// already settled (the hourly job or the sweep got there before the webhook).
func TestANoticeKicksTheDeliveryWhetherItSettledTheChargeOrFoundItSettled(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.issueHook(t)
	var kicks []bool
	env.webhooks.WithDeliveryKick(func(live bool) { kicks = append(kicks, live) })
	inv := env.finalizeInvoice(t, 5000)
	line := env.payLine(t, inv, "L-kick-1", &byok.Payer{Name: "Fulano de Tal", Document: env.customer.TaxID})
	ref := env.chargeRef(t, inv)
	notify := func() int {
		return env.webhooks.Notify(ctxT(t), services.NotifyRequest{Provider: "inter", HookID: env.hookID, Secret: env.secret, Product: "pix",
			ChargeRefs: []string{ref.Ref}, PaymentIDs: []string{line.PaymentID}}, now())
	}
	if st := notify(); st != 200 || len(kicks) != 1 || !kicks[0] {
		t.Fatalf("the notice that settles it kicks (live): %d %v", st, kicks)
	}
	if st := notify(); st != 200 || len(kicks) != 2 {
		t.Fatalf("a notice that finds it settled kicks too: %d %v", st, kicks)
	}
}
