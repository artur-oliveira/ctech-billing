package brcal

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAddMonthsClampsInsteadOfOverflowing(t *testing.T) {
	// The bug this guards: time.AddDate(0,1,0) on 31 January yields 2/3 March,
	// which makes a subscription anchored on the 31st skip February entirely and
	// bill twice in March.
	cases := []struct {
		name string
		from Date
		n    int
		want Date
	}{
		{"31 Jan +1 clamps to 28 Feb (common year)", New(2026, time.January, 31), 1, New(2026, time.February, 28)},
		{"31 Jan +1 clamps to 29 Feb (leap year)", New(2028, time.January, 31), 1, New(2028, time.February, 29)},
		{"31 Jan +2 lands on 31 Mar, anchor preserved", New(2026, time.January, 31), 2, New(2026, time.March, 31)},
		{"31 Mar +1 clamps to 30 Apr", New(2026, time.March, 31), 1, New(2026, time.April, 30)},
		{"15th is never clamped", New(2026, time.January, 15), 1, New(2026, time.February, 15)},
		{"+12 months is the same day next year", New(2026, time.August, 15), 12, New(2027, time.August, 15)},
		{"crosses year forward", New(2026, time.November, 30), 3, New(2027, time.February, 28)},
		{"crosses year backward", New(2026, time.January, 31), -1, New(2025, time.December, 31)},
		{"-2 from January", New(2026, time.January, 15), -2, New(2025, time.November, 15)},
		{"-12 is the same day last year", New(2026, time.January, 15), -12, New(2025, time.January, 15)},
		{"-13 crosses two boundaries", New(2026, time.January, 15), -13, New(2024, time.December, 15)},
		{"29 Feb -12 clamps to 28 Feb", New(2028, time.February, 29), -12, New(2027, time.February, 28)},
		{"zero shift is identity", New(2026, time.February, 28), 0, New(2026, time.February, 28)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.from.AddMonths(tc.n); got != tc.want {
				t.Fatalf("%s.AddMonths(%d) = %s, want %s", tc.from, tc.n, got, tc.want)
			}
		})
	}
}

func TestAddYearsClampsLeapDay(t *testing.T) {
	if got, want := New(2028, time.February, 29).AddYears(1), New(2029, time.February, 28); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestAddMonthsFromAnchorDoesNotDrift(t *testing.T) {
	// Periods must always be derived from the anchor, never from the previous
	// period's date — otherwise a single clamp permanently moves the billing day.
	anchor := New(2026, time.January, 31)
	want := []Date{
		New(2026, time.February, 28),
		New(2026, time.March, 31),
		New(2026, time.April, 30),
		New(2026, time.May, 31),
	}
	for i, w := range want {
		if got := anchor.AddMonths(i + 1); got != w {
			t.Fatalf("anchor+%d months = %s, want %s", i+1, got, w)
		}
	}

	// Contrast: chaining from the previous result drifts to the 28th forever.
	drift := anchor
	for range 2 {
		drift = drift.AddMonths(1)
	}
	if drift == New(2026, time.March, 31) {
		t.Fatal("chaining unexpectedly preserved the anchor; the drift warning in the docs is stale")
	}
}

func TestDaysBetween(t *testing.T) {
	cases := []struct {
		from, to Date
		want     int
	}{
		{New(2026, time.January, 1), New(2026, time.January, 1), 0},
		{New(2026, time.January, 1), New(2026, time.January, 31), 30},
		{New(2026, time.January, 31), New(2026, time.January, 1), -30},
		{New(2026, time.January, 1), New(2027, time.January, 1), 365},
		{New(2028, time.January, 1), New(2029, time.January, 1), 366}, // leap
		{New(2026, time.February, 28), New(2026, time.March, 1), 1},
	}
	for _, tc := range cases {
		if got := tc.from.DaysBetween(tc.to); got != tc.want {
			t.Fatalf("%s.DaysBetween(%s) = %d, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestFromTimeUsesSaoPauloDayNotUTCDay(t *testing.T) {
	// 00:30 BRT on 1 March is 03:30 UTC on 1 March; but 23:30 BRT on 28 February
	// is 02:30 UTC on 1 March. A report built from UTC misfiles that invoice in
	// the wrong month. This is the bug ADR 0002 requires year/month/day to avoid.
	utcInstant := time.Date(2026, time.March, 1, 2, 30, 0, 0, time.UTC)
	if got, want := FromTime(utcInstant), New(2026, time.February, 28); got != want {
		t.Fatalf("FromTime(%s) = %s, want %s", utcInstant, got, want)
	}
}

func TestCompareAndOrdering(t *testing.T) {
	a, b := New(2026, time.January, 1), New(2026, time.January, 2)
	if !a.Before(b) || !b.After(a) || a.Compare(a) != 0 {
		t.Fatal("ordering is wrong")
	}
	if a.Compare(b) != -1 || b.Compare(a) != 1 {
		t.Fatal("Compare must return -1/0/+1")
	}
}

func TestParseRoundTripAndJSON(t *testing.T) {
	d, err := Parse("2026-08-15")
	if err != nil {
		t.Fatal(err)
	}
	if d.String() != "2026-08-15" {
		t.Fatalf("round trip lost information: %s", d)
	}
	if _, err := Parse("15/08/2026"); err == nil {
		t.Fatal("Parse must reject non-ISO input")
	}

	type payload struct {
		Due Date `json:"due"`
	}
	raw, err := json.Marshal(payload{Due: d})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"due":"2026-08-15"}` {
		t.Fatalf("Date must serialize as a plain ISO string, got %s", raw)
	}
	var back payload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Due != d {
		t.Fatalf("JSON round trip: got %s, want %s", back.Due, d)
	}
}

func TestDaysInMonth(t *testing.T) {
	cases := map[Date]int{
		New(2026, time.February, 1): 28,
		New(2028, time.February, 1): 29,
		New(2000, time.February, 1): 29, // divisible by 400
		New(1900, time.February, 1): 28, // divisible by 100, not 400
		New(2026, time.April, 1):    30,
		New(2026, time.December, 1): 31,
	}
	for d, want := range cases {
		if got := DaysInMonth(d.Year, d.Month); got != want {
			t.Fatalf("DaysInMonth(%d, %s) = %d, want %d", d.Year, d.Month, got, want)
		}
	}
}
