package finance

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrInvalidExpression wraps every reason an expression is refused.
var ErrInvalidExpression = errors.New("invalid temporal expression")

// Limits on what a stored expression may contain. The tree is user input that
// every daily job and every projection evaluates, so it is bounded on write.
const (
	maxExpressionDepth = 4
	maxDates           = 366
)

// exprJSON is the wire and storage form: a tagged union, one shape for every
// kind, with only that kind's fields set.
type exprJSON struct {
	Kind    string          `json:"kind"`
	Day     int             `json:"day,omitempty"`
	N       int             `json:"n,omitempty"`
	Weekday *time.Weekday   `json:"weekday,omitempty"`
	Every   int             `json:"every,omitempty"`
	Anchor  *brcal.Date     `json:"anchor,omitempty"`
	Month   time.Month      `json:"month,omitempty"`
	Months  []time.Month    `json:"months,omitempty"`
	Dates   []brcal.Date    `json:"dates,omitempty"`
	Include json.RawMessage `json:"include,omitempty"`
	Exclude json.RawMessage `json:"exclude,omitempty"`
}

// MarshalExpression renders e in its stored form.
func MarshalExpression(e Expression) ([]byte, error) {
	j, err := toJSON(e)
	if err != nil {
		return nil, err
	}
	return json.Marshal(j)
}

func toJSON(e Expression) (exprJSON, error) {
	j := exprJSON{Kind: e.kind()}
	switch v := e.(type) {
	case DayOfMonth:
		j.Day = v.Day
	case WorkdayOfMonth:
		j.N = v.N
	case NthWeekdayOfMonth:
		wd := v.Weekday
		j.Weekday, j.N = &wd, v.N
	case Weekly:
		wd, anchor := v.Weekday, v.Anchor
		j.Weekday, j.Every, j.Anchor = &wd, v.Every, &anchor
	case Yearly:
		j.Month, j.Day = v.Month, v.Day
	case MonthsOfYear:
		j.Months = v.Months
	case Dates:
		j.Dates = v.Days
	case Difference:
		inc, err := MarshalExpression(v.Include)
		if err != nil {
			return exprJSON{}, err
		}
		exc, err := MarshalExpression(v.Exclude)
		if err != nil {
			return exprJSON{}, err
		}
		j.Include, j.Exclude = inc, exc
	default:
		return exprJSON{}, fmt.Errorf("%w: unknown type %T", ErrInvalidExpression, e)
	}
	return j, nil
}

// ParseExpression reads and validates a stored expression. Validation happens
// here, once, so every Expression value in the program is one that was accepted.
func ParseExpression(b []byte) (Expression, error) {
	return parse(b, 1)
}

func parse(b []byte, depth int) (Expression, error) {
	if depth > maxExpressionDepth {
		return nil, fmt.Errorf("%w: nested deeper than %d", ErrInvalidExpression, maxExpressionDepth)
	}
	var j exprJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidExpression, err)
	}
	switch j.Kind {
	case "day_of_month":
		if j.Day < 1 || j.Day > 31 {
			return nil, fmt.Errorf("%w: day %d is not 1..31", ErrInvalidExpression, j.Day)
		}
		return DayOfMonth{Day: j.Day}, nil
	case "workday_of_month":
		if j.N != -1 && (j.N < 1 || j.N > 23) {
			return nil, fmt.Errorf("%w: business day %d is not 1..23 or -1", ErrInvalidExpression, j.N)
		}
		return WorkdayOfMonth{N: j.N}, nil
	case "nth_weekday_of_month":
		if j.Weekday == nil || *j.Weekday < time.Sunday || *j.Weekday > time.Saturday {
			return nil, fmt.Errorf("%w: weekday is required and must be 0..6", ErrInvalidExpression)
		}
		if j.N != -1 && (j.N < 1 || j.N > 4) {
			return nil, fmt.Errorf("%w: week %d is not 1..4 or -1", ErrInvalidExpression, j.N)
		}
		return NthWeekdayOfMonth{Weekday: *j.Weekday, N: j.N}, nil
	case "weekly":
		if j.Weekday == nil || *j.Weekday < time.Sunday || *j.Weekday > time.Saturday {
			return nil, fmt.Errorf("%w: weekday is required and must be 0..6", ErrInvalidExpression)
		}
		if j.Every < 1 || j.Every > 52 {
			return nil, fmt.Errorf("%w: every %d is not 1..52", ErrInvalidExpression, j.Every)
		}
		if j.Anchor == nil || j.Anchor.IsZero() || j.Anchor.Weekday() != *j.Weekday {
			return nil, fmt.Errorf("%w: anchor is required and must fall on the weekday", ErrInvalidExpression)
		}
		return Weekly{Weekday: *j.Weekday, Every: j.Every, Anchor: *j.Anchor}, nil
	case "yearly":
		if j.Month < time.January || j.Month > time.December {
			return nil, fmt.Errorf("%w: month %d is not 1..12", ErrInvalidExpression, j.Month)
		}
		if j.Day < 1 || j.Day > brcal.DaysInMonth(2028, j.Month) { // 2028: a leap year admits 29/02
			return nil, fmt.Errorf("%w: day %d does not exist in month %d", ErrInvalidExpression, j.Day, j.Month)
		}
		return Yearly{Month: j.Month, Day: j.Day}, nil
	case "months_of_year":
		if len(j.Months) == 0 || len(j.Months) > 12 {
			return nil, fmt.Errorf("%w: months must list 1..12 entries", ErrInvalidExpression)
		}
		for _, m := range j.Months {
			if m < time.January || m > time.December {
				return nil, fmt.Errorf("%w: month %d is not 1..12", ErrInvalidExpression, m)
			}
		}
		return MonthsOfYear{Months: j.Months}, nil
	case "dates":
		if len(j.Dates) > maxDates {
			return nil, fmt.Errorf("%w: more than %d dates", ErrInvalidExpression, maxDates)
		}
		return Dates{Days: j.Dates}, nil
	case "difference":
		if len(j.Include) == 0 || len(j.Exclude) == 0 {
			return nil, fmt.Errorf("%w: difference needs include and exclude", ErrInvalidExpression)
		}
		inc, err := parse(j.Include, depth+1)
		if err != nil {
			return nil, err
		}
		exc, err := parse(j.Exclude, depth+1)
		if err != nil {
			return nil, err
		}
		if !schedulable(inc) {
			return nil, fmt.Errorf("%w: %s cannot be the included side", ErrInvalidExpression, inc.kind())
		}
		return Difference{Include: inc, Exclude: exc}, nil
	default:
		return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalidExpression, j.Kind)
	}
}

// schedulable reports whether e may be what a recurrence fires on. Dates and
// MonthsOfYear exist to be excluded: "every day of December" is not a bill.
func schedulable(e Expression) bool {
	switch v := e.(type) {
	case Dates, MonthsOfYear:
		return false
	case Difference:
		return schedulable(v.Include)
	default:
		return true
	}
}

// ParseSchedule is ParseExpression for the expression a recurrence fires on: it
// also refuses an exclusion-only kind at the top.
func ParseSchedule(b []byte) (Expression, error) {
	e, err := ParseExpression(b)
	if err != nil {
		return nil, err
	}
	if !schedulable(e) {
		return nil, fmt.Errorf("%w: %s cannot be a recurrence's schedule", ErrInvalidExpression, e.kind())
	}
	return e, nil
}
