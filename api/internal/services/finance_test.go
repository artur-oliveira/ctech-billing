package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func day(y int, m time.Month, d int) brcal.Date { return brcal.New(y, m, d) }

func testSpace(t *testing.T) space.ResolvedSpace {
	t.Helper()
	sp, err := space.ForJob("USER#u1", true)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func monthly(id string, dayOfMonth int) finance.Recurrence {
	return finance.Recurrence{
		ID: id, Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: dayOfMonth}, Start: day(2026, time.January, 1), Adjust: finance.AdjustNone},
	}
}

// ---- fakes --------------------------------------------------------------------

type fakeBills struct {
	byRef    map[string]finance.Bill // OriginRef -> bill: the occurrence lock
	created  int
	settled  map[string]int
	autoDue  []repositories.DueBill
	open     map[finance.Direction][]finance.Bill
	settleFn func(id string) error
	failRef  string // CreateFromOccurrence fails for this OriginRef
	// changedRef makes CreateFromOccurrence answer ErrRecurrenceChanged for this ref.
	changedRef string
}

func newFakeBills() *fakeBills {
	return &fakeBills{byRef: map[string]finance.Bill{}, settled: map[string]int{}, open: map[finance.Direction][]finance.Bill{}}
}

func (f *fakeBills) CreateFromOccurrence(_ context.Context, _ space.ResolvedSpace, d finance.Draft, recID string, _ repositories.PostMeta, _ time.Time) (bool, finance.Bill, error) {
	ref := finance.OccurrenceRef(recID, d.Nominal)
	if ref == f.changedRef {
		return false, finance.Bill{}, repositories.ErrRecurrenceChanged
	}
	if ref == f.failRef {
		return false, finance.Bill{}, errors.New("dynamodb: boom")
	}
	if b, ok := f.byRef[ref]; ok {
		return false, b, nil
	}
	b := d.Bill
	b.ID = "bill-" + ref
	f.byRef[ref] = b
	f.created++
	return true, b, nil
}

func (f *fakeBills) Settle(_ context.Context, _ space.ResolvedSpace, id string, _ billing.Cents, _ string, _ brcal.Date, _ repositories.PostMeta, _ time.Time) (finance.Bill, error) {
	if f.settleFn != nil {
		if err := f.settleFn(id); err != nil {
			return finance.Bill{}, err
		}
	}
	f.settled[id]++
	return finance.Bill{ID: id}, nil
}

func (f *fakeBills) DueForAutoSettle(context.Context, bool, brcal.Date, int) ([]repositories.DueBill, int, error) {
	return f.autoDue, 0, nil
}

func (f *fakeBills) ListOpen(_ context.Context, _ space.ResolvedSpace, dir finance.Direction, _ int, _ map[string]types.AttributeValue) (*repositories.Page[finance.Bill], error) {
	return &repositories.Page[finance.Bill]{Items: f.open[dir]}, nil
}

type fakeRecs struct {
	recs    map[string]finance.Recurrence
	cursors map[string]brcal.Date
	markErr error
	marks   int
}

func newFakeRecs(rs ...finance.Recurrence) *fakeRecs {
	f := &fakeRecs{recs: map[string]finance.Recurrence{}, cursors: map[string]brcal.Date{}}
	for _, r := range rs {
		f.recs[r.ID] = r
	}
	return f
}

// DueToMaterialise models the schedule index: a recurrence is on the list when
// the day its next occurrence enters the horizon has arrived.
func (f *fakeRecs) DueToMaterialise(_ context.Context, live bool, today brcal.Date, _ int) ([]repositories.DueRecurrence, int, error) {
	sp, _ := space.ForJob("USER#u1", live)
	var out []repositories.DueRecurrence
	for id, r := range f.recs {
		if next, ok := r.NextMaterialiseDate(f.cursors[id]); ok && !next.After(today) {
			out = append(out, repositories.DueRecurrence{Space: sp, Recurrence: r, Cursor: f.cursors[id]})
		}
	}
	return out, 0, nil
}

func (f *fakeRecs) MarkMaterialised(_ context.Context, _ space.ResolvedSpace, id string, cursor brcal.Date, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marks++
	if cursor.After(f.cursors[id]) {
		f.cursors[id] = cursor
	}
	return nil
}

func (f *fakeRecs) ListWithCursors(_ context.Context, sp space.ResolvedSpace) ([]repositories.DueRecurrence, error) {
	out := make([]repositories.DueRecurrence, 0, len(f.recs))
	for id, r := range f.recs {
		out = append(out, repositories.DueRecurrence{Space: sp, Recurrence: r, Cursor: f.cursors[id]})
	}
	return out, nil
}

// ---- Materialise ---------------------------------------------------------------

// Review Focus 1.
func TestMaterialiseIsRerunnable(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	jobs := NewFinanceJobs(bills, recs)
	today, now := day(2026, time.March, 20), time.Now()

	first := jobs.Materialise(context.Background(), true, today, now)
	if first.Examined != 1 || first.Done != 4 || first.Skipped != 0 || first.Failed != 0 {
		t.Fatalf("first run = %+v, want 4 bills (Jan..Apr)", first)
	}
	for i := 0; i < 2; i++ {
		again := jobs.Materialise(context.Background(), true, today, now)
		if again.Examined != 0 || again.Done != 0 {
			t.Fatalf("re-run %d = %+v, want nothing to do", i+1, again)
		}
	}
	if bills.created != 4 || len(bills.byRef) != 4 {
		t.Fatalf("created %d bills (%d refs), want exactly 4", bills.created, len(bills.byRef))
	}
}

// A crash between creating the bills and moving the cursor: the next run finds
// the recurrence due again with the old cursor, asks for the same occurrences,
// and the lock turns every one into a skip — not a second bill.
func TestARerunAfterACrashBeforeTheCursorMovedCreatesNothingNew(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	jobs := NewFinanceJobs(bills, recs)
	today, now := day(2026, time.March, 20), time.Now()

	recs.markErr = errors.New("crash")
	crashed := jobs.Materialise(context.Background(), true, today, now)
	if crashed.Failed != 1 || bills.created != 4 {
		t.Fatalf("crashed run = %+v, created %d", crashed, bills.created)
	}
	recs.markErr = nil
	healed := jobs.Materialise(context.Background(), true, today, now)
	if healed.Done != 0 || healed.Skipped != 4 || healed.Failed != 0 {
		t.Fatalf("healing run = %+v, want 4 skipped and 0 created", healed)
	}
	if bills.created != 4 {
		t.Fatalf("%d bills after the rerun, want still 4", bills.created)
	}
	if recs.cursors["r1"] != day(2026, time.April, 10) {
		t.Fatalf("cursor = %s, want 2026-04-10", recs.cursors["r1"])
	}
}

func TestMaterialiseStopsARecurrenceAtItsFirstFailureAndKeepsTheCursor(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	bills.failRef = finance.OccurrenceRef("r1", day(2026, time.March, 10))
	jobs := NewFinanceJobs(bills, recs)

	res := jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 1 || len(res.Errors) != 1 || res.Done != 2 {
		t.Fatalf("res = %+v, want Jan and Feb created, March failed", res)
	}
	if _, ok := bills.byRef[finance.OccurrenceRef("r1", day(2026, time.April, 10))]; ok {
		t.Fatal("the run kept going past the failed occurrence")
	}
	if !recs.cursors["r1"].IsZero() || recs.marks != 0 {
		t.Fatalf("the cursor moved (%s) although the recurrence failed", recs.cursors["r1"])
	}
}

func TestMaterialiseOneBadRecurrenceDoesNotStopTheOthers(t *testing.T) {
	bad := monthly("bad", 10)
	bad.Schedule.Start = day(1900, time.January, 1) // refused: catch-up beyond the bound
	good := monthly("good", 15)
	bills, recs := newFakeBills(), newFakeRecs(bad, good)
	jobs := NewFinanceJobs(bills, recs)

	res := jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 1 || res.Done != 4 {
		t.Fatalf("res = %+v, want the bad one failed and the good one's 4 bills created", res)
	}
}

// ---- AutoSettle ----------------------------------------------------------------

func dueBill(id string, due brcal.Date) repositories.DueBill {
	return repositories.DueBill{Space: space.ResolvedSpace{}, Bill: finance.Bill{ID: id, Amount: 4200, Due: due, Direction: finance.Payable}}
}

func TestAutoSettleSettlesAtTheDueDateAndTheBillsOwnAmount(t *testing.T) {
	bills := newFakeBills()
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10)), dueBill("b2", day(2026, time.March, 12))}
	var gotAmount billing.Cents
	var gotDates []brcal.Date
	jobs := NewFinanceJobs(&recordingSettler{fakeBills: bills, amount: &gotAmount, dates: &gotDates}, newFakeRecs())

	res := jobs.AutoSettle(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Examined != 2 || res.Done != 2 || res.Failed != 0 {
		t.Fatalf("res = %+v", res)
	}
	if gotAmount != 4200 || len(gotDates) != 2 || gotDates[0] != day(2026, time.March, 10) || gotDates[1] != day(2026, time.March, 12) {
		t.Fatalf("settled at %v amount %d: each bill settles on ITS due date, for its own amount, not on the day the job ran", gotDates, gotAmount)
	}
}

// recordingSettler records what Settle was called with.
type recordingSettler struct {
	*fakeBills
	amount *billing.Cents
	dates  *[]brcal.Date
}

func (r *recordingSettler) Settle(ctx context.Context, sp space.ResolvedSpace, id string, paid billing.Cents, diff string, date brcal.Date, m repositories.PostMeta, now time.Time) (finance.Bill, error) {
	*r.amount = paid
	*r.dates = append(*r.dates, date)
	return r.fakeBills.Settle(ctx, sp, id, paid, diff, date, m, now)
}

func TestAutoSettleTreatsAnAlreadySettledBillAsSkipped(t *testing.T) {
	bills := newFakeBills()
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10)), dueBill("b2", day(2026, time.March, 10))}
	bills.settleFn = func(id string) error {
		if id == "b1" {
			return finance.ErrBillState // settled or cancelled since the index was read
		}
		return nil
	}
	res := NewFinanceJobs(bills, newFakeRecs()).AutoSettle(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Skipped != 1 || res.Done != 1 || res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("res = %+v: a bill that is no longer a forecast is not a failure", res)
	}
}

func TestAutoSettleReportsOtherFailuresAndKeepsGoing(t *testing.T) {
	bills := newFakeBills()
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10)), dueBill("b2", day(2026, time.March, 10))}
	bills.settleFn = func(id string) error {
		if id == "b1" {
			return errors.New("dynamodb: throttled")
		}
		return nil
	}
	res := NewFinanceJobs(bills, newFakeRecs()).AutoSettle(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 1 || res.Done != 1 || len(res.Errors) != 1 {
		t.Fatalf("res = %+v", res)
	}
}

// ---- Projection ----------------------------------------------------------------

func TestProjectKeepsVirtualOccurrencesApartFromForecastBills(t *testing.T) {
	bills := newFakeBills()
	bills.open[finance.Payable] = []finance.Bill{
		{ID: "p1", Direction: finance.Payable, Amount: 40000, Due: day(2026, time.April, 15)},
		{ID: "overdue", Direction: finance.Payable, Amount: 1000, Due: day(2026, time.February, 1)}, // before the window
	}
	bills.open[finance.Receivable] = []finance.Bill{{ID: "r1", Direction: finance.Receivable, Amount: 90000, Due: day(2026, time.April, 2)}}
	recs := newFakeRecs(monthly("rent", 10))
	recs.cursors["rent"] = day(2026, time.April, 10) // the job has materialised through the horizon
	jobs := NewFinanceJobs(bills, recs)

	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 5) // Mar..Jul
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Months) != 5 || p.Months[0].Month != (finance.Month{Year: 2026, Month: time.March}) {
		t.Fatalf("months = %+v", p.Months)
	}
	byMonth := map[time.Month]ProjectionMonth{}
	for _, m := range p.Months {
		byMonth[m.Month.Month] = m
	}
	if got := byMonth[time.April]; got.Payable != 40000 || got.Receivable != 90000 {
		t.Errorf("April = %+v: the bills are forecast, in their due month", got)
	}
	if got := byMonth[time.March]; got.Payable != 1000 {
		t.Errorf("March = %+v: an overdue bill counts in the first month", got)
	}
	// The horizon at 20/03 ends 30/04, so the occurrences of 10/05, 10/06 and
	// 10/07 are virtual: net -150000 each, and never inside Payable.
	for _, m := range []time.Month{time.May, time.June, time.July} {
		if got := byMonth[m]; got.Virtual != -150000 || got.Payable != 0 {
			t.Errorf("%s = %+v, want virtual -150000 apart from the bills", m, got)
		}
	}
	if byMonth[time.April].Virtual != 0 || byMonth[time.March].Virtual != 0 {
		t.Error("an occurrence inside the horizon is a bill, not a virtual one")
	}
}

// A recurrence created today has no bills yet (the job runs tomorrow): every
// occurrence after its cursor is virtual, so the months inside the horizon are
// not empty in the meantime.
func TestProjectCountsWhatTheJobHasNotMaterialisedYet(t *testing.T) {
	r := monthly("rent", 10)
	r.Schedule.Start = day(2026, time.March, 25)
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs(r))
	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 25), 3) // Mar..May
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Months[1:] {
		if m.Virtual != -150000 {
			t.Errorf("%s virtual = %d, want -150000 (not yet a bill)", m.Month, m.Virtual)
		}
	}
	if p.Months[0].Virtual != 0 {
		t.Errorf("March: the first occurrence is April 10, got %d", p.Months[0].Virtual)
	}
}

func TestProjectRefusesAnUnreasonableWindow(t *testing.T) {
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs())
	for _, n := range []int{0, 13, -1} {
		if _, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), n); err == nil {
			t.Errorf("%d months was accepted", n)
		}
	}
}

// One tenant must not be able to stretch the run: a recurrence with years of
// catch-up is worked in batches across runs, the cursor carrying the resume.
func TestMaterialiseCapsTheDraftsPerRecurrencePerRun(t *testing.T) {
	r := monthly("old", 10)
	r.Schedule.Start = day(2021, time.April, 1) // five years back: 60+ months owed
	bills, recs := newFakeBills(), newFakeRecs(r)
	jobs := NewFinanceJobs(bills, recs)
	today, now := day(2026, time.March, 20), time.Now()

	first := jobs.Materialise(context.Background(), true, today, now)
	if first.Done != maxDraftsPerRun || first.Failed != 0 {
		t.Fatalf("first run = %+v, want exactly %d bills", first, maxDraftsPerRun)
	}
	total := first.Done
	for i := 0; i < 5 && total < 61; i++ {
		total += jobs.Materialise(context.Background(), true, today, now).Done
	}
	if total != 61 { // Apr 2021 .. Apr 2026, inclusive
		t.Fatalf("after catching up %d bills exist, want 61 (Apr 2021 to Apr 2026)", total)
	}
}

// The recurrence was archived or retargeted after the job read it: the stale
// snapshot must not turn into bills. It is a skip, not a failure, and the cursor
// stays so the next run works from the current rule.
func TestARecurrenceChangedUnderTheJobIsSkippedNotFailed(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	bills.changedRef = finance.OccurrenceRef("r1", day(2026, time.February, 10))
	jobs := NewFinanceJobs(bills, recs)

	res := jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 0 || len(res.Errors) != 0 || res.Skipped < 1 {
		t.Fatalf("res = %+v: a changed recurrence is a skip", res)
	}
	if !recs.cursors["r1"].IsZero() {
		t.Fatalf("the cursor moved to %s although the recurrence changed under the job", recs.cursors["r1"])
	}
}

// ---- CloseStatements -------------------------------------------------------------

type fakeCards struct {
	due    []repositories.DueCard
	closed []string
	fail   string
}

func (f *fakeCards) DueToClose(context.Context, bool, brcal.Date, int) ([]repositories.DueCard, int, error) {
	return f.due, 0, nil
}

func (f *fakeCards) CloseDue(_ context.Context, _ space.ResolvedSpace, cardID string, _ brcal.Date, _ time.Time) (int, error) {
	if cardID == f.fail {
		return 0, errors.New("dynamodb: throttled")
	}
	f.closed = append(f.closed, cardID)
	return 1, nil
}

func TestCloseStatementsClosesEveryDueCardAndSurvivesOneFailure(t *testing.T) {
	sp := testSpace(t)
	cards := &fakeCards{due: []repositories.DueCard{{Space: sp, CardID: "a"}, {Space: sp, CardID: "b"}, {Space: sp, CardID: "c"}}, fail: "b"}
	res := NewFinanceJobs(newFakeBills(), newFakeRecs()).WithCards(cards).CloseStatements(context.Background(), true, day(2026, time.March, 3), time.Now())
	if res.Examined != 3 || res.Done != 2 || res.Failed != 1 || len(cards.closed) != 2 {
		t.Fatalf("res = %+v, closed = %v", res, cards.closed)
	}
}
