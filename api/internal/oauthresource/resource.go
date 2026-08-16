// Package oauthresource exposes the OAuth protected-resource metadata and the
// versioned scope manifest this service owns.
//
// Billing is a resource server, so it publishes its own scopes rather than
// waiting for ctech-account to be taught about them: the deploy pipeline pushes
// this manifest to the identity provider (see the `scopes` job in
// .github/workflows/deploy.yml), which is the same shape ctech-dfe uses.
// The scope list and the service that enforces it therefore ship together, and
// a scope cannot exist at the IdP that no route here honours.
package oauthresource

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gofiber/fiber/v3"
)

//go:embed scope-manifest.json
var manifestJSON []byte

type Scope struct {
	Name         string            `json:"name"`
	Descriptions map[string]string `json:"descriptions"`
	Visibility   string            `json:"visibility"`
	Status       string            `json:"status"`
}

type Manifest struct {
	SchemaVersion    int     `json:"schema_version"`
	ResourceServerID string  `json:"resource_server_id"`
	DisplayName      string  `json:"display_name"`
	Scopes           []Scope `json:"scopes"`
}

// ManifestDocument returns a fresh copy of the embedded manifest.
func ManifestDocument() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode embedded OAuth scope manifest: %w", err)
	}
	return m, nil
}

// ManifestBytes returns a copy suitable for tooling and contract tests.
func ManifestBytes() []byte { return append([]byte(nil), manifestJSON...) }

// PublicActiveScopes returns the scopes clients may request interactively.
func PublicActiveScopes() ([]string, error) {
	m, err := ManifestDocument()
	if err != nil {
		return nil, err
	}
	scopes := make([]string, 0, len(m.Scopes))
	for _, scope := range m.Scopes {
		if scope.Visibility == "public" && scope.Status == "active" {
			scopes = append(scopes, scope.Name)
		}
	}
	sort.Strings(scopes)
	return scopes, nil
}

// Register mounts RFC 9728 OAuth Protected Resource Metadata.
//
// Unauthenticated by design — it is how a client discovers which authorization
// server to go to and which scopes exist here, which is a question it must be
// able to ask before it holds a token.
func Register(app *fiber.App, resource, authorizationServer string) {
	app.Get("/.well-known/oauth-protected-resource", func(c fiber.Ctx) error {
		scopes, err := PublicActiveScopes()
		if err != nil {
			return fiber.ErrInternalServerError
		}
		return c.JSON(fiber.Map{
			"resource":              resource,
			"authorization_servers": []string{authorizationServer},
			"scopes_supported":      scopes,
		})
	})
}
