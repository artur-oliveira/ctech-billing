package billing

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// InvoiceItem is one line of an invoice, carrying **its own period**.
//
// A per-line period is what makes proration auditable: an upgrade mid-month
// produces two lines with two date ranges, and a customer can reconstruct the
// arithmetic from the invoice itself years later. A single net line cannot be
// explained to anyone.
type InvoiceItem struct {
	// Description is what the customer reads. It is rendered when the line is
	// built and then frozen with the rest of the invoice, so renaming a product
	// later does not rewrite issued documents.
	Description string `dynamodbav:"description" json:"description"`
	PriceID     string `dynamodbav:"price_id,omitempty" json:"price_id,omitempty"`

	Period Period `dynamodbav:"period" json:"period"`

	Quantity   int64 `dynamodbav:"quantity"    json:"quantity"`
	UnitAmount Cents `dynamodbav:"unit_amount" json:"unit_amount"`
	// Amount is the line total. For a prorated line it is not
	// Quantity × UnitAmount — it is the prorated figure, and Proration says so.
	Amount Cents `dynamodbav:"amount" json:"amount"`

	// Proration marks a line produced by a partial period. The UI must show it:
	// an unexplained fraction of a monthly price is the single most common
	// billing support ticket.
	Proration bool `dynamodbav:"proration" json:"proration"`
}

// ErrInvoiceItems reports a set of lines that cannot form an invoice.
var ErrInvoiceItems = errors.New("invalid invoice items")

// FixedLine builds the full-period line for a fixed price.
func FixedLine(p *Price, productName string, period Period, quantity int64) InvoiceItem {
	return InvoiceItem{
		Description: productName,
		PriceID:     p.ID,
		Period:      period,
		Quantity:    quantity,
		UnitAmount:  p.UnitAmount,
		Amount:      p.UnitAmount * Cents(quantity),
	}
}

// MeteredLine builds the line for a closed metered period.
func MeteredLine(p *Price, productName string, period Period, units int64) InvoiceItem {
	return InvoiceItem{
		Description: productName,
		PriceID:     p.ID,
		Period:      period,
		Quantity:    units,
		UnitAmount:  p.UnitAmount,
		Amount:      p.UnitAmount * Cents(units),
	}
}

// SwapLines turns a mid-period price change into the **two** lines an invoice
// must carry: a credit for the unused remainder of the old price and a charge
// for the remainder of the new one.
//
// Returning two lines rather than one net figure is a deliberate constraint of
// this function's signature — there is no way to call it and get a single
// collapsed amount, which is the point.
func SwapLines(oldPrice, newPrice *Price, oldName, newName string, period Period, at brcal.Date) (credit, charge InvoiceItem) {
	return SwapSideLines(
		SwapSide{Total: oldPrice.UnitAmount, Name: oldName, PriceID: oldPrice.ID},
		SwapSide{Total: newPrice.UnitAmount, Name: newName, PriceID: newPrice.ID},
		period, at,
	)
}

// SwapSide is one half of a mid-period change: what the customer was paying, or
// what they are moving to.
//
// It exists because a plan is not always one price. A subscription can bill
// several fixed items as one agreement, and the proration owed is a property of
// the **agreement**, not of each item — prorating item by item rounds several
// times and produces a total that does not match the plan's own arithmetic.
type SwapSide struct {
	// Total is the full-period cost of this side's fixed items. Metered items
	// contribute nothing: usage is billed for what was actually consumed, so
	// there is no unearned remainder to credit and nothing to charge in advance.
	Total Cents
	// Name is what the customer reads on the line.
	Name string
	// PriceID is set only when this side is a single price. Empty for an
	// aggregate, because a line pointing at one of several prices would be a
	// worse answer than pointing at none.
	PriceID string
}

// SwapSideLines is SwapLines for whole item sets rather than single prices.
//
// It returns two lines and there is no way to call it and get one collapsed
// amount, which is the point — see SwapLines.
func SwapSideLines(old, updated SwapSide, period Period, at brcal.Date) (credit, charge InvoiceItem) {
	s := ProrateSwap(old.Total, updated.Total, period, at)
	remainder := Period{Start: at, End: period.End}

	credit = InvoiceItem{
		Description: fmt.Sprintf("Crédito proporcional — %s (%d de %d dias)", old.Name, s.RemainingDays, s.PeriodDays),
		PriceID:     old.PriceID,
		Period:      remainder,
		Quantity:    1,
		UnitAmount:  old.Total,
		Amount:      -s.Credit,
		Proration:   true,
	}
	charge = InvoiceItem{
		Description: fmt.Sprintf("%s (%d de %d dias)", updated.Name, s.RemainingDays, s.PeriodDays),
		PriceID:     updated.PriceID,
		Period:      remainder,
		Quantity:    1,
		UnitAmount:  updated.Total,
		Amount:      s.Charge,
		Proration:   true,
	}
	return credit, charge
}

// Subtotal sums the lines. Credits are already negative amounts, so the sum is
// the subtotal and there is no second sign convention to remember.
func Subtotal(items []InvoiceItem) Cents {
	var total Cents
	for _, it := range items {
		total += it.Amount
	}
	return total
}

// ApplyToInvoice writes the lines' totals onto the invoice, rejecting a set that
// cannot be billed.
//
// A total below zero is refused on purpose. A negative invoice is not a refund —
// it is a CreditNote, which is a different document with a different meaning to
// an accountant. Letting a negative total through here is how a billing system
// starts issuing money instead of demanding it.
func ApplyToInvoice(inv *Invoice, items []InvoiceItem, discount Cents) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: an invoice needs at least one line", ErrInvoiceItems)
	}
	if discount < 0 {
		return fmt.Errorf("%w: discount %d is negative", ErrInvoiceItems, discount)
	}
	subtotal := Subtotal(items)
	total := subtotal - discount
	if total < 0 {
		return fmt.Errorf("%w: total would be %s; a negative balance is a credit note, not an invoice", ErrInvoiceItems, total)
	}
	inv.Subtotal = subtotal
	inv.Discount = discount
	inv.Total = total
	return nil
}

// GenerationKey is the idempotency key for creating the invoice of a period.
//
// It is {subscription_id}:{period_start}. This is what makes the daily scheduler
// safe to re-run: a second sweep on the same day computes the same key and
// writes nothing.
//
// It keys on the **subscription**, not on a subscription item, because one
// period of one subscription is one invoice regardless of how many items it
// carries. Keying on an item was correct only while a subscription could hold
// exactly one; with several it would let each item claim the period separately,
// which is a set of single-line invoices for what the customer agreed to receive
// as one bill.
//
// The plan version that used to be part of this key is gone because Price is
// immutable and is pinned on the item — which makes the key shorter, stable, and
// immune to a version being renumbered.
func GenerationKey(subscriptionID string, periodStart brcal.Date) string {
	return subscriptionID + ":" + periodStart.String()
}
