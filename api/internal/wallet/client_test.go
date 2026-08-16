package wallet

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The signature is what tells billing a notify-back came from wallet at all.
// Getting it wrong in the permissive direction means anyone who can reach the
// webhook can wake up a settlement; the re-read stops them being believed, but
// this is the first door.
//
// The expected value is computed here rather than through the client, so the
// test fails if the client's construction drifts from wallet's
// (ctech-wallet/api/internal/services/m2m_webhook.go:148) instead of drifting
// along with it.
func TestVerifySignature(t *testing.T) {
	const secret = "shhh"
	c := New(Config{WebhookSecret: secret})
	body := []byte(`{"purchase_id":"prdp_1","status":"confirmed"}`)
	good := sign(body, secret)

	if !c.VerifySignature(body, good) {
		t.Fatalf("rejected a valid signature %q", good)
	}

	for name, tc := range map[string]struct {
		body []byte
		sig  string
	}{
		"empty signature":     {body, ""},
		"wrong key":           {body, sign(body, "not-the-key")},
		"different body":      {[]byte(`{"purchase_id":"prdp_2"}`), good},
		"missing prefix":      {body, good[len("sha256="):]},
		"truncated signature": {body, good[:len(good)-2]},
	} {
		t.Run(name, func(t *testing.T) {
			if c.VerifySignature(tc.body, tc.sig) {
				t.Fatal("accepted")
			}
		})
	}
}

// An unconfigured client verifies nothing. Without this, a deployment missing
// WALLET_WEBHOOK_SECRET would accept every call as authentic — the failure mode
// that looks like everything working.
func TestVerifySignatureWithoutASecret(t *testing.T) {
	c := New(Config{})
	body := []byte("{}")
	if c.VerifySignature(body, sign(body, "")) {
		t.Fatal("accepted a signature with no secret configured")
	}
}

func TestConfigEnabledNeedsEveryField(t *testing.T) {
	full := Config{
		BaseURL: "https://wallet.test", TokenURL: "https://account.test/token",
		ClientID: "cli", ClientSecret: "sec", WebhookSecret: "hmac",
	}
	if !full.Enabled() {
		t.Fatal("a complete configuration is not enabled")
	}
	// Each field blanked in turn: a half-configured wallet must disable
	// collection, never half-enable it.
	for name, blank := range map[string]func(*Config){
		"base url":       func(c *Config) { c.BaseURL = "" },
		"token url":      func(c *Config) { c.TokenURL = "" },
		"client id":      func(c *Config) { c.ClientID = "" },
		"client secret":  func(c *Config) { c.ClientSecret = "" },
		"webhook secret": func(c *Config) { c.WebhookSecret = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := full
			blank(&cfg)
			if cfg.Enabled() {
				t.Fatal("enabled without it")
			}
		})
	}
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
