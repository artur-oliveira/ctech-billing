package v1

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
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

type invoiceDetailResponse struct {
	Invoice invoiceResponse `json:"invoice"`
	// PaymentLink is the public URL that opens this invoice's checkout with no
	// sign-in. It is published on the detail screen because that is where an
	// operator is standing when a customer asks "can you send it again?" — and
	// without it the link exists but has no way of reaching anybody.
	//
	// Absent for an invoice nobody can pay (a draft, a paid one, a void one), so
	// the screen cannot offer to send a link to a bill that is settled.
	PaymentLink string          `json:"payment_link,omitempty"`
	Timeline    []auditResponse `json:"timeline"`
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

type productResponse struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Active   bool             `json:"active"`
	Metadata billing.Metadata `json:"metadata,omitempty"`
	Prices   []priceResponse  `json:"prices,omitempty"`
	Livemode bool             `json:"livemode"`
}

func newProductResponse(p *billing.Product, prices []billing.Price) productResponse {
	out := productResponse{
		ID:       p.ID,
		Name:     p.Name,
		Active:   p.Active,
		Metadata: p.Metadata,
		Livemode: p.Livemode,
	}
	for i := range prices {
		out.Prices = append(out.Prices, newPriceResponse(&prices[i]))
	}
	return out
}
