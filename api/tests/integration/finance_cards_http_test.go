//go:build integration

package integration

import (
	"fmt"
	"testing"
)

// The pinned clock is 2026-03-10, so the card's open month is March 2026.
// A purchase of 300,00 in 3× on Mar 9 bills 100,00 on March; closing March now
// makes a statement bill due Mar 25; paying it moves cash and the card, never
// the DRE, which already holds the whole purchase once.
func TestPayingAStatementLeavesTheDREAlone(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, food, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &card)
	f.must(t, 201, "POST", "/cards/"+card.ID+"/purchases",
		fmt.Sprintf(`{"date":"2026-03-09","description":"Geladeira","category_id":%q,"total":30000,"installments":3}`, food.ID), nil)

	var st struct {
		Status string
		Total  int64
		BillID string `json:"bill_id"`
		Items  []struct{ Number, Of int }
	}
	f.must(t, 200, "POST", "/cards/"+card.ID+"/close", `{"month":"2026-03"}`, &st)
	if st.Status != "closed" || st.Total != 10000 || st.BillID == "" || len(st.Items) != 1 || st.Items[0].Of != 3 {
		t.Fatalf("closed statement = %+v", st)
	}
	if res := f.call(t, "PATCH", "/bills/"+st.BillID, `{"amount":5000}`); res.status != 409 {
		t.Fatalf("editing a statement's amount = %d %s", res.status, res.body)
	}
	if res := f.call(t, "POST", "/bills/"+st.BillID+"/cancel", `{}`); res.status != 409 {
		t.Fatalf("canceling a statement bill = %d %s", res.status, res.body)
	}
	// The paying account of a closed statement can change (the card's did).
	var savings struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Poupança","class":"asset"}`, &savings)
	f.must(t, 200, "PATCH", "/bills/"+st.BillID, fmt.Sprintf(`{"account_id":%q}`, savings.ID), nil)
	f.must(t, 200, "POST", "/bills/"+st.BillID+"/settle", `{"paid_date":"2026-03-10"}`, nil)
	f.must(t, 200, "GET", "/cards/"+card.ID+"/statements/2026-03", "", &st)
	if st.Status != "paid" {
		t.Fatalf("statement after paying its bill = %q", st.Status)
	}

	var dre struct{ Total int64 }
	f.must(t, 200, "GET", "/reports/dre?from=2026-03&to=2026-03", "", &dre)
	if dre.Total != -30000 {
		t.Fatalf("DRE March = %d, want the whole purchase once (-30000)", dre.Total)
	}
	var cf struct {
		Months []struct {
			Out   int64
			Lines []struct {
				CategoryID string `json:"category_id"`
				Amount     int64
			}
		}
	}
	f.must(t, 200, "GET", "/reports/cash-flow?from=2026-03&to=2026-03", "", &cf)
	m := cf.Months[0]
	if m.Out != 10000 || len(m.Lines) != 1 || m.Lines[0].CategoryID != "card:"+card.ID || m.Lines[0].Amount != -10000 {
		t.Fatalf("cash flow March = %+v", m)
	}
}

type cardBrandView struct {
	ID    string
	Brand string
	Last4 string
}

// UX batch 3: a card carries its brand (closed set) and optionally its last four
// digits, set on create and changed or cleared on edit. They live on the card's
// row in the cards table; nothing else about the card moves.
func TestACardKeepsItsBrandAndLastFourDigits(t *testing.T) {
	f := newFinanceEnv(t)
	var bank struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)

	var card cardBrandView
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Nubank","closing_day":3,"due_day":10,"paying_account_id":%q,"brand":"mastercard","last4":"4242"}`, bank.ID), &card)
	if card.Brand != "mastercard" || card.Last4 != "4242" {
		t.Fatalf("created = %+v", card)
	}
	var list struct{ Data []cardBrandView }
	f.must(t, 200, "GET", "/cards", "", &list)
	if len(list.Data) != 1 || list.Data[0].Brand != "mastercard" || list.Data[0].Last4 != "4242" {
		t.Fatalf("listed = %+v", list.Data)
	}

	var edited cardBrandView
	f.must(t, 200, "PATCH", "/cards/"+card.ID, `{"brand":"elo","last4":""}`, &edited)
	if edited.Brand != "elo" || edited.Last4 != "" {
		t.Fatalf("edited = %+v, want elo with no digits", edited)
	}
	list.Data = nil // json keeps a reused element's field when the reply omits it
	f.must(t, 200, "GET", "/cards", "", &list)
	if list.Data[0].Brand != "elo" || list.Data[0].Last4 != "" {
		t.Fatalf("after edit, listed = %+v", list.Data)
	}
	// A PATCH without them leaves them.
	edited = cardBrandView{}
	f.must(t, 200, "PATCH", "/cards/"+card.ID, `{"due_day":12}`, &edited)
	if edited.Brand != "elo" {
		t.Fatalf("a patch of the due day dropped the brand: %+v", edited)
	}
}

func TestACardRefusesAnUnknownBrandAndAnythingButFourDigits(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	for _, c := range []struct{ body, field string }{
		{`"brand":"discover"`, "brand"},
		{`"last4":"12a4"`, "last4"},
		{`"last4":"123"`, "last4"},
	} {
		res := f.call(t, "POST", "/cards", fmt.Sprintf(`{"name":"X","closing_day":3,"due_day":10,"paying_account_id":%q,%s}`, bank.ID, c.body))
		var p struct{ Errors []struct{ Field string } }
		res.decode(t, &p)
		if res.status != 422 || len(p.Errors) != 1 || p.Errors[0].Field != c.field {
			t.Errorf("create with %s = %d %s, want 422 on %s", c.body, res.status, res.body, c.field)
		}
	}
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Y","closing_day":3,"due_day":10,"paying_account_id":%q}`, bank.ID), &card)
	if res := f.call(t, "PATCH", "/cards/"+card.ID, `{"last4":"98765"}`); res.status != 422 {
		t.Errorf("patch last4 98765 = %d %s, want 422", res.status, res.body)
	}
}
