package v1

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/settlement"
)

// The portal surface (ADR 0012), screens P1–P3.
//
// Two rules run through all of it. Every read is filtered to **this customer**,
// not merely to this tenant — the console's tenant scoping is not enough here,
// because everyone in the portal shares one tenant. And nothing internal reaches
// the payload: the translation lives in portal_dto.go, and these handlers never
// publish a status, a metadata map or an audit entry.

// portalLimit bounds a page. A person's own invoice history is small, and the
// screen it feeds is a list somebody scrolls, not a report.
const portalLimit = 50

type portalHandlers struct {
	*handlers
	// collector is nil when the deployment has no wallet configuration. The pay
	// route is then not mounted at all, rather than mounted and failing at the
	// last step in front of somebody holding a bill.
	collector *services.Collector
	// bus turns the settlement stream from a poll into a notification. Nil where
	// no Valkey is configured, which the stream handles by re-reading faster.
	bus settlement.Bus
}

// session is who the portal thinks you are. The console has an equivalent, and
// they are separate calls on purpose: holding one identity says nothing about
// holding the other.
func (h *portalHandlers) session(c fiber.Ctx) error {
	customer := middleware.GetCustomer(c)
	return c.JSON(portalSessionResponse{
		CustomerID:    customer.ID,
		Name:          customer.Name,
		Email:         customer.Email,
		Since:         civilDate(customer.Since),
		TermsAccepted: customer.AcceptedCurrentTerms(),
	})
}

// acceptTerms records agreement to the billing terms addendum.
//
// It takes no body. The version is the server's — a client that could name one
// could accept a document it chose, which is the whole failure mode this guards
// against, and there is only ever one version in force to accept.
func (h *portalHandlers) acceptTerms(c fiber.Ctx) error {
	customer := middleware.GetCustomer(c)
	if err := h.customers.AcceptTerms(
		c.Context(), customer, actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(portalSessionResponse{
		CustomerID:    customer.ID,
		Name:          customer.Name,
		Email:         customer.Email,
		Since:         civilDate(customer.Since),
		TermsAccepted: customer.AcceptedCurrentTerms(),
	})
}

// listSubscriptions is P4, and P1's main block: what am I paying for.
func (h *portalHandlers) listSubscriptions(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	subs, err := h.subs.ListByCustomer(c.Context(), t.OrganizationID, t.Livemode, customer.ID, portalLimit)
	if err != nil {
		return fail(c, err)
	}
	out := make([]portalSubscriptionResponse, 0, len(subs))
	for i := range subs {
		view, err := h.describeSubscription(c, &subs[i])
		if err != nil {
			return fail(c, err)
		}
		out = append(out, view)
	}
	return c.JSON(listResponse[portalSubscriptionResponse]{Data: out})
}

// getSubscription is P5. What it includes, what it costs, when it renews, and
// how to cancel — the last one stated rather than hidden, because a
// subscription nobody can find how to cancel is a subscription disputed with a
// bank instead.
func (h *portalHandlers) getSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if sub.CustomerID != customer.ID {
		// 404 rather than 403: this tenant holds every portal user's data, so a
		// 403 would confirm that somebody else's subscription exists under that id.
		return problem.NotFound("assinatura não encontrada").Send(c)
	}
	view, err := h.describeSubscription(c, sub)
	if err != nil {
		return fail(c, err)
	}
	view.Invoices, err = h.recentInvoices(c, sub.ID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(view)
}

// recentInvoices is this subscription's billing history, newest first.
//
// The query is the sparse lookup partition written at invoice creation
// (LookupSubscriptionInvoicesPK), not a tenant listing filtered down — a
// customer with two years of invoices should not pay for reading all of them to
// show six.
func (h *portalHandlers) recentInvoices(c fiber.Ctx, subscriptionID string) ([]portalInvoiceResponse, error) {
	t := middleware.GetTenant(c)
	today := h.today()

	invoices, err := h.invoices.ListBySubscription(
		c.Context(), t.OrganizationID, t.Livemode, subscriptionID, subscriptionInvoiceLimit)
	if err != nil {
		return nil, err
	}

	out := make([]portalInvoiceResponse, 0, len(invoices))
	for i := range invoices {
		inv := &invoices[i]
		// Draft invoices are not bills yet, same rule as the invoice list: an
		// amount that can still change must not appear as history.
		if inv.Status == billing.InvoiceDraft {
			continue
		}
		lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
		if err != nil {
			return nil, err
		}
		view := newPortalInvoiceResponse(inv, lines, today)
		// The lines are read to name the invoice and then dropped. This is a
		// history row, and the breakdown belongs on the invoice's own screen.
		view.Lines = nil
		out = append(out, view)
	}
	return out, nil
}

// listInvoices is P2.
func (h *portalHandlers) listInvoices(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)
	today := h.today()

	page, err := h.invoices.ListByCustomer(c.Context(), t.OrganizationID, t.Livemode, customer.ID, portalLimit)
	if err != nil {
		return fail(c, err)
	}
	out := make([]portalInvoiceResponse, 0, len(page))
	for i := range page {
		inv := &page[i]
		// Draft invoices are not bills yet. Showing one would be presenting an
		// amount that can still change as something owed.
		if inv.Status == billing.InvoiceDraft {
			continue
		}
		lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
		if err != nil {
			return fail(c, err)
		}
		out = append(out, newPortalInvoiceResponse(inv, lines, today))
	}
	return c.JSON(listResponse[portalInvoiceResponse]{Data: out})
}

// getInvoice is P3: what it is, how much, when, and the lines that explain it.
func (h *portalHandlers) getInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if inv.CustomerID != customer.ID || inv.Status == billing.InvoiceDraft {
		return problem.NotFound("fatura não encontrada").Send(c)
	}
	lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newPortalInvoiceResponse(inv, lines, h.today()))
}

// payInvoice opens (or re-opens) the PIX charge for one of my invoices — the
// signed-in half of screen X1.
//
// It shares Collector with the public payment link, so a customer who pays from
// the portal and one who pays from an e-mailed link go through the same charge,
// the same idempotency key and the same audit trail. Two payment paths that
// merely agree are two payment paths that will stop agreeing.
func (h *portalHandlers) payInvoice(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if inv.CustomerID != customer.ID || inv.Status == billing.InvoiceDraft {
		return problem.NotFound("fatura não encontrada").Send(c)
	}

	session, _, err := h.collector.Pay(
		c.Context(), t.OrganizationID, t.Livemode, inv.ID, "user:"+customer.UserID, middleware.GetRequestID(c), h.now())
	if err != nil {
		return failCheckout(c, err)
	}
	lines, err := h.invoices.ListItems(c.Context(), t.OrganizationID, t.Livemode, inv.ID)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(portalPaymentResponse{
		Invoice: newPortalInvoiceResponse(inv, lines, h.today()),
		Payment: checkoutPayment{
			Method:    string(billing.MethodPIX),
			PixCode:   session.PixCode,
			ExpiresAt: session.ExpiresAt,
		},
	})
}

// cancelSubscription is the consumer's own cancellation — **at period end only**.
//
// A consumer cancelling in the middle of a period they have already paid for is
// asking for money back, and money back is a credit note: a different decision,
// with a different authority and a different audit cause. Silently turning one
// into the other is how a billing system starts refunding by accident, so the
// portal simply does not offer the immediate one. Somebody who wants it talks to
// a human, and the human uses the console.
//
// The cause is CauseCustomer, not CauseManual: an operator ending a subscription
// and a customer ending their own are the same transition for two entirely
// different reasons, and six months later the audit trail is the only thing that
// still knows which happened.
func (h *portalHandlers) cancelSubscription(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	sub, err := h.subs.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if sub.CustomerID != customer.ID {
		return problem.NotFound("assinatura não encontrada").Send(c)
	}
	// Cancelling something already ending is not an error to the person who asked
	// — they got what they wanted. It writes no second audit row either, because
	// nothing changed.
	if sub.Status == billing.SubscriptionCanceled || sub.CancelAtPeriodEnd {
		view, err := h.describeSubscription(c, sub)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(view)
	}

	if err := h.subscriber.Cancel(
		c.Context(), sub, true, billing.CauseCustomer, "user:"+customer.UserID, middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	view, err := h.describeSubscription(c, sub)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(view)
}

// describeSubscription turns a subscription and its price into the three facts a
// person wants: what it is, what it costs, and when it renews.
func (h *portalHandlers) describeSubscription(c fiber.Ctx, sub *billing.Subscription) (portalSubscriptionResponse, error) {
	t := middleware.GetTenant(c)
	state, tone := subscriptionState(sub)
	period := sub.CurrentPeriod()

	out := portalSubscriptionResponse{
		ID:          sub.ID,
		Description: "Assinatura",
		State:       state,
		Tone:        tone,
		Period:      portalPeriod{Start: period.Start, End: period.End},
		Since:       civilDate(sub.Since),
		// Everything short of already-ended can be stopped. Whether it stops now
		// or at the period end is the screen's question, not this one's.
		Cancelable: sub.Status != billing.SubscriptionCanceled,
	}
	if sub.Status != billing.SubscriptionCanceled && !sub.CancelAtPeriodEnd {
		out.Renewal = new(period.End)
	}

	items, err := h.subs.ListItems(c.Context(), t.OrganizationID, t.Livemode, sub.ID)
	if err != nil {
		return out, err
	}
	if len(items) == 0 {
		return out, nil
	}

	// One line per item, summed. A subscription that meters four document types
	// is one plan to the person reading this screen, and four rows would be the
	// data model leaking into their bill.
	fixed := billing.Cents(0)
	metered := false
	names := make([]string, 0, len(items))

	for _, it := range items {
		price, err := h.cat.GetPrice(c.Context(), t.OrganizationID, t.Livemode, it.PriceID)
		if err != nil {
			return out, err
		}
		product, err := h.cat.GetProduct(c.Context(), t.OrganizationID, t.Livemode, price.ProductID)
		if err != nil {
			return out, err
		}
		out.Currency = price.Currency
		if !slices.Contains(names, product.Name) {
			names = append(names, product.Name)
		}
		if price.Type == billing.PriceMetered {
			metered = true
			continue
		}
		fixed += price.UnitAmount * billing.Cents(it.Quantity)
	}

	out.Description = strings.Join(names, " + ")
	// Metered if *any* item is: the amount below is then only the part that is
	// knowable in advance, and the screen has to say the total is not final.
	// Reporting a partial figure as the amount is how a customer learns the price
	// from us and the total from the invoice.
	out.Metered = metered
	if !metered {
		// A metered subscription has no amount until its period closes. Publishing
		// the unit price as "the amount" would be a number the next invoice
		// contradicts.
		out.Amount = fixed
	}
	return out, nil
}
