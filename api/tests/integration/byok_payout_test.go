//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

const validCPF = "39053344705"

// payoutEnv is a live tenant on the BYOK rail whose connection can pay out.
func (e *collectEnv) payoutEnv(t *testing.T) *services.Payouts {
	t.Helper()
	conn := e.connection(t)
	conn.Capabilities = append(conn.Capabilities, byok.CapabilityPayout)
	if err := e.repo.SaveConnection(ctxT(t), conn, "test", "req", "byok.test", now()); err != nil {
		t.Fatal(err)
	}
	return services.NewPayouts(e.payments, e.orgs, e.repo, e.conns.Session)
}

func (e *collectEnv) payout(t *testing.T, id string) *billing.Payout {
	t.Helper()
	p, err := e.payments.GetPayout(ctxT(t), e.tenant.ID, e.livemode, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPayoutIsReservedSentAndSettledOnTheBanksWord(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	in := services.PayoutInput{Reference: "wd-1", CPF: validCPF, AmountCents: 1050, Message: "Saque"}

	p, created, err := svc.Create(ctxT(t), env.tenant.ID, true, in, now())
	if err != nil || !created {
		t.Fatalf("%+v %v %v", p, created, err)
	}
	if p.Status != billing.PayoutProcessing || p.ProviderRef == "" || env.fake.PayoutCalls() != 1 {
		t.Fatalf("accepted by the bank is not paid yet: %+v calls=%d", p, env.fake.PayoutCalls())
	}

	env.fake.SettlePayout(p.Ref, byok.PayoutDone, "E123", "")
	if err := svc.Resolve(ctxT(t), p, now()); err != nil {
		t.Fatal(err)
	}
	if got := env.payout(t, p.ID); got.Status != billing.PayoutDone || got.PaymentID != "E123" {
		t.Fatalf("%+v", got)
	}
	// A settled payout is never resent.
	if err := svc.Resolve(ctxT(t), p, now()); err != nil || env.fake.PayoutCalls() != 1 {
		t.Fatalf("%v calls=%d", err, env.fake.PayoutCalls())
	}
}

func TestTheSameReferenceIsTheSamePayoutAndNeverPaysTwice(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	in := services.PayoutInput{Reference: "wd-2", CPF: validCPF, AmountCents: 2000}
	first, _, err := svc.Create(ctxT(t), env.tenant.ID, true, in, now())
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := svc.Create(ctxT(t), env.tenant.ID, true, in, now())
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("%+v %v %v", again, created, err)
	}
	if env.fake.PayoutCalls() != 1 {
		t.Fatalf("the bank was asked %d times", env.fake.PayoutCalls())
	}
	in.AmountCents = 2001
	if _, _, err := svc.Create(ctxT(t), env.tenant.ID, true, in, now()); !errors.Is(err, services.ErrPayoutConflict) {
		t.Fatalf("another amount under the same reference: %v", err)
	}
}

func TestAnUnknownKeyFailsThePayoutAtOnce(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	env.fake.FailNext(byok.OpPayout, byok.ErrPixKeyNotFound)
	p, _, err := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-3", CPF: validCPF, AmountCents: 500}, now())
	if err != nil {
		t.Fatal(err)
	}
	if got := env.payout(t, p.ID); got.Status != billing.PayoutFailed || got.FailureReason != billing.PayoutKeyNotFound {
		t.Fatalf("%+v", got)
	}
}

func TestATransportErrorLeavesTheMoneyUndecidedAndTheSweepFinishesIt(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	env.fake.FailNext(byok.OpPayout, byok.ErrUnavailable)
	p, _, err := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-4", CPF: validCPF, AmountCents: 700}, now())
	if err != nil {
		t.Fatalf("a payout whose call failed is reserved and left processing: %v", err)
	}
	got := env.payout(t, p.ID)
	if got.Status != billing.PayoutProcessing || got.ProviderRef != "" {
		t.Fatalf("not failed, not sent: %+v", got)
	}
	// The sweep sends it again with the SAME UUID, then reads the answer.
	if err := svc.Resolve(ctxT(t), got, now()); err != nil {
		t.Fatal(err)
	}
	if got = env.payout(t, p.ID); got.ProviderRef == "" || got.Status != billing.PayoutProcessing {
		t.Fatalf("%+v", got)
	}
	env.fake.SettlePayout(got.Ref, byok.PayoutDone, "E9", "")
	if err := svc.Resolve(ctxT(t), got, now()); err != nil {
		t.Fatal(err)
	}
	if env.payout(t, p.ID).Status != billing.PayoutDone {
		t.Fatal("paid after the retry")
	}
}

func TestTheBanksRefusalFailsItWithItsReasonAndAnUnknownStatusDoesNot(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	refused, _, _ := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-5", CPF: validCPF, AmountCents: 100}, now())
	env.fake.SettlePayout(refused.Ref, byok.PayoutFailed, "", "Saldo insuficiente")
	if err := svc.Resolve(ctxT(t), refused, now()); err != nil {
		t.Fatal(err)
	}
	if got := env.payout(t, refused.ID); got.Status != billing.PayoutFailed || got.FailureReason != "Saldo insuficiente" {
		t.Fatalf("%+v", got)
	}
	odd, _, _ := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-6", CPF: validCPF, AmountCents: 100}, now())
	env.fake.SettlePayout(odd.Ref, byok.PayoutUnknown, "", "")
	if err := svc.Resolve(ctxT(t), odd, now()); err != nil {
		t.Fatal(err)
	}
	if env.payout(t, odd.ID).Status != billing.PayoutProcessing {
		t.Fatal("a status nobody mapped is never read as paid or failed")
	}
}

func TestAPayoutIsRefusedBeforeTheBankWhenItCannotBeOne(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	for name, in := range map[string]services.PayoutInput{
		"cnpj, not a cpf": {Reference: "r1", CPF: "11222333000181", AmountCents: 100},
		"invalid digits":  {Reference: "r2", CPF: "39053344700", AmountCents: 100},
		"no amount":       {Reference: "r3", CPF: validCPF, AmountCents: 0},
		"no reference":    {CPF: validCPF, AmountCents: 100},
		"reference chars": {Reference: "wd#1", CPF: validCPF, AmountCents: 100},
	} {
		if _, _, err := svc.Create(ctxT(t), env.tenant.ID, true, in, now()); !errors.Is(err, services.ErrPayoutInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if env.fake.PayoutCalls() != 0 {
		t.Fatalf("the bank was reached %d times", env.fake.PayoutCalls())
	}
}

func TestATenantWithoutThePayoutCapabilityCannotPay(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := services.NewPayouts(env.payments, env.orgs, env.repo, env.conns.Session) // the connection never got the capability
	_, _, err := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-7", CPF: validCPF, AmountCents: 100}, now())
	if !errors.Is(err, services.ErrPayoutNotAllowed) || env.fake.PayoutCalls() != 0 {
		t.Fatalf("%v calls=%d", err, env.fake.PayoutCalls())
	}
}

// A payout the bank has taken but not settled is followed soon after, in the
// background, and its settlement is announced and kicked without waiting for the
// sweep or the caller's next read.
func TestAProcessingPayoutIsFollowedSoonAndItsSettlementIsAnnounced(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	kicked := make(chan bool, 4)
	svc := env.payoutEnv(t).WithFollowSteps(20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond).
		WithSettlementKick(func(live bool) { kicked <- live })

	p, created, err := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-follow", CPF: validCPF, AmountCents: 700, OwnerKey: "wallet"}, now())
	if err != nil || !created || p.Final() {
		t.Fatalf("%+v %v %v", p, created, err)
	}
	svc.FollowSoon(p)
	svc.FollowSoon(p) // one follower per payout
	env.fake.SettlePayout(env.payout(t, p.ID).Ref, byok.PayoutDone, "E-follow", "")

	select {
	case live := <-kicked:
		if !live {
			t.Fatal("the kick is for live mode")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the payout was not followed to its settlement")
	}
	if got := env.payout(t, p.ID); got.Status != billing.PayoutDone || got.PaymentID != "E-follow" {
		t.Fatalf("%+v", got)
	}
	evs, err := repositories.NewWebhookRepository(testDB, testCfg).EventsOf(ctxT(t), env.tenant.ID, true, "payout", p.ID, nil, 5)
	if err != nil || len(evs.Items) != 1 || evs.Items[0].Type != billing.EventPayoutDone || evs.Items[0].OwnerKey != "wallet" {
		t.Fatalf("event: %v %+v", err, evs)
	}
	select {
	case <-kicked:
		t.Fatal("kicked twice for one settlement")
	case <-time.After(200 * time.Millisecond):
	}
}
