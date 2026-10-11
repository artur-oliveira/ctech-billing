//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// hookedSubs is the subscription repository the way app wires it.
func hookedSubs(tz string) *repositories.SubscriptionRepository {
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	return subs.WithEntitlementHook(repositories.NewTenantSuspension(tz,
		repositories.NewCustomerRepository(testDB, testCfg), repositories.NewOrganizationRepository(testDB, testCfg), subs))
}

type merchantFixture struct {
	tz, acct   string
	live, test *billing.Organization
	customer   *billing.Customer
	subs       *repositories.SubscriptionRepository
	subscriber *services.Subscriber
}

// newMerchantFixture: a seeded tenant zero, and an approved merchant (test and
// live tenants linked to acct) whose ORG_ customer lives in tenant zero.
func newMerchantFixture(t *testing.T) merchantFixture {
	t.Helper()
	tz, _ := seedTenantZero(t)
	acct := newSpaceOrgID()
	live := newLinkedOrg(t, true, acct)
	test := &billing.Organization{ID: live.ID, DisplayName: live.DisplayName, Livemode: false, AccountOrganizationID: acct}
	if err := orgRepo().Create(ctxT(t), test, now()); err != nil {
		t.Fatal(err)
	}
	return withOrgCustomer(t, tz, acct, live, test)
}

func withOrgCustomer(t *testing.T, tz, acct string, live, test *billing.Organization) merchantFixture {
	t.Helper()
	c := orgCustomer(tz, acct)
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), c, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	subs := hookedSubs(tz)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	inv := services.NewInvoicer(subs, repositories.NewInvoiceRepository(testDB, testCfg), cat, repositories.NewUsageRepository(testDB, testCfg))
	return merchantFixture{tz: tz, acct: acct, live: live, test: test, customer: c, subs: subs, subscriber: services.NewSubscriber(subs, cat, inv)}
}

func (f merchantFixture) entitle(t *testing.T) *billing.Subscription {
	t.Helper()
	sub, _, err := f.subscriber.Subscribe(ctxT(t), services.SubscribeInput{
		OrganizationID: f.tz, Livemode: true, CustomerID: f.customer.ID,
		Items: []services.SubscribeItem{{PriceID: "price_billing_early_access"}}, Actor: "user:rev",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionActive {
		t.Fatalf("a R$ 0 advance plan starts ACTIVE, got %s", sub.Status)
	}
	return sub
}

func (f merchantFixture) cancel(t *testing.T, sub *billing.Subscription) {
	t.Helper()
	if err := f.subscriber.Cancel(ctxT(t), sub, false, billing.CauseManual, "user:rev", "req_c", now()); err != nil {
		t.Fatal(err)
	}
}

func suspendedAt(t *testing.T, orgID string, live bool) string {
	t.Helper()
	o, err := orgRepo().Get(ctxT(t), orgID, live)
	if err != nil {
		t.Fatal(err)
	}
	return o.SuspendedAt
}

// Spec § 10.3.
func TestCancellingTheBillingSubscriptionSuspendsTheLiveTenantAndANewOneLiftsIt(t *testing.T) {
	f := newMerchantFixture(t)
	sub := f.entitle(t)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("suspended while entitled")
	}
	f.cancel(t, sub)
	if suspendedAt(t, f.live.ID, true) == "" {
		t.Fatal("cancelling prod_billing did not suspend the live tenant")
	}
	if suspendedAt(t, f.test.ID, false) != "" {
		t.Fatal("test mode was suspended")
	}
	trail, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctxT(t), f.live.ID, true, f.live.ID, 20)
	if err != nil || !hasAction(trail, billing.AuditOrganizationSuspended) {
		t.Fatalf("audit = %+v, %v", trail, err)
	}
	f.entitle(t)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("a new entitled subscription did not lift the suspension")
	}
}

func TestAnotherEntitledBillingSubscriptionKeepsTheTenantActive(t *testing.T) {
	f := newMerchantFixture(t)
	first := f.entitle(t)
	f.entitle(t)
	f.cancel(t, first)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("suspended although another prod_billing subscription is entitled")
	}
}

func TestOtherProductsNeverSuspend(t *testing.T) {
	f := newMerchantFixture(t)
	f.entitle(t)
	sub, _, err := f.subscriber.Subscribe(ctxT(t), services.SubscribeInput{
		OrganizationID: f.tz, Livemode: true, CustomerID: f.customer.ID,
		Items: []services.SubscribeItem{{PriceID: "price_finance_free"}}, Actor: "user:x",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	f.cancel(t, sub)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("cancelling a Finanças subscription suspended billing")
	}
}

// Review Focus 2.
func TestTenantZeroIsNeverSuspended(t *testing.T) {
	tz, ctechOrg := seedTenantZero(t)
	zero, err := orgRepo().Get(ctxT(t), tz, true)
	if err != nil {
		t.Fatal(err)
	}
	f := withOrgCustomer(t, tz, ctechOrg, zero, nil)
	f.cancel(t, f.entitle(t))
	if suspendedAt(t, tz, true) != "" {
		t.Fatal("tenant zero was suspended")
	}
}

// A merchant that was never approved has no live tenant: nothing to suspend,
// and the cancellation still commits.
func TestNoLiveTenantIsNothingToSuspend(t *testing.T) {
	tz, _ := seedTenantZero(t)
	f := withOrgCustomer(t, tz, newSpaceOrgID(), nil, nil)
	f.cancel(t, f.entitle(t))
}

func hasAction(trail []billing.AuditLog, action string) bool {
	for _, a := range trail {
		if a.Action == action {
			return true
		}
	}
	return false
}

// setSuspended marks a live tenant suspended directly, as the hook would.
func setSuspended(t *testing.T, org *billing.Organization) {
	t.Helper()
	setLookupAttr(t, org, "suspended_at", "2026-10-10T12:00:00Z")
}

func TestASuspendedTenantReadsAndTestModeKeepWorking(t *testing.T) {
	e := newAPI(t)
	setSuspended(t, e.org)
	live := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeProductsRead)

	r := e.do(t, http.MethodPost, "/v1.0/customers", live, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`)
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), `"tenant_suspended"`) {
		t.Fatalf("live write: %d %s", r.status, r.body)
	}
	if r := e.do(t, http.MethodGet, "/v1.0/products", live, "", ""); r.status != http.StatusOK {
		t.Fatalf("live read: %d %s", r.status, r.body)
	}

	// The same merchant's test credential keeps writing.
	test := &billing.Organization{ID: e.org.ID, DisplayName: e.org.DisplayName, Livemode: false, PayoutStatus: billing.PayoutEnabled}
	if err := orgRepo().Create(ctxT(t), test, now()); err != nil {
		t.Fatal(err)
	}
	testClient := "cli_" + id.New()
	if err := repositories.NewCredentialRepository(testDB, testCfg).Create(ctxT(t), &billing.APICredential{
		ClientID: testClient, OrganizationID: e.org.ID, Livemode: false, Description: "test", Active: true,
	}, now()); err != nil {
		t.Fatal(err)
	}
	tok := e.token(t, testClient, "", middleware.ScopeCustomersWrite)
	if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`); r.status >= 400 {
		t.Fatalf("test-mode write: %d %s", r.status, r.body)
	}
}
