package app

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/config"
)

func deadlineApp(t *testing.T, timeout time.Duration) *fiber.App {
	t.Helper()
	app := newFiber(&config.Config{RequestTimeout: timeout})
	wait := func(c fiber.Ctx) error {
		select {
		case <-c.Context().Done():
			return c.Context().Err()
		case <-time.After(300 * time.Millisecond):
			return c.SendString("finished")
		}
	}
	app.Get("/v1.0/customers", wait)
	app.Post("/v1.0/finance/imports", wait)
	app.Get("/v1.0/invoices/inv_1/pdf", wait)
	return app
}

func statusOf(t *testing.T, app *fiber.App, method, path string) int {
	t.Helper()
	res, err := app.Test(httptest.NewRequest(method, path, nil), fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode
}

// A call that outlives the request budget ends with the budget.
func TestARouteIsBoundedByTheRequestTimeout(t *testing.T) {
	res, err := deadlineApp(t, 40*time.Millisecond).Test(httptest.NewRequest("GET", "/v1.0/customers", nil), fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != fiber.StatusGatewayTimeout || !strings.Contains(string(body), "/problems/timeout") {
		t.Fatalf("status %d body %s, want a 504 problem of type /problems/timeout", res.StatusCode, body)
	}
}

// The statement upload and the first PDF render are the two routes that may take longer.
func TestUploadAndPDFAreNotBounded(t *testing.T) {
	app := deadlineApp(t, 40*time.Millisecond)
	if got := statusOf(t, app, "POST", "/v1.0/finance/imports"); got != 200 {
		t.Fatalf("upload: status %d, want 200", got)
	}
	if got := statusOf(t, app, "GET", "/v1.0/invoices/inv_1/pdf"); got != 200 {
		t.Fatalf("pdf: status %d, want 200", got)
	}
}

// Zero turns it off, which is what a Config literal without the field gets.
func TestZeroRequestTimeoutDisablesIt(t *testing.T) {
	if got := statusOf(t, deadlineApp(t, 0), "GET", "/v1.0/customers"); got != 200 {
		t.Fatalf("status %d, want 200", got)
	}
}
