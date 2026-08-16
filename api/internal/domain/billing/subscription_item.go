package billing

import (
	"errors"
	"fmt"
)

// ErrInvalidSubscriptionItem reports an item that cannot be billed.
var ErrInvalidSubscriptionItem = errors.New("invalid subscription item")

// SubscriptionItem binds a subscription to a Price and a quantity.
//
// A subscription holds **one or more**. That is what a usage-based plan needs:
// "R$ 0,05 per NF-e, R$ 0,01 per NFC-e, R$ 0,50 per CT-e" is one agreement with
// one monthly bill, and modelling it as three subscriptions would produce three
// invoices and three PIX charges for what the customer signed once.
//
// The items of one subscription must agree on their cycle — same Recurrence,
// same BillingTiming — because the subscription has exactly one of each and they
// decide which period is being billed. Mixing an advance item with an arrears
// one would put two different periods on the same document. Subscriber.Subscribe
// enforces that at the boundary.
//
// It is also the anchor of the usage sub-partition: consumption is reported
// against an item, which is what lets five metered items on one subscription
// each accumulate their own meter. That needs an identifier which survives a
// price change, and the subscription id cannot provide one once an upgrade
// repoints the item at a new Price.
type SubscriptionItem struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`
	SubscriptionID string `dynamodbav:"subscription_id" json:"subscription_id"`

	// PriceID is pinned at subscribe time and only changes on an explicit
	// upgrade/downgrade. Because a Price is immutable, pinning it here is what
	// makes grandfathering automatic rather than a flag.
	PriceID string `dynamodbav:"price_id" json:"price_id"`

	// Quantity multiplies a fixed price (five seats of the same plan). It is
	// ignored for a metered price, where the quantity is the reported usage.
	Quantity int64 `dynamodbav:"quantity" json:"quantity"`
}

// Validate reports whether the item can produce an invoice line.
func (i *SubscriptionItem) Validate() error {
	if i.PriceID == "" {
		return fmt.Errorf("%w: missing price", ErrInvalidSubscriptionItem)
	}
	if i.Quantity < 1 {
		return fmt.Errorf("%w: quantity must be at least 1, got %d", ErrInvalidSubscriptionItem, i.Quantity)
	}
	return nil
}
