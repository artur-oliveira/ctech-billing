//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAccount is the ctech-account every apiEnv talks to: membership and the
// workspace list from a table, in the shapes ctech-account PR #46 implemented.

type fakeAccount struct {
	mu      sync.Mutex
	members map[string][2]string // "ws|user" -> {kind, role}
	names   map[string]string    // ws -> display name
	listed  []string             // user ids the list route was asked about
	counts  map[string][2]int    // ws -> {people, pending}, as on owned personal spaces
}

func (a *fakeAccount) count(ws string, people, pending int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.counts == nil {
		a.counts = map[string][2]int{}
	}
	a.counts[ws] = [2]int{people, pending}
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
			out := []map[string]any{}
			for k, m := range a.members {
				ws, u, _ := strings.Cut(k, "|")
				if u == user {
					entry := map[string]any{"id": ws, "display_name": a.names[ws], "kind": m[0], "role": m[1]}
					if c, ok := a.counts[ws]; ok {
						entry["people"], entry["pending_invitations"] = c[0], c[1]
					}
					out = append(out, entry)
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

// name sets a workspace's display name, as the list route returns it.
func (a *fakeAccount) name(ws, displayName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.names[ws] = displayName
}
