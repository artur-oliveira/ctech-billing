package finance

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestWorkdayOfMonth(t *testing.T) {
	cases := []struct {
		name string
		n    int
		in   Month
		want brcal.Date
	}{
		// 2026-02: Carnaval is Mon 16 and Tue 17. Business days: 2,3,4,5,6,9,10,11,12,13,18,...
		{"5th business day, plain week", 5, Month{2026, time.February}, d(2026, time.February, 6)},
		{"11th business day skips Carnaval", 11, Month{2026, time.February}, d(2026, time.February, 18)},
		// 2026-01-01 is a holiday (Thu), 2 is Fri.
		{"1st business day after New Year", 1, Month{2026, time.January}, d(2026, time.January, 2)},
		// 2026-06-04 is Corpus Christi (Thu). Business days: 1,2,3,5,8 ...
		{"4th business day skips Corpus Christi", 4, Month{2026, time.June}, d(2026, time.June, 5)},
		// 2026-12-31 is a Thursday and not a national holiday; 25 (Fri) is.
		{"last business day", -1, Month{2026, time.December}, d(2026, time.December, 31)},
		// 2026-02 has 18 business days; asking for the 23rd clamps to the last.
		{"past the count clamps to the last", 23, Month{2026, time.February}, d(2026, time.February, 27)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustOccurrences(t, WorkdayOfMonth{N: tc.n}, tc.in.First(), tc.in.Last().AddDays(1))
			assertDates(t, got, tc.want)
		})
	}
}

func TestWorkdayOfMonthIsAlwaysABusinessDay(t *testing.T) {
	from, to := d(2026, time.January, 1), d(2031, time.January, 1)
	for _, n := range []int{1, 2, 5, 10, 15, 20, 23, -1} {
		got := mustOccurrences(t, WorkdayOfMonth{N: n}, from, to)
		if len(got) != 60 {
			t.Fatalf("n=%d: %d occurrences in 60 months", n, len(got))
		}
		for _, day := range got {
			if !brcal.IsBusinessDay(day) {
				t.Fatalf("n=%d: %s is not a business day", n, day)
			}
		}
	}
}

func TestNthWeekdayOfMonth(t *testing.T) {
	got := mustOccurrences(t, NthWeekdayOfMonth{Weekday: time.Monday, N: 2}, d(2026, time.March, 1), d(2026, time.May, 1))
	assertDates(t, got, d(2026, time.March, 9), d(2026, time.April, 13))

	got = mustOccurrences(t, NthWeekdayOfMonth{Weekday: time.Friday, N: -1}, d(2026, time.March, 1), d(2026, time.May, 1))
	assertDates(t, got, d(2026, time.March, 27), d(2026, time.April, 24))
}

func TestWeeklyCountsFromTheAnchor(t *testing.T) {
	e := Weekly{Weekday: time.Friday, Every: 2, Anchor: d(2026, time.January, 2)}
	got := mustOccurrences(t, e, d(2025, time.December, 1), d(2026, time.February, 1))
	assertDates(t, got, d(2026, time.January, 2), d(2026, time.January, 16), d(2026, time.January, 30))
}
