package main

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestSweepDatesRunOldestFirstAndEndOnToday(t *testing.T) {
	today, _ := brcal.Parse("2026-03-02")
	got := sweepDates(today, 3)
	want := []string{"2026-02-27", "2026-02-28", "2026-03-01", "2026-03-02"}
	if len(got) != len(want) {
		t.Fatalf("got %d dates, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("date %d = %s, want %s", i, got[i], want[i])
		}
	}
	if only := sweepDates(today, 0); len(only) != 1 || only[0] != today {
		t.Fatalf("no catch-up must sweep exactly the given date, got %v", only)
	}
}
