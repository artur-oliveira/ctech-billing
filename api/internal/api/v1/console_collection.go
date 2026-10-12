package v1

import (
	"math"
	"slices"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// registerCollection mounts the BYOK collection routes of the merchant console
// (spec 2026-10-11 § 12.1): owner/admin of the merchant tenant (the console
// group's resolver), billing:organization:write. Not mounted without BYOK.
func registerCollection(console fiber.Router, ch *consoleHandlers) {
	if ch.byok == nil {
		return
	}
	write := middleware.RequireUserScope(middleware.ScopeOrganizationWrite)
	console.Post("/invoices/:id/charge", write, ch.newCharge)
	console.Post("/excess-payments/:id/refund", write, ch.refundExcess)
	if ch.setup != nil {
		console.Put("/collection", write, ch.useForCollection)
	}
}

// useForCollection is "Usar o Inter para cobrar" with the late fees, as
// percentages with two decimals on the wire (plan scope decision 3).
func (h *consoleHandlers) useForCollection(c fiber.Ctx) error {
	var body struct {
		Provider               string  `json:"provider"`
		LateFeePercent         float64 `json:"late_fee_percent"`
		MonthlyInterestPercent float64 `json:"monthly_interest_percent"`
	}
	if p := decodeStrict(c, &body); p != nil {
		return p.Send(c)
	}
	if !slices.Contains(services.KnownProviders, body.Provider) {
		return problem.Validation([]problem.FieldError{problem.Field("provider", "invalid", "unknown provider")}).Send(c)
	}
	settings := billing.CollectionSettings{FineBasisPoints: int(math.Round(body.LateFeePercent * 100)),
		MonthlyInterestBasisPoints: int(math.Round(body.MonthlyInterestPercent * 100))}
	if err := settings.Validate(); err != nil {
		return problem.Validation([]problem.FieldError{problem.Field("late_fee_percent", "out_of_range", err.Error())}).Send(c)
	}
	t := middleware.GetTenant(c)
	org, err := h.orgs.Get(c.Context(), t.OrganizationID, t.Livemode)
	if err != nil {
		return fail(c, err)
	}
	if err := h.setup.UseForCollection(c.Context(), middleware.GetSpace(c), org, body.Provider, settings,
		actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return h.settings(c)
}

// refundExcess gives an excess payment back (D19: only on this press).
func (h *consoleHandlers) refundExcess(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	e, err := h.byok.RefundExcess(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"), actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(e)
}

// newCharge opens a new BYOK charge after the previous one ended unpaid
// ("Gerar novo Pix", spec § 7.3), or completes one that was never created.
func (h *consoleHandlers) newCharge(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if _, err := h.byok.NewAttempt(c.Context(), inv, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return h.invoiceDetail(c, inv)
}
