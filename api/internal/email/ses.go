// Package email says what billing owes a customer. It does not send it.
//
// The transport — building an SES client, putting one HTML message on the wire —
// moved to api-commons/email, because it was the same code in ctech-account and
// here. What stays is the part that is billing's: which messages exist, when
// they are sent, and what they say.
//
// That split is the reason the shared package carries no templates. A shared
// package that knew about invoices would be a notification service, and
// inventing one to send two messages is a much larger decision than the feature.
package email

import (
	"context"
	"fmt"
	"html"

	commonemail "gopkg.aoctech.app/api-commons/email"
)

// Sender is what dunning needs. An interface at this seam, not because there
// will be a second implementation, but because the dunning job's tests must not
// send mail — and a job that can only be tested by giving it real SES
// credentials is a job nobody tests.
type Sender interface {
	SendInvoiceReminder(ctx context.Context, to Reminder) error
}

// Reminder is one message. It carries what the template renders and nothing
// else — no invoice, no customer record, so this package cannot grow into a
// second place that decides what a bill says.
type Reminder struct {
	To          string
	Name        string
	AmountLabel string
	DueLabel    string
	PayURL      string
	// Overdue changes the tone, not the facts. A note three days before a bill
	// is due and one seven days after it are the same information and very
	// different messages.
	Overdue bool
}

// transport is the shared SES client, narrowed to what this package uses. The
// interface is here rather than in the shared package because it is this
// package's tests that need to substitute it.
type transport interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Client renders billing's messages and hands them to the shared transport.
type Client struct {
	tx transport
}

// New builds a client. An empty `from` is refused by the shared transport.
func New(ctx context.Context, region, from string) (*Client, error) {
	tx, err := commonemail.New(ctx, region, from)
	if err != nil {
		return nil, err
	}
	return &Client{tx: tx}, nil
}

func (c *Client) SendInvoiceReminder(ctx context.Context, r Reminder) error {
	if r.To == "" {
		// A customer with no email is not an error to retry — it is a customer
		// nobody can remind. The caller decides what to do about it.
		return fmt.Errorf("email: no recipient")
	}
	subject := "Sua fatura vence em breve — CTech"
	if r.Overdue {
		subject = "Sua fatura está em aberto — CTech"
	}
	return c.tx.Send(ctx, r.To, subject, reminderHTML(r))
}

// reminderHTML renders the message.
//
// Every interpolated value is escaped. A customer's own name is attacker-
// controlled in the sense that matters — a merchant creates it through the M2M
// API — and an unescaped name in an HTML email is a link somebody else chose.
//
// The amount and the due date are pre-formatted by the caller: rendering
// currency in two places is how a customer is shown one number in the portal and
// a different one in their inbox.
func reminderHTML(r Reminder) string {
	lead := "Sua fatura vence em breve."
	if r.Overdue {
		lead = "Sua fatura está em aberto."
	}
	name := html.EscapeString(r.Name)
	if name == "" {
		name = "Olá"
	} else {
		name = "Olá, " + name
	}

	return `<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">` +
		`<p>` + name + `,</p>` +
		`<p>` + lead + `</p>` +
		`<p style="font-size:24px;font-weight:600">` + html.EscapeString(r.AmountLabel) + `</p>` +
		`<p>` + html.EscapeString(r.DueLabel) + `</p>` +
		`<p><a href="` + html.EscapeString(r.PayURL) + `" style="display:inline-block;background:#7c3f22;color:#fff;` +
		`padding:12px 20px;border-radius:8px;text-decoration:none">Pagar com PIX</a></p>` +
		`<p style="color:#666;font-size:13px">Se você já pagou, ignore este aviso.</p>` +
		`</body></html>`
}

// ApplicationReceived tells CTech someone asked to bill through CTech Billing
// (spec § 4.1). Every field is what the applicant typed or chose: escaped.
type ApplicationReceived struct {
	ApplicationID, OrganizationName, CompanyLegalName, Description, Website, ContactEmail string
}

// ApplicationDecided tells the applicant the outcome (spec § 4.2).
type ApplicationDecided struct {
	OrganizationName string
	Approved         bool
	Reason           string
}

func (c *Client) SendApplicationReceived(ctx context.Context, to string, n ApplicationReceived) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	subject := "Novo pedido de cobrança: " + n.OrganizationName
	body := `<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">` +
		`<p>` + html.EscapeString(n.OrganizationName) + ` pediu para cobrar os próprios clientes pelo CTech Billing.</p>` +
		`<p><strong>Empresa:</strong> ` + html.EscapeString(n.CompanyLegalName) + `</p>` +
		`<p><strong>O que vende:</strong> ` + html.EscapeString(n.Description) + `</p>` +
		`<p><strong>Site:</strong> ` + html.EscapeString(n.Website) + `</p>` +
		`<p><strong>Contato:</strong> ` + html.EscapeString(n.ContactEmail) + `</p>` +
		`<p style="color:#666;font-size:13px">Pedido ` + html.EscapeString(n.ApplicationID) + `. Decida em Console, Solicitações.</p>` +
		`</body></html>`
	return c.tx.Send(ctx, to, subject, body)
}

func (c *Client) SendApplicationDecided(ctx context.Context, to string, n ApplicationDecided) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	name := html.EscapeString(n.OrganizationName)
	subject := "Seu pedido de cobrança foi aprovado: CTech Billing"
	lead := `<p>O modo produção de ` + name + ` está pronto. Abra o console do CTech Billing para conectar suas credenciais.</p>`
	if !n.Approved {
		subject = "Seu pedido de cobrança não foi aprovado: CTech Billing"
		lead = `<p>O pedido de modo produção de ` + name + ` não foi aprovado.</p>` +
			`<p><strong>Motivo:</strong> ` + html.EscapeString(n.Reason) + `</p>` +
			`<p>O modo de teste continua disponível, e você pode pedir de novo pelo console.</p>`
	}
	return c.tx.Send(ctx, to, subject,
		`<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">`+lead+`</body></html>`)
}
