package billing

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func monthly() Recurrence { return Recurrence{Interval: IntervalMonth, Count: 1} }

func TestRecurrenceValidate(t *testing.T) {
	valid := []Recurrence{
		{IntervalDay, 1}, {IntervalWeek, 2}, {IntervalMonth, 3}, {IntervalYear, 1},
	}
	for _, r := range valid {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v must be valid: %v", r, err)
		}
	}
	invalid := []Recurrence{
		{IntervalMonth, 0}, {IntervalMonth, -1}, {"fortnight", 1}, {"", 1},
	}
	for _, r := range invalid {
		if err := r.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
			t.Errorf("%+v must be invalid, got %v", r, err)
		}
	}
}

func TestPeriodsTileTheCalendarWithoutGapOrOverlap(t *testing.T) {
	// Half-open periods must chain end-to-start exactly. A gap loses a day of
	// service; an overlap double-counts a metered usage record.
	for _, r := range []Recurrence{
		{IntervalDay, 15}, {IntervalWeek, 2}, {IntervalMonth, 1}, {IntervalMonth, 3}, {IntervalYear, 1},
	} {
		anchor := brcal.New(2026, time.January, 31)
		for n := range 30 {
			cur, next := r.PeriodAt(anchor, n), r.PeriodAt(anchor, n+1)
			if cur.End != next.Start {
				t.Fatalf("%+v period %d ends %s but %d starts %s", r, n, cur.End, n+1, next.Start)
			}
			if cur.Days() <= 0 {
				t.Fatalf("%+v period %d has %d days", r, n, cur.Days())
			}
		}
	}
}

func TestMonthlyPeriodsKeepTheAnchorDay(t *testing.T) {
	// The end-of-month bug in one test: a subscription anchored on the 31st must
	// come back to the 31st, not stay on the 28th forever.
	r := monthly()
	anchor := brcal.New(2026, time.January, 31)
	want := []brcal.Date{
		brcal.New(2026, time.January, 31),
		brcal.New(2026, time.February, 28),
		brcal.New(2026, time.March, 31),
		brcal.New(2026, time.April, 30),
		brcal.New(2026, time.May, 31),
	}
	for n, w := range want {
		if got := r.PeriodAt(anchor, n).Start; got != w {
			t.Fatalf("period %d starts %s, want %s", n, got, w)
		}
	}
}

func TestPeriodContainsIsHalfOpen(t *testing.T) {
	p := Period{Start: brcal.New(2026, time.March, 1), End: brcal.New(2026, time.April, 1)}
	if !p.Contains(p.Start) {
		t.Error("the start day is inside the period")
	}
	if p.Contains(p.End) {
		t.Error("the end day belongs to the next period, not this one")
	}
	if p.Contains(brcal.New(2026, time.February, 28)) {
		t.Error("a day before the start is outside")
	}
	if p.Days() != 31 {
		t.Errorf("March has 31 days, got %d", p.Days())
	}
}

func TestPeriodToInvoice(t *testing.T) {
	// Both timings are swept on the same date — the boundary between period 0 and
	// period 1 — but they bill opposite sides of it.
	anchor := brcal.New(2026, time.March, 1)
	base := Subscription{Recurrence: monthly(), Anchor: anchor, PeriodIndex: 0}

	advance := base
	advance.Timing = BillAdvance
	if got := PeriodToInvoice(&advance); got.Start != brcal.New(2026, time.April, 1) {
		t.Fatalf("in advance the sweep bills the period about to start, got %s", got.Start)
	}

	arrears := base
	arrears.Timing = BillArrears
	if got := PeriodToInvoice(&arrears); got.Start != anchor || got.End != brcal.New(2026, time.April, 1) {
		t.Fatalf("in arrears the sweep bills the period that just closed, got %s..%s", got.Start, got.End)
	}

	// The two must never name the same period, or one of them is billing twice.
	if PeriodToInvoice(&advance) == PeriodToInvoice(&arrears) {
		t.Fatal("advance and arrears must bill opposite sides of the boundary")
	}
}

func TestDueDate(t *testing.T) {
	// March 2026: 1st is a Sunday, 31st is a Tuesday.
	p := Period{Start: brcal.New(2026, time.March, 1), End: brcal.New(2026, time.April, 1)}

	cases := []struct {
		name    string
		timing  BillingTiming
		netDays int
		want    brcal.Date
	}{
		{"advance, net 0, Sunday rolls to Monday", BillAdvance, 0, brcal.New(2026, time.March, 2)},
		{"advance, net 5", BillAdvance, 5, brcal.New(2026, time.March, 6)},
		{"arrears, net 0", BillArrears, 0, brcal.New(2026, time.April, 1)},
		{"arrears, net 10", BillArrears, 10, brcal.New(2026, time.April, 13)}, // 11 Apr is a Saturday
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DueDate(p, tc.timing, tc.netDays); got != tc.want {
				t.Fatalf("DueDate = %s (%s), want %s", got, got.Weekday(), tc.want)
			}
		})
	}
}

func TestDueDateNeverMovesTheAccrualPeriod(t *testing.T) {
	// Rolling a December due date into January must not move the invoice into the
	// next accrual year — otherwise twelve annual cycles become eleven or thirteen.
	p := Period{Start: brcal.New(2022, time.December, 1), End: brcal.New(2023, time.January, 1)}
	due := DueDate(p, BillArrears, 0)
	if due.Year != 2023 {
		t.Fatalf("expected the due date to roll into 2023, got %s", due)
	}
	if p.Start != brcal.New(2022, time.December, 1) || p.End != brcal.New(2023, time.January, 1) {
		t.Fatal("DueDate must not mutate the period it was given")
	}
}

func TestDueDateAlwaysLandsOnABusinessDay(t *testing.T) {
	r := monthly()
	anchor := brcal.New(2026, time.January, 1)
	for n := range 60 {
		for _, netDays := range []int{0, 3, 7, 10} {
			due := DueDate(r.PeriodAt(anchor, n), BillAdvance, netDays)
			if !brcal.IsBusinessDay(due) {
				t.Fatalf("period %d net %d due %s (%s) is not a business day", n, netDays, due, due.Weekday())
			}
		}
	}
}
