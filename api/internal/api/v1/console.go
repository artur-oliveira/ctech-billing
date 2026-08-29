package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
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
	orgs   *repositories.OrganizationRepository
	audit  *repositories.AuditRepository
	credit *repositories.CreditNoteRepository
	// invoicer issues a draft invoice, through the same path the sweep uses.
	invoicer *services.Invoicer
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
	// The customers on this page, read once each. A page holds at most `pageLimit`
	// invoices and usually far fewer distinct customers, so this is a handful of
	// point reads rather than a join — and it is deliberately not an index or a
	// denormalized copy of the name on the invoice, which would be a second
	// version of a record that gets edited.
	//
	// A customer that cannot be read is not an error: the invoice is still a
	// real document and the row renders without a name. Failing the whole
	// listing because one customer row is missing would take the screen down
	// for a defect it can survive.
	names := map[string]string{}
	for i := range page.Items {
		id := page.Items[i].CustomerID
		if _, seen := names[id]; seen || id == "" {
			continue
		}
		customer, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, id)
		if err != nil {
			names[id] = ""
			continue
		}
		names[id] = customer.Name
	}

	out := make([]consoleInvoiceListItem, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, consoleInvoiceListItem{
			invoiceResponse: newInvoiceResponse(&page.Items[i], nil, today, h.links),
			CustomerName:    names[page.Items[i].CustomerID],
		})
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
	return h.invoiceDetail(c, inv)
}

// finalizeInvoice issues a draft invoice (C3).
//
// A DRAFT invoice is normally transient — the sweep creates and finalizes one in
// the same call — so this route exists for the row a half-failed run left
// behind: written, unnumbered, and never picked up again, because the sweep
// skips a period it has already billed. Without it that invoice is
// unrecoverable from any screen.
//
// Already OPEN is not an error. Finalizing twice is what a double-click is, and
// the second one must not burn a second invoice number.
func (h *consoleHandlers) finalizeInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if inv.Status != billing.InvoiceDraft {
		return h.invoiceDetail(c, inv)
	}
	if err := h.invoicer.Issue(
		c.Context(), inv, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return h.invoiceDetail(c, inv)
}

// voidInvoice cancels an invoice that should never have been issued (C3).
//
// It is the destructive one on this surface, and the domain is what bounds it:
// only DRAFT and OPEN reach VOID, so a PAID invoice cannot be voided at all —
// money that arrived is corrected with a credit note, never by deleting the
// document that recorded it.
//
// Already VOID answers with the invoice rather than an error, for the same
// reason a repeated cancellation does: a second identical audit row makes a
// timeline harder to read.
func (h *consoleHandlers) voidInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if inv.Status == billing.InvoiceVoid {
		return h.invoiceDetail(c, inv)
	}
	if _, err := h.invoices.Transition(
		c.Context(), inv, billing.InvoiceVoid, billing.CauseManual,
		actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return h.invoiceDetail(c, inv)
}

// creditInvoice issues a credit note (C3).
//
// The correction path for an invoice that has been issued, and the only one:
// editing the lines of a document a customer has already been shown destroys the
// record of what they were asked to pay, which is what an invoice is for.
//
// It never moves money. `refunded_externally` records that wallet or the PSP
// returned it, with the reference there — billing issuing money is how this
// service would start becoming a wallet.
func (h *consoleHandlers) creditInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req creditNoteRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if req.Reason == "" {
		// Required, and refused here rather than defaulted: a credit note with no
		// reason is the one document nobody can explain a year later, and every
		// default this could invent would be a sentence a person did not write.
		return problem.BadRequest("informe o motivo do crédito").Send(c)
	}
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}

	cn := &billing.CreditNote{
		ID:                 id.NewWithPrefix(id.PrefixCreditNote),
		InvoiceID:          inv.ID,
		Amount:             req.Amount,
		Reason:             req.Reason,
		RefundedExternally: req.RefundedExternally,
		ExternalRefundRef:  req.ExternalRefundRef,
		Metadata:           req.Metadata,
	}
	if err := h.credit.Issue(
		c.Context(), cn, inv, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newCreditNoteResponse(cn))
}

// invoiceDetail is the response every write on this surface answers with: the
// invoice as C3 renders it, freshly composed rather than echoed back.
//
// One shape for the read and the three writes, so a screen re-renders from the
// response instead of refetching — and so a write can never publish a field the
// read does not.
func (h *consoleHandlers) invoiceDetail(c fiber.Ctx, inv *billing.Invoice) error {
	t := middleware.GetTenant(c)
	lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	trail, err := h.timeline(c, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	notes, err := h.credit.ListByInvoice(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	// Best effort, like the listing's: a name is what the screen shows a person,
	// and its absence is a worse row rather than a broken page.
	name := ""
	if customer, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, inv.CustomerID); err == nil {
		name = customer.Name
	}
	return c.JSON(newInvoiceDetailResponse(inv, lines, notes, trail, name, h.today(), h.links))
}

// createCustomer is C6's "novo cliente", on the shared implementation.
func (h *consoleHandlers) createCustomer(c fiber.Ctx) error {
	return h.createCustomerAs(c, actorOfUser(c))
}

// createProduct adds a product to the catalogue (C8).
func (h *consoleHandlers) createProduct(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req createProductRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if req.Name == "" {
		return problem.Validation([]problem.FieldError{
			{Field: "name", Message: "obrigatório", Tag: "required"},
		}).Send(c)
	}

	product := &billing.Product{
		ID:             id.NewWithPrefix(id.PrefixProduct),
		OrganizationID: t.OrganizationID,
		Livemode:       t.Livemode,
		Name:           req.Name,
		// A product nobody can sell is not what "novo produto" means. Archiving
		// is a later, separate decision, and there is no screen that asks for an
		// inactive one at creation.
		Active:   true,
		OwnerKey: req.OwnerKey,
		Metadata: req.Metadata,
	}
	if err := h.cat.CreateProduct(
		c.Context(), product, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newProductResponse(product, nil))
}

// createPrice adds a price to a product (C9).
//
// There is no "editar preço" and there never will be: a price is immutable, so
// changing what something costs is this route plus, if the old one should stop
// being sold, an explicit archive. The UI is expected to teach that rather than
// hide it behind an edit button that creates something new behind the operator's
// back.
func (h *consoleHandlers) createPrice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req createPriceRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	// Read first: a price pointing at a product that does not exist bills
	// nothing and is discovered at invoice generation, weeks later.
	product, err := h.cat.GetProduct(c.Context(), t.OrganizationID, t.Livemode, req.ProductID)
	if err != nil {
		return fail(c, err)
	}

	price := &billing.Price{
		ID:             id.NewWithPrefix(id.PrefixPrice),
		OrganizationID: t.OrganizationID,
		Livemode:       t.Livemode,
		ProductID:      product.ID,
		Type:           req.Type,
		Currency:       billing.CurrencyBRL,
		UnitAmount:     req.UnitAmount,
		Recurrence:     req.Recurrence,
		Timing:         req.Timing,
		Metadata:       req.Metadata,
	}
	// Refused here rather than at the charge: a fixed price above the wallet's
	// per-client ceiling produces an invoice that is issued and then cannot be
	// paid, which is the worst of both — the customer owes it and no screen can
	// collect it (ADR 0004).
	if price.ExceedsChargeCeiling() {
		return problem.Unprocessable(
			"o valor excede o teto de cobrança do wallet; uma fatura nesse valor seria emitida e não poderia ser paga",
		).Send(c)
	}
	if err := h.cat.CreatePrice(
		c.Context(), price, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newPriceResponse(price))
}

// archivePrice withdraws a price from the catalogue (C9).
//
// It does not touch the subscriptions already on it, and that is the whole
// point: archiving means "do not sell this any more", never "change what
// existing customers pay". Repeating it answers with the price rather than an
// error — a double click is not a second decision — and writes no second audit
// row, because the update is conditional on it not already being archived.
func (h *consoleHandlers) archivePrice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	price, err := h.cat.GetPrice(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if price.Archived {
		return c.JSON(newPriceResponse(price))
	}
	if err := h.cat.ArchivePrice(
		c.Context(), price, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(newPriceResponse(price))
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
