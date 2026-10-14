package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

// rules are the environment's constraints on what this Lambda does. There is
// no legal gate (owner decision 2026-10-11, spec amendment): the merchant
// grants access to an official bank API. The op allowlist (byok.GroupOf) is a
// security control only: an unknown op or provider never reaches a bank.
type rules struct {
	forceSandbox bool // every non-prod environment: no real bank, whatever the mode
	// payoutOrgs are the account organizations whose connections may pay out
	// (byok.GroupPayout): money leaving an account is allowed for the named ones
	// only, whatever the api asks. Empty means nobody.
	payoutOrgs map[string]bool
}

// payoutAllowed is the gate for byok.GroupPayout.
func (r rules) payoutAllowed(accountOrgID string) bool {
	return accountOrgID != "" && r.payoutOrgs[accountOrgID]
}

func parseOrgs(csv string) map[string]bool {
	out := map[string]bool{}
	for _, o := range strings.Split(csv, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out[o] = true
		}
	}
	return out
}

type sandboxCreds struct {
	values          map[string]string
	account, pixKey string
}

type handler struct {
	sealer    *byokseal.Sealer
	providers map[string]byok.Provider
	rules     rules
	sandbox   func(context.Context) (sandboxCreds, error)
	bundles   *bundleCache
}

// bundleCache keeps opened bundles up to 15 minutes per container (§ 3.3).
type bundleCache struct {
	mu sync.Mutex
	m  map[[32]byte]cachedBundle
}

type cachedBundle struct {
	values map[string]string
	until  time.Time
}

func newBundleCache() *bundleCache { return &bundleCache{m: map[[32]byte]cachedBundle{}} }

func (b *bundleCache) get(k [32]byte) (map[string]string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[k]
	if !ok || time.Now().After(e.until) {
		delete(b.m, k)
		return nil, false
	}
	return e.values, true
}

func (b *bundleCache) put(k [32]byte, v map[string]string) {
	b.mu.Lock()
	b.m[k] = cachedBundle{values: v, until: time.Now().Add(15 * time.Minute)}
	b.mu.Unlock()
}

// handle never returns an invoke error for a provider or allowlist failure, and
// never logs a payload, a bundle, a token or an account number.
func (h *handler) handle(ctx context.Context, req byok.Request) (byok.Response, error) {
	resp := h.dispatch(ctx, req)
	slog.InfoContext(ctx, "byok outbound", "op", req.Op, "provider", req.Provider, "mode", req.Mode, "code", resp.Code)
	return resp, nil
}

func fail(err error) byok.Response {
	code := byok.ErrorCode(err)
	msg := code
	if code == "internal" {
		msg = "internal error"
	}
	return byok.Response{Code: code, Error: msg}
}

func ok(v any) byok.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return fail(err)
	}
	return byok.Response{Payload: b}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("%w: payload", byok.ErrInvalidRequest)
	}
	return v, nil
}

func (h *handler) dispatch(ctx context.Context, req byok.Request) byok.Response {
	p, found := h.providers[req.Provider]
	group := byok.GroupOf(req.Op)
	if !found || group == "" || (req.Mode != "live" && req.Mode != "test") {
		return fail(byok.ErrInvalidRequest)
	}
	sandboxRef := req.Connection.ID == "sandbox"
	if req.Mode == "test" && !sandboxRef {
		return fail(fmt.Errorf("%w: test mode uses the shared sandbox integration only", byok.ErrInvalidRequest))
	}
	sandbox := sandboxRef || h.rules.forceSandbox
	if group == byok.GroupSandbox && !sandbox {
		return fail(byok.ErrSandboxOnly)
	}
	// Money out: the sandbox moves none, a real bank only for the named organizations.
	if group == byok.GroupPayout && !sandbox && !h.rules.payoutAllowed(req.Connection.AccountOrganizationID) {
		return fail(byok.ErrOpRefused)
	}

	switch req.Op {
	case byok.OpConnect:
		a, err := decode[byok.ConnectArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return h.connect(ctx, p, req, a.Values, a.Account, a.PixKey, req.Connection.Version, sandbox)
	case byok.OpReseal:
		a, err := decode[byok.ResealArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		old, err := h.open(req)
		if err != nil {
			return fail(err)
		}
		next := map[string]string{}
		for k, v := range old {
			next[k] = v
		}
		next["cert_pem"], next["key_pem"] = a.CertPEM, a.KeyPEM
		return h.connect(ctx, p, req, next, req.Connection.Account, req.Connection.PixKey, req.Connection.Version+1, sandbox)
	}

	creds, err := h.credentials(ctx, req, sandbox)
	if err != nil {
		return fail(err)
	}
	tok := byok.Token{Value: req.OAuthToken}
	switch req.Op {
	case byok.OpToken:
		a, err := decode[byok.TokenArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		t, err := p.Token(ctx, creds, a.Set)
		return result(t, err)
	case byok.OpBalance:
		a, err := decode[byok.BalanceArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.Balance(ctx, creds, tok, a.At))
	case byok.OpStatementPage:
		a, err := decode[byok.StatementArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.StatementPage(ctx, creds, tok, a.From, a.To, a.Page))
	case byok.OpCreateDueCharge:
		a, err := decode[byok.DueCharge](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.CreateDueCharge(ctx, creds, tok, a))
	case byok.OpGetCharge, byok.OpGetPayment:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.GetCharge(ctx, creds, tok, a.Ref))
	case byok.OpUpdateCharge:
		a, err := decode[byok.UpdateArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.UpdateCharge(ctx, creds, tok, a.Ref, a.Amount))
	case byok.OpCancelCharge:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.CancelCharge(ctx, creds, tok, a.Ref))
	case byok.OpPayout:
		a, err := decode[byok.PayoutRequest](req.Payload)
		if err != nil {
			return fail(err)
		}
		pp, ok := p.(byok.Payouts)
		if !ok {
			return fail(byok.ErrInvalidRequest)
		}
		return result(pp.Payout(ctx, creds, tok, a))
	case byok.OpGetPayout:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		pp, ok := p.(byok.Payouts)
		if !ok {
			return fail(byok.ErrInvalidRequest)
		}
		return result(pp.GetPayout(ctx, creds, tok, a.Ref))
	case byok.OpRefund:
		a, err := decode[byok.RefundRequest](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.Refund(ctx, creds, tok, a))
	case byok.OpGetRefund:
		a, err := decode[byok.GetRefundArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.GetRefund(ctx, creds, tok, a.PaymentID, a.RefundID))
	case byok.OpGetWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		url, err := p.GetWebhook(ctx, creds, tok, a.Product)
		return result(byok.WebhookResult{URL: url}, err)
	case byok.OpRegisterWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.RegisterWebhook(ctx, creds, tok, a.Product, a.URL))
	case byok.OpDeleteWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.DeleteWebhook(ctx, creds, tok, a.Product))
	case byok.OpListCallbacks:
		a, err := decode[byok.CallbacksArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.ListCallbacks(ctx, creds, tok, a.Since))
	case byok.OpRetryCallbacks:
		a, err := decode[byok.RetryArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.RetryCallbacks(ctx, creds, tok, a.Refs))
	case byok.OpSimulatePayment:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.SimulatePayment(ctx, creds, tok, a.Ref))
	}
	return fail(byok.ErrInvalidRequest)
}

func result[T any](v T, err error) byok.Response {
	if err != nil {
		return fail(err)
	}
	return ok(v)
}

func (h *handler) aad(req byok.Request, version int) []byte {
	return byokseal.AAD(req.Connection.AccountOrganizationID, req.Mode, req.Provider, req.Connection.ID, version)
}

func (h *handler) open(req byok.Request) (map[string]string, error) {
	k := sha256.Sum256(append([]byte(req.Connection.Sealed), h.aad(req, req.Connection.Version)...))
	if v, ok := h.bundles.get(k); ok {
		return v, nil
	}
	plain, err := h.sealer.Open(req.Connection.Sealed, h.aad(req, req.Connection.Version))
	if err != nil {
		return nil, fmt.Errorf("%w: stored credentials do not open", byok.ErrCredentialsRefused)
	}
	var v map[string]string
	if err := json.Unmarshal(plain, &v); err != nil {
		return nil, fmt.Errorf("%w: bundle", byok.ErrCredentialsRefused)
	}
	h.bundles.put(k, v)
	return v, nil
}

func (h *handler) credentials(ctx context.Context, req byok.Request, sandbox bool) (byok.Credentials, error) {
	if req.Connection.ID == "sandbox" {
		sc, err := h.sandbox(ctx)
		if err != nil {
			return byok.Credentials{}, fmt.Errorf("%w: sandbox credentials", byok.ErrUnavailable)
		}
		return byok.Credentials{Values: sc.values, Account: sc.account, PixKey: sc.pixKey, Sandbox: true}, nil
	}
	v, err := h.open(req)
	if err != nil {
		return byok.Credentials{}, err
	}
	return byok.Credentials{Values: v, Account: req.Connection.Account, PixKey: req.Connection.PixKey, Sandbox: sandbox}, nil
}

func (h *handler) connect(ctx context.Context, p byok.Provider, req byok.Request, values map[string]string, account, pixKey string, version int, sandbox bool) byok.Response {
	got, err := p.Connect(ctx, byok.Credentials{Values: values, Account: account, PixKey: pixKey, Sandbox: sandbox})
	if err != nil {
		return fail(err)
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return fail(err)
	}
	got.Sealed, err = h.sealer.Seal(plain, h.aad(req, version))
	if err != nil {
		return fail(errors.Join(byok.ErrUnavailable, err))
	}
	return ok(got)
}
