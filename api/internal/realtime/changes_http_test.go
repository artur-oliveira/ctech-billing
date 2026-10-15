package realtime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

func changesApp(t *testing.T, f *fixture, log ChangeLog) *fiber.App {
	t.Helper()
	app := fiber.New()
	RegisterChanges(app.Group("/v1.0"), &ChangesHandler{
		Authorizers: map[string]Authorizer{KindConsole: f.console, KindPortal: f.portal, KindCheckout: f.checkout},
		Log:         log,
		Now:         func() time.Time { return testNow },
	})
	return app
}

type requestSpec struct {
	name    string
	headers map[string]string
}

func (r requestSpec) get(t *testing.T, app *fiber.App, query string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1.0/realtime/changes"+query, nil)
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	return res, body
}

func requestsFor(f *fixture) []requestSpec {
	console := func(token, selector, mode string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + f.tokens[token], "X-Billing-Space": selector, "X-Billing-Mode": mode}
	}
	return []requestSpec{
		{"console A live", console("s1", "org:"+acctA, "live")},
		{"console A test", console("s1", "org:"+acctA, "test")},
		{"console B live", console("s2", "org:"+acctB, "live")},
		{"portal U1", map[string]string{"Authorization": "Bearer " + f.tokens["u1"]}},
		{"portal U2", map[string]string{"Authorization": "Bearer " + f.tokens["u2"]}},
		{"checkout 1", map[string]string{"Authorization": "PayLink " + f.links.Sign(orgA, true, "in_1")}},
		{"checkout 2", map[string]string{"Authorization": "PayLink " + f.links.Sign(orgB, true, "in_2")}},
	}
}

type changesBody struct {
	Changes []struct {
		ID       string `json:"id"`
		EntityID string `json:"entity_id"`
	} `json:"changes"`
	Head string `json:"head"`
	More bool   `json:"more"`
	Gap  string `json:"gap"`
}

func oldCursor() string { return id.NewAt(testNow.Add(-time.Hour)) }

// The same isolation matrix as the authorizers' (Task 2), now over HTTP: every
// scope holds one change whose entity id is the scope's own name.
func TestOverHTTPEachPrincipalGetsExactlyTheChangesOfItsScopes(t *testing.T) {
	f := newFixture(t)
	log := &fakeLog{}
	for _, s := range allScopes {
		log.add(change(s, testNow.Add(-30*time.Minute), s))
	}
	app := changesApp(t, f, log)

	for _, r := range requestsFor(f) {
		res, raw := r.get(t, app, "?cursor="+oldCursor())
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d %s", r.name, res.StatusCode, raw)
		}
		var body changesBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, c := range body.Changes {
			got[c.EntityID] = true
		}
		for _, s := range allScopes {
			if got[s] != owns(r.name, s) {
				t.Errorf("%s / %s: delivered=%v, want %v", r.name, s, got[s], owns(r.name, s))
			}
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", r.name, res.Header.Get("Cache-Control"))
		}
	}
}

func TestACredentialInTheURLIsIgnored(t *testing.T) {
	f := newFixture(t)
	app := changesApp(t, f, &fakeLog{})
	for _, query := range []string{"?token=" + f.tokens["u1"], "?pay_link=" + f.links.Sign(orgA, true, "in_1"), "?access_token=" + f.tokens["u1"]} {
		res, _ := requestSpec{name: "none"}.get(t, app, query)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", query, res.StatusCode)
		}
	}
	res, _ := requestSpec{headers: map[string]string{"Authorization": "Basic abc"}}.get(t, app, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a Basic credential: status %d, want 401", res.StatusCode)
	}
	res, _ = requestSpec{headers: map[string]string{"Authorization": "Bearer nope"}}.get(t, app, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a bad token: status %d, want 401", res.StatusCode)
	}
}

func TestTheLimitIsCappedAndMoreSaysThereAreOthers(t *testing.T) {
	f := newFixture(t)
	log := &fakeLog{}
	scope := UserScope("u1")
	for i := 0; i < 250; i++ {
		log.add(change(scope, testNow.Add(-30*time.Minute+time.Duration(i)*time.Millisecond), "c"))
	}
	app := changesApp(t, f, log)
	r := requestSpec{headers: map[string]string{"Authorization": "Bearer " + f.tokens["u1"]}}

	_, raw := r.get(t, app, "?limit=1000&cursor="+oldCursor())
	var body changesBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Changes) != MaxChangesLimit || !body.More {
		t.Fatalf("changes=%d more=%v, want %d and more", len(body.Changes), body.More, MaxChangesLimit)
	}
	_, raw = r.get(t, app, "?cursor="+oldCursor())
	json.Unmarshal(raw, &body)
	if len(body.Changes) != DefaultChangesLimit {
		t.Fatalf("default page = %d, want %d", len(body.Changes), DefaultChangesLimit)
	}
}

func TestAnEmptyCursorIsAPositionABadOneIsRefusedAndAnOldOneIsAGap(t *testing.T) {
	f := newFixture(t)
	log := &fakeLog{}
	newest := change(UserScope("u1"), testNow.Add(-time.Minute), "newest")
	log.add(newest)
	app := changesApp(t, f, log)
	r := requestSpec{headers: map[string]string{"Authorization": "Bearer " + f.tokens["u1"]}}

	_, raw := r.get(t, app, "")
	var body changesBody
	json.Unmarshal(raw, &body)
	if len(body.Changes) != 0 || body.Head != newest.ID {
		t.Fatalf("no cursor: %+v, want no changes and head %s", body, newest.ID)
	}

	if res, _ := r.get(t, app, "?cursor=garbage"); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("garbage cursor: %d, want 400", res.StatusCode)
	}

	_, raw = r.get(t, app, "?cursor="+id.NewAt(testNow.Add(-49*time.Hour)))
	body = changesBody{}
	json.Unmarshal(raw, &body)
	if body.Gap != GapCursorTooOld || len(body.Changes) != 0 {
		t.Fatalf("old cursor: %+v, want gap cursor_too_old", body)
	}
}

func TestAConsoleWithoutAMemberRoleGetsNothingOverHTTPEither(t *testing.T) {
	f := newFixture(t)
	app := changesApp(t, f, &fakeLog{})
	r := requestSpec{headers: map[string]string{"Authorization": "Bearer " + f.tokens["s1"], "X-Billing-Space": "org:" + acctB, "X-Billing-Mode": "live"}}
	if res, _ := r.get(t, app, "?cursor="+oldCursor()); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a user asking for another organization's space: %d, want 401", res.StatusCode)
	}
}
