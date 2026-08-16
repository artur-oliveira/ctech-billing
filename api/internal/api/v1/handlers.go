package v1

import (
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// handlers bundles what every route closure needs.
type handlers struct {
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	invoices   *repositories.InvoiceRepository
	usage      *repositories.UsageRepository
	subscriber *services.Subscriber
	// clock is injected so tests are not at the mercy of the wall clock, and so
	// "today" is always decided in one place.
	clock func() time.Time
}

func (h *handlers) now() time.Time    { return h.clock() }
func (h *handlers) today() brcal.Date { return brcal.FromTime(h.clock()) }

// fail maps an error to its RFC 7807 response.
func fail(c fiber.Ctx, err error) error {
	p := problem.FromError(err)
	if p == nil {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return p.Send(c)
}

func (h *handlers) createCustomer(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req createCustomerRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if req.Name == "" {
		return problem.Validation([]problem.FieldError{{Field: "name", Message: "obrigatório", Tag: "required"}}).Send(c)
	}

	customer := &billing.Customer{
		ID:             id.NewWithPrefix(id.PrefixCustomer),
		OrganizationID: t.OrganizationID,
		Livemode:       t.Livemode,
		ExternalRef:    req.ExternalRef,
		UserID:         req.UserID,
		Name:           req.Name,
		Email:          req.Email,
		TaxID:          req.TaxID,
		Metadata:       req.Metadata,
	}
	if err := h.customers.Create(c.Context(), customer, h.now()); err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newCustomerResponse(customer))
}

func (h *handlers) getCustomer(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newCustomerResponse(customer))
}

func (h *handlers) createSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req createSubscriptionRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	var fieldErrs []problem.FieldError
	if req.CustomerID == "" {
		fieldErrs = append(fieldErrs, problem.FieldError{Field: "customer_id", Message: "obrigatório", Tag: "required"})
	}
	if len(req.Items) == 0 {
		fieldErrs = append(fieldErrs, problem.FieldError{Field: "items", Message: "informe ao menos um preço", Tag: "required"})
	}
	for i, it := range req.Items {
		if it.PriceID == "" {
			fieldErrs = append(fieldErrs, problem.FieldError{
				Field:   fmt.Sprintf("items[%d].price_id", i),
				Message: "obrigatório",
				Tag:     "required",
			})
		}
	}
	if len(fieldErrs) > 0 {
		return problem.Validation(fieldErrs).Send(c)
	}

	anchor := brcal.Date{}
	if req.Anchor != "" {
		parsed, err := brcal.Parse(req.Anchor)
		if err != nil {
			return problem.Validation([]problem.FieldError{
				{Field: "anchor", Message: "use o formato YYYY-MM-DD", Tag: "format"},
			}).Send(c)
		}
		anchor = parsed
	}

	// The customer must exist in this tenant. Checking here rather than letting
	// a dangling id through is what keeps an invoice from being addressed to
	// nobody months later.
	if _, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, req.CustomerID); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return problem.Validation([]problem.FieldError{
				{Field: "customer_id", Message: "cliente não encontrado", Tag: "exists"},
			}).Send(c)
		}
		return fail(c, err)
	}

	items := make([]services.SubscribeItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = services.SubscribeItem{PriceID: it.PriceID, Quantity: it.Quantity}
	}

	sub, inv, err := h.subscriber.Subscribe(c.Context(), services.SubscribeInput{
		OrganizationID: t.OrganizationID,
		Livemode:       t.Livemode,
		CustomerID:     req.CustomerID,
		Items:          items,
		Anchor:         anchor,
		NetDays:        req.NetDays,
		Metadata:       req.Metadata,
		Actor:          actorOf(c),
	}, h.now())
	if err != nil {
		return fail(c, err)
	}

	body := fiber.Map{"subscription": newSubscriptionResponse(sub)}
	if inv != nil {
		body["invoice"] = newInvoiceResponse(inv, nil, h.today())
	}
	return c.Status(fiber.StatusCreated).JSON(body)
}

func (h *handlers) getSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newSubscriptionResponse(sub))
}

func (h *handlers) cancelSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req cancelSubscriptionRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	// CauseManual, not CauseCustomer: this is the M2M surface, so the caller is
	// an integration acting on the merchant's behalf. The consumer-initiated
	// cancellation arrives with the portal and carries a different cause, which
	// is what makes the audit trail able to tell them apart.
	if err := h.subscriber.Cancel(c.Context(), sub, req.AtPeriodEnd, billing.CauseManual, actorOf(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(newSubscriptionResponse(sub))
}

func (h *handlers) reportUsage(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req reportUsageRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if req.IdempotencyKey == "" {
		return problem.Validation([]problem.FieldError{
			{Field: "idempotency_key", Message: "obrigatório", Tag: "required"},
		}).Send(c)
	}

	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, req.SubscriptionID)
	if err != nil {
		return fail(c, err)
	}
	items, err := h.subs.ListItems(c.Context(), t.OrganizationID, t.Livemode, sub.ID)
	if err != nil {
		return fail(c, err)
	}
	if len(items) == 0 {
		return problem.Unprocessable("assinatura sem item cobrável").Send(c)
	}
	item, err := usageItem(items, req.PriceID)
	if err != nil {
		return problem.Validation([]problem.FieldError{
			{Field: "price_id", Message: err.Error(), Tag: "exists"},
		}).Send(c)
	}

	occurred := h.now()
	if req.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			return problem.Validation([]problem.FieldError{
				{Field: "occurred_at", Message: "use RFC 3339", Tag: "format"},
			}).Send(c)
		}
		occurred = parsed
	}

	record := &billing.UsageRecord{
		ID:                 id.NewWithPrefix(id.PrefixUsageRecord),
		OrganizationID:     t.OrganizationID,
		Livemode:           t.Livemode,
		SubscriptionItemID: item.ID,
		Quantity:           req.Quantity,
		OccurredAt:         occurred,
		IdempotencyKey:     req.IdempotencyKey,
	}
	// The record is filed under the period its own date falls in, not the
	// subscription's current one: a report that arrives late still belongs to the
	// period it happened in.
	period := periodContaining(sub, record.Date())

	if err := h.usage.Append(c.Context(), record, period.Start, h.now()); err != nil {
		if errors.Is(err, repositories.ErrDuplicateUsage) {
			// The caller's retry succeeded the first time. Reporting this as an
			// error would make every well-behaved integrator log a failure on
			// every retry.
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"recorded": true, "duplicate": true})
		}
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"recorded": true, "duplicate": false})
}

// usageItem picks the item a usage report belongs to.
//
// An omitted price_id resolves only when there is exactly one item. That is not
// leniency for its own sake — it keeps every existing single-meter integration
// working unchanged — but it stops at the first subscription where the answer is
// a guess. Consumption filed against the wrong item is charged at the wrong unit
// price, and the customer finds that before we do.
func usageItem(items []billing.SubscriptionItem, priceID string) (billing.SubscriptionItem, error) {
	if priceID == "" {
		if len(items) == 1 {
			return items[0], nil
		}
		return billing.SubscriptionItem{}, fmt.Errorf(
			"obrigatório: a assinatura tem %d itens e o consumo precisa dizer a qual deles pertence", len(items))
	}
	for _, it := range items {
		if it.PriceID == priceID {
			return it, nil
		}
	}
	return billing.SubscriptionItem{}, fmt.Errorf("a assinatura não tem item para o preço %s", priceID)
}

// periodContaining finds which of the subscription's periods a date falls in.
//
// It walks outward from the current period rather than computing an index,
// because month lengths make the arithmetic inexact and a wrong index files
// consumption against the wrong invoice. The walk is bounded: a report more than
// a couple of years out of place is filed in the nearest period rather than
// searched for forever.
func periodContaining(sub *billing.Subscription, d brcal.Date) billing.Period {
	current := sub.CurrentPeriod()
	if current.Contains(d) {
		return current
	}
	for offset := 1; offset <= 24; offset++ {
		for _, n := range []int{sub.PeriodIndex - offset, sub.PeriodIndex + offset} {
			if n < 0 {
				continue
			}
			if p := sub.Recurrence.PeriodAt(sub.Anchor, n); p.Contains(d) {
				return p
			}
		}
	}
	return current
}

func (h *handlers) getInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newInvoiceResponse(inv, lines, h.today()))
}

func (h *handlers) listInvoices(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	today := h.today()
	year := fiber.Query(c, "year", today.Year)
	month := fiber.Query(c, "month", int(today.Month))
	if month < 1 || month > 12 {
		return problem.Validation([]problem.FieldError{
			{Field: "month", Message: "entre 1 e 12", Tag: "range"},
		}).Send(c)
	}

	page, err := h.invoices.ListByMonth(c.Context(), t.OrganizationID, t.Livemode, year, month, 100, nil)
	if err != nil {
		return fail(c, err)
	}
	out := make([]invoiceResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newInvoiceResponse(&page.Items[i], nil, today))
	}
	return c.JSON(listResponse[invoiceResponse]{Data: out, HasMore: page.LastEvaluatedKey != nil})
}

func (h *handlers) getEntitlements(c fiber.Ctx) error {
	t := middleware.GetTenant(c)

	customerID := c.Query("customer_id")
	if ref := c.Query("customer_ref"); ref != "" {
		customer, err := h.customers.GetByExternalRef(c.Context(), t.OrganizationID, t.Livemode, ref)
		if err != nil {
			return fail(c, err)
		}
		customerID = customer.ID
	}
	if customerID == "" {
		return problem.BadRequest("informe customer_id ou customer_ref").Send(c)
	}

	subs, err := h.subs.ListByCustomer(c.Context(), t.OrganizationID, t.Livemode, customerID, 100)
	if err != nil {
		return fail(c, err)
	}

	resp := entitlementResponse{CustomerID: customerID}
	for i := range subs {
		sub := &subs[i]
		entitled := sub.IsEntitled()
		resp.Entitled = resp.Entitled || entitled
		resp.Subscriptions = append(resp.Subscriptions, entitlementSubscription{
			ID:       sub.ID,
			Status:   sub.Status,
			Entitled: entitled,
			Period:   sub.CurrentPeriod(),
		})
	}
	return c.JSON(resp)
}

// actorOf names who performed an action, for the audit trail. For an M2M call it
// is the OAuth client — which is the honest answer, and the reason credentials
// are per integration rather than shared.
func actorOf(c fiber.Ctx) string {
	if cl := middleware.GetClaims(c); cl != nil {
		if cl.AZP != "" {
			return "client:" + cl.AZP
		}
		return "client:" + cl.Sub
	}
	return "unknown"
}

// actorOfUser names a person, for the console and portal surfaces.
//
// Separate from actorOf and prefixed differently on purpose: "client:dfe" and
// "user:01J..." are two different kinds of actor, and an audit trail that renders
// them the same way cannot answer whether an integration or a human did
// something.
func actorOfUser(c fiber.Ctx) string {
	if cl := middleware.GetClaims(c); cl != nil && cl.Sub != "" {
		return "user:" + cl.Sub
	}
	return "unknown"
}
