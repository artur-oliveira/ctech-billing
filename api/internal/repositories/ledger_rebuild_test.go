package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
)

func key(y int, m time.Month, acct string) finance.SummaryKey {
	return finance.SummaryKey{Month: finance.Month{Year: y, Month: m}, AccountID: acct}
}

func TestCompareFindsNoDriftWhenCacheEqualsDerivation(t *testing.T) {
	d := finance.Derived{
		Balances: map[string]billing.Cents{"a": 100, "b": -100},
		Summary: map[finance.SummaryKey]finance.Totals{
			key(2026, time.March, "a"): {Debits: 100},
			key(2026, time.March, "b"): {Credits: 100},
		},
	}
	s := StoredLedger{Balances: map[string]billing.Cents{"a": 100, "b": -100}, Summary: d.Summary}
	if got := Compare(s, d); len(got) != 0 {
		t.Fatalf("drift = %+v", got)
	}
}

func TestCompareReportsEveryKindOfDrift(t *testing.T) {
	d := finance.Derived{
		Balances: map[string]billing.Cents{"a": 100, "c": 50},
		Summary: map[finance.SummaryKey]finance.Totals{
			key(2026, time.March, "a"): {Debits: 100},
			key(2026, time.March, "c"): {Debits: 50},
		},
	}
	s := StoredLedger{
		Balances: map[string]billing.Cents{"a": 90, "ghost": 7}, // a wrong; ghost has no entries; c missing
		Summary: map[finance.SummaryKey]finance.Totals{
			key(2026, time.March, "a"):    {Debits: 90},
			key(2026, time.February, "a"): {Debits: 5}, // stored with no derivation
		},
	}
	got := Compare(s, d)
	want := map[string]bool{"balance:a": false, "balance:ghost": false, "balance:c": false,
		"summary:2026-03:a": false, "summary:2026-03:c": false, "summary:2026-02:a": false}
	for _, dr := range got {
		k := dr.Kind + ":" + dr.Account
		if dr.Month != "" {
			k = dr.Kind + ":" + dr.Month + ":" + dr.Account
		}
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected drift %s", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing drift %s", k)
		}
	}
}
