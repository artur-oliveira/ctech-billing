package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/pushsvc"
	"gopkg.aoctech.app/api-commons/webpush"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/pendingpush"
)

type memReminders struct {
	mu   sync.Mutex
	rows map[string]pushsvc.Reminder
}

func (m *memReminders) Put(_ context.Context, r pushsvc.Reminder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.Sub+"/"+r.Kind] = r
	return nil
}

func (m *memReminders) Get(_ context.Context, sub, kind string) (pushsvc.Reminder, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[sub+"/"+kind]
	return r, ok, nil
}

type recordingInvoker struct{ jobs []pushsvc.Job }

func (r *recordingInvoker) Send(_ context.Context, j pushsvc.Job) error {
	r.jobs = append(r.jobs, j)
	return nil
}

func pushApp(t *testing.T, enabled bool, inv *recordingInvoker, claims *middleware.Claims) *fiber.App {
	t.Helper()
	key := ""
	if enabled {
		_, k, err := webpush.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		key = k.PublicBase64URL()
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals(middleware.ClaimsKey, claims)
		}
		return c.Next()
	})
	prefs := pendingpush.NewService(&memReminders{rows: map[string]pushsvc.Reminder{}})
	mountPush(app.Group("/console/finance"), &PushDeps{Service: pushsvc.NewService(nil, key), Prefs: prefs, Sender: inv})
	return app
}

func pushDo(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/console/finance"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func user(scopes string) *middleware.Claims {
	return &middleware.Claims{Sub: "u1", SID: "s1", Scope: scopes}
}

func TestPushRoutesFollowTheFinanceScopes(t *testing.T) {
	const body = `{"pending_reminder":{"enabled":true,"time":"08:00"}}`
	read, write := middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite
	for _, tc := range []struct {
		name, scopes, method, path, body string
		want                             int
	}{
		{"read with the read scope", read, "GET", "/push/preferences", "", 200},
		{"read without any scope", "", "GET", "/push/preferences", "", 403},
		{"write with only the read scope", read, "PUT", "/push/preferences", body, 403},
		{"write with the write scope", write, "PUT", "/push/preferences", body, 200},
		{"config with the read scope", read, "GET", "/push/config", "", 200},
		{"test with the read scope only", read, "POST", "/push/test", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pushDo(t, pushApp(t, true, &recordingInvoker{}, user(tc.scopes)), tc.method, tc.path, tc.body).StatusCode; got != tc.want {
				t.Errorf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPushRoutesRefuseAServiceToken(t *testing.T) {
	m2m := &middleware.Claims{Sub: "svc", SID: "", Scope: middleware.ScopeFinanceRead + " " + middleware.ScopeFinanceWrite}
	if got := pushDo(t, pushApp(t, true, &recordingInvoker{}, m2m), "GET", "/push/preferences", "").StatusCode; got != 403 {
		t.Errorf("status = %d: push is personal, a service token must not reach it", got)
	}
}

func TestPutPreferencesRoundTripAndValidation(t *testing.T) {
	app := pushApp(t, true, &recordingInvoker{}, user(middleware.ScopeFinanceRead+" "+middleware.ScopeFinanceWrite))
	if res := pushDo(t, app, "PUT", "/push/preferences", `{"pending_reminder":{"enabled":true,"time":"08:07"}}`); res.StatusCode != 200 {
		t.Fatalf("put = %d", res.StatusCode)
	}
	var got pendingpush.Preferences
	_ = json.NewDecoder(pushDo(t, app, "GET", "/push/preferences", "").Body).Decode(&got)
	if !got.PendingReminder.Enabled || got.PendingReminder.Time != "08:00" {
		t.Errorf("get = %+v", got)
	}
	if res := pushDo(t, app, "PUT", "/push/preferences", `{"pending_reminder":{"enabled":true,"time":""}}`); res.StatusCode != 400 {
		t.Errorf("enabled without a time = %d", res.StatusCode)
	}
	if res := pushDo(t, app, "PUT", "/push/preferences", `{"admin":true}`); res.StatusCode != 400 {
		t.Errorf("unknown field = %d", res.StatusCode)
	}
}

func TestPushTestSendsOnlyToTheCaller(t *testing.T) {
	inv := &recordingInvoker{}
	app := pushApp(t, true, inv, user(middleware.ScopeFinanceRead+" "+middleware.ScopeFinanceWrite))
	if res := pushDo(t, app, "POST", "/push/test", ""); res.StatusCode != 202 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if len(inv.jobs) != 1 || len(inv.jobs[0].Recipients) != 1 || inv.jobs[0].Recipients[0] != "u1" || inv.jobs[0].EventID == "" {
		t.Errorf("jobs = %+v", inv.jobs)
	}
}

func TestPushTestWhenPushIsOffIs503(t *testing.T) {
	inv := &recordingInvoker{}
	app := pushApp(t, false, inv, user(middleware.ScopeFinanceRead+" "+middleware.ScopeFinanceWrite))
	if res := pushDo(t, app, "POST", "/push/test", ""); res.StatusCode != 503 || len(inv.jobs) != 0 {
		t.Errorf("status = %d jobs = %d", res.StatusCode, len(inv.jobs))
	}
}
