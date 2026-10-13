//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/alerts"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func (e *collectEnv) usageService() *services.ByokUsage {
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	subs := hookedSubs(e.tz)
	return services.NewByokUsage(e.repo, e.payments, e.orgs,
		services.NewBillingPlans(repositories.NewCustomerRepository(testDB, testCfg), subs, cat, e.tz),
		subs, cat, repositories.NewUsageRepository(testDB, testCfg), e.tz)
}

// runUsage reports through the real service. The day partitions are shared
// by every test in the table, so tests assert on what is reported against
// their own tenant zero, never on how many rows were skipped.
func (e *collectEnv) runUsage(t *testing.T, at time.Time) (reported, skipped int) {
	t.Helper()
	reported, skipped, errs := e.usageService().Report(ctxT(t), at)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return reported, skipped
}

// usageRecords are the merchant's billing_charges records in its plan's
// current period.
func (e *collectEnv) usageRecords(t *testing.T, sub *billing.Subscription) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	if sub == nil {
		return out
	}
	items, err := e.merchant.subs.ListItems(ctxT(t), e.tz, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	usage := repositories.NewUsageRepository(testDB, testCfg)
	for _, it := range items {
		recs, err := usage.ListForPeriod(ctxT(t), e.tz, true, it.ID, sub.CurrentPeriod().Start)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			out[r.IdempotencyKey] += r.Quantity
		}
	}
	return out
}

// Review Focus 2: cancelled at 23 h never counts, at 25 h counts once,
// whatever the number of runs.
func TestUsageReportsSurvivorsOnce(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	sub := env.subscribeMerchantTo(t, "basic")
	early, late, keep := env.finalizeInvoice(t, 100), env.finalizeInvoice(t, 100), env.finalizeInvoice(t, 100)
	env.voidAt(t, early, now().Add(23*time.Hour))
	env.voidAt(t, late, now().Add(25*time.Hour))
	if r, _ := env.runUsage(t, now().Add(20*time.Hour)); r != 0 {
		t.Fatalf("nothing is 24 hours old yet: %d", r)
	}
	for range 3 {
		env.runUsage(t, now().Add(30*time.Hour))
	}
	recs := env.usageRecords(t, sub)
	if len(recs) != 2 || recs[env.attempt(t, late).ID] != 1 || recs[env.attempt(t, keep).ID] != 1 {
		t.Fatalf("%+v", recs)
	}
	if r, _ := env.runUsage(t, now().Add(31*time.Hour)); r != 0 {
		t.Fatalf("a reported charge leaves the partition: %d", r)
	}
}

// Grátis and early access have no metered price: nothing is billable.
func TestAPlanWithoutOverageReportsNothing(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	sub := env.subscribeMerchantTo(t, "free")
	env.finalizeInvoice(t, 100)
	if r, s := env.runUsage(t, now().Add(30*time.Hour)); r != 0 || s < 1 {
		t.Fatalf("%d %d", r, s)
	}
	if n := len(env.usageRecords(t, sub)); n != 0 {
		t.Fatalf("%d", n)
	}
}

// Review Focus 3: Básico past 500 bills (n − 500) × R$ 0,25 with its fee.
func TestOverageIsBilledFromUsage(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	sub := env.subscribeMerchantTo(t, "basic")
	items, err := env.merchant.subs.ListItems(ctxT(t), env.tz, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	usage := repositories.NewUsageRepository(testDB, testCfg)
	for _, it := range items {
		if it.PriceID != "price_billing_basic_charges" {
			continue
		}
		if err := usage.Append(ctxT(t), &billing.UsageRecord{ID: "usg_overage", OrganizationID: env.tz, Livemode: true,
			SubscriptionItemID: it.ID, Quantity: 520, OccurredAt: now(), IdempotencyKey: "k-520"}, sub.CurrentPeriod().Start, now()); err != nil {
			t.Fatal(err)
		}
	}
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	invoicer := services.NewInvoicer(env.merchant.subs, invoices, cat, usage)
	inv, err := invoicer.GenerateForPeriod(ctxT(t), sub, items, sub.CurrentPeriod(), "test", now())
	if err != nil {
		t.Fatal(err)
	}
	lines, err := invoices.ListItems(ctxT(t), env.tz, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]billing.Cents{}
	for _, l := range lines {
		got[l.PriceID] = l.Amount
	}
	if got["price_billing_basic_charges"] != 20*25 || got["price_billing_basic_monthly"] != 9990 || inv.Total != 9990+500 {
		t.Fatalf("lines %+v total %d", got, inv.Total)
	}
}

// Review Focus 5: test-mode charges are never reported.
func TestTestModeChargesAreNeverReported(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	sub := env.subscribeMerchantTo(t, "basic")
	env.finalizeInvoice(t, 100)
	if r, _ := env.runUsage(t, now().Add(30*time.Hour)); r != 0 {
		t.Fatalf("%d", r)
	}
	if n := len(env.usageRecords(t, sub)); n != 0 {
		t.Fatalf("%d", n)
	}
}

// cmd/byok runs the usage step once per live run, after the connections.
func TestTheHourlyLiveRunReportsUsage(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	sub := env.subscribeMerchantTo(t, "basic")
	inv := env.finalizeInvoice(t, 100)
	spaceFor := func(c *byokconn.Connection) (space.ResolvedSpace, error) {
		return space.ForJob(c.AccountOrganizationID, c.Livemode)
	}
	job := services.NewByokJob(env.repo, env.conns, env.statement, nil, alerts.Nop{}, spaceFor)
	env.sweep().WithUsage(env.usageService()).Register(job)
	job.Run(ctxT(t), true, now().Add(30*time.Hour))
	if recs := env.usageRecords(t, sub); recs[env.attempt(t, inv).ID] != 1 {
		t.Fatalf("%+v", recs)
	}
}
