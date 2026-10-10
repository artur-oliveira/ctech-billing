//go:build integration

package integration

import (
	"context"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// recurrenceThroughHTTP creates, in the HTTP env's personal space, a payable
// "every 10th" since January and materialises it as the daily job would on
// now() (10/03/2026): bills for 10/01, 10/02, 10/03 and 10/04 (the horizon),
// cursor on 10/04.
func recurrenceThroughHTTP(t *testing.T, f financeEnv) (recID string, bills map[string]string, sp space.ResolvedSpace) {
	t.Helper()
	return recurrenceThroughHTTPWith(t, f, false)
}

func recurrenceThroughHTTPWith(t *testing.T, f financeEnv, autoSettle bool) (recID string, bills map[string]string, sp space.ResolvedSpace) {
	t.Helper()
	auto := ""
	if autoSettle {
		auto = `,"auto_settle":true`
	}
	var bank, rent, rec struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	f.must(t, 201, "POST", "/recurrences", `{"direction":"payable","amount":150000,"category_id":"`+rent.ID+`","account_id":"`+bank.ID+`","description":"Aluguel","expression":{"kind":"day_of_month","day":10},"start":"2026-01-01","business_day_adjust":"none"`+auto+`}`, &rec)

	sp = jobSpace(t, "USER#"+f.org.OwnerUserID, true)
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
	return rec.ID, bills, sp
}

type recurrenceView struct {
	ID       string
	End      string
	Archived bool
}

func findRecurrence(t *testing.T, f financeEnv, id string) recurrenceView {
	t.Helper()
	var list struct{ Data []recurrenceView }
	f.must(t, 200, "GET", "/recurrences", "", &list)
	for _, r := range list.Data {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("recurrence %s not listed", id)
	return recurrenceView{}
}

// UX batch 3. An end date with nothing left to come (no occurrence after today,
// none the job still owes) ends the recurrence. Without the request saying so,
// the API refuses with a coded 422 and saves nothing; with archive:true the end
// and the archive are ONE write. Bills already made stay as they are.
func TestAnEndThatLeavesNothingToComeIsRefusedUnlessTheRequestArchives(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, _ := recurrenceThroughHTTP(t, f)

	// UX batch 5 review: an end with unpaid bills after it asks for that first
	// (end_cancels_bills); confirmed, the end that leaves nothing asks to archive.
	if res := f.call(t, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-10"}`); res.status != 422 || problemCodeOf(t, res) != "end_cancels_bills" {
		t.Fatalf("PATCH end today = %d %s, want 422 end_cancels_bills", res.status, res.body)
	}
	res := f.call(t, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-10","cancel_after_end":true}`)
	if res.status != 422 {
		t.Fatalf("PATCH end today = %d %s, want 422", res.status, res.body)
	}
	var p struct{ Code string }
	res.decode(t, &p)
	if p.Code != "recurrence_would_end" {
		t.Fatalf("code = %q, want recurrence_would_end", p.Code)
	}
	if got := findRecurrence(t, f, recID); got.End != "" || got.Archived {
		t.Fatalf("a refused edit saved something: %+v", got)
	}

	var saved recurrenceView
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-10","archive":true,"cancel_after_end":true}`, &saved)
	if !saved.Archived || saved.End != "2026-03-10" {
		t.Fatalf("PATCH with archive = %+v, want archived with the end saved", saved)
	}
	// UX batch 5: the bills on or before the end stay; the unpaid one after it
	// (10/04) is cancelled (finance_recurrence_end_test.go).
	for nominal, billID := range bills {
		var b struct{ Status string }
		f.must(t, 200, "GET", "/bills/"+billID, "", &b)
		want := "forecast"
		if nominal == "2026-04-10" {
			want = "canceled"
		}
		if b.Status != want {
			t.Errorf("bill of %s is %q after the recurrence ended, want %q", nominal, b.Status, want)
		}
	}
}

// An end that still leaves an occurrence after today is an ordinary edit.
func TestAnEndWithAnOccurrenceStillToComeSavesWithoutArchiving(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTP(t, f)
	var saved recurrenceView
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-05-10"}`, &saved)
	if saved.Archived || saved.End != "2026-05-10" {
		t.Fatalf("PATCH end = %+v, want saved and active", saved)
	}
}

type occurrencesView struct {
	History []struct {
		Nominal    string `json:"nominal"`
		Due        string `json:"due"`
		BillID     string `json:"bill_id"`
		State      string `json:"state"`
		AutoSettle bool   `json:"auto_settle"`
		PaidDate   string `json:"paid_date"`
	} `json:"history"`
	Upcoming []struct {
		Nominal string `json:"nominal"`
		Due     string `json:"due"`
	} `json:"upcoming"`
}

// F4's inline detail: what the recurrence made, each with its state (paid,
// forecast, overdue, skipped = cancelled), and the dates it will make next.
func TestARecurrenceShowsWhatItMadeAndWhatComesNext(t *testing.T) {
	f := newFinanceEnv(t)
	recID, bills, _ := recurrenceThroughHTTP(t, f)
	f.must(t, 200, "POST", "/bills/"+bills["2026-01-10"]+"/settle", `{"paid_date":"2026-01-10"}`, nil)
	f.must(t, 200, "POST", "/bills/"+bills["2026-03-10"]+"/cancel", `{}`, nil)

	var got occurrencesView
	f.must(t, 200, "GET", "/recurrences/"+recID+"/occurrences", "", &got)
	want := []struct{ nominal, state string }{
		{"2026-01-10", "paid"}, {"2026-02-10", "overdue"}, {"2026-03-10", "skipped"}, {"2026-04-10", "forecast"},
	}
	if len(got.History) != len(want) {
		t.Fatalf("history = %+v", got.History)
	}
	for i, w := range want {
		h := got.History[i]
		if h.Nominal != w.nominal || h.State != w.state || h.BillID != bills[w.nominal] {
			t.Errorf("history[%d] = %+v, want %s %s bill %s", i, h, w.nominal, w.state, bills[w.nominal])
		}
	}
	if got.History[0].PaidDate != "2026-01-10" {
		t.Errorf("paid occurrence has paid_date %q", got.History[0].PaidDate)
	}
	if len(got.Upcoming) == 0 || got.Upcoming[0].Nominal != "2026-05-10" {
		t.Fatalf("upcoming = %+v, want it to start after the cursor (10/05)", got.Upcoming)
	}
}

// An archived recurrence has a history and nothing to come.
func TestAnArchivedRecurrenceHasNoUpcomingDates(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTP(t, f)
	f.must(t, 204, "POST", "/recurrences/"+recID+"/archive", `{}`, nil)
	var got occurrencesView
	f.must(t, 200, "GET", "/recurrences/"+recID+"/occurrences", "", &got)
	if len(got.Upcoming) != 0 || len(got.History) != 4 {
		t.Fatalf("archived: upcoming %d, history %d", len(got.Upcoming), len(got.History))
	}
}

// No cross-space leak: another person's space (their own personal one) does not
// know the id, and answers the ordinary 404 on both new behaviours.
func TestARecurrenceFromAnotherSpaceIsNotFound(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTP(t, f)
	other := financeEnv{apiEnv: f.apiEnv, token: f.apiEnv.token(t, "user_"+id.New(), "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)}
	if res := other.call(t, "GET", "/recurrences/"+recID+"/occurrences", ""); res.status != 404 {
		t.Fatalf("GET occurrences from another space = %d %s, want 404", res.status, res.body)
	}
	if res := other.call(t, "PATCH", "/recurrences/"+recID, `{"end":"2026-03-10","archive":true}`); res.status != 404 {
		t.Fatalf("PATCH from another space = %d %s, want 404", res.status, res.body)
	}
	if got := findRecurrence(t, f, recID); got.Archived {
		t.Fatal("a PATCH from another space archived the recurrence")
	}
}

// Review fix: the console warns, before an end closes a recurrence, which bills
// already made keep going and whether they will still be paid automatically.
// It needs each made bill's own auto-settle flag (copied when it was made).
func TestARecurrencesMadeBillsSayWhetherTheyAutoSettle(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTPWith(t, f, true)
	var got occurrencesView
	f.must(t, 200, "GET", "/recurrences/"+recID+"/occurrences", "", &got)
	if len(got.History) == 0 {
		t.Fatal("no history")
	}
	for _, h := range got.History {
		if !h.AutoSettle {
			t.Errorf("bill of %s: auto_settle false, want true", h.Nominal)
		}
	}
}
