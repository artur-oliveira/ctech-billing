package finance

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestNewTransactionRefusesLegsThatWrapInt64(t *testing.T) {
	cases := map[string][]Leg{
		"three legs wrapping to zero": {{AccountID: "a", Amount: math.MaxInt64}, {AccountID: "b", Amount: math.MaxInt64}, {AccountID: "c", Amount: 2}},
		"two credits of MinInt64":     {{AccountID: "a", Amount: math.MinInt64}, {AccountID: "b", Amount: math.MinInt64}},
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

func TestNewTransactionRefusesTheSameAccountTwice(t *testing.T) {
	_, err := NewTransaction(KindTransfer, d(2026, time.March, 2), Leg{AccountID: "a", Amount: 100}, Leg{AccountID: "a", Amount: -100})
	if !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("err = %v, want ErrInvalidTransaction", err)
	}
}

// ADD on a summary only ever grows. The per-leg bound must leave room for
// millions of legs on one account in one month before int64 can wrap.
func TestMaxLegAmountLeavesRoomForAMonthOfActivity(t *testing.T) {
	if MaxLegAmount > math.MaxInt64/5_000_000 {
		t.Fatalf("MaxLegAmount = %d: five million max-size legs would overflow a summary", MaxLegAmount)
	}
}

func TestLedgerAccountIDsSortInsideTheSummaryRange(t *testing.T) {
	for _, id := range []string{"água", "é", "~x", "a~", "a b", "a\x7f", "a/b", "a#b", ""} {
		a := LedgerAccount{ID: id, Name: "n", Class: ClassAsset}
		if err := a.Validate(); !errors.Is(err, ErrInvalidAccount) {
			t.Errorf("id %q was accepted", id)
		}
	}
	for _, id := range []string{"bank", "01J9ZXQ0ABC", "sys-payables", "a_b-C9"} {
		a := LedgerAccount{ID: id, Name: "n", Class: ClassAsset}
		if err := a.Validate(); err != nil {
			t.Errorf("id %q refused: %v", id, err)
		}
	}
}

func TestNewTransactionRefusesAnUnknownKind(t *testing.T) {
	_, err := NewTransaction(TxKind("whatever"), d(2026, time.March, 2), Leg{AccountID: "a", Amount: 1}, Leg{AccountID: "b", Amount: -1})
	if !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("err = %v", err)
	}
}
