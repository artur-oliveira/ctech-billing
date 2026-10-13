//go:build integration

package integration

import (
	"encoding/json"
	"slices"
	"testing"
)

type consolePlanBody struct {
	Current struct {
		Plan           string `json:"plan"`
		SubscriptionID string `json:"subscription_id"`
	} `json:"current"`
	Usage struct {
		ChargesThisMonth     int64 `json:"charges_this_month"`
		Quota                int64 `json:"quota"`
		SubscriptionsActive  int64 `json:"subscriptions_active"`
		SubscriptionsQuota   int64 `json:"subscriptions_quota"`
		BankConnections      int64 `json:"bank_connections"`
		BankConnectionsQuota int64 `json:"bank_connections_quota"`
	} `json:"usage"`
	Plans []struct {
		Plan            string `json:"plan"`
		Fee             int64  `json:"fee"`
		Included        int64  `json:"included"`
		OverageUnit     int64  `json:"overage_unit"`
		Refuse          bool   `json:"refuse"`
		BankConnections int64  `json:"bank_connections"`
		Users           int64  `json:"users"`
		APIAccess       bool   `json:"api_access"`
	} `json:"plans"`
}

func (e *collectEnv) consolePlan(t *testing.T, mode string) consolePlanBody {
	t.Helper()
	r := e.byokWrite(t, "GET", "/v1.0/console/plan", e.sessionToken(t, collectScopes...), mode, "org:"+e.accountOrg, "")
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var b consolePlanBody
	if err := json.Unmarshal(r.body, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestThePlanScreenListsThePublicPlans(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	b := env.consolePlan(t, "live")
	var keys []string
	for _, p := range b.Plans {
		keys = append(keys, p.Plan)
	}
	if !slices.Equal(keys, []string{"free", "basic", "growth", "pro"}) {
		t.Fatalf("public plans %v", keys)
	}
	basic := b.Plans[1]
	if basic.Fee != 9990 || basic.Included != 500 || basic.OverageUnit != 25 || basic.Refuse || basic.BankConnections != 1 || basic.Users != 3 {
		t.Fatalf("%+v", basic)
	}
	if !b.Plans[0].Refuse || b.Plans[0].OverageUnit != 0 {
		t.Fatalf("free %+v", b.Plans[0])
	}
	if b.Current.Plan != "early_access" || b.Usage.Quota != -1 || b.Usage.BankConnections != 1 || b.Usage.BankConnectionsQuota != -1 {
		t.Fatalf("%+v", b)
	}
}

func TestChangingToGrowthSwapsBothPrices(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	r := env.consoleWrite(t, "POST", "/v1.0/console/plan", `{"plan":"growth"}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	b := env.consolePlan(t, "live")
	if b.Current.Plan != "growth" || b.Usage.Quota != 2000 || b.Usage.SubscriptionsQuota != 2000 || b.Usage.BankConnectionsQuota != 2 {
		t.Fatalf("%+v", b)
	}
	items, err := env.merchant.subs.ListItems(ctxT(t), env.tz, true, b.Current.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	var prices []string
	for _, it := range items {
		prices = append(prices, it.PriceID)
	}
	slices.Sort(prices)
	if !slices.Equal(prices, []string{"price_billing_growth_charges", "price_billing_growth_monthly"}) {
		t.Fatalf("%v", prices)
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/plan", `{"plan":"free"}`); r.status != 200 {
		t.Fatalf("a downgrade is allowed: %d %s", r.status, r.body)
	}
	if b := env.consolePlan(t, "live"); b.Current.Plan != "free" || b.Usage.Quota != 50 {
		t.Fatalf("%+v", b)
	}
}

func TestOnlyPublicPlansCanBeChosen(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	for _, plan := range []string{"unlimited", "early_access", "ondemand", ""} {
		if r := env.consoleWrite(t, "POST", "/v1.0/console/plan", `{"plan":"`+plan+`"}`); r.status != 422 {
			t.Fatalf("%q: %d %s", plan, r.status, r.body)
		}
	}
}

// Test mode shows the live plan and changes nothing.
func TestTestModeReadsTheLivePlan(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	if b := env.consolePlan(t, "test"); b.Current.Plan != "early_access" {
		t.Fatalf("%+v", b)
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/plan", `{"plan":"growth"}`); r.status != 404 || r.code() != "plan_live_only" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
