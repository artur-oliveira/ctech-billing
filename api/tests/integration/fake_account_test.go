//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
)

// fakeAccount is the ctech-account every apiEnv talks to: membership and the
// workspace list from a table, in the shapes ctech-account PR #46 implemented.

type fakeAccount struct {
	mu      sync.Mutex
	members map[string][2]string // "ws|user" -> {kind, role}
	names   map[string]string    // ws -> display name
	listed  []string             // user ids the list route was asked about
	counts  map[string][2]int    // ws -> {people, pending}, as on owned personal spaces

	companies          map[string][]accountclient.Company // org -> companies
	companiesDown      bool                               // both company routes answer 503
	companiesTruncated bool                               // the list route says it cut the list
}

// company registers one of an organization's companies.
func (a *fakeAccount) company(org string, c accountclient.Company) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.companies == nil {
		a.companies = map[string][]accountclient.Company{}
	}
	a.companies[org] = append(a.companies[org], c)
}

// knows reports an organization ctech-account would know: one with a member,
// a name or a company. The list route answers 404 for any other.
func (a *fakeAccount) knows(org string) bool {
	if _, ok := a.names[org]; ok {
		return true
	}
	if _, ok := a.companies[org]; ok {
		return true
	}
	for k := range a.members {
		if ws, _, _ := strings.Cut(k, "|"); ws == org {
			return true
		}
	}
	return false
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
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1.0/internal/organizations/"), "/")
			switch {
			case len(parts) == 3 && parts[1] == "members":
				m, ok := a.members[parts[0]+"|"+parts[2]]
				if !ok {
					_, _ = w.Write([]byte(`{"member":false}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"member": true, "kind": m[0], "role": m[1]})
			case len(parts) >= 2 && parts[1] == "companies" && a.companiesDown:
				http.Error(w, "down", http.StatusServiceUnavailable)
			case len(parts) == 2 && parts[1] == "companies":
				if !a.knows(parts[0]) {
					http.NotFound(w, r)
					return
				}
				list := a.companies[parts[0]]
				if list == nil {
					list = []accountclient.Company{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"organization_id": parts[0], "organization_kind": "organization",
					"companies": list, "truncated": a.companiesTruncated,
				})
			case len(parts) == 3 && parts[1] == "companies":
				for _, c := range a.companies[parts[0]] {
					if c.ID == parts[2] {
						_ = json.NewEncoder(w).Encode(map[string]any{
							"organization_id": parts[0], "organization_kind": "organization",
							"tax_id": c.TaxID, "tax_id_kind": c.TaxIDKind, "legal_name": c.LegalName, "trade_name": c.TradeName,
						})
						return
					}
				}
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
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
