//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// A tenant over its M2M budget gets 429 with Retry-After and a code the console
// translates; under it the request reaches the handler (here a 404: no such user).
func TestAnM2MTenantOverItsBudgetIsToldToSlowDown(t *testing.T) {
	e := newAPIWith(t, func(c *config.Config) { c.M2MRatePerSecond, c.M2MBurst = 0.01, 3 })
	tok := e.token(t, e.client, "", middleware.ScopeCustomersRead)
	for i := 0; i < 3; i++ {
		if r := e.do(t, http.MethodGet, "/v1.0/customers/by-user/nobody", tok, "", ""); r.status != http.StatusNotFound {
			t.Fatalf("request %d inside the burst: %d %s", i, r.status, r.body)
		}
	}
	r := e.do(t, http.MethodGet, "/v1.0/customers/by-user/nobody", tok, "", "")
	if r.status != http.StatusTooManyRequests || r.code() != "too_many_requests" {
		t.Fatalf("over the budget: %d %s", r.status, r.body)
	}
	if secs, err := strconv.Atoi(r.header.Get("Retry-After")); err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q", r.header.Get("Retry-After"))
	}
}

// With the budget off (zero) nothing is limited.
func TestTheM2MBudgetCanBeTurnedOff(t *testing.T) {
	e := newAPIWith(t, func(c *config.Config) { c.M2MRatePerSecond, c.M2MBurst = 0, 0 })
	tok := e.token(t, e.client, "", middleware.ScopeCustomersRead)
	for i := 0; i < 30; i++ {
		if r := e.do(t, http.MethodGet, "/v1.0/customers/by-user/nobody", tok, "", ""); r.status == http.StatusTooManyRequests {
			t.Fatalf("request %d limited with the budget off", i)
		}
	}
}
