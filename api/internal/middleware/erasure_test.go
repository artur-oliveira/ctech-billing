package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

func TestRefuseLockedStopsOnlyALockedPerson(t *testing.T) {
	calls := 0
	blocked := func(_ context.Context, sub string) (bool, error) {
		calls++
		switch sub {
		case "gone":
			return true, nil
		case "broken":
			return false, errors.New("dynamodb: boom")
		}
		return false, nil
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, &Claims{Sub: c.Get("X-Sub"), SID: c.Get("X-Sid")})
		return c.Next()
	})
	app.Use(RefuseLocked(blocked))
	app.All("/", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	for _, tc := range []struct {
		name, method, sub, sid string
		want                   int
	}{
		{"an active person", "GET", "alive", "s1", 200},
		{"a locked person reading", "GET", "gone", "s1", 403},
		{"a locked person writing", "POST", "gone", "s1", 403},
		{"a service token whose client id happens to be the sub", "POST", "gone", "", 200},
		{"the state cannot be read", "GET", "broken", "s1", 500},
	} {
		req := httptest.NewRequest(tc.method, "/", nil)
		req.Header.Set("X-Sub", tc.sub)
		req.Header.Set("X-Sid", tc.sid)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, resp.StatusCode, tc.want, body)
		}
		if tc.want == 403 && !strings.Contains(string(body), problem.TypeAccountLocked) {
			t.Errorf("%s: body %s does not name %s", tc.name, body, problem.TypeAccountLocked)
		}
	}
	if calls != 4 {
		t.Fatalf("store asked %d times, want 4: a service token must not cost a lookup", calls)
	}
}
