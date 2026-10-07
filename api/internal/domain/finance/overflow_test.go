package finance

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestNewTransactionRefusesLegsThatWrapInt64(t *testing.T) {
	cases := map[string][]Leg{
		"three legs wrapping to zero": {{"a", math.MaxInt64}, {"b", math.MaxInt64}, {"c", 2}},
		"two credits of MinInt64":     {{"a", math.MinInt64}, {"b", math.MinInt64}},
	}
	for name, legs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTransaction(KindTransfer, d(2026, time.March, 2), legs...); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("err = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}

func TestOpeningBalanceRefusesMinInt64(t *testing.T) {
	sys := SystemAccounts{OpeningBalance: "opening"}
	if _, err := OpeningBalance(sys, "checking", math.MinInt64, d(2026, time.March, 2)); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("err = %v, want ErrInvalidTransaction", err)
	}
}

func TestScheduleValidateRefusesExpressionsThatWouldPanic(t *testing.T) {
	for name, e := range map[string]Expression{
		"workday 0":      WorkdayOfMonth{N: 0},
		"weekly every 0": Weekly{Weekday: time.Friday, Every: 0, Anchor: d(2026, time.January, 2)},
		"day 0":          DayOfMonth{Day: 0},
		"nested":         Difference{Include: WorkdayOfMonth{N: 0}, Exclude: Dates{}},
	} {
		t.Run(name, func(t *testing.T) {
			s := Schedule{Expression: e, Start: d(2026, time.January, 1), Adjust: AdjustNone}
			if err := s.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
				t.Fatalf("Validate = %v, want ErrInvalidRecurrence", err)
			}
		})
	}
}
