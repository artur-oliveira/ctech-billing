package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// failResponse runs one error through the mapper and returns the response body
// and everything the instance wrote about it.
func failResponse(t *testing.T, err error) (status int, body string, logged string) {
	t.Helper()
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })

	app := fiber.New()
	app.Use(middleware.RequestID())
	app.Get("/x", func(c fiber.Ctx) error { return fail(c, err) })

	res, rerr := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if rerr != nil {
		t.Fatal(rerr)
	}
	defer func() { _ = res.Body.Close() }()
	b := make([]byte, 4096)
	n, _ := res.Body.Read(b)
	return res.StatusCode, string(b[:n]), buf.String()
}

// The response for a 500 says "erro interno" and must keep saying it — the
// client has no business learning that a table throttled. That makes the log
// the only place the real error survives, and before fail() wrote it there it
// survived nowhere: handlers return `fail(c, err)`, which sends the response and
// returns nil, so Fiber's ErrorHandler never ran for a handler error.
func TestInternalErrorsAreLoggedAndNotDisclosed(t *testing.T) {
	err := errors.New("dynamodb: ProvisionedThroughputExceededException on billing_invoices")
	status, body, logged := failResponse(t, err)

	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if strings.Contains(body, "ProvisionedThroughput") || strings.Contains(body, "billing_invoices") {
		t.Errorf("the 500 body leaks the cause: %s", body)
	}
	if logged == "" {
		t.Fatal("the instance recorded nothing about a 500")
	}

	var line struct {
		Level     string `json:"level"`
		Msg       string `json:"msg"`
		Error     string `json:"error"`
		RequestID string `json:"request_id"`
		Path      string `json:"path"`
	}
	if e := json.Unmarshal([]byte(logged), &line); e != nil {
		t.Fatalf("log line is not JSON: %v\n%s", e, logged)
	}
	if line.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR", line.Level)
	}
	if !strings.Contains(line.Error, "ProvisionedThroughputExceededException") {
		t.Errorf("the logged error is not the real one: %q", line.Error)
	}
	if line.RequestID == "" {
		t.Error("no request_id — the log cannot be tied to the response the caller saw")
	}
	if line.Path != "/x" {
		t.Errorf("path = %q", line.Path)
	}
}

// A 4xx is the caller being told no. It is already in the access log with its
// status, and logging it at error level teaches whoever reads the group that
// the level means nothing.
func TestClientErrorsAreNotLoggedAsFailures(t *testing.T) {
	for _, err := range []error{
		repositories.ErrNotFound,
		repositories.ErrConcurrentModification,
	} {
		status, _, logged := failResponse(t, err)
		if status >= 500 {
			t.Fatalf("%v mapped to %d", err, status)
		}
		if logged != "" {
			t.Errorf("%v was logged: %s", err, logged)
		}
	}
}
