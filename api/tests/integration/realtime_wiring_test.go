//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func status(t *testing.T, e *apiEnv, path string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode
}

// With the flag off the routes do not exist and a transition writes no change:
// Billing behaves exactly as it did before realtime.
func TestWithRealtimeOffTheRoutesAre404AndNoChangeIsWritten(t *testing.T) {
	t.Cleanup(app.ResetRealtime)
	e := newAPIWith(t, func(c *config.Config) { c.RealtimeEnabled = false })
	if got := status(t, e, "/v1.0/ws", nil); got != http.StatusNotFound {
		t.Errorf("GET /v1.0/ws = %d, want 404", got)
	}
	if got := status(t, e, "/v1.0/realtime/changes", nil); got != http.StatusNotFound {
		t.Errorf("GET /v1.0/realtime/changes = %d, want 404", got)
	}

	// A repository built now has no realtime: an invoice that is paid writes nothing.
	org := newOrg(t, true)
	inv := payAndPublish(t, repositories.NewInvoiceRepository(testDB, testCfg), org)
	log := repositories.NewRealtimeRepository(testDB, testCfg)
	head, err := log.Head(ctxT(t), []string{realtime.TenantScope(org.ID, true), realtime.InvoiceScope(org.ID, true, inv.ID)})
	if err != nil || head != "" {
		t.Fatalf("a change was written with the flag off: head=%q err=%v", head, err)
	}
}

// With it on, both routes exist (and refuse an unauthenticated call the way they
// should) and the repositories built by the process write and publish.
func TestWithRealtimeOnTheRoutesExistAndTheRepositoriesAnnounce(t *testing.T) {
	t.Cleanup(app.ResetRealtime)
	e := newAPIWith(t, func(c *config.Config) { c.RealtimeEnabled = true })

	// Not a WebSocket handshake: the upgrader answers 400 (a 404 would mean no route).
	if got := status(t, e, "/v1.0/ws", nil); got == http.StatusNotFound {
		t.Errorf("GET /v1.0/ws = 404 with realtime on")
	}
	if got := status(t, e, "/v1.0/realtime/changes", nil); got != http.StatusUnauthorized {
		t.Errorf("GET /v1.0/realtime/changes without a credential = %d, want 401", got)
	}

	org := newOrg(t, true)
	inv := payAndPublish(t, repositories.NewInvoiceRepository(testDB, testCfg), org)
	log := repositories.NewRealtimeRepository(testDB, testCfg)
	got, _, err := log.Since(ctxT(t), []string{realtime.TenantScope(org.ID, true)}, id.NewAt(now().Add(-1e9*3600)), 20)
	if err != nil || len(got) < 2 || got[len(got)-1].Type != "invoice.paid" || got[len(got)-1].EntityID != inv.ID {
		t.Fatalf("changes = %+v (%v), want the finalize and the payment", got, err)
	}
}
