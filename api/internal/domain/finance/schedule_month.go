package finance

import (
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// WorkdayOfMonth is "the N-th business day of the month" — the way salaries and
// many bills are due in Brazil ("até o 5º dia útil"). N = -1 is the last business
// day. Business days are brcal's: weekdays that are not national holidays.
//
// An N larger than the month's count of business days clamps to the last one,
// the same choice DayOfMonth makes: a payment due "by the 22nd business day" in
// a short February is still due that month, not skipped.
type WorkdayOfMonth struct {
	N int // 1..23, or -1
}

func (e WorkdayOfMonth) kind() string { return "workday_of_month" }

func (e WorkdayOfMonth) Includes(d brcal.Date) bool {
	target, ok := e.dayIn(MonthOf(d))
	return ok && target == d
}

// dayIn returns the matching day of m. ok is false only for a month with no
// business day at all, which this calendar cannot produce.
func (e WorkdayOfMonth) dayIn(m Month) (brcal.Date, bool) {
	var days []brcal.Date
	for day := m.First(); MonthOf(day) == m; day = day.AddDays(1) {
		if brcal.IsBusinessDay(day) {
			days = append(days, day)
		}
	}
	if len(days) == 0 {
		return brcal.Date{}, false
	}
	if e.N == -1 || e.N > len(days) {
		return days[len(days)-1], true
	}
	return days[e.N-1], true
}

// NthWeekdayOfMonth is "the second Monday", "the last Friday" (N = -1). N runs
// 1..4: a fifth weekday exists in some months only, and a schedule that fires in
// some months only is a surprise nobody asked for.
type NthWeekdayOfMonth struct {
	Weekday time.Weekday
	N       int // 1..4, or -1
}

func (e NthWeekdayOfMonth) kind() string { return "nth_weekday_of_month" }

func (e NthWeekdayOfMonth) Includes(d brcal.Date) bool {
	if d.Weekday() != e.Weekday {
		return false
	}
	if e.N == -1 {
		return d.AddDays(7).Month != d.Month
	}
	return (d.Day-1)/7+1 == e.N
}

// Weekly is "every Every-th week on Weekday", counted from the week of Anchor.
// Anchor is the first occurrence and must fall on Weekday; nothing before it is
// included.
type Weekly struct {
	Weekday time.Weekday
	Every   int // 1..52
	Anchor  brcal.Date
}

func (e Weekly) kind() string { return "weekly" }

func (e Weekly) Includes(d brcal.Date) bool {
	if d.Weekday() != e.Weekday || d.Before(e.Anchor) {
		return false
	}
	return (e.Anchor.DaysBetween(d)/7)%e.Every == 0
}
