//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// A tenant whose owner confirmed closing it answers 403 tenant_closing to every
// write, reads keep working, and a cancel lifts it.
func TestAClosingTenantRefusesWritesOnTheM2MApiAndReadsStillWork(t *testing.T) {
	e := newAPIWith(t, func(cfg *config.Config) { cfg.TenantClosureEnabled = true })
	repo := repositories.NewClosureRepository(testDB, testCfg)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeProductsRead)
	write := func() apiResponse {
		return e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`)
	}
	if r := write(); r.status >= 400 {
		t.Fatalf("before the closure: %d %s", r.status, r.body)
	}

	if err := repo.SetReadOnly(ctxT(t), []string{e.org.ID}, "clo_gate", true, now()); err != nil {
		t.Fatal(err)
	}
	if r := write(); r.status != http.StatusForbidden || !strings.Contains(string(r.body), `"tenant_closing"`) {
		t.Fatalf("during the closure: %d %s", r.status, r.body)
	}
	if r := e.do(t, http.MethodGet, "/v1.0/products", tok, "", ""); r.status != http.StatusOK {
		t.Fatalf("a read during the closure: %d %s", r.status, r.body)
	}

	if err := repo.SetReadOnly(ctxT(t), []string{e.org.ID}, "clo_gate", false, now()); err != nil {
		t.Fatal(err)
	}
	if r := write(); r.status >= 400 {
		t.Fatalf("after the cancel: %d %s", r.status, r.body)
	}
}

// With the feature off the gate is not wired at all: a flag row means nothing.
func TestTheClosingGateIsOffWithTheFeatureOff(t *testing.T) {
	e := newAPI(t)
	if err := repositories.NewClosureRepository(testDB, testCfg).SetReadOnly(ctxT(t), []string{e.org.ID}, "clo_off", true, now()); err != nil {
		t.Fatal(err)
	}
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`); r.status >= 400 {
		t.Fatalf("feature off: %d %s", r.status, r.body)
	}
}

// Review: a tenant in its read-only grace issues no new invoices; a cancel
// lets the next run bill the day it missed.
func TestTheSweepSkipsAClosingTenantAndBillsItAfterTheCancel(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	anchor := brcal.New(2034, time.March, 7)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	repo := repositories.NewClosureRepository(testDB, testCfg)
	if err := repo.SetReadOnly(ctx, []string{f.org.ID}, "clo_sweep", true, now()); err != nil {
		t.Fatal(err)
	}
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage).WithOrganizations(orgRepo()).WithTenantGuard(repo.ReadOnly)
	day := sub.CurrentPeriod().End
	if res := inv.RunDailySweep(ctx, true, day, "scheduler", now()); res.Suspended < 1 || res.Failed != 0 {
		t.Fatalf("sweep while closing = %+v", res)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PeriodIndex != sub.PeriodIndex {
		t.Fatal("a closing tenant's subscription was renewed")
	}
	if err := repo.SetReadOnly(ctx, []string{f.org.ID}, "clo_sweep", false, now()); err != nil {
		t.Fatal(err)
	}
	if res := inv.RunDailySweep(ctx, true, day, "scheduler", now()); res.Failed != 0 {
		t.Fatalf("re-run after the cancel = %+v", res)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PeriodIndex != sub.PeriodIndex+1 {
		t.Fatal("re-running after the cancel did not renew")
	}
}
