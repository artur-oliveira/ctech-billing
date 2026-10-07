package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func d(y int, m time.Month, day int) brcal.Date { return brcal.New(y, m, day) }

func mustOccurrences(t *testing.T, e Expression, from, to brcal.Date) []brcal.Date {
	t.Helper()
	got, err := Occurrences(e, from, to)
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}
	return got
}

func assertDates(t *testing.T, got []brcal.Date, want ...brcal.Date) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestMonth(t *testing.T) {
	m := MonthOf(d(2026, time.December, 15))
	if m.String() != "2026-12" {
		t.Fatalf("String = %s", m)
	}
	if got := m.Add(1); got != (Month{2027, time.January}) {
		t.Fatalf("Add(1) = %s", got)
	}
	if got := m.Add(-12); got != (Month{2025, time.December}) {
		t.Fatalf("Add(-12) = %s", got)
	}
	if got := (Month{2028, time.February}).Last(); got != d(2028, time.February, 29) {
		t.Fatalf("Last = %s", got)
	}
	if m.Compare(m.Add(1)) != -1 || m.Add(1).Compare(m) != 1 || m.Compare(m) != 0 {
		t.Fatal("Compare is not an order")
	}
}

func TestDayOfMonthClampsToTheLastDay(t *testing.T) {
	got := mustOccurrences(t, DayOfMonth{Day: 31}, d(2026, time.January, 1), d(2026, time.May, 1))
	assertDates(t, got,
		d(2026, time.January, 31), d(2026, time.February, 28),
		d(2026, time.March, 31), d(2026, time.April, 30))
}

func TestYearlyClampsTheLeapDay(t *testing.T) {
	got := mustOccurrences(t, Yearly{Month: time.February, Day: 29}, d(2027, time.January, 1), d(2029, time.January, 1))
	assertDates(t, got, d(2027, time.February, 28), d(2028, time.February, 29))
}

func TestDifferenceExcludesAMonthAndADay(t *testing.T) {
	e := Difference{
		Include: DayOfMonth{Day: 10},
		Exclude: MonthsOfYear{Months: []time.Month{time.December}},
	}
	got := mustOccurrences(t, e, d(2026, time.October, 1), d(2027, time.February, 1))
	assertDates(t, got, d(2026, time.October, 10), d(2026, time.November, 10), d(2027, time.January, 10))

	skipOne := Difference{Include: DayOfMonth{Day: 10}, Exclude: Dates{Days: []brcal.Date{d(2026, time.November, 10)}}}
	got = mustOccurrences(t, skipOne, d(2026, time.October, 1), d(2026, time.December, 31))
	assertDates(t, got, d(2026, time.October, 10), d(2026, time.December, 10))
}

func TestOccurrencesIsHalfOpen(t *testing.T) {
	got := mustOccurrences(t, DayOfMonth{Day: 10}, d(2026, time.March, 10), d(2026, time.April, 10))
	assertDates(t, got, d(2026, time.March, 10))
}

func TestOccurrencesRefusesAnUnboundedRange(t *testing.T) {
	_, err := Occurrences(DayOfMonth{Day: 1}, d(2026, time.January, 1), d(2032, time.January, 1))
	if !errors.Is(err, ErrSpanTooLong) {
		t.Fatalf("err = %v, want ErrSpanTooLong", err)
	}
}

func TestNext(t *testing.T) {
	next, ok := Next(DayOfMonth{Day: 10}, d(2026, time.March, 10))
	if !ok || next != d(2026, time.April, 10) {
		t.Fatalf("Next = %s, %v", next, ok)
	}
}

func TestNextEndsOnAnExpressionThatIncludesNothing(t *testing.T) {
	never := Difference{Include: DayOfMonth{Day: 10}, Exclude: DayOfMonth{Day: 10}}
	if _, ok := Next(never, d(2026, time.March, 1)); ok {
		t.Fatal("Next found a day in an empty expression")
	}
}
