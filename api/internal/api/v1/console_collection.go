package v1

import (
	"github.com/gofiber/fiber/v3"

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
