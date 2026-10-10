//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestAnAdvanceSubscriptionCancelledAtPeriodEndIsNotBilledAgain(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	anchor := brcal.New(2033, time.July, 19)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "user:x", "r", now()); err != nil {
		t.Fatal(err)
	}
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	res := inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now())
	if res.Failed != 0 {
		t.Fatalf("sweep: %+v", res.Errors)
	}
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	if got.Status != billing.SubscriptionCanceled {
		t.Fatalf("status = %s", got.Status)
	}
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if len(invoices) != 1 { // the first period's, from Subscribe
		t.Fatalf("%d invoices, want 1", len(invoices))
	}
}

func TestAnArrearsSubscriptionCancelledAtPeriodEndBillsTheEndedPeriod(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)
	anchor := brcal.New(2033, time.September, 23)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	_ = f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "user:x", "r", now())
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	if res := inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now()); res.Failed != 0 {
		t.Fatalf("sweep: %+v", res.Errors)
	}
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if got.Status != billing.SubscriptionCanceled || len(invoices) != 1 {
		t.Fatalf("status %s, %d invoices (want CANCELED and the ended period's)", got.Status, len(invoices))
	}
}
