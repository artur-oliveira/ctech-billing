package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestScheduleRollsForwardButKeepsTheNominalDay(t *testing.T) {
	s := Schedule{Expression: DayOfMonth{Day: 31}, Start: d(2026, time.January, 1), Adjust: AdjustRollForward}
	got, err := s.Occurrences(d(2026, time.January, 1), d(2026, time.April, 1))
	if err != nil {
		t.Fatal(err)
	}
	want := []Occurrence{
		// 31/01/2026 is a Saturday: due Monday 02/02, still the January occurrence.
		{Nominal: d(2026, time.January, 31), Due: d(2026, time.February, 2)},
		{Nominal: d(2026, time.February, 28), Due: d(2026, time.March, 2)},
		{Nominal: d(2026, time.March, 31), Due: d(2026, time.March, 31)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestScheduleWindowsNeverCountAnOccurrenceTwice(t *testing.T) {
	s := Schedule{Expression: DayOfMonth{Day: 31}, Start: d(2026, time.January, 1), Adjust: AdjustRollForward}
	seen := map[brcal.Date]int{}
	for m := (Month{2026, time.January}); m.Compare(Month{2027, time.January}) < 0; m = m.Add(1) {
		occ, err := s.Occurrences(m.First(), m.Add(1).First())
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range occ {
			seen[o.Nominal]++
		}
	}
	if len(seen) != 12 {
		t.Fatalf("%d distinct occurrences in 12 monthly windows", len(seen))
	}
	for day, n := range seen {
		if n != 1 {
			t.Fatalf("%s seen %d times", day, n)
		}
	}
}

func TestScheduleIsClippedToStartAndInclusiveEnd(t *testing.T) {
	s := Schedule{
		Expression: DayOfMonth{Day: 10},
		Start:      d(2026, time.February, 11),
		End:        d(2026, time.May, 10),
		Adjust:     AdjustNone,
	}
	got, err := s.Occurrences(d(2026, time.January, 1), d(2027, time.January, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Nominal != d(2026, time.March, 10) || got[2].Nominal != d(2026, time.May, 10) {
		t.Fatalf("got %v", got)
	}
}

func TestScheduleValidate(t *testing.T) {
	ok := Schedule{Expression: DayOfMonth{Day: 1}, Start: d(2026, time.January, 1), Adjust: AdjustNone}
	for name, s := range map[string]Schedule{
		"no expression":    {Start: ok.Start, Adjust: AdjustNone},
		"exclusion only":   {Expression: Dates{}, Start: ok.Start, Adjust: AdjustNone},
		"no start":         {Expression: ok.Expression, Adjust: AdjustNone},
		"end before start": {Expression: ok.Expression, Start: ok.Start, End: ok.Start.AddDays(-1), Adjust: AdjustNone},
		"unknown adjust":   {Expression: ok.Expression, Start: ok.Start, Adjust: "roll_backward"},
	} {
		if err := s.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
			t.Fatalf("%s: err = %v, want ErrInvalidRecurrence", name, err)
		}
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
