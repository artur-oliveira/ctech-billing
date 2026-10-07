package finance

import (
	"errors"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// BusinessDayAdjust says what happens to an occurrence that lands on a weekend
// or holiday. Only forward exists, for ADR 0006's reason: rolling backward would
// make something due before the date the person set.
type BusinessDayAdjust string

const (
	AdjustNone        BusinessDayAdjust = "none"
	AdjustRollForward BusinessDayAdjust = "roll_forward"
)

// ErrInvalidRecurrence wraps every reason a recurrence's dates are refused.
var ErrInvalidRecurrence = errors.New("invalid recurrence")

// Schedule is the date half of a recurrence: when it fires, between which days,
// and how a non-business day is handled. Amount, account and category are not
// here — they are the bill's business, not the calendar's.
type Schedule struct {
	Expression Expression
	Start      brcal.Date
	End        brcal.Date // zero: open-ended. Inclusive when set.
	Adjust     BusinessDayAdjust
}

// Occurrence is one firing of a schedule. Nominal is the day the expression
// produced and is the occurrence's identity — (recurrence id, Nominal) is what
// the materialisation lock row keys on, so moving Due never creates a second
// bill. Due is Nominal after the business-day adjustment.
type Occurrence struct {
	Nominal brcal.Date
	Due     brcal.Date
}

// Validate refuses a schedule that cannot fire as written.
func (s Schedule) Validate() error {
	switch {
	case s.Expression == nil:
		return errors.Join(ErrInvalidRecurrence, errors.New("expression is required"))
	case !schedulable(s.Expression):
		return errors.Join(ErrInvalidRecurrence, errors.New("expression only excludes days"))
	case expressionInvalid(s.Expression):
		return errors.Join(ErrInvalidRecurrence, errors.New("expression has out-of-range parameters"))
	case s.Start.IsZero():
		return errors.Join(ErrInvalidRecurrence, errors.New("start is required"))
	case !s.End.IsZero() && s.End.Before(s.Start):
		return errors.Join(ErrInvalidRecurrence, errors.New("end is before start"))
	case s.Adjust != AdjustNone && s.Adjust != AdjustRollForward:
		return errors.Join(ErrInvalidRecurrence, errors.New("unknown business-day adjustment"))
	}
	return nil
}

// Occurrences returns the occurrences whose **nominal** day is in [from, to),
// clipped to the schedule's own Start..End. Selecting by nominal day rather than
// by due day is deliberate: a 31/01 roll-forward lands in February but still
// belongs to the January occurrence, and selecting by due day would let one
// month's window miss it and the next month's catch it twice.
func (s Schedule) Occurrences(from, to brcal.Date) ([]Occurrence, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if from.Before(s.Start) {
		from = s.Start
	}
	if !s.End.IsZero() && s.End.AddDays(1).Before(to) {
		to = s.End.AddDays(1)
	}
	if !from.Before(to) {
		return nil, nil
	}
	days, err := Occurrences(s.Expression, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]Occurrence, len(days))
	for i, day := range days {
		due := day
		if s.Adjust == AdjustRollForward {
			due = brcal.RollForward(day) // a no-op on WorkdayOfMonth, which is already a business day
		}
		out[i] = Occurrence{Nominal: day, Due: due}
	}
	return out, nil
}

// expressionInvalid runs e through the stored form and the parser, so a Schedule
// built in code is held to exactly the rules of one read from storage.
func expressionInvalid(e Expression) bool {
	b, err := MarshalExpression(e)
	if err != nil {
		return true
	}
	_, err = ParseExpression(b)
	return err != nil
}
