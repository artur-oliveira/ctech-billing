package v1

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// aggregate decides both the reported status and whether a balancer would pull
// the instance. Getting it backwards in either direction is expensive: fail
// where warn belonged takes healthy capacity out during an incident, and warn
// where fail belonged leaves an instance serving that cannot reach its data.
func TestAggregateTakesTheWorstStatus(t *testing.T) {
	entry := func(status string) healthEntry {
		return healthEntry{Status: status}
	}

	cases := []struct {
		name       string
		checks     map[string]healthEntry
		wantStatus string
		wantCode   int
	}{
		{
			name:       "all pass",
			checks:     map[string]healthEntry{"a": entry(statusPass), "b": entry(statusPass)},
			wantStatus: statusPass,
			wantCode:   200,
		},
		{
			// The everyday case: Valkey is down, billing still bills.
			name:       "one warn degrades without removing the instance",
			checks:     map[string]healthEntry{"a": entry(statusPass), "b": entry(statusWarn)},
			wantStatus: statusWarn,
			wantCode:   statusMultiStatus,
		},
		{
			name:       "one fail beats any number of warns",
			checks:     map[string]healthEntry{"a": entry(statusWarn), "b": entry(statusFail), "c": entry(statusPass)},
			wantStatus: statusFail,
			wantCode:   503,
		},
		{
			// Not reachable through healthCheck, which always builds six entries.
			// Asserted anyway: an empty map must not read as an outage.
			name:       "no checks is not a failure",
			checks:     map[string]healthEntry{},
			wantStatus: statusPass,
			wantCode:   200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := aggregate(tc.checks)
			if status != tc.wantStatus || code != tc.wantCode {
				t.Errorf("aggregate() = (%q, %d), want (%q, %d)", status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// "We could not measure it" and "it is fine" are different answers. Reporting
// the second for the first is how a saturated instance looks healthy.
func TestUnmeasurableUtilizationWarnsRatherThanPasses(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	cases := []struct {
		name string
		pct  float64
		want string
	}{
		{"idle", 3.2, statusPass},
		{"busy but under the threshold", utilizationWarnPercent, statusPass},
		{"over the threshold", utilizationWarnPercent + 0.1, statusWarn},
		{"unmeasurable", healthUnavailableV, statusWarn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := utilization(componentCPU, tc.pct, now)
			if got.Status != tc.want {
				t.Errorf("utilization(%v).Status = %q, want %q", tc.pct, got.Status, tc.want)
			}
			if got.ObservedUnit != unitPercent || got.ComponentType != typeSystem {
				t.Errorf("entry is not a system utilisation measurement: %+v", got)
			}
		})
	}
}

// The offset is the whole point of the clock check — a host without tzdata reads
// 0 and looks correct on every other endpoint.
func TestClockReportsTheSaoPauloOffset(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	// Deep in the year so the assertion does not depend on a DST rule Brazil no
	// longer has, and would notice if the loaded tzdata still applied one.
	got := checkClock(time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC), nowStr)

	if got.Status != statusPass {
		t.Fatalf("status = %q; brcal.Location is %q", got.Status, brcal.Location.String())
	}
	if got.ObservedValue != -10800 {
		t.Errorf("utcOffset = %v, want -10800 seconds", got.ObservedValue)
	}
	if got.MeasurementName != measureUTCOffset || got.ObservedUnit != unitSecond {
		t.Errorf("entry is not a UTC-offset measurement: %+v", got)
	}
	// The civil date, which is the fact somebody reads this endpoint for.
	if got.Output != "2026-08-16 America/Sao_Paulo" {
		t.Errorf("output = %q", got.Output)
	}
}

// Nil dependencies are what a partially wired deployment has. The report must
// describe that rather than panic — and it must describe it the way each one
// actually matters: no database is fatal, no cache is not.
func TestMissingDependenciesAreReportedNotFatal(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)

	db := checkDynamoDB(t.Context(), nil, "", nowStr)
	if db.Status != statusFail {
		t.Errorf("dynamodb with no client = %q, want fail: billing cannot serve a request without its tables", db.Status)
	}

	cache := checkCache(t.Context(), nil, nowStr)
	if cache.Status != statusWarn {
		t.Errorf("cache with no backend = %q, want warn: a single-instance deployment runs on the in-memory fallback", cache.Status)
	}

	for _, e := range []healthEntry{db, cache} {
		if e.ObservedValue != healthUnavailableV {
			t.Errorf("%s reported a measured value it never took: %+v", e.ComponentName, e)
		}
	}
}
