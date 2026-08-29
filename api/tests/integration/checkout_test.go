//go:build integration

package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// The payment path, end to end, against a fake wallet.
//
// Fake wallet rather than a fake client, and now that the real route ships
// (ctech-wallet `services/charge_amount.go`) that choice matters more, not less:
// the fake answers exactly what docs/specs/2026-08-15-wallet-invoice-charge.md
// says, so it is the contract test between the two repositories. Nothing else
// checks that they still agree — there is no shared package and no consumer-
// driven test running in wallet's CI — so these tests are the only thing that
// fails if wallet renames a field.

const (
	testWalletSecret = "wallet-hmac-secret"
	testLinkSecret   = "checkout-link-secret"
)

// fakeWallet implements the spec's three behaviours that billing depends on:
// idempotency by key, the amount coming from the request, and a charge whose
// status only changes when the provider says so.
type fakeWallet struct {
	mu sync.Mutex
	// byKey is the idempotency store. A repeat with the same key returns the same
	// charge; that is what stops a double-click opening two PIX charges.
	byKey   map[string]*fakeCharge
	byID    map[string]*fakeCharge
	server  *httptest.Server
	opens   int
	ceiling int64
}

type fakeCharge struct {
	ID        string `json:"purchase_id"`
	Reference string `json:"sku"`
	UserID    string `json:"user_id"`
	Amount    int64  `json:"amount_expected"`
	Status    string `json:"status"`
	PixCode   string `json:"pix_copia_e_cola"`
	ExpiresAt string `json:"expires_at"`
}

func newFakeWallet(t *testing.T) *fakeWallet {
	t.Helper()
	w := &fakeWallet{
		byKey:   map[string]*fakeCharge{},
		byID:    map[string]*fakeCharge{},
		ceiling: 100000,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(rw http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
	})
	mux.HandleFunc("/v1.0/internal/wallet/charge", w.open)
	mux.HandleFunc("/v1.0/internal/wallet/charge/", w.get)
	w.server = httptest.NewServer(mux)
	t.Cleanup(w.server.Close)
	return w
}

func (w *fakeWallet) open(rw http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID         string `json:"user_id"`
		Amount         int64  `json:"amount_cents"`
		Reference      string `json:"reference"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	w.mu.Lock()
	defer w.mu.Unlock()

	if existing, ok := w.byKey[body.IdempotencyKey]; ok {
		// Spec § 2.4: the request hash binds the key to the amount *from the
		// request*, so the same key with a different amount is a conflict rather
		// than a silent replay of the original charge.
		if existing.Amount != body.Amount || existing.UserID != body.UserID {
			rw.WriteHeader(http.StatusConflict)
			return
		}
		writeJSON(rw, http.StatusCreated, existing)
		return
	}
	if body.Amount > w.ceiling {
		// Spec § 2.3: over the ceiling is a refusal, never a truncation.
		rw.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	w.opens++
	charge := &fakeCharge{
		ID:        "prdp_" + id.New(),
		Reference: body.Reference,
		UserID:    body.UserID,
		Amount:    body.Amount,
		Status:    wallet.StatusPending,
		PixCode:   "00020126BR.GOV.BCB.PIX" + body.IdempotencyKey,
		ExpiresAt: now().Add(30 * time.Minute).Format(time.RFC3339),
	}
	w.byKey[body.IdempotencyKey] = charge
	w.byID[charge.ID] = charge
	writeJSON(rw, http.StatusCreated, charge)
}

func (w *fakeWallet) get(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	charge, ok := w.byID[strings.TrimPrefix(r.URL.Path, "/v1.0/internal/wallet/charge/")]
	if !ok {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(rw, http.StatusOK, charge)
}

// settle is the provider confirming. Only this makes a charge payable, which is
// what the re-read in Collector.Confirm exists to observe.
func (w *fakeWallet) settle(chargeID string, amount int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if charge, ok := w.byID[chargeID]; ok {
		charge.Status = wallet.StatusConfirmed
		charge.Amount = amount
	}
}

// forget drops a charge wallet once returned. It is how the reconciliation
// tests produce the one condition that means "integration fault" rather than
// "customer did not pay": billing holds an id wallet cannot account for.
func (w *fakeWallet) forget(chargeID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.byID, chargeID)
}

func (w *fakeWallet) chargeFor(pixCode string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, charge := range w.byID {
		if charge.PixCode == pixCode {
			return id
		}
	}
	return ""
}

func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}

// payEnv is a portal environment whose app is wired to a fake wallet.
type payEnv struct {
	*portalEnv
	wallet *fakeWallet
	links  *services.PayLink
	// collector is the same object the app holds, built again against the same
	// fake. The reconciliation job has no route by design (ADR 0002), so a test
	// reaches it the way cmd/reconcile does — directly.
	collector *services.Collector
}

func newPayEnv(t *testing.T) *payEnv {
	t.Helper()
	base := newPortal(t)
	fake := newFakeWallet(t)

	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL = testIssuer
	cfg.CtechJWKSURL = base.jwksURL
	cfg.ServiceAudience = testAudience
	cfg.PortalOrganizationID = base.org.ID
	cfg.WalletBaseURL = fake.server.URL
	cfg.WalletTokenURL = fake.server.URL + "/token"
	cfg.WalletClientID = "billing"
	cfg.WalletClientSecret = "secret"
	cfg.WalletWebhookSecret = testWalletSecret
	cfg.CheckoutLinkSecret = testLinkSecret
	cfg.CheckoutBaseURL = "https://pay.test/c"

	server, err := app.Build(ctxT(t), &cfg, func() time.Time { return now() })
	if err != nil {
		t.Fatal(err)
	}
	base.app = server
	return &payEnv{
		portalEnv: base,
		wallet:    fake,
		links:     services.NewPayLink(testLinkSecret, "https://pay.test/c"),
		collector: services.NewCollector(
			repositories.NewInvoiceRepository(testDB, testCfg),
			repositories.NewPaymentRepository(testDB, testCfg),
			repositories.NewCustomerRepository(testDB, testCfg),
			repositories.NewOrganizationRepository(testDB, testCfg),
			repositories.NewSubscriptionRepository(testDB, testCfg),
			wallet.New(wallet.Config{
				BaseURL:       cfg.WalletBaseURL,
				TokenURL:      cfg.WalletTokenURL,
				ClientID:      cfg.WalletClientID,
				ClientSecret:  cfg.WalletClientSecret,
				WebhookSecret: cfg.WalletWebhookSecret,
				Cache:         cache.NewMemoryBackend(10),
			}),
		),
	}
}

// openCharge pays an invoice through the portal and returns the wallet charge id
// the checkout is waiting on.
func (e *payEnv) openCharge(t *testing.T, inv *billing.Invoice) string {
	t.Helper()
	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)
	var body paymentBody
	e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "").decode(t, &body)
	charge := e.wallet.chargeFor(body.Payment.PixCode)
	if charge == "" {
		t.Fatal("no charge opened")
	}
	return charge
}

// attemptFor reads back the invoice's only attempt.
func (e *payEnv) attemptFor(t *testing.T, invoiceID string) billing.PaymentAttempt {
	t.Helper()
	attempts, err := repositories.NewPaymentRepository(testDB, testCfg).
		ListAttempts(ctxT(t), e.org.ID, true, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("%d attempts, want 1", len(attempts))
	}
	return attempts[0]
}

func (e *payEnv) invoiceStatus(t *testing.T, invoiceID string) billing.InvoiceStatus {
	t.Helper()
	inv, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), e.org.ID, true, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Status
}

// openInvoice creates a finalized invoice addressed to the portal customer.
func (e *payEnv) openInvoice(t *testing.T) *billing.Invoice {
	t.Helper()
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	due := brcal.New(2026, time.March, 20)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctxT(t), inv, due, due, billing.CauseScheduler, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

func (e *payEnv) post(t *testing.T, path, token, body string) apiResponse {
	t.Helper()
	return e.do(t, http.MethodPost, path, token, "", body)
}

// notify posts a wallet-signed webhook, the way wallet's dispatcher would.
func (e *payEnv) notify(t *testing.T, chargeID string) apiResponse {
	t.Helper()
	body := fmt.Sprintf(`{"purchase_id":%q,"status":"confirmed","kind":"charge"}`, chargeID)
	mac := hmac.New(sha256.New, []byte(testWalletSecret))
	mac.Write([]byte(body))

	req := httptest.NewRequest(http.MethodPost, "/v1.0/internal/webhooks/wallet", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(wallet.HeaderSignature, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return apiResponse{status: resp.StatusCode, header: resp.Header}
}

type paymentBody struct {
	Payment struct {
		PixCode   string `json:"pix_code"`
		ExpiresAt string `json:"expires_at"`
	} `json:"payment"`
}

// The MVP's core demo: an open invoice becomes a QR code, and wallet's
// confirmation makes it PAID.
func TestPayingAnInvoiceEndToEnd(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)

	res := e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "")
	if res.status != http.StatusOK {
		t.Fatalf("pay: %d %s", res.status, res.body)
	}
	var body paymentBody
	res.decode(t, &body)
	if body.Payment.PixCode == "" {
		t.Fatalf("no PIX code returned: %s", res.body)
	}

	charge := e.wallet.chargeFor(body.Payment.PixCode)
	if charge == "" {
		t.Fatal("billing returned a PIX code for a charge wallet never opened")
	}

	// The attempt exists, is pending, and names the charge — the only evidence
	// that later makes SUCCEEDED legitimate.
	payments := repositories.NewPaymentRepository(testDB, testCfg)
	attempts, err := payments.ListAttempts(ctx, e.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Status != billing.AttemptPending || attempts[0].WalletChargeID != charge {
		t.Fatalf("attempts = %+v", attempts)
	}

	e.wallet.settle(charge, int64(inv.Total))
	if res := e.notify(t, charge); res.status != http.StatusOK {
		t.Fatalf("webhook: %d", res.status)
	}

	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	paid, err := invoices.Get(ctx, e.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Status != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", paid.Status)
	}
	if paid.AmountPaid != inv.Total {
		t.Fatalf("amount_paid = %d, want %d", paid.AmountPaid, inv.Total)
	}

	// And the audit says who: not "system", and not the operator — the rail.
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, inv.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawWallet bool
	for _, entry := range trail {
		if entry.Actor == "service:ctech-wallet" && entry.After == string(billing.InvoicePaid) {
			sawWallet = true
		}
	}
	if !sawWallet {
		t.Fatalf("no audit entry attributing the payment to wallet: %+v", trail)
	}
}

// The single most important property of the checkout: paying twice does not
// charge twice. A customer who reloads, or clicks again, or comes back from
// their bank app must meet the same QR code.
func TestPayingTwiceReusesTheSameCharge(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)

	var first, second paymentBody
	e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "").decode(t, &first)
	e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "").decode(t, &second)

	if first.Payment.PixCode != second.Payment.PixCode {
		t.Fatalf("two different PIX codes: %q vs %q", first.Payment.PixCode, second.Payment.PixCode)
	}
	if e.wallet.opens != 1 {
		t.Fatalf("wallet opened %d charges, want 1", e.wallet.opens)
	}
}

// The webhook is a wake-up signal, never payment authority. Wallet's own posture
// toward its provider, one layer up: if the charge is still pending when we ask,
// nothing moves — no matter what the body claimed.
func TestWebhookDoesNotSettleAnUnpaidCharge(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)

	var body paymentBody
	e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "").decode(t, &body)
	charge := e.wallet.chargeFor(body.Payment.PixCode)

	// Not settled in the fake: wallet still reports pending.
	if res := e.notify(t, charge); res.status != http.StatusOK {
		t.Fatalf("webhook: %d", res.status)
	}

	still, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctx, e.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != billing.InvoiceOpen {
		t.Fatalf("a lying webhook moved the invoice to %s", still.Status)
	}
}

// A forged webhook must not even reach the re-read. The signature is the first
// door; without it, anyone who can reach the endpoint can make billing ask wallet
// about arbitrary charges.
func TestWebhookRejectsABadSignature(t *testing.T) {
	e := newPayEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/v1.0/internal/webhooks/wallet",
		strings.NewReader(`{"purchase_id":"prdp_forged"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(wallet.HeaderSignature, "sha256=deadbeef")

	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// A settled amount that is not the amount billing opened the charge for is an
// alarm, never a partial payment. Marking an invoice paid for the wrong amount
// is worse than leaving it open: the second is visible on a screen somebody
// reads.
func TestWebhookRefusesAnAmountMismatch(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)

	var body paymentBody
	e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "").decode(t, &body)
	charge := e.wallet.chargeFor(body.Payment.PixCode)

	e.wallet.settle(charge, int64(inv.Total)-1)
	if res := e.notify(t, charge); res.status == http.StatusOK {
		t.Fatal("a short payment was accepted silently")
	}

	still, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctx, e.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != billing.InvoiceOpen {
		t.Fatalf("invoice is %s after a short payment", still.Status)
	}
}

// The public checkout: a signed link opens one invoice with no session at all.
func TestPaymentLinkOpensOneInvoiceWithoutSigningIn(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	link := e.links.Sign(e.org.ID, true, inv.ID)

	res := e.do(t, http.MethodGet, "/v1.0/checkout/"+link, "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("view: %d %s", res.status, res.body)
	}

	// The payload must carry what a payer needs and nothing that identifies them.
	// A forwarded link is a link somebody else opens.
	body := string(res.body)
	for _, forbidden := range []string{
		e.customer.Name, e.customer.Email, e.customer.ID, e.org.ID,
		"OPEN", "DRAFT", "PAID", "metadata", "organization_id",
	} {
		if forbidden != "" && strings.Contains(body, forbidden) {
			t.Errorf("the public checkout published %q:\n%s", forbidden, body)
		}
	}

	// Merely opening the link opens no charge — a crawler or a forwarded e-mail
	// must not create PIX charges.
	if e.wallet.opens != 0 {
		t.Fatalf("viewing the link opened %d charges", e.wallet.opens)
	}

	if res := e.post(t, "/v1.0/checkout/"+link+"/pay", "", ""); res.status != http.StatusOK {
		t.Fatalf("pay: %d %s", res.status, res.body)
	}
	if e.wallet.opens != 1 {
		t.Fatalf("wallet opened %d charges, want 1", e.wallet.opens)
	}
}

// Test mode has no rail, so it collects nothing (ADR 0004, second amendment).
//
// The failure this prevents is the expensive one, and it is quiet: wallet has no
// sandbox charge kind and billing holds one set of wallet credentials, so a
// test-mode invoice taken through checkout would open a **real** PIX charge for
// real money — and then never settle, because the notify-back route resolves the
// attempt in live mode only and correctly reports the charge as not billing's.
//
// So the assertion that matters is not the error. It is `opens == 0`: the guard
// has to fire before wallet is reached, because after that the money has already
// been asked for.
func TestATestModeInvoiceIsNeverCollected(t *testing.T) {
	e := newPayEnv(t)

	testOrg := newOrg(t, false)
	customer := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: testOrg.ID, Livemode: false,
		Name: "Sandbox Ltda", UserID: "usr_sandbox",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).
		Create(ctxT(t), customer, now()); err != nil {
		t.Fatal(err)
	}
	inv := newInvoiceFor(t, testOrg, customer.ID)
	due := brcal.New(2026, time.March, 20)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctxT(t), inv, due, due, billing.CauseScheduler, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	_, _, err := e.collector.Pay(ctxT(t), testOrg.ID, false, inv.ID, "test", "req_pay", now())
	if !errors.Is(err, services.ErrTestModeNotPayable) {
		t.Fatalf("Pay() error = %v, want ErrTestModeNotPayable", err)
	}
	if e.wallet.opens != 0 {
		t.Fatalf("a test-mode invoice opened %d real charges", e.wallet.opens)
	}

	// And the public link offers nothing to press, so nobody reaches the refusal
	// by clicking. The page still renders — the invoice is real and a reader may
	// legitimately look at it — it simply cannot be paid.
	link := e.links.Sign(testOrg.ID, false, inv.ID)
	res := e.do(t, http.MethodGet, "/v1.0/checkout/"+link, "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("view: %d %s", res.status, res.body)
	}
	if strings.Contains(string(res.body), `"payable":true`) {
		t.Errorf("the checkout offered to collect a test-mode invoice:\n%s", res.body)
	}

	if res := e.post(t, "/v1.0/checkout/"+link+"/pay", "", ""); res.status != http.StatusConflict {
		t.Fatalf("pay: %d %s, want 409", res.status, res.body)
	}
	if e.wallet.opens != 0 {
		t.Fatalf("the pay route opened %d real charges in test mode", e.wallet.opens)
	}
}

// The link is the only credential on that route, so a forged one must not read
// an invoice — and must not say whether the invoice exists.
func TestPaymentLinkRejectsAForgedToken(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	forged := services.NewPayLink("a-different-secret", "").Sign(e.org.ID, true, inv.ID)

	res := e.do(t, http.MethodGet, "/v1.0/checkout/"+forged, "", "", "")
	if res.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.status, res.body)
	}
}

// A draft is not a bill. Its amount can still change, so a link must not present
// one as something owed.
func TestPaymentLinkHidesADraftInvoice(t *testing.T) {
	e := newPayEnv(t)
	draft := newInvoiceFor(t, e.org, e.customer.ID)
	link := e.links.Sign(e.org.ID, true, draft.ID)

	if res := e.do(t, http.MethodGet, "/v1.0/checkout/"+link, "", "", ""); res.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.status, res.body)
	}
}

// The payout gate is server-side and on the write path (ADR 0005): a merchant
// who is not enabled cannot open a charge, whatever the console renders.
func TestChargesAreBlockedForAnUngatedOrganization(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	inv := e.openInvoice(t)

	orgs := repositories.NewOrganizationRepository(testDB, testCfg)
	if err := orgs.SetPayoutStatus(ctx, e.org, billing.PayoutNotConfigured, "test", "req_gate", now()); err != nil {
		t.Fatal(err)
	}

	token := e.portalToken(t, middleware.ScopeMyInvoicesWrite)
	res := e.post(t, "/v1.0/portal/invoices/"+inv.ID+"/pay", token, "")
	if res.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.status, res.body)
	}
	if e.wallet.opens != 0 {
		t.Fatalf("a gated organization opened %d charges", e.wallet.opens)
	}
}

// A customer with no ctech-account subject cannot be charged, because wallet's
// purchase path is keyed on a user id end to end. This is the structural half of
// why a third-party merchant's checkout is not merely unbuilt but blocked
// (docs/specs/2026-08-15-wallet-invoice-charge.md § 4).
func TestChargesAreRefusedWithoutAPayerAccount(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)

	anon := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Cliente Do Merchant",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, anon, now()); err != nil {
		t.Fatal(err)
	}
	inv := newInvoiceFor(t, e.org, anon.ID)
	due := brcal.New(2026, time.March, 20)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctx, inv, due, due, billing.CauseScheduler, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	link := e.links.Sign(e.org.ID, true, inv.ID)
	res := e.post(t, "/v1.0/checkout/"+link+"/pay", "", "")
	if res.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.status, res.body)
	}
	if e.wallet.opens != 0 {
		t.Fatalf("billing called wallet without a payer: %d opens", e.wallet.opens)
	}
}

// Cancellation, on the surface a consumer uses. At period end only, and it says
// so — a mid-period cancellation is a refund question, not this route.
func TestPortalCancellationIsAtPeriodEnd(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsWrite)

	sub := newActiveSubscription(t, e.org, e.customer.ID, e.priceID)
	res := e.post(t, "/v1.0/portal/subscriptions/"+sub.ID+"/cancel", token, "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	after, err := subs.Get(ctx, e.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != billing.SubscriptionActive || !after.CancelAtPeriodEnd {
		t.Fatalf("status = %s, cancel_at_period_end = %v; want ACTIVE and true",
			after.Status, after.CancelAtPeriodEnd)
	}

	// Repeating it is not an error and writes no second audit row: a double-click
	// must not double the timeline.
	if res := e.post(t, "/v1.0/portal/subscriptions/"+sub.ID+"/cancel", token, ""); res.status != http.StatusOK {
		t.Fatalf("repeat: %d %s", res.status, res.body)
	}
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, sub.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var cancellations int
	for _, entry := range trail {
		if entry.Cause == billing.CauseCustomer {
			cancellations++
		}
	}
	if cancellations != 1 {
		t.Fatalf("%d customer-caused audit rows, want 1", cancellations)
	}
}

// A read token must not be able to cancel. The scopes are split by direction for
// exactly this.
func TestPortalCancellationNeedsTheWriteScope(t *testing.T) {
	e := newPayEnv(t)
	sub := newActiveSubscription(t, e.org, e.customer.ID, e.priceID)
	readOnly := e.portalToken(t, middleware.ScopeMySubscriptionsRead)

	if res := e.post(t, "/v1.0/portal/subscriptions/"+sub.ID+"/cancel", readOnly, ""); res.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", res.status, res.body)
	}
}

// The console cancels immediately as well, and the audit names the person rather
// than a client id — which is the whole reason the console has its own route.
func TestConsoleCancellationIsAttributedToTheOperator(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	sub := newActiveSubscription(t, e.org, e.customer.ID, e.priceID)
	operator := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), middleware.ScopeSubscriptionsWrite)

	req := httptest.NewRequest(http.MethodPost,
		"/v1.0/console/subscriptions/"+sub.ID+"/cancel", strings.NewReader(`{"at_period_end":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+operator)
	req.Header.Set(middleware.ModeHeader, "live")
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	after, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != billing.SubscriptionCanceled {
		t.Fatalf("status = %s, want CANCELED", after.Status)
	}

	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, sub.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:" + e.org.OwnerUserID
	for _, entry := range trail {
		if entry.Actor == want {
			return
		}
	}
	t.Fatalf("no audit entry naming the operator %q: %+v", want, trail)
}

// newActiveSubscription creates a subscription the cancellation tests can end.
func newActiveSubscription(t *testing.T, org *billing.Organization, customerID, priceID string) *billing.Subscription {
	t.Helper()
	return newSubscriptionIn(t, org, customerID, priceID, billing.SubscriptionActive)
}

func newSubscriptionIn(t *testing.T, org *billing.Organization, customerID, priceID string, status billing.SubscriptionStatus) *billing.Subscription {
	t.Helper()
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: org.Livemode,
		CustomerID: customerID,
		Status:     status,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
		Anchor:     brcal.New(2026, time.March, 1),
	}
	item := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: org.Livemode,
		SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1,
	}
	if err := repositories.NewSubscriptionRepository(testDB, testCfg).
		Create(ctxT(t), sub, []billing.SubscriptionItem{item}, now()); err != nil {
		t.Fatal(err)
	}
	return sub
}

// --- What a payment does to the service it pays for -------------------------
//
// An invoice reaching PAID is only half of a settlement. The other half is the
// subscription: a first payment grants service, and a late payment gives it
// back. Neither happened before Collector.activateSubscription — the money
// landed, the invoice went PAID, and the subscription stayed exactly where
// dunning had left it.

// payInFull takes a subscription's invoice all the way through the rail.
func (e *payEnv) payInFull(t *testing.T, inv *billing.Invoice) {
	t.Helper()
	charge := e.openCharge(t, inv)
	e.wallet.settle(charge, int64(inv.Total))
	if res := e.notify(t, charge); res.status != http.StatusOK {
		t.Fatalf("webhook: %d", res.status)
	}
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
}

// openSubscriptionInvoice finalizes a bill for sub, addressed to the portal
// customer so the portal's own pay route can reach it.
func (e *payEnv) openSubscriptionInvoice(t *testing.T, sub *billing.Subscription) *billing.Invoice {
	t.Helper()
	inv := newSubscriptionInvoice(t, e.org, sub)
	due := brcal.New(2026, time.March, 20)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctxT(t), inv, due, due, billing.CauseScheduler, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

func (e *payEnv) subscriptionStatus(t *testing.T, subID string) billing.SubscriptionStatus {
	t.Helper()
	sub, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctxT(t), e.org.ID, true, subID)
	if err != nil {
		t.Fatal(err)
	}
	return sub.Status
}

// The regression that matters most, and the one that was live: dunning
// restricts a subscriber on D+10, the subscriber pays on D+12, and the service
// has to come back. Before the fix the invoice went PAID and the subscription
// stayed PAST_DUE forever — permanently gated by a bill they had settled.
func TestPaymentRecoversAPastDueSubscription(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)

	sub := newActiveSubscription(t, e.org, e.customer.ID, e.priceID)
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	if _, err := subs.Transition(ctx, sub, billing.SubscriptionPastDue,
		billing.CausePaymentFailed, "service:dunning", "", now()); err != nil {
		t.Fatal(err)
	}

	e.payInFull(t, e.openSubscriptionInvoice(t, sub))

	if got := e.subscriptionStatus(t, sub.ID); got != billing.SubscriptionActive {
		t.Fatalf("subscription is %s after payment, want ACTIVE", got)
	}

	// The trail has to say the payment did it. CauseInvoicePaid rather than the
	// cause that settled the charge, because what restored the service is the
	// money arriving — not which of the webhook or the reconciler noticed.
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, sub.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var recovered bool
	for _, entry := range trail {
		if entry.Cause == billing.CauseInvoicePaid &&
			entry.Action == string(billing.EventSubscriptionRecovered) &&
			entry.Actor == "service:ctech-wallet" {
			recovered = true
		}
	}
	if !recovered {
		t.Fatalf("no recovery attributed to the payment: %+v", trail)
	}
}

// The other edge: a subscription that never had service in the first place.
// INCOMPLETE exists precisely so a paid plan is not granted before it is paid,
// and it is only worth creating if something can leave it.
func TestPaymentActivatesAnIncompleteSubscription(t *testing.T) {
	e := newPayEnv(t)
	sub := newSubscriptionIn(t, e.org, e.customer.ID, e.priceID, billing.SubscriptionIncomplete)

	e.payInFull(t, e.openSubscriptionInvoice(t, sub))

	if got := e.subscriptionStatus(t, sub.ID); got != billing.SubscriptionActive {
		t.Fatalf("subscription is %s after its first payment, want ACTIVE", got)
	}
}

// The ordinary case, which is most of them: a renewal being paid changes
// nothing about an already-ACTIVE subscription, and must leave no audit row
// claiming it did. A timeline that records a state change on every renewal is a
// timeline nobody can read during an incident.
func TestPaymentLeavesAnActiveSubscriptionAlone(t *testing.T) {
	ctx := ctxT(t)
	e := newPayEnv(t)
	sub := newActiveSubscription(t, e.org, e.customer.ID, e.priceID)

	e.payInFull(t, e.openSubscriptionInvoice(t, sub))

	if got := e.subscriptionStatus(t, sub.ID); got != billing.SubscriptionActive {
		t.Fatalf("subscription is %s, want ACTIVE", got)
	}
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctx, e.org.ID, true, sub.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range trail {
		if entry.Cause == billing.CauseInvoicePaid {
			t.Fatalf("payment wrote a state change on an already-active subscription: %+v", entry)
		}
	}
}

// A one-off invoice — no subscription behind it — settles normally and gates
// nothing. The guard is a return, not a lookup that fails.
func TestPayingAOneOffInvoiceTouchesNoSubscription(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	if inv.SubscriptionID != "" {
		t.Fatal("fixture is not a one-off invoice")
	}
	e.payInFull(t, inv)
}
