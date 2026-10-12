package finance

import (
	"cmp"
	"slices"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// SpendLine is one amount spent on a card, in a category, in a month: a
// statement line (by statement month) or a purchase (by purchase month). A
// refund's credit is negative.
type SpendLine struct {
	CardID, CategoryID string
	Month              Month
	Amount             billing.Cents
}

// SpendRow is one category's or one card's spending per month and in all.
type SpendRow struct {
	CategoryID, CardID string // one of them, by the row's kind
	Amounts            []billing.Cents
	Total              billing.Cents
}

// CardSpendingReport is card spending for a range of months, by category and by
// card, each largest first.
type CardSpendingReport struct {
	Months     []Month
	Categories []SpendRow
	Cards      []SpendRow
	ByMonth    []billing.Cents
	Total      billing.Cents
}

// CardSpending folds the lines into the report; lines outside from..to are
// ignored, and a category or card with nothing in any month is not a row (one
// that nets to zero over the range, a purchase and its refund on a later
// statement, stays: the rows must add up to the total row month by month).
func CardSpending(lines []SpendLine, from, to Month) CardSpendingReport {
	r := CardSpendingReport{Months: months(from, to)}
	idx := map[Month]int{}
	for i, m := range r.Months {
		idx[m] = i
	}
	n := len(r.Months)
	r.ByMonth = make([]billing.Cents, n)
	cats, cards := map[string]*SpendRow{}, map[string]*SpendRow{}
	row := func(rows map[string]*SpendRow, key string, mk func() SpendRow) *SpendRow {
		if rows[key] == nil {
			v := mk()
			rows[key] = &v
		}
		return rows[key]
	}
	for _, l := range lines {
		i, ok := idx[l.Month]
		if !ok {
			continue
		}
		for _, x := range []*SpendRow{
			row(cats, l.CategoryID, func() SpendRow { return SpendRow{CategoryID: l.CategoryID, Amounts: make([]billing.Cents, n)} }),
			row(cards, l.CardID, func() SpendRow { return SpendRow{CardID: l.CardID, Amounts: make([]billing.Cents, n)} }),
		} {
			x.Amounts[i] += l.Amount
			x.Total += l.Amount
		}
		r.ByMonth[i] += l.Amount
		r.Total += l.Amount
	}
	r.Categories, r.Cards = largestFirst(cats, func(x SpendRow) string { return x.CategoryID }), largestFirst(cards, func(x SpendRow) string { return x.CardID })
	return r
}

func largestFirst(rows map[string]*SpendRow, key func(SpendRow) string) []SpendRow {
	out := make([]SpendRow, 0, len(rows))
	for _, x := range rows {
		if slices.ContainsFunc(x.Amounts, func(a billing.Cents) bool { return a != 0 }) {
			out = append(out, *x)
		}
	}
	slices.SortFunc(out, func(a, b SpendRow) int {
		if c := cmp.Compare(b.Total, a.Total); c != 0 {
			return c
		}
		return strings.Compare(key(a), key(b))
	})
	return out
}
