package billing

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func march2026() Period {
	return Period{Start: brcal.New(2026, time.March, 1), End: brcal.New(2026, time.April, 1)}
}

func TestProrate(t *testing.T) {
	p := march2026() // 31 days
	full := Cents(31000)

	cases := []struct {
		name     string
		from, to brcal.Date
		want     Cents
	}{
		{"whole period", p.Start, p.End, full},
		{"empty range", p.Start, p.Start, 0},
		{"inverted range", p.End, p.Start, 0},
		{"one day", p.Start, brcal.New(2026, time.March, 2), 1000},
		{"half-ish: 16 of 31 days", brcal.New(2026, time.March, 16), p.End, 16000},
		{"clamped below the start", brcal.New(2026, time.February, 1), brcal.New(2026, time.March, 2), 1000},
		{"clamped above the end", brcal.New(2026, time.March, 31), brcal.New(2026, time.May, 1), 1000},
		{"range entirely outside", brcal.New(2026, time.May, 1), brcal.New(2026, time.June, 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Prorate(full, p, tc.from, tc.to); got != tc.want {
				t.Fatalf("Prorate = %d (%s), want %d", got, got, tc.want)
			}
		})
	}
}

func TestProrateUsedAndRemainingSumToTheWhole(t *testing.T) {
	// The property that matters: whatever the rounding, the two halves must add
	// back up to the full price. A customer must never be billed 1 centavo more
	// or less than the plan because they changed something mid-month.
	for _, p := range []Period{
		march2026(),
		{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)},
		{Start: brcal.New(2028, time.February, 1), End: brcal.New(2028, time.March, 1)}, // leap
		{Start: brcal.New(2026, time.January, 1), End: brcal.New(2027, time.January, 1)},
	} {
		for _, full := range []Cents{100, 999, 4990, 31000, 99999} {
			for d := p.Start; d.Before(p.End); d = d.AddDays(1) {
				used, remaining := ProrateUsed(full, p, d), ProrateRemaining(full, p, d)
				if used+remaining != full {
					t.Fatalf("period %s..%s full %d at %s: used %d + remaining %d = %d",
						p.Start, p.End, full, d, used, remaining, used+remaining)
				}
			}
		}
	}
}

func TestProrateExactHalfDoesNotOverbillByOneCentavo(t *testing.T) {
	// The regression this file's complement rule exists for: R$ 9,99 over a
	// 28-day February, split at exactly half. Rounding each side independently
	// gives R$ 5,00 + R$ 5,00 = R$ 10,00 — one centavo more than the plan costs.
	feb := Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)}
	full := Cents(999)
	at := brcal.New(2026, time.February, 15) // 14 days used, 14 remaining

	independently := Prorate(full, feb, feb.Start, at) + Prorate(full, feb, at, feb.End)
	if independently != full+1 {
		t.Logf("independent rounding gave %d (expected the off-by-one this test guards)", independently)
	}
	if used, remaining := ProrateUsed(full, feb, at), ProrateRemaining(full, feb, at); used+remaining != full {
		t.Fatalf("used %s + remaining %s = %s, want %s", used, remaining, used+remaining, full)
	}
}

func TestProrateUsesRealMonthLengths(t *testing.T) {
	// Half of February is a bigger fraction of the month than half of March.
	// A fixed 30-day month would make these equal and would not reconcile against
	// a calendar.
	feb := Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)}
	mar := march2026()
	full := Cents(10000)

	// The same 14 elapsed days are a bigger share of February than of March.
	febUsed := ProrateUsed(full, feb, brcal.New(2026, time.February, 15))
	marUsed := ProrateUsed(full, mar, brcal.New(2026, time.March, 15))
	if febUsed <= marUsed {
		t.Fatalf("14/28 (%s) should exceed 14/31 (%s)", febUsed, marUsed)
	}
	if febUsed != 5000 {
		t.Fatalf("14/28 of R$ 100,00 is R$ 50,00, got %s", febUsed)
	}
	if marUsed != MulDiv(full, 14, 31) {
		t.Fatalf("14/31 of R$ 100,00 mismatch: %s", marUsed)
	}
	// A fixed 30-day month would make both R$ 46,67 and reconcile against nothing.
	if febUsed == MulDiv(full, 14, 30) {
		t.Fatal("proration is using a fixed 30-day month")
	}
}

func TestProrateSwap(t *testing.T) {
	p := march2026()
	old, upgraded := Cents(31000), Cents(62000)
	at := brcal.New(2026, time.March, 16) // 16 days remaining

	s := ProrateSwap(old, upgraded, p, at)
	if s.RemainingDays != 16 || s.PeriodDays != 31 {
		t.Fatalf("days: remaining %d of %d, want 16 of 31", s.RemainingDays, s.PeriodDays)
	}
	if s.Credit != 16000 {
		t.Fatalf("credit = %s, want R$ 160,00", s.Credit)
	}
	if s.Charge != 32000 {
		t.Fatalf("charge = %s, want R$ 320,00", s.Charge)
	}
	if s.Net() != 16000 {
		t.Fatalf("net = %s, want R$ 160,00", s.Net())
	}
}

func TestSwapToTheSamePriceNetsExactlyZero(t *testing.T) {
	// Both sides use the same day count, so an identical price must leave no
	// rounding residue on the invoice.
	p := march2026()
	for _, full := range []Cents{1, 7, 333, 4990, 99999} {
		for d := p.Start; d.Before(p.End); d = d.AddDays(1) {
			if net := ProrateSwap(full, full, p, d).Net(); net != 0 {
				t.Fatalf("swapping %d to itself at %s netted %d", full, d, net)
			}
		}
	}
}

func TestProrateSwapClampsTheChangeDate(t *testing.T) {
	p := march2026()
	before := ProrateSwap(1000, 2000, p, brcal.New(2026, time.January, 1))
	if before.RemainingDays != 31 || before.Charge != 2000 {
		t.Fatalf("a change before the period covers the whole period, got %+v", before)
	}
	after := ProrateSwap(1000, 2000, p, brcal.New(2026, time.June, 1))
	if after.RemainingDays != 0 || after.Charge != 0 || after.Credit != 0 {
		t.Fatalf("a change after the period covers nothing, got %+v", after)
	}
}
