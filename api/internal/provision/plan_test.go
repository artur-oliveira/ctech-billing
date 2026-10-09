package provision

import (
	"os"
	"strings"
	"testing"
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
