package provision

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

const minimal = `{
  "organization": {"id": "org_x", "display_name": "X"},
  "products": [{"id": "prod_a", "name": "A"}],
  "prices": [{
    "id": "price_a", "product_id": "prod_a", "type": "fixed",
    "unit_amount": 9900,
    "recurrence": {"interval": "month", "count": 1},
    "billing_timing": "advance"
  }]
}`

func TestParseAcceptsAMinimalPlan(t *testing.T) {
	p, err := Parse(strings.NewReader(minimal))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Organization.PayoutStatus != "" {
		t.Errorf("payout_status defaulted in the document; it must default at write time, not here")
	}
	// Currency is filled in on the way to the domain, not in the document: a plan
	// that has to spell "BRL" on every price is a plan with one more place to typo.
	if got := p.Prices[0].entity("org_x", true).Currency; got != "BRL" {
		t.Errorf("currency = %q, want BRL", got)
	}
	if !p.Products[0].entity("org_x", true).Active {
		t.Errorf("a product with no `active` field must arrive active")
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		// The whole reason DisallowUnknownFields is on: this is a typo, and a
		// lenient decoder makes it an organization with no owner instead.
		"unknown field": `{"organization": {"id": "o", "display_name": "O", "owner_user": "u"}}`,

		"no organization id": `{"organization": {"display_name": "O"}}`,

		"unknown payout status": `{"organization": {"id": "o", "display_name": "O", "payout_status": "yes"}}`,

		"price pointing outside the plan": `{
			"organization": {"id": "o", "display_name": "O"},
			"prices": [{"id": "p", "product_id": "missing", "type": "fixed", "unit_amount": 1,
			            "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance"}]}`,

		// Delegated to billing.Price.Validate. Asserted here because the plan is
		// the only place these are caught before an invoice tries to use them.
		"metered price billed in advance": `{
			"organization": {"id": "o", "display_name": "O"},
			"products": [{"id": "prod", "name": "P"}],
			"prices": [{"id": "p", "product_id": "prod", "type": "metered", "unit_amount": 1,
			            "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance"}]}`,

		"duplicate id": `{
			"organization": {"id": "o", "display_name": "O"},
			"products": [{"id": "prod", "name": "P"}, {"id": "prod", "name": "Q"}]}`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(doc)); err == nil {
				t.Fatalf("Parse accepted %s", name)
			}
		})
	}
}

// Tenant zero's plan is the file seed applies: it must parse, and it must link
// CTech's billing tenant to CTech's ctech-account organization ("A O CARVALHO
// TECH"), or its revenue never reaches Finanças (spec § 3.8).
func TestTenantZeroPlanLinksCTechsAccountOrganization(t *testing.T) {
	f, err := os.Open("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Organization.ID != "ctech" || p.Organization.AccountOrganizationID != "01a04ed6-1af9-745e-bcf7-d3b66fe52321" {
		t.Fatalf("organization = %+v", p.Organization)
	}
	creds := map[string]string{}
	for _, c := range p.Credentials {
		creds[c.ClientID] = c.OwnerKey
	}
	if creds["dfe-billing"] != "dfe" || creds["account-billing"] != "finance" {
		t.Fatalf("credentials = %v", creds)
	}
	var fin *Product
	for i := range p.Products {
		if p.Products[i].ID == "prod_finance" {
			fin = &p.Products[i]
		}
	}
	if fin == nil || fin.Name != "CTech Finanças" || fin.OwnerKey != "finance" || fin.DefaultPriceID != "price_finance_free" {
		t.Fatalf("prod_finance = %+v", fin)
	}
	prices := map[string]Price{}
	for _, pr := range p.Prices {
		prices[pr.ID] = pr
		// BYOK spec § 12.5 gives prod_billing's plans a user quota of their own.
		if _, has := pr.Metadata["quota_users"]; has && pr.ProductID != "prod_billing" {
			t.Errorf("price %s still carries quota_users (DF-e spec O4)", pr.ID)
		}
	}
	for _, archived := range []string{"price_dfe_ondemand_user", "price_dfe_ondemand_company"} {
		if !prices[archived].Archived {
			t.Errorf("%s must be archived", archived)
		}
	}
	comp := prices["price_dfe_ondemand_companies_monthly"]
	if comp.ProductID != "prod_dfe_ondemand" || comp.Type != billing.PriceMetered || comp.Aggregation != billing.AggregationMax ||
		comp.Meter != "dfe_companies" || comp.IncludedQuantity != 0 || comp.UnitAmount != 500 || comp.Timing != billing.BillArrears ||
		comp.Metadata["quota_companies"] != "-1" || comp.Metadata["plan"] != "ondemand" {
		t.Errorf("price_dfe_ondemand_companies_monthly = %+v", comp)
	}
	// The fixed DF-e plans keep their company quotas.
	if prices["price_dfe_pro_monthly"].Metadata["quota_companies"] != "10" || prices["price_dfe_free_monthly"].Metadata["quota_companies"] != "1" {
		t.Error("fixed DF-e company quotas changed")
	}
	var bill *Product
	for i := range p.Products {
		if p.Products[i].ID == "prod_billing" {
			bill = &p.Products[i]
		}
	}
	// No default price: a default would make every customer of tenant zero
	// "entitled" to billing, which is exactly what approval decides.
	if bill == nil || bill.OwnerKey != billing.MerchantOwnerKey || bill.DefaultPriceID != "" {
		t.Fatalf("prod_billing = %+v", bill)
	}
	ea := prices["price_billing_early_access"]
	if ea.ProductID != "prod_billing" || ea.Type != billing.PriceFixed || ea.UnitAmount != 0 ||
		ea.Timing != billing.BillAdvance || ea.Recurrence.Interval != billing.IntervalMonth || ea.Recurrence.Count != 1 ||
		ea.Metadata["plan"] != "early_access" || ea.Archived {
		t.Fatalf("price_billing_early_access = %+v", ea)
	}
	want := map[string]struct {
		amount   billing.Cents
		typ      billing.PriceType
		meter    string
		included int64
		plan     string
	}{
		"price_finance_free":            {0, billing.PriceFixed, "", 0, "free"},
		"price_finance_basic_monthly":   {1990, billing.PriceFixed, "", 0, "basic"},
		"price_finance_pro_monthly":     {4990, billing.PriceFixed, "", 0, "pro"},
		"price_finance_ondemand_spaces": {490, billing.PriceMetered, "finance_spaces", 1, "ondemand"},
		"price_finance_ondemand_people": {290, billing.PriceMetered, "finance_people", 1, "ondemand"},
	}
	for id, w := range want {
		g, ok := prices[id]
		if !ok || g.ProductID != "prod_finance" || g.UnitAmount != w.amount || g.Type != w.typ || g.Meter != w.meter ||
			g.IncludedQuantity != w.included || g.Metadata["plan"] != w.plan {
			t.Errorf("%s = %+v", id, g)
		}
		if w.typ == billing.PriceMetered && (g.Aggregation != billing.AggregationMax || g.Timing != billing.BillArrears) {
			t.Errorf("%s: aggregation %q timing %q", id, g.Aggregation, g.Timing)
		}
	}
	quotas := map[string][2]string{
		"price_finance_free": {"1", "1"}, "price_finance_basic_monthly": {"3", "5"},
		"price_finance_pro_monthly": {"10", "10"}, "price_finance_ondemand_spaces": {"-1", "-1"},
		"price_finance_ondemand_people": {"-1", "-1"},
	}
	for id, q := range quotas {
		md := prices[id].Metadata
		if md["quota_spaces"] != q[0] || md["quota_people_per_space"] != q[1] {
			t.Errorf("%s quotas = %v", id, md)
		}
	}
}

// The link names a finance space key (spec § 3.8), so a plan cannot write
// anything a space key would refuse: not billing's own tenant id, not a key.
func TestPlanRefusesAnAccountOrganizationThatIsNotAnID(t *testing.T) {
	for _, link := range []string{"ctech", "USER#x", "0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"} {
		_, err := Parse(strings.NewReader(`{"organization":{"id":"ctech","display_name":"CTech","account_organization_id":"` + link + `"}}`))
		if err == nil || !strings.Contains(err.Error(), "account_organization_id") {
			t.Errorf("link %q: err = %v", link, err)
		}
	}
	p, err := Parse(strings.NewReader(`{"organization":{"id":"ctech","display_name":"CTech","account_organization_id":"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}}`))
	if err != nil || p.Organization.AccountOrganizationID != "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}
func TestParseRejectsABadDefaultPrice(t *testing.T) {
	base := func(products, prices string) string {
		return `{"organization":{"id":"o","display_name":"O"},"products":[` + products + `],"prices":[` + prices + `]}`
	}
	free := `{"id":"free","product_id":"fin","type":"fixed","unit_amount":0,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	paid := `{"id":"paid","product_id":"fin","type":"fixed","unit_amount":1990,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	other := `{"id":"oth","product_id":"dfe","type":"fixed","unit_amount":0,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	cases := map[string]string{
		"not free":          base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"paid"}`, paid),
		"another product":   base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"oth"},{"id":"dfe","name":"D","owner_key":"dfe"}`, other),
		"not declared":      base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"ghost"}`, free),
		"two for one owner": base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"},{"id":"fin2","name":"G","owner_key":"finance","default_price_id":"free2"}`, free+`,`+strings.Replace(free, `"free","product_id":"fin"`, `"free2","product_id":"fin2"`, 1)),
		"archived default":  base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"}`, strings.Replace(free, `"billing_timing"`, `"archived":true,"billing_timing"`, 1)),
	}
	for name, doc := range cases {
		if _, err := Parse(strings.NewReader(doc)); err == nil || !strings.Contains(err.Error(), "default_price_id") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	ok := base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"}`, free+","+paid)
	if _, err := Parse(strings.NewReader(ok)); err != nil {
		t.Fatalf("a valid default: %v", err)
	}
}

func TestACredentialOwnerMustBeAnOwnerOfThePlan(t *testing.T) {
	doc := `{"organization":{"id":"o","display_name":"O"},"credentials":[{"client_id":"c","owner_key":"finnace"}],
	         "products":[{"id":"fin","name":"F","owner_key":"finance"}]}`
	if _, err := Parse(strings.NewReader(doc)); err == nil || !strings.Contains(err.Error(), "owner_key") {
		t.Fatalf("a typo in a credential's owner must be refused, err = %v", err)
	}
}

func TestPriceMeteringFieldsReachTheDomain(t *testing.T) {
	doc := `{"organization":{"id":"o","display_name":"O"},"products":[{"id":"fin","name":"F"}],
	  "prices":[{"id":"sp","product_id":"fin","type":"metered","unit_amount":490,"aggregation":"max","meter":"finance_spaces",
	    "included_quantity":1,"recurrence":{"interval":"month","count":1},"billing_timing":"arrears"}]}`
	p, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	e := p.Prices[0].entity("o", true)
	if e.Aggregation != billing.AggregationMax || e.Meter != "finance_spaces" || e.IncludedQuantity != 1 {
		t.Fatalf("entity = %+v", e)
	}
}

// BYOK spec § 12.6: the pilot's price is prod_billing's own, R$ 0, unlimited
// and internal (never in a public plan list).
func TestTheInternalBillingPriceIsInTheCatalogueAndNotPublic(t *testing.T) {
	f, err := os.Open("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	plan, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	var p *Price
	for i := range plan.Prices {
		if plan.Prices[i].ID == "price_billing_unlimited_internal_monthly" {
			p = &plan.Prices[i]
		}
	}
	if p == nil || p.ProductID != "prod_billing" || p.UnitAmount != 0 || p.Metadata["visibility"] != "internal" ||
		p.Metadata["quota_charges_month"] != "-1" || p.Metadata["overage"] != "bill" || p.Metadata["plan"] != "unlimited" {
		t.Fatalf("%+v", p)
	}
}

func loadCTechPlan(t *testing.T) *Plan {
	t.Helper()
	f, err := os.Open("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	plan, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// billingPlanPrices splits prod_billing's prices of one plan into its fixed fee
// and its metered overage.
func billingPlanPrices(plan *Plan, key string) (fixed, metered *Price) {
	for i := range plan.Prices {
		p := &plan.Prices[i]
		if p.ProductID != "prod_billing" || p.Metadata["plan"] != key {
			continue
		}
		if p.Type == billing.PriceMetered {
			metered = p
		} else {
			fixed = p
		}
	}
	return fixed, metered
}

// BYOK part C: the catalogue is spec § 12.5's table, priced by cobrança emitida.
func TestTheBillingPlansMatchTheSpecTable(t *testing.T) {
	plan := loadCTechPlan(t)
	type row struct {
		fee, included, overage int64
		refuse                 bool
	}
	want := map[string]row{"free": {0, 50, 0, true}, "basic": {9990, 500, 25, false}, "growth": {29990, 2000, 18, false}, "pro": {69990, 5000, 15, false}}
	for key, w := range want {
		fixed, metered := billingPlanPrices(plan, key)
		if fixed == nil || int64(fixed.UnitAmount) != w.fee || fixed.Metadata["quota_charges_month"] != strconv.FormatInt(w.included, 10) {
			t.Fatalf("%s fee: %+v", key, fixed)
		}
		if (fixed.Metadata["overage"] == "refuse") != w.refuse {
			t.Fatalf("%s overage", key)
		}
		if w.refuse {
			if metered != nil {
				t.Fatalf("%s has no overage price", key)
			}
			continue
		}
		// ADR 0018: one subscription has one timing, so a paid plan's fee is
		// billed in arrears with its overage, on one invoice.
		if fixed.Timing != billing.BillArrears {
			t.Fatalf("%s fee must be arrears like its overage", key)
		}
		if metered == nil || metered.Meter != "billing_charges" || metered.IncludedQuantity != w.included || int64(metered.UnitAmount) != w.overage || metered.Timing != billing.BillArrears {
			t.Fatalf("%s overage: %+v", key, metered)
		}
	}
	for _, p := range plan.Prices {
		if p.ProductID == "prod_billing" && (p.Metadata["plan"] == "ondemand" || p.Metadata["plan"] == "enterprise") {
			t.Fatalf("not in the catalogue: %s", p.ID)
		}
	}
	early, _ := billingPlanPrices(plan, "early_access")
	if early == nil || early.UnitAmount != 0 || early.Metadata["quota_charges_month"] != "-1" || early.Metadata["quota_bank_connections"] != "-1" {
		t.Fatalf("early access stays free and unlimited: %+v", early)
	}
}

// The DF-e catalogue is one product, prod_dfe, holding one plan per `plan`
// metadata. Each price carries exactly what its one-product-per-plan
// predecessor did (the DF-e enforces quotas from this metadata), the old public
// prices are archived, and the internal unlimited price that two live
// subscriptions bill is untouched.
func TestTheDFeCatalogueIsOneProductWithTheSameQuotasAndPrices(t *testing.T) {
	plan := loadCTechPlan(t)
	prices := map[string]Price{}
	for _, p := range plan.Prices {
		prices[p.ID] = p
	}
	successors := map[string]string{
		"price_dfe_free_monthly":               "price_dfe_v2_free",
		"price_dfe_pro_monthly":                "price_dfe_v2_pro",
		"price_dfe_unlimited_monthly":          "price_dfe_v2_unlimited",
		"price_dfe_ondemand_nfe":               "price_dfe_v2_ondemand_nfe",
		"price_dfe_ondemand_nfce":              "price_dfe_v2_ondemand_nfce",
		"price_dfe_ondemand_nfse":              "price_dfe_v2_ondemand_nfse",
		"price_dfe_ondemand_cte":               "price_dfe_v2_ondemand_cte",
		"price_dfe_ondemand_mdfe":              "price_dfe_v2_ondemand_mdfe",
		"price_dfe_ondemand_companies_monthly": "price_dfe_v2_ondemand_companies",
	}
	for oldID, newID := range successors {
		o, n := prices[oldID], prices[newID]
		if n.ID == "" || n.ProductID != "prod_dfe" || n.Name == "" {
			t.Fatalf("%s: %+v", newID, n)
		}
		if !o.Archived {
			t.Errorf("%s is still on sale", oldID)
		}
		if n.Archived || n.UnitAmount != o.UnitAmount || n.Type != o.Type || n.Timing != o.Timing ||
			n.Aggregation != o.Aggregation || n.Meter != o.Meter || n.IncludedQuantity != o.IncludedQuantity {
			t.Errorf("%s differs from %s: %+v vs %+v", newID, oldID, n, o)
		}
		for k, v := range o.Metadata {
			if n.Metadata[k] != v {
				t.Errorf("%s: metadata %q = %q, was %q", newID, k, n.Metadata[k], v)
			}
		}
		if n.Metadata["plan_name"] == "" {
			t.Errorf("%s has no plan_name", newID)
		}
	}
	internal := prices["price_dfe_unlimited_internal_monthly"]
	if internal.Archived || internal.ProductID != "prod_dfe_unlimited_internal" {
		t.Fatalf("the internal price changed: %+v", internal)
	}
	active := map[string]bool{}
	for _, p := range plan.Products {
		active[p.ID] = p.Active == nil || *p.Active
	}
	for _, id := range []string{"prod_dfe_free", "prod_dfe_pro", "prod_dfe_unlimited", "prod_dfe_ondemand"} {
		if active[id] {
			t.Errorf("%s should be retired", id)
		}
	}
	if !active["prod_dfe"] || !active["prod_dfe_unlimited_internal"] {
		t.Fatal("prod_dfe and the internal product stay active")
	}
}
