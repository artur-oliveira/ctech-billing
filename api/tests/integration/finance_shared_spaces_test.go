//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// The § 9 leak tests of the shared-spaces spec that need the whole service:
// real tokens, the real ctech-account client, the real route table — and a fake
// ctech-account that answers membership and the workspace list from a table,
// in the shapes ctech-account PR #46 implemented.

type fakeAccount struct {
	mu      sync.Mutex
	members map[string][2]string // "ws|user" -> {kind, role}
	names   map[string]string    // ws -> display name
	listed  []string             // user ids the list route was asked about
}

func (a *fakeAccount) set(ws, user, kind, role string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.members[ws+"|"+user] = [2]string{kind, role}
}

func (a *fakeAccount) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/v1.0/internal/organizations/"):
			// {ws}/members/{user}
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1.0/internal/organizations/"), "/")
			if len(parts) != 3 {
				http.NotFound(w, r)
				return
			}
			m, ok := a.members[parts[0]+"|"+parts[2]]
			if !ok {
				_, _ = w.Write([]byte(`{"member":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"member": true, "kind": m[0], "role": m[1]})
		case strings.HasPrefix(r.URL.Path, "/v1.0/internal/users/"):
			user := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/internal/users/"), "/organizations")
			a.listed = append(a.listed, user)
			out := []map[string]string{}
			for k, m := range a.members {
				ws, u, _ := strings.Cut(k, "|")
				if u == user {
					out = append(out, map[string]string{"id": ws, "display_name": a.names[ws], "kind": m[0], "role": m[1]})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"organizations": out})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type spacesEnv struct {
	*apiEnv
	account *fakeAccount
}

func newSpacesEnv(t *testing.T) spacesEnv {
	t.Helper()
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}}
	srv := acct.serve(t)
	e := newAPIWith(t, func(c *config.Config) {
		c.AccountBaseURL, c.AccountTokenURL = srv.URL, srv.URL+"/token"
		c.AccountClientID, c.AccountClientSecret = "billing-test", "secret"
	})
	return spacesEnv{apiEnv: e, account: acct}
}

// person is a fresh signed-in user with both finance scopes.
func (s spacesEnv) person(t *testing.T) (sub, token string) {
	t.Helper()
	sub = "user_" + id.New()
	return sub, s.token(t, sub, "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
}

// in calls a finance route in one space ("personal" or "org:{id}").
func (s spacesEnv) in(t *testing.T, token, selector, method, path, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, "/v1.0/console/finance"+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(middleware.ModeHeader, "live")
	req.Header.Set(middleware.SpaceHeader, selector)
	if method != http.MethodGet {
		req.Header.Set(middleware.IdempotencyHeader, id.New())
	}
	resp, err := s.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
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

func (s spacesEnv) must(t *testing.T, want int, token, selector, method, path, body string, into any) {
	t.Helper()
	res := s.in(t, token, selector, method, path, body)
	if res.status != want {
		t.Fatalf("%s %s in %s = %d %s, want %d", method, path, selector, res.status, res.body, want)
	}
	if into != nil {
		res.decode(t, into)
	}
}

// The kind travels through the real client: Acesso total configures (an
// organization member could not), and a personal workspace starts with the
// personal chart of categories.
func TestAPersonalWorkspaceMemberHasFullAccessThroughTheRealClient(t *testing.T) {
	s := newSpacesEnv(t)
	sub, tok := s.person(t)
	ws := newSpaceOrgID()
	s.account.set(ws, sub, "personal", "member")

	var accounts struct{ Data []struct{ Name string } }
	s.must(t, 200, tok, "org:"+ws, "GET", "/accounts", "", &accounts)
	found := false
	for _, a := range accounts.Data {
		found = found || a.Name == "Moradia"
	}
	if !found {
		t.Fatalf("a personal workspace was not seeded with the personal chart: %+v", accounts.Data)
	}
	s.must(t, 201, tok, "org:"+ws, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, nil)

	var current struct{ Kind string }
	s.must(t, 200, tok, "org:"+ws, "GET", "/space", "", &current)
	if current.Kind != "personal" {
		t.Fatalf("GET /space kind = %q, want personal", current.Kind)
	}
}

// § 9.7: ids from one space are 404 in another — between two personal
// workspaces of the SAME person, and between one of them and the default space.
func TestIDsFromAnotherSpaceAreNotFoundEvenBetweenTwoSpacesOfOneUser(t *testing.T) {
	s := newSpacesEnv(t)
	sub, tok := s.person(t)
	ws1, ws2 := newSpaceOrgID(), newSpaceOrgID()
	s.account.set(ws1, sub, "personal", "owner")
	s.account.set(ws2, sub, "personal", "owner")

	home := "org:" + ws1
	var bank, rent, bill, card struct{ ID string }
	s.must(t, 201, tok, home, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	s.must(t, 201, tok, home, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	s.must(t, 201, tok, home, "POST", "/bills", `{"direction":"payable","amount":100,"account_id":"`+bank.ID+`","category_id":"`+rent.ID+`","due_date":"2026-03-10"}`, &bill)
	s.must(t, 201, tok, home, "POST", "/cards", `{"name":"Visa","closing_day":3,"due_day":10,"paying_account_id":"`+bank.ID+`"}`, &card)
	s.must(t, 200, tok, home, "GET", "/bills/"+bill.ID, "", nil)

	for _, other := range []string{"org:" + ws2, "personal"} {
		for _, c := range []struct{ method, path, body string }{
			{"GET", "/bills/" + bill.ID, ""},
			{"POST", "/bills/" + bill.ID + "/cancel", `{}`},
			{"GET", "/accounts/" + bank.ID + "/statement?from=2026-03-01&to=2026-03-31", ""},
			{"POST", "/accounts/" + bank.ID + "/archive", `{}`},
			{"GET", "/cards/" + card.ID + "/purchases", ""},
			{"PATCH", "/cards/" + card.ID, `{"due_day":12}`},
		} {
			if res := s.in(t, tok, other, c.method, c.path, c.body); res.status != 404 {
				t.Errorf("%s %s from %s = %d %s, want 404", c.method, c.path, other, res.status, res.body)
			}
		}
	}
	var got struct{ Status string }
	s.must(t, 200, tok, home, "GET", "/bills/"+bill.ID, "", &got)
	if got.Status == "cancelled" {
		t.Fatal("a cancel sent from another space cancelled the bill")
	}
}

// § 9.5 (the API's half): an organization_id forged onto the handoff return,
// for a workspace the person is not in, is the ordinary 404 on every request.
func TestAForgedHandoffIDIsA404(t *testing.T) {
	s := newSpacesEnv(t)
	sub, tok := s.person(t)
	mine, theirs := newSpaceOrgID(), newSpaceOrgID()
	s.account.set(mine, sub, "personal", "owner")
	s.account.set(theirs, "someone-else", "personal", "owner")

	nobody := s.in(t, tok, "org:"+newSpaceOrgID(), "GET", "/accounts", "")
	if nobody.status != 404 {
		t.Fatalf("a space nobody holds answered %d", nobody.status)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/accounts", ""},
		{"GET", "/space", ""},
		{"POST", "/accounts", `{"name":"x","class":"asset"}`},
	} {
		res := s.in(t, tok, "org:"+theirs, c.method, c.path, c.body)
		if res.status != 404 || !bytes.Equal(res.body, nobody.body) {
			t.Errorf("%s %s in a forged space = %d %s, want the same 404 as a space that does not exist (%s)", c.method, c.path, res.status, res.body, nobody.body)
		}
	}
}

// § 9.6: the default personal space has no id. A selector that tries to name
// one — the caller's own included — fails the UUID check and is the same 404.
func TestTheDefaultPersonalSpaceCannotBeAddressedByID(t *testing.T) {
	s := newSpacesEnv(t)
	sub, tok := s.person(t)
	s.must(t, 201, tok, "personal", "POST", "/accounts", `{"name":"Banco","class":"asset"}`, nil)
	nobody := s.in(t, tok, "org:"+newSpaceOrgID(), "GET", "/accounts", "")
	for _, sel := range []string{"org:USER#" + sub, "org:USER#" + sub + "#live", "org:USER%23" + sub, "org:" + sub} {
		res := s.in(t, tok, sel, "GET", "/accounts", "")
		if res.status != 404 || !bytes.Equal(res.body, nobody.body) {
			t.Errorf("%q = %d %s, want the ordinary 404", sel, res.status, res.body)
		}
	}
}

// § 9.9 through the real client: the list route is asked about the token's
// subject and nobody else, whatever the request says.
func TestTheSpacesListIsAskedOnlyForTheTokensSubject(t *testing.T) {
	s := newSpacesEnv(t)
	sub, tok := s.person(t)
	mine, bobs := newSpaceOrgID(), newSpaceOrgID()
	s.account.set(mine, sub, "personal", "owner")
	s.account.set(bobs, "bob", "personal", "owner")
	s.account.names[mine], s.account.names[bobs] = "Casa", "Bob"

	req := httptest.NewRequest("GET", "/v1.0/console/finance/spaces?user_id=bob", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-User-Id", "bob")
	resp, err := s.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || strings.Contains(string(raw), bobs) || !strings.Contains(string(raw), mine) {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	s.account.mu.Lock()
	defer s.account.mu.Unlock()
	if len(s.account.listed) != 1 || s.account.listed[0] != sub {
		t.Fatalf("ctech-account was asked about %v, want only %s", s.account.listed, sub)
	}
}
