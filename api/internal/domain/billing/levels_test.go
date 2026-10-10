package billing

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func lvl(v int64, y int, m time.Month, d, h int) LevelRecord {
	return LevelRecord{Value: v, OccurredAt: time.Date(y, m, d, h, 0, 0, 0, time.UTC)}
}

var october = Period{Start: brcal.New(2026, time.October, 1), End: brcal.New(2026, time.November, 1)}

func TestNoReportInThePeriodBillsTheCarriedInLevel(t *testing.T) {
	if got := MaxLevel(nil, 4, october); got != 4 {
		t.Fatalf("MaxLevel = %d, want 4", got)
	}
}

func TestAPeakInTheMiddleIsBilled(t *testing.T) {
	recs := []LevelRecord{lvl(5, 2026, time.October, 10, 15), lvl(2, 2026, time.October, 20, 15)}
	if got := MaxLevel(recs, 3, october); got != 5 {
		t.Fatalf("MaxLevel = %d, want 5", got)
	}
}

func TestADropBelowTheCarriedInLevelStillBillsTheCarriedIn(t *testing.T) {
	if got := MaxLevel([]LevelRecord{lvl(1, 2026, time.October, 2, 15)}, 3, october); got != 3 {
		t.Fatalf("MaxLevel = %d, want 3 (the level held on day 1)", got)
	}
}

// A subscription anchored on the 20th: its period starts on the 20th and what
// existed before is the carried-in level; a report on the 19th is outside.
func TestASubscriptionStartingMidMonthCountsFromItsStart(t *testing.T) {
	p := Period{Start: brcal.New(2026, time.October, 20), End: brcal.New(2026, time.November, 20)}
	recs := []LevelRecord{lvl(9, 2026, time.October, 19, 15), lvl(2, 2026, time.October, 25, 15)}
	if got := MaxLevel(recs, 1, p); got != 2 {
		t.Fatalf("MaxLevel = %d, want 2", got)
	}
}

// 02:30 UTC on 1 November is still 31 October in São Paulo.
func TestTheBoundaryIsTheSaoPauloDay(t *testing.T) {
	late := LevelRecord{Value: 8, OccurredAt: time.Date(2026, time.November, 1, 2, 30, 0, 0, time.UTC)}
	if got := MaxLevel([]LevelRecord{late}, 0, october); got != 8 {
		t.Fatalf("MaxLevel = %d, want 8", got)
	}
}

func TestIncludedQuantityIsSubtractedNeverBelowZero(t *testing.T) {
	if BillableUnits(MaxLevel(nil, 0, october), 1) != 0 || BillableUnits(MaxLevel(nil, 3, october), 1) != 2 {
		t.Fatal("included units")
	}
}

func TestALevelRecordIsValidated(t *testing.T) {
	ok := LevelRecord{CustomerRef: "USER_x", Meter: "finance_spaces", Value: 0, OccurredAt: time.Now(), IdempotencyKey: "k"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*LevelRecord){
		"no ref":    func(l *LevelRecord) { l.CustomerRef = "" },
		"bad meter": func(l *LevelRecord) { l.Meter = "a#b" },
		"negative":  func(l *LevelRecord) { l.Value = -1 },
		"no time":   func(l *LevelRecord) { l.OccurredAt = time.Time{} },
		"no key":    func(l *LevelRecord) { l.IdempotencyKey = "" },
	} {
		l := ok
		mut(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
