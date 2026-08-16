// Package v1 wires billing's HTTP routes onto a Fiber app.
//
// Handlers are thin on purpose: bind, resolve the tenant from the credential,
// call a service, map the error. Anything that decides something belongs in
// internal/services or internal/domain, where it is testable without HTTP.
package v1

import (
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
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
	Livemode   bool                  `json:"livemode"`
}

func newInvoiceResponse(inv *billing.Invoice, lines []billing.InvoiceItem, today brcal.Date) invoiceResponse {
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
}

type listResponse[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	Cursor  string `json:"cursor,omitempty"`
}
