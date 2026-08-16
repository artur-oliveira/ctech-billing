// Package brcal holds the civil-date type used across billing and the Brazilian
// business calendar built on it.
//
// Why a Date type instead of time.Time: a billing period, a due date and an
// anchor day are civil dates, not instants. Carrying them as time.Time invites
// two bugs that cost real money — an unnormalized clock component making two
// equal dates compare unequal, and a UTC instant naming a different day than the
// customer's day. "Today" in this system is always decided in America/Sao_Paulo
// (assessment § 13, ADR 0002): an invoice due 01/03 at 00:30 BRT is 28/02 in UTC.
//
// Timestamps (created_at, paid_at) stay time.Time in UTC. Only dates use Date.
package brcal

import (
	"fmt"
	"time"
	_ "time/tzdata" // billing decides "today" in America/Sao_Paulo on hosts with no tz database
)

// Location is the timezone every "what day is it" question in billing is
// answered in. Imported tzdata above guarantees it resolves inside a scratch
// container too, so this can panic on failure: there is no sane fallback — a
// billing service that silently computes days in UTC bills on the wrong day.
var Location = mustLoadLocation("America/Sao_Paulo")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("brcal: cannot load timezone " + name + ": " + err.Error())
	}
	return loc
}

// Date is a civil date — a calendar day with no time and no zone.
// The zero value is not a valid date; construct with New, FromTime, or Parse.
//
// **Every method except UnmarshalText takes a value receiver, and that is load
// bearing rather than style.** Date is a three-word comparable struct stored as
// a value field on Invoice, Subscription and Period; a pointer receiver on
// MarshalText would mean the value type does not implement
// encoding.TextMarshaler, and every date in JSON and in DynamoDB would silently
// serialize as {"Year":…,"Month":…,"Day":…} instead of "YYYY-MM-DD".
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// New returns the Date for y-m-d, normalizing out-of-range components the same
// way time.Date does (New(2026, 1, 32) is 2026-02-01).
func New(y int, m time.Month, d int) Date {
	return FromTime(time.Date(y, m, d, 0, 0, 0, 0, Location))
}

// FromTime returns the civil date t names in Location. Callers passing a UTC
// instant get the São Paulo day, which is the intended behavior everywhere in
// billing — see the package comment.
func FromTime(t time.Time) Date {
	t = t.In(Location)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// Today returns the current civil date in Location.
func Today() Date { return FromTime(time.Now()) }

// Parse reads a date in YYYY-MM-DD form.
func Parse(s string) (Date, error) {
	t, err := time.ParseInLocation(time.DateOnly, s, Location)
	if err != nil {
		return Date{}, fmt.Errorf("brcal: parse %q: %w", s, err)
	}
	return FromTime(t), nil
}

// Time returns midnight of d in Location. This is the only sanctioned way to
// turn a Date back into an instant.
func (d Date) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, Location)
}

// String renders YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// IsZero reports whether d is the zero value (an unset date).
func (d Date) IsZero() bool { return d == Date{} }

// Weekday returns the day of the week.
func (d Date) Weekday() time.Weekday { return d.Time().Weekday() }

// AddDays returns d shifted by n days (n may be negative).
func (d Date) AddDays(n int) Date { return FromTime(d.Time().AddDate(0, 0, n)) }

// AddMonths returns d shifted by n months, **clamping to the last day of the
// target month** instead of overflowing into the next one.
//
// This is the single most expensive off-by-one in subscription billing:
// time.AddDate(0, 1, 0) turns 31 January into 2 or 3 March, so a subscription
// anchored on the 31st silently skips February and bills twice in March. Clamped,
// it lands on the 28th/29th and the anchor day is preserved for later periods
// because the anchor — not the previous period's date — is what gets shifted.
func (d Date) AddMonths(n int) Date {
	y, m := d.Year, int(d.Month)+n
	// Normalize the month into 1..12, carrying into the year.
	y += (m - 1) / 12
	m = (m-1)%12 + 1
	if m <= 0 {
		m += 12
		y--
	}
	day := d.Day
	if last := DaysInMonth(y, time.Month(m)); day > last {
		day = last
	}
	return Date{Year: y, Month: time.Month(m), Day: day}
}

// AddYears returns d shifted by n years, clamping 29 February to the 28th in a
// non-leap year for the same reason AddMonths clamps.
func (d Date) AddYears(n int) Date { return d.AddMonths(12 * n) }

// DaysInMonth returns the number of days in the given month.
func DaysInMonth(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, Location).Day()
}

// Before reports whether d is strictly before o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is strictly after o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

// Compare returns -1, 0 or +1 as d is before, equal to, or after o.
func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return sign(d.Year - o.Year)
	case d.Month != o.Month:
		return sign(int(d.Month) - int(o.Month))
	default:
		return sign(d.Day - o.Day)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// DaysBetween returns the number of days from d to o, negative if o precedes d.
// The interval is half-open: DaysBetween(x, x) is 0.
//
// Both ends are converted to UTC midnight before subtracting so a DST
// transition in Location cannot shorten or lengthen the count. Brazil has no DST
// today, but it has had it twice in this timezone's history and the calculation
// must not depend on that staying true.
func (d Date) DaysBetween(o Date) int {
	const day = 24 * time.Hour
	du := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
	ou := time.Date(o.Year, o.Month, o.Day, 0, 0, 0, 0, time.UTC)
	return int(ou.Sub(du) / day)
}

// MarshalText implements encoding.TextMarshaler, so Date serializes as
// "YYYY-MM-DD" in JSON and in DynamoDB attribute values.
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
