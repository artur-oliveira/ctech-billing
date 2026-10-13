package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// livePlanOrg is the live tenant whose plan the screen shows: the plan is
// one per organization, so test mode reads the live one.
func (h *consoleHandlers) livePlanOrg(c fiber.Ctx) (*billing.Organization, error) {
	return h.orgs.Get(c.Context(), middleware.GetTenant(c).OrganizationID, true)
}

// billingPlan is the BYOK plan screen (part C Task 6).
func (h *consoleHandlers) billingPlan(c fiber.Ctx) error {
	org, err := h.livePlanOrg(c)
	if err != nil {
		return fail(c, err)
	}
	st, err := h.planScreen.State(c.Context(), org, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(st)
}

type changeBillingPlanRequest struct {
	Plan string `json:"plan"`
}

// changeBillingPlan moves the organization onto a public plan; live only.
func (h *consoleHandlers) changeBillingPlan(c fiber.Ctx) error {
	if !middleware.GetTenant(c).Livemode {
		return problem.NotFound("the plan is changed in live mode").WithCode("plan_live_only").Send(c)
	}
	var req changeBillingPlanRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	org, err := h.livePlanOrg(c)
	if err != nil {
		return fail(c, err)
	}
	if err := h.planScreen.Change(c.Context(), org, req.Plan, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return h.billingPlan(c)
}
