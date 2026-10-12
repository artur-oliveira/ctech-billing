package finance

import (
	"testing"
	"time"
)

func TestCardSpendingFoldsByCategoryAndMonth(t *testing.T) {
	mar, apr, may := Month{Year: 2026, Month: time.March}, Month{Year: 2026, Month: time.April}, Month{Year: 2026, Month: time.May}
	lines := []SpendLine{
		{CardID: "visa", CategoryID: "food", Month: mar, Amount: 10000},
		{CardID: "visa", CategoryID: "food", Month: apr, Amount: 10000},
		{CardID: "master", CategoryID: "home", Month: apr, Amount: 30000},
		{CardID: "visa", CategoryID: "food", Month: apr, Amount: -2500},                               // a refund's credit
		{CardID: "visa", CategoryID: "food", Month: Month{Year: 2026, Month: time.June}, Amount: 999}, // outside: ignored
	}
	r := CardSpending(lines, mar, may)
	if len(r.Months) != 3 || r.Total != 47500 {
		t.Fatalf("report = %+v", r)
	}
	// Largest first.
	if len(r.Categories) != 2 || r.Categories[0].CategoryID != "home" || r.Categories[0].Total != 30000 ||
		r.Categories[1].CategoryID != "food" || r.Categories[1].Total != 17500 ||
		r.Categories[1].Amounts[0] != 10000 || r.Categories[1].Amounts[1] != 7500 || r.Categories[1].Amounts[2] != 0 {
		t.Fatalf("categories = %+v", r.Categories)
	}
	if len(r.Cards) != 2 || r.Cards[0].CardID != "master" || r.Cards[0].Total != 30000 || r.Cards[1].Total != 17500 {
		t.Fatalf("cards = %+v", r.Cards)
	}
	if r.ByMonth[0] != 10000 || r.ByMonth[1] != 37500 || r.ByMonth[2] != 0 {
		t.Fatalf("by month = %v", r.ByMonth)
	}
}

func TestCardSpendingOmitsCategoriesThatNetToNothing(t *testing.T) {
	mar := Month{Year: 2026, Month: time.March}
	r := CardSpending([]SpendLine{
		{CardID: "visa", CategoryID: "food", Month: mar, Amount: 10000},
		{CardID: "visa", CategoryID: "food", Month: mar, Amount: -10000},
	}, mar, mar)
	if len(r.Categories) != 0 || r.Total != 0 {
		t.Fatalf("report = %+v", r)
	}
}

// A purchase billed in March and refunded on April's statement nets to zero
// over the range, but both months moved money: the row stays, so the rows add
// up to the total row month by month.
func TestCardSpendingKeepsARowThatNetsToZeroAcrossMonths(t *testing.T) {
	mar, apr := Month{Year: 2026, Month: time.March}, Month{Year: 2026, Month: time.April}
	r := CardSpending([]SpendLine{
		{CardID: "visa", CategoryID: "food", Month: mar, Amount: 10000},
		{CardID: "visa", CategoryID: "food", Month: apr, Amount: -10000},
	}, mar, apr)
	if len(r.Categories) != 1 || r.Categories[0].Amounts[0] != 10000 || r.Categories[0].Amounts[1] != -10000 || len(r.Cards) != 1 {
		t.Fatalf("report = %+v", r)
	}
}
