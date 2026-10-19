package services

import (
	"context"
	"errors"
	"fmt"
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

func (f *fakeBills) DueForAutoSettlePage(context.Context, bool, brcal.Date, int, map[string]types.AttributeValue) ([]repositories.DueBill, int, map[string]types.AttributeValue, error) {
	return f.autoDue, 0, nil, nil
}

func (f *fakeBills) ListOpen(_ context.Context, _ space.ResolvedSpace, dir finance.Direction, _ int, _ map[string]types.AttributeValue) (*repositories.Page[finance.Bill], error) {
	return &repositories.Page[finance.Bill]{Items: f.open[dir]}, nil
}

type fakeRecs struct {
	recs    map[string]finance.Recurrence
	cursors map[string]brcal.Date
	markErr error
	marks   int
	flags   map[string]string
	pending map[string]repositories.PendingOccurrence
}

func newFakeRecs(rs ...finance.Recurrence) *fakeRecs {
	f := &fakeRecs{recs: map[string]finance.Recurrence{}, cursors: map[string]brcal.Date{}, flags: map[string]string{}, pending: map[string]repositories.PendingOccurrence{}}
	for _, r := range rs {
		f.recs[r.ID] = r
	}
	return f
}

// DueToMaterialise models the schedule index: a recurrence is on the list when
// the day its next occurrence enters the horizon has arrived.
func (f *fakeRecs) DueToMaterialisePage(_ context.Context, live bool, today brcal.Date, _ int, _ map[string]types.AttributeValue) ([]repositories.DueRecurrence, int, map[string]types.AttributeValue, error) {
	sp, _ := space.ForJob("USER#u1", live)
	var out []repositories.DueRecurrence
	for id, r := range f.recs {
		if next, ok := r.NextMaterialiseDate(f.cursors[id]); ok && !next.After(today) {
			out = append(out, repositories.DueRecurrence{Space: sp, Recurrence: r, Cursor: f.cursors[id]})
		}
	}
	return out, 0, nil, nil
}

func (f *fakeRecs) MarkMaterialised(_ context.Context, _ space.ResolvedSpace, id string, cursor brcal.Date, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marks++
	if cursor.After(f.cursors[id]) {
		f.cursors[id] = cursor
		delete(f.flags, id)
	}
	return nil
}

func (f *fakeRecs) FlagAttention(_ context.Context, _ space.ResolvedSpace, id, reason string, _ time.Time) error {
	f.flags[id] = reason
	return nil
}

func (f *fakeRecs) CreatePendingOccurrence(_ context.Context, _ space.ResolvedSpace, rec finance.Recurrence, nominal, due brcal.Date, _ time.Time) (bool, error) {
	key := finance.OccurrenceRef(rec.ID, nominal)
	if _, exists := f.pending[key]; exists {
		return false, nil
	}
	f.pending[key] = repositories.PendingOccurrence{RecurrenceID: rec.ID, Nominal: nominal, Due: due, Expected: rec.Amount, State: "pending_confirmation"}
	return true, nil
}

func (f *fakeRecs) ListPendingOccurrences(_ context.Context, _ space.ResolvedSpace, _ int) ([]repositories.PendingOccurrence, error) {
	out := make([]repositories.PendingOccurrence, 0, len(f.pending))
	for _, p := range f.pending {
		out = append(out, p)
	}
	return out, nil
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

func TestExpectedRecurrenceQueuesConfirmationWithoutPostingBill(t *testing.T) {
	rec := monthly("variable-food", 10)
	rec.AmountMode = finance.AmountExpected
	bills, recs := newFakeBills(), newFakeRecs(rec)
	result := NewFinanceJobs(bills, recs).Materialise(context.Background(), true, day(2026, time.January, 10), time.Date(2026, time.January, 10, 12, 0, 0, 0, time.UTC))
	ref := finance.OccurrenceRef(rec.ID, day(2026, time.January, 10))
	if result.Failed != 0 || len(recs.pending) != 1 || bills.created != 0 {
		t.Fatalf("result=%+v pending=%v bills=%d", result, recs.pending, bills.created)
	}
	if got := recs.pending[ref]; got.Expected != rec.Amount || got.State != "pending_confirmation" {
		t.Fatalf("pending = %+v", got)
	}
}

func TestExpectedCardOccurrenceWaitsForConfirmation(t *testing.T) {
	rec := onCard("variable-card", 28, 1)
	rec.AmountMode = finance.AmountExpected
	bills, cards, recs := newFakeBills(), newRecCards(visa25()), newFakeRecs(rec)
	result := NewFinanceJobs(bills, recs).WithCards(cards).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if result.Failed != 0 || len(recs.pending) != 1 || len(cards.posted) != 0 || bills.created != 0 {
		t.Fatalf("result=%+v pending=%v posted=%v bills=%d", result, recs.pending, cards.posted, bills.created)
	}
}

func TestPendingExpectedAmountRemainsInVirtualProjection(t *testing.T) {
	rec := monthly("projected-variable", 10)
	rec.AmountMode = finance.AmountExpected
	bills, recs := newFakeBills(), newFakeRecs(rec)
	jobs := NewFinanceJobs(bills, recs)
	jobs.Materialise(context.Background(), true, day(2026, time.January, 10), time.Now())
	projection, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.January, 10), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := projection.Months[0].VirtualPayable; got != rec.Amount {
		t.Fatalf("pending occurrence disappeared from projection: %d", got)
	}
	if got := projection.Months[0].Payable; got != 0 {
		t.Fatalf("pending estimate counted as booked payable: %d", got)
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
		if got := byMonth[m]; got.VirtualPayable != 150000 || got.VirtualReceivable != 0 || got.Virtual != got.VirtualReceivable-got.VirtualPayable {
			t.Errorf("%s = %+v: the two sides of virtual must add back to the net", m, got)
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

func (f *fakeCards) DueToClosePage(context.Context, bool, brcal.Date, int, map[string]types.AttributeValue) ([]repositories.DueCard, int, map[string]types.AttributeValue, error) {
	return f.due, 0, nil, nil
}

func (f *fakeCards) CloseDue(_ context.Context, _ space.ResolvedSpace, cardID string, _ brcal.Date, _ time.Time) (int, error) {
	if cardID == f.fail {
		return 0, errors.New("dynamodb: throttled")
	}
	f.closed = append(f.closed, cardID)
	return 1, nil
}

func (f *fakeCards) GetCard(context.Context, space.ResolvedSpace, string) (*repositories.CardRow, error) {
	return nil, repositories.ErrNotFound
}

func (f *fakeCards) PostOccurrence(context.Context, space.ResolvedSpace, finance.CardCharge, repositories.PostMeta, time.Time) (bool, repositories.Purchase, error) {
	return false, repositories.Purchase{}, errors.New("not used")
}

func TestCloseStatementsClosesEveryDueCardAndSurvivesOneFailure(t *testing.T) {
	sp := testSpace(t)
	cards := &fakeCards{due: []repositories.DueCard{{Space: sp, CardID: "a"}, {Space: sp, CardID: "b"}, {Space: sp, CardID: "c"}}, fail: "b"}
	res := NewFinanceJobs(newFakeBills(), newFakeRecs()).WithCards(cards).CloseStatements(context.Background(), true, day(2026, time.March, 3), time.Now())
	if res.Examined != 3 || res.Done != 2 || res.Failed != 1 || len(cards.closed) != 2 {
		t.Fatalf("res = %+v, closed = %v", res, cards.closed)
	}
}

func (f *fakeCards) ListCards(context.Context, space.ResolvedSpace) ([]repositories.CardRow, error) {
	return []repositories.CardRow{{Card: finance.Card{ID: "visa", ClosingDay: 3, DueDay: 10, PayingAccountID: "bank",
		OpenMonth: finance.Month{Year: 2026, Month: time.March}}, Name: "Visa"}}, nil
}

func (f *fakeCards) GetStatement(_ context.Context, _ space.ResolvedSpace, _ string, m finance.Month) (repositories.Statement, error) {
	if m.Month == time.March || m.Month == time.April {
		return repositories.Statement{Month: m, Total: 10000}, nil
	}
	return repositories.Statement{Month: m}, nil
}

// The card has to be paid: its open and future statements are payables in the
// month they fall due (closed ones are already bills and counted as such).
func TestProjectCountsCardStatementsOnTheirDueMonth(t *testing.T) {
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs()).WithCards(&fakeCards{})
	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 2), 3) // Mar..May
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{10000, 10000, 0} {
		if got := int64(p.Months[i].Payable); got != want {
			t.Errorf("%s payable = %d, want %d", p.Months[i].Month, got, want)
		}
	}
}

// ---- card recurrences ------------------------------------------------------------

// recCards is a card store for the card branch: one or more cards, purchases
// keyed by occurrence ref (the lock), and switchable failures.
type recCards struct {
	fakeCards
	rows    map[string]repositories.CardRow
	posted  map[string]finance.CardCharge
	postErr error                    // returned by every PostOccurrence while set
	moved   int                      // the next `moved` calls answer ErrCardMoved
	totals  map[string]billing.Cents // "{card}|{month}" -> open/future statement total
}

func newRecCards(rows ...repositories.CardRow) *recCards {
	c := &recCards{rows: map[string]repositories.CardRow{}, posted: map[string]finance.CardCharge{}, totals: map[string]billing.Cents{}}
	for _, r := range rows {
		c.rows[r.ID] = r
	}
	return c
}

func (c *recCards) GetCard(_ context.Context, _ space.ResolvedSpace, id string) (*repositories.CardRow, error) {
	r, ok := c.rows[id]
	if !ok {
		return nil, repositories.ErrNotFound
	}
	return &r, nil
}

func (c *recCards) ListCards(context.Context, space.ResolvedSpace) ([]repositories.CardRow, error) {
	out := make([]repositories.CardRow, 0, len(c.rows))
	for _, r := range c.rows {
		out = append(out, r)
	}
	return out, nil
}

func (c *recCards) GetStatement(_ context.Context, _ space.ResolvedSpace, id string, m finance.Month) (repositories.Statement, error) {
	return repositories.Statement{Month: m, Total: c.totals[id+"|"+m.String()]}, nil
}

func (c *recCards) PostOccurrence(_ context.Context, _ space.ResolvedSpace, ch finance.CardCharge, _ repositories.PostMeta, _ time.Time) (bool, repositories.Purchase, error) {
	if c.moved > 0 {
		c.moved--
		return false, repositories.Purchase{}, repositories.ErrCardMoved
	}
	if c.postErr != nil {
		return false, repositories.Purchase{}, c.postErr
	}
	ref := finance.OccurrenceRef(ch.RecurrenceID, ch.Nominal)
	if _, ok := c.posted[ref]; ok {
		return false, repositories.Purchase{}, nil
	}
	c.posted[ref] = ch
	return true, repositories.Purchase{ID: "p-" + ref}, nil
}

// visa25 closes on the 25th and is due on the 5th; added and open in March 2026.
func visa25() repositories.CardRow {
	return repositories.CardRow{Card: finance.Card{ID: "visa", ClosingDay: 25, DueDay: 5, PayingAccountID: "bank",
		OpenMonth: finance.Month{Year: 2026, Month: time.March}}, Name: "Visa", FirstMonth: finance.Month{Year: 2026, Month: time.March}}
}

func onCard(id string, day int, n int) finance.Recurrence {
	return finance.Recurrence{ID: id, Direction: finance.Payable, Amount: 30000, CategoryID: "subs", CardID: "visa", Installments: n,
		Description: "Streaming", Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: day0(), Adjust: finance.AdjustNone}}
}

func day0() brcal.Date { return day(2026, time.March, 1) }

// Spec § 6.1 (job half; the real tables are Task 6).
func TestTheJobPostsEachCardOccurrenceOnceOnItsDay(t *testing.T) {
	bills, cards, recs := newFakeBills(), newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(bills, recs).WithCards(cards)

	before := jobs.Materialise(context.Background(), true, day(2026, time.March, 27), time.Now())
	if before.Examined != 0 || len(cards.posted) != 0 {
		t.Fatalf("on the 27th = %+v, posted %d: nothing is posted before its day", before, len(cards.posted))
	}
	for run := 0; run < 2; run++ {
		jobs.Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	}
	if len(cards.posted) != 1 {
		t.Fatalf("posted %d, want exactly the 28th once", len(cards.posted))
	}
	if recs.cursors["s"] != day(2026, time.March, 28) {
		t.Fatalf("cursor = %s, want today", recs.cursors["s"])
	}
	if bills.created != 0 {
		t.Fatal("a card recurrence makes no bills")
	}
}

func TestTheJobRetriesALostCloseRaceOnce(t *testing.T) {
	cards, recs := newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	cards.moved = 1
	res := NewFinanceJobs(newFakeBills(), recs).WithCards(cards).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if res.Failed != 0 || len(cards.posted) != 1 {
		t.Fatalf("res = %+v, posted %d: one ErrCardMoved is retried in the run", res, len(cards.posted))
	}
	cards.moved = 2
	recs2 := newFakeRecs(onCard("t", 28, 1))
	res = NewFinanceJobs(newFakeBills(), recs2).WithCards(cards).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if res.Failed != 0 || !recs2.cursors["t"].IsZero() {
		t.Fatalf("res = %+v, cursor %s: a second loss waits for the next run, cursor kept", res, recs2.cursors["t"])
	}
}

// Spec § 6.4 (job half).
func TestABrokenTargetIsFlaggedNotPostedAndTheCursorStays(t *testing.T) {
	archived := visa25()
	archived.Archived = true
	cards, recs := newRecCards(archived), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(newFakeBills(), recs).WithCards(cards)
	today := day(2026, time.March, 28)

	res := jobs.Materialise(context.Background(), true, today, time.Now())
	if res.Failed != 0 || len(cards.posted) != 0 || recs.flags["s"] != finance.AttentionCardArchived || !recs.cursors["s"].IsZero() {
		t.Fatalf("archived card: res %+v posted %d flag %q cursor %s", res, len(cards.posted), recs.flags["s"], recs.cursors["s"])
	}

	cards.rows["visa"] = visa25() // the person picked a working card
	cards.postErr = fmt.Errorf("wrapped: %w", repositories.ErrCategoryUnusable)
	jobs.Materialise(context.Background(), true, today, time.Now())
	if recs.flags["s"] != finance.AttentionCategoryArchived || len(cards.posted) != 0 {
		t.Fatalf("archived category: flag %q posted %d", recs.flags["s"], len(cards.posted))
	}

	cards.postErr = nil
	later := day(2026, time.April, 28)
	jobs.Materialise(context.Background(), true, later, time.Now())
	if len(cards.posted) != 2 || recs.flags["s"] != "" || recs.cursors["s"] != later {
		t.Fatalf("after the fix: posted %d (want Mar 28 and Apr 28), flag %q, cursor %s", len(cards.posted), recs.flags["s"], recs.cursors["s"])
	}
}

func TestAnUnknownCardIsFlaggedAsArchived(t *testing.T) {
	recs := newFakeRecs(onCard("s", 28, 1))
	NewFinanceJobs(newFakeBills(), recs).WithCards(newRecCards()).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if recs.flags["s"] != finance.AttentionCardArchived {
		t.Fatalf("flag = %q", recs.flags["s"])
	}
}

func TestAccountAndCardRecurrencesRunSideBySide(t *testing.T) {
	bills, cards := newFakeBills(), newRecCards(visa25())
	recs := newFakeRecs(monthly("rent", 10), onCard("s", 15, 1))
	jobs := NewFinanceJobs(bills, recs).WithCards(cards)
	jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if bills.created != 4 || len(cards.posted) != 1 {
		t.Fatalf("bills %d (want Jan..Apr), purchases %d (want Mar 15)", bills.created, len(cards.posted))
	}
}
func monthOf(p Projection, m time.Month) ProjectionMonth {
	for _, x := range p.Months {
		if x.Month.Month == m {
			return x
		}
	}
	return ProjectionMonth{}
}

// Spec § 6.2: charged on the 28th, closing on the 25th: next month's
// statement, due on the 5th of the month after. Virtual there while not posted;
// once posted, only Payable (through the statement), never both.
func TestACardOccurrenceIsVirtualInItsStatementsDueMonthThenOnlyPayable(t *testing.T) {
	cards, recs := newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(newFakeBills(), recs).WithCards(cards)

	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 4) // Mar..Jun
	if err != nil {
		t.Fatal(err)
	}
	// Mar 28 -> April statement -> due May 5; Apr 28 -> May -> June 5.
	if got := monthOf(p, time.May); got.VirtualPayable != 30000 || got.Virtual != -30000 || got.Payable != 0 {
		t.Fatalf("May before posting = %+v", got)
	}
	if got := monthOf(p, time.April); got.VirtualPayable != 0 {
		t.Fatalf("April = %+v: nothing is due in the month it is charged", got)
	}

	// The job posts Mar 28 (cursor = Mar 28); the April statement now holds it.
	recs.cursors["s"] = day(2026, time.March, 28)
	cards.totals["visa|2026-04"] = 30000
	p, _ = jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 28), 4)
	if got := monthOf(p, time.May); got.Payable != 30000 || got.VirtualPayable != 0 {
		t.Fatalf("May after posting = %+v: Payable once, no virtual", got)
	}
	if got := monthOf(p, time.June); got.VirtualPayable != 30000 {
		t.Fatalf("June = %+v: the April 28 occurrence is still virtual", got)
	}
}

// Spec § 6.3 (virtual half): 3x spreads over three statements' due months.
func TestAnInstallmentCardOccurrenceSpreadsOverThreeDueMonths(t *testing.T) {
	r := onCard("s", 28, 3)
	r.Schedule.End = day(2026, time.March, 31) // one occurrence only
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs(r)).WithCards(newRecCards(visa25()))
	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 6) // Mar..Aug
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []time.Month{time.May, time.June, time.July} {
		if got := monthOf(p, m); got.VirtualPayable != 10000 {
			t.Errorf("%s = %+v, want one installment (100,00)", m, got)
		}
	}
	if got := monthOf(p, time.August); got.VirtualPayable != 0 {
		t.Errorf("August = %+v", got)
	}
}

func TestACardRecurrenceOnAnArchivedCardProjectsNothing(t *testing.T) {
	archived := visa25()
	archived.Archived = true
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs(onCard("s", 28, 1))).WithCards(newRecCards(archived))
	p, _ := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 4)
	for _, m := range p.Months {
		if m.VirtualPayable != 0 {
			t.Fatalf("%s = %+v: an archived card is charged nothing until the recurrence is retargeted", m.Month, m)
		}
	}
}

// Review Focus 4: a work item read before the purge must not write into the
// space after it.
func TestTheJobLeavesAnErasedSpaceAlone(t *testing.T) {
	sp := testSpace(t)
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	cards := &fakeCards{due: []repositories.DueCard{{Space: sp, CardID: "c1"}}}
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10))}
	asked := 0
	jobs := NewFinanceJobs(bills, recs).WithCards(cards).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) {
		asked++
		return true, nil
	})
	today, now := day(2026, time.March, 20), time.Now()

	m := jobs.Materialise(context.Background(), true, today, now)
	a := jobs.AutoSettle(context.Background(), true, today, now)
	c := jobs.CloseStatements(context.Background(), true, today, now)
	if m.Done != 0 || m.Skipped != 1 || bills.created != 0 || recs.marks != 0 {
		t.Fatalf("materialise = %+v, created %d, cursor moves %d; want the erased space skipped", m, bills.created, recs.marks)
	}
	if a.Done != 0 || a.Skipped != 1 || len(bills.settled) != 0 {
		t.Fatalf("auto-settle = %+v, settled %v; want the erased space skipped", a, bills.settled)
	}
	if c.Done != 0 || c.Skipped != 1 || len(cards.closed) != 0 {
		t.Fatalf("close = %+v, closed %v; want the erased space skipped", c, cards.closed)
	}
	if asked != 3 {
		t.Fatalf("guard asked %d times, want once per item", asked)
	}
}

func TestAGuardErrorFailsTheItemNotTheRun(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10), monthly("r2", 12))
	jobs := NewFinanceJobs(bills, recs).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) {
		return false, errors.New("dynamodb: boom")
	})
	res := jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 2 || res.Done != 0 || bills.created != 0 {
		t.Fatalf("res = %+v; an unknown erasure state is a failure, never a write", res)
	}
}

// ---- MonthFlows ----------------------------------------------------------------

func sumFlows(ls []finance.CategoryFlow) map[string]billing.Cents {
	out := map[string]billing.Cents{}
	for _, l := range ls {
		out[string(l.Direction)+":"+l.CategoryID] += l.Amount
	}
	return out
}

// The month's open bills are scheduled (by competence, never a statement bill);
// the occurrences after the cursor are forecast, card recurrences too, whole and
// on the payable side; the ones the job already made are not counted twice.
func TestMonthFlowsSplitsOpenBillsFromUnmaterialisedOccurrences(t *testing.T) {
	bills := newFakeBills()
	bills.open[finance.Payable] = []finance.Bill{
		{ID: "a", Direction: finance.Payable, Amount: 40000, CategoryID: "rent", Competence: day(2026, time.May, 10), Due: day(2026, time.May, 10)},
		{ID: "other-month", Direction: finance.Payable, Amount: 1, CategoryID: "rent", Competence: day(2026, time.June, 10)},
		{ID: "stmt", Direction: finance.Payable, Amount: 99, CategoryID: "card", Origin: finance.OriginCardStatement, Competence: day(2026, time.May, 5)},
	}
	rent := monthly("rent", 10) // cursor in April: May's occurrence is not a bill yet
	recs := newFakeRecs(rent, onCard("s", 28, 3))
	recs.cursors["rent"] = day(2026, time.April, 10)
	jobs := NewFinanceJobs(bills, recs).WithCards(newRecCards(visa25()))

	open, forecast, err := jobs.MonthFlows(context.Background(), testSpace(t), day(2026, time.March, 20), finance.Month{Year: 2026, Month: time.May})
	if err != nil {
		t.Fatal(err)
	}
	if got := sumFlows(open); len(got) != 1 || got["payable:rent"] != 40000 {
		t.Errorf("open = %v, want only the May bill of rent", got)
	}
	got := sumFlows(forecast)
	if got["payable:rent"] != 150000 || got["payable:subs"] != 30000 || len(got) != 2 {
		t.Errorf("forecast = %v, want rent 150000 and the card recurrence whole (30000, not 3 installments)", got)
	}
}

func TestMonthFlowsRefusesAMonthBeyondTheProjectionHorizon(t *testing.T) {
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs())
	if _, _, err := jobs.MonthFlows(context.Background(), testSpace(t), day(2026, time.March, 20), finance.Month{Year: 2027, Month: time.March}); err == nil {
		t.Fatal("a month 12 ahead was accepted")
	}
}
