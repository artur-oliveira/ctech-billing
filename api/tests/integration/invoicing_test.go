//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type fixture struct {
	org      *billing.Organization
	sub      *billing.Subscription
	item     billing.SubscriptionItem
	price    *billing.Price
	invoicer *services.Invoicer
	invoices *repositories.InvoiceRepository
	subs     *repositories.SubscriptionRepository
}

// newSubscribed provisions the whole chain a sweep needs: organization,
// product, price, subscription and its item.
func newSubscribed(t *testing.T, priceType billing.PriceType, timing billing.BillingTiming, amount billing.Cents, anchor brcal.Date) *fixture {
	t.Helper()
	ctx := ctxT(t)

	org := newOrg(t, true)
	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	usage := repositories.NewUsageRepository(testDB, testCfg)

	product := &billing.Product{
		ID: id.NewWithPrefix(id.PrefixProduct), OrganizationID: org.ID, Livemode: true,
		Name: "DF-e Basic", Active: true,
	}
	if err := catalog.CreateProduct(ctx, product, now()); err != nil {
		t.Fatal(err)
	}
	price := &billing.Price{
		ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: org.ID, Livemode: true,
		ProductID: product.ID, Type: priceType, Currency: billing.CurrencyBRL,
		UnitAmount: amount,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     timing,
	}
	if err := catalog.CreatePrice(ctx, price, now()); err != nil {
		t.Fatal(err)
	}

	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: true,
		CustomerID: "cus_1", Status: billing.SubscriptionActive,
		Recurrence: price.Recurrence, Timing: timing, Anchor: anchor,
		Metadata: billing.Metadata{"nfe_ref": "12345"},
	}
	item := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: true,
		SubscriptionID: sub.ID, PriceID: price.ID, Quantity: 1,
	}
	if err := subs.Create(ctx, sub, []billing.SubscriptionItem{item}, now()); err != nil {
		t.Fatal(err)
	}

	return &fixture{
		org: org, sub: sub, item: item, price: price,
		invoicer: services.NewInvoicer(subs, invoices, catalog, usage),
		invoices: invoices, subs: subs,
	}
}

// TestSweepGeneratesTheInvoiceOnTheRightDateWithTheRightDueDate is the MVP's
// core demo minus the money leg: subscribe, sweep on the boundary, and get one
// invoice for the right period with a due date that respects the Brazilian
// calendar.
func TestSweepGeneratesTheInvoiceOnTheRightDateWithTheRightDueDate(t *testing.T) {
	ctx := ctxT(t)
	// Anchored 1 March 2026. Period 1 starts 1 April, a Wednesday.
	f := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 4990, brcal.New(2026, time.March, 1))

	sweepDate := brcal.New(2026, time.April, 1)
	// Assertions are per tenant, never on the sweep's global counts: the sweep is
	// cross-tenant by design, so another test's subscription may legitimately be
	// billed in the same run.
	res := f.invoicer.RunDailySweep(ctx, true, sweepDate, "scheduler", now())
	if res.Failed != 0 {
		t.Fatalf("sweep result = %+v", res)
	}

	page, err := f.invoices.ListByMonth(ctx, f.org.ID, true, 2026, 4, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one April invoice, got %d", len(page.Items))
	}
	inv := page.Items[0]

	if inv.Status != billing.InvoiceOpen {
		t.Fatalf("status = %s, want OPEN", inv.Status)
	}
	if inv.Number != 1 {
		t.Fatalf("number = %d, want 1", inv.Number)
	}
	if inv.Total != 4990 {
		t.Fatalf("total = %s", inv.Total)
	}
	if inv.Period.Start != brcal.New(2026, time.April, 1) || inv.Period.End != brcal.New(2026, time.May, 1) {
		t.Fatalf("period = %s..%s, want April", inv.Period.Start, inv.Period.End)
	}
	if inv.DueDate != brcal.New(2026, time.April, 1) {
		t.Fatalf("due date = %s, want 2026-04-01 (a Wednesday, no roll)", inv.DueDate)
	}
	// Subscription metadata is copied onto the invoice, not shared with it.
	if inv.Metadata["nfe_ref"] != "12345" {
		t.Fatalf("metadata was not copied onto the invoice: %v", inv.Metadata)
	}

	lines, err := f.invoices.ListItems(ctx, f.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Description != "DF-e Basic" || lines[0].Amount != 4990 {
		t.Fatalf("lines = %+v", lines)
	}

	// The subscription moved to the next period, so tomorrow's sweep will not
	// find it again.
	fresh, err := f.subs.Get(ctx, f.org.ID, true, f.sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.PeriodIndex != 1 {
		t.Fatalf("period index = %d, want 1", fresh.PeriodIndex)
	}
}

// TestDueDateRollsOffAHoliday is the calendar rule reaching all the way through
// to a stored invoice, not just a unit test of the date function.
func TestDueDateRollsOffAHoliday(t *testing.T) {
	ctx := ctxT(t)
	// Anchored 1 April 2026, so period 1 starts 1 May — Dia do Trabalho, a
	// Friday. The due date must roll to Monday 4 May.
	f := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 1000, brcal.New(2026, time.April, 1))

	res := f.invoicer.RunDailySweep(ctx, true, brcal.New(2026, time.May, 1), "scheduler", now())
	if res.Failed != 0 {
		t.Fatalf("sweep result = %+v", res)
	}

	page, err := f.invoices.ListByMonth(ctx, f.org.ID, true, 2026, 5, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	inv := page.Items[0]

	if inv.DueDate != brcal.New(2026, time.May, 4) {
		t.Fatalf("due date = %s (%s), want 2026-05-04 — 1 May is Dia do Trabalho", inv.DueDate, inv.DueDate.Weekday())
	}
	// The accrual period must not have moved with the due date.
	if inv.Period.Start != brcal.New(2026, time.May, 1) {
		t.Fatalf("rolling the due date moved the accrual period to %s", inv.Period.Start)
	}
}

// TestSweepIsIdempotent: running the daily job three times must produce one
// invoice. PLAN.md § 12.6 asks for exactly this test.
func TestSweepIsIdempotent(t *testing.T) {
	ctx := ctxT(t)
	f := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 4990, brcal.New(2026, time.March, 1))
	sweepDate := brcal.New(2026, time.April, 1)

	if first := f.invoicer.RunDailySweep(ctx, true, sweepDate, "scheduler", now()); first.Failed != 0 {
		t.Fatalf("first run = %+v", first)
	}
	for i := range 2 {
		if res := f.invoicer.RunDailySweep(ctx, true, sweepDate, "scheduler", now()); res.Failed != 0 {
			t.Fatalf("re-run %d failed: %+v", i+2, res)
		}
	}

	page, err := f.invoices.ListByMonth(ctx, f.org.ID, true, 2026, 4, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("three sweeps produced %d invoices", len(page.Items))
	}
}

// TestGenerateForPeriodRefusesToBillTheSamePeriodTwice attacks the generation
// key directly, without relying on the subscription having moved on — the case
// that matters when a sweep is retried after a timeout mid-run.
func TestGenerateForPeriodRefusesToBillTheSamePeriodTwice(t *testing.T) {
	ctx := ctxT(t)
	f := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 4990, brcal.New(2026, time.March, 1))
	period := f.sub.NextPeriod()

	if _, err := f.invoicer.GenerateForPeriod(ctx, f.sub, []billing.SubscriptionItem{f.item}, period, "scheduler", now()); err != nil {
		t.Fatal(err)
	}
	_, err := f.invoicer.GenerateForPeriod(ctx, f.sub, []billing.SubscriptionItem{f.item}, period, "scheduler", now())
	if err == nil {
		t.Fatal("the second call for the same period must be refused")
	}
	if !errors.Is(err, repositories.ErrAlreadyGenerated) {
		t.Fatalf("want ErrAlreadyGenerated, got %v", err)
	}
}

// TestMeteredInvoiceBillsTheClosedPeriod: usage reported inside the period is
// billed; usage from the next period is not.
func TestMeteredInvoiceBillsTheClosedPeriod(t *testing.T) {
	ctx := ctxT(t)
	f := newSubscribed(t, billing.PriceMetered, billing.BillArrears, 15, brcal.New(2026, time.March, 1))
	usage := repositories.NewUsageRepository(testDB, testCfg)
	period := f.sub.CurrentPeriod() // March, the period that closes on 1 April

	report := func(key string, qty int64, day int) {
		t.Helper()
		rec := &billing.UsageRecord{
			ID: id.NewWithPrefix(id.PrefixUsageRecord), OrganizationID: f.org.ID, Livemode: true,
			SubscriptionItemID: f.item.ID, Quantity: qty,
			OccurredAt:     time.Date(2026, time.March, day, 12, 0, 0, 0, time.UTC),
			IdempotencyKey: key,
		}
		if err := usage.Append(ctx, rec, period.Start, now()); err != nil {
			t.Fatal(err)
		}
	}
	report("u1", 100, 5)
	report("u2", 40, 20)

	if res := f.invoicer.RunDailySweep(ctx, true, brcal.New(2026, time.April, 1), "scheduler", now()); res.Failed != 0 {
		t.Fatalf("sweep result = %+v", res)
	}

	// The invoice is filed under the period it bills — March — not under the day
	// it was generated.
	page, err := f.invoices.ListByMonth(ctx, f.org.ID, true, 2026, 3, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one March invoice, got %d", len(page.Items))
	}
	inv := page.Items[0]

	if inv.Total != 140*15 {
		t.Fatalf("total = %s, want 140 units x R$ 0,15", inv.Total)
	}
	if inv.Period.Start != brcal.New(2026, time.March, 1) {
		t.Fatalf("a metered invoice must bill the closed period, got %s", inv.Period.Start)
	}
	// Billed in arrears, so it falls due at the end of the period it covers.
	if inv.DueDate != brcal.New(2026, time.April, 1) {
		t.Fatalf("due date = %s, want 2026-04-01", inv.DueDate)
	}

	lines, err := f.invoices.ListItems(ctx, f.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].Quantity != 140 {
		t.Fatalf("billed quantity = %d, want 140", lines[0].Quantity)
	}
}

// TestOneBrokenSubscriptionDoesNotStopTheSweep: a single bad row must not stop
// a company's revenue for the day.
func TestOneBrokenSubscriptionDoesNotStopTheSweep(t *testing.T) {
	ctx := ctxT(t)
	anchor := brcal.New(2026, time.March, 1)
	sweepDate := brcal.New(2026, time.April, 1)

	good := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 4990, anchor)

	// A subscription in the same tenant pointing at a price that does not exist.
	broken := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: good.org.ID, Livemode: true,
		CustomerID: "cus_2", Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance, Anchor: anchor,
	}
	brokenItem := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: good.org.ID, Livemode: true,
		SubscriptionID: broken.ID, PriceID: "price_does_not_exist", Quantity: 1,
	}
	if err := good.subs.Create(ctx, broken, []billing.SubscriptionItem{brokenItem}, now()); err != nil {
		t.Fatal(err)
	}

	res := good.invoicer.RunDailySweep(ctx, true, sweepDate, "scheduler", now())
	if res.Failed < 1 || len(res.Errors) < 1 {
		t.Fatalf("the broken subscription must be reported, not swallowed: %+v", res)
	}
	// The healthy one in the same tenant was still billed.
	page, err := good.invoices.ListByMonth(ctx, good.org.ID, true, 2026, 4, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("the healthy subscription produced %d invoices, want 1", len(page.Items))
	}
}

// TestSweepRespectsModePartitions: a test-mode sweep must never touch live rows.
func TestSweepRespectsModePartitions(t *testing.T) {
	ctx := ctxT(t)
	f := newSubscribed(t, billing.PriceFixed, billing.BillAdvance, 4990, brcal.New(2026, time.March, 1))

	if res := f.invoicer.RunDailySweep(ctx, false, brcal.New(2026, time.April, 1), "scheduler", now()); res.Failed != 0 {
		t.Fatalf("sweep result = %+v", res)
	}
	// The live subscription must be untouched: no invoice in either partition.
	for _, livemode := range []bool{true, false} {
		page, err := f.invoices.ListByMonth(ctx, f.org.ID, livemode, 2026, 4, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("a test-mode sweep billed %d invoices in livemode=%v", len(page.Items), livemode)
		}
	}
}
