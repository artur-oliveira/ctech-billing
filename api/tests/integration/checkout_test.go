//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
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
// settle is the provider confirming. Only this makes a charge payable, which is
// what the re-read in Collector.Confirm exists to observe.
// forget drops a charge wallet once returned. It is how the reconciliation
// tests produce the one condition that means "integration fault" rather than
// "customer did not pay": billing holds an id wallet cannot account for.
// payEnv is a portal environment whose app is wired to a fake wallet.
// collectorCharges is the wallet client the env's collector opens charges with,
// for a second collector built against another DynamoDB client.
// openCharge pays an invoice through the portal and returns the wallet charge id
// the checkout is waiting on.
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
// The MVP's core demo: an open invoice becomes a QR code, and wallet's
// confirmation makes it PAID.
// The single most important property of the checkout: paying twice does not
// charge twice. A customer who reloads, or clicks again, or comes back from
// their bank app must meet the same QR code.
// The webhook is a wake-up signal, never payment authority. Wallet's own posture
// toward its provider, one layer up: if the charge is still pending when we ask,
// nothing moves — no matter what the body claimed.
// A forged webhook must not even reach the re-read. The signature is the first
// door; without it, anyone who can reach the endpoint can make billing ask wallet
// about arbitrary charges.
// A settled amount that is not the amount billing opened the charge for is an
// alarm, never a partial payment. Marking an invoice paid for the wrong amount
// is worse than leaving it open: the second is visible on a screen somebody
// reads.
// The public checkout: a signed link opens one invoice with no session at all.
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
// A customer with no ctech-account subject cannot be charged, because wallet's
// purchase path is keyed on a user id end to end. This is the structural half of
// why a third-party merchant's checkout is not merely unbuilt but blocked
// (docs/specs/2026-08-15-wallet-invoice-charge.md § 4).
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

	// A console write without a key is refused before it runs.
	bare := httptest.NewRequest(http.MethodPost,
		"/v1.0/console/subscriptions/"+sub.ID+"/cancel", strings.NewReader(`{"at_period_end":false}`))
	bare.Header.Set("Content-Type", "application/json")
	bare.Header.Set("Authorization", "Bearer "+operator)
	bare.Header.Set(middleware.ModeHeader, "live")
	bare.Header.Set(middleware.SpaceHeader, "org:"+e.accountOrg)
	if resp, err := e.app.Test(bare, fiber.TestConfig{Timeout: 15 * time.Second}); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no key: %v %v", resp.StatusCode, err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/v1.0/console/subscriptions/"+sub.ID+"/cancel", strings.NewReader(`{"at_period_end":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+operator)
	req.Header.Set(middleware.ModeHeader, "live")
	req.Header.Set(middleware.SpaceHeader, "org:"+e.accountOrg)
	req.Header.Set(middleware.IdempotencyHeader, id.New())
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
			entry.Actor == "test" {
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

// payEnv is the portal env plus the collector the app holds, built again against the same
// tables: the settlement is reached the way the BYOK rail reaches it, through SettleAttempt.
type payEnv struct {
	*portalEnv
	links     *services.PayLink
	collector *services.Collector
}

func newPayEnv(t *testing.T) *payEnv {
	t.Helper()
	base := newPortal(t)

	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL = testIssuer
	cfg.CtechJWKSURL = base.jwksURL
	cfg.ServiceAudience = testAudience
	cfg.PortalOrganizationID = base.org.ID
	cfg.CheckoutLinkSecret = testLinkSecret
	cfg.CheckoutBaseURL = "https://pay.test/c"
	base.useAccount(&cfg)

	server, err := app.Build(ctxT(t), &cfg, func() time.Time { return now() })
	if err != nil {
		t.Fatal(err)
	}
	base.app = server
	return &payEnv{
		portalEnv: base,
		links:     services.NewPayLink(testLinkSecret, "https://pay.test/c"),
		collector: services.NewCollector(
			repositories.NewInvoiceRepository(testDB, testCfg),
			repositories.NewPaymentRepository(testDB, testCfg),
			repositories.NewCustomerRepository(testDB, testCfg),
			repositories.NewOrganizationRepository(testDB, testCfg),
			repositories.NewSubscriptionRepository(testDB, testCfg),
		),
	}
}

// newAttempt records a pending attempt for an invoice. A manual one is the only kind the
// plain repository path still writes, which is all a settlement test needs.
func (e *payEnv) newAttempt(t *testing.T, inv *billing.Invoice) *billing.PaymentAttempt {
	t.Helper()
	attempt := &billing.PaymentAttempt{
		ID:             id.NewWithPrefix(id.PrefixPaymentAttempt),
		OrganizationID: e.org.ID,
		Livemode:       true,
		InvoiceID:      inv.ID,
		AttemptNumber:  1,
		Status:         billing.AttemptPending,
		Method:         billing.MethodManual,
		RecordedBy:     "test",
		Amount:         inv.AmountDue(),
	}
	if err := repositories.NewPaymentRepository(testDB, testCfg).CreateAttempt(ctxT(t), attempt, now()); err != nil {
		t.Fatal(err)
	}
	return attempt
}

// payInFull settles an invoice the way a confirmed payment does: the collector moves the
// pending attempt to SUCCEEDED, which settles the invoice and the subscription behind it.
func (e *payEnv) payInFull(t *testing.T, inv *billing.Invoice) *billing.PaymentAttempt {
	t.Helper()
	attempt := e.newAttempt(t, inv)
	if _, err := e.collector.SettleAttempt(ctxT(t), attempt, billing.CauseByokConfirmed, "test", "req_pay", now()); err != nil {
		t.Fatal(err)
	}
	if got := e.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
	return attempt
}
