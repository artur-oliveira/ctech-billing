//go:build integration

package integration

import (
	"fmt"
	"testing"
)

// The pinned clock is 2026-03-10.
func TestARecurrenceIsPaidByCardOverHTTP(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, subs, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Assinaturas","class":"expense","dre_group":"operating_expenses"}`, &subs)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":25,"due_day":5,"paying_account_id":%q}`, bank.ID), &card)
	rule := `"expression":{"kind":"day_of_month","day":28},"start":"2026-03-01"`

	var rec struct {
		ID           string
		AccountID    string `json:"account_id"`
		CardID       string `json:"card_id"`
		Installments int
	}
	f.must(t, 201, "POST", "/recurrences", fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,%s}`, subs.ID, card.ID, rule), &rec)
	if rec.CardID != card.ID || rec.AccountID != "" || rec.Installments != 1 {
		t.Fatalf("created = %+v (installments default to 1)", rec)
	}

	for name, body := range map[string]string{
		"both targets":            fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"account_id":%q,%s}`, subs.ID, card.ID, bank.ID, rule),
		"no target":               fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,%s}`, subs.ID, rule),
		"receivable":              fmt.Sprintf(`{"direction":"receivable","amount":3990,"category_id":%q,"card_id":%q,%s}`, subs.ID, card.ID, rule),
		"auto settle":             fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"auto_settle":true,%s}`, subs.ID, card.ID, rule),
		"49 installments":         fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"installments":49,%s}`, subs.ID, card.ID, rule),
		"tiny installments":       fmt.Sprintf(`{"direction":"payable","amount":2,"category_id":%q,"card_id":%q,"installments":3,%s}`, subs.ID, card.ID, rule),
		"installments on account": fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"account_id":%q,"installments":2,%s}`, subs.ID, bank.ID, rule),
	} {
		if res := f.call(t, "POST", "/recurrences", body); res.status != 422 {
			t.Errorf("%s = %d %s, want 422", name, res.status, res.body)
		}
	}

	f.must(t, 200, "PATCH", "/recurrences/"+rec.ID, `{"installments":3}`, &rec)
	if rec.Installments != 3 {
		t.Fatalf("patched = %+v", rec)
	}
	if res := f.call(t, "PATCH", "/recurrences/"+rec.ID, `{"card_id":null}`); res.status != 422 {
		t.Fatalf("card_id null = %d, want 422 required", res.status)
	}

	var occ struct {
		Upcoming []struct {
			Nominal        string
			StatementMonth string `json:"statement_month"`
		}
	}
	f.must(t, 200, "GET", "/recurrences/"+rec.ID+"/occurrences", "", &occ)
	if len(occ.Upcoming) == 0 || occ.Upcoming[0].Nominal != "2026-03-28" || occ.Upcoming[0].StatementMonth != "2026-04" {
		t.Fatalf("upcoming = %+v: the 28th lands on April (closing day 25)", occ.Upcoming)
	}

	id := rec.ID
	rec = struct {
		ID           string
		AccountID    string `json:"account_id"`
		CardID       string `json:"card_id"`
		Installments int
	}{} // card_id and installments are omitted once on an account: decode into a fresh value
	f.must(t, 200, "PATCH", "/recurrences/"+id, fmt.Sprintf(`{"account_id":%q}`, bank.ID), &rec)
	if rec.AccountID != bank.ID || rec.CardID != "" || rec.Installments != 0 {
		t.Fatalf("switched back = %+v", rec)
	}
}
