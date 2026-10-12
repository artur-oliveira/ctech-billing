package v1

import (
	"math"
	"slices"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
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
	console.Post("/invoices/:id/simulate-payment", write, ch.simulatePayment)
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

// simulatePayment pays a test invoice's Pix in the provider's sandbox (spec
// § 9). Live mode: 404, the route does not exist there. The confirmation runs
// through the real path (webhook or the hourly sweep), hence 202.
func (h *consoleHandlers) simulatePayment(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	if t.Livemode {
		return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
	}
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if err := h.byok.SimulatePayment(c.Context(), inv, h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusAccepted)
}

type invoiceByokResponse struct {
	Provider      string        `json:"provider,omitempty"`
	AttemptStatus string        `json:"attempt_status,omitempty"`
	Payable       string        `json:"payable,omitempty"`
	DueDate       brcal.Date    `json:"due_date,omitzero"`
	PayableUntil  brcal.Date    `json:"payable_until,omitzero"`
	LateCharges   billing.Cents `json:"late_charges,omitempty"`
	// Reason is why there is no payable Pix, as a problem code the console
	// says in words (customer_tax_id_required, the gate's codes,
	// byok_charge_ended, byok_charge_pending).
	Reason string `json:"reason,omitempty"`
	// CanRetry offers "Gerar novo Pix"; Simulate offers "Simular pagamento".
	CanRetry       bool                    `json:"can_retry"`
	Simulate       bool                    `json:"simulate"`
	ExcessPayments []billing.ExcessPayment `json:"excess_payments"`
}

// byokBlock composes the invoice's Pix; nil on the wallet rail. Best effort:
// a read that fails leaves the block out rather than the screen.
func (h *consoleHandlers) byokBlock(c fiber.Ctx, inv *billing.Invoice) *invoiceByokResponse {
	if h.byok == nil {
		return nil
	}
	org := middleware.GetOrganization(c)
	attempts, err := h.payments.ListAttempts(c.Context(), inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		return nil
	}
	var last *billing.PaymentAttempt
	for i := range attempts {
		if attempts[i].Method == billing.MethodBYOK {
			last = &attempts[i]
		}
	}
	if last == nil && (org == nil || org.Rail() != billing.RailBYOK) {
		return nil
	}
	out := &invoiceByokResponse{ExcessPayments: []billing.ExcessPayment{}}
	if ex, err := h.payments.ListExcess(c.Context(), inv.OrganizationID, inv.Livemode, inv.ID); err == nil {
		out.ExcessPayments = ex
	}
	if org != nil {
		out.Provider = org.PayoutProvider
	}
	open := inv.Status == billing.InvoiceOpen
	if last != nil {
		out.Provider, out.AttemptStatus, out.LateCharges = last.Provider, string(last.Status), last.LateCharges
		if last.Status == billing.AttemptPending {
			out.Payable, out.DueDate, out.PayableUntil = last.Payable, last.DueDate, last.PayableUntil
		}
	}
	switch {
	case !open:
	case last != nil && last.Status == billing.AttemptPending && last.Payable != "":
		out.Simulate = !inv.Livemode
	case last != nil && last.Status == billing.AttemptPending:
		out.Reason, out.CanRetry = "byok_charge_pending", true
	default:
		out.CanRetry = true
		out.Reason = "byok_charge_ended"
		if org != nil {
			if err := h.gates.Authorize(c.Context(), org); err != nil {
				if p := problem.FromError(err); p != nil {
					out.Reason, out.CanRetry = p.Code, false
				}
			} else if cust, err := h.customers.Get(c.Context(), inv.OrganizationID, inv.Livemode, inv.CustomerID); err == nil && !hasTaxID(cust.TaxID) {
				out.Reason = "customer_tax_id_required"
			}
		}
	}
	return out
}

func hasTaxID(s string) bool {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n == 11 || n == 14
}
