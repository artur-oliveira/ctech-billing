//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// UX batch 5 (spec § 3.5): ending a recurrence and archiving it cancels the
// bills it already made for dates after the new end that are not paid, through
// the ordinary cancel path (one transaction per bill, the recognition
// reversed). A paid one stays: real money moved. The OCCURRENCE# locks stay, so
// the job never makes those dates again.

type endedRecurrence struct {
	ID              string
	End             string
	Archived        bool
	CanceledBillIDs []string `json:"canceled_bill_ids"`
}

// otherPersonalSpace is another person, in their own personal space.
func otherPersonalSpace(t *testing.T, f financeEnv) financeEnv {
	t.Helper()
	return financeEnv{apiEnv: f.apiEnv, token: f.apiEnv.token(t, "user_"+id.New(), "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)}
}

func billStatus(t *testing.T, f financeEnv, billID string) string {
	t.Helper()
	var b struct{ Status string }
	f.must(t, 200, "GET", "/bills/"+billID, "", &b)
	return b.Status
}

func accountBalance(t *testing.T, f financeEnv, name string) int64 {
	t.Helper()
	var accounts struct {
		Data []struct {
			Name    string
			Balance int64
		}
	}
	f.must(t, 200, "GET", "/accounts", "", &accounts)
	for _, a := range accounts.Data {
		if a.Name == name {
			return a.Balance
		}
	}
	t.Fatalf("account %q not listed", name)
	return 0
}

func TestEndingARecurrenceCancelsItsUnpaidBillsAfterTheEnd(t *testing.T) {
	f := newFinanceEnv(t)
	// Bills for 10/01, 10/02, 10/03 and 10/04 (today is 10/03); 10/04 paid early.
	recID, bills, sp := recurrenceThroughHTTP(t, f)
	f.must(t, 200, "POST", "/bills/"+bills["2026-04-10"]+"/settle", `{"paid_date":"2026-03-05"}`, nil)
	recognised := accountBalance(t, f, "Aluguel")

	var saved endedRecurrence
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-02-10","archive":true,"cancel_after_end":true}`, &saved)
	if !saved.Archived || saved.End != "2026-02-10" {
		t.Fatalf("PATCH = %+v, want archived with the end", saved)
	}
	if fmt.Sprint(saved.CanceledBillIDs) != fmt.Sprint([]string{bills["2026-03-10"]}) {
		t.Fatalf("canceled_bill_ids = %v, want only the forecast bill after the end (%s)", saved.CanceledBillIDs, bills["2026-03-10"])
	}
	want := map[string]string{"2026-01-10": "forecast", "2026-02-10": "forecast", "2026-03-10": "canceled", "2026-04-10": "paid"}
	for nominal, status := range want {
		if got := billStatus(t, f, bills[nominal]); got != status {
			t.Errorf("bill of %s is %q, want %q", nominal, got, status)
		}
	}
	// The recognition of the cancelled bill is reversed: three of four remain.
	if got := accountBalance(t, f, "Aluguel"); got*4 != recognised*3 {
		t.Fatalf("Aluguel balance %d after cancelling one of four (was %d): the recognition was not reversed", got, recognised)
	}

	// The job makes nothing again: archived, and the locks are still there.
	for _, day := range []brcal.Date{brcal.FromTime(now()), brcal.New(2026, time.April, 20), brcal.New(2026, time.May, 15)} {
		runJob(t, day)
	}
	if got := madeNominals(t, sp, recID); fmt.Sprint(got) != "[2026-01-10 2026-02-10 2026-03-10 2026-04-10]" {
		t.Fatalf("after the job, made %v: a cancelled date was made again or a new one appeared", got)
	}
	r, err := repositories.NewRecurrenceRepository(testDB, testCfg).Get(context.Background(), sp, recID)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := r.Materialise(brcal.Date{}, brcal.FromTime(now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drafts {
		if d.Nominal.String() != "2026-03-10" {
			continue
		}
		created, _, err := repositories.NewBillRepository(testDB, testCfg).CreateFromOccurrence(context.Background(), sp, d, recID, repositories.PostMeta{Actor: "scheduler"}, now())
		if err != nil || created {
			t.Fatalf("re-making 10/03 = created %v, %v: its OCCURRENCE# lock must refuse it", created, err)
		}
	}
}

// Re-runnable: the same end again (a retry after a failure half-way) answers
// the same state and cancels nothing twice.
func TestEndingARecurrenceAgainCancelsNothingMore(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, _ := recurrenceThroughHTTP(t, f)
	var first, second endedRecurrence
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-02-10","archive":true,"cancel_after_end":true}`, &first)
	recognised := accountBalance(t, f, "Aluguel")
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-02-10","archive":true,"cancel_after_end":true}`, &second)
	got := append([]string(nil), first.CanceledBillIDs...)
	sort.Strings(got)
	wantIDs := []string{bills["2026-03-10"], bills["2026-04-10"]}
	sort.Strings(wantIDs)
	if fmt.Sprint(got) != fmt.Sprint(wantIDs) {
		t.Fatalf("first: canceled %v, want %v", got, wantIDs)
	}
	if len(second.CanceledBillIDs) != 0 || !second.Archived {
		t.Fatalf("second: %+v, want archived and nothing more cancelled", second)
	}
	if after := accountBalance(t, f, "Aluguel"); after != recognised {
		t.Fatalf("Aluguel %d after the retry, was %d: a reversal posted twice", after, recognised)
	}
}

// A bill paid between the confirmation and the cancellation (a stale list) is
// not cancelled: the cancel path re-reads it and its guard refuses a paid bill.
func TestABillPaidAfterTheListIsNotCancelled(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, sp := recurrenceThroughHTTP(t, f)
	repo := repositories.NewBillRepository(testDB, testCfg)
	ctx := context.Background()
	made, err := repo.MadeAfter(ctx, sp, recID, brcal.New(2026, time.February, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 2 {
		t.Fatalf("made after 10/02 = %d bills, want 2", len(made))
	}
	f.must(t, 200, "POST", "/bills/"+bills["2026-04-10"]+"/settle", `{"paid_date":"2026-03-09"}`, nil)
	// One already cancelled by hand, too.
	f.must(t, 200, "POST", "/bills/"+bills["2026-03-10"]+"/cancel", `{}`, nil)

	canceled, err := services.NewFinanceBills(repo).CancelOccurrences(ctx, sp, made, "user", "req", now())
	if err != nil {
		t.Fatalf("CancelOccurrences on a stale list = %v, want it to skip what moved", err)
	}
	if len(canceled) != 0 {
		t.Fatalf("canceled %v, want nothing: one was paid, one already cancelled", canceled)
	}
	if got := billStatus(t, f, bills["2026-04-10"]); got != string(finance.BillPaid) {
		t.Fatalf("the paid bill is %q", got)
	}
}

// Only the resolved space: another space's id is the ordinary 404 and cancels nothing.
func TestEndingARecurrenceFromAnotherSpaceCancelsNothing(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, _ := recurrenceThroughHTTP(t, f)
	other := otherPersonalSpace(t, f)
	if res := other.call(t, "PATCH", "/recurrences/"+recID, `{"end":"2026-02-10","archive":true,"cancel_after_end":true}`); res.status != 404 {
		t.Fatalf("PATCH from another space = %d %s, want 404", res.status, res.body)
	}
	for nominal, billID := range bills {
		if got := billStatus(t, f, billID); got != "forecast" {
			t.Errorf("bill of %s is %q after another space's PATCH", nominal, got)
		}
	}
}

// day25Recurrence is a payable "every 25th" since January, materialised as the
// job would on now() (10/03/2026): bills for 25/01, 25/02, 25/03 and 25/04.
// An end on 31/03 still leaves 25/03 to come, so it does not end the rule.
func day25Recurrence(t *testing.T, f financeEnv) (recID string, bills map[string]string) {
	t.Helper()
	var bank, rent, rec struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	f.must(t, 201, "POST", "/recurrences", `{"direction":"payable","amount":150000,"category_id":"`+rent.ID+`","account_id":"`+bank.ID+`","description":"Aluguel","expression":{"kind":"day_of_month","day":25},"start":"2026-01-01","business_day_adjust":"none"}`, &rec)
	sp := jobSpace(t, "USER#"+f.org.OwnerUserID, true)
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	billRepo := repositories.NewBillRepository(testDB, testCfg)
	ctx := context.Background()
	r, err := recs.Get(ctx, sp, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := r.Materialise(brcal.Date{}, brcal.FromTime(now()))
	if err != nil || len(drafts) != 4 {
		t.Fatalf("drafts = %d, %v", len(drafts), err)
	}
	bills = map[string]string{}
	for _, d := range drafts {
		_, b, err := billRepo.CreateFromOccurrence(ctx, sp, d, rec.ID, repositories.PostMeta{Actor: "scheduler"}, now())
		if err != nil {
			t.Fatal(err)
		}
		bills[d.Nominal.String()] = b.ID
	}
	if err := recs.MarkMaterialised(ctx, sp, rec.ID, drafts[len(drafts)-1].Nominal, now()); err != nil {
		t.Fatal(err)
	}
	return rec.ID, bills
}

// UX batch 5 review (I2): ANY end that leaves unpaid bills after it cancels them,
// not only one that ends the rule — and only once the request confirms it.
func TestAnEarlierEndCancelsTheUnpaidBillsAfterItOnlyWhenConfirmed(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills := day25Recurrence(t, f)

	res := f.call(t, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-31"}`)
	if res.status != 422 || problemCodeOf(t, res) != "end_cancels_bills" {
		t.Fatalf("PATCH end 31/03 = %d %s, want 422 end_cancels_bills", res.status, res.body)
	}
	if got := findRecurrence(t, f, recID); got.End != "" {
		t.Fatalf("an unconfirmed end was saved: %+v", got)
	}
	if got := billStatus(t, f, bills["2026-04-25"]); got != "forecast" {
		t.Fatalf("25/04 is %q before any confirmation", got)
	}

	var saved endedRecurrence
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-31","cancel_after_end":true}`, &saved)
	if saved.Archived || saved.End != "2026-03-31" {
		t.Fatalf("PATCH = %+v, want the end saved and the rule still running", saved)
	}
	if fmt.Sprint(saved.CanceledBillIDs) != fmt.Sprint([]string{bills["2026-04-25"]}) {
		t.Fatalf("canceled_bill_ids = %v, want only 25/04", saved.CanceledBillIDs)
	}
	for nominal, want := range map[string]string{"2026-01-25": "forecast", "2026-02-25": "forecast", "2026-03-25": "forecast", "2026-04-25": "canceled"} {
		if got := billStatus(t, f, bills[nominal]); got != want {
			t.Errorf("bill of %s is %q, want %q", nominal, got, want)
		}
	}
}

// Only unpaid ones ask: an earlier end with nothing but paid bills after it is
// an ordinary edit, and the paid bill stays.
func TestAnEarlierEndWithOnlyPaidBillsAfterItNeedsNoConfirmation(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills := day25Recurrence(t, f)
	f.must(t, 200, "POST", "/bills/"+bills["2026-04-25"]+"/settle", `{"paid_date":"2026-03-09"}`, nil)
	var saved endedRecurrence
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-31"}`, &saved)
	if len(saved.CanceledBillIDs) != 0 || billStatus(t, f, bills["2026-04-25"]) != "paid" {
		t.Fatalf("PATCH = %+v; 25/04 is %s", saved, billStatus(t, f, bills["2026-04-25"]))
	}
}

// I3: a cancellation cancelled once by a concurrent transaction is retried.
func TestCancelOccurrencesRetriesATransactionConflict(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, sp := recurrenceThroughHTTP(t, f)
	made, err := repositories.NewBillRepository(testDB, testCfg).MadeAfter(context.Background(), sp, recID, brcal.New(2026, time.March, 31))
	if err != nil || len(made) != 1 {
		t.Fatalf("made after 31/03 = %d, %v", len(made), err)
	}
	client, fired := conflictOnce("_bills", nil)
	canceled, err := services.NewFinanceBills(repositories.NewBillRepository(client, testCfg)).CancelOccurrences(context.Background(), sp, made, "user", "req", now())
	if err != nil || fmt.Sprint(canceled) != fmt.Sprint([]string{bills["2026-04-10"]}) {
		t.Fatalf("CancelOccurrences after a conflict = %v, %v; want 10/04 cancelled", canceled, err)
	}
	if fired.Load() < 1 {
		t.Fatal("the conflict was never injected")
	}
}

// M2: a forecast bill that still could not be cancelled is an error (the route's
// "encerrada, mas alguns lançamentos não foram cancelados"), never a 200.
func TestCancelOccurrencesFailsWhenAForecastStaysOpen(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, sp := recurrenceThroughHTTP(t, f)
	made, err := repositories.NewBillRepository(testDB, testCfg).MadeAfter(context.Background(), sp, recID, brcal.New(2026, time.March, 31))
	if err != nil || len(made) != 1 {
		t.Fatalf("made after 31/03 = %d, %v", len(made), err)
	}
	client, _ := conflictTimes("_bills", 100, nil)
	_, err = services.NewFinanceBills(repositories.NewBillRepository(client, testCfg)).CancelOccurrences(context.Background(), sp, made, "user", "req", now())
	if !errors.Is(err, services.ErrEndIncomplete) {
		t.Fatalf("CancelOccurrences with the bill still open = %v, want ErrEndIncomplete", err)
	}
	if got := billStatus(t, f, bills["2026-04-10"]); got != "forecast" {
		t.Fatalf("10/04 is %q", got)
	}
}
