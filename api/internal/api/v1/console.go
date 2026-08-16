package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The console surface (assessment § 15, screens C1–C9 and C17).
//
// It is read-only apart from cancellation. A second write path to the same
// entities is a second place for the audit cause to be wrong, so console writes
// arrive one at a time, with the screen that needs them and with their own
// cause — never as a batch of "the console can now edit things".
//
// These handlers take the tenant from middleware.GetTenant, which the console
// resolver filled from the signed-in owner — the same shape the M2M handlers
// read, so neither can reach data the other cannot.

type consoleHandlers struct {
	*handlers
	orgs  *repositories.OrganizationRepository
	audit *repositories.AuditRepository
	// portalOrganizationID is tenant zero, needed only by the /v1/me route, which
	// answers for both shells and therefore belongs to neither.
	portalOrganizationID string
}

// cancelSubscription is the console's cancellation, and the **first write on
// this surface** (C6).
//
// It takes at_period_end from the request because on this surface both are
// legitimate: an operator ending a subscription now is a decision somebody made
// deliberately, and refusing it would push them to do it by hand in the M2M API
// where the audit trail says "client:", not who they are.
//
// The audit row is the point of the whole route. "Who cancelled this, and why"
// is asked about cancellations more often than about anything else in the
// system, and it is answerable only if the answer was written at the time.
func (h *consoleHandlers) cancelSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req cancelSubscriptionRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	// Already in the requested end-state: not an error, and not a second audit
	// row. Repeating a cancellation is what a double-click is, and a timeline with
	// two identical entries makes the real history harder to read, not easier.
	if sub.Status == billing.SubscriptionCanceled || (req.AtPeriodEnd && sub.CancelAtPeriodEnd) {
		return c.JSON(newSubscriptionResponse(sub))
	}
	if err := h.subscriber.Cancel(
		c.Context(), sub, req.AtPeriodEnd, billing.CauseManual, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(newSubscriptionResponse(sub))
}

// changeSubscription is the console's mirror of the M2M plan change (C5).
//
// Same body, same service, same cause — only the actor differs, and that is the
// whole reason it is a separate route rather than an operator being told to use
// the integration's credentials. A change made in the console must read as
// "user:01J…" in the trail, not as the integration that happens to serve the
// same tenant.
func (h *consoleHandlers) changeSubscription(c fiber.Ctx) error {
	return h.changePlan(c, actorOfUser(c))
}

// session answers "who am I, where am I, and what can I do here" in one call, so
// the console's shell renders without three round trips (C17).
func (h *consoleHandlers) session(c fiber.Ctx) error {
	org := middleware.GetOrganization(c)
	return c.JSON(sessionResponse{
		OrganizationID: org.ID,
		DisplayName:    org.DisplayName,
		Livemode:       org.Livemode,
		PayoutStatus:   org.PayoutStatus,
		// CanCharge is the server's answer, published so the console can explain
		// the state — never so it can decide it. The gate itself is
		// Organization.AuthorizeCharge, on the write path (ADR 0005).
		CanCharge: org.AuthorizeCharge() == nil,
	})
}

// listInvoicesConsole is C2. It defaults to the current month because that is
// the month an operator opens the screen to look at.
func (h *consoleHandlers) listInvoices(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	today := h.today()
	year := fiber.Query(c, "year", today.Year)
	month := fiber.Query(c, "month", int(today.Month))
	if month < 1 || month > 12 {
		return problem.Validation([]problem.FieldError{
			{Field: "month", Message: "entre 1 e 12", Tag: "range"},
		}).Send(c)
	}
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("cursor inválido").Send(c)
	}

	page, err := h.invoices.ListByMonth(c.Context(), t.OrganizationID, t.Livemode, year, month, pageLimit, start)
	if err != nil {
		return fail(c, err)
	}
	out := make([]invoiceResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newInvoiceResponse(&page.Items[i], nil, today, h.links))
	}
	return c.JSON(pageOf(out, page.LastEvaluatedKey))
}

// getInvoice is C3, the product's most important screen: the invoice with its
// lines and the trail of who changed it.
func (h *consoleHandlers) getInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	trail, err := h.timeline(c, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(invoiceDetailResponse{
		Invoice:  newInvoiceResponse(inv, lines, h.today(), h.links),
		Timeline: trail,
	})
}

// listSubscriptions is C4.
func (h *consoleHandlers) listSubscriptions(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("cursor inválido").Send(c)
	}
	page, err := h.subs.List(c.Context(), t.OrganizationID, t.Livemode, pageLimit, start)
	if err != nil {
		return fail(c, err)
	}
	out := make([]subscriptionResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newSubscriptionResponse(&page.Items[i]))
	}
	return c.JSON(pageOf(out, page.LastEvaluatedKey))
}

// getSubscription is C5: the subscription, what it bills, and its history.
func (h *consoleHandlers) getSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	items, err := h.subs.ListItems(c.Context(), t.OrganizationID, t.Livemode, sub.ID)
	if err != nil {
		return fail(c, err)
	}
	trail, err := h.timeline(c, sub.ID)
	if err != nil {
		return fail(c, err)
	}
	out := subscriptionDetailResponse{
		Subscription: newSubscriptionResponse(sub),
		Timeline:     trail,
	}
	for _, it := range items {
		line := subscriptionItemResponse{ID: it.ID, PriceID: it.PriceID, Quantity: it.Quantity}
		// The price is resolved here rather than left as an id for the console to
		// fetch: whether an item is metered decides whether the screen shows a
		// consumption bar at all, and a screen that has to ask a second time
		// renders the wrong shape first.
		price, err := h.cat.GetPrice(c.Context(), t.OrganizationID, t.Livemode, it.PriceID)
		if err != nil {
			return fail(c, err)
		}
		line.Price = newPriceResponse(price)
		out.Items = append(out.Items, line)
	}
	return c.JSON(out)
}

// listCustomers is C6.
func (h *consoleHandlers) listCustomers(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("cursor inválido").Send(c)
	}
	page, err := h.customers.List(c.Context(), t.OrganizationID, t.Livemode, pageLimit, start)
	if err != nil {
		return fail(c, err)
	}
	out := make([]customerResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newCustomerResponse(&page.Items[i]))
	}
	return c.JSON(pageOf(out, page.LastEvaluatedKey))
}

// getCustomer is C7. The tax id stays masked here exactly as on the M2M surface:
// revealing it is a separate, audited action (assessment § 8), not a field that
// happens to be in the detail payload.
func (h *consoleHandlers) getCustomer(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	subs, err := h.subs.ListByCustomer(c.Context(), t.OrganizationID, t.Livemode, customer.ID, pageLimit)
	if err != nil {
		return fail(c, err)
	}
	trail, err := h.timeline(c, customer.ID)
	if err != nil {
		return fail(c, err)
	}
	out := customerDetailResponse{
		Customer: newCustomerResponse(customer),
		Timeline: trail,
	}
	for i := range subs {
		out.Subscriptions = append(out.Subscriptions, newSubscriptionResponse(&subs[i]))
	}
	return c.JSON(out)
}

// timeline reads the audit trail for one entity — the panel every detail screen
// carries, and the reason audit is written inside the transaction of the change
// it records rather than alongside it.
func (h *consoleHandlers) timeline(c fiber.Ctx, entityID string) ([]auditResponse, error) {
	t := middleware.GetTenant(c)
	entries, err := h.audit.ListForEntity(c.Context(), t.OrganizationID, t.Livemode, entityID, pageLimit)
	if err != nil {
		return nil, err
	}
	out := make([]auditResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditResponse{
			ID:        e.ID,
			Action:    e.Action,
			Cause:     e.Cause,
			Actor:     e.Actor,
			Before:    e.Before,
			After:     e.After,
			RequestID: e.RequestID,
			CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}
