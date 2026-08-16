//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// The RFC health report, against the real DynamoDB the rest of this package
// uses (draft-inadarei-api-health-check).

type healthEntry struct {
	ComponentName   string  `json:"componentName"`
	MeasurementName string  `json:"measurementName"`
	ComponentType   string  `json:"componentType"`
	ObservedValue   float64 `json:"observedValue"`
	ObservedUnit    string  `json:"observedUnit"`
	Status          string  `json:"status"`
	Time            string  `json:"time"`
	Output          string  `json:"output"`
}

type healthReport struct {
	Status      string                 `json:"status"`
	Version     string                 `json:"version"`
	ReleaseID   string                 `json:"releaseId"`
	ServiceID   string                 `json:"serviceId"`
	Description string                 `json:"description"`
	Checks      map[string]healthEntry `json:"checks"`
}

func (e *apiEnv) report(t *testing.T) (int, healthReport) {
	t.Helper()
	res := e.do(t, http.MethodGet, "/v1.0/health-check", "", "", "")
	// 200 or 207, never 503 here: DynamoDB is up, and the only other checks that
	// can degrade are CPU and memory. **Not asserting 200** is deliberate —
	// cpuPercent reads utilisation since boot, and a loaded CI runner legitimately
	// sits above the warn threshold. A test that demanded 200 would fail for a
	// reason that has nothing to do with billing.
	if res.status != http.StatusOK && res.status != 207 {
		t.Fatalf("status = %d, want 200 or 207: %s", res.status, res.body)
	}
	var body healthReport
	res.decode(t, &body)
	return res.status, body
}

func TestHealthCheckReportsEveryDependency(t *testing.T) {
	e := newAPI(t)
	_, body := e.report(t)

	if body.ServiceID != "CTech Billing" {
		t.Errorf("serviceId = %q", body.ServiceID)
	}
	if body.ReleaseID == "" {
		t.Error("releaseId is empty; a report that cannot say which build answered is a report you cannot act on")
	}

	// Every one of these is something an operator asks about during an incident,
	// and a missing key is indistinguishable from a healthy one on a dashboard.
	for _, name := range []string{"uptime", "cpu", "memory", "dynamodb", "cache", "clock"} {
		entry, ok := body.Checks[name]
		if !ok {
			t.Errorf("no %q check in the report", name)
			continue
		}
		if entry.Status == "" || entry.Time == "" || entry.ComponentType == "" {
			t.Errorf("%q entry is not a complete RFC entry: %+v", name, entry)
		}
	}
}

// DynamoDB is the one check allowed to fail the report, so it is the one whose
// passing has to be asserted rather than assumed — against the real tables this
// package creates, under the prefix this deployment is configured with.
func TestHealthCheckProvesTheConfiguredTableExists(t *testing.T) {
	e := newAPI(t)
	_, body := e.report(t)

	db := body.Checks["dynamodb"]
	if db.Status != "pass" {
		t.Fatalf("dynamodb = %q (%+v); the table prefix does not resolve to a real table", db.Status, db)
	}
	if db.ObservedValue < 0 {
		t.Errorf("observedValue = %v, want a measured latency", db.ObservedValue)
	}
	if db.ObservedUnit != "millisecond" {
		t.Errorf("observedUnit = %q", db.ObservedUnit)
	}
}

// The check that exists because its failure is silent everywhere else: a host
// running in UTC bills a day early for every event after 21:00.
func TestHealthCheckReportsTheBillingTimezone(t *testing.T) {
	e := newAPI(t)
	_, body := e.report(t)

	clock := body.Checks["clock"]
	if clock.Status != "pass" {
		t.Fatalf("clock = %q (%+v)", clock.Status, clock)
	}
	// -03:00. A host without tzdata reports 0 here and looks fine everywhere else.
	if clock.ObservedValue != -10800 {
		t.Errorf("utcOffset = %v seconds, want -10800 (America/Sao_Paulo)", clock.ObservedValue)
	}
}

// Liveness stays dependency-free and stays 200. It is what HAProxy probes with
// `healthyStatuses = [200]` and `autoHeal = true`, so anything else it could
// return is an instance getting replaced.
func TestLivenessIsSeparateFromTheReport(t *testing.T) {
	e := newAPI(t)
	res := e.do(t, http.MethodGet, "/v1.0/health", "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body map[string]any
	res.decode(t, &body)

	if body["status"] != "pass" {
		t.Errorf("status = %v", body["status"])
	}
	if _, ok := body["checks"]; ok {
		t.Error("liveness carries dependency checks; the probe that must never fail now can")
	}
}
