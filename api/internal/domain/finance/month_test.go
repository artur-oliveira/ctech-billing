package finance

import (
	"testing"
	"time"
)

func TestParseMonthIsStrict(t *testing.T) {
	for in, ok := range map[string]bool{"2026-03": true, "2026-1": false, "2026-00": false, "2026-13": false, "": false, "2026-03-01": false} {
		m, err := ParseMonth(in)
		if (err == nil) != ok || (ok && m.String() != in) {
			t.Errorf("ParseMonth(%q) = %v, %v", in, m, err)
		}
	}
	if n := (Month{2027, time.December}).MonthsSince(Month{2026, time.January}); n != 23 {
		t.Errorf("MonthsSince = %d, want 23", n)
	}
}
