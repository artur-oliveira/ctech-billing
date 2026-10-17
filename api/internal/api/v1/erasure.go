package v1

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// maxWorkspaces bounds the eligibility question; a person who is the only member
// of more workspaces than this has a plan limit to explain first.
const maxWorkspaces = 100

// workspacesParam parses ?workspaces=a,b. Empty is no workspaces. An id that is
// not shaped like a workspace is a 400, because it will become a partition key.
func workspacesParam(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	ids := strings.Split(raw, ",")
	if len(ids) > maxWorkspaces {
		return nil, fmt.Errorf("at most %d workspaces", maxWorkspaces)
	}
	for _, id := range ids {
		if !space.IsOrganizationID(id) {
			return nil, fmt.Errorf("workspaces: %q is not a workspace id", id)
		}
	}
	return ids, nil
}

// registerErasure mounts ctech-account's eligibility question (saga protocol
// § 4.1).
//
// It is not behind ResolveTenant: the caller is ctech-account itself, which is
// not a billing credential, and the question is about a person rather than a
// tenant. The scope is the gate, and nothing but ctech-account can hold it
// (middleware.ScopeErasureEligibility).
func registerErasure(v1 fiber.Router, d Deps, auth fiber.Handler) {
	if d.Eraser == nil {
		return
	}
	v1.Get("/internal/erasure/eligibility/:sub", auth,
		middleware.RequireM2MScope(middleware.ScopeErasureEligibility),
		func(c fiber.Ctx) error {
			workspaces, err := workspacesParam(c.Query("workspaces"))
			if err != nil {
				return problem.BadRequest(err.Error()).Send(c)
			}
			blockers, err := d.Eraser.Blockers(c.Context(), c.Params("sub"), workspaces)
			if err != nil {
				// Never "eligible": ctech-account reads any non-2xx as a transient blocker.
				return fail(c, err)
			}
			return c.JSON(erasure.NewEligibility(blockers...))
		})
}
