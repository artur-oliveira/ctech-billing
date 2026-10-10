package v1

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type planHandlers struct {
	*handlers
	plan   *services.FinancePlan
	spaces spaceLister
}

type planPriceDTO struct {
	ID               string              `json:"id"`
	Type             billing.PriceType   `json:"type"`
	UnitAmount       billing.Cents       `json:"unit_amount"`
	Aggregation      billing.Aggregation `json:"aggregation"`
	Meter            string              `json:"meter"`
	IncludedQuantity int64               `json:"included_quantity"`
	Metadata         billing.Metadata    `json:"metadata"`
}

type planSubscriptionDTO struct {
	ID                string                     `json:"id"`
	Status            billing.SubscriptionStatus `json:"status"`
	CancelAtPeriodEnd bool                       `json:"cancel_at_period_end"`
	PriceIDs          []string                   `json:"price_ids"`
	Period            billing.Period             `json:"period"`
	PendingChange     *billing.PendingChange     `json:"pending_change"`
}

type planSpaceDTO struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"display_name"`
	People             int    `json:"people"`
	PendingInvitations int    `json:"pending_invitations"`
}

type planResponse struct {
	LiveOnly       bool                          `json:"live_only"`
	Subscription   *planSubscriptionDTO          `json:"subscription"`
	OpenInvoice    *entitlementInvoice           `json:"open_invoice"`
	Invoice        *invoiceResponse              `json:"invoice,omitempty"`
	DefaultPriceID string                        `json:"default_price_id"`
	Prices         []planPriceDTO                `json:"prices"`
	Usage          planUsageDTO                  `json:"usage"`
	Levels         map[string]services.LevelView `json:"levels"`
	Projected      billing.Cents                 `json:"projected_ondemand_cents"`
}

type planUsageDTO struct {
	Unavailable bool           `json:"unavailable"`
	Spaces      []planSpaceDTO `json:"spaces"`
}

// mountPlan registers the plan routes beside /spaces: they write no finance
// data, so they are outside the route table and its ensuringSpace (a plan
// choice must not create a finance space), but behind the same scope, space
// and idempotency middleware.
func mountPlan(router fiber.Router, resolver *space.Resolver, idem fiber.Handler, h *planHandlers) {
	read := []any{middleware.RequireUserScope(middleware.ScopeFinanceRead), middleware.ResolveSpace(resolver), h.personalOnly}
	write := []any{middleware.RequireUserScope(middleware.ScopeFinanceWrite), middleware.ResolveSpace(resolver), h.personalOnly, idem}
	router.Get("/plan", read[0], append(read[1:], any(h.get))...)
	router.Post("/plan", write[0], append(write[1:], any(h.choose))...)
	router.Post("/plan/cancel", write[0], append(write[1:], any(h.cancel))...)
}

// personalOnly: the plan is the person's (D2), seen from Pessoal only.
func (h *planHandlers) personalOnly(c fiber.Ctx) error {
	if !h.plan.Enabled() || middleware.GetSpace(c).Kind() != space.KindPersonalDefault {
		return problem.NotFound("the plan is shown in Pessoal").WithCode("plan_personal_only").Send(c)
	}
	return c.Next()
}

func (h *planHandlers) get(c fiber.Ctx) error { return h.answer(c, nil) }

type choosePlanRequest struct {
	PriceIDs []string `json:"price_ids"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
}

func (h *planHandlers) choose(c fiber.Ctx) error {
	var req choosePlanRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.text("name", req.Name, true, limits.CustomerName)
	ch.email("email", req.Email, false)
	if len(req.PriceIDs) == 0 || len(req.PriceIDs) > limits.MaxSubscriptionItems {
		ch.fail("price_ids", "required", "choose a plan")
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	_, inv, err := h.plan.Choose(c.Context(), services.ChooseInput{
		UserID: middleware.GetClaims(c).Sub, Name: req.Name, Email: req.Email,
		PriceIDs: req.PriceIDs, RequestID: middleware.GetRequestID(c),
	}, h.now())
	var pending *services.PlanPaymentPending
	if errors.As(err, &pending) {
		body := fiber.Map{"code": "plan_payment_pending", "title": "Payment pending", "status": 409}
		if pending.Invoice != nil {
			r := newInvoiceResponse(pending.Invoice, nil, h.today(), h.links)
			body["invoice_id"], body["checkout_url"] = r.ID, r.CheckoutURL
		}
		return c.Status(fiber.StatusConflict).JSON(body)
	}
	if err != nil {
		return fail(c, err)
	}
	return h.answer(c, inv)
}

func (h *planHandlers) cancel(c fiber.Ctx) error {
	if _, err := h.plan.Cancel(c.Context(), middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return h.answer(c, nil)
}

// answer renders the state after a read or a write; inv is a write's invoice.
func (h *planHandlers) answer(c fiber.Ctx, inv *billing.Invoice) error {
	sub := middleware.GetClaims(c).Sub
	st, err := h.plan.State(c.Context(), sub, h.now())
	if err != nil {
		return fail(c, err)
	}
	out := planResponse{
		LiveOnly: !middleware.GetSpace(c).Livemode(), DefaultPriceID: st.DefaultPriceID,
		Prices: []planPriceDTO{}, Levels: st.Levels, Projected: st.Projected,
		Usage: h.usage(c, sub),
	}
	for _, p := range st.Prices {
		out.Prices = append(out.Prices, planPriceDTO{ID: p.ID, Type: p.Type, UnitAmount: p.UnitAmount, Aggregation: p.Aggregation,
			Meter: p.Meter, IncludedQuantity: p.IncludedQuantity, Metadata: p.Metadata})
	}
	if s := st.Subscription; s != nil {
		out.Subscription = &planSubscriptionDTO{ID: s.ID, Status: s.Status, CancelAtPeriodEnd: s.CancelAtPeriodEnd, PriceIDs: st.PriceIDs, Period: st.Period, PendingChange: st.PendingChange}
	}
	if st.OpenInvoice != nil {
		r := newInvoiceResponse(st.OpenInvoice, nil, h.today(), h.links)
		out.OpenInvoice = &entitlementInvoice{ID: r.ID, TotalCents: r.Total, DueDate: r.DueDate, CheckoutURL: r.CheckoutURL}
	}
	if inv != nil {
		r := newInvoiceResponse(inv, nil, h.today(), h.links)
		out.Invoice = &r
	}
	return c.JSON(out)
}

// usage lists the personal workspaces the person owns, with ctech-account's
// counts. Unavailable when ctech-account fails or omits a count.
func (h *planHandlers) usage(c fiber.Ctx, sub string) planUsageDTO {
	u := planUsageDTO{Spaces: []planSpaceDTO{}}
	ws, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		slog.Warn("plan: listing workspaces failed", "error", err)
		u.Unavailable = true
		return u
	}
	for _, w := range ws {
		if space.WorkspaceKind(w.Kind) != space.KindPersonal || w.Role != "owner" || !space.IsOrganizationID(w.ID) {
			continue
		}
		if w.People == nil || w.PendingInvitations == nil {
			u.Unavailable = true
			continue
		}
		u.Spaces = append(u.Spaces, planSpaceDTO{ID: w.ID, DisplayName: w.DisplayName, People: *w.People, PendingInvitations: *w.PendingInvitations})
	}
	slices.SortStableFunc(u.Spaces, func(a, b planSpaceDTO) int {
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	return u
}
