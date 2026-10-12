//go:build integration

package integration

import (
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

func (e *collectEnv) connWrite(t *testing.T, method, path, body string) apiResponse {
	t.Helper()
	return e.byokWrite(t, method, path, e.sessionToken(t, collectScopes...), e.mode(), "org:"+e.accountOrg, body)
}

func (e *collectEnv) registerWebhook(t *testing.T) {
	t.Helper()
	if r := e.connWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":false}`); r.status != 200 {
		t.Fatalf("register: %d %s", r.status, r.body)
	}
	c := e.connection(t)
	e.hookID = c.HookID
}

func (e *collectEnv) connection(t *testing.T) *byokconn.Connection {
	t.Helper()
	c, err := e.repo.GetConnection(ctxT(t), e.accountOrg, e.livemode, "inter")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDisconnectOrderAndForeignWebhook(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	inv := env.finalizeInvoice(t, 1000)
	if r := env.connWrite(t, "DELETE", "/v1.0/console/connections/inter", `{}`); r.code() != "byok_open_charges" || !bodyHas(r.body, `"count":1`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	if r := env.connWrite(t, "DELETE", "/v1.0/console/connections/inter", `{"cancel_open_charges":true}`); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if ch, _ := env.fake.Charge(env.chargeRefOf(inv)); ch.Status != byok.ChargeRemoved {
		t.Fatal("the open charge was removed before the credentials")
	}
	if env.attempt(t, inv).Status != billing.AttemptFailed || env.fake.WebhookURL(byok.ProductPix) != "https://erp.example.com/pix" {
		t.Fatal("attempt failed; a foreign webhook is never deleted")
	}
	if org := env.tenantNow(t); org.PayoutStatus != billing.PayoutNotConfigured || org.PayoutRail != "" {
		t.Fatalf("gate reset: %+v", org)
	}
	if r := env.post(t, "/v1.0/internal/byok/notify", env.m2mToken(t, "internal:billing:byok-notify"), env.notifyBody(env.hookID, env.secret, "inter", nil)); r.status != 404 {
		t.Fatalf("a late callback after disconnect is 404: %d", r.status)
	}
}

func TestDisconnectDeletesOurWebhook(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	if !strings.HasPrefix(env.fake.WebhookURL(byok.ProductPix), "https://billing-byok-hooks-test.aoctech.app/w/inter/") {
		t.Fatal(env.fake.WebhookURL(byok.ProductPix))
	}
	if r := env.connWrite(t, "DELETE", "/v1.0/console/connections/inter", `{}`); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.fake.WebhookURL(byok.ProductPix) != "" {
		t.Fatal("ours is deleted")
	}
}

func TestDisconnectAnywayWhenTheBankIsDown(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	inv := env.finalizeInvoice(t, 1000)
	env.fake.FailNext(byok.OpCancelCharge, byok.ErrUnavailable)
	if r := env.connWrite(t, "DELETE", "/v1.0/console/connections/inter", `{"cancel_open_charges":true}`); r.code() != "byok_provider_unavailable" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	env.fake.FailNext(byok.OpCancelCharge, byok.ErrUnavailable)
	r := env.connWrite(t, "DELETE", "/v1.0/console/connections/inter", `{"cancel_open_charges":true,"force":true}`)
	if r.status != 200 || !bodyHas(r.body, env.chargeRefOf(inv)) {
		t.Fatalf("the merchant is told which charges to remove: %d %s", r.status, r.body)
	}
	if _, err := env.repo.GetConnection(ctxT(t), env.accountOrg, true, "inter"); err == nil {
		t.Fatal("disconnected anyway")
	}
}

func TestAForeignWebhookIsReplacedOnlyWithConfirmation(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	r := env.connWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":false}`)
	if r.code() != "byok_webhook_conflict" || !bodyHas(r.body, `"existing_host":"erp.example.com"`) || env.fake.WebhookURL(byok.ProductPix) != "https://erp.example.com/pix" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.connection(t).WebhookStatus != byokconn.WebhookConflict {
		t.Fatal("conflict recorded")
	}
	if r := env.connWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":true}`); r.status != 200 || !strings.HasPrefix(env.fake.WebhookURL(byok.ProductPix), "https://billing-byok-hooks-") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.connection(t).WebhookStatus != byokconn.WebhookRegistered {
		t.Fatal("registered")
	}
}

func TestUseForCollectionTurnsTheRailOnWithTheLateFees(t *testing.T) {
	env := newCollectEnv(t, collectTestWallet)
	env.connect(t)
	r := env.consoleWrite(t, "PUT", "/v1.0/console/collection", `{"provider":"inter","late_fee_percent":2.5,"monthly_interest_percent":1}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	org := env.tenantNow(t)
	if org.PayoutRail != billing.RailBYOK || org.PayoutProvider != "inter" || org.PayoutStatus != billing.PayoutEnabled ||
		org.Collection.FineBasisPoints != 250 || org.Collection.MonthlyInterestBasisPoints != 100 {
		t.Fatalf("%+v", org)
	}
	if r := env.consoleWrite(t, "PUT", "/v1.0/console/collection", `{"provider":"inter","late_fee_percent":25}`); r.status != 422 {
		t.Fatalf("over 20%%: %d %s", r.status, r.body)
	}
}

func TestATestTenantCannotReplaceTheSharedSandboxWebhook(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	env.fake.SetWebhookURL(byok.ProductPix, "https://pix.wallet.aoctech.app/pix/webhook")
	if r := env.connWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":true}`); r.status != 409 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.fake.WebhookURL(byok.ProductPix) != "https://pix.wallet.aoctech.app/pix/webhook" {
		t.Fatal("the shared key's webhook is the operator's")
	}
}
