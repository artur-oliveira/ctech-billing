package main

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestCollectionSettingsOverrideOnlyWhatWasAsked(t *testing.T) {
	current := billing.CollectionSettings{FineBasisPoints: 100, MonthlyInterestBasisPoints: 50}
	cases := []struct {
		name           string
		fine, interest int
		want           billing.CollectionSettings
	}{
		{"nothing asked keeps both", -1, -1, current},
		{"fine only", 200, -1, billing.CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 50}},
		{"interest only", -1, 100, billing.CollectionSettings{FineBasisPoints: 100, MonthlyInterestBasisPoints: 100}},
		{"both", 200, 100, billing.CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 100}},
		{"zero is a value, not 'unset'", 0, 0, billing.CollectionSettings{}},
	}
	for _, c := range cases {
		got, err := collectionSettings(current, c.fine, c.interest)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
}

func TestCollectionSettingsRefuseWhatTheConsoleWouldRefuse(t *testing.T) {
	for _, c := range [][2]int{{2001, -1}, {-1, 1001}} {
		if _, err := collectionSettings(billing.CollectionSettings{}, c[0], c[1]); !errors.Is(err, billing.ErrInvalidCollectionSettings) {
			t.Errorf("%v: %v", c, err)
		}
	}
}
