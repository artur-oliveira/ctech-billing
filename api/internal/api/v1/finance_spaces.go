package v1

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// spaceLister is the one question the switcher asks ctech-account.
type spaceLister interface {
	Organizations(ctx context.Context, userID string) ([]accountclient.Organization, error)
}

type spaceDTO struct {
	Kind           string   `json:"kind"`
	OrganizationID string   `json:"organization_id,omitempty"`
	Label          string   `json:"label"`
	Role           string   `json:"role,omitempty"`
	Verbs          []string `json:"verbs"`
}

// mountSpaces registers GET /spaces. It is deliberately outside the route table
// and outside the space chain: it lists the spaces a person may pick from, so
// none is resolved yet. It needs the finance read scope and a signed-in person
// (RequireUserScope refuses service tokens), nothing else.
func mountSpaces(router fiber.Router, h *financeHandlers) {
	router.Get("/spaces", middleware.RequireUserScope(middleware.ScopeFinanceRead), h.listSpaces)
}

// listSpaces answers the switcher. The list is INFORMATION: ResolveSpace
// re-authorizes every finance request, so a stale or generous list cannot grant
// access, and the person asked about is always the token's subject — never a
// value from the request.
func (h *financeHandlers) listSpaces(c fiber.Ctx) error {
	sub := middleware.GetClaims(c).Sub
	out := struct {
		Spaces                   []spaceDTO `json:"spaces"`
		OrganizationsUnavailable bool       `json:"organizations_unavailable"`
	}{Spaces: []spaceDTO{{Kind: "personal", Label: "Pessoal", Verbs: space.All.Names()}}}

	orgs, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		// The personal space needs no account; say the rest is unavailable rather
		// than failing the whole answer.
		slog.Warn("finance: listing organizations failed", "error", err)
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	for _, o := range orgs {
		verbs := space.VerbsForRole(o.Role)
		if verbs == 0 {
			continue // the resolver would answer 404 for it
		}
		out.Spaces = append(out.Spaces, spaceDTO{
			Kind: "organization", OrganizationID: o.ID, Label: o.DisplayName, Role: o.Role, Verbs: verbs.Names(),
		})
	}
	return c.JSON(out)
}
