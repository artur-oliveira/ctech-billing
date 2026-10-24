//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const (
	testIssuer   = "https://account.test"
	testAudience = "https://billing.test"
)

// apiEnv is a running billing API with a fake ctech-account behind it.
//
// The JWKS server is real and the tokens are really signed and really verified.
// A test that bypasses verification proves the handlers work for tokens the
// service would never accept.
// fakeVerifier stands in for the webhook challenge check, which the real
// guarded client could not run against httptest servers. It accepts every URL
// unless a test sets fn.
type fakeVerifier struct{ fn func(url string) error }

func (f *fakeVerifier) Verify(_ context.Context, url string) error {
	if f.fn == nil {
		return nil
	}
	return f.fn(url)
}

type apiEnv struct {
	verifier *fakeVerifier
	app      *fiber.App
	key      *rsa.PrivateKey
	jwksURL  string
	org      *billing.Organization
	client   string
	priceID  string
	// account is the fake ctech-account this env's server asks, unless the
	// configure func pointed AccountBaseURL elsewhere. accountOrg is the
	// account organization e.org is linked to, with e.org.OwnerUserID as its
	// owner: the console reaches e.org through it (ADR 0028).
	account    *fakeAccount
	accountOrg string
	accountURL string
}

func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	return newAPIWith(t, nil)
}

// useAccount points an app rebuilt from this env (the portal's, the payment
// env's) at the same fake ctech-account, so the console still resolves e.org.
func (e *apiEnv) useAccount(cfg *config.Config) {
	cfg.AccountBaseURL, cfg.AccountTokenURL = e.accountURL, e.accountURL+"/token"
	cfg.AccountClientID, cfg.AccountClientSecret = "billing-test", "secret"
}

// newAPIWith is newAPI with a change to its configuration before the server is
// built — a fake ctech-account behind the real client, for instance.
func newAPIWith(t *testing.T, configure func(*config.Config), opts ...app.Option) *apiEnv {
	t.Helper()
	ctx := ctxT(t)

	key, jwks := newJWKS(t)
	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL = testIssuer
	cfg.CtechJWKSURL = jwks
	cfg.ServiceAudience = testAudience
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}}
	srv := acct.serve(t)
	cfg.AccountBaseURL, cfg.AccountTokenURL = srv.URL, srv.URL+"/token"
	cfg.AccountClientID, cfg.AccountClientSecret = "billing-test", "secret"
	if configure != nil {
		configure(&cfg)
	}

	// A pinned clock: a billing service tested against the wall clock passes in
	// March and fails in February.
	clock := func() time.Time { return now() }

	verifier := &fakeVerifier{}
	server, err := app.Build(ctx, &cfg, clock, append([]app.Option{app.WithWebhookVerifier(verifier)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	accountOrg := newSpaceOrgID()
	org := newLinkedOrg(t, true, accountOrg)
	acct.set(accountOrg, org.OwnerUserID, "organization", "owner")
	acct.name(accountOrg, org.DisplayName)
	clientID := "cli_" + id.New()
	creds := repositories.NewCredentialRepository(testDB, testCfg)
	if err := creds.Create(ctx, &billing.APICredential{
		ClientID: clientID, OrganizationID: org.ID, Livemode: true,
		Description: "integration test", Active: true,
	}, now()); err != nil {
		t.Fatal(err)
	}

	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	product := &billing.Product{
		ID: id.NewWithPrefix(id.PrefixProduct), OrganizationID: org.ID, Livemode: true,
		Name: "DF-e Basic", Active: true,
	}
	if err := catalog.CreateProduct(ctx, product, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	price := &billing.Price{
		ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: org.ID, Livemode: true,
		ProductID: product.ID, Type: billing.PriceFixed, Currency: billing.CurrencyBRL,
		UnitAmount: 4990,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
	}
	if err := catalog.CreatePrice(ctx, price, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	return &apiEnv{verifier: verifier, app: server, key: key, jwksURL: jwks, org: org, client: clientID, priceID: price.ID, account: acct, accountOrg: accountOrg, accountURL: srv.URL}
}

// token mints an access token. sid empty means an M2M client_credentials token.
func (e *apiEnv) token(t *testing.T, clientID, sid string, scopes ...string) string {
	t.Helper()
	return e.tokenWith(t, nil, clientID, sid, scopes...)
}

// tokenWith is token plus extra claims (last_mfa_at, for instance).
func (e *apiEnv) tokenWith(t *testing.T, extra map[string]any, clientID, sid string, scopes ...string) string {
	t.Helper()
	claims := jwt.MapClaims{
		// token_use is part of ctech-account's contract: the verifier rejects
		// anything that is not an access token, so an id token or a refresh token
		// cannot be replayed against this API.
		"token_use": "access",
		"sub":       clientID,
		"azp":       clientID,
		"iss":       testIssuer,
		"aud":       testAudience,
		"scope":     strings.Join(scopes, " "),
		// Real wall-clock validity: the JWT library checks exp against the real
		// clock, not the service's pinned one.
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	if sid != "" {
		claims["sid"] = sid
	}
	for k, v := range extra {
		claims[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(e.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r apiResponse) decode(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal(r.body, into); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (e *apiEnv) do(t *testing.T, method, path, token, idemKey, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey == "" && method != http.MethodGet && (strings.HasPrefix(path, "/v1.0/console/") || strings.HasPrefix(path, "/v1.0/portal/")) {
		idemKey = id.New() // a browser write always carries one (the UI's per-intent key)
	}
	if idemKey != "" {
		req.Header.Set(middleware.IdempotencyHeader, idemKey)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

func TestHealthNeedsNoToken(t *testing.T) {
	e := newAPI(t)
	res := e.do(t, http.MethodGet, "/v1.0/health", "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body map[string]any
	res.decode(t, &body)
	// The timezone moved to the report's clock check (TestHealthCheckReportsTheBillingTimezone).
	if body["serviceId"] != "CTech Billing" {
		t.Fatalf("serviceId = %v", body["serviceId"])
	}
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	e := newAPI(t)
	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"garbage token", "not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := e.do(t, http.MethodGet, "/v1.0/invoices", tc.token, "", "")
			if res.status != http.StatusUnauthorized {
				t.Fatalf("status = %d: %s", res.status, res.body)
			}
			if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
				t.Fatalf("content type = %q, want RFC 7807", ct)
			}
		})
	}
}

func TestScopeIsEnforcedPerRoute(t *testing.T) {
	e := newAPI(t)

	// A token with the read scope must not be able to write.
	readOnly := e.token(t, e.client, "", middleware.ScopeSubscriptionsRead)
	res := e.do(t, http.MethodPost, "/v1.0/subscriptions", readOnly, "k1", `{"customer_id":"x","price_id":"y"}`)
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// TestUserTokensAreRejectedOnM2MRoutes: a browser session token must never act
// as an integration, even if it somehow carries the scope.
func TestUserTokensAreRejectedOnM2MRoutes(t *testing.T) {
	e := newAPI(t)
	userToken := e.token(t, e.client, "sess_123", middleware.ScopeCustomersWrite)
	res := e.do(t, http.MethodPost, "/v1.0/customers", userToken, "k1", `{"name":"Fulana"}`)
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// TestUnknownClientIsRejected: a validly signed token whose client billing does
// not know cannot act for any tenant.
func TestUnknownClientIsRejected(t *testing.T) {
	e := newAPI(t)
	stranger := e.token(t, "cli_unknown", "", middleware.ScopeCustomersWrite)
	res := e.do(t, http.MethodPost, "/v1.0/customers", stranger, "k1", `{"name":"Fulana"}`)
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

func TestSubscribeThroughTheAPI(t *testing.T) {
	e := newAPI(t)
	writeToken := e.token(t, e.client, "",
		middleware.ScopeCustomersWrite, middleware.ScopeSubscriptionsWrite,
		middleware.ScopeInvoicesRead, middleware.ScopeEntitlementsRead)

	// 1. Create the customer.
	res := e.do(t, http.MethodPost, "/v1.0/customers", writeToken, "cust-1",
		`{"name":"Fulana de Tal","email":"f@example.com","tax_id":"12345678909","external_ref":"user_42"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("create customer: %d %s", res.status, res.body)
	}
	var customer struct {
		ID          string `json:"id"`
		TaxIDMasked string `json:"tax_id_masked"`
	}
	res.decode(t, &customer)
	// The full tax id must never leave the service.
	if customer.TaxIDMasked != "•••••••8909" {
		t.Fatalf("tax id mask = %q", customer.TaxIDMasked)
	}
	if bytes.Contains(res.body, []byte("12345678909")) {
		t.Fatal("the response leaked the full tax id")
	}

	// 2. Subscribe. Billed in advance, so the first invoice comes back with it.
	body := `{"customer_id":"` + customer.ID + `","items":[{"price_id":"` + e.priceID + `"}],"anchor":"2026-03-10"}`
	res = e.do(t, http.MethodPost, "/v1.0/subscriptions", writeToken, "sub-1", body)
	if res.status != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", res.status, res.body)
	}
	var created struct {
		Subscription struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Entitled bool   `json:"entitled"`
		} `json:"subscription"`
		Invoice struct {
			ID     string `json:"id"`
			Number int64  `json:"number"`
			Total  int64  `json:"total"`
			Status string `json:"status"`
		} `json:"invoice"`
	}
	res.decode(t, &created)
	// INCOMPLETE, not ACTIVE: a paid plan billed in advance does not grant service
	// before its first invoice is paid. What the integration gets back is an
	// invoice to send the customer to, and no entitlement until they use it.
	if created.Subscription.Status != "INCOMPLETE" || created.Subscription.Entitled {
		t.Fatalf("subscription = %+v", created.Subscription)
	}
	if created.Invoice.ID == "" || created.Invoice.Total != 4990 || created.Invoice.Status != "OPEN" {
		t.Fatalf("first invoice = %+v", created.Invoice)
	}

	// 3. Read the invoice back with its lines.
	res = e.do(t, http.MethodGet, "/v1.0/invoices/"+created.Invoice.ID, writeToken, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("get invoice: %d %s", res.status, res.body)
	}
	var invoice struct {
		Number  int64      `json:"number"`
		DueDate brcal.Date `json:"due_date"`
		Overdue bool       `json:"overdue"`
		Lines   []struct {
			Description string `json:"description"`
			Amount      int64  `json:"amount"`
		} `json:"lines"`
	}
	res.decode(t, &invoice)
	if invoice.Number != 1 {
		t.Fatalf("number = %d", invoice.Number)
	}
	if len(invoice.Lines) != 1 || invoice.Lines[0].Description != "DF-e Basic" {
		t.Fatalf("lines = %+v", invoice.Lines)
	}
	// Anchored 10 March 2026, a Tuesday — no roll.
	if invoice.DueDate != brcal.New(2026, time.March, 10) {
		t.Fatalf("due date = %s", invoice.DueDate)
	}

	// 4. Entitlements, by the caller's own reference.
	res = e.do(t, http.MethodGet, "/v1.0/entitlements?customer_ref=user_42", writeToken, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("entitlements: %d %s", res.status, res.body)
	}
	var ent struct {
		Entitled      bool `json:"entitled"`
		Subscriptions []struct {
			ID       string `json:"id"`
			Entitled bool   `json:"entitled"`
		} `json:"subscriptions"`
	}
	res.decode(t, &ent)
	// Not entitled yet, and that is the point of INCOMPLETE: the consuming
	// product asks this before letting anyone in, and the answer before the first
	// payment is no.
	if ent.Entitled || len(ent.Subscriptions) != 1 || ent.Subscriptions[0].Entitled {
		t.Fatalf("entitlements = %+v", ent)
	}

	// The first payment, applied directly. This walk has no wallet — the settled
	// path is TestPaymentActivatesAnIncompleteSubscription — and what step 5 is
	// about is scheduling a cancellation, which is a thing only an active
	// subscriber can do.
	activateSubscription(t, e.org, created.Subscription.ID)

	// 5. Cancel at period end changes nothing about the status.
	res = e.do(t, http.MethodPost, "/v1.0/subscriptions/"+created.Subscription.ID+"/cancel",
		writeToken, "cancel-1", `{"at_period_end":true}`)
	if res.status != http.StatusOK {
		t.Fatalf("cancel: %d %s", res.status, res.body)
	}
	var canceled struct {
		Status            string `json:"status"`
		CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	}
	res.decode(t, &canceled)
	if canceled.Status != "ACTIVE" || !canceled.CancelAtPeriodEnd {
		t.Fatalf("scheduled cancellation must not change the status: %+v", canceled)
	}
}

// TestIdempotencyReplaysTheFirstResponse is the property § 12.5 requires: a
// retry must be indistinguishable from the original, and must not create a
// second resource.
func TestIdempotencyReplaysTheFirstResponse(t *testing.T) {
	e := newAPI(t)
	token := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeCustomersRead)
	body := `{"name":"Fulana","email":"f@example.com"}`

	first := e.do(t, http.MethodPost, "/v1.0/customers", token, "retry-me", body)
	if first.status != http.StatusCreated {
		t.Fatalf("first: %d %s", first.status, first.body)
	}
	second := e.do(t, http.MethodPost, "/v1.0/customers", token, "retry-me", body)
	if second.status != first.status {
		t.Fatalf("replay status = %d, want %d", second.status, first.status)
	}
	if !bytes.Equal(first.body, second.body) {
		t.Fatalf("replay returned a different body:\n%s\n%s", first.body, second.body)
	}
	if second.header.Get("Idempotent-Replay") != "true" {
		t.Fatal("a replay must be marked, so a client can tell without diffing")
	}

	// Different body, same key: a conflict, never a replay of the wrong thing.
	conflict := e.do(t, http.MethodPost, "/v1.0/customers", token, "retry-me", `{"name":"Outra Pessoa"}`)
	if conflict.status != http.StatusConflict {
		t.Fatalf("conflict status = %d: %s", conflict.status, conflict.body)
	}
	var p struct {
		Type string `json:"type"`
	}
	conflict.decode(t, &p)
	if p.Type != "/problems/idempotency-conflict" {
		t.Fatalf("problem type = %q", p.Type)
	}
}

// Retries racing each other with one key run the operation once: the rest
// replay its answer or are told it is still running, never a second customer.
func TestConcurrentRetriesWithOneKeyRunOnce(t *testing.T) {
	e := newAPI(t)
	token := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	body := `{"name":"Fulana","email":"f@example.com"}`
	var wg sync.WaitGroup
	results := make(chan apiResponse, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- e.do(t, http.MethodPost, "/v1.0/customers", token, "race-me", body)
		}()
	}
	wg.Wait()
	close(results)
	var created []byte
	for r := range results {
		switch {
		case r.status == http.StatusCreated && created == nil:
			created = r.body
		case r.status == http.StatusCreated:
			if !bytes.Equal(created, r.body) {
				t.Fatalf("two operations ran:\n%s\n%s", created, r.body)
			}
		case r.status == http.StatusConflict && strings.Contains(string(r.body), "idempotency_in_progress"):
		default:
			t.Fatalf("unexpected %d %s", r.status, r.body)
		}
	}
	if created == nil {
		t.Fatal("nothing ran")
	}
	again := e.do(t, http.MethodPost, "/v1.0/customers", token, "race-me", body)
	if again.status != http.StatusCreated || !bytes.Equal(again.body, created) {
		t.Fatalf("after the race: %d %s", again.status, again.body)
	}
}

func TestMutatingRoutesRequireAnIdempotencyKey(t *testing.T) {
	e := newAPI(t)
	token := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	res := e.do(t, http.MethodPost, "/v1.0/customers", token, "", `{"name":"Fulana"}`)
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// TestCredentialsCannotReachAnotherTenant: the tenant comes from the credential,
// so an id from another organization is simply not there.
func TestCredentialsCannotReachAnotherTenant(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeCustomersRead)

	// A customer belonging to a different organization entirely.
	other := newOrg(t, true)
	stranger := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: other.ID, Livemode: true,
		Name: "De Outra Org",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, stranger, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	res := e.do(t, http.MethodGet, "/v1.0/customers/"+stranger.ID, token, "", "")
	// 404, not 403: a 403 would confirm the id exists somewhere.
	if res.status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

func TestRequestIDIsEchoed(t *testing.T) {
	e := newAPI(t)
	res := e.do(t, http.MethodGet, "/v1.0/health", "", "", "")
	if res.header.Get(middleware.RequestIDHeader) == "" {
		t.Fatal("every response must carry a correlation id")
	}
}

// --- fake ctech-account -----------------------------------------------------

func newJWKS(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": "test-key",
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(server.Close)
	return key, server.URL
}

func mustEnv(t *testing.T, name string) string {
	t.Helper()
	v := envOrEmpty(name)
	if v == "" {
		t.Fatalf("%s is required", name)
	}
	return v
}
