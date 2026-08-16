// Package v1 wires billing's HTTP routes onto a Fiber app.
//
// Handlers are thin on purpose: bind, resolve the tenant from the credential,
// call a service, map the error. Anything that decides something belongs in
// internal/services or internal/domain, where it is testable without HTTP.
package v1

import (
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// Request bodies never carry organization_id. It comes from the credential (see
// middleware.ResolveTenant) — accepting it from a client would make cross-tenant
// access a matter of sending the right string. Nor does any body carry livemode:
// on this surface the credential decides it too. The console is the one place a
// request states its mode, and it is a different surface for that reason
// (ADR 0011).

type createCustomerRequest struct {
	ExternalRef string `json:"external_ref"`
	// UserID is the customer's ctech-account subject, when they are a person with
	// a CTech account. It is what lets them open the portal and see their own
	// invoices (ADR 0012), and it is supplied here rather than inferred from the
	// email because an address changes and an address is mistyped.
	UserID   string           `json:"user_id"`
	Name     string           `json:"name"`
	Email    string           `json:"email"`
	TaxID    string           `json:"tax_id"`
	Metadata billing.Metadata `json:"metadata"`
}

type createSubscriptionRequest struct {
	CustomerID string `json:"customer_id"`
	// Items is one or more prices billed as one agreement. A usage-based plan
	// meters several things and sends one bill, so several is the normal case,
	// not an advanced one.
	Items []createSubscriptionItem `json:"items"`
	// Anchor is optional, "YYYY-MM-DD". Defaults to today in America/Sao_Paulo.
	Anchor   string           `json:"anchor"`
	NetDays  int              `json:"net_days"`
	Metadata billing.Metadata `json:"metadata"`
}

type createSubscriptionItem struct {
	PriceID  string `json:"price_id"`
	Quantity int64  `json:"quantity"`
}

// effectiveNow is the only value `effective` accepts today.
//
// The field exists rather than being implied because the other answer —
// "at the end of the period" — is a real product decision that will arrive, and
// a body that never said which one it meant cannot be told apart from one that
// meant the new default.
const effectiveNow = "now"

type changeSubscriptionRequest struct {
	// Items is the complete new price set, not a delta.
	Items []createSubscriptionItem `json:"items"`
	// Effective may be omitted, which means "now".
	Effective string `json:"effective"`
}

type cancelSubscriptionRequest struct {
	// AtPeriodEnd distinguishes the two cancellations, which are different
	// operations rather than a shade of one.
	AtPeriodEnd bool `json:"at_period_end"`
}

type reportUsageRequest struct {
	SubscriptionID string `json:"subscription_id"`
	// PriceID says which of the subscription's items this consumption is for. It
	// may be omitted only when the subscription has exactly one item — a
	// subscription metering NF-e, NFC-e and CT-e separately has no defensible
	// default, and guessing one would silently bill NFC-e volume at the CT-e
	// price.
	PriceID  string `json:"price_id"`
	Quantity int64  `json:"quantity"`
	// OccurredAt is RFC 3339. Defaults to now. Which period it falls in is
	// decided by the São Paulo civil date, never the UTC one.
	OccurredAt string `json:"occurred_at"`
	// IdempotencyKey identifies the consumption event itself, separately from the
	// HTTP Idempotency-Key: a caller may batch several events into one request,
	// and a retried request must not double-count any of them.
	IdempotencyKey string `json:"idempotency_key"`
}

// Responses are explicit structs rather than the domain entities, so a field
// added to an entity is never published by accident. Customer's tax_id is the
// case that matters: it is masked here and never returned in full.

type customerResponse struct {
	ID          string           `json:"id"`
	ExternalRef string           `json:"external_ref,omitempty"`
	Name        string           `json:"name"`
	Email       string           `json:"email,omitempty"`
	TaxIDMasked string           `json:"tax_id_masked,omitempty"`
	Anonymized  bool             `json:"anonymized"`
	Metadata    billing.Metadata `json:"metadata,omitempty"`
	Livemode    bool             `json:"livemode"`
}

func newCustomerResponse(c *billing.Customer) customerResponse {
	return customerResponse{
		ID:          c.ID,
		ExternalRef: c.ExternalRef,
		Name:        c.Name,
		Email:       c.Email,
		TaxIDMasked: billing.MaskedTaxID(c.TaxID),
		Anonymized:  c.Anonymized,
		Metadata:    c.Metadata,
		Livemode:    c.Livemode,
	}
}

type subscriptionResponse struct {
	ID                string                     `json:"id"`
	CustomerID        string                     `json:"customer_id"`
	Status            billing.SubscriptionStatus `json:"status"`
	Entitled          bool                       `json:"entitled"`
	Recurrence        billing.Recurrence         `json:"recurrence"`
	Timing            billing.BillingTiming      `json:"billing_timing"`
	Anchor            brcal.Date                 `json:"anchor"`
	CurrentPeriod     billing.Period             `json:"current_period"`
	CancelAtPeriodEnd bool                       `json:"cancel_at_period_end"`
	Metadata          billing.Metadata           `json:"metadata,omitempty"`
	Livemode          bool                       `json:"livemode"`
}

func newSubscriptionResponse(s *billing.Subscription) subscriptionResponse {
	return subscriptionResponse{
		ID:                s.ID,
		CustomerID:        s.CustomerID,
		Status:            s.Status,
		Entitled:          s.IsEntitled(),
		Recurrence:        s.Recurrence,
		Timing:            s.Timing,
		Anchor:            s.Anchor,
		CurrentPeriod:     s.CurrentPeriod(),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		Metadata:          s.Metadata,
		Livemode:          s.Livemode,
	}
}

type invoiceLineResponse struct {
	Description string         `json:"description"`
	Period      billing.Period `json:"period"`
	Quantity    int64          `json:"quantity"`
	UnitAmount  billing.Cents  `json:"unit_amount"`
	Amount      billing.Cents  `json:"amount"`
	Proration   bool           `json:"proration"`
}

type invoiceResponse struct {
	ID             string                `json:"id"`
	Number         int64                 `json:"number,omitempty"`
	CustomerID     string                `json:"customer_id"`
	SubscriptionID string                `json:"subscription_id,omitempty"`
	Status         billing.InvoiceStatus `json:"status"`
	// Overdue is derived on read, never stored — that is why "overdue" is not a
	// status (assessment § 6.1).
	Overdue    bool                  `json:"overdue"`
	Period     billing.Period        `json:"period"`
	DueDate    brcal.Date            `json:"due_date"`
	Currency   string                `json:"currency"`
	Subtotal   billing.Cents         `json:"subtotal"`
	Discount   billing.Cents         `json:"discount"`
	Total      billing.Cents         `json:"total"`
	AmountPaid billing.Cents         `json:"amount_paid"`
	AmountDue  billing.Cents         `json:"amount_due"`
	Attempts   int                   `json:"attempt_count"`
	Lines      []invoiceLineResponse `json:"lines,omitempty"`
	Metadata   billing.Metadata      `json:"metadata,omitempty"`
	// CheckoutURL is where to send the customer to pay this invoice. It is the
	// signed public link (services.PayLink) and not a portal URL, because the
	// integration's customer has just finished signing in *there* — asking them
	// to consent to a second OAuth client to pay their first bill is friction at
	// the exact moment of conversion.
	//
	// Absent unless the invoice is Payable, and that omission is the contract: a
	// link to a draft 404s by design, and one to a paid or zero-total invoice
	// opens a page whose only message is that there is nothing to do. An
	// integrator who branches on the field's presence is right; one who builds
	// the URL themselves is one invoice state away from a broken button.
	//
	// Also absent when the deployment has no CHECKOUT_LINK_SECRET or no
	// CHECKOUT_BASE_URL — the same configuration that disables the public
	// checkout routes entirely.
	CheckoutURL string `json:"checkout_url,omitempty"`
	Livemode    bool   `json:"livemode"`
}

// newInvoiceResponse renders an invoice for the M2M and console surfaces.
//
// links may be nil, and nil is a real deployment rather than a test convenience:
// without wallet configuration the checkout routes are never mounted, so a URL
// pointing at them would be a link to a 404.
func newInvoiceResponse(inv *billing.Invoice, lines []billing.InvoiceItem, today brcal.Date, links *services.PayLink) invoiceResponse {
	out := invoiceResponse{
		ID:             inv.ID,
		Number:         inv.Number,
		CustomerID:     inv.CustomerID,
		SubscriptionID: inv.SubscriptionID,
		Status:         inv.Status,
		Overdue:        inv.IsOverdue(today),
		Period:         inv.Period,
		DueDate:        inv.DueDate,
		Currency:       inv.Currency,
		Subtotal:       inv.Subtotal,
		Discount:       inv.Discount,
		Total:          inv.Total,
		AmountPaid:     inv.AmountPaid,
		AmountDue:      inv.AmountDue(),
		Attempts:       inv.AttemptCount,
		Metadata:       inv.Metadata,
		Livemode:       inv.Livemode,
	}
	if links != nil && inv.Payable() {
		// URL answers "" when links are configured off, which omitempty drops. So
		// the disabled deployment and the unpayable invoice produce the same
		// absent field, which is what a consumer should branch on either way.
		out.CheckoutURL = links.URL(inv.OrganizationID, inv.Livemode, inv.ID)
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, invoiceLineResponse{
			Description: l.Description,
			Period:      l.Period,
			Quantity:    l.Quantity,
			UnitAmount:  l.UnitAmount,
			Amount:      l.Amount,
			Proration:   l.Proration,
		})
	}
	return out
}

// entitlementResponse answers "can this customer use the product?" once, so
// every product does not reimplement it and then disagree the first time a
// status is added (assessment § 13).
type entitlementResponse struct {
	CustomerID    string                    `json:"customer_id"`
	Entitled      bool                      `json:"entitled"`
	Subscriptions []entitlementSubscription `json:"subscriptions"`
}

type entitlementSubscription struct {
	ID       string                     `json:"id"`
	Status   billing.SubscriptionStatus `json:"status"`
	Entitled bool                       `json:"entitled"`
	PriceID  string                     `json:"price_id,omitempty"`
	Period   billing.Period             `json:"current_period"`

	// Plan is the plan key from the first item's price metadata — "free", "pro",
	// "unlimited", "ondemand". Published so a consumer can name the plan without
	// keeping its own map of price ids, which is the copy that goes stale on the
	// day a price is superseded.
	Plan string `json:"plan,omitempty"`
	// Items is what this subscription bills for, prices and all. The quotas a
	// product enforces are in each price's metadata.
	Items []entitlementItem `json:"items,omitempty"`
	// CancelAtPeriodEnd is the notice that entitlement ends at the period
	// boundary. `entitled` is still true today, and a UI that shows only that
	// tells the customer nothing is happening right up until it does.
	CancelAtPeriodEnd bool `json:"cancel_at_period_end"`
	// OpenInvoice is the bill waiting to be paid, when there is one. Its presence
	// is what turns "your subscription is past due" into a button.
	OpenInvoice *entitlementInvoice `json:"open_invoice,omitempty"`
}

// entitlementInvoiceScan is how far back to look for an open invoice.
//
// Small on purpose: the newest few is where an outstanding bill is, and a
// subscription with more than a handful of open invoices behind it is a dunning
// problem, not a rendering one.
const entitlementInvoiceScan = 10

// metadataKeyPlan is the price-metadata key naming the plan. It is a constant
// because two places read it — this response and the DF-e side that branches on
// it — and a string literal in each is how they come to disagree by a typo.
const metadataKeyPlan = "plan"

type entitlementItem struct {
	PriceID    string            `json:"price_id"`
	ProductID  string            `json:"product_id"`
	Type       billing.PriceType `json:"type"`
	UnitAmount billing.Cents     `json:"unit_amount"`
	Quantity   int64             `json:"quantity"`
	Metadata   billing.Metadata  `json:"metadata,omitempty"`
}

type entitlementInvoice struct {
	ID         string        `json:"id"`
	TotalCents billing.Cents `json:"total_cents"`
	DueDate    brcal.Date    `json:"due_date"`
	// CheckoutURL follows the same rule as everywhere else: present only when the
	// invoice is actually payable and the deployment signs links.
	CheckoutURL string `json:"checkout_url,omitempty"`
}

type listResponse[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	Cursor  string `json:"cursor,omitempty"`
}
