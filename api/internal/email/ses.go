// Package email sends the two messages billing owes a customer.
//
// It is deliberately small and it is deliberately here. Notification delivery is
// arguably not billing's domain — but there is no notification service in this
// family, and ctech-account already sends its own mail
// (ctech-account/api/internal/email). The convention is that a service sends
// what it is responsible for saying, and inventing a shared notification service
// to send two templates would be a larger decision than the feature.
//
// **This is the third SES client in the company** (ctech-account, ctech-wallet's
// Asaas notifications, and now this). That is a duplication worth collapsing
// into ctech-go-common the next time one of them changes, and it is noted here
// rather than fixed here because moving it is a change to two other repositories.
package email

import (
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"gopkg.aoctech.app/api-commons/awsconfig"
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

// Client sends through SESv2.
type Client struct {
	ses  *sesv2.Client
	from string
}

// New builds a client. An empty `from` disables sending entirely — see Disabled.
func New(ctx context.Context, region, from string) (*Client, error) {
	if strings.TrimSpace(from) == "" {
		return nil, fmt.Errorf("email: a sender address is required")
	}
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("email: loading AWS config: %w", err)
	}
	return &Client{ses: sesv2.NewFromConfig(cfg), from: from}, nil
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
	return c.send(ctx, r.To, subject, reminderHTML(r))
}

func (c *Client) send(ctx context.Context, to, subject, body string) error {
	_, err := c.ses.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(c.from),
		Destination:      &sestypes.Destination{ToAddresses: []string{to}},
		Content: &sestypes.EmailContent{
			Simple: &sestypes.Message{
				Subject: &sestypes.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
				Body: &sestypes.Body{
					Html: &sestypes.Content{Data: aws.String(body), Charset: aws.String("UTF-8")},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("email: sending to %s: %w", to, err)
	}
	return nil
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
