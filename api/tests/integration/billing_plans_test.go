//go:build integration

package integration

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// billingPlanPrices are the catalogue's prices of each BYOK plan (part C Task 1).
var billingPlanPrices = map[string][]string{
	"free":         {"price_billing_free_monthly"},
	"basic":        {"price_billing_basic_monthly", "price_billing_basic_charges"},
	"growth":       {"price_billing_growth_monthly", "price_billing_growth_charges"},
	"pro":          {"price_billing_pro_monthly", "price_billing_pro_charges"},
	"early_access": {"price_billing_early_access"},
	"unlimited":    {"price_billing_unlimited_internal_monthly"},
}

// subscribeTo cancels the merchant's entitling prod_billing subscriptions and
// subscribes its ORG_ customer to the plan, active (a paid plan's first
// invoice counts as paid).
func (f merchantFixture) subscribeTo(t *testing.T, plan string) *billing.Subscription {
	t.Helper()
	list, err := f.subs.ListByCustomer(ctxT(t), f.tz, true, f.customer.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := range list {
		if list[i].IsEntitled() {
			f.cancel(t, &list[i])
		}
	}
	var items []services.SubscribeItem
	for _, p := range billingPlanPrices[plan] {
		items = append(items, services.SubscribeItem{PriceID: p})
	}
	sub, _, err := f.subscriber.Subscribe(ctxT(t), services.SubscribeInput{
		OrganizationID: f.tz, Livemode: true, CustomerID: f.customer.ID, Items: items, Actor: "user:rev",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionActive {
		if _, err := f.subs.Transition(ctxT(t), sub, billing.SubscriptionActive, billing.CauseInvoicePaid, "test", "", now()); err != nil {
			t.Fatal(err)
		}
	}
	return sub
}

func newBillingPlans(tz string) *services.BillingPlans {
	return services.NewBillingPlans(repositories.NewCustomerRepository(testDB, testCfg), hookedSubs(tz),
		repositories.NewCatalogRepository(testDB, testCfg), tz)
}

func TestBillingPlansReadTheLimitsOfTheMerchantsPlan(t *testing.T) {
	f := newMerchantFixture(t)
	plans := newBillingPlans(f.tz)
	want := map[string]services.PlanLimits{
		"free":         {Plan: "free", ChargesPerMonth: 50, RefuseOverage: true, BankConnections: 1, Users: 1},
		"basic":        {Plan: "basic", ChargesPerMonth: 500, BankConnections: 1, Users: 3},
		"growth":       {Plan: "growth", ChargesPerMonth: 2000, BankConnections: 2, Users: 10, APIAccess: true},
		"pro":          {Plan: "pro", ChargesPerMonth: 5000, BankConnections: 5, Users: 25, APIAccess: true},
		"early_access": {Plan: "early_access", ChargesPerMonth: -1, BankConnections: -1, Users: -1},
		"unlimited":    {Plan: "unlimited", ChargesPerMonth: -1, BankConnections: -1, Users: -1, APIAccess: true},
	}
	for _, plan := range []string{"free", "basic", "growth", "pro", "early_access", "unlimited"} {
		sub := f.subscribeTo(t, plan)
		got, ok, err := plans.For(ctxT(t), f.live)
		if err != nil || !ok {
			t.Fatalf("%s: entitled %v, %v", plan, ok, err)
		}
		w := want[plan]
		w.SubscriptionID = sub.ID
		if got != w {
			t.Fatalf("%s: %+v, want %+v", plan, got, w)
		}
	}
}

func TestWithoutAPlanAMerchantIsNotEntitled(t *testing.T) {
	f := newMerchantFixture(t)
	plans := newBillingPlans(f.tz)
	if _, ok, err := plans.For(ctxT(t), f.live); ok || err != nil {
		t.Fatalf("no subscription: entitled %v, %v", ok, err)
	}
	sub := f.subscribeTo(t, "basic")
	f.cancel(t, sub)
	if _, ok, err := plans.For(ctxT(t), f.live); ok || err != nil {
		t.Fatalf("cancelled: entitled %v, %v", ok, err)
	}
	stranger := newLinkedOrg(t, true, newSpaceOrgID())
	if _, ok, err := plans.For(ctxT(t), stranger); ok || err != nil {
		t.Fatalf("no ORG_ customer: entitled %v, %v", ok, err)
	}
}

// Tenant zero is entitled as it always was; without a
// subscription of its own it is unlimited.
func TestTenantZeroWithoutASubscriptionIsUnlimited(t *testing.T) {
	tz, _ := seedTenantZero(t)
	org, err := repositories.NewOrganizationRepository(testDB, testCfg).Get(ctxT(t), tz, true)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := newBillingPlans(tz).For(ctxT(t), org)
	if err != nil || !ok || got != services.Unlimited {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}
