// Package finance is the pure domain of the console's finance section (spec
// docs/specs/2026-10-07-finance-erp-design.md): temporal expressions for
// recurrences, the double-entry transactions that record every fact, the
// posting rules that build them, and credit-card installment allocation.
//
// Like package billing, nothing here performs I/O, reads a clock, or knows about
// DynamoDB or HTTP. This is a management ledger of money held elsewhere, never
// custody (ADR 0024): nothing in this package authorises moving money.
package finance

import (
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Month is a calendar month. It names a card statement and a summary row, which
// are both about a month and never about a day in it.
type Month struct {
	Year  int
	Month time.Month
}

// MonthOf returns the month d falls in.
func MonthOf(d brcal.Date) Month { return Month{Year: d.Year, Month: d.Month} }

// Add returns m shifted by n months (n may be negative).
func (m Month) Add(n int) Month { return MonthOf(m.First().AddMonths(n)) }

// First returns the first day of m.
func (m Month) First() brcal.Date { return brcal.New(m.Year, m.Month, 1) }

// Last returns the last day of m.
func (m Month) Last() brcal.Date {
	return brcal.New(m.Year, m.Month, brcal.DaysInMonth(m.Year, m.Month))
}

// Compare returns -1, 0 or +1 as m is before, equal to, or after o.
func (m Month) Compare(o Month) int { return m.First().Compare(o.First()) }

// String renders YYYY-MM, the form a statement and a summary key carry.
func (m Month) String() string { return fmt.Sprintf("%04d-%02d", m.Year, int(m.Month)) }
