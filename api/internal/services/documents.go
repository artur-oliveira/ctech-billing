package services

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/invoicepdf"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Documents produces and serves an invoice's PDF.
//
// **Rendered on first request, stored, never re-rendered.** The alternative —
// generating at finalization — buys nothing and costs two things: a window in
// which an invoice exists and its document does not, and a failure mode where
// the sweep stops issuing bills because a PDF library returned an error. Lazy
// generation is safe here only because the document carries frozen facts alone
// (see invoicepdf), so it is the same document whenever it is produced.
//
// One consequence is deliberate and worth stating: an invoice nobody ever
// downloads never becomes a file. The record is the invoice; the PDF is a
// rendering of it, and storage costs nothing for documents nobody wanted.
type Documents struct {
	invoices  *repositories.InvoiceRepository
	customers *repositories.CustomerRepository
	orgs      *repositories.OrganizationRepository
	store     *invoicepdf.Store
}

func NewDocuments(
	invoices *repositories.InvoiceRepository,
	customers *repositories.CustomerRepository,
	orgs *repositories.OrganizationRepository,
	store *invoicepdf.Store,
) *Documents {
	return &Documents{invoices: invoices, customers: customers, orgs: orgs, store: store}
}

// Enabled reports a deployment that can serve documents at all.
func (d *Documents) Enabled() bool { return d != nil && d.store.Enabled() }

// ErrNotIssued reports a document asked for on an invoice that is not one yet.
var ErrNotIssued = fmt.Errorf("%w: a draft invoice has no document", billing.ErrInvoiceItems)

// DownloadURL returns a short-lived link to the invoice's document, rendering
// and storing it if this is the first time anybody asked.
//
// The caller has already decided the reader may see this invoice — the portal
// filters to the signed-in customer, the console to the tenant — so there is no
// authorization here. What there is instead is one rule this function does
// enforce: a DRAFT has no number and is not a document, and rendering one would
// produce a file that looks official and refers to nothing.
func (d *Documents) DownloadURL(ctx context.Context, inv *billing.Invoice, now time.Time) (string, error) {
	if !d.Enabled() {
		return "", invoicepdf.ErrNotConfigured
	}
	if inv.Status == billing.InvoiceDraft || inv.Number == 0 {
		return "", ErrNotIssued
	}

	key := invoicepdf.Key(inv.OrganizationID, inv.Livemode, inv.ID)
	// The stored key is trusted over the derived one when present: it is what an
	// earlier render actually wrote, and a change to the naming scheme must not
	// orphan documents somebody already has links to.
	if inv.PDFKey != "" {
		key = inv.PDFKey
	}

	stored, err := d.store.Exists(ctx, key)
	if err != nil {
		return "", err
	}
	if !stored {
		if err := d.render(ctx, inv, key, now); err != nil {
			return "", err
		}
	}

	return d.store.DownloadURL(ctx, key, fmt.Sprintf("fatura-%d.pdf", inv.Number))
}

func (d *Documents) render(ctx context.Context, inv *billing.Invoice, key string, now time.Time) error {
	lines, err := d.invoices.ListItems(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		return fmt.Errorf("reading the invoice lines: %w", err)
	}

	in := invoicepdf.Input{Invoice: inv, Lines: lines}

	// The issuer and the customer are best-effort in different ways. An
	// organization that cannot be read is a broken deployment and worth
	// failing on — every invoice belongs to one. A customer that cannot be read
	// is a document with a blank recipient, which is worse than no document, so
	// it fails too. Neither is degraded silently: a PDF is filed, and one that
	// quietly omits who it is for is filed wrong.
	org, err := d.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return fmt.Errorf("reading the issuer: %w", err)
	}
	in.Issuer = invoicepdf.Issuer{
		Name:      org.DisplayName,
		LegalName: org.LegalName,
		TaxID:     org.IssuerTaxID,
		Address:   org.IssuerAddress,
		Email:     org.IssuerEmail,
	}

	customer, err := d.customers.Get(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID)
	if err != nil {
		return fmt.Errorf("reading the customer: %w", err)
	}
	in.Customer = invoicepdf.Customer{
		Name: customer.Name,
		// In full, and this is the one place billing publishes an unmasked tax
		// id without an audit row. It is not a reveal: the document is the
		// customer's own, it goes to them, and an invoice that masks the payer
		// is not one an accountant can file. Which is also why the public
		// checkout has no download — that surface deliberately carries no name
		// and no tax id at all.
		TaxID: customer.TaxID,
		Email: customer.Email,
	}

	pdf, err := invoicepdf.Render(in)
	if err != nil {
		return err
	}
	if err := d.store.Put(ctx, key, pdf); err != nil {
		return fmt.Errorf("storing the document: %w", err)
	}
	// Recorded after the object exists. The other order leaves a key pointing at
	// nothing, which reads as a stored document and downloads as a 404.
	return d.invoices.RecordPDFKey(ctx, inv, key, now)
}
