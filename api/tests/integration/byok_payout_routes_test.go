//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type payoutBody struct {
	ID            string `json:"id"`
	Reference     string `json:"reference"`
	Status        string `json:"status"`
	AmountCents   int64  `json:"amount_cents"`
	PaymentID     string `json:"payment_id"`
	FailureReason string `json:"failure_reason"`
}

func (e *collectEnv) payoutToken(t *testing.T, scopes ...string) string {
	t.Helper()
	return e.token(t, e.client, "", scopes...)
}

func TestThePayoutRoutesNeedTheirInternalScopes(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.payoutEnv(t)
	body := `{"reference":"wd-r1","cpf":"` + validCPF + `","amount_cents":1050}`
	for _, tok := range []string{
		env.payoutToken(t, middleware.ScopeInvoicesWrite), // a merchant integration's scope
		env.payoutToken(t, middleware.ScopePayoutsRead),   // can read, not pay
	} {
		if r := env.do(t, http.MethodPost, "/v1.0/payouts", tok, "k-scope", body); r.status != http.StatusForbidden {
			t.Fatalf("%d %s", r.status, r.body)
		}
	}
	if r := env.do(t, http.MethodGet, "/v1.0/payouts/po_x", env.payoutToken(t, middleware.ScopePayoutsWrite), "", ""); r.status != http.StatusForbidden {
		t.Fatalf("write alone does not read: %d", r.status)
	}
	if env.fake.PayoutCalls() != 0 {
		t.Fatal("the bank was not reached")
	}
}

func TestAPayoutThroughTheRouteIsReservedReplayedAndReadBack(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.payoutEnv(t)
	write := env.payoutToken(t, middleware.ScopePayoutsWrite, middleware.ScopePayoutsRead)
	body := `{"reference":"wd-r2","cpf":"` + validCPF + `","amount_cents":1050,"message":"Saque"}`

	r := env.do(t, http.MethodPost, "/v1.0/payouts", write, "k-r2", body)
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var first payoutBody
	r.decode(t, &first)
	if first.Status != "processing" || first.AmountCents != 1050 || first.Reference != "wd-r2" || first.ID == "" {
		t.Fatalf("%+v", first)
	}
	if strings.Contains(string(r.body), validCPF) {
		t.Fatalf("the destination is never echoed: %s", r.body)
	}

	// Same reference, another Idempotency-Key: the same payout, no second payment.
	r = env.do(t, http.MethodPost, "/v1.0/payouts", write, "k-r2-b", body)
	var again payoutBody
	r.decode(t, &again)
	if r.status != http.StatusOK || again.ID != first.ID || env.fake.PayoutCalls() != 1 {
		t.Fatalf("%d %+v calls=%d", r.status, again, env.fake.PayoutCalls())
	}
	// The same reference for another amount is a conflict.
	conflict := strings.Replace(body, `"amount_cents":1050`, `"amount_cents":1051`, 1)
	if r = env.do(t, http.MethodPost, "/v1.0/payouts", write, "k-r2-c", conflict); r.status != http.StatusConflict {
		t.Fatalf("%d %s", r.status, r.body)
	}

	for _, path := range []string{"/v1.0/payouts/" + first.ID, "/v1.0/payouts/by-reference/wd-r2"} {
		var got payoutBody
		if r = env.do(t, http.MethodGet, path, write, "", ""); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		r.decode(t, &got)
		if got.ID != first.ID {
			t.Fatalf("%s: %+v", path, got)
		}
	}
	if r = env.do(t, http.MethodGet, "/v1.0/payouts/by-reference/never", write, "", ""); r.status != http.StatusNotFound {
		t.Fatalf("an unknown reference is a 404: %d", r.status)
	}
}

func TestARouteRefusesWhatCannotBeAPayoutWith422(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.payoutEnv(t)
	write := env.payoutToken(t, middleware.ScopePayoutsWrite)
	for name, body := range map[string]string{
		"cnpj":       `{"reference":"r1","cpf":"11222333000181","amount_cents":100}`,
		"bad digits": `{"reference":"r2","cpf":"39053344700","amount_cents":100}`,
		"no amount":  `{"reference":"r3","cpf":"` + validCPF + `","amount_cents":0}`,
		"no ref":     `{"cpf":"` + validCPF + `","amount_cents":100}`,
	} {
		if r := env.do(t, http.MethodPost, "/v1.0/payouts", write, "k-"+name, body); r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if env.fake.PayoutCalls() != 0 {
		t.Fatal("nothing reached the bank")
	}
}

// ours keeps the errors about this test's payouts: the processing partition is
// shared with every other test's tenants (each with its own fake bank), whose
// payouts the sweep chases too and cannot find here.
func ours(errs []error, ids ...string) []error {
	var out []error
	for _, err := range errs {
		for _, id := range ids {
			if strings.Contains(err.Error(), id) {
				out = append(out, err)
				break
			}
		}
	}
	return out
}

func TestTheSweepFinishesPayoutsAndFlagsTheOnesStuckForHours(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	svc := env.payoutEnv(t)
	sweep := env.sweep().WithPayouts(svc)

	paid, _, _ := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-s1", CPF: validCPF, AmountCents: 100}, now())
	stuck, _, _ := svc.Create(ctxT(t), env.tenant.ID, true, services.PayoutInput{Reference: "wd-s2", CPF: validCPF, AmountCents: 200}, now())
	env.fake.SettlePayout(paid.Ref, byok.PayoutDone, "E1", "")

	// Just created: left alone, nothing to report.
	if errs := ours(sweep.FollowPayouts(ctxT(t), true, now()), paid.ID, stuck.ID); len(errs) != 0 || env.payout(t, paid.ID).Status != billing.PayoutProcessing {
		t.Fatalf("too young to chase: %v", errs)
	}
	// Two minutes later the paid one is settled, the other still processing but not yet alarming.
	if errs := ours(sweep.FollowPayouts(ctxT(t), true, now().Add(2*time.Minute)), paid.ID, stuck.ID); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	if env.payout(t, paid.ID).Status != billing.PayoutDone || env.payout(t, stuck.ID).Status != billing.PayoutProcessing {
		t.Fatal("the bank's word settles; silence does not")
	}
	// Hours later the unsettled one is an alarm.
	errs := ours(sweep.FollowPayouts(ctxT(t), true, now().Add(7*time.Hour)), stuck.ID)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), stuck.ID) {
		t.Fatalf("%v", errs)
	}
	if sweep.FollowPayouts(ctxT(t), false, now()) != nil {
		t.Fatal("test mode pays nobody")
	}
}
