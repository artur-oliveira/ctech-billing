package finance

import (
	"cmp"
	"slices"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// CategoryFlow is one amount of one category on one side: an open bill's, or a
// recurrence occurrence nobody has materialised yet.
type CategoryFlow struct {
	CategoryID string
	Direction  Direction
	Amount     billing.Cents // > 0
}

// CategoryAmounts is one category's month, split by how firm it is. Total is
// the three added up: what the month is expected to close at.
type CategoryAmounts struct {
	CategoryID string
	// Realised is recognised in the ledger and not waiting on a settlement:
	// settled bills, card purchases, imported lines. Scheduled is an open bill
	// (recognised too, but not paid). Forecast is a recurrence occurrence the
	// job has not turned into a bill or purchase yet.
	Realised, Scheduled, Forecast, Total billing.Cents
}

// CategoryMonthReport is one month by category, income and expense apart, each
// side largest first. Amounts are magnitudes (never negative for a normal
// month): an expense of R$ 10 is +1000 under Expense.
type CategoryMonthReport struct {
	Month        Month
	Income       []CategoryAmounts
	Expense      []CategoryAmounts
	IncomeTotal  CategoryAmounts // CategoryID ""
	ExpenseTotal CategoryAmounts
}

// CategoryMonth folds one month. The ledger's summaries already hold every bill
// at its competence month, paid or not (creating a bill recognises it), so the
// open bills are carved out of them as Scheduled rather than added again;
// Forecast is what the ledger does not know yet. `open` must be the open bills
// competent in this month, `forecast` the unmaterialised occurrences nominal in
// it.
func CategoryMonth(month Month, summaries map[SummaryKey]Totals, accounts map[string]CategoryInfo, open, forecast []CategoryFlow) CategoryMonthReport {
	income, expense := map[string]*CategoryAmounts{}, map[string]*CategoryAmounts{}
	row := func(side map[string]*CategoryAmounts, id string) *CategoryAmounts {
		if side[id] == nil {
			side[id] = &CategoryAmounts{CategoryID: id}
		}
		return side[id]
	}
	for k, t := range summaries {
		info, ok := accounts[k.AccountID]
		if !ok || k.Month != month {
			continue
		}
		switch info.Class {
		case ClassIncome:
			row(income, k.AccountID).Realised += t.Credits - t.Debits
		case ClassExpense:
			row(expense, k.AccountID).Realised += t.Debits - t.Credits
		}
	}
	side := func(d Direction) map[string]*CategoryAmounts {
		if d == Receivable {
			return income
		}
		return expense
	}
	for _, l := range open {
		r := row(side(l.Direction), l.CategoryID)
		r.Scheduled += l.Amount
		r.Realised -= l.Amount // it is in the ledger's figure; it is not realised yet
	}
	for _, l := range forecast {
		row(side(l.Direction), l.CategoryID).Forecast += l.Amount
	}
	fold := func(m map[string]*CategoryAmounts) ([]CategoryAmounts, CategoryAmounts) {
		var total CategoryAmounts
		out := make([]CategoryAmounts, 0, len(m))
		for _, r := range m {
			r.Total = r.Realised + r.Scheduled + r.Forecast
			if r.Realised == 0 && r.Scheduled == 0 && r.Forecast == 0 {
				continue
			}
			total.Realised += r.Realised
			total.Scheduled += r.Scheduled
			total.Forecast += r.Forecast
			total.Total += r.Total
			out = append(out, *r)
		}
		slices.SortFunc(out, func(a, b CategoryAmounts) int {
			return cmp.Or(cmp.Compare(b.Total, a.Total), strings.Compare(a.CategoryID, b.CategoryID))
		})
		return out, total
	}
	r := CategoryMonthReport{Month: month}
	r.Income, r.IncomeTotal = fold(income)
	r.Expense, r.ExpenseTotal = fold(expense)
	return r
}
