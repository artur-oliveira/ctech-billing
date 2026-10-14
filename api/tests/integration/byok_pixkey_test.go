//go:build integration

package integration

import (
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

const pixKeyPath = "/v1.0/console/connections/inter/pix-key"

func TestChangingThePixKeyMovesTheConnectionAndKeepsTheNotice(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	old := env.connection(t).PixKey
	env.fake.SetWebhookURL(byok.ProductPix, "") // the new key has no notice yet
	r := env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"evp-new-key"}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	c := env.connection(t)
	if c.PixKey != "evp-new-key" || c.PixKey == old {
		t.Fatalf("the key is persisted: %q (was %q)", c.PixKey, old)
	}
	if c.WebhookStatus != byokconn.WebhookRegistered {
		t.Fatalf("the notice follows the key: %q", c.WebhookStatus)
	}
}

func TestChangingThePixKeyIsRefusedWhileChargesAreOpen(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	env.finalizeInvoice(t, 1000)
	old := env.connection(t).PixKey
	r := env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"evp-new-key"}`)
	if r.code() != "byok_open_charges" || !bodyHas(r.body, `"count":1`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.connection(t).PixKey != old {
		t.Fatal("a refused change leaves the key alone")
	}
}

func TestChangingToTheSamePixKeyDoesNothing(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	old := env.connection(t).PixKey
	if r := env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"`+old+`"}`); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.connection(t).PixKey != old {
		t.Fatal("unchanged")
	}
}

func TestChangingThePixKeyNeedsAKey(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	for _, body := range []string{`{}`, `{"pix_key":"   "}`, `{"pix_key":"` + strings.Repeat("a", 200) + `"}`} {
		if r := env.connWrite(t, "PUT", pixKeyPath, body); r.status != 422 {
			t.Fatalf("%q: %d %s", body, r.status, r.body)
		}
	}
}

func TestAKeyTheBankDoesNotKnowChangesNothing(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	old := env.connection(t).PixKey
	// Inter answers GET /webhook/{key} 404 for a key without a notice and for
	// one it does not know alike, so only the PUT can tell them apart.
	env.fake.SetWebhookURL(byok.ProductPix, "")
	env.fake.FailNext(byok.OpRegisterWebhook, byok.ErrNotFound)
	r := env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"evp-unknown"}`)
	if r.status != 422 || r.code() != "byok_pix_key_unknown" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if env.connection(t).PixKey != old {
		t.Fatal("the key is only saved once the bank has answered for it")
	}
}

func TestAForeignNoticeOnTheNewKeyNeedsConfirmation(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	env.fake.SetWebhookURL(byok.ProductPix, "https://pix.wallet.example.com/pix/webhook?hmac=x")
	r := env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"evp-shared"}`)
	if r.status != 409 || r.code() != "byok_webhook_conflict" || !bodyHas(r.body, "pix.wallet.example.com") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r = env.connWrite(t, "PUT", pixKeyPath, `{"pix_key":"evp-shared","replace_existing":true}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
