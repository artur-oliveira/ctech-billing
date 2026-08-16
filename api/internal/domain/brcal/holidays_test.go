package brcal

import (
	"testing"
	"time"
)

// knownEaster is the regression table ARCHITECTURE.md § 4 asks for. Production
// code computes; this table only proves the computation has not drifted.
var knownEaster = map[int]Date{
	2020: New(2020, time.April, 12),
	2021: New(2021, time.April, 4),
	2022: New(2022, time.April, 17),
	2023: New(2023, time.April, 9),
	2024: New(2024, time.March, 31),
	2025: New(2025, time.April, 20),
	2026: New(2026, time.April, 5),
	2027: New(2027, time.March, 28),
	2028: New(2028, time.April, 16),
	2029: New(2029, time.April, 1),
	2030: New(2030, time.April, 21),
	2031: New(2031, time.April, 13),
	2032: New(2032, time.March, 28),
	2033: New(2033, time.April, 17),
	2034: New(2034, time.April, 9),
	2035: New(2035, time.March, 25),
}

func TestEasterMatchesKnownDates(t *testing.T) {
	for year, want := range knownEaster {
		if got := Easter(year); got != want {
			t.Errorf("Easter(%d) = %s, want %s", year, got, want)
		}
	}
}

func TestEasterIsAlwaysASunday(t *testing.T) {
	for year := 1900; year <= 2200; year++ {
		if wd := Easter(year).Weekday(); wd != time.Sunday {
			t.Fatalf("Easter(%d) falls on %s", year, wd)
		}
	}
}

func TestMoveableHolidays(t *testing.T) {
	// Derived from Easter 2026 = 5 April.
	cases := []struct {
		date Date
		name string
	}{
		{New(2026, time.February, 16), Carnaval},    // Easter − 48
		{New(2026, time.February, 17), Carnaval},    // Easter − 47
		{New(2026, time.February, 18), ""},          // Ash Wednesday is not one
		{New(2026, time.April, 3), SextaFeiraSanta}, // Easter − 2
		{New(2026, time.April, 5), ""},              // Easter Sunday itself is not a listed holiday
		{New(2026, time.June, 4), CorpusChristi},    // Easter + 60
		{New(2025, time.March, 3), Carnaval},        // Easter 2025 = 20 April
		{New(2025, time.April, 18), SextaFeiraSanta},
		{New(2025, time.June, 19), CorpusChristi},
	}
	for _, tc := range cases {
		name, ok := HolidayName(tc.date)
		if tc.name == "" {
			if ok {
				t.Errorf("%s: expected no holiday, got %q", tc.date, name)
			}
			continue
		}
		if !ok || name != tc.name {
			t.Errorf("%s: got (%q, %v), want %q", tc.date, name, ok, tc.name)
		}
	}
}

func TestFixedHolidays(t *testing.T) {
	want := map[Date]string{
		New(2026, time.January, 1):   Confraternizacao,
		New(2026, time.April, 21):    Tiradentes,
		New(2026, time.May, 1):       DiaDoTrabalho,
		New(2026, time.September, 7): Independencia,
		New(2026, time.October, 12):  NossaSraAparecida,
		New(2026, time.November, 2):  Finados,
		New(2026, time.November, 15): ProclamacaoRepub,
		New(2026, time.December, 25): Natal,
	}
	for d, name := range want {
		got, ok := HolidayName(d)
		if !ok || got != name {
			t.Errorf("%s: got (%q, %v), want %q", d, got, ok, name)
		}
	}
}

func TestConscienciaNegraOnlyFrom2024(t *testing.T) {
	// Lei 14.759/2023 made 20 November national from 2024. Recomputing a 2023 due
	// date during an audit must still yield what was correct in 2023.
	if _, ok := HolidayName(New(2023, time.November, 20)); ok {
		t.Error("20 Nov 2023 must not be a national holiday")
	}
	name, ok := HolidayName(New(2024, time.November, 20))
	if !ok || name != ConscienciaNegra {
		t.Errorf("20 Nov 2024: got (%q, %v), want %q", name, ok, ConscienciaNegra)
	}
	if _, ok := HolidayName(New(2026, time.November, 20)); !ok {
		t.Error("20 Nov 2026 must be a national holiday")
	}
}

func TestNonHolidayIsNotFlagged(t *testing.T) {
	for _, d := range []Date{
		New(2026, time.August, 15),
		New(2026, time.March, 10),
		New(2026, time.July, 9), // state holiday in São Paulo — deliberately out of scope
	} {
		if IsHoliday(d) {
			t.Errorf("%s must not be a national holiday", d)
		}
	}
}

func TestRollForward(t *testing.T) {
	cases := []struct {
		name       string
		from, want Date
	}{
		{"business day is unchanged", New(2026, time.August, 13), New(2026, time.August, 13)}, // Thursday
		{"Saturday rolls to Monday", New(2026, time.August, 15), New(2026, time.August, 17)},
		{"Sunday rolls to Monday", New(2026, time.August, 16), New(2026, time.August, 17)},
		{"Friday holiday rolls to Monday", New(2026, time.May, 1), New(2026, time.May, 4)},
		{"Carnaval Monday rolls past Tuesday", New(2026, time.February, 16), New(2026, time.February, 18)},
		{"Good Friday rolls to Monday", New(2026, time.April, 3), New(2026, time.April, 6)},
		{"Corpus Christi (Thursday) rolls to Friday", New(2026, time.June, 4), New(2026, time.June, 5)},
		{"crosses into the next month", New(2026, time.January, 31), New(2026, time.February, 2)},
		{"31 Dec is not itself a holiday", New(2026, time.December, 31), New(2026, time.December, 31)}, // Thursday
		{"crosses into the next year", New(2022, time.December, 31), New(2023, time.January, 2)},       // Sat → Sun (New Year) → Mon
		{"Christmas 2026 (Friday) rolls to Monday", New(2026, time.December, 25), New(2026, time.December, 28)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RollForward(tc.from); got != tc.want {
				t.Fatalf("RollForward(%s) = %s (%s), want %s", tc.from, got, got.Weekday(), tc.want)
			}
		})
	}
}

func TestRollForwardAlwaysLandsOnABusinessDay(t *testing.T) {
	d := New(2024, time.January, 1)
	for range 366 * 6 {
		got := RollForward(d)
		if !IsBusinessDay(got) {
			t.Fatalf("RollForward(%s) = %s, which is not a business day", d, got)
		}
		if got.Before(d) {
			t.Fatalf("RollForward(%s) went backwards to %s", d, got)
		}
		d = d.AddDays(1)
	}
}
