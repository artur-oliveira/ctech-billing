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
