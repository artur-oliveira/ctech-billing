//go:build integration

package integration

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// UX batch 4: a PATCH clears an optional field with an explicit null; an absent
// field keeps it; null on a required field is a 422 naming it.

// madeNominals is every occurrence the recurrence has made, oldest first.
func madeNominals(t *testing.T, sp space.ResolvedSpace, recID string) []string {
	t.Helper()
	made, err := repositories.NewBillRepository(testDB, testCfg).ForRecurrence(context.Background(), sp, recID, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(made))
	for i, m := range made {
		out[i] = m.Nominal.String()
	}
	sort.Strings(out)
	return out
}

func runMaterialise(t *testing.T, day brcal.Date) {
	t.Helper()
	job := services.NewFinanceJobs(repositories.NewBillRepository(testDB, testCfg), repositories.NewRecurrenceRepository(testDB, testCfg))
	at := time.Date(day.Year, day.Month, day.Day, 12, 0, 0, 0, time.UTC)
	_ = job.Materialise(context.Background(), true, day, at)
}

// The bug the user hit: an end date, once saved, could not be removed. Clearing
// it re-opens the rule: the job finds it again and makes the dates after the
// old end, each exactly once, however often it runs.
func TestClearingARecurrencesEndReopensItWithoutDuplicates(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, sp := recurrenceThroughHTTP(t, f) // Jan..Apr made, cursor 10/04, today 10/03

	var saved recurrenceView
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-04-30"}`, &saved)
	if saved.End != "2026-04-30" {
		t.Fatalf("end = %q", saved.End)
	}
	// Ended at April: the job makes nothing for May.
	runMaterialise(t, brcal.New(2026, time.April, 15))
	if got := madeNominals(t, sp, recID); len(got) != 4 {
		t.Fatalf("with an end, made %v, want only Jan..Apr", got)
	}

	var cleared map[string]any
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":null}`, &cleared)
	if _, has := cleared["end"]; has {
		t.Fatalf("PATCH end:null answered %v, want no end", cleared)
	}
	if got := findRecurrence(t, f, recID); got.End != "" || got.Archived {
		t.Fatalf("after clearing, listed %+v, want no end and active", got)
	}

	for run := 0; run < 3; run++ {
		runMaterialise(t, brcal.New(2026, time.April, 15))
	}
	want := []string{"2026-01-10", "2026-02-10", "2026-03-10", "2026-04-10", "2026-05-10"}
	if got := madeNominals(t, sp, recID); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("re-opened, made %v, want %v (each once)", got, want)
	}
	runMaterialise(t, brcal.New(2026, time.May, 15))
	if got := madeNominals(t, sp, recID); len(got) != 6 || got[5] != "2026-06-10" {
		t.Fatalf("a month later, made %v, want June added", got)
	}
}

func TestARecurrencePatchWithoutEndKeepsItAndNullOnARequiredFieldIs422(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTP(t, f)
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-05-10"}`, nil)
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"description":"Aluguel novo"}`, nil)
	if got := findRecurrence(t, f, recID); got.End != "2026-05-10" {
		t.Fatalf("a PATCH without end changed it to %q", got.End)
	}
	var desc struct{ Description string }
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"description":null}`, &desc)
	if desc.Description != "" {
		t.Fatalf("description:null left %q", desc.Description)
	}
	for _, field := range []string{"amount", "category_id", "account_id", "auto_settle"} {
		res := f.call(t, "PATCH", "/recurrences/"+recID, `{"`+field+`":null}`)
		var p struct {
			Errors []struct{ Field, Code string }
		}
		res.decode(t, &p)
		if res.status != 422 || len(p.Errors) != 1 || p.Errors[0].Field != field || p.Errors[0].Code != "required" {
			t.Errorf("%s:null = %d %s, want 422 required on it", field, res.status, res.body)
		}
	}
}

func TestABillsDescriptionIsClearedWithNullAndARequiredFieldIsNot(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, rent, bill struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	f.must(t, 201, "POST", "/bills", `{"direction":"payable","amount":1000,"account_id":"`+bank.ID+`","category_id":"`+rent.ID+`","description":"Luz","due_date":"2026-03-20"}`, &bill)
	f.must(t, 200, "PATCH", "/bills/"+bill.ID, `{"description":null}`, nil)
	var got map[string]any
	f.must(t, 200, "GET", "/bills/"+bill.ID, "", &got)
	if d, has := got["description"]; has && d != "" {
		t.Fatalf("description after null = %v", d)
	}
	for _, field := range []string{"amount", "due_date", "category_id", "account_id", "auto_settle"} {
		if res := f.call(t, "PATCH", "/bills/"+bill.ID, `{"`+field+`":null}`); res.status != 422 {
			t.Errorf("bill %s:null = %d %s, want 422", field, res.status, res.body)
		}
	}
}

func TestACardsBrandAndDigitsAreClearedWithNull(t *testing.T) {
	f := newFinanceEnv(t)
	var bank struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	var card cardBrandView
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Nubank","closing_day":3,"due_day":10,"paying_account_id":%q,"brand":"visa","last4":"1234"}`, bank.ID), &card)
	f.must(t, 200, "PATCH", "/cards/"+card.ID, `{"brand":null,"last4":null}`, nil)
	var list struct{ Data []cardBrandView }
	f.must(t, 200, "GET", "/cards", "", &list)
	if list.Data[0].Brand != "" || list.Data[0].Last4 != "" {
		t.Fatalf("after null, listed %+v", list.Data[0])
	}
	for _, field := range []string{"closing_day", "due_day", "paying_account_id"} {
		if res := f.call(t, "PATCH", "/cards/"+card.ID, `{"`+field+`":null}`); res.status != 422 {
			t.Errorf("card %s:null = %d %s, want 422", field, res.status, res.body)
		}
	}
}

func TestTheDefaultReceivingAccountIsClearedWithNull(t *testing.T) {
	f := newFinanceEnv(t)
	var bank struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 200, "PUT", "/settings/default-receiving-account", `{"default_receiving_account_id":"`+bank.ID+`"}`, nil)
	f.must(t, 200, "PUT", "/settings/default-receiving-account", `{"default_receiving_account_id":null}`, nil)
	var s map[string]any
	f.must(t, 200, "GET", "/settings", "", &s)
	if v, has := s["default_receiving_account_id"]; has && v != "" {
		t.Fatalf("after null, settings = %v", s)
	}
	if res := f.call(t, "PUT", "/settings/default-receiving-account", `{}`); res.status != 422 {
		t.Fatalf("an empty body = %d %s, want 422", res.status, res.body)
	}
}

// Clearing resolves inside the space like every edit: another person's null is
// the ordinary 404 and changes nothing.
func TestClearingAnotherSpacesEndIsNotFound(t *testing.T) {
	f := newFinanceEnv(t)
	recID, _, _ := recurrenceThroughHTTP(t, f)
	f.must(t, 200, "PATCH", "/recurrences/"+recID, `{"end":"2026-05-10"}`, nil)
	other := financeEnv{apiEnv: f.apiEnv, token: f.apiEnv.token(t, "user_"+id.New(), "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)}
	if res := other.call(t, "PATCH", "/recurrences/"+recID, `{"end":null}`); res.status != 404 {
		t.Fatalf("PATCH end:null from another space = %d %s, want 404", res.status, res.body)
	}
	if got := findRecurrence(t, f, recID); got.End != "2026-05-10" {
		t.Fatalf("another space's null cleared the end: %+v", got)
	}
}
