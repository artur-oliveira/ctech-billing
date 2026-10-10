//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type maxFixture struct {
	*catalogFixture
	levels   *repositories.LevelRepository
	invoicer *services.Invoicer
	ref      string
	lastKey  string
	sub      *billing.Subscription
	items    []billing.SubscriptionItem
}

func newMaxFixture(t *testing.T, anchor brcal.Date) *maxFixture {
	t.Helper()
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	ref := "USER_" + id.New()
	cust := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: f.org.ID, Livemode: true, Name: "P", ExternalRef: ref}
	if err := customers.Create(ctx, cust, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	cat := f.catalog
	price := &billing.Price{ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: f.org.ID, Livemode: true, ProductID: f.product.ID,
		Type: billing.PriceMetered, Currency: billing.CurrencyBRL, UnitAmount: 490,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1}, Timing: billing.BillArrears,
		Aggregation: billing.AggregationMax, Meter: "finance_spaces", IncludedQuantity: 1}
	if err := cat.CreatePrice(ctx, price, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true, CustomerID: cust.ID,
		Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	items, _ := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	inv := services.NewInvoicer(f.subs, f.invoices, cat, f.usage).WithLevels(levels, customers)
	return &maxFixture{catalogFixture: f, levels: levels, invoicer: inv, ref: ref, sub: sub, items: items}
}

func (m *maxFixture) report(t *testing.T, v int64, at time.Time) {
	t.Helper()
	m.lastKey = id.New()
	if err := m.levels.Append(ctxT(t), &billing.LevelRecord{OrganizationID: m.org.ID, Livemode: true, CustomerRef: m.ref,
		Meter: "finance_spaces", Value: v, OccurredAt: at, IdempotencyKey: m.lastKey}, now()); err != nil {
		t.Fatal(err)
	}
}

func (m *maxFixture) close(t *testing.T, p billing.Period) *billing.Invoice {
	t.Helper()
	inv, err := m.invoicer.GenerateForPeriod(ctxT(t), m.sub, m.items, p, "scheduler", now())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestAPeakMidMonthIsBilledBeyondTheIncludedUnit(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.February, 1))
	m.report(t, 2, time.Date(2026, time.January, 20, 15, 0, 0, 0, time.UTC)) // carried in
	m.report(t, 5, time.Date(2026, time.February, 10, 15, 0, 0, 0, time.UTC))
	m.report(t, 1, time.Date(2026, time.February, 20, 15, 0, 0, 0, time.UTC))
	inv := m.close(t, billing.Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)})
	if inv.Total != 4*490 {
		t.Fatalf("total = %d, want %d", inv.Total, 4*490)
	}
}

// Review Focus 3: the level was reported long ago and never changed; its
// report is gone (TTL), and the period is still billed on it.
func TestALevelOlderThanItsTTLIsStillCarriedIn(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.January, 1))
	at := time.Date(2024, time.November, 15, 15, 0, 0, 0, time.UTC)
	m.report(t, 3, at)
	// Simulate the TTL: delete every report of the meter, keep the latest item.
	base := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	recs, _ := m.levels.InPeriod(ctxT(t), m.org.ID, true, m.ref, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	for _, r := range recs {
		_, _ = base.DeleteItem(ctxT(t), repositories.LevelPK(m.org.ID, true, m.ref, "finance_spaces"), repositories.LevelSK(r.OccurredAt, r.IdempotencyKey))
	}
	jan := billing.Period{Start: brcal.New(2026, time.January, 1), End: brcal.New(2026, time.February, 1)}
	if inv := m.close(t, jan); inv.Total != 2*490 {
		t.Fatalf("january = %d, want %d", inv.Total, 2*490)
	}
}

// The old report expired, and the level changed during the period: the
// report's own `previous` is the carried-in level.
func TestAnExpiredCarriedInLevelIsReadFromTheFirstReportsPrevious(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.January, 1))
	old := time.Date(2024, time.November, 15, 15, 0, 0, 0, time.UTC)
	m.report(t, 6, old)
	usageBase := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	_, _ = usageBase.DeleteItem(ctxT(t),
		repositories.LevelPK(m.org.ID, true, m.ref, "finance_spaces"), repositories.LevelSK(old, m.lastKey))
	m.report(t, 2, time.Date(2026, time.January, 20, 15, 0, 0, 0, time.UTC))
	jan := billing.Period{Start: brcal.New(2026, time.January, 1), End: brcal.New(2026, time.February, 1)}
	if inv := m.close(t, jan); inv.Total != 5*490 {
		t.Fatalf("january = %d, want %d (6 held until the 20th)", inv.Total, 5*490)
	}
}

func TestAMaxPriceOnACustomerWithoutRefFailsLoudly(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.February, 1))
	m.sub.CustomerID = "cus_missing"
	if _, err := m.invoicer.GenerateForPeriod(ctxT(t), m.sub, m.items,
		billing.Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)}, "scheduler", now()); err == nil {
		t.Fatal("a max price with no customer reference must not bill 0")
	}
}
