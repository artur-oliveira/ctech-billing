//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

type closureEnv struct {
	*apiEnv
	mail *closureFakes
}

func newClosureEnv(t *testing.T) *closureEnv {
	t.Helper()
	f := &closureFakes{owner: true}
	e := newAPIWith(t, func(cfg *config.Config) {
		cfg.TenantClosureEnabled = true
		cfg.PortalOrganizationID = "ten_zero_" + newSpaceOrgID()
	}, app.WithClosureMailer(f))
	return &closureEnv{apiEnv: e, mail: f}
}

func (e *closureEnv) ownerToken(t *testing.T, mfaAge time.Duration) string {
	t.Helper()
	extra := map[string]any{}
	if mfaAge >= 0 {
		extra["last_mfa_at"] = time.Now().Add(-mfaAge).Unix()
	}
	return e.tokenWith(t, extra, e.org.OwnerUserID, "sess_"+newSpaceOrgID(), middleware.ScopeOrganizationRead, middleware.ScopeOrganizationWrite)
}

func (e *closureEnv) call(t *testing.T, method, path, token, body string) apiResponse {
	t.Helper()
	return e.consoleAs(t, method, path, token, "live", "org:"+e.accountOrg, body)
}

const phraseBody = `{"confirmation_phrase":"ENCERRAR CONTA DE COBRANÇA"}`

func TestTheOwnerRequestsConfirmsAndCancelsThroughTheApi(t *testing.T) {
	e := newClosureEnv(t)
	tok := e.ownerToken(t, time.Minute)

	if r := e.call(t, "GET", "/v1.0/console/closure", tok, ""); r.status != http.StatusNotFound {
		t.Fatalf("no closure yet: %d %s", r.status, r.body)
	}
	r := e.call(t, "POST", "/v1.0/console/closure", tok, phraseBody)
	if r.status != http.StatusAccepted || !strings.Contains(string(r.body), "awaiting_confirmation") {
		t.Fatalf("request: %d %s", r.status, r.body)
	}
	var created struct {
		RequestID string `json:"request_id"`
	}
	r.decode(t, &created)

	if got := e.do(t, "POST", "/v1.0/closure/confirm", "", "", `{"request_id":"`+created.RequestID+`","token":"wrong"}`); got.status != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d %s", got.status, got.body)
	}
	body := `{"request_id":"` + created.RequestID + `","token":"` + e.mail.rawConfirm + `"}`
	if got := e.do(t, "POST", "/v1.0/closure/confirm", "", "", body); got.status != http.StatusOK || !strings.Contains(string(got.body), `"closing"`) {
		t.Fatalf("confirm: %d %s", got.status, got.body)
	}

	// The tenant is read-only now, but the closure routes still answer.
	m2m := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	if w := e.do(t, "POST", "/v1.0/customers", m2m, "k-"+newSpaceOrgID(), `{"name":"Ana","email":"ana@example.com"}`); w.status != http.StatusForbidden || !strings.Contains(string(w.body), "tenant_closing") {
		t.Fatalf("a write during the grace: %d %s", w.status, w.body)
	}
	if r := e.call(t, "GET", "/v1.0/console/closure", tok, ""); r.status != http.StatusOK || !strings.Contains(string(r.body), `"closing"`) {
		t.Fatalf("status while closing: %d %s", r.status, r.body)
	}

	cancel := `{"request_id":"` + created.RequestID + `","token":"` + e.mail.rawCancel + `"}`
	if got := e.do(t, "POST", "/v1.0/closure/cancel", "", "", cancel); got.status != http.StatusOK {
		t.Fatalf("cancel: %d %s", got.status, got.body)
	}
	if w := e.do(t, "POST", "/v1.0/customers", m2m, "k-"+newSpaceOrgID(), `{"name":"Ana","email":"ana@example.com"}`); w.status >= 400 {
		t.Fatalf("a write after the cancel: %d %s", w.status, w.body)
	}
}

func TestAClosureNeedsTheTypedPhraseAndARecentMfaProof(t *testing.T) {
	e := newClosureEnv(t)
	if r := e.call(t, "POST", "/v1.0/console/closure", e.ownerToken(t, time.Minute), `{"confirmation_phrase":"encerrar"}`); r.status != http.StatusBadRequest {
		t.Fatalf("a wrong phrase: %d %s", r.status, r.body)
	}
	for name, age := range map[string]time.Duration{"stale MFA": 2 * time.Hour, "no MFA": -1} {
		r := e.call(t, "POST", "/v1.0/console/closure", e.ownerToken(t, age), phraseBody)
		if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "step_up_required") {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestOnlyAnOwnerOfTheWorkspaceMayAsk(t *testing.T) {
	e := newClosureEnv(t)
	admin := "usr_admin_" + newSpaceOrgID()
	e.account.set(e.accountOrg, admin, "organization", "admin")
	tok := e.tokenWith(t, map[string]any{"last_mfa_at": time.Now().Unix()}, admin, "sess_x", middleware.ScopeOrganizationRead, middleware.ScopeOrganizationWrite)
	r := e.call(t, "POST", "/v1.0/console/closure", tok, phraseBody)
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "owner_required") {
		t.Fatalf("an admin: %d %s", r.status, r.body)
	}
	if r := e.call(t, "GET", "/v1.0/console/closure/blockers", tok, ""); r.status != http.StatusForbidden {
		t.Fatalf("an admin reading blockers: %d", r.status)
	}
}
