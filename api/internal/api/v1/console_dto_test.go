package v1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// The plan a subscription is on, as the console names it: the products its
// items bill, in item order, each once. A base price and a metered price of the
// same product are one plan, not "X + X".
func TestPlanNameJoinsDistinctProductNames(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"DF-e Ilimitado interno"}, "DF-e Ilimitado interno"},
		{[]string{"Plano Essencial", "Emissões adicionais"}, "Plano Essencial + Emissões adicionais"},
		{[]string{"Plano Essencial", "Plano Essencial"}, "Plano Essencial"},
		{[]string{"", "Finanças Free", ""}, "Finanças Free"},
	}
	for _, c := range cases {
		if got := planName(c.names); got != c.want {
			t.Errorf("planName(%q) = %q, want %q", c.names, got, c.want)
		}
	}
}

// A console row is the subscription the M2M API publishes, flat, plus the two
// labels an operator reads. The labels must not leak into the shared
// subscriptionResponse: ctech-dfe decodes that JSON.
func TestConsoleSubscriptionRowCarriesTheLabelsAndTheSharedFields(t *testing.T) {
	sub := &billing.Subscription{
		ID: "sub_1", CustomerID: "cus_1", Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
		Anchor:     brcal.New(2026, time.October, 10),
	}
	row := consoleSubscriptionRow{
		subscriptionResponse: newSubscriptionResponse(sub),
		CustomerName:         "A O CARVALHO TECH",
		PlanName:             "DF-e Ilimitado interno",
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "sub_1" || got["customer_id"] != "cus_1" {
		t.Fatalf("shared fields missing: %s", raw)
	}
	if got["customer_name"] != "A O CARVALHO TECH" || got["plan_name"] != "DF-e Ilimitado interno" {
		t.Fatalf("labels missing: %s", raw)
	}
	period, ok := got["current_period"].(map[string]any)
	if !ok || period["period_start"] == nil || period["period_end"] == nil {
		t.Fatalf("current_period must stay period_start/period_end: %s", raw)
	}

	shared, err := json.Marshal(newSubscriptionResponse(sub))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "customer_name") || strings.Contains(string(shared), "plan_name") {
		t.Fatalf("the M2M subscription JSON changed: %s", shared)
	}
}
