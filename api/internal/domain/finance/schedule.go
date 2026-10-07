package finance

import (
	"errors"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Expression is a temporal expression, after Fowler's "Recurring Events for
// Calendars": it answers whether a day belongs to a schedule, and nothing else.
// A recurrence holds one of these, never a list of dates, which is what lets a
// projection reach any month without anything being written ahead of time.
//
// The set of implementations is closed (the unexported method), because each
// one must also have a JSON form (expression_json.go) and a validation rule.
type Expression interface {
	Includes(d brcal.Date) bool
	kind() string
}

// MaxSpanDays bounds every search over an expression. Occurrences and Next walk
// day by day, so an unbounded range is an unbounded loop; five years is longer
// than any projection the console shows.
const MaxSpanDays = 5 * 366

// ErrSpanTooLong is returned for a range longer than MaxSpanDays.
var ErrSpanTooLong = errors.New("finance: date range longer than five years")

// Occurrences returns the days in [from, to) that e includes, in order.
func Occurrences(e Expression, from, to brcal.Date) ([]brcal.Date, error) {
	if from.DaysBetween(to) > MaxSpanDays {
		return nil, ErrSpanTooLong
	}
	var out []brcal.Date
	for d := from; d.Before(to); d = d.AddDays(1) {
		if e.Includes(d) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Next returns the first day strictly after `after` that e includes. ok is false
// when no such day exists within MaxSpanDays — an expression that excludes
// everything (a Difference of two equal sets) must end, not spin.
func Next(e Expression, after brcal.Date) (next brcal.Date, ok bool) {
	d := after.AddDays(1)
	for range MaxSpanDays {
		if e.Includes(d) {
			return d, true
		}
		d = d.AddDays(1)
	}
	return brcal.Date{}, false
}

// DayOfMonth is "every N-th of the month". A day past the month's end clamps to
// its last day — the same rule as a subscription anchor (brcal.Date.AddMonths) —
// so "every 31st" is paid in February rather than skipped.
type DayOfMonth struct {
	Day int // 1..31
}

func (e DayOfMonth) kind() string { return "day_of_month" }

func (e DayOfMonth) Includes(d brcal.Date) bool {
	return d.Day == min(e.Day, brcal.DaysInMonth(d.Year, d.Month))
}

// Yearly is "every year on this day". 29 February clamps to the 28th in a
// common year, for the same reason DayOfMonth clamps.
type Yearly struct {
	Month time.Month
	Day   int
}

func (e Yearly) kind() string { return "yearly" }

func (e Yearly) Includes(d brcal.Date) bool {
	return d.Month == e.Month && d.Day == min(e.Day, brcal.DaysInMonth(d.Year, d.Month))
}

// Dates is an explicit set of days. Its use is as the excluded side of a
// Difference ("skip 15/03"), not as a schedule of its own.
type Dates struct {
	Days []brcal.Date
}

func (e Dates) kind() string { return "dates" }

func (e Dates) Includes(d brcal.Date) bool {
	for _, x := range e.Days {
		if x == d {
			return true
		}
	}
	return false
}

// Difference includes the days Include does and Exclude does not: "every 10th
// except December", "every Friday except 25/12".
type Difference struct {
	Include Expression
	Exclude Expression
}

func (e Difference) kind() string { return "difference" }

func (e Difference) Includes(d brcal.Date) bool {
	return e.Include.Includes(d) && !e.Exclude.Includes(d)
}

// MonthsOfYear includes every day of the listed months. Its use is as the
// excluded side of a Difference ("except December"); on its own it would be a
// schedule that fires every day for a month.
type MonthsOfYear struct {
	Months []time.Month
}

func (e MonthsOfYear) kind() string { return "months_of_year" }

func (e MonthsOfYear) Includes(d brcal.Date) bool {
	for _, m := range e.Months {
		if d.Month == m {
			return true
		}
	}
	return false
}
