package v1

import (
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// The portal's payloads (ADR 0012).
//
// They are not the console's with fields removed — they are a different answer
// to a different question. The console asks "what is the state of this record";
// the portal asks "what am I paying, how much, and when". So there is no status
// enum here, no metadata, no audit timeline, and no organization: the internal
// name is absent from the payload rather than merely unused by the screen, which
// is the only version of that rule that survives a new client being written.

type portalSessionResponse struct {
	CustomerID string `json:"customer_id"`
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	// Since is when this person became a customer, so a screen can say "cliente
	// desde 2024" without counting anything.
	Since *brcal.Date `json:"since,omitempty"`
	// TermsAccepted is the server's answer to whether this person has agreed to
	// the billing terms addendum **in force**, not to any version of it. The
	// comparison is made here and only the result is published: which version
	// somebody is on is billing's business, and a client that could see it would
	// eventually decide for itself whether it was recent enough.
	//
	// The gate it drives is a screen, not a security control — the routes behind
	// it are already scoped, and refusing to serve an invoice to somebody who has
	// not re-read a document would be withholding a bill they still owe.
	TermsAccepted bool `json:"terms_accepted"`
}

// portalInvoiceResponse describes one invoice in the words a person uses.
//
// State is a short phrase plus a machine-readable tone, never the internal
// status. The tone exists so the UI can color and sort without parsing the
// phrase, and it is deliberately a small closed set — a client that switches on
// it cannot come to depend on wording.
type portalInvoiceResponse struct {
	ID     string `json:"id"`
	Number int64  `json:"number,omitempty"`
	// Description says what was bought, taken from the invoice lines. Without it
	// the screen is a list of amounts and dates that answers "how much" and
	// "when" but never "what for".
	Description string        `json:"description"`
	State       string        `json:"state"`
	Tone        string        `json:"tone"`
	DueDate     brcal.Date    `json:"due_date"`
	Total       billing.Cents `json:"total"`
	AmountPaid  billing.Cents `json:"amount_paid,omitempty"`
	AmountDue   billing.Cents `json:"amount_due"`
	Currency    string        `json:"currency"`
	Period      portalPeriod  `json:"period"`
	// PaidOn is the civil date the invoice was settled, absent until it is. A
	// date and not a timestamp: a receipt is read as "paguei no dia 17", and
	// publishing the instant would put a UTC hour on a consumer screen.
	PaidOn *brcal.Date `json:"paid_on,omitempty"`
	// Settled is the server's answer to "is this bill done with", and the screen
	// must not re-derive it from the amounts. A zero-total invoice — the Free
	// plan, ADR 0019 — is issued and settled on the spot with nothing paid, so
	// `amount_paid > 0` is a test that calls it unpaid and shows a person a
	// message about a bill that never existed.
	Settled bool         `json:"settled"`
	Lines   []portalLine `json:"lines,omitempty"`
	// Payable says whether there is anything for this person to do. The server
	// decides it; a UI that derives "can I pay this?" from a status string is a UI
	// that will offer to pay a voided invoice.
	//
	// Not payable and not settled are two different facts and both are
	// published, because "no button" has several reasons and they need different
	// sentences: a settled invoice is finished, a voided one was cancelled, an
	// uncollectible one is a conversation.
	Payable bool `json:"payable"`
}

// portalPaymentResponse answers "pagar" with the invoice **and** the charge.
//
// Both, because the state of the invoice is what the screen must re-render after
// a payment starts, and a client that has to fetch it again renders a stale
// amount for one round trip.
type portalPaymentResponse struct {
	Invoice portalInvoiceResponse `json:"invoice"`
	Payment checkoutPayment       `json:"payment"`
}

type portalPeriod struct {
	Start brcal.Date `json:"start"`
	End   brcal.Date `json:"end"`
}

type portalLine struct {
	Description string        `json:"description"`
	Amount      billing.Cents `json:"amount"`
	// Proration is kept, and named in the phrase, because an unexplained partial
	// amount is the single most common "what is this charge?" support message.
	Proration bool `json:"proration"`
}

type portalSubscriptionResponse struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	State       string `json:"state"`
	Tone        string `json:"tone"`
	// Renewal is when it next bills, absent once it is ending or ended — a date
	// that will not happen is worse than no date.
	Renewal *brcal.Date   `json:"renews_on,omitempty"`
	Amount  billing.Cents `json:"amount,omitempty"`
	// Metered marks a subscription whose amount is only known when the period
	// closes, so the screen can say that instead of showing a confident zero.
	Metered  bool         `json:"metered"`
	Currency string       `json:"currency,omitempty"`
	Period   portalPeriod `json:"current_period"`
	// Since is when this subscription started, so the screen can say how long
	// somebody has been a customer without counting invoices.
	Since *brcal.Date `json:"since,omitempty"`
	// Cancelable is the server's answer, and the screen must not hide it: a
	// subscription you cannot find how to cancel is a subscription you dispute
	// with your bank.
	Cancelable bool `json:"cancelable"`
	// Invoices is the recent billing history of this subscription, newest
	// first, and it is populated on the detail only — the list endpoint would
	// otherwise fetch every subscription's invoices to render rows that do not
	// show them. Capped, not paginated: "what has this plan charged me lately"
	// is context, and the whole history is what /invoices is for.
	Invoices []portalInvoiceResponse `json:"recent_invoices,omitempty"`
}

// subscriptionInvoiceLimit is how many past invoices the subscription detail
// carries. Six covers half a year of a monthly plan, which is as far back as
// anybody reads before going to the invoice list instead.
const subscriptionInvoiceLimit = 6

// Tones. A closed set, ordered by how much they want the reader's attention.
const (
	toneNeutral   = "neutral"
	tonePositive  = "positive"
	toneAttention = "attention"
	toneUrgent    = "urgent"
)

// invoiceState renders an invoice's status as a phrase and a tone.
//
// Every branch answers "what does this mean for me", never "what state is the
// record in". UNCOLLECTIBLE is the one that matters most: internally it means
// billing gave up collecting automatically, and telling a person their invoice
// is "uncollectible" is both frightening and useless. It reads as what it is —
// something to sort out with a human.
func invoiceState(inv *billing.Invoice, today brcal.Date) (state, tone string) {
	switch inv.Status {
	case billing.InvoicePaid:
		return "Paga", tonePositive
	case billing.InvoiceVoid:
		return "Cancelada", toneNeutral
	case billing.InvoiceUncollectible:
		return "Pendente de acordo", toneAttention
	case billing.InvoiceDraft:
		// A draft is not yet a bill. Saying "em aberto" would be a demand for
		// money nobody has asked for yet.
		return "Em preparação", toneNeutral
	}

	days := today.DaysBetween(inv.DueDate)
	switch {
	case days < 0:
		return pluralDays("Vencida há %d dia", "Vencida há %d dias", -days), toneUrgent
	case days == 0:
		return "Vence hoje", toneUrgent
	case days == 1:
		return "Vence amanhã", toneAttention
	case days <= 7:
		return fmt.Sprintf("Vence em %d dias", days), toneAttention
	default:
		// Beyond a week, a countdown stops being useful and a date starts being
		// useful: nobody plans around "vence em 23 dias".
		return "Vence em " + formatBR(inv.DueDate), toneNeutral
	}
}

// formatBR renders a civil date the way it is read in Brazil.
func formatBR(d brcal.Date) string {
	return fmt.Sprintf("%02d/%02d/%04d", d.Day, int(d.Month), d.Year)
}

// subscriptionState renders a subscription's status the same way.
func subscriptionState(sub *billing.Subscription) (state, tone string) {
	switch sub.Status {
	case billing.SubscriptionTrialing:
		return "Em teste", tonePositive
	case billing.SubscriptionPastDue:
		// Not "PAST_DUE" and not "inadimplente": the second is a judgement, and
		// the person reading it usually just needs to pay one invoice.
		return "Pagamento pendente", toneUrgent
	case billing.SubscriptionPaused:
		return "Pausada", toneNeutral
	case billing.SubscriptionCanceled:
		return "Encerrada", toneNeutral
	case billing.SubscriptionIncomplete:
		return "Aguardando confirmação", toneAttention
	default:
		if sub.CancelAtPeriodEnd {
			return "Ativa até o fim do período", toneAttention
		}
		return "Ativa", tonePositive
	}
}

func pluralDays(one, many string, n int) string {
	if n == 1 {
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}

func newPortalInvoiceResponse(inv *billing.Invoice, lines []billing.InvoiceItem, today brcal.Date) portalInvoiceResponse {
	state, tone := invoiceState(inv, today)
	out := portalInvoiceResponse{
		ID:          inv.ID,
		Number:      inv.Number,
		Description: describeLines(lines),
		State:       state,
		Tone:        tone,
		DueDate:     inv.DueDate,
		Total:       inv.Total,
		AmountPaid:  inv.AmountPaid,
		AmountDue:   inv.AmountDue(),
		Currency:    inv.Currency,
		Period:      portalPeriod{Start: inv.Period.Start, End: inv.Period.End},
		PaidOn:      civilDate(inv.PaidAt),
		Settled:     inv.Status == billing.InvoicePaid,
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

// civilDate renders a stored RFC 3339 instant as the civil date it happened on
// in São Paulo, or nothing at all. A value that does not parse is dropped
// rather than surfaced: an unreadable timestamp is a bug to fix in the logs,
// not a broken date on somebody's receipt.
func civilDate(stamp string) *brcal.Date {
	if stamp == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return nil
	}
	d := brcal.FromTime(t)
	return &d
}

// describeLines names what an invoice is for in one phrase.
//
// One line is its own description; several become "X e mais N". A concatenation
// of every line would be a paragraph in a table cell, and the detail screen is
// where the full list belongs.
func describeLines(lines []billing.InvoiceItem) string {
	switch len(lines) {
	case 0:
		return "Fatura"
	case 1:
		return lines[0].Description
	default:
		return fmt.Sprintf("%s e mais %d", lines[0].Description, len(lines)-1)
	}
}
