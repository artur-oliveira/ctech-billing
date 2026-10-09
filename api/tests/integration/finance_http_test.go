//go:build integration

package integration

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// financeEnv drives the finance routes through the real app, in the signed-in
// user's personal space (no membership lookup needed).
type financeEnv struct {
	*apiEnv
	token string
}

func newFinanceEnv(t *testing.T) financeEnv {
	t.Helper()
	e := newAPI(t)
	return financeEnv{apiEnv: e, token: e.sessionToken(t, middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)}
}

func (f financeEnv) call(t *testing.T, method, path, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, "/v1.0/console/finance"+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set(middleware.ModeHeader, "live")
	req.Header.Set(middleware.SpaceHeader, "personal")
	if method != http.MethodGet {
		req.Header.Set(middleware.IdempotencyHeader, id.New())
	}
	resp, err := f.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

func (f financeEnv) must(t *testing.T, status int, method, path, body string, into any) {
	t.Helper()
	res := f.call(t, method, path, body)
	if res.status != status {
		t.Fatalf("%s %s = %d %s, want %d", method, path, res.status, res.body, status)
	}
	if into != nil {
		res.decode(t, into)
	}
}

// The console's own group resolves ONE organization per owner and needs the mode
// header. Fiber mounts a group's handlers as prefix middleware, so a /console
// group would also run in front of /console/finance. A person with no
// organization must still reach their personal space, and the space list needs
// no mode at all.
func TestFinanceIsNotBehindTheConsoleTenantResolver(t *testing.T) {
	e := newAPI(t)
	f := financeEnv{apiEnv: e, token: e.token(t, "user_"+id.New(), "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)}
	req := httptest.NewRequest(http.MethodGet, "/v1.0/console/finance/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := f.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /spaces without a mode = %d %s", resp.StatusCode, body)
	}
	f.must(t, 200, "GET", "/accounts", "", nil)
}
