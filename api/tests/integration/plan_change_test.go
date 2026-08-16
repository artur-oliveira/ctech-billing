//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// changeAt is the day every test here swaps plan on: the 10th of a 31-day March,
// anchored on the 1st. Nine days served, twenty-two remaining — a fraction that
// is not a half, so a proration that silently returns the full price or half of
// it fails rather than passing by coincidence.
var changeAt = time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)

// subscribeFixed starts an ACTIVE subscription on a fixed advance price.
//
// It pays the first invoice, because a paid plan starts INCOMPLETE (1.3) and
// INCOMPLETE has no self-edge — a plan change on a subscription that never
// activated is refused, which is its own test below.
func subscribeFixed(t *testing.T, f *catalogFixture, price *billing.Price) *billing.Subscription {
	t.Helper()
	ctx := ctxT(t)

	sub, inv, err := f.subscribe(ctx, price)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionIncomplete {
		t.Fatalf("a paid plan starts INCOMPLETE, got %s", sub.Status)
	}
	if _, err := f.invoices.Transition(
		ctx, inv, billing.InvoicePaid, billing.CauseManualPayment, "test", "", now(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.subs.Transition(
		ctx, sub, billing.SubscriptionActive, billing.CauseInvoicePaid, "test", "", now(),
	); err != nil {
		t.Fatal(err)
	}
	return sub
}

// TestUpgradeMidPeriodChargesTheRemainderAndCreditsTheOld is the case the whole
// feature exists for: Pro to Ilimitado on the 10th of a 31-day month.
//
// It asserts the two lines separately rather than only the total, because the
// total is the one thing that would still look right if the credit and the
// charge were computed from the wrong period each.
func TestUpgradeMidPeriodChargesTheRemainderAndCreditsTheOld(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	unlimited := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 250000, billing.IntervalMonth)
	sub := subscribeFixed(t, f, pro)

	inv, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{
		Items: []services.SubscribeItem{{PriceID: unlimited.ID, Quantity: 1}},
		Cause: billing.CauseManual,
		Actor: "test",
	}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil {
		t.Fatal("an upgrade costs money, so it must produce an invoice")
	}

	// The same arithmetic the proration property test already pins, computed
	// here from the domain rather than hard-coded: a literal would have to be
	// recomputed by hand the day the anchor in this fixture changes, and a
	// recomputed literal is a literal somebody gets wrong.
	period := sub.CurrentPeriod()
	at := brcal.FromTime(changeAt)
	want := billing.ProrateSwap(35000, 250000, period, at)
	if inv.Total != want.Net() {
		t.Fatalf("invoice total = %s, want %s", inv.Total, want.Net())
	}

	lines, err := f.invoices.ListItems(ctx, f.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("a swap is two lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Amount != -want.Credit {
		t.Fatalf("credit line = %s, want %s", lines[0].Amount, -want.Credit)
	}
	if lines[1].Amount != want.Charge {
		t.Fatalf("charge line = %s, want %s", lines[1].Amount, want.Charge)
	}
	for _, l := range lines {
		if !l.Proration {
			t.Fatalf("every line of a swap is a proration: %+v", l)
		}
	}

	// The items were actually replaced, and the billing day did not move.
	items, err := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PriceID != unlimited.ID {
		t.Fatalf("items = %+v, want one item on %s", items, unlimited.ID)
	}
	if sub.CurrentPeriod() != period {
		t.Fatalf("the period moved: %+v, want %+v", sub.CurrentPeriod(), period)
	}
}

// TestUpgradingFromFreeChargesOnlyTheNewPlan: the credit line is dropped when
// there is nothing to credit, so a customer leaving the free plan does not read
// "Crédito proporcional — R$ 0,00" on their first real bill.
func TestUpgradingFromFreeChargesOnlyTheNewPlan(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	free := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)

	// A free plan starts ACTIVE — nothing is owed, so there is no payment to wait
	// for. That is what makes it changeable without the activation dance above.
	sub, _, err := f.subscribe(ctx, free)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionActive {
		t.Fatalf("a free plan starts ACTIVE, got %s", sub.Status)
	}

	inv, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{
		Items: []services.SubscribeItem{{PriceID: pro.ID, Quantity: 1}},
		Cause: billing.CauseManual,
		Actor: "test",
	}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := f.invoices.ListItems(ctx, f.org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("nothing to credit, so one line; got %d: %+v", len(lines), lines)
	}
	want := billing.ProrateRemaining(35000, sub.CurrentPeriod(), brcal.FromTime(changeAt))
	if lines[0].Amount != want || inv.Total != want {
		t.Fatalf("charge = %s / total = %s, want %s", lines[0].Amount, inv.Total, want)
	}
}

// TestDowngradeIssuesNoInvoice pins the decision in Subscriber.ChangePlan: a
// change that nets zero or negative is money owed back, which is a credit note
// and not a negative invoice. The plan changes; nothing is billed.
//
// This is the test to change when credit notes exist.
func TestDowngradeIssuesNoInvoice(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	free := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	sub := subscribeFixed(t, f, pro)

	inv, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{
		Items: []services.SubscribeItem{{PriceID: free.ID, Quantity: 1}},
		Cause: billing.CauseManual,
		Actor: "test",
	}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	if inv != nil {
		t.Fatalf("a downgrade must not produce an invoice, got %s totalling %s", inv.ID, inv.Total)
	}
	items, err := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PriceID != free.ID {
		t.Fatalf("the plan must still change: items = %+v", items)
	}
}

// TestChangingToMeteredCreditsNothingAndChargesNothing: moving from a fixed plan
// to sob-demanda charges nothing in advance, because nothing has been consumed
// yet. The subscription's timing follows the new prices, which is what makes the
// closed period's usage arrive on the normal sweep.
func TestChangingToMeteredCreditsNothingAndChargesNothing(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	nfe := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)
	sub := subscribeFixed(t, f, pro)

	inv, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{
		Items: []services.SubscribeItem{{PriceID: nfe.ID, Quantity: 1}},
		Cause: billing.CauseManual,
		Actor: "test",
	}, changeAt)
	if err != nil {
		t.Fatal(err)
	}
	if inv != nil {
		t.Fatalf("moving to usage-based bills nothing up front, got invoice %s", inv.ID)
	}
	if sub.Timing != billing.BillArrears {
		t.Fatalf("timing = %s, want arrears — it follows the new prices", sub.Timing)
	}
}

// TestChangingAnIncompleteSubscriptionIsRefused: the guard is the domain's, not
// a check in the service. INCOMPLETE has no self-edge, so a plan change on a
// subscription whose first invoice was never paid cannot be expressed at all.
func TestChangingAnIncompleteSubscriptionIsRefused(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe")

	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 35000, billing.IntervalMonth)
	unlimited := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 250000, billing.IntervalMonth)

	sub, _, err := f.subscribe(ctx, pro)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.subber.ChangePlan(ctx, sub, services.ChangeInput{
		Items: []services.SubscribeItem{{PriceID: unlimited.ID, Quantity: 1}},
		Cause: billing.CauseManual,
		Actor: "test",
	}, changeAt)
	if err == nil {
		t.Fatal("a subscription that never activated must not be changeable")
	}
}
