//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func (e *collectEnv) issueHook(t *testing.T) {
	t.Helper()
	hookID := ""
	if !e.livemode {
		hookID = repositories.SandboxHookID
	}
	id, secret, err := e.webhooks.IssueHook(ctxT(t), e.conn, hookID, now())
	if err != nil {
		t.Fatal(err)
	}
	e.hookID, e.secret = id, secret
}

func (e *collectEnv) m2mToken(t *testing.T, scope string) string {
	t.Helper()
	return e.token(t, "billing-byok-webhook-test", "", scope)
}

func (e *collectEnv) notifyBodyAccount(hookID, secret, account string, refs []string) string {
	b, _ := json.Marshal(map[string]any{"provider": "inter", "hook_id": hookID, "secret": secret, "product": "pix",
		"account": account, "charge_refs": refs})
	return string(b)
}

func (e *collectEnv) notifyBody(hookID, secret, provider string, refs []string) string {
	b, _ := json.Marshal(map[string]any{"provider": provider, "hook_id": hookID, "secret": secret, "product": "pix", "charge_refs": refs})
	return string(b)
}

func (e *collectEnv) post(t *testing.T, path, token, body string) apiResponse {
	t.Helper()
	return e.do(t, http.MethodPost, path, token, "", body)
}

func TestNoWebhookPayloadSettlesAnything(t *testing.T) {
	a := newCollectEnv(t, collectLive)
	a.issueHook(t)
	b := newCollectEnv(t, collectLive) // another organization, its own connection and hook
	b.issueHook(t)
	invA := a.finalizeInvoice(t, 5000)
	refA := a.chargeRef(t, invA).Ref
	invB := b.finalizeInvoice(t, 5000)
	b.fake.Pay(b.chargeRef(t, invB).Ref, 5000, 0, now())
	tok := a.m2mToken(t, "internal:billing:byok-notify")
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"wrong secret":          {a.notifyBody(a.hookID, "wrong", "inter", []string{refA}), 404},
		"provider mismatch":     {a.notifyBody(a.hookID, a.secret, "other", []string{refA}), 404},
		"other org's hook":      {a.notifyBody(b.hookID, b.secret, "inter", []string{refA}), 200},
		"account mismatch":      {a.notifyBodyAccount(a.hookID, a.secret, "999", []string{refA}), 200},
		"claims an open charge": {a.notifyBody(a.hookID, a.secret, "inter", []string{refA}), 200},
		"unknown hook":          {a.notifyBody("01NOPE", a.secret, "inter", []string{refA}), 404},
	} {
		r := a.post(t, "/v1.0/internal/byok/notify", tok, tc.body)
		if r.status != tc.want || a.reloadInvoice(t, invA).Status == billing.InvoicePaid {
			t.Fatalf("%s: %d, invoice %s", name, r.status, a.reloadInvoice(t, invA).Status)
		}
	}
	// A valid reference of another organization at this organization's hook.
	if r := a.post(t, "/v1.0/internal/byok/notify", tok, a.notifyBody(a.hookID, a.secret, "inter", []string{b.chargeRef(t, invB).Ref})); r.status != 200 || b.reloadInvoice(t, invB).Status == billing.InvoicePaid {
		t.Fatalf("cross-organization reference: %d", r.status)
	}
	if r := a.post(t, "/v1.0/internal/byok/notify", a.m2mToken(t, "billing:invoices:read"), a.notifyBody(a.hookID, a.secret, "inter", []string{refA})); r.status != 403 {
		t.Fatalf("scope: %d", r.status)
	}
	if r := a.post(t, "/v1.0/internal/byok/notify", a.sessionToken(t, "internal:billing:byok-notify"), a.notifyBody(a.hookID, a.secret, "inter", []string{refA})); r.status != 403 {
		t.Fatalf("a user token: %d", r.status)
	}
	a.fake.Pay(refA, 5000, 0, now())
	if r := a.post(t, "/v1.0/internal/byok/notify", tok, a.notifyBody(a.hookID, a.secret, "inter", []string{refA})); r.status != 200 || a.reloadInvoice(t, invA).Status != billing.InvoicePaid {
		t.Fatalf("a real payment, re-queried, settles: %d", r.status)
	}
}

func TestAFailedRequeryAnswers503SoTheProviderRetries(t *testing.T) {
	a := newCollectEnv(t, collectTest)
	a.issueHook(t)
	inv := a.finalizeInvoice(t, 5000)
	a.fake.FailNext(byok.OpGetCharge, byok.ErrUnavailable)
	r := a.post(t, "/v1.0/internal/byok/notify", a.m2mToken(t, "internal:billing:byok-notify"), a.notifyBody(repositories.SandboxHookID, a.secret, "inter", []string{a.chargeRef(t, inv).Ref}))
	if r.status != 503 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestARotatedSecretKeepsWorkingForADay(t *testing.T) {
	a := newCollectEnv(t, collectTest)
	a.issueHook(t)
	old := a.secret
	a.issueHook(t)
	inv := a.finalizeInvoice(t, 5000)
	a.fake.Pay(a.chargeRef(t, inv).Ref, 5000, 0, now())
	if r := a.post(t, "/v1.0/internal/byok/notify", a.m2mToken(t, "internal:billing:byok-notify"), a.notifyBody(repositories.SandboxHookID, old, "inter", []string{a.chargeRef(t, inv).Ref})); r.status != 200 || a.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatalf("%d", r.status)
	}
}
