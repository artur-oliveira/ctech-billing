//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// catalogFixture is a product plus however many prices a test needs on it.
type catalogFixture struct {
	org      *billing.Organization
	product  *billing.Product
	catalog  *repositories.CatalogRepository
	subs     *repositories.SubscriptionRepository
	invoices *repositories.InvoiceRepository
	usage    *repositories.UsageRepository
	subber   *services.Subscriber
}

func newCatalog(t *testing.T, ownerKey string) *catalogFixture {
	t.Helper()
	ctx := ctxT(t)

	org := newOrg(t, true)
	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	usage := repositories.NewUsageRepository(testDB, testCfg)

	product := &billing.Product{
		ID: id.NewWithPrefix(id.PrefixProduct), OrganizationID: org.ID, Livemode: true,
		Name: "DF-e Sob Demanda", Active: true, OwnerKey: ownerKey,
	}
	if err := catalog.CreateProduct(ctx, product, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return &catalogFixture{
		org: org, product: product, catalog: catalog,
		subs: subs, invoices: invoices, usage: usage,
		subber: services.NewSubscriber(subs, catalog, services.NewInvoicer(subs, invoices, catalog, usage)),
	}
}

func (f *catalogFixture) price(
	t *testing.T,
	productID string,
	priceType billing.PriceType,
	timing billing.BillingTiming,
	amount billing.Cents,
	interval billing.Interval,
) *billing.Price {
	t.Helper()
	p := &billing.Price{
		ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: f.org.ID, Livemode: true,
		ProductID: productID, Type: priceType, Currency: billing.CurrencyBRL,
		UnitAmount: amount,
		Recurrence: billing.Recurrence{Interval: interval, Count: 1},
		Timing:     timing,
	}
	if err := f.catalog.CreatePrice(ctxT(t), p, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *catalogFixture) subscribe(ctx context.Context, prices ...*billing.Price) (*billing.Subscription, *billing.Invoice, error) {
	items := make([]services.SubscribeItem, len(prices))
	for i, p := range prices {
		items[i] = services.SubscribeItem{PriceID: p.ID, Quantity: 1}
	}
	return f.subber.Subscribe(ctx, services.SubscribeInput{
		OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(),
		Items:      items,
		Anchor:     brcal.New(2026, time.March, 1),
		Actor:      "test",
	}, now())
}

// TestFourMetersProduceOneInvoiceWithFourLines is the sob-demanda plan: NF-e,
// NFC-e, CT-e and MDF-e are metered separately and billed as one document. The
// old one-item-per-subscription shape silently billed only the first of them,
// which is the failure this whole change exists to remove.
func TestFourMetersProduceOneInvoiceWithFourLines(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	nfe := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)
	nfce := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 1, billing.IntervalMonth)
	cte := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 50, billing.IntervalMonth)
	mdfe := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 10, billing.IntervalMonth)

	sub, inv, err := f.subscribe(ctx, nfe, nfce, cte, mdfe)
	if err != nil {
		t.Fatal(err)
	}
	if inv != nil {
		t.Fatal("an arrears subscription owes nothing on the day it starts")
	}

	items, err := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("subscription has %d items, want 4", len(items))
	}

	// 100 NF-e, 1000 NFC-e, 2 CT-e, 3 MDF-e — each against its own item, which
	// is the whole point of the item being the usage sub-partition.
	usageFor := map[string]int64{nfe.ID: 100, nfce.ID: 1000, cte.ID: 2, mdfe.ID: 3}
	period := sub.CurrentPeriod()
	for _, it := range items {
		rec := &billing.UsageRecord{
			ID: id.NewWithPrefix(id.PrefixUsageRecord), OrganizationID: f.org.ID, Livemode: true,
			SubscriptionItemID: it.ID, Quantity: usageFor[it.PriceID],
			OccurredAt:     time.Date(2026, time.March, 15, 12, 0, 0, 0, time.UTC),
			IdempotencyKey: "u_" + it.ID,
		}
		if err := f.usage.Append(ctx, rec, period.Start, now()); err != nil {
			t.Fatal(err)
		}
	}

	invoicer := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	produced, err := invoicer.GenerateForPeriod(ctx, sub, items, period, "scheduler", now())
	if err != nil {
		t.Fatal(err)
	}

	lines, err := f.invoices.ListItems(ctx, f.org.ID, true, produced.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 {
		t.Fatalf("invoice has %d lines, want 4", len(lines))
	}
	// 100*5 + 1000*1 + 2*50 + 3*10 = 500 + 1000 + 100 + 30
	const want billing.Cents = 1630
	if produced.Total != want {
		t.Fatalf("invoice total is %s, want %s", produced.Total, want)
	}
}

// TestTheSamePeriodIsBilledOnceAcrossAllItems: the generation key moved from the
// item to the subscription. Keyed on the item, four items would each claim the
// period and produce four single-line invoices.
func TestTheSamePeriodIsBilledOnceAcrossAllItems(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")
	a := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)
	b := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 1, billing.IntervalMonth)

	sub, _, err := f.subscribe(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}

	invoicer := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	period := sub.CurrentPeriod()
	if _, err := invoicer.GenerateForPeriod(ctx, sub, items, period, "scheduler", now()); err != nil {
		t.Fatal(err)
	}
	_, err = invoicer.GenerateForPeriod(ctx, sub, items, period, "scheduler", now())
	if !errors.Is(err, repositories.ErrAlreadyGenerated) {
		t.Fatalf("the second call for the same period must be refused, got %v", err)
	}
}

// TestItemsMustAgreeOnTheCycle: a subscription has one Recurrence and one
// BillingTiming, so items that disagree would put two different periods on one
// document with nothing downstream able to tell which.
func TestItemsMustAgreeOnTheCycle(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	monthly := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	yearly := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 350000, billing.IntervalYear)
	arrears := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)

	if _, _, err := f.subscribe(ctx, monthly, yearly); err == nil {
		t.Fatal("a monthly and a yearly price must not share a subscription")
	} else if !strings.Contains(err.Error(), "one cycle") {
		t.Fatalf("want a cycle mismatch, got %v", err)
	}

	if _, _, err := f.subscribe(ctx, monthly, arrears); err == nil {
		t.Fatal("an advance and an arrears price must not share a subscription")
	} else if !strings.Contains(err.Error(), "one timing") {
		t.Fatalf("want a timing mismatch, got %v", err)
	}

	if _, _, err := f.subscribe(ctx, monthly, monthly); err == nil {
		t.Fatal("the same price twice must be refused")
	}
}

// TestItemsMustAgreeOnTheOwner guards the tenant-zero routing (ADR 0016): items
// owned by two services on one subscription would send each of them the other's
// invoices, which is the exact failure OwnerKey exists to prevent.
func TestItemsMustAgreeOnTheOwner(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	poker := &billing.Product{
		ID: id.NewWithPrefix(id.PrefixProduct), OrganizationID: f.org.ID, Livemode: true,
		Name: "Poker", Active: true, OwnerKey: "poker",
	}
	if err := f.catalog.CreateProduct(ctx, poker, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	dfePrice := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	pokerPrice := f.price(t, poker.ID, billing.PriceFixed, billing.BillAdvance, 1000, billing.IntervalMonth)

	_, _, err := f.subscribe(ctx, dfePrice, pokerPrice)
	if err == nil {
		t.Fatal("two services must not share one subscription")
	}
	if !strings.Contains(err.Error(), "one owner") {
		t.Fatalf("want an owner mismatch, got %v", err)
	}
}

// TestAFreePlanIsInvoicedAndSettledWithoutBeingChased is the Free tier: a real
// subscription and a real invoice with a real number, closed on issue and never
// entered into the dunning queue.
func TestAFreePlanIsInvoicedAndSettledWithoutBeingChased(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")
	free := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)

	_, inv, err := f.subscribe(ctx, free)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil {
		t.Fatal("a free plan is still invoiced: the period was served and the document says so")
	}
	if inv.Status != billing.InvoicePaid {
		t.Fatalf("a zero-total invoice is settled on issue, got %s", inv.Status)
	}
	if inv.Number == 0 {
		t.Fatal("a free invoice takes a number from the same gapless sequence")
	}

	// The real assertion: it is not in the dunning partition on any day the
	// policy would have visited. Reading the queue rather than the invoice is
	// what proves the schedule keys were never written — an invoice filtered out
	// after the read would still cost a morning's query and would still be one
	// bad transition away from a reminder about R$ 0,00.
	for _, step := range []int{-3, 1, 3, 7, 10, 30} {
		date, ok := billing.DunningDate(inv.DueDate, indexOfOffset(step))
		if !ok {
			continue
		}
		page, err := f.invoices.DueForDunning(ctx, true, date, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, queued := range page.Items {
			if queued.ID == inv.ID {
				t.Fatalf("the free invoice is queued for dunning on %s", date)
			}
		}
	}
}

// indexOfOffset maps a policy offset in days back to its step index, so the test
// above states the dates it cares about in the terms the policy is written in.
func indexOfOffset(offset int) int {
	for i, step := range billing.DunningPolicy {
		if step.Offset == offset {
			return i
		}
	}
	return -1
}

// TestNothingDueCannotCloseAnInvoiceThatOwesMoney is the guard that makes
// CauseNothingDue safe to have at all: without it, it is a way to mark any
// invoice PAID with no money behind it and no operator named.
func TestNothingDueCannotCloseAnInvoiceThatOwesMoney(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")
	paid := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)

	_, inv, err := f.subscribe(ctx, paid)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != billing.InvoiceOpen {
		t.Fatalf("a paid plan's invoice waits to be paid, got %s", inv.Status)
	}

	_, err = f.invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseNothingDue, "attacker", "", now())
	if !errors.Is(err, billing.ErrCauseNotAllowed) {
		t.Fatalf("want ErrCauseNotAllowed, got %v", err)
	}
}
