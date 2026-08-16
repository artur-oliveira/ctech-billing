package billing

import (
	"errors"
	"fmt"
)

// PriceType is how the amount for a period is determined.
type PriceType string

const (
	// PriceFixed charges the same amount every period, known in advance.
	PriceFixed PriceType = "fixed"
	// PriceMetered charges for reported usage, so the amount is only known once
	// the period closes.
	PriceMetered PriceType = "metered"
)

// ErrInvalidPrice reports a price that cannot be billed.
var ErrInvalidPrice = errors.New("invalid price")

// Product is the thing being sold. It groups prices and it is what the customer
// reads on the invoice line.
type Product struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`
	Name           string `dynamodbav:"name"            json:"name"`
	Active         bool   `dynamodbav:"active"          json:"active"`

	// OwnerKey names the service this product belongs to — "dfe", "poker". It is
	// what routes the product's events to one webhook endpoint instead of all of
	// them (ADR 0016), and it exists because tenant zero is one organization
	// holding every CTech service's subscriptions: routing per tenant would send
	// dfe every invoice poker issued.
	//
	// Empty for an ordinary merchant, who owns their whole catalogue and whose
	// endpoints therefore want everything. That asymmetry is deliberate — the
	// common case needs no configuration, and only the case that actually has the
	// problem carries the field.
	//
	// It is **not** metadata. Metadata is opaque to billing by decision
	// (ADR 0008), and a routing key read out of a caller-writable map is a caller
	// who can redirect somebody else's events.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"owner_key,omitempty"`

	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// Price is **immutable**. Changing what something costs means creating a new
// Price; existing subscriptions keep pointing at the old one.
//
// This replaces the plan-version scheme in the original spec, and it is a better
// trade than it looks: grandfathering stops being a feature with a flag and
// becomes a consequence of the model. There is no code path that can change what
// an existing subscriber agreed to pay, because there is no code path that
// changes a Price at all. Archiving only hides it from the catalogue.
type Price struct {
	ID             string    `dynamodbav:"id"              json:"id"`
	OrganizationID string    `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool      `dynamodbav:"livemode"        json:"livemode"`
	ProductID      string    `dynamodbav:"product_id"      json:"product_id"`
	Type           PriceType `dynamodbav:"type"            json:"type"`
	Currency       string    `dynamodbav:"currency"        json:"currency"`

	// UnitAmount is the amount per period for a fixed price, or the amount per
	// reported unit for a metered one.
	UnitAmount Cents `dynamodbav:"unit_amount" json:"unit_amount"`

	Recurrence Recurrence    `dynamodbav:"recurrence"     json:"recurrence"`
	Timing     BillingTiming `dynamodbav:"billing_timing" json:"billing_timing"`

	// Archived hides the price from the catalogue. It does not affect
	// subscriptions already on it — that is the point of immutability.
	Archived bool     `dynamodbav:"archived" json:"archived"`
	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// Validate reports whether the price can be billed at all.
func (p *Price) Validate() error {
	switch p.Type {
	case PriceFixed, PriceMetered:
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidPrice, p.Type)
	}
	if p.Currency != CurrencyBRL {
		return fmt.Errorf("%w: currency %q is not supported", ErrInvalidPrice, p.Currency)
	}
	if p.UnitAmount < 0 {
		return fmt.Errorf("%w: unit amount %d is negative", ErrInvalidPrice, p.UnitAmount)
	}
	if err := p.Recurrence.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidPrice, err)
	}
	// A metered price billed in advance would have to guess the usage it is
	// charging for. The two are not a valid combination, and catching it here is
	// the difference between a rejected price and a wrong invoice.
	if p.Type == PriceMetered && p.Timing != BillArrears {
		return fmt.Errorf("%w: a metered price must be billed in arrears, not %q", ErrInvalidPrice, p.Timing)
	}
	if p.Timing != BillAdvance && p.Timing != BillArrears {
		return fmt.Errorf("%w: unknown billing timing %q", ErrInvalidPrice, p.Timing)
	}
	if err := p.Metadata.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidPrice, err)
	}
	return nil
}

// ExceedsChargeCeiling reports whether one period of this price would be
// rejected by the wallet's per-charge ceiling (ADR 0004).
//
// It only answers for a fixed price: a metered total is not knowable in advance,
// so a metered subscription can still hit the ceiling at period close. That is a
// real limit of the design, not an oversight — the honest place to catch it is
// the charge itself, in the wallet.
func (p *Price) ExceedsChargeCeiling() bool {
	return p.Type == PriceFixed && p.UnitAmount > MaxChargeCents
}
