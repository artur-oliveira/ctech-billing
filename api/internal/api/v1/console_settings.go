package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// C17 — configurações — and the two writes behind it.
//
// What is here is what an operator can actually change: the dunning policy.
// The rest of C17's list (numbering, retention, e-mails) is published read-only
// because it is not configurable — numbering is gapless per year with no
// options, retention is a constant (ADR 0009), and the sender address is a
// deployment secret. Showing them as fields with no way to edit would be a
// settings screen that lies about what it controls; showing them as facts is
// the truth.

// settingsResponse is C17.
type settingsResponse struct {
	Organization sessionResponse `json:"organization"`
	// Dunning is the schedule in force, always populated: an organization on the
	// built-in default gets the built-in default here rather than an empty list,
	// because the screen's question is "what happens to an unpaid invoice" and
	// "nothing configured" is not an answer to it.
	Dunning dunningPolicyResponse `json:"dunning"`
	// Numbering, Retention and Sender are facts, not fields. They are published
	// so the screen can state them rather than leave an operator guessing.
	Numbering string `json:"numbering"`
	Retention string `json:"retention"`
}

type dunningPolicyResponse struct {
	Steps []dunningStepResponse `json:"steps"`
	// Custom says whether this organization wrote the policy or inherited it.
	// The screen renders the same steps either way and labels them differently,
	// which is the honest reading: an inherited policy is in force, not absent.
	Custom bool `json:"custom"`
}

type dunningStepResponse struct {
	// Offset is days from the due date. Negative is before it.
	Offset int                   `json:"offset"`
	Action billing.DunningAction `json:"action"`
}

type dunningPolicyRequest struct {
	// Steps replaces the whole schedule. An empty list restores the default
	// rather than disabling dunning: an invoice that is never chased and never
	// written off sits OPEN forever looking like revenue.
	Steps []dunningStepResponse `json:"steps"`
}

func newDunningPolicyResponse(p billing.DunningSchedule) dunningPolicyResponse {
	custom := len(p) > 0
	if !custom {
		p = billing.DefaultDunningPolicy
	}
	out := dunningPolicyResponse{Custom: custom, Steps: make([]dunningStepResponse, 0, len(p))}
	for _, step := range p {
		out.Steps = append(out.Steps, dunningStepResponse{Offset: step.Offset, Action: step.Action})
	}
	return out
}

func (req dunningPolicyRequest) schedule() billing.DunningSchedule {
	if len(req.Steps) == 0 {
		return nil
	}
	out := make(billing.DunningSchedule, 0, len(req.Steps))
	for _, step := range req.Steps {
		out = append(out, billing.DunningStep{Offset: step.Offset, Action: step.Action})
	}
	return out
}

func (h *consoleHandlers) settings(c fiber.Ctx) error {
	org := middleware.GetOrganization(c)
	return c.JSON(settingsResponse{
		Organization: sessionResponse{
			OrganizationID: org.ID,
			DisplayName:    org.DisplayName,
			Livemode:       org.Livemode,
			PayoutStatus:   org.PayoutStatus,
			CanCharge:      org.AuthorizeCharge() == nil,
		},
		Dunning:   newDunningPolicyResponse(org.DunningPolicy),
		Numbering: "sequencial por ano, sem lacunas",
		Retention: "faturas e notas de crédito permanentes; auditoria por 5 anos",
	})
}

// setDunningPolicy replaces the organization's default schedule.
//
// It changes no invoice. The schedule is copied onto an invoice when it is
// finalized, so this decides what happens to invoices issued afterwards and
// nothing about the ones already being chased — an operator who shortened the
// policy has not just moved everybody's write-off date three weeks forward.
func (h *consoleHandlers) setDunningPolicy(c fiber.Ctx) error {
	org := middleware.GetOrganization(c)
	var req dunningPolicyRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if err := h.orgs.SetDunningPolicy(
		c.Context(), org, req.schedule(), actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(newDunningPolicyResponse(org.DunningPolicy))
}

// setProductDunningPolicy overrides the schedule for one product (C9).
func (h *consoleHandlers) setProductDunningPolicy(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req dunningPolicyRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	product, err := h.cat.GetProduct(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if err := h.cat.SetProductDunningPolicy(
		c.Context(), product, req.schedule(), actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(newDunningPolicyResponse(product.DunningPolicy))
}

// revealTaxID answers with a customer's full CPF/CNPJ, and writes the audit row
// that says who looked.
//
// **A POST, not a GET, and that is not pedantry.** This request has an effect —
// it records an access to personal data — and a GET is a thing browsers
// prefetch, proxies cache and crawlers follow. It is also why the masked value
// is what every listing and every detail carries: revealing is a deliberate act
// somebody performs, not a field that happens to be on screen.
//
// Without the audit row the masking would be theatre: a data-subject request
// asking "who has seen my CPF" would have no honest answer.
func (h *consoleHandlers) revealTaxID(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if customer.TaxID == "" {
		return problem.NotFound("este cliente não tem CPF/CNPJ cadastrado").Send(c)
	}
	// Written **before** the value is returned. The other order loses the record
	// whenever the response fails to reach the browser, which is exactly the
	// case somebody would later dispute.
	if err := h.customers.RecordTaxIDAccess(
		c.Context(), customer, actorOfUser(c), middleware.GetRequestID(c), c.IP(), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"tax_id": customer.TaxID})
}
