package finance

import (
	"testing"
	"time"
)

func TestCategoryMonthSplitsRealisedScheduledAndForecast(t *testing.T) {
	m := Month{Year: 2026, Month: time.March}
	accounts := map[string]CategoryInfo{"rent": {Class: ClassExpense}, "food": {Class: ClassExpense}, "pay": {Class: ClassIncome}, "bank": {Class: ClassAsset}}
	sums := map[SummaryKey]Totals{
		{Month: m, AccountID: "rent"}:        {Debits: 150000},
		{Month: m, AccountID: "food"}:        {Debits: 30000, Credits: 5000}, // a refund nets it
		{Month: m, AccountID: "pay"}:         {Credits: 500000},
		{Month: m, AccountID: "bank"}:        {Debits: 999}, // not a category
		{Month: m.Add(1), AccountID: "rent"}: {Debits: 777}, // another month
	}
	r := CategoryMonth(m, sums, accounts,
		[]CategoryFlow{{"rent", Payable, 150000}, {"pay", Receivable, 100000}},
		[]CategoryFlow{{"food", Payable, 20000}, {"rent", Payable, 1}, {"gym", Payable, 9000}, {"pay", Receivable, 50000}})

	if len(r.Expense) != 3 || r.Expense[0].CategoryID != "rent" {
		t.Fatalf("expense = %+v, want rent first of 3", r.Expense)
	}
	rent := r.Expense[0]
	if rent.Realised != 0 || rent.Scheduled != 150000 || rent.Forecast != 1 || rent.Total != 150001 {
		t.Errorf("rent = %+v: the open bill is scheduled, not realised, and counted once", rent)
	}
	if food := r.Expense[1]; food.CategoryID != "food" || food.Realised != 25000 || food.Forecast != 20000 || food.Total != 45000 {
		t.Errorf("food = %+v", food)
	}
	if gym := r.Expense[2]; gym.CategoryID != "gym" || gym.Forecast != 9000 || gym.Total != 9000 {
		t.Errorf("gym = %+v: a forecast alone is a row", gym)
	}
	if pay := r.Income[0]; pay.Realised != 400000 || pay.Scheduled != 100000 || pay.Forecast != 50000 || pay.Total != 550000 {
		t.Errorf("pay = %+v", pay)
	}
	if r.ExpenseTotal.Total != 150001+45000+9000 || r.IncomeTotal.Total != 550000 {
		t.Errorf("totals = %+v / %+v", r.ExpenseTotal, r.IncomeTotal)
	}
}

func TestCategoryMonthOfNothingIsEmpty(t *testing.T) {
	r := CategoryMonth(Month{Year: 2026, Month: time.March}, nil, nil, nil, nil)
	if len(r.Income) != 0 || len(r.Expense) != 0 || r.ExpenseTotal.Total != 0 {
		t.Fatalf("%+v", r)
	}
}
