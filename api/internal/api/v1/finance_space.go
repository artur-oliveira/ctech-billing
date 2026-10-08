package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

type financeSpaceDTO struct {
	Kind           string   `json:"kind"` // "personal" | "organization"
	OrganizationID string   `json:"organization_id,omitempty"`
	Mode           string   `json:"mode"`
	Verbs          []string `json:"verbs"`
}

// financeSpace reports the space the request resolved to and the verbs held in
// it, so the console can render a switcher and hide what it cannot do. It is
// information for the UI, not an authorization: every route re-resolves.
func financeSpace(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	kind := "organization"
	if sp.Personal() {
		kind = "personal"
	}
	return c.JSON(financeSpaceDTO{
		Kind:           kind,
		OrganizationID: sp.OrganizationID(),
		Mode:           sp.Mode(),
		Verbs:          sp.Verbs().Names(),
	})
}
