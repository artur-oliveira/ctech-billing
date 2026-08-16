package oauthresource

import (
	"encoding/json"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// The manifest and the enforcement have to be the same list, and this is the
// only thing that makes them so.
//
// They are two files with two readers: this JSON is pushed to ctech-account by
// CI and decides what a credential can be *granted*, while middleware.AllScopes
// is what the routes *check*. A scope in one and not the other fails silently
// and in opposite directions — granted-but-unenforced is a permission nobody
// applies, enforced-but-ungrantable is a route no client can ever call. Neither
// shows up in a build, and both show up in production.
//
// ctech-dfe's equivalent test asserts a hardcoded count (30). That catches a
// deletion and misses a rename, which is the mistake this pairing is actually
// exposed to.
func TestManifestMatchesEnforcedScopes(t *testing.T) {
	m, err := ManifestDocument()
	if err != nil {
		t.Fatal(err)
	}

	declared := make([]string, 0, len(m.Scopes))
	for _, s := range m.Scopes {
		declared = append(declared, s.Name)
	}
	enforced := append([]string(nil), middleware.AllScopes...)
	sort.Strings(declared)
	sort.Strings(enforced)

	if len(declared) != len(enforced) {
		t.Fatalf("manifest has %d scopes, middleware.AllScopes has %d\nmanifest: %v\nenforced: %v",
			len(declared), len(enforced), declared, enforced)
	}
	for i := range declared {
		if declared[i] != enforced[i] {
			t.Fatalf("scope %d differs: manifest %q, enforced %q", i, declared[i], enforced[i])
		}
	}
}

// Every scope must carry both languages. The IdP renders these on the consent
// screen, and a missing pt-BR is an English sentence shown to a Brazilian
// consumer at the moment they are deciding whether to trust an app.
func TestEveryScopeIsDescribedInBothLanguages(t *testing.T) {
	m, err := ManifestDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Scopes {
		for _, lang := range []string{"pt-BR", "en"} {
			if s.Descriptions[lang] == "" {
				t.Errorf("scope %q has no %s description", s.Name, lang)
			}
		}
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	app := fiber.New()
	Register(app, "https://billing.example.test", "https://accounts.example.test")

	resp, err := app.Test(httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var body struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Resource != "https://billing.example.test" {
		t.Errorf("resource = %q", body.Resource)
	}
	if len(body.AuthorizationServers) != 1 || body.AuthorizationServers[0] != "https://accounts.example.test" {
		t.Errorf("authorization_servers = %v", body.AuthorizationServers)
	}
	if len(body.Scopes) != len(middleware.AllScopes) {
		t.Errorf("scopes_supported = %d, want %d", len(body.Scopes), len(middleware.AllScopes))
	}
}
