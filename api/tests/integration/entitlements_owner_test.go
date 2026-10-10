//go:build integration

package integration

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ownersEnv is newAPI plus a Finanças product (with its default), a DF-e
// product, and one credential scoped to each owner.
type ownersEnv struct {
	*apiEnv
	finClient, dfeClient          string
	free, basic, spaces, dfePrice string
}

func newOwnersEnv(t *testing.T) ownersEnv {
	t.Helper()
	e, ctx := newAPI(t), ctxT(t)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	mk := func(p *billing.Product) {
		p.OrganizationID, p.Livemode, p.Active = e.org.ID, true, true
		if err := cat.CreateProduct(ctx, p, "test", "r", now()); err != nil {
			t.Fatal(err)
		}
	}
	price := func(product string, typ billing.PriceType, amount billing.Cents, md billing.Metadata, set func(*billing.Price)) string {
		p := &billing.Price{ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: e.org.ID, Livemode: true, ProductID: product,
			Type: typ, Currency: billing.CurrencyBRL, UnitAmount: amount,
			Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1}, Timing: billing.BillAdvance, Metadata: md}
		if set != nil {
			set(p)
		}
		if err := cat.CreatePrice(ctx, p, "test", "r", now()); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	o := ownersEnv{apiEnv: e, free: id.NewWithPrefix(id.PrefixPrice)}
	fin := "prod_fin_" + id.New()
	mk(&billing.Product{ID: fin, Name: "CTech Finanças", OwnerKey: "finance", DefaultPriceID: o.free})
	price(fin, billing.PriceFixed, 0, billing.Metadata{"plan": "free", "quota_spaces": "1", "quota_people_per_space": "1"},
		func(p *billing.Price) { p.ID = o.free })
	o.basic = price(fin, billing.PriceFixed, 1990, billing.Metadata{"plan": "basic", "quota_spaces": "3"}, nil)
	o.spaces = price(fin, billing.PriceMetered, 490, billing.Metadata{"plan": "ondemand"}, func(p *billing.Price) {
		p.Timing, p.Aggregation, p.Meter, p.IncludedQuantity = billing.BillArrears, billing.AggregationMax, "finance_spaces", 1
	})
	dfe := "prod_dfe_" + id.New()
	mk(&billing.Product{ID: dfe, Name: "DF-e Pro", OwnerKey: "dfe"})
	o.dfePrice = price(dfe, billing.PriceFixed, 0, billing.Metadata{"plan": "pro"}, nil)
	price(dfe, billing.PriceMetered, 500, billing.Metadata{"plan": "ondemand"}, func(p *billing.Price) {
		p.Timing, p.Aggregation, p.Meter = billing.BillArrears, billing.AggregationMax, "dfe_companies"
	})

	creds := repositories.NewCredentialRepository(testDB, testCfg)
	for owner, client := range map[string]*string{"finance": &o.finClient, "dfe": &o.dfeClient} {
		*client = "cli_" + owner + "_" + id.New()
		if err := creds.Create(ctx, &billing.APICredential{ClientID: *client, OrganizationID: e.org.ID, Livemode: true, Active: true, OwnerKey: owner}, now()); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func levelBody(ref, meter, key, at string, v int) string {
	return `{"customer_ref":"` + ref + `","meter":"` + meter + `","value":` + itoa(v) + `,"occurred_at":"` + at + `","idempotency_key":"` + key + `"}`
}


// Spec test 4.
func TestLevelReportsAreIdempotentByBodyKey(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	body := levelBody("USER_a", "finance_spaces", "lvl:a:1", "2026-03-10T11:00:00Z", 4)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", body); r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.body)
	}
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", body); r.status != http.StatusOK || !strings.Contains(string(r.body), `"duplicate":true`) {
		t.Fatalf("retry: %d %s", r.status, r.body)
	}
	other := levelBody("USER_a", "finance_spaces", "lvl:a:1", "2026-03-10T11:00:00Z", 5)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", other); r.status != http.StatusConflict || !strings.Contains(string(r.body), "idempotency_key_reused") {
		t.Fatalf("reuse: %d %s", r.status, r.body)
	}
}

func TestALevelForAnotherOwnersMeterIsRefused(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.dfeClient, "", middleware.ScopeUsageWrite)
	r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("USER_a", "finance_spaces", "k", "2026-03-10T11:00:00Z", 1))
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "meter_not_allowed") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fin := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", fin, "", levelBody("USER_a", "nobody_meters", "k2", "2026-03-10T11:00:00Z", 1)); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown meter: %d %s", r.status, r.body)
	}
}

// User decision 2026-10-10: dfe-billing reports its own meter's levels.
func TestDfeReportsItsCompaniesLevel(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.dfeClient, "", middleware.ScopeUsageWrite)
	r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("ORG_x", "dfe_companies", "c1", "2026-03-10T11:00:00Z", 3))
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fin := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", fin, "", levelBody("ORG_x", "dfe_companies", "c2", "2026-03-10T11:00:00Z", 3)); r.status != http.StatusForbidden {
		t.Fatalf("finance reporting dfe_companies: %d", r.status)
	}
}

func TestALevelNeedsNoCustomerAndCreatesNone(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("USER_nobody", "finance_spaces", "k", "2026-03-10T11:00:00Z", 2)); r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByExternalRef(ctxT(t), o.org.ID, true, "USER_nobody"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a report created a customer: %v", err)
	}
}
