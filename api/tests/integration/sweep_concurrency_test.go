//go:build integration

package integration

import (
	"slices"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// One tenant with many subscriptions due the same day, swept by a pool of workers:
// every one is invoiced exactly once and the tenant's invoice numbers stay unique
// and gapless, which is what the per-tenant cap protects (they share one counter).
func TestAConcurrentSweepBillsEveryoneOnceWithGaplessNumbers(t *testing.T) {
	const n = 40
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 5000, billing.IntervalMonth)
	anchor := brcal.New(2034, time.May, 12)

	var subsIDs []string
	var due brcal.Date
	for i := 0; i < n; i++ {
		sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
			CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
		if err != nil {
			t.Fatal(err)
		}
		activateSubscription(t, f.org, sub.ID)
		subsIDs = append(subsIDs, sub.ID)
		fresh, err := f.subs.Get(ctx, f.org.ID, true, sub.ID)
		if err != nil {
			t.Fatal(err)
		}
		due = fresh.NextPeriod().Start
	}

	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage).WithConcurrency(8, 4)
	res := inv.RunDailySweep(ctx, true, due, "scheduler", now())
	if res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("sweep failed: %+v", res.Errors)
	}
	if res.Invoiced < n {
		t.Fatalf("invoiced %d of at least %d: %+v", res.Invoiced, n, res)
	}

	var numbers []int64
	for _, sid := range subsIDs {
		invoices, err := f.invoices.ListBySubscription(ctx, f.org.ID, true, sid, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(invoices) != 2 { // the first period's, from Subscribe, and this renewal's
			t.Fatalf("subscription %s has %d invoices, want 2", sid, len(invoices))
		}
		for _, in := range invoices {
			numbers = append(numbers, in.Number)
		}
	}
	slices.Sort(numbers)
	for i, got := range numbers {
		if want := int64(i + 1); got != want {
			t.Fatalf("invoice numbers are not 1..%d without gaps or repeats: position %d is %d", len(numbers), i, got)
		}
	}
}
