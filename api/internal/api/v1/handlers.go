package v1

import (
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// handlers bundles what every route closure needs.
type handlers struct {
	// documents renders and serves invoice PDFs, shared by both signed-in
	// surfaces because they render the same document from the same invoice.
	// Nil when the deployment has no bucket, and then neither route exists.
	documents  *services.Documents
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	invoices   *repositories.InvoiceRepository
	usage      *repositories.UsageRepository
	subscriber *services.Subscriber
	// cat is the catalogue. It sits on the shared struct rather than on one
	// surface's because all three read it and they must read it identically: a
	// quota an integration sees in a price's metadata and a quota the console
	// shows an operator are the same number or they are a support call.
	cat *repositories.CatalogRepository
	// links signs the public checkout URL published on every invoice payload. It
	// lives here rather than on one of the embedding structs because all three
	// surfaces render invoices, and a link an integrator gets from the M2M API
	// that an operator cannot get from the console is a support call.
	//
	// Nil when payment links are not configured, which is a real deployment: the
	// checkout routes are then not mounted at all.
	links *services.PayLink
	// clock is injected so tests are not at the mercy of the wall clock, and so
	// "today" is always decided in one place.
	clock func() time.Time

	// The health report's dependencies, and nothing else uses them. They are the
	// raw client and the raw backend rather than a repository because the report
	// asks a question no repository has a method for — "is the table there" —
	// and giving one a HealthCheck method would put an operations concern on
	// every type that stores something.
	db            *dynamodb.Client
	cache         cache.Backend
	invoicesTable string
	appVersion    string
}

func (h *handlers) now() time.Time    { return h.clock() }
func (h *handlers) today() brcal.Date { return brcal.FromTime(h.clock()) }

// fail maps an error to its RFC 7807 response, and logs the ones the response
// deliberately hides.
//
// A 5xx body says "internal error" and nothing else, which is right — the client
// must not learn that a DynamoDB table is throttling or which field failed to
// decrypt. But that makes this the **only** place the real error still exists,
// and until it was written down here it existed nowhere: handlers return
// `fail(c, err)`, which sends the response and returns nil, so Fiber's
// ErrorHandler — the one thing that did log — never ran for a handler error.
// Every 500 this service has ever served was silent on the instance.
//
// 4xx are not logged: they are the caller being told no, they are already in
// the access log with their status, and logging them at error level trains
// whoever reads the group to ignore the level.
func fail(c fiber.Ctx, err error) error {
	p := problem.FromError(err)
	if p == nil {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return p.WithCause(err).Send(c)
}

func (h *handlers) createCustomer(c fiber.Ctx) error {
	return h.createCustomerAs(c, actorOf(c))
}

// createCustomerAs is the one implementation both surfaces use (C6). The M2M
// route names the integration, the console route names the operator, and
// nothing else differs — which is exactly why it is one function: a second
// customer-creation path is a second place for the tenant, the validation or
// the audit actor to be wrong.
func (h *handlers) createCustomerAs(c fiber.Ctx, actor string) error {
	t := middleware.GetTenant(c)
	var req createCustomerRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	// Integrators already in production call this: lengths, an email's shape and
	// a document's shape are checked, never a CPF's check digits (test data with
	// made-up documents is legitimate in test mode, and refusing it overnight
	// would break them).
	ch := &checks{}
	ch.text("name", req.Name, true, limits.CustomerName)
	ch.email("email", req.Email, false)
	ch.taxID("tax_id", req.TaxID, false, false)
	ch.text("external_ref", req.ExternalRef, false, limits.ExternalRef)
	ch.text("user_id", req.UserID, false, limits.ExternalRef)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
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
	if err := h.customers.Create(
		c.Context(), customer, actor, middleware.GetRequestID(c), h.now(),
	); err != nil {
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
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	var fieldErrs []problem.FieldError
	if req.CustomerID == "" {
		fieldErrs = append(fieldErrs, fieldErr("customer_id", "required", "required"))
	}
	if len(req.Items) == 0 {
		fieldErrs = append(fieldErrs, fieldErr("items", "required", "at least one price is required"))
	}
	if len(req.Items) > limits.MaxSubscriptionItems {
		fieldErrs = append(fieldErrs, fieldErr("items", "too_many", fmt.Sprintf("at most %d items", limits.MaxSubscriptionItems), "max", limits.MaxSubscriptionItems))
	}
	if req.NetDays < 0 || req.NetDays > limits.MaxNetDays {
		fieldErrs = append(fieldErrs, fieldErr("net_days", "out_of_range", fmt.Sprintf("between 0 and %d days", limits.MaxNetDays), "min", 0, "max", limits.MaxNetDays))
	}
	for i, it := range req.Items {
		if it.PriceID == "" {
			fieldErrs = append(fieldErrs, fieldErr(fmt.Sprintf("items[%d].price_id", i), "required", "required"))
		}
		if it.Quantity > limits.MaxUsageQuantity {
			fieldErrs = append(fieldErrs, fieldErr(fmt.Sprintf("items[%d].quantity", i), "too_large", fmt.Sprintf("at most %d", limits.MaxUsageQuantity), "max", limits.MaxUsageQuantity))
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
				fieldErr("anchor", "invalid_format", "use the format YYYY-MM-DD", "format", "YYYY-MM-DD"),
			}).Send(c)
		}
		anchor = parsed
		ch := &checks{}
		ch.date("anchor", anchor, limits.MinDate, limits.MaxDate(brcal.FromTime(h.now())))
		if len(ch.errs) > 0 {
			return problem.Validation(ch.errs).Send(c)
		}
	}

	// The customer must exist in this tenant. Checking here rather than letting
	// a dangling id through is what keeps an invoice from being addressed to
	// nobody months later.
	if _, err := h.customers.Get(c.Context(), t.OrganizationID, t.Livemode, req.CustomerID); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return problem.Validation([]problem.FieldError{
				fieldErr("customer_id", "not_found", "customer not found"),
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

	// The invoice is what makes this response actionable. A caller subscribing
	// somebody to a paid plan needs to know there is a bill and where to send them
	// to pay it, and `invoice.checkout_url` is that — absent on the free and the
	// arrears plans, where the first period genuinely costs nothing yet.
	body := fiber.Map{"subscription": newSubscriptionResponse(sub)}
	if inv != nil {
		body["invoice"] = newInvoiceResponse(inv, nil, h.today(), h.links)
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
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
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

// changeSubscription is the M2M upgrade/downgrade.
func (h *handlers) changeSubscription(c fiber.Ctx) error {
	return h.changePlan(c, actorOf(c))
}

// changePlan moves a subscription onto a different price set and returns it with
// the proration invoice, when the change produced one.
//
// The actor is a parameter rather than read from the context, because this one
// body serves two surfaces and they name their actor differently: an integration
// is "client:dfe-billing" and an operator is "user:01J…". A single actorOf here
// would record every console change as if an integration had made it, which is
// exactly the distinction the audit trail exists to keep.
func (h *handlers) changePlan(c fiber.Ctx, actor string) error {
	t := middleware.GetTenant(c)
	var req changeSubscriptionRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}

	var fieldErrs []problem.FieldError
	if len(req.Items) == 0 {
		fieldErrs = append(fieldErrs, fieldErr("items", "required", "at least one price is required"))
	}
	for i, it := range req.Items {
		if it.PriceID == "" {
			fieldErrs = append(fieldErrs, fieldErr(fmt.Sprintf("items[%d].price_id", i), "required", "required"))
		}
		if it.Quantity > limits.MaxUsageQuantity {
			fieldErrs = append(fieldErrs, fieldErr(fmt.Sprintf("items[%d].quantity", i), "too_large", fmt.Sprintf("at most %d", limits.MaxUsageQuantity), "max", limits.MaxUsageQuantity))
		}
	}
	if len(req.Items) > limits.MaxSubscriptionItems {
		fieldErrs = append(fieldErrs, fieldErr("items", "too_many", fmt.Sprintf("at most %d items", limits.MaxSubscriptionItems), "max", limits.MaxSubscriptionItems))
	}
	if req.Effective != "" && req.Effective != effectiveNow {
		fieldErrs = append(fieldErrs, fieldErr("effective", "unsupported_value", `only "now" is accepted for now`, "allowed", []string{effectiveNow}))
	}
	if len(fieldErrs) > 0 {
		return problem.Validation(fieldErrs).Send(c)
	}

	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}

	items := make([]services.SubscribeItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = services.SubscribeItem{PriceID: it.PriceID, Quantity: it.Quantity}
	}

	inv, err := h.subscriber.ChangePlan(c.Context(), sub, services.ChangeInput{
		Items: items,
		// CauseManual on both surfaces this mounts on: an integration acting for
		// the merchant and an operator in the console are both "somebody decided
		// this", as opposed to the customer doing it themselves in the portal,
		// which would carry CauseCustomer.
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: middleware.GetRequestID(c),
	}, h.now())
	if err != nil {
		return fail(c, err)
	}

	// No invoice is a real, common outcome — a downgrade, or a swap that costs
	// nothing for the remainder of the period. The field is absent rather than
	// null-with-a-zero-total so a caller branches on the same shape they already
	// branch on when subscribing to a free plan.
	body := fiber.Map{"subscription": newSubscriptionResponse(sub)}
	if inv != nil {
		body["invoice"] = newInvoiceResponse(inv, nil, h.today(), h.links)
	}
	return c.JSON(body)
}

func (h *handlers) reportUsage(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req reportUsageRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.text("idempotency_key", req.IdempotencyKey, true, 255)
	if req.Quantity < 0 || req.Quantity > limits.MaxUsageQuantity {
		ch.fail("quantity", "out_of_range", fmt.Sprintf("between 0 and %d", limits.MaxUsageQuantity), "min", 0, "max", limits.MaxUsageQuantity)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
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
		return problem.Unprocessable("subscription has no billable item").WithCode("subscription_no_billable_item").Send(c)
	}
	item, err := usageItem(items, req.PriceID)
	if err != nil {
		return problem.Validation([]problem.FieldError{
			usageItemFieldErr(err),
		}).Send(c)
	}

	occurred := h.now()
	if req.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			return problem.Validation([]problem.FieldError{
				fieldErr("occurred_at", "invalid_format", "use RFC 3339", "format", "RFC3339"),
			}).Send(c)
		}
		// Usage is reported as it happens: a timestamp more than a day ahead, or
		// before 2000, is a clock or a unit error, not usage.
		if parsed.Before(limits.MinDate.Time()) || parsed.After(h.now().Add(24*time.Hour)) {
			return problem.Validation([]problem.FieldError{
				fieldErr("occurred_at", "out_of_range", "outside the accepted range"),
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

// usageItemError is a refused usage price_id, with the stable code the client
// translates ("required" when it is ambiguous, "not_found" when it matches none).
type usageItemError struct{ code, msg string }

func (e *usageItemError) Error() string { return e.msg }

func usageItemFieldErr(err error) problem.FieldError {
	var ue *usageItemError
	if errors.As(err, &ue) {
		return fieldErr("price_id", ue.code, ue.msg)
	}
	return fieldErr("price_id", "not_found", err.Error())
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
		return billing.SubscriptionItem{}, &usageItemError{code: "required", msg: fmt.Sprintf(
			"required: the subscription has %d items and the usage must say which one it belongs to", len(items))}
	}
	for _, it := range items {
		if it.PriceID == priceID {
			return it, nil
		}
	}
	return billing.SubscriptionItem{}, &usageItemError{code: "not_found", msg: fmt.Sprintf("the subscription has no item for price %s", priceID)}
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
	return c.JSON(newInvoiceResponse(inv, lines, h.today(), h.links))
}

func (h *handlers) listInvoices(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	today := h.today()
	year := fiber.Query(c, "year", today.Year)
	month := fiber.Query(c, "month", int(today.Month))
	if month < 1 || month > 12 {
		return problem.Validation([]problem.FieldError{
			fieldErr("month", "out_of_range", "between 1 and 12", "min", 1, "max", 12),
		}).Send(c)
	}

	page, err := h.invoices.ListByMonth(c.Context(), t.OrganizationID, t.Livemode, year, month, 100, nil)
	if err != nil {
		return fail(c, err)
	}
	out := make([]invoiceResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newInvoiceResponse(&page.Items[i], nil, today, h.links))
	}
	return c.JSON(listResponse[invoiceResponse]{Data: out, HasMore: page.LastEvaluatedKey != nil})
}

// pageLimit bounds every page. It is not a client parameter: nobody scrolling a
// table has a reason to ask for a different page size, and a parameter that
// reaches DynamoDB's limit is a parameter that can be used to make one request
// expensive.
const pageLimit = 100

// listProducts is the catalogue, on every surface (console C8, and the M2M read
// an integration needs to render a plan picker).
//
// It is one handler rather than one per surface because the only thing that
// differs between them is which resolver filled the tenant, and that has already
// happened by the time this runs. Two copies would be two answers to "what does
// this plan include" — and the quotas an integration enforces come out of the
// price metadata published here, so a divergence is a customer allowed past a
// limit the invoice says they have.
func (h *handlers) listProducts(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	products, err := h.cat.ListProducts(c.Context(), t.OrganizationID, t.Livemode, pageLimit)
	if err != nil {
		return fail(c, err)
	}
	out := make([]productResponse, 0, len(products))
	for i := range products {
		out = append(out, newProductResponse(&products[i], nil))
	}
	return c.JSON(listResponse[productResponse]{Data: out})
}

// getProduct is a product and its prices, active and archived together
// (console C9).
//
// Archived prices are returned rather than filtered out because a subscription
// created under one keeps billing at it — a price list that hides them makes an
// invoice look like it came from nowhere (OVERVIEW.md § 7).
func (h *handlers) getProduct(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	product, err := h.cat.GetProduct(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	prices, err := h.cat.ListPrices(c.Context(), t.OrganizationID, t.Livemode, pageLimit)
	if err != nil {
		return fail(c, err)
	}
	mine := make([]billing.Price, 0, len(prices))
	for _, p := range prices {
		if p.ProductID == product.ID {
			mine = append(mine, p)
		}
	}
	return c.JSON(newProductResponse(product, mine))
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

		out := entitlementSubscription{
			ID:                sub.ID,
			Status:            sub.Status,
			Entitled:          entitled,
			CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
			Period:            sub.CurrentPeriod(),
		}
		if err := h.describeEntitlement(c, sub, &out); err != nil {
			return fail(c, err)
		}
		resp.Subscriptions = append(resp.Subscriptions, out)
	}
	return c.JSON(resp)
}

// describeEntitlement fills in what the consuming product needs beyond
// "entitled: true": which plan this is, what it includes, and whether there is a
// bill waiting to be paid.
//
// Those three arrive together because a consumer asks them together. Without the
// plan and its quotas, a product that gates on a limit has to hold its own copy
// of the catalogue and the two drift; without the open invoice, a customer whose
// subscription is PAST_DUE sees "pagamento pendente" and no way to pay it.
//
// It costs one read per item plus one invoice query per subscription. That is
// acceptable **because of who calls it**: a product checks entitlement for one
// customer at a time, and caches the answer. It would not be acceptable in a
// list endpoint, and there is deliberately not one.
func (h *handlers) describeEntitlement(c fiber.Ctx, sub *billing.Subscription, out *entitlementSubscription) error {
	t := middleware.GetTenant(c)

	items, err := h.subs.ListItems(c.Context(), t.OrganizationID, t.Livemode, sub.ID)
	if err != nil {
		return err
	}
	for _, it := range items {
		price, err := h.cat.GetPrice(c.Context(), t.OrganizationID, t.Livemode, it.PriceID)
		if err != nil {
			return err
		}
		out.Items = append(out.Items, entitlementItem{
			PriceID:    price.ID,
			ProductID:  price.ProductID,
			Type:       price.Type,
			UnitAmount: price.UnitAmount,
			Quantity:   it.Quantity,
			// The quotas live here. Billing does not read them (ADR 0008 — metadata
			// is opaque to this service), it only carries them; what a quota means
			// is the consuming product's business rule, and this is the one place
			// both sides agree on the number.
			Metadata: price.Metadata,
		})
		// The plan name comes from the first item, which is well defined rather
		// than arbitrary: every item of one subscription belongs to one plan, and
		// Subscriber.resolveItemPrices refuses a set that mixes owners or cycles.
		if out.Plan == "" {
			out.Plan = price.Metadata[metadataKeyPlan]
		}
		if out.PriceID == "" {
			out.PriceID = price.ID
		}
	}

	// Newest first, so the first OPEN one is the bill the customer is looking at.
	// Capped rather than paginated: a consumer needs the outstanding invoice, and
	// the full history is what the invoice list is for.
	invoices, err := h.invoices.ListBySubscription(c.Context(), t.OrganizationID, t.Livemode, sub.ID, entitlementInvoiceScan)
	if err != nil {
		return err
	}
	for i := range invoices {
		if invoices[i].Status != billing.InvoiceOpen {
			continue
		}
		// Rendered through newInvoiceResponse rather than field by field, so
		// `checkout_url` obeys exactly the same Payable-and-links rule here as it
		// does everywhere else. A second copy of that rule is how the M2M surface
		// comes to publish a link the console does not, or one that 404s.
		rendered := newInvoiceResponse(&invoices[i], nil, h.today(), h.links)
		out.OpenInvoice = &entitlementInvoice{
			ID:          rendered.ID,
			TotalCents:  rendered.Total,
			DueDate:     rendered.DueDate,
			CheckoutURL: rendered.CheckoutURL,
		}
		break
	}
	return nil
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
