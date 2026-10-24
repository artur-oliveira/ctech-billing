package v1

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func healthApp(d Deps) *fiber.App {
	app := fiber.New()
	h := newHealth(d, time.Now)
	app.Get("/v1.0/health", h.Liveness())
	app.Get("/v1.0/health-check", h.Report())
	return app
}

// Liveness has no dependencies: HAProxy probes it with healthyStatuses=[200].
func TestLivenessIsAlways200WithNoDependencies(t *testing.T) {
	res, err := healthApp(Deps{}).Test(httptest.NewRequest("GET", "/v1.0/health", nil))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("liveness = %v, %v", res, err)
	}
}

// Nil dependencies are a partially wired deployment. The report describes that
// instead of panicking, as each one matters: no database is fatal (503), and the
// cache warn is not what makes it so.
func TestReportWithNoDatabaseIs503(t *testing.T) {
	res, err := healthApp(Deps{AppVersion: "x"}).Test(httptest.NewRequest("GET", "/v1.0/health-check", nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 503 {
		t.Fatalf("status = %d, want 503: billing cannot serve a request without its tables", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/health+json") {
		t.Errorf("content-type = %q", ct)
	}
}

// The offset is the whole point of the clock check: a host without tzdata reads
// 0 and looks correct on every other endpoint.
func TestClockReportsTheSaoPauloOffset(t *testing.T) {
	// Deep in the year so the assertion does not depend on a DST rule Brazil no
	// longer has.
	at := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	obs, err := clockCheck(func() time.Time { return at }).Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if obs.Warn {
		t.Fatalf("warn set; brcal.Location is %q", brcal.Location.String())
	}
	if obs.Value != -10800 || obs.Measurement != measureUTCOff {
		t.Errorf("obs = %+v, want utcOffset -10800", obs)
	}
}
