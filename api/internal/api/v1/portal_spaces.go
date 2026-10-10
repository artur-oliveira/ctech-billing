package v1

import (
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type portalSpaceDTO struct {
	Selector    string `json:"selector"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role,omitempty"`
}

// listSpaces is the portal's selector: Pessoal, then the organizations (kind
// organization only) where the token's subject is owner or admin. Information
// only — ResolvePortalIdentity re-authorizes every request.
func (h *portalHandlers) listSpaces(c fiber.Ctx) error {
	if h.portalOrg == "" {
		return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
	}
	out := struct {
		Spaces                   []portalSpaceDTO `json:"spaces"`
		OrganizationsUnavailable bool             `json:"organizations_unavailable"`
	}{Spaces: []portalSpaceDTO{{Selector: "personal", DisplayName: "Pessoal"}}}
	if h.spaces == nil {
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	ws, err := h.spaces.Organizations(c.Context(), middleware.GetClaims(c).Sub)
	if err != nil {
		slog.Warn("portal: listing organizations failed", "error", err)
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	var orgs []spaceDTO
	for _, w := range ws {
		kind := space.WorkspaceKind(w.Kind)
		if kind != space.KindOrganization || !space.IsOrganizationID(w.ID) || !space.VerbsFor(kind, w.Role).Has(space.Configure) {
			continue
		}
		orgs = append(orgs, spaceDTO{Selector: "org:" + w.ID, DisplayName: w.DisplayName, Role: w.Role})
	}
	sortByName(orgs)
	for _, o := range orgs {
		out.Spaces = append(out.Spaces, portalSpaceDTO{Selector: o.Selector, DisplayName: o.DisplayName, Role: o.Role})
	}
	return c.JSON(out)
}
