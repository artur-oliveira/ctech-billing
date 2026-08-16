package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// Fiber resolves its default stream to os.Stdout at package init, so there is
// no swapping it from out here. The test mounts the real accessLog() — same
// format, same Skip — over a buffer instead.
func logRequest(t *testing.T, path string, headers map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	cfg := accessLog()
	cfg.Stream = &buf

	app := fiber.New()
	app.Use(logger.New(cfg))
	app.Use(middleware.RequestID())
	app.Get("/v1.0/health", func(c fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/v1.0/portal/session", func(c fiber.Ctx) error { return c.SendString("ok") })
	request(t, app, http.MethodGet, path, headers)
	return buf.String()
}

// The line has to be one object per request that CloudWatch Logs Insights can
// parse — the metric filters in terraform/billing/logging.tf select on `$.msg`
// and `$.status`, and a line that is not JSON matches nothing and alarms on
// nothing.
func TestAccessLogIsOneJSONObjectPerRequest(t *testing.T) {
	out := logRequest(t, "/v1.0/portal/session", nil)

	var line struct {
		Status    int    `json:"status"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		RequestID string `json:"request_id"`
		Latency   string `json:"latency"`
	}
	if err := json.Unmarshal([]byte(out), &line); err != nil {
		t.Fatalf("access log is not JSON: %v\nline: %q", err, out)
	}
	if line.Status != 200 || line.Method != http.MethodGet || line.Path != "/v1.0/portal/session" {
		t.Errorf("got %+v", line)
	}
	if line.Latency == "" {
		t.Error("no latency")
	}
}

// The field the siblings all have and none of them populate.
//
// `${request-id}` is not a Fiber tag; an unknown tag renders empty rather than
// failing, so ctech-wallet, ctech-poker and ctech-dfe all log `"request-id":""`
// on every request. This asserts billing does not — the id is the only thing
// that ties a support ticket to a request, and an empty one fails silently for
// as long as nobody looks.
func TestAccessLogCarriesTheRequestID(t *testing.T) {
	// Generated when the caller sends none.
	out := logRequest(t, "/v1.0/portal/session", nil)
	var line struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(out), &line); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if line.RequestID == "" {
		t.Fatal(`request_id is empty — the ${request-id} tag does not exist in Fiber`)
	}

	// Preserved when it does, so a trace survives the hop from another service.
	out = logRequest(t, "/v1.0/portal/session",
		map[string]string{middleware.RequestIDHeader: "rid-from-caller"})
	if err := json.Unmarshal([]byte(out), &line); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if line.RequestID != "rid-from-caller" {
		t.Errorf("request_id = %q, want the caller's", line.RequestID)
	}
}

// HAProxy probes liveness every few seconds forever. Logged, the probe
// outnumbers the traffic in the group somebody has to read during an incident —
// and nginx already drops it from its own access log.
func TestHealthIsNotAccessLogged(t *testing.T) {
	out := logRequest(t, "/v1.0/health", nil)
	if out != "" {
		t.Errorf("health was logged: %q", out)
	}
}

// Fiber colours its output whenever the stream is the default stdout, and it
// colours the values, not the keys — `"status":<esc>[92m200<esc>[0m`. The
// escape codes land inside the JSON, which parses as nothing and matches no
// metric filter. DisableColors is what stops it, and nothing else in the config
// implies it.
func TestAccessLogCarriesNoTerminalEscapes(t *testing.T) {
	out := logRequest(t, "/v1.0/portal/session", nil)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("ANSI escape in the access log: %q", out)
	}
}
