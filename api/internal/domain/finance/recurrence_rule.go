package finance

import (
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Recurrence is a rule that produces bills (spec § 3.5). It holds a temporal
// expression through its Schedule, never a list of dates.
type Recurrence struct {
	ID          string
	Direction   Direction
	Amount      billing.Cents
	CategoryID  string
	AccountID   string
	Description string
	Schedule    Schedule
	// AutoSettle makes the daily job settle each materialised bill on its due
	// date, at the bill's own amount.
	AutoSettle bool
	// Archived stops materialisation. Recurrences are ended or archived, never
	// deleted: the bills they made keep pointing at them.
	Archived bool
}

// Validate refuses a recurrence the job could not materialise.
func (r Recurrence) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidRecurrence}, a...)...)
	}
	switch {
	case r.Direction != Payable && r.Direction != Receivable:
		return fail("direction must be payable or receivable")
	case r.Amount <= 0 || r.Amount > MaxLegAmount:
		return fail("amount must be between 1 and %d centavos", MaxLegAmount)
	case r.CategoryID == "":
		return fail("category is required")
	case r.AccountID == "":
		return fail("account is required")
	case len(r.Description) > maxBillDescription:
		return fail("description is at most %d characters", maxBillDescription)
	}
	return r.Schedule.Validate()
}

// Horizon is the window the job keeps materialised: from the first day of
// today's month to the last day of the next month, inclusive.
func Horizon(today brcal.Date) (from, to brcal.Date) {
	m := MonthOf(today)
	return m.First(), m.Add(1).Last()
}

// OccurrenceRef is an occurrence's identity: (recurrence, nominal day). The
// daily job guards it with a conditional lock row, so a re-run never duplicates.
func OccurrenceRef(recurrenceID string, nominal brcal.Date) string {
	return recurrenceID + "@" + nominal.String()
}

// Draft is a bill an occurrence produces, before the service gives it an id.
type Draft struct {
	Nominal brcal.Date
	Bill    Bill
}

// Materialise returns the bills owed for occurrences whose nominal day is in
// (cursor, end of the horizon]. A zero cursor means nothing has been
// materialised yet, so the recurrence is caught up from its Start: someone who
// enters "rent, since January" gets every month.
func (r Recurrence) Materialise(cursor, today brcal.Date) ([]Draft, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Archived {
		return nil, nil
	}
	_, to := Horizon(today)
	from := r.Schedule.Start
	if !cursor.IsZero() {
		from = cursor.AddDays(1)
	}
	if from.After(to) {
		return nil, nil
	}
	var out []Draft
	// Occurrences is bounded to MaxSpanDays, so walk the range in chunks: a
	// recurrence entered with a Start years ago is caught up without a refusal.
	for lo := from; !lo.After(to); {
		hi := lo.AddDays(MaxSpanDays)
		if hi.After(to.AddDays(1)) {
			hi = to.AddDays(1)
		}
		occ, err := r.Schedule.Occurrences(lo, hi)
		if err != nil {
			return nil, err
		}
		for _, o := range occ {
			out = append(out, Draft{Nominal: o.Nominal, Bill: Bill{
				Direction: r.Direction, Amount: r.Amount, AccountID: r.AccountID, CategoryID: r.CategoryID,
				Description: r.Description, Competence: o.Nominal, Due: o.Due,
				Status: BillForecast, Origin: OriginRecurrence, OriginRef: OccurrenceRef(r.ID, o.Nominal),
				AutoSettle: r.AutoSettle,
			}})
		}
		lo = hi
	}
	return out, nil
}

// NextMaterialiseDate is the first day the occurrence following `after` enters
// the horizon: the first day of the month before its month. ok is false when
// the schedule has no further occurrence.
func (r Recurrence) NextMaterialiseDate(after brcal.Date) (brcal.Date, bool) {
	if r.Archived {
		return brcal.Date{}, false
	}
	from := r.Schedule.Start
	if !after.IsZero() && !after.Before(from) {
		from = after.AddDays(1)
	}
	end := from.AddDays(MaxSpanDays)
	occ, err := r.Schedule.Occurrences(from, end)
	if err != nil || len(occ) == 0 {
		return brcal.Date{}, false
	}
	return MonthOf(occ[0].Nominal).Add(-1).First(), true
}

// MaxPreview bounds F4's "next occurrences before saving".
const MaxPreview = 24

// previewSpans bounds the search: 20 spans of MaxSpanDays is about a century, and
// a schedule with no occurrence in that long has none.
const previewSpans = 20

// Preview returns the next n occurrences on or after from, for the screen to
// show before anything is saved.
func Preview(s Schedule, from brcal.Date, n int) ([]Occurrence, error) {
	if n < 1 || n > MaxPreview {
		return nil, fmt.Errorf("%w: preview asks for 1..%d occurrences", ErrInvalidRecurrence, MaxPreview)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var out []Occurrence
	lo := from
	for i := 0; i < previewSpans && len(out) < n; i++ {
		hi := lo.AddDays(MaxSpanDays)
		occ, err := s.Occurrences(lo, hi)
		if err != nil {
			return nil, err
		}
		out = append(out, occ...)
		lo = hi
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
