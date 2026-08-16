package billing

import "gopkg.aoctech.app/billing/api/internal/domain/brcal"

// Proration: charging for part of a period.
//
// The rule the whole file follows is day-based and stated once here: the share
// of a period's price owed for [from, to) is
//
//	full * days(from, to) / days(period)
//
// rounded once, at the end, by MulDiv. Days are civil days in America/Sao_Paulo,
// so a 31-day month and a 28-day month genuinely differ — a customer who joins
// on the 15th of February pays a larger fraction than one who joins on the 15th
// of March, which is what "half a month" honestly means when the months differ.
//
// The alternative, a fixed 30-day month, makes the arithmetic prettier and the
// answer wrong: twelve 30-day months are 360 days, so an annual plan prorated
// that way loses five days of revenue and cannot be reconciled against a
// calendar.

// Prorate returns the portion of full owed for [from, to) inside period.
//
// from and to are clamped to the period, so a caller passing a range that starts
// before the period or ends after it gets the overlap rather than a nonsense
// amount. An empty or inverted range yields zero.
func Prorate(full Cents, period Period, from, to brcal.Date) Cents {
	total := period.Days()
	if total <= 0 {
		return 0
	}
	if from.Before(period.Start) {
		from = period.Start
	}
	if to.After(period.End) {
		to = period.End
	}
	covered := from.DaysBetween(to)
	if covered <= 0 {
		return 0
	}
	if covered >= total {
		return full
	}
	return MulDiv(full, int64(covered), int64(total))
}

// ProrateUsed returns the portion of full covering the part of period already
// served at date at — the amount to keep when someone leaves mid-period.
func ProrateUsed(full Cents, period Period, at brcal.Date) Cents {
	return Prorate(full, period, period.Start, at)
}

// ProrateRemaining returns the portion of full owed for the part of period that
// is still unserved at date at — the amount to charge someone joining mid-period.
//
// It is defined as the **complement** of ProrateUsed rather than as an
// independent calculation, and that is not a shortcut. Rounding both halves
// separately breaks on exact-half cases: 14 of 28 days of R$ 9,99 rounds to
// R$ 5,00 on each side, and the customer is billed one centavo more than the
// plan costs because they changed something mid-month. Deriving one side from the
// other makes used + remaining == full an identity instead of a coincidence.
func ProrateRemaining(full Cents, period Period, at brcal.Date) Cents {
	return full - ProrateUsed(full, period, at)
}

// Swap is the result of changing price mid-period. It is deliberately two
// amounts and never a single net figure.
//
// OVERVIEW.md § 6 already made this call and it is right: an invoice carrying one
// net line cannot be explained to a customer who asks "why R$ 37,42?". Two lines —
// a credit for what they will not use of the old price and a charge for what they
// will use of the new one — reconstruct the arithmetic on the invoice itself, and
// are what make the proration auditable years later.
type Swap struct {
	// Credit is the unused remainder of the old price, as a positive amount. The
	// caller writes it as a negative invoice line or a credit note.
	Credit Cents
	// Charge is the remaining share of the new price, as a positive amount.
	Charge Cents
	// RemainingDays and PeriodDays are carried so the invoice line can state the
	// fraction it was computed from instead of only its result.
	RemainingDays int
	PeriodDays    int
}

// Net is Charge − Credit: what the swap adds to the invoice total. It exists for
// assertions and totals, never as a substitute for the two lines.
func (s Swap) Net() Cents { return s.Charge - s.Credit }

// ProrateSwap computes a mid-period price change effective at date at.
//
// Both sides use the same day count, so swapping to an identical price nets to
// exactly zero rather than to a rounding residue.
func ProrateSwap(oldFull, newFull Cents, period Period, at brcal.Date) Swap {
	if at.Before(period.Start) {
		at = period.Start
	}
	if at.After(period.End) {
		at = period.End
	}
	remaining := at.DaysBetween(period.End)
	if remaining < 0 {
		remaining = 0
	}
	return Swap{
		Credit:        ProrateRemaining(oldFull, period, at),
		Charge:        ProrateRemaining(newFull, period, at),
		RemainingDays: remaining,
		PeriodDays:    period.Days(),
	}
}
