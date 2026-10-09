package v1

import (
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

const (
	maxReportMonths  = 24
	maxStatementDays = 366
)

// rangeError is a refused from..to, carrying the stable code and params the
// client translates; its Error() is the English fallback.
type rangeError struct {
	code   string
	msg    string
	params []any
}

func (e *rangeError) Error() string { return e.msg }

// rangeFieldErr turns a parse*Range error into the field error for "from".
func rangeFieldErr(err error) problem.FieldError {
	var re *rangeError
	if errors.As(err, &re) {
		return fieldErr("from", re.code, re.msg, re.params...)
	}
	return fieldErr("from", "out_of_range", err.Error())
}

// parseMonthRange reads a report's from..to, both YYYY-MM and inclusive.
func parseMonthRange(from, to string) (finance.Month, finance.Month, error) {
	f, err1 := finance.ParseMonth(from)
	t, err2 := finance.ParseMonth(to)
	if err1 != nil || err2 != nil {
		return f, t, &rangeError{code: "invalid_format", msg: "use YYYY-MM", params: []any{"format", "YYYY-MM"}}
	}
	if t.Compare(f) < 0 {
		return f, t, &rangeError{code: "range_end_before_start", msg: "the end is before the start"}
	}
	if n := t.MonthsSince(f) + 1; n > maxReportMonths {
		return f, t, &rangeError{code: "range_too_long", msg: fmt.Sprintf("at most %d months", maxReportMonths), params: []any{"max", maxReportMonths, "unit", "months"}}
	}
	return f, t, nil
}

// parseDateRange reads a statement's from..to, YYYY-MM-DD, to exclusive.
func parseDateRange(from, to string) (brcal.Date, brcal.Date, error) {
	f, err1 := brcal.Parse(from)
	t, err2 := brcal.Parse(to)
	if err1 != nil || err2 != nil {
		return f, t, &rangeError{code: "invalid_format", msg: "use YYYY-MM-DD", params: []any{"format", "YYYY-MM-DD"}}
	}
	if !f.Before(t) {
		return f, t, &rangeError{code: "range_end_before_start", msg: "the end must be after the start"}
	}
	if f.DaysBetween(t) > maxStatementDays {
		return f, t, &rangeError{code: "range_too_long", msg: fmt.Sprintf("at most %d days", maxStatementDays), params: []any{"max", maxStatementDays, "unit", "days"}}
	}
	return f, t, nil
}

type dreCategoryDTO struct {
	CategoryID string          `json:"category_id"`
	Amounts    []billing.Cents `json:"amounts"`
	Total      billing.Cents   `json:"total"`
}

type dreGroupDTO struct {
	Group      string           `json:"group"`
	Categories []dreCategoryDTO `json:"categories"`
	Amounts    []billing.Cents  `json:"amounts"`
	Total      billing.Cents    `json:"total"`
}

type dreDTO struct {
	Months []string        `json:"months"`
	Groups []dreGroupDTO   `json:"groups"`
	Result []billing.Cents `json:"result"`
	Total  billing.Cents   `json:"total"`
}

// dre is the accrual result: the cached SUMMARY rows of income and expense
// accounts, one range Query.
func (h *financeHandlers) dre(c fiber.Ctx) error {
	from, to, err := parseMonthRange(c.Query("from"), c.Query("to"))
	if err != nil {
		return problem.Validation([]problem.FieldError{rangeFieldErr(err)}).Send(c)
	}
	sp := middleware.GetSpace(c)
	rows, err := h.ledger.Summaries(c.Context(), sp, from, to)
	if err != nil {
		return fail(c, err)
	}
	accounts, err := h.ledger.ListAccounts(c.Context(), sp)
	if err != nil {
		return fail(c, err)
	}
	sums := make(map[finance.SummaryKey]finance.Totals, len(rows))
	for _, r := range rows {
		sums[finance.SummaryKey{Month: r.Month, AccountID: r.AccountID}] = r.Totals
	}
	info := make(map[string]finance.CategoryInfo, len(accounts))
	for _, a := range accounts {
		info[a.ID] = finance.CategoryInfo{Class: a.Class, Group: a.Group}
	}
	r := finance.DRE(sums, info, from, to)
	out := dreDTO{Months: make([]string, len(r.Months)), Groups: make([]dreGroupDTO, 0, len(r.Groups)), Result: r.Result, Total: r.Total}
	for i, m := range r.Months {
		out.Months[i] = m.String()
	}
	for _, g := range r.Groups {
		gd := dreGroupDTO{Group: string(g.Group), Amounts: g.Amounts, Total: g.Total, Categories: make([]dreCategoryDTO, len(g.Categories))}
		for i, cat := range g.Categories {
			gd.Categories[i] = dreCategoryDTO{CategoryID: cat.AccountID, Amounts: cat.Amounts, Total: cat.Total}
		}
		out.Groups = append(out.Groups, gd)
	}
	return c.JSON(out)
}

type flowLineDTO struct {
	CategoryID string        `json:"category_id"` // "" = Sem categoria
	Amount     billing.Cents `json:"amount"`
}

type cashMonthDTO struct {
	Month    string        `json:"month"`
	In       billing.Cents `json:"in"`
	Out      billing.Cents `json:"out"`
	Openings billing.Cents `json:"openings"`
	Lines    []flowLineDTO `json:"lines"`
}

type cashFlowDTO struct {
	From        string         `json:"from"`
	To          string         `json:"to"`
	OpeningCash billing.Cents  `json:"opening_cash"`
	ClosingCash billing.Cents  `json:"closing_cash"`
	Months      []cashMonthDTO `json:"months"`
}

// cashFlow folds the entries of every cash account (non-system assets, archived
// included: their history is real) from the first day of the period onward.
func (h *financeHandlers) cashFlow(c fiber.Ctx) error {
	from, to, err := parseMonthRange(c.Query("from"), c.Query("to"))
	if err != nil {
		return problem.Validation([]problem.FieldError{rangeFieldErr(err)}).Send(c)
	}
	sp := middleware.GetSpace(c)
	accounts, err := h.ledger.ListAccounts(c.Context(), sp)
	if err != nil {
		return fail(c, err)
	}
	balances := map[string]billing.Cents{}
	var entries []finance.Entry
	for _, a := range accounts {
		if a.Class != finance.ClassAsset || a.System {
			continue
		}
		balances[a.ID] = a.Balance
		es, err := h.ledger.EntriesFrom(c.Context(), sp, a.ID, from.First())
		if err != nil {
			return fail(c, err)
		}
		entries = append(entries, es...)
	}
	r := finance.CashFlow(balances, entries, from, to)
	out := cashFlowDTO{From: from.String(), To: to.String(), OpeningCash: r.OpeningCash, ClosingCash: r.ClosingCash, Months: make([]cashMonthDTO, len(r.Months))}
	for i, m := range r.Months {
		md := cashMonthDTO{Month: m.Month.String(), In: m.In, Out: m.Out, Openings: m.Openings, Lines: make([]flowLineDTO, len(m.ByFlow))}
		for j, l := range m.ByFlow {
			md.Lines[j] = flowLineDTO{CategoryID: l.Flow, Amount: l.Amount}
		}
		out.Months[i] = md
	}
	return c.JSON(out)
}
