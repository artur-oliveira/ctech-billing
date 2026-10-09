package v1

import (
	"context"
	"log/slog"
	"slices"
	"strings"

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
	// Selector is what X-Billing-Space carries for this space: "personal" or
	// "org:{id}". The console never builds one from anything else.
	Selector    string     `json:"selector"`
	Kind        space.Kind `json:"kind"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role,omitempty"`
	Verbs       []string   `json:"verbs"`
	// ManagePeople is true for the owner of a personal workspace only: the one
	// person ctech-account lets invite, change and remove there (ADR 0027).
	ManagePeople bool `json:"manage_people"`
}

// mountSpaces registers GET /spaces. It is deliberately outside the route table
// and outside the space chain: it lists the spaces a person may pick from, so
// none is resolved yet. It needs the finance read scope and a signed-in person
// (RequireUserScope refuses service tokens), nothing else.
func mountSpaces(router fiber.Router, h *financeHandlers) {
	router.Get("/spaces", middleware.RequireUserScope(middleware.ScopeFinanceRead), h.listSpaces)
}

// listSpaces answers the switcher: Pessoal, then the person's personal
// workspaces, then their organizations, each group by name (spec § 5.1). The
// list is INFORMATION: ResolveSpace re-authorizes every finance request, so a
// stale or generous list cannot grant access, and the person asked about is
// always the token's subject — never a value from the request.
func (h *financeHandlers) listSpaces(c fiber.Ctx) error {
	sub := middleware.GetClaims(c).Sub
	out := struct {
		Spaces                   []spaceDTO `json:"spaces"`
		OrganizationsUnavailable bool       `json:"organizations_unavailable"`
	}{Spaces: []spaceDTO{{Selector: "personal", Kind: space.KindPersonalDefault, DisplayName: "Pessoal", Verbs: space.All.Names()}}}

	workspaces, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		// The personal space needs no account; say the rest is unavailable rather
		// than failing the whole answer.
		slog.Warn("finance: listing workspaces failed", "error", err)
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	var personal, orgs []spaceDTO
	for _, w := range workspaces {
		kind := space.WorkspaceKind(w.Kind)
		verbs := space.VerbsFor(kind, w.Role)
		if verbs == 0 || !space.IsOrganizationID(w.ID) {
			continue // the resolver would answer 404 for it
		}
		d := spaceDTO{
			Selector: "org:" + w.ID, Kind: kind, DisplayName: w.DisplayName, Role: w.Role, Verbs: verbs.Names(),
			ManagePeople: kind == space.KindPersonal && w.Role == "owner",
		}
		if kind == space.KindPersonal {
			personal = append(personal, d)
		} else {
			orgs = append(orgs, d)
		}
	}
	sortByName(personal)
	sortByName(orgs)
	out.Spaces = append(append(out.Spaces, personal...), orgs...)
	return c.JSON(out)
}

// sortByName orders a group alphabetically, ignoring case, with the selector as
// a tiebreak so two spaces of the same name keep one order.
func sortByName(s []spaceDTO) {
	slices.SortStableFunc(s, func(a, b spaceDTO) int {
		if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
			return c
		}
		return strings.Compare(a.Selector, b.Selector)
	})
}
