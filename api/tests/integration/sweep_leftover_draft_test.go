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

// A run that created the period's DRAFT and then failed to finalize it (counter
// contention, throttling, a crash) leaves the generation marker behind. The next
// run used to see "already generated", skip straight to the renewal, and the
// period was never billed: no OPEN invoice, no number, no dunning, no charge.
func TestARerunIssuesTheDraftAPreviousRunLeftBehind(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 5000, billing.IntervalMonth)
	anchor := brcal.New(2034, time.February, 10)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}

	// Only an ACTIVE subscription is in the sweep.
	activateSubscription(t, f.org, sub.ID)
	if sub, err = f.subs.Get(ctx, f.org.ID, true, sub.ID); err != nil {
		t.Fatal(err)
	}

	// What the dead run wrote: the DRAFT and its marker for the next period, nothing else.
	period := billing.PeriodToInvoice(sub)
	items, err := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("items: %v %v", len(items), err)
	}
	line := billing.InvoiceItem{Description: "plan", PriceID: items[0].PriceID, Period: period, Quantity: 1, UnitAmount: 5000, Amount: 5000}
	draft := &billing.Invoice{
		ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: f.org.ID, Livemode: true,
		CustomerID: sub.CustomerID, SubscriptionID: sub.ID, Status: billing.InvoiceDraft,
		Period: period, Currency: billing.CurrencyBRL,
	}
	if err := billing.ApplyToInvoice(draft, []billing.InvoiceItem{line}, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.invoices.Create(ctx, draft, []billing.InvoiceItem{line}, billing.GenerationKey(sub.ID, period.Start), now()); err != nil {
		t.Fatal(err)
	}

	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	if res := inv.RunDailySweep(ctx, true, sub.NextPeriod().Start, "scheduler", now()); res.Examined == 0 {
		t.Fatalf("the subscription was not in the sweep: %+v", res)
	}

	got, err := f.invoices.Get(ctx, f.org.ID, true, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != billing.InvoiceOpen {
		t.Fatalf("the leftover invoice is %s, want OPEN: the period was renewed past it unbilled", got.Status)
	}
}
