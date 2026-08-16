package billing

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Interval is the unit of a recurrence.
type Interval string

const (
	IntervalDay   Interval = "day"
	IntervalWeek  Interval = "week"
	IntervalMonth Interval = "month"
	IntervalYear  Interval = "year"
)

// BillingTiming says whether a period is charged before or after it is served.
//
// This is a **separate axis** from Interval, which resolves the inconsistency
// README.md flags as backlog B37 ("FIXED_MONTHLY vs billing_timing=ADVANCE"):
// those were never two values of one enum. "Monthly" is the recurrence;
// "in advance" is when the invoice for it comes due. A fixed monthly plan billed
// in arrears and a metered monthly plan billed in arrears share a recurrence and
// differ in nothing here.
type BillingTiming string

const (
	// BillAdvance charges at the start of the period it covers. Correct for fixed
	// prices: the customer pays for access they are about to get.
	BillAdvance BillingTiming = "advance"
	// BillArrears charges at the end of the period. Required for metered prices —
	// the amount is not known until the period closes.
	BillArrears BillingTiming = "arrears"
)

// ErrInvalidRecurrence reports a recurrence that cannot produce periods.
var ErrInvalidRecurrence = errors.New("invalid recurrence")

// Recurrence is "every Count Intervals" — every 1 month, every 3 months, every
// 1 year.
type Recurrence struct {
	Interval Interval `dynamodbav:"interval" json:"interval"`
	Count    int      `dynamodbav:"count"    json:"count"`
}

// Validate reports whether the recurrence can generate periods.
func (r Recurrence) Validate() error {
	switch r.Interval {
	case IntervalDay, IntervalWeek, IntervalMonth, IntervalYear:
	default:
		return fmt.Errorf("%w: unknown interval %q", ErrInvalidRecurrence, r.Interval)
	}
	if r.Count < 1 {
		return fmt.Errorf("%w: count must be at least 1, got %d", ErrInvalidRecurrence, r.Count)
	}
	return nil
}

// Shift returns anchor advanced by n recurrences (n may be negative).
//
// Month and year steps clamp to the end of the target month
// (brcal.Date.AddMonths), so an anchor on the 31st never skips February.
func (r Recurrence) Shift(anchor brcal.Date, n int) brcal.Date {
	steps := r.Count * n
	switch r.Interval {
	case IntervalDay:
		return anchor.AddDays(steps)
	case IntervalWeek:
		return anchor.AddDays(7 * steps)
	case IntervalMonth:
		return anchor.AddMonths(steps)
	case IntervalYear:
		return anchor.AddYears(steps)
	default:
		panic("billing: Shift on unvalidated recurrence " + string(r.Interval))
	}
}

// Period is the service window an invoice covers: half-open, [Start, End).
//
// Half-open is what makes consecutive periods tile the calendar with no gap and
// no shared day. A closed interval would double-count the boundary day in
// metered usage — one usage record landing in two invoices.
type Period struct {
	Start brcal.Date `dynamodbav:"period_start" json:"period_start"`
	End   brcal.Date `dynamodbav:"period_end"   json:"period_end"`
}

// PeriodAt returns the nth period counted from anchor, 0-based: PeriodAt(a, 0)
// is the first period, starting on the anchor itself.
//
// Every period is derived from the anchor, never from the previous period's
// dates. Chaining would let a single end-of-month clamp move the billing day
// permanently: 31 Jan → 28 Feb → 28 Mar, and the customer's billing day has
// silently changed forever.
func (r Recurrence) PeriodAt(anchor brcal.Date, n int) Period {
	return Period{Start: r.Shift(anchor, n), End: r.Shift(anchor, n+1)}
}

// Days is the length of the period in days.
func (p Period) Days() int { return p.Start.DaysBetween(p.End) }

// Contains reports whether d falls in [Start, End).
func (p Period) Contains(d brcal.Date) bool {
	return !d.Before(p.Start) && d.Before(p.End)
}

// PeriodToInvoice returns the period the daily sweep bills when it fires for a
// subscription, which depends entirely on the timing:
//
//   - In arrears, the sweep fires as a period closes, and bills the period that
//     just ended — the only moment metered usage is knowable.
//   - In advance, the same firing bills the period about to start.
//
// Both share one sweep date (the boundary between the two periods), which is why
// the schedule index needs no timing-specific key. Getting this backwards bills
// a customer for a period they already paid for, or for one they have not had.
//
// The **first** period of a subscription billed in advance is never produced
// here: the sweep only fires at a period boundary, and period 0 has none behind
// it. That invoice is created at subscribe time, which is also what makes the
// first payment the thing that moves INCOMPLETE to ACTIVE.
func PeriodToInvoice(s *Subscription) Period {
	if s.Timing == BillArrears {
		return s.CurrentPeriod()
	}
	return s.NextPeriod()
}

// DueDate returns the date the invoice for p falls due: the base date implied by
// the timing, plus netDays, rolled forward to the next business day (ADR 0006).
//
// Two properties this must preserve, both of which cost money if broken:
//
//   - The accrual period is computed **before** the adjustment and never moves.
//     Rolling a December due date into January does not move the invoice into the
//     next accrual year — otherwise twelve annual cycles become eleven or thirteen.
//   - The dunning clock starts from the date this returns, not from the
//     unadjusted one. Otherwise the customer is marked late for a day on which
//     they could not pay.
func DueDate(p Period, timing BillingTiming, netDays int) brcal.Date {
	base := p.Start
	if timing == BillArrears {
		base = p.End
	}
	return brcal.RollForward(base.AddDays(netDays))
}
