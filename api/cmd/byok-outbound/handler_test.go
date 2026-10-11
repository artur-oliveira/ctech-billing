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

func newHandler(t *testing.T, gate gate) (*handler, *byoktest.Fake) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	s, err := byokseal.New(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	f := byoktest.New()
	return &handler{sealer: s, providers: map[string]byok.Provider{"inter": f}, gate: gate,
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

func TestLiveCollectOpsAreRefusedUnlessTheProviderIsCleared(t *testing.T) {
	for _, g := range []gate{{liveCollection: false, providers: []string{"inter"}}, {liveCollection: true}} {
		h, f := newHandler(t, g)
		ref := connect(t, h, "live")
		create := call(t, h, byok.Request{Op: byok.OpCreateDueCharge, Provider: "inter", Mode: "live", Connection: ref,
			OAuthToken: "t", Payload: payload(byok.DueCharge{Ref: "ctb01JABCDEFGHJKMNPQRSTVWXYZ", Amount: 100})})
		if create.Code != "op_refused" || f.Calls(byok.OpCreateDueCharge) != 0 {
			t.Fatalf("gate %+v: %+v, provider calls %d", g, create, f.Calls(byok.OpCreateDueCharge))
		}
		for _, op := range []byok.Op{byok.OpRegisterWebhook, byok.OpRefund, byok.OpUpdateCharge} {
			if r := call(t, h, byok.Request{Op: op, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(map[string]any{})}); r.Code != "op_refused" {
				t.Fatalf("%s: %+v", op, r)
			}
		}
		if r := call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.BalanceArgs{})}); r.Code != "" {
			t.Fatalf("read ops always work: %+v", r)
		}
		if r := call(t, h, byok.Request{Op: byok.OpDeleteWebhook, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.WebhookArgs{Product: byok.ProductPix})}); r.Code != "" {
			t.Fatalf("teardown ops always work: %+v", r)
		}
	}
	h, f := newHandler(t, gate{liveCollection: true, providers: []string{"inter"}})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpCreateDueCharge, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t",
		Payload: payload(byok.DueCharge{Ref: "ctb01JABCDEFGHJKMNPQRSTVWXYZ", Amount: 100})}); r.Code != "" || f.Calls(byok.OpCreateDueCharge) != 1 {
		t.Fatalf("cleared: %+v", r)
	}
}

func TestSimulatePaymentIsRefusedOutsideTheSandbox(t *testing.T) {
	h, _ := newHandler(t, gate{liveCollection: true, providers: []string{"inter"}})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpSimulatePayment, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.RefArgs{Ref: "x"})}); r.Code != "sandbox_only" {
		t.Fatalf("%+v", r)
	}
}

func TestTestModeOnlyEverUsesTheSandboxIntegration(t *testing.T) {
	h, _ := newHandler(t, gate{})
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
	h, _ := newHandler(t, gate{})
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
	h, f := newHandler(t, gate{})
	f.FailNext(byok.OpBalance, byok.ErrUnavailable)
	ref := connect(t, h, "live")
	call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "bearer-xyz", Payload: payload(byok.BalanceArgs{})})
	for _, secret := range []string{"s3cr3t", "bearer-xyz", ref.Sealed, "1234567"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
}
