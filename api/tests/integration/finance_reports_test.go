//go:build integration

package integration

import (
	"fmt"
	"testing"
)

type reportsFixture struct {
	bank, cash, rent, bill string
}

// seedReports: bank with an opening of 1.000,00 on Mar 1, a payable of 300,00
// (rent) settled Mar 10, a transfer of 100,00 bank to cash on Mar 12.
func seedReports(t *testing.T, f financeEnv) reportsFixture {
	t.Helper()
	var fx reportsFixture
	var acc struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &acc)
	fx.bank = acc.ID
	f.must(t, 201, "POST", "/accounts", `{"name":"Caixa","class":"asset"}`, &acc)
	fx.cash = acc.ID
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &acc)
	fx.rent = acc.ID
	f.must(t, 201, "POST", "/accounts/"+fx.bank+"/opening-balance", `{"amount":100000,"date":"2026-03-01"}`, nil)
	var bill struct{ ID string }
	f.must(t, 201, "POST", "/bills", fmt.Sprintf(`{"direction":"payable","amount":30000,"account_id":%q,"category_id":%q,"description":"Aluguel","due_date":"2026-03-10"}`, fx.bank, fx.rent), &bill)
	fx.bill = bill.ID
	f.must(t, 200, "POST", "/bills/"+fx.bill+"/settle", `{"paid_date":"2026-03-10"}`, nil)
	f.must(t, 201, "POST", "/transfers", fmt.Sprintf(`{"from_account_id":%q,"to_account_id":%q,"amount":10000,"date":"2026-03-12"}`, fx.bank, fx.cash), nil)
	return fx
}

type statementBody struct {
	Opening, Closing int64
	Entries          []struct {
		TransactionID string `json:"transaction_id"`
		Amount        int64
		Balance       int64
		Kind          string
		Memo          string
		CategoryID    string `json:"category_id"`
		BillID        string `json:"bill_id"`
	}
}

func TestCashFlowEndpointReconcilesAgainstBalances(t *testing.T) {
	f := newFinanceEnv(t)
	fx := seedReports(t, f)
	var cf struct {
		OpeningCash int64 `json:"opening_cash"`
		ClosingCash int64 `json:"closing_cash"`
		Months      []struct {
			Month             string
			In, Out, Openings int64
			Lines             []struct {
				CategoryID string `json:"category_id"`
				Amount     int64
			}
		}
	}
	f.must(t, 200, "GET", "/reports/cash-flow?from=2026-03&to=2026-03", "", &cf)
	m := cf.Months[0]
	if cf.OpeningCash != 0 || m.Openings != 100000 || m.Out != 30000 || m.In != 0 || cf.ClosingCash != 70000 {
		t.Fatalf("cash flow = %+v", cf)
	}
	if len(m.Lines) != 1 || m.Lines[0].CategoryID != fx.rent || m.Lines[0].Amount != -30000 {
		t.Fatalf("lines = %+v", m.Lines)
	}
	var accounts struct {
		Data []struct {
			ID      string
			Balance int64
		}
	}
	f.must(t, 200, "GET", "/accounts", "", &accounts)
	var cash int64
	for _, a := range accounts.Data {
		if a.ID == fx.bank || a.ID == fx.cash {
			cash += a.Balance
		}
	}
	if cash != cf.ClosingCash {
		t.Fatalf("closing %d != balances %d", cf.ClosingCash, cash)
	}

	var dre struct {
		Total  int64
		Groups []struct{ Group string }
	}
	f.must(t, 200, "GET", "/reports/dre?from=2026-03&to=2026-03", "", &dre)
	if dre.Total != -30000 || len(dre.Groups) != 1 || dre.Groups[0].Group != "operating_expenses" {
		t.Fatalf("dre = %+v (the transfer and the opening are not results)", dre)
	}
}

func TestStatementPeriodBeyondTheEntries(t *testing.T) {
	f := newFinanceEnv(t)
	fx := seedReports(t, f)
	var s statementBody
	f.must(t, 200, "GET", "/accounts/"+fx.bank+"/statement?from=2026-01-01&to=2026-02-01", "", &s)
	if s.Opening != 0 || s.Closing != 0 || len(s.Entries) != 0 {
		t.Fatalf("early statement = %+v", s)
	}
	f.must(t, 200, "GET", "/accounts/"+fx.bank+"/statement?from=2026-03-01&to=2027-03-01", "", &s)
	if s.Opening != 0 || s.Closing != 60000 || len(s.Entries) != 3 {
		t.Fatalf("statement = %+v", s)
	}
	for i, want := range []int64{100000, 70000, 60000} {
		if s.Entries[i].Balance != want {
			t.Fatalf("entry %d balance = %d, want %d", i, s.Entries[i].Balance, want)
		}
	}
	if pay := s.Entries[1]; pay.Kind != "settlement" || pay.CategoryID != fx.rent || pay.BillID != fx.bill || pay.Memo != "Aluguel" {
		t.Fatalf("payment line = %+v", pay)
	}
	if f.call(t, "GET", "/accounts/"+fx.rent+"/statement?from=2026-03-01&to=2026-04-01", "").status != 404 {
		t.Fatal("a category has no statement")
	}
	if f.call(t, "GET", "/accounts/"+fx.bank+"/statement?from=2026-01-01&to=2027-01-03", "").status != 422 {
		t.Fatal("a period over 366 days was accepted")
	}
}

func TestReversingASettlementFromTheStatementIsRefused(t *testing.T) {
	f := newFinanceEnv(t)
	fx := seedReports(t, f)
	var s statementBody
	f.must(t, 200, "GET", "/accounts/"+fx.bank+"/statement?from=2026-03-01&to=2026-04-01", "", &s)
	settlement, transfer := s.Entries[1].TransactionID, s.Entries[2].TransactionID
	if res := f.call(t, "POST", "/transactions/"+settlement+"/reverse", `{}`); res.status != 409 {
		t.Fatalf("reverse settlement = %d %s", res.status, res.body)
	}
	f.must(t, 201, "POST", "/transactions/"+transfer+"/reverse", `{}`, nil)
	if res := f.call(t, "POST", "/accounts/"+fx.bank+"/opening-balance", `{"amount":5,"date":"2026-03-01"}`); res.status != 409 {
		t.Fatalf("second opening = %d %s", res.status, res.body)
	}
	var bill struct {
		Status     string
		AutoSettle bool `json:"auto_settle"`
	}
	f.must(t, 200, "POST", "/bills/"+fx.bill+"/unsettle", `{}`, &bill)
	if bill.Status != "forecast" || bill.AutoSettle {
		t.Fatalf("unsettled bill = %+v", bill)
	}
	f.must(t, 200, "GET", "/accounts/"+fx.bank+"/statement?from=2026-03-01&to=2026-04-01", "", &s)
	if s.Closing != 100000 {
		t.Fatalf("bank after undoing the payment and the transfer = %d, want 100000", s.Closing)
	}
}
