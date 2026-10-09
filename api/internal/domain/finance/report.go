package finance

import (
	"slices"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Reports are folds over entries and summaries. They read only: what a report
// says is derived here, once, so the API and any later export agree.

// Entry is one leg as stored on an account, with what the statement shows.
type Entry struct {
	AccountID string
	TxID      string
	Leg       int
	Date      brcal.Date
	Amount    billing.Cents // signed, debit positive
	Kind      TxKind        // "" on entries posted before 6.4
	Flow      string
	Memo      string
	Ref       string // "bill:{id}" when a bill produced it
	Reversal  bool   // a reversal's entries carry the ORIGINAL kind, flow, memo and ref
	Reversed  bool   // set by the reader when a REVERSAL# marker exists
}

// StatementLine is an entry with the account's running balance after it.
type StatementLine struct {
	Entry
	Balance billing.Cents
}

// Statement is an account's lines for a period, between its opening and closing balance.
type Statement struct {
	Opening, Closing billing.Cents
	Lines            []StatementLine
}

// BuildStatement: entriesFrom are the account's entries dated >= from, in key
// order; current is the cached balance. Lines are those with from <= date < to.
func BuildStatement(current billing.Cents, entriesFrom []Entry, from, to brcal.Date) Statement {
	var sinceFrom billing.Cents
	for _, e := range entriesFrom {
		sinceFrom += e.Amount
	}
	s := Statement{Opening: current - sinceFrom}
	bal := s.Opening
	for _, e := range entriesFrom {
		if e.Date.Before(from) || !e.Date.Before(to) {
			continue
		}
		bal += e.Amount
		s.Lines = append(s.Lines, StatementLine{Entry: e, Balance: bal})
	}
	s.Closing = bal
	return s
}

// FlowLine is one category's net cash in a month.
type FlowLine struct {
	Flow   string        // category id, or "" for Sem categoria
	Amount billing.Cents // positive in, negative out
}

// CashMonth is one month of the cash flow.
type CashMonth struct {
	Month    Month
	In, Out  billing.Cents // Out is a magnitude
	Openings billing.Cents // opening balances posted this month (FlowNone, KindOpeningBalance)
	ByFlow   []FlowLine    // sorted by Flow
}

// CashFlowReport is the cash flow for a range of months.
type CashFlowReport struct {
	OpeningCash, ClosingCash billing.Cents
	Months                   []CashMonth // every month from..to, empty ones included
}

// months lists every month from..to, inclusive.
func months(from, to Month) []Month {
	var ms []Month
	for m := from; m.Compare(to) <= 0; m = m.Add(1) {
		ms = append(ms, m)
	}
	return ms
}

// CashFlow: balances are the current balances of the cash accounts; entriesFrom
// are all their entries dated >= first day of `from`.
func CashFlow(balances map[string]billing.Cents, entriesFrom []Entry, from, to Month) CashFlowReport {
	var r CashFlowReport
	idx := map[Month]int{}
	for _, m := range months(from, to) {
		idx[m] = len(r.Months)
		r.Months = append(r.Months, CashMonth{Month: m})
	}
	var now, sinceFrom billing.Cents
	for _, b := range balances {
		now += b
	}
	byFlow := make([]map[string]billing.Cents, len(r.Months))
	for _, e := range entriesFrom {
		sinceFrom += e.Amount
		i, ok := idx[MonthOf(e.Date)]
		if !ok {
			continue // after `to`: only counts toward the opening
		}
		m := &r.Months[i]
		switch {
		case e.Kind == KindOpeningBalance:
			// an opening balance, or its reversal (same kind, opposite sign)
			m.Openings += e.Amount
		case e.Flow == FlowNone:
			// a transfer, or its reversal: cash moved between own accounts
		default:
			// A reversal undoes the side its original was on: a reversed payment
			// lowers Out instead of raising In, so gross figures do not swell.
			orig := e.Amount
			if e.Reversal {
				orig = -e.Amount
			}
			if orig > 0 {
				m.In += e.Amount
			} else {
				m.Out -= e.Amount
			}
			if byFlow[i] == nil {
				byFlow[i] = map[string]billing.Cents{}
			}
			byFlow[i][e.Flow] += e.Amount
		}
	}
	r.OpeningCash = now - sinceFrom
	r.ClosingCash = r.OpeningCash
	for i := range r.Months {
		m := &r.Months[i]
		r.ClosingCash += m.In - m.Out + m.Openings
		for f, a := range byFlow[i] {
			if a != 0 { // a category whose movements cancelled out is not a line
				m.ByFlow = append(m.ByFlow, FlowLine{Flow: f, Amount: a})
			}
		}
		slices.SortFunc(m.ByFlow, func(a, b FlowLine) int { return strings.Compare(a.Flow, b.Flow) })
	}
	return r
}

// DRECategory is one income or expense account's line in the DRE.
type DRECategory struct {
	AccountID string
	Amounts   []billing.Cents // one per month; positive raises the result
	Total     billing.Cents
}

// DREGroupLine is one DRE group with its categories and subtotal.
type DREGroupLine struct {
	Group      DREGroup
	Categories []DRECategory
	Amounts    []billing.Cents
	Total      billing.Cents
}

// DREReport is the accrual result for a range of months.
type DREReport struct {
	Months []Month
	Groups []DREGroupLine // in the fixed DRE order, groups with no rows omitted
	Result []billing.Cents
	Total  billing.Cents
}

// CategoryInfo is what DRE needs to know about an account.
type CategoryInfo struct {
	Class AccountClass
	Group DREGroup
}

var dreOrder = []DREGroup{GroupGrossRevenue, GroupDeductions, GroupCosts, GroupOperatingExpenses, GroupFinancialResult, GroupOther}

// DRE folds the cached monthly summaries of income and expense accounts.
func DRE(summaries map[SummaryKey]Totals, accounts map[string]CategoryInfo, from, to Month) DREReport {
	r := DREReport{Months: months(from, to)}
	idx := map[Month]int{}
	for i, m := range r.Months {
		idx[m] = i
	}
	n := len(r.Months)
	cats := map[string]*DRECategory{}
	for k, t := range summaries {
		info, ok := accounts[k.AccountID]
		i, inRange := idx[k.Month]
		if !ok || !inRange || (info.Class != ClassIncome && info.Class != ClassExpense) {
			continue
		}
		// Income raises the result by its net credits, expense lowers it by its net debits.
		v := t.Credits - t.Debits
		c := cats[k.AccountID]
		if c == nil {
			c = &DRECategory{AccountID: k.AccountID, Amounts: make([]billing.Cents, n)}
			cats[k.AccountID] = c
		}
		c.Amounts[i] += v
		c.Total += v
	}
	r.Result = make([]billing.Cents, n)
	for _, g := range dreOrder {
		line := DREGroupLine{Group: g, Amounts: make([]billing.Cents, n)}
		for id, c := range cats {
			if cg := accounts[id].Group; cg != g && (cg != "" || g != GroupOther) {
				continue
			}
			line.Categories = append(line.Categories, *c)
			for i, a := range c.Amounts {
				line.Amounts[i] += a
				r.Result[i] += a
			}
			line.Total += c.Total
		}
		if len(line.Categories) == 0 {
			continue
		}
		slices.SortFunc(line.Categories, func(a, b DRECategory) int { return strings.Compare(a.AccountID, b.AccountID) })
		r.Groups = append(r.Groups, line)
		r.Total += line.Total
	}
	return r
}
