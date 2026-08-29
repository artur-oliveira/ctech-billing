package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// The invoice PDF, on both signed-in surfaces.
//
// It is **not** on the public checkout, and that absence is deliberate: a
// payment link gets forwarded, so its payload carries no name, no e-mail and no
// tax id (ADR 0009 § minimization). The document carries all three, because it
// is the customer's own invoice — which is exactly why it lives behind a
// session and not behind a link somebody can pass on.
//
// Both routes answer with a short-lived signed URL rather than the bytes. The
// API instances are t4g.nano behind one shared edge and a download path through
// them is a download path that competes with paying a bill; the link expires in
// minutes, so a URL left in a browser's history is not a standing grant.

type documentResponse struct {
	URL string `json:"url"`
	// ExpiresIn is seconds, published so a client can decide whether the link it
	// is holding is still worth opening rather than discovering it is not.
	ExpiresIn int `json:"expires_in"`
}

// invoicePDF serves the console's copy: any invoice of the signed-in owner's
// organization.
func (h *consoleHandlers) invoicePDF(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return h.serveDocument(c, inv)
}

// invoicePDF serves the portal's copy, filtered to the signed-in customer by
// the same helper every other portal read uses — a document is a rendering of
// an invoice, and the rule about whose invoice it is does not change because
// the format did.
func (h *portalHandlers) invoicePDF(c fiber.Ctx) error {
	inv, err := h.ownInvoice(c)
	if err != nil {
		return fail(c, err)
	}
	return h.serveDocument(c, inv)
}

func (h *handlers) serveDocument(c fiber.Ctx, inv *billing.Invoice) error {
	url, err := h.documents.DownloadURL(c.Context(), inv, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(documentResponse{URL: url, ExpiresIn: int(invoicePDFTTLSeconds)})
}

// setIssuer records who the invoice PDF says is charging (C17).
//
// Nothing here is validated beyond being present: billing is not the authority
// on a CNPJ or an address, and a service that rejected a legitimate one because
// its own check was wrong would be worse than one that prints what it was told.
// What it does refuse is a legal name longer than a line, because the document
// has a layout.
func (h *consoleHandlers) setIssuer(c fiber.Ctx) error {
	org := middleware.GetOrganization(c)
	var req issuerRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("corpo inválido").Send(c)
	}
	if len(req.LegalName) > maxIssuerField || len(req.TaxID) > maxIssuerField ||
		len(req.Address) > maxIssuerAddress || len(req.Email) > maxIssuerField {
		return problem.BadRequest("um dos campos do emissor é longo demais para caber no documento").Send(c)
	}
	if err := h.orgs.SetIssuer(
		c.Context(), org,
		req.LegalName, req.TaxID, req.Address, req.Email,
		actorOfUser(c), middleware.GetRequestID(c), h.now(),
	); err != nil {
		return fail(c, err)
	}
	return c.JSON(newIssuerResponse(org))
}

const (
	maxIssuerField   = 120
	maxIssuerAddress = 240
	// invoicePDFTTLSeconds mirrors invoicepdf.DownloadTTL. Published as seconds
	// because a client cannot import a Go duration.
	invoicePDFTTLSeconds = 300
)

type issuerRequest struct {
	LegalName string `json:"legal_name"`
	TaxID     string `json:"tax_id"`
	Address   string `json:"address"`
	Email     string `json:"email"`
}

type issuerResponse struct {
	LegalName string `json:"legal_name,omitempty"`
	TaxID     string `json:"tax_id,omitempty"`
	Address   string `json:"address,omitempty"`
	Email     string `json:"email,omitempty"`
}

func newIssuerResponse(org *billing.Organization) issuerResponse {
	return issuerResponse{
		LegalName: org.LegalName,
		TaxID:     org.IssuerTaxID,
		Address:   org.IssuerAddress,
		Email:     org.IssuerEmail,
	}
}
