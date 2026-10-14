package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var (
	// ErrOneOffInvalid: a field of the request cannot make an invoice.
	ErrOneOffInvalid = errors.New("invalid one-off invoice")
	// ErrOneOffConflict: the reference already names an invoice for something else.
	ErrOneOffConflict = errors.New("the reference belongs to another invoice")
)

const (
	maxReferenceLen  = 64
	oneOffKeyPrefix  = "oneoff:"
	oneOffCurrencyBR = "BRL"
)

// OneOffInput is an invoice for one amount, asked for by a service: a
// deposit, a pack of game credits, a proof of collection.
type OneOffInput struct {
	OrganizationID string
	Livemode       bool
	CustomerID     string
	Description    string
	// Reference is the caller's own name for the thing being sold, unique per
	// tenant. It is what makes the call safe to repeat: the same reference with
	// the same customer and amount returns the same invoice, never a second one.
	Reference   string
	AmountCents int64
	OwnerKey    string
	Metadata    billing.Metadata
}

// OneOffInvoices issues invoices that no subscription produced. It is the
// sweep's own issuing path (Invoicer.Issue), so a one-off invoice is numbered,
// gets its due date and its dunning like any other, and is collected on the
// tenant's rail.
type OneOffInvoices struct {
	invoices  *repositories.InvoiceRepository
	customers *repositories.CustomerRepository
	invoicer  *Invoicer
}

func NewOneOffInvoices(invoices *repositories.InvoiceRepository, customers *repositories.CustomerRepository, invoicer *Invoicer) *OneOffInvoices {
	return &OneOffInvoices{invoices: invoices, customers: customers, invoicer: invoicer}
}

func validReference(r string) bool {
	if r == "" || len(r) > maxReferenceLen {
		return false
	}
	for _, c := range r {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("-_.:", c):
		default:
			return false
		}
	}
	return true
}

func (in *OneOffInput) validate() error {
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case in.CustomerID == "":
		return fmt.Errorf("%w: customer_id", ErrOneOffInvalid)
	case in.AmountCents <= 0 || in.AmountCents > limits.MaxAmountCents:
		return fmt.Errorf("%w: amount_cents", ErrOneOffInvalid)
	case in.Description == "" || len([]rune(in.Description)) > limits.Description:
		return fmt.Errorf("%w: description", ErrOneOffInvalid)
	case !validReference(in.Reference):
		return fmt.Errorf("%w: reference", ErrOneOffInvalid)
	}
	return nil
}

// Create issues the invoice, or returns the one this reference already named.
// created says which. The invoice is OPEN when it returns; collecting it is the
// tenant's rail's business (the checkout link shows the Pix).
func (s *OneOffInvoices) Create(ctx context.Context, in OneOffInput, actor, requestID string, now time.Time) (inv *billing.Invoice, lines []billing.InvoiceItem, created bool, err error) {
	if err = in.validate(); err != nil {
		return nil, nil, false, err
	}
	if _, err = s.customers.Get(ctx, in.OrganizationID, in.Livemode, in.CustomerID); err != nil {
		return nil, nil, false, err
	}
	key := oneOffKeyPrefix + in.Reference
	inv, err = s.invoices.GetByGenerationKey(ctx, in.OrganizationID, in.Livemode, key)
	if errors.Is(err, repositories.ErrNotFound) {
		inv, lines = s.build(in, now)
		if err = s.invoices.Create(ctx, inv, lines, key, now); err != nil {
			if !errors.Is(err, repositories.ErrAlreadyGenerated) {
				return nil, nil, false, err
			}
			// Lost a race with the same reference: read the winner.
			if inv, err = s.invoices.GetByGenerationKey(ctx, in.OrganizationID, in.Livemode, key); err != nil {
				return nil, nil, false, err
			}
		} else {
			created = true
		}
	} else if err != nil {
		return nil, nil, false, err
	}
	if inv.CustomerID != in.CustomerID || int64(inv.Total) != in.AmountCents {
		return nil, nil, false, fmt.Errorf("%w: %s", ErrOneOffConflict, in.Reference)
	}
	if inv.Status == billing.InvoiceDraft {
		if err = s.invoicer.Issue(ctx, inv, actor, requestID, now); err != nil {
			return nil, nil, false, err
		}
	}
	if lines, err = s.invoices.ListItems(ctx, inv.OrganizationID, inv.Livemode, inv.ID); err != nil {
		return nil, nil, false, err
	}
	return inv, lines, created, nil
}

func (s *OneOffInvoices) build(in OneOffInput, now time.Time) (*billing.Invoice, []billing.InvoiceItem) {
	today := brcal.FromTime(now)
	period := billing.Period{Start: today, End: today} // due the day it is issued
	amount := billing.Cents(in.AmountCents)
	inv := &billing.Invoice{
		ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: in.OrganizationID, Livemode: in.Livemode,
		CustomerID: in.CustomerID, Status: billing.InvoiceDraft, OwnerKey: in.OwnerKey,
		Period: period, Currency: oneOffCurrencyBR, Subtotal: amount, Total: amount, Metadata: in.Metadata,
	}
	line := billing.InvoiceItem{Description: in.Description, Period: period, Quantity: 1, UnitAmount: amount, Amount: amount}
	return inv, []billing.InvoiceItem{line}
}
