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

func TestAdvanceToArrearsHappensAtTheBoundaryAndBillsNothingTwice(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth) // free, so it starts ACTIVE
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	anchor := brcal.New(2034, time.March, 7)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}},
		Cause: billing.CauseCustomer, Actor: "user:x"}, now()); err != nil {
		t.Fatal(err)
	}
	// Until the boundary nothing changes: same items, same timing.
	items, _ := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	if len(items) != 1 || items[0].PriceID != basic.ID || got.Timing != billing.BillAdvance ||
		got.PendingChange == nil || got.PendingChange.At != sub.CurrentPeriod().End {
		t.Fatalf("before the boundary: %+v %+v", items, got)
	}

	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	boundary := sub.CurrentPeriod().End
	for run := 0; run < 2; run++ { // re-runnable
		if res := inv.RunDailySweep(ctx, true, boundary, "scheduler", now()); res.Failed != 0 {
			t.Fatalf("sweep %d: %+v", run, res.Errors)
		}
	}
	got, _ = f.subs.Get(ctx, f.org.ID, true, sub.ID)
	items, _ = f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if got.Timing != billing.BillArrears || got.PendingChange != nil || got.PeriodIndex != 1 || len(items) != 1 || items[0].PriceID != spaces.ID {
		t.Fatalf("after the boundary: %+v %+v", got, items)
	}
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if len(invoices) != 1 { // only the first, prepaid period's; the new period is billed when it ends
		t.Fatalf("%d invoices, want 1", len(invoices))
	}
}

func TestAnImmediateChangeClearsAScheduledOne(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	sub, _, _ := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: brcal.New(2034, time.May, 11), Actor: "t"}, now())
	_ = f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now())
	if _, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: pro.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PendingChange != nil {
		t.Fatalf("pending = %+v", got.PendingChange)
	}
}

func TestACancellationAtPeriodEndWinsOverAScheduledChange(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	sub, _, _ := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: brcal.New(2034, time.June, 13), Actor: "t"}, now())
	_ = f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now())
	_ = f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "u", "r", now())
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	_ = inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now())
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.Status != billing.SubscriptionCanceled {
		t.Fatalf("status = %s", got.Status)
	}
}
