package v1

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// The checkout surface — screen X1, and the only routes in the service that
// answer to nobody signed in.
//
// A payment link is a URL sent in an email that says "your invoice is ready".
// The person who opens it is paying a bill, and a bill is not a good moment to
// ask somebody to create an account. So authentication here is the signed token
// in the path and nothing else, and three things follow from that:
//
//   - The token is checked before any read. A forged one never reaches DynamoDB.
//   - The payload is the **minimum** to pay: what it is for, how much, when it is
//     due, and who is charging. No name, no e-mail, no tax id, no other invoice,
//     no internal status. A forwarded link must not become a disclosure, and the
//     way to guarantee that is for the data never to be in the response (ADR 0009
//     § minimization).
//   - It reads the invoice, never the customer's list. The token addresses one
//     invoice, so one invoice is all it can open.

type checkoutHandlers struct {
	*handlers
	orgs      *repositories.OrganizationRepository
	collector *services.Collector
}

// checkoutResponse is the public page's whole payload.
type checkoutResponse struct {
	// Merchant is who is being paid. Without it the page asks a person to send
	// money to nobody in particular, which is exactly what a phishing page looks
	// like.
	Merchant string           `json:"merchant"`
	Invoice  checkoutInvoice  `json:"invoice"`
	Payment  *checkoutPayment `json:"payment,omitempty"`
}

type checkoutInvoice struct {
	Number      int64         `json:"number,omitempty"`
	Description string        `json:"description"`
	State       string        `json:"state"`
	Tone        string        `json:"tone"`
	DueDate     brcal.Date    `json:"due_date"`
	AmountDue   billing.Cents `json:"amount_due"`
	Currency    string        `json:"currency"`
	Lines       []portalLine  `json:"lines,omitempty"`
	// Payable is the server's answer to "is there anything to do here". A page
	// that decides it from the state phrase will eventually offer to pay a voided
	// invoice.
	Payable bool `json:"payable"`
}

// checkoutPayment is the live charge. It appears only after the page asks to
// pay, so merely opening a link never opens a charge — a forwarded or crawled
// URL must not create a PIX charge.
type checkoutPayment struct {
	Method    string    `json:"method"`
	PixCode   string    `json:"pix_code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// view renders the invoice behind a payment link.
func (h *checkoutHandlers) view(c fiber.Ctx) error {
	org, livemode, invoiceID, err := h.links.Parse(c.Params("token"))
	if err != nil {
		return problem.NotFound("link inválido ou expirado").Send(c)
	}
	inv, lines, merchant, err := h.load(c, org, livemode, invoiceID)
	if err != nil {
		return failLink(c, err)
	}
	return c.JSON(checkoutResponse{Merchant: merchant, Invoice: newCheckoutInvoice(inv, lines, h.today())})
}

// pay opens the charge, or returns the one that is still live.
//
// It is a POST because it creates something, and it carries no body at all:
// everything it needs is in the token, and a body on this route would be a field
// a stranger gets to fill in on a payment.
func (h *checkoutHandlers) pay(c fiber.Ctx) error {
	org, livemode, invoiceID, err := h.links.Parse(c.Params("token"))
	if err != nil {
		return problem.NotFound("link inválido ou expirado").Send(c)
	}
	inv, lines, merchant, err := h.load(c, org, livemode, invoiceID)
	if err != nil {
		return failLink(c, err)
	}

	session, _, payErr := h.collector.Pay(
		c.Context(), org, livemode, invoiceID, actorCheckoutLink, middleware.GetRequestID(c), h.now())
	if payErr != nil {
		return failCheckout(c, payErr)
	}
	return c.JSON(checkoutResponse{
		Merchant: merchant,
		Invoice:  newCheckoutInvoice(inv, lines, h.today()),
		Payment: &checkoutPayment{
			Method:    string(billing.MethodPIX),
			PixCode:   session.PixCode,
			ExpiresAt: session.ExpiresAt,
		},
	})
}

// actorCheckoutLink names the payer in the audit trail. It is not a user id
// because there is no session — "somebody holding this invoice's link" is the
// honest answer, and an audit entry that claims more than that is worse than one
// that admits it.
const actorCheckoutLink = "checkout:link"

// errNoSuchLink is what both "no such invoice" and "that invoice is a draft"
// become. One error for both, because the answer on the wire must be the same:
// a link that says "this invoice exists but you may not see it" is a link that
// confirms invoice ids by trial.
var errNoSuchLink = errors.New("no invoice behind this link")

// load reads what both routes need, and refuses a draft.
//
// A draft is not a bill. Its amount can still change, so putting one behind a
// public link would present a provisional number as something owed.
//
// It returns a real error rather than a written response: problem.Send returns
// nil on success, so a helper that returns *that* tells its caller "no error"
// while having already answered — and the caller then works with a nil invoice.
func (h *checkoutHandlers) load(c fiber.Ctx, org string, livemode bool, invoiceID string) (
	*billing.Invoice, []billing.InvoiceItem, string, error,
) {
	inv, err := h.invoices.Get(c.Context(), org, livemode, invoiceID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, nil, "", errNoSuchLink
		}
		return nil, nil, "", err
	}
	if inv.Status == billing.InvoiceDraft {
		return nil, nil, "", errNoSuchLink
	}
	lines, err := h.invoices.ListItems(c.Context(), org, livemode, inv.ID)
	if err != nil {
		return nil, nil, "", err
	}
	organization, err := h.orgs.Get(c.Context(), org, livemode)
	if err != nil {
		return nil, nil, "", err
	}
	return inv, lines, organization.DisplayName, nil
}

func newCheckoutInvoice(inv *billing.Invoice, lines []billing.InvoiceItem, today brcal.Date) checkoutInvoice {
	state, tone := invoiceState(inv, today)
	out := checkoutInvoice{
		Number:      inv.Number,
		Description: describeLines(lines),
		State:       state,
		Tone:        tone,
		DueDate:     inv.DueDate,
		AmountDue:   inv.AmountDue(),
		Currency:    inv.Currency,
		Payable:     inv.Payable(),
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, portalLine{
			Description: l.Description,
			Amount:      l.Amount,
			Proration:   l.Proration,
		})
	}
	return out
}

// webhook is wallet's notify-back.
//
// The signature says the call is from wallet. It does **not** say the charge was
// paid — Collector.Confirm re-reads the charge from wallet before an invoice
// moves, which is wallet's own posture toward its provider and must not get
// weaker one layer up.
//
// It answers 200 for anything it has correctly decided not to act on, including
// a charge it has never heard of. Wallet retries non-2xx through its sweep, and
// a permanent disagreement retried forever is noise that buries the real one.
func (h *checkoutHandlers) webhook(c fiber.Ctx) error {
	body := c.Body()
	if !h.collector.VerifyWebhook(body, c.Get(wallet.HeaderSignature)) {
		return problem.Unauthorized("assinatura inválida").Send(c)
	}
	var note wallet.Notification
	if err := c.Bind().Body(&note); err != nil || note.ChargeID == "" {
		return problem.BadRequest("corpo inválido").Send(c)
	}

	// Live only, and that is now true by construction rather than by assumption:
	// Collector.Pay refuses a test-mode invoice outright
	// (services.ErrTestModeNotPayable), so no test-mode attempt can exist to
	// resolve. Passing the mode through from the body instead would be the hole
	// this closes — a test notification settling a live invoice.
	if err := h.collector.Confirm(c.Context(), true, note.ChargeID, billing.CauseWalletWebhook, middleware.GetRequestID(c), h.now()); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			// Not ours. Wallet notifies its client, and billing is one of several.
			return c.SendStatus(fiber.StatusOK)
		}
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}

// failLink answers the two link failures identically, so probing a token learns
// nothing about which invoices exist.
func failLink(c fiber.Ctx, err error) error {
	if errors.Is(err, errNoSuchLink) {
		return problem.NotFound("link inválido ou expirado").Send(c)
	}
	return fail(c, err)
}

// failCheckout maps the collection errors to what a payer can act on.
//
// The generic mapper cannot do this: "this organization cannot open charges" is
// a true sentence that means nothing to a customer, and the reason it is blocked
// is CTech's business, not theirs.
func failCheckout(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrInvoiceNotPayable):
		return problem.Conflict("esta fatura não está aberta para pagamento").Send(c)
	case errors.Is(err, services.ErrNoPayerAccount),
		errors.Is(err, services.ErrTestModeNotPayable),
		errors.Is(err, billing.ErrPayoutNotEnabled):
		// One message for three refusals, deliberately. Each has a reason that is
		// CTech's business — no linked account, a test-mode document, a merchant
		// still behind the payout gate — and none of them is something the person
		// holding the bill can act on. The specific reason is in the log.
		return problem.Conflict("pagamento indisponível para esta fatura").Send(c)
	case errors.Is(err, wallet.ErrChargeRejected):
		return problem.Unprocessable("não foi possível abrir a cobrança para este valor").Send(c)
	default:
		return fail(c, err)
	}
}
