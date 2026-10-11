//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

type renamedAccount struct {
	ID       string
	Name     string
	Class    string
	Archived bool
	Balance  int64
}

func (f financeEnv) accountByID(t *testing.T, id string) renamedAccount {
	t.Helper()
	var list struct{ Data []renamedAccount }
	f.must(t, 200, "GET", "/accounts", "", &list)
	for _, a := range list.Data {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("account %s not listed", id)
	return renamedAccount{}
}

// PATCH /accounts/:id renames an account or category the person holds; only the
// name moves: class, balance and entries stay.
func TestAnAccountIsRenamedAndKeepsItsMoney(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, food struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/accounts/"+bank.ID+"/opening-balance", `{"amount":12345,"date":"2026-03-01"}`, nil)

	var got renamedAccount
	f.must(t, 200, "PATCH", "/accounts/"+bank.ID, `{"name":"  Banco do Brasil  "}`, &got)
	if got.ID != bank.ID || got.Name != "Banco do Brasil" || got.Class != "asset" || got.Balance != 12345 {
		t.Fatalf("rename reply = %+v", got)
	}
	if listed := f.accountByID(t, bank.ID); listed.Name != "Banco do Brasil" || listed.Balance != 12345 {
		t.Fatalf("listed after rename = %+v", listed)
	}
	var st struct{ Entries []any }
	f.must(t, 200, "GET", "/accounts/"+bank.ID+"/statement?from=2026-03-01&to=2026-03-31", "", &st)
	if len(st.Entries) != 1 {
		t.Fatalf("statement after rename has %d entries, want the opening balance", len(st.Entries))
	}
	f.must(t, 200, "PATCH", "/accounts/"+food.ID, `{"name":"Supermercado"}`, &got)
	if got.Name != "Supermercado" || got.Class != "expense" {
		t.Fatalf("category rename = %+v", got)
	}
}

// The limit is 80 characters, as the UI counts them: 80 accented letters are a
// valid name on create and on rename, and 81 is the field's too_long.
func TestANameOfEightyAccentedCharactersIsAccepted(t *testing.T) {
	f := newFinanceEnv(t)
	long := strings.Repeat("é", 80)
	var bank renamedAccount
	f.must(t, 201, "POST", "/accounts", `{"name":"`+long+`","class":"asset"}`, &bank)
	f.must(t, 200, "PATCH", "/accounts/"+bank.ID, `{"name":"`+strings.Repeat("ã", 80)+`"}`, nil)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":%q,"closing_day":3,"due_day":10,"paying_account_id":%q}`, long, bank.ID), &bank)
	res := f.call(t, "PATCH", "/accounts/"+bank.ID, `{"name":"`+strings.Repeat("ã", 81)+`"}`)
	if res.status != 422 || !strings.Contains(string(res.body), "too_long") {
		t.Errorf("81 characters = %d %s, want 422 too_long", res.status, res.body)
	}
}

func TestRenamingRefusesSystemArchivedUnknownAndInvalid(t *testing.T) {
	f := newFinanceEnv(t)
	var bank struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)

	if res := f.call(t, "PATCH", "/accounts/sys-payables", `{"name":"Minhas contas"}`); res.status != 422 || !strings.Contains(string(res.body), "invalid_account") {
		t.Errorf("renaming a system account = %d %s, want 422 invalid_account", res.status, res.body)
	}
	if res := f.call(t, "PATCH", "/accounts/nope", `{"name":"X"}`); res.status != 404 {
		t.Errorf("renaming an unknown account = %d %s, want 404", res.status, res.body)
	}
	if res := f.call(t, "PATCH", "/accounts/"+bank.ID, `{"name":""}`); res.status != 422 {
		t.Errorf("renaming to empty = %d %s, want 422", res.status, res.body)
	}
	if res := f.call(t, "PATCH", "/accounts/"+bank.ID, `{"name":"X","class":"income"}`); res.status != 400 {
		t.Errorf("a rename that also sends class = %d %s, want 400", res.status, res.body)
	}
	f.must(t, 204, "POST", "/accounts/"+bank.ID+"/archive", `{}`, nil)
	if res := f.call(t, "PATCH", "/accounts/"+bank.ID, `{"name":"Outro"}`); res.status != 422 || !strings.Contains(string(res.body), "invalid_account") {
		t.Errorf("renaming an archived account = %d %s, want 422", res.status, res.body)
	}
	if a := f.accountByID(t, bank.ID); a.Name != "Banco" {
		t.Errorf("a refused rename changed the name to %q", a.Name)
	}
	if a := f.accountByID(t, "sys-payables"); a.Name != "Contas a pagar" {
		t.Errorf("a refused rename changed the system account's name to %q", a.Name)
	}
}

// A card's name is its liability account's: PATCH /cards/:id renames it, and
// the account and the card read the same new name.
func TestACardIsRenamedThroughItsPatch(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":3,"due_day":10,"paying_account_id":%q,"brand":"visa"}`, bank.ID), &card)

	var edited struct {
		Name   string
		DueDay int `json:"due_day"`
		Brand  string
	}
	f.must(t, 200, "PATCH", "/cards/"+card.ID, `{"name":" Visa Infinite ","due_day":12}`, &edited)
	if edited.Name != "Visa Infinite" || edited.DueDay != 12 || edited.Brand != "visa" {
		t.Fatalf("edited = %+v", edited)
	}
	var list struct{ Data []struct{ ID, Name string } }
	f.must(t, 200, "GET", "/cards", "", &list)
	if len(list.Data) != 1 || list.Data[0].Name != "Visa Infinite" {
		t.Fatalf("cards after rename = %+v", list.Data)
	}
	if a := f.accountByID(t, card.ID); a.Name != "Visa Infinite" {
		t.Fatalf("the card's account reads %q", a.Name)
	}
	// A patch without a name keeps it.
	f.must(t, 200, "PATCH", "/cards/"+card.ID, `{"closing_day":4}`, &edited)
	if edited.Name != "Visa Infinite" {
		t.Fatalf("a patch without name dropped it: %+v", edited)
	}
	// The same row through PATCH /accounts.
	f.must(t, 200, "PATCH", "/accounts/"+card.ID, `{"name":"Visa"}`, nil)
	f.must(t, 200, "GET", "/cards", "", &list)
	if list.Data[0].Name != "Visa" {
		t.Fatalf("card after renaming its account = %+v", list.Data)
	}
	if res := f.call(t, "PATCH", "/cards/"+card.ID, `{"name":""}`); res.status != 422 {
		t.Errorf("renaming a card to empty = %d %s, want 422", res.status, res.body)
	}
	f.must(t, 204, "POST", "/accounts/"+card.ID+"/archive", `{}`, nil)
	if res := f.call(t, "PATCH", "/cards/"+card.ID, `{"name":"Outro"}`); res.status != 422 || !strings.Contains(string(res.body), "invalid_account") {
		t.Errorf("renaming an archived card = %d %s, want 422", res.status, res.body)
	}
	if a := f.accountByID(t, card.ID); a.Name != "Visa" {
		t.Errorf("a refused card rename changed the name to %q", a.Name)
	}
}
