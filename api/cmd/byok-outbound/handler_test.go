package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

func newHandler(t *testing.T, r rules) (*handler, *byoktest.Fake) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	s, err := byokseal.New(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	f := byoktest.New()
	return &handler{sealer: s, providers: map[string]byok.Provider{"inter": f}, rules: r,
		sandbox: func(context.Context) (sandboxCreds, error) {
			return sandboxCreds{values: map[string]string{"client_id": "ctech"}, account: "999", pixKey: "evp"}, nil
		}, bundles: newBundleCache()}, f
}

func call(t *testing.T, h *handler, req byok.Request) byok.Response {
	t.Helper()
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("a provider failure is never an invoke error: %v", err)
	}
	return resp
}

func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func connect(t *testing.T, h *handler, mode string) byok.ConnectionRef {
	ref := byok.ConnectionRef{AccountOrganizationID: "org-a", ID: "conn-1", Version: 1}
	resp := call(t, h, byok.Request{Op: byok.OpConnect, Provider: "inter", Mode: mode, Connection: ref,
		Payload: payload(byok.ConnectArgs{Values: map[string]string{"client_id": "c", "client_secret": "s3cr3t"}, Account: "1234567"})})
	if resp.Code != "" {
		t.Fatalf("connect: %+v", resp)
	}
	var got byok.Connected
	_ = json.Unmarshal(resp.Payload, &got)
	if !strings.HasPrefix(got.Sealed, "b1.") || strings.Contains(string(resp.Payload), "s3cr3t") {
		t.Fatalf("connect must return only ciphertext: %s", resp.Payload)
	}
	ref.Sealed, ref.Account = got.Sealed, "1234567"
	return ref
}

func TestLiveCollectOpsReachTheProviderAndUnknownOpsAreRefused(t *testing.T) {
	// Owner decision 2026-10-11 (spec amendment): no legal gate. The allowlist
	// is a security control only: every known op is accepted in live, an
	// unknown one never reaches a provider.
	h, f := newHandler(t, rules{})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpCreateDueCharge, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t",
		Payload: payload(byok.DueCharge{Ref: "ctb01JABCDEFGHJKMNPQRSTVWXYZ", Amount: 100})}); r.Code != "" || f.Calls(byok.OpCreateDueCharge) != 1 {
		t.Fatalf("a live collect op is accepted: %+v", r)
	}
	for _, op := range []byok.Op{"DeleteAllCharges", "", "connect"} {
		if r := call(t, h, byok.Request{Op: op, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t"}); r.Code != "invalid_request" {
			t.Fatalf("unknown op %q: %+v", op, r)
		}
	}
	if r := call(t, h, byok.Request{Op: byok.OpBalance, Provider: "other", Mode: "live", Connection: ref, OAuthToken: "t"}); r.Code != "invalid_request" {
		t.Fatalf("unknown provider: %+v", r)
	}
}

func TestSimulatePaymentIsRefusedOutsideTheSandbox(t *testing.T) {
	h, _ := newHandler(t, rules{})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpSimulatePayment, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.RefArgs{Ref: "x"})}); r.Code != "sandbox_only" {
		t.Fatalf("%+v", r)
	}
}

func TestTestModeOnlyEverUsesTheSandboxIntegration(t *testing.T) {
	h, _ := newHandler(t, rules{})
	r := call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "test",
		Connection: byok.ConnectionRef{ID: "conn-1", Sealed: "b1.x"}, OAuthToken: "t", Payload: payload(byok.BalanceArgs{})})
	if r.Code != "invalid_request" {
		t.Fatalf("a merchant connection in test mode: %+v", r)
	}
	r = call(t, h, byok.Request{Op: byok.OpToken, Provider: "inter", Mode: "test", Connection: byok.ConnectionRef{ID: "sandbox"}, Payload: payload(byok.TokenArgs{Set: byok.CapabilityStatement})})
	if r.Code != "" {
		t.Fatalf("sandbox token: %+v", r)
	}
}

func TestResealKeepsTheSecretAndBindsTheNextVersion(t *testing.T) {
	h, _ := newHandler(t, rules{})
	ref := connect(t, h, "live")
	resp := call(t, h, byok.Request{Op: byok.OpReseal, Provider: "inter", Mode: "live", Connection: ref,
		Payload: payload(byok.ResealArgs{CertPEM: "new-cert", KeyPEM: "new-key"})})
	var got byok.Connected
	_ = json.Unmarshal(resp.Payload, &got)
	plain, err := h.sealer.Open(got.Sealed, byokseal.AAD("org-a", "live", "inter", "conn-1", 2))
	if err != nil || !strings.Contains(string(plain), `"client_secret":"s3cr3t"`) || !strings.Contains(string(plain), "new-cert") {
		t.Fatalf("resealed bundle: %v", err)
	}
}

func TestPayloadsAndSecretsAreNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	h, f := newHandler(t, rules{})
	f.FailNext(byok.OpBalance, byok.ErrUnavailable)
	ref := connect(t, h, "live")
	call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "bearer-xyz", Payload: payload(byok.BalanceArgs{})})
	for _, secret := range []string{"s3cr3t", "bearer-xyz", ref.Sealed, "1234567"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
}

func TestMoneyOutIsAllowedOnlyForTheNamedOrganizations(t *testing.T) {
	pay := func(t *testing.T, r rules) (byok.Response, int) {
		h, f := newHandler(t, r)
		ref := connect(t, h, "live")
		resp := call(t, h, byok.Request{Op: byok.OpPayout, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t",
			Payload: payload(byok.PayoutRequest{Ref: "8bbdede4-35db-4ec9-b652-e176841e62c8", Amount: 100, PixKey: "39053344705"})})
		return resp, f.PayoutCalls()
	}
	if r, calls := pay(t, rules{}); r.Code != "op_refused" || calls != 0 {
		t.Fatalf("nobody is named: %+v calls=%d", r, calls)
	}
	if r, calls := pay(t, rules{payoutOrgs: parseOrgs("org-b, org-c")}); r.Code != "op_refused" || calls != 0 {
		t.Fatalf("another organization is named: %+v calls=%d", r, calls)
	}
	if r, calls := pay(t, rules{payoutOrgs: parseOrgs(" org-a ,org-b")}); r.Code != "" || calls != 1 {
		t.Fatalf("the named organization pays: %+v calls=%d", r, calls)
	}
}

func TestParseOrgsIgnoresBlanks(t *testing.T) {
	got := parseOrgs(" a, ,b,, ")
	if len(got) != 2 || !got["a"] || !got["b"] || (rules{}).payoutAllowed("") {
		t.Fatalf("%v", got)
	}
}

func TestResealWithNoNewPairRetestsTheStoredCredentials(t *testing.T) {
	// An operator refresh: nothing to replace, so the stored certificate and key stay
	// and the connection is tested (and sealed under the next version) again.
	h, f := newHandler(t, rules{})
	ref := connect(t, h, "live")
	resp := call(t, h, byok.Request{Op: byok.OpReseal, Provider: "inter", Mode: "live", Connection: ref, Payload: payload(byok.ResealArgs{})})
	if resp.Code != "" || f.Calls(byok.OpConnect) != 2 {
		t.Fatalf("%+v connects=%d", resp, f.Calls(byok.OpConnect))
	}
	var got byok.Connected
	_ = json.Unmarshal(resp.Payload, &got)
	plain, err := h.sealer.Open(got.Sealed, byokseal.AAD("org-a", "live", "inter", "conn-1", 2))
	if err != nil || !strings.Contains(string(plain), `"client_secret":"s3cr3t"`) || strings.Contains(string(plain), `"cert_pem":""`) {
		t.Fatalf("the stored bundle is kept as it was: %v %s", err, plain)
	}
}
