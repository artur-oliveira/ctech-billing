package v1

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// Console responses, kept apart from the M2M ones in dto.go for one reason: the
// two surfaces have different audiences and must be allowed to diverge without
// either dragging the other. Where they answer the same question they reuse the
// same constructor — a customer is masked identically in both, and that is not
// something a screen gets to decide.

// pageOf wraps a page and its continuation. HasMore is derived from the cursor
// rather than tracked separately, so a page cannot claim there is more and then
// hand back nothing to continue from.
func pageOf[T any](items []T, lastKey map[string]types.AttributeValue) listResponse[T] {
	cursor := repositories.EncodeCursor(lastKey)
	return listResponse[T]{Data: items, HasMore: cursor != "", Cursor: cursor}
}

type sessionResponse struct {
	OrganizationID string               `json:"organization_id"`
	DisplayName    string               `json:"display_name"`
	Livemode       bool                 `json:"livemode"`
	PayoutStatus   billing.PayoutStatus `json:"payout_status"`
	CanCharge      bool                 `json:"can_charge"`
}

// auditResponse is one entry of a detail screen's timeline. It publishes who,
// what, why and when — and the request id, because a support conversation that
// cannot name the request is a support conversation that goes in circles.
type auditResponse struct {
	ID        string        `json:"id"`
	Action    string        `json:"action"`
	Cause     billing.Cause `json:"cause,omitempty"`
	Actor     string        `json:"actor"`
	Before    string        `json:"before,omitempty"`
	After     string        `json:"after,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// consoleInvoiceListItem is one row of C2.
//
// The invoice plus the customer's name, because a list of invoices without the
// customer on it is a list an operator cannot use: the question at this screen
// is "who owes what", and an id answers half of it. The name is not on the
// shared invoiceResponse — an integration reading the M2M API already holds its
// own customer records and does not need billing's copy.
type consoleInvoiceListItem struct {
	invoiceResponse
	CustomerName string `json:"customer_name,omitempty"`
}

// invoiceDetailResponse is C3.
//
// The payment link an operator sends when a customer asks "can you send it
// again?" is `invoice.checkout_url`, not a field of its own. It used to be one,
// and the two disagreed the moment the rule grew a second condition: this
// surface published a link for any OPEN invoice, which included a zero-total one
// and a test-mode one — both of which open a page that cannot be paid. One
// field, one rule (Invoice.Payable).
type invoiceDetailResponse struct {
	Invoice invoiceResponse `json:"invoice"`
	// CustomerName, for the same reason C2 carries it: an operator reading this
	// screen is talking to a person, not to an id. Absent when the customer row
	// cannot be read — the invoice is still a real document, and refusing to
	// render it over a missing name would take the screen down for a defect it
	// can survive.
	CustomerName string `json:"customer_name,omitempty"`
	// CreditNotes are the corrections issued against this invoice, oldest first,
	// with the total they add up to. The total is published rather than left to
	// the client: "is this fully credited" is a rule (billing.FullyCredited), and
	// a screen that re-derives it from a list is a screen that will disagree with
	// the server the first time the rule grows a condition.
	CreditNotes   []creditNoteResponse `json:"credit_notes,omitempty"`
	CreditedCents billing.Cents        `json:"credited"`
	// FullyCredited is what a screen renders as "estornada". It is not a status
	// — assessment § 6.1 — because an invoice that was paid and then fully
	// credited is still a paid invoice, and rewriting its status would destroy
	// the record of the money that actually arrived.
	FullyCredited bool            `json:"fully_credited"`
	Timeline      []auditResponse `json:"timeline"`
}

// creditNoteRequest is the console's "emitir nota de crédito" (C3).
type creditNoteRequest struct {
	// Amount is positive: the amount being credited, never a negative charge.
	Amount billing.Cents `json:"amount"`
	Reason string        `json:"reason"`
	// RefundedExternally says wallet or the PSP actually returned the money.
	// Billing records that it happened; it never does it.
	RefundedExternally bool             `json:"refunded_externally"`
	ExternalRefundRef  string           `json:"external_refund_ref"`
	Metadata           billing.Metadata `json:"metadata"`
}

type creditNoteResponse struct {
	ID                 string        `json:"id"`
	InvoiceID          string        `json:"invoice_id"`
	Amount             billing.Cents `json:"amount"`
	Currency           string        `json:"currency"`
	Reason             string        `json:"reason"`
	RefundedExternally bool          `json:"refunded_externally"`
	ExternalRefundRef  string        `json:"external_refund_ref,omitempty"`
	CreatedBy          string        `json:"created_by"`
	CreatedAt          time.Time     `json:"created_at"`
}

func newCreditNoteResponse(cn *billing.CreditNote) creditNoteResponse {
	return creditNoteResponse{
		ID:                 cn.ID,
		InvoiceID:          cn.InvoiceID,
		Amount:             cn.Amount,
		Currency:           cn.Currency,
		Reason:             cn.Reason,
		RefundedExternally: cn.RefundedExternally,
		ExternalRefundRef:  cn.ExternalRefundRef,
		CreatedBy:          cn.CreatedBy,
		CreatedAt:          cn.CreatedAt,
	}
}

func newInvoiceDetailResponse(
	inv *billing.Invoice,
	lines []billing.InvoiceItem,
	notes []billing.CreditNote,
	trail []auditResponse,
	customerName string,
	today brcal.Date,
	links *services.PayLink,
) invoiceDetailResponse {
	out := invoiceDetailResponse{
		Invoice:      newInvoiceResponse(inv, lines, today, links),
		CustomerName: customerName,
		Timeline:     trail,
	}
	for i := range notes {
		out.CreditNotes = append(out.CreditNotes, newCreditNoteResponse(&notes[i]))
		out.CreditedCents += notes[i].Amount
	}
	out.FullyCredited = billing.FullyCredited(inv, out.CreditedCents)
	return out
}

type subscriptionItemResponse struct {
	ID       string        `json:"id"`
	PriceID  string        `json:"price_id"`
	Quantity int64         `json:"quantity"`
	Price    priceResponse `json:"price"`
}

type subscriptionDetailResponse struct {
	Subscription subscriptionResponse       `json:"subscription"`
	Items        []subscriptionItemResponse `json:"items"`
	Timeline     []auditResponse            `json:"timeline"`
}

type customerDetailResponse struct {
	Customer      customerResponse       `json:"customer"`
	Subscriptions []subscriptionResponse `json:"subscriptions"`
	Timeline      []auditResponse        `json:"timeline"`
}

type priceResponse struct {
	ID         string                `json:"id"`
	ProductID  string                `json:"product_id"`
	Type       billing.PriceType     `json:"type"`
	Currency   string                `json:"currency"`
	UnitAmount billing.Cents         `json:"unit_amount"`
	Recurrence billing.Recurrence    `json:"recurrence"`
	Timing     billing.BillingTiming `json:"billing_timing"`
	Archived   bool                  `json:"archived"`
	Metadata   billing.Metadata      `json:"metadata,omitempty"`
}

func newPriceResponse(p *billing.Price) priceResponse {
	return priceResponse{
		ID:         p.ID,
		ProductID:  p.ProductID,
		Type:       p.Type,
		Currency:   p.Currency,
		UnitAmount: p.UnitAmount,
		Recurrence: p.Recurrence,
		Timing:     p.Timing,
		Archived:   p.Archived,
		Metadata:   p.Metadata,
	}
}

// createProductRequest is C8's "novo produto".
//
// OwnerKey is accepted here and nowhere else on this surface: it routes the
// product's events to one endpoint (ADR 0016) and it is not metadata, because a
// routing key read out of a caller-writable map is a caller who can redirect
// somebody else's events.
type createProductRequest struct {
	Name     string           `json:"name"`
	OwnerKey string           `json:"owner_key"`
	Metadata billing.Metadata `json:"metadata"`
}

// createPriceRequest is C9's "novo preço" — never "editar preço".
//
// A price is immutable, so this is the only way an amount ever changes, and the
// screen is expected to say so. Archiving the old one is a separate, deliberate
// second action rather than something this request implies: replacing a price
// and withdrawing it from sale are two decisions, and an operator correcting a
// typo in a new price does not always mean to stop selling the old one.
type createPriceRequest struct {
	ProductID  string                `json:"product_id"`
	Type       billing.PriceType     `json:"type"`
	UnitAmount billing.Cents         `json:"unit_amount"`
	Recurrence billing.Recurrence    `json:"recurrence"`
	Timing     billing.BillingTiming `json:"billing_timing"`
	Metadata   billing.Metadata      `json:"metadata"`
}

type productResponse struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Active   bool             `json:"active"`
	OwnerKey string           `json:"owner_key,omitempty"`
	Metadata billing.Metadata `json:"metadata,omitempty"`
	Prices   []priceResponse  `json:"prices,omitempty"`
	// Dunning is the schedule invoices billing this product follow, always
	// populated and flagged as inherited or overridden. An empty override is
	// not "no policy" — it is the organization's, and the screen has to be able
	// to say which.
	Dunning  dunningPolicyResponse `json:"dunning"`
	Livemode bool                  `json:"livemode"`
}

func newProductResponse(p *billing.Product, prices []billing.Price) productResponse {
	out := productResponse{
		ID:       p.ID,
		Name:     p.Name,
		Active:   p.Active,
		OwnerKey: p.OwnerKey,
		Metadata: p.Metadata,
		Dunning:  newDunningPolicyResponse(p.DunningPolicy),
		Livemode: p.Livemode,
	}
	for i := range prices {
		out.Prices = append(out.Prices, newPriceResponse(&prices[i]))
	}
	return out
}
