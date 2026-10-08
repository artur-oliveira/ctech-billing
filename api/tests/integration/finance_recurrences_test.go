//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func (f billsFixture) recurrence(t *testing.T, day int, autoSettle bool) (finance.Recurrence, *repositories.RecurrenceRepository) {
	t.Helper()
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	rec, err := recs.Create(context.Background(), f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		AutoSettle: autoSettle,
		Schedule:   finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return rec, recs
}

func TestAnOccurrenceCreatesExactlyOneBillHoweverOftenItIsAsked(t *testing.T) {
	f := newBillsFixture(t)
	rec, _ := f.recurrence(t, 10, false)
	drafts, err := rec.Materialise(brcal.Date{}, brcal.New(2026, 3, 20))
	if err != nil || len(drafts) != 4 {
		t.Fatalf("drafts = %d, %v", len(drafts), err)
	}
	for pass := 0; pass < 3; pass++ {
		for _, d := range drafts {
			created, _, err := f.bills.CreateFromOccurrence(context.Background(), f.sp, d, rec.ID, repositories.PostMeta{Actor: "scheduler"}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if want := pass == 0; created != want {
				t.Fatalf("pass %d nominal %s: created = %v, want %v", pass, d.Nominal, created, want)
			}
		}
	}
	if got := f.bal(t, "sys-payables"); got != -4*150000 {
		t.Fatalf("payables = %d, want exactly four recognitions", got)
	}
	drift, err := f.ledger.Rebuild(context.Background(), f.sp, false, time.Now())
	if err != nil || len(drift) != 0 {
		t.Fatalf("drift %+v %v", drift, err)
	}
}

func TestTheMaterialiseListHoldsARecurrenceUntilItIsDueAndCatchesUpMissedDays(t *testing.T) {
	f := newBillsFixture(t)
	rec, recs := f.recurrence(t, 10, false) // created "on" 2026-03-20; first run owes Jan..Apr

	due, skipped, err := recs.DueToMaterialise(context.Background(), true, brcal.New(2026, 3, 20), 10000)
	if err != nil || skipped != 0 {
		t.Fatal(err, skipped)
	}
	if !containsRecurrence(due, rec.ID) {
		t.Fatalf("a new recurrence is not on the work list (%d rows)", len(due))
	}
	for _, d := range due {
		if d.Recurrence.ID == rec.ID {
			if !d.Cursor.IsZero() {
				t.Fatalf("a never-run recurrence has a cursor: %s", d.Cursor)
			}
		}
	}
	// Run it up to April 10th, then it should come due again only when 10/05
	// enters the horizon (1 April).
	if err := recs.MarkMaterialised(context.Background(), f.sp, rec.ID, brcal.New(2026, time.April, 10), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := recs.MarkMaterialised(context.Background(), f.sp, rec.ID, brcal.New(2026, time.April, 10), time.Now()); err != nil {
		t.Fatalf("a repeated MarkMaterialised must be a no-op: %v", err)
	}
	if err := recs.MarkMaterialised(context.Background(), f.sp, rec.ID, brcal.New(2026, time.February, 10), time.Now()); err != nil {
		t.Fatalf("an older cursor must be a no-op, never a rewind: %v", err)
	}
	for _, c := range []struct {
		today brcal.Date
		want  bool
	}{
		{brcal.New(2026, 3, 31), false},
		{brcal.New(2026, 4, 1), true},
		{brcal.New(2026, 4, 20), true}, // a missed day is caught up
	} {
		due, _, err := recs.DueToMaterialise(context.Background(), true, c.today, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if got := containsRecurrence(due, rec.ID); got != c.want {
			t.Errorf("on %s: due = %v, want %v", c.today, got, c.want)
		}
		for _, d := range due {
			if d.Recurrence.ID == rec.ID && d.Cursor != brcal.New(2026, time.April, 10) {
				t.Errorf("cursor = %s, want 2026-04-10 (the repeated and the older call changed it)", d.Cursor)
			}
		}
	}
}

func containsRecurrence(due []repositories.DueRecurrence, id string) bool {
	for _, d := range due {
		if d.Recurrence.ID == id {
			return true
		}
	}
	return false
}

func TestAScheduleRowWithAnUnknownOwnerIsSkippedNotTrusted(t *testing.T) {
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	base := repositories.NewBase(testDB, testCfg, repositories.TableRecurrences)
	forged := map[string]types.AttributeValue{
		"pk":          &types.AttributeValueMemberS{Value: "attacker#live"},
		"sk":          &types.AttributeValueMemberS{Value: "RECURRENCE#x"},
		"schedule_pk": &types.AttributeValueMemberS{Value: repositories.MaterialisePK(true)},
		// owner "not-a-uuid" cannot be turned into a space
		"schedule_sk": &types.AttributeValueMemberS{Value: "2026-01-01#not-a-uuid#x"},
	}
	if err := base.PutItem(context.Background(), forged); err != nil {
		t.Fatal(err)
	}
	due, skipped, err := recs.DueToMaterialise(context.Background(), true, brcal.New(2026, 3, 20), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if skipped < 1 {
		t.Fatalf("the forged row was not counted as skipped (skipped=%d)", skipped)
	}
	for _, d := range due {
		if d.Space.Owner() == "not-a-uuid" {
			t.Fatal("the job would act on a forged owner")
		}
	}
}

func TestAutoSettleBillsAreOnTheWorkListUntilSettled(t *testing.T) {
	f := newBillsFixture(t)
	b, err := f.bills.Create(context.Background(), f.sp, finance.Bill{
		Direction: finance.Payable, Amount: 777, AccountID: "bank", CategoryID: "rent", AutoSettle: true,
		Competence: brcal.New(2026, 3, 1), Due: brcal.New(2026, 3, 10), Origin: finance.OriginManual,
	}, repositories.PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	has := func(today brcal.Date) bool {
		due, _, err := f.bills.DueForAutoSettle(context.Background(), true, today, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range due {
			if d.Bill.ID == b.ID {
				return true
			}
		}
		return false
	}
	if has(brcal.New(2026, 3, 9)) || !has(brcal.New(2026, 3, 10)) || !has(brcal.New(2026, 3, 25)) {
		t.Fatal("the work list does not follow the due date (or a missed day is not caught up)")
	}
	if _, err := f.bills.Settle(context.Background(), f.sp, b.ID, 777, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if has(brcal.New(2026, 3, 25)) {
		t.Fatal("a settled bill is still on the auto-settle list")
	}
}

// Review Focus 1, end to end: the real job logic over the real tables, run
// repeatedly, with a recurrence that auto-settles.
func TestTheJobIsRerunnableAgainstRealTables(t *testing.T) {
	f := newBillsFixture(t)
	ctx := context.Background()
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	today := brcal.New(2026, 3, 20)

	_, err := recs.Create(ctx, f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		AutoSettle: true,
		Schedule:   finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, now)
	if err != nil {
		t.Fatal(err)
	}
	job := services.NewFinanceJobs(f.bills, recs)

	// The work lists are shared with the other tests in this run, so assertions
	// are on THIS space's data, never on the job's counters.
	for run := 0; run < 3; run++ {
		_ = job.Materialise(ctx, true, today, now)
		_ = job.AutoSettle(ctx, true, today, now)
	}

	// Jan, Feb, Mar and Apr occurrences: four recognitions.
	if got := f.bal(t, "rent"); got != 4*150000 {
		t.Fatalf("rent = %d, want exactly four months recognised", got)
	}
	// Auto-settle runs for bills due on or before today: Jan 10, Feb 10, Mar 10.
	// April's is due in the future and stays a forecast.
	if got := f.bal(t, "bank"); got != -3*150000 {
		t.Fatalf("bank = %d, want exactly three settlements (one per due bill, however often the job ran)", got)
	}
	if got := f.bal(t, "sys-payables"); got != -150000 {
		t.Fatalf("payables = %d, want only April still owed", got)
	}
	page, err := f.bills.ListOpen(ctx, f.sp, finance.Payable, 50, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].Due != brcal.New(2026, time.April, 10) {
		t.Fatalf("open bills = %+v, %v; want only April's", page.Items, err)
	}
	drift, err := f.ledger.Rebuild(ctx, f.sp, false, time.Now())
	if err != nil || len(drift) != 0 {
		t.Fatalf("drift %+v %v", drift, err)
	}

	// A later day catches up: April 10th arrives, the recurrence rolls its
	// horizon (May enters on 1 April) and the April bill settles.
	later, laterNow := brcal.New(2026, 4, 12), time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	for run := 0; run < 2; run++ {
		_ = job.Materialise(ctx, true, later, laterNow)
		_ = job.AutoSettle(ctx, true, later, laterNow)
	}
	if got := f.bal(t, "rent"); got != 5*150000 {
		t.Fatalf("after April: rent = %d, want five months", got)
	}
	if got := f.bal(t, "bank"); got != -4*150000 {
		t.Fatalf("after April: bank = %d, want four settlements", got)
	}
}
