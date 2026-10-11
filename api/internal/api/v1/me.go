package v1

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/accountorgs"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// meIdentity is one of the two things a person can be here: a customer with
// invoices, or the owner of an organization that issues them.
type meIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// meResponse says which of billing's two shells this person can open.
//
// Everyone who signs in is a customer; some are also operators, and the same
// account holds both at once (ADR 0012). The app needs that answer before it can
// render its own navigation, and the alternative — calling both session routes
// and reading a 403 as "no" — treats an error as data. A 403 means something
// went wrong; "you are not an operator" is not something going wrong.
//
// It carries **identity only**: an id and a name for each shell, and nothing
// about payout gates, modes or balances. Each shell asks its own session route
// for what it needs, under its own scope. That is what lets this route require
// no scope at all — it publishes only that you are who you already are.
type meResponse struct {
	// Null when the person does not hold that identity. Null rather than a
	// boolean plus an empty object: there is no half-held identity, and a client
	// checking for null cannot forget the other field.
	Portal  *meIdentity `json:"portal"`
	Console *meIdentity `json:"console"`
}

// me is mounted with authentication but **no tenant resolver**, because which
// tenant this person belongs to is exactly what it answers.
func (h *consoleHandlers) me(c fiber.Ctx) error {
	cl := middleware.GetClaims(c)
	if cl == nil {
		return problem.Unauthorized("credenciais ausentes").Send(c)
	}
	if cl.SID == "" {
		// A service token has neither identity. Answering with two nulls would
		// suggest an integration might one day have them.
		return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
	}

	var out meResponse

	if h.portalOrganizationID != "" {
		customer, err := h.customers.GetByUser(c.Context(), h.portalOrganizationID, true, cl.Sub)
		switch {
		case err == nil && !customer.Anonymized:
			out.Portal = &meIdentity{ID: customer.ID, Name: customer.Name}
		case err != nil && !errors.Is(err, repositories.ErrNotFound):
			return fail(c, err)
		}
	}

	// The console identity is a live tenant of an organization the person
	// administers; mode is a console concern and this route has none.
	org, err := h.consoleIdentity(c.Context(), cl.Sub)
	if err != nil {
		return fail(c, err)
	}
	if org != nil {
		out.Console = &meIdentity{ID: org.ID, Name: org.DisplayName}
	}

	return c.JSON(out)
}

// consoleIdentity is the tenant this person would open the console on: the
// first organization, by name, where they are owner or admin and a live tenant
// exists (ADR 0028). ctech-account not answering is "no console identity", not
// an error: /me is a probe.
func (h *consoleHandlers) consoleIdentity(ctx context.Context, sub string) (*billing.Organization, error) {
	if h.spaces == nil {
		return nil, nil
	}
	workspaces, err := h.spaces.Organizations(ctx, sub)
	if err != nil {
		slog.Warn("me: listing organizations failed", "error", err)
		return nil, nil
	}
	ws := slices.Clone(workspaces)
	slices.SortStableFunc(ws, func(a, b accountorgs.Organization) int {
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	for _, w := range ws {
		if !space.IsOrganizationID(w.ID) || !space.IsMerchantRole(space.WorkspaceKind(w.Kind), w.Role) {
			continue
		}
		org, err := middleware.LookupMerchantTenant(ctx, h.merchants, sub, w.ID, true)
		switch {
		case err == nil:
			return org, nil
		case !errors.Is(err, repositories.ErrNotFound):
			return nil, err
		}
	}
	return nil, nil
}
