package brcal

import "time"

// Brazilian national non-business days, computed rather than tabulated.
//
// ARCHITECTURE.md § 4 asks for a pure function and not a maintained table, for a
// good reason: a table needs someone to remember to extend it, and the year it
// is forgotten every due date in the service is wrong with no error anywhere.
//
// Scope is **national only** (ADR 0006). Municipal and state holidays are
// deliberately excluded: they would make a due date depend on the customer's
// address, which is data billing chose not to hold.

// Holiday names, in Portuguese because that is how they are shown and logged.
const (
	Confraternizacao  = "Confraternização Universal"
	Carnaval          = "Carnaval"
	SextaFeiraSanta   = "Sexta-feira Santa"
	Tiradentes        = "Tiradentes"
	DiaDoTrabalho     = "Dia do Trabalho"
	CorpusChristi     = "Corpus Christi"
	Independencia     = "Independência do Brasil"
	NossaSraAparecida = "Nossa Senhora Aparecida"
	Finados           = "Finados"
	ProclamacaoRepub  = "Proclamação da República"
	ConscienciaNegra  = "Dia Nacional de Zumbi e da Consciência Negra"
	Natal             = "Natal"
)

// ConscienciaNegraFirstYear is the first year 20 November is a national holiday.
// Lei 14.759/2023 created it; before 2024 it was municipal/state only. Encoded as
// a year condition rather than added unconditionally so that recomputing a 2023
// due date — during a dispute, an audit, or a backfill — still yields the date
// that was actually correct then.
const ConscienciaNegraFirstYear = 2024

type monthDay struct {
	month time.Month
	day   int
}

var fixedHolidays = map[monthDay]string{
	{time.January, 1}:   Confraternizacao,
	{time.April, 21}:    Tiradentes,
	{time.May, 1}:       DiaDoTrabalho,
	{time.September, 7}: Independencia,
	{time.October, 12}:  NossaSraAparecida,
	{time.November, 2}:  Finados,
	{time.November, 15}: ProclamacaoRepub,
	{time.December, 25}: Natal,
}

// Easter returns Easter Sunday for the given year in the Gregorian calendar,
// using the Meeus/Jones/Butcher anonymous algorithm. Valid for any Gregorian
// year; the tests pin it against a table of known dates as a regression check,
// but production always computes.
func Easter(year int) Date {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := ((h + l - 7*m + 114) % 31) + 1
	return Date{Year: year, Month: time.Month(month), Day: day}
}

// HolidayName returns the name of the national holiday falling on d, and whether
// there is one.
func HolidayName(d Date) (string, bool) {
	if name, ok := fixedHolidays[monthDay{d.Month, d.Day}]; ok {
		return name, true
	}
	if d.Month == time.November && d.Day == 20 && d.Year >= ConscienciaNegraFirstYear {
		return ConscienciaNegra, true
	}
	easter := Easter(d.Year)
	switch d.DaysBetween(easter) { // days from d to Easter
	case 48, 47:
		return Carnaval, true // Monday and Tuesday before Ash Wednesday
	case 2:
		return SextaFeiraSanta, true
	case -60:
		return CorpusChristi, true
	}
	return "", false
}

// IsHoliday reports whether d is a national holiday.
func IsHoliday(d Date) bool {
	_, ok := HolidayName(d)
	return ok
}

// IsWeekend reports whether d falls on a Saturday or Sunday.
func IsWeekend(d Date) bool {
	wd := d.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// IsBusinessDay reports whether d is a weekday that is not a national holiday.
func IsBusinessDay(d Date) bool { return !IsWeekend(d) && !IsHoliday(d) }

// RollForward returns d if it is a business day, otherwise the next business
// day after it (ADR 0006). It is deliberately not paired with a RollBackward:
// rolling backward would charge the customer before the date the contract says,
// and having both in the codebase invites picking the wrong one.
//
// It may cross a month or year boundary — that is allowed, and it never moves
// the invoice's accrual period, which is computed before this adjustment.
func RollForward(d Date) Date {
	// Bounded so a bug in the holiday rules can never spin forever: the longest
	// possible run of consecutive non-business days in this calendar is a handful.
	for range 15 {
		if IsBusinessDay(d) {
			return d
		}
		d = d.AddDays(1)
	}
	panic("brcal: more than 15 consecutive non-business days — holiday rules are wrong")
}
