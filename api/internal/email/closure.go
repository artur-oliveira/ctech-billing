package email

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"
)

// ClosureNotice is one of the merchant-tenant-closure e-mails to the workspace
// owner (spec 2026-10-17-merchant-tenant-deletion.md). Link is empty for the
// messages that carry none.
type ClosureNotice struct {
	Kind       string // confirm | scheduled | blocked | cancelled
	FirstName  string
	Link       string
	GraceUntil time.Time
	Blockers   []string
}

var closureBrt = time.FixedZone("BRT", -3*60*60)

var closureBlockerText = map[string]string{
	"billing.tenant_active_subscriptions": "Há assinaturas de clientes ativas: cancele-as no console.",
	"billing.tenant_payment_in_flight":    "Há uma cobrança em andamento: aguarde o pagamento ou a expiração.",
	"billing.tenant_payout_processing":    "Há um repasse em processamento: aguarde a conclusão.",
	"billing.tenant_refund_open":          "Há um reembolso em andamento: aguarde a conclusão.",
}

// SendClosureNotice sends one closure e-mail to the owner's verified address.
func (c *Client) SendClosureNotice(ctx context.Context, to string, n ClosureNotice) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	subject, body := closureHTML(n)
	if subject == "" {
		return fmt.Errorf("email: unknown closure notice %q", n.Kind)
	}
	return c.tx.Send(ctx, to, subject, body)
}

func closureHTML(n ClosureNotice) (subject, body string) {
	greet := "Olá"
	if n.FirstName != "" {
		greet = "Olá, " + html.EscapeString(n.FirstName)
	}
	button := func(label string) string {
		return `<p><a href="` + html.EscapeString(n.Link) + `" style="display:inline-block;background:#7c3f22;color:#fff;` +
			`padding:12px 20px;border-radius:8px;text-decoration:none">` + label + `</a></p>`
	}
	until := n.GraceUntil.In(closureBrt).Format("02/01/2006 às 15:04")
	var main string
	switch n.Kind {
	case "confirm":
		subject = "Confirme o encerramento da sua conta de cobrança — CTech"
		main = `<p>Recebemos um pedido para <strong>encerrar a conta de cobrança</strong> da sua organização no CTech Billing: ` +
			`credenciais, clientes, assinaturas, faturas e webhooks serão apagados. Se quiser guardar os documentos, exporte-os antes. ` +
			`O link vale 24 horas.</p><p>Ao confirmar, a conta fica somente para leitura na hora e é encerrada de vez em 7 dias; até lá você pode cancelar.</p>` + button("Confirmar encerramento")
	case "scheduled":
		subject = "Sua conta de cobrança será encerrada — CTech"
		main = `<p>A conta de cobrança da sua organização está somente para leitura e será <strong>encerrada definitivamente em ` + until +
			`</strong> (horário de Brasília). Depois disso não há como desfazer. Para manter a conta, cancele antes.</p>` + button("Cancelar encerramento")
	case "blocked":
		subject = "O encerramento da conta de cobrança está pendente — CTech"
		var items strings.Builder
		for _, b := range n.Blockers {
			text, ok := closureBlockerText[b]
			if !ok {
				text = html.EscapeString(b)
			}
			items.WriteString("<li>" + text + "</li>")
		}
		main = `<p>O prazo para encerrar a conta de cobrança terminou, mas ela não pôde ser concluída:</p><ul>` + items.String() +
			`</ul><p>A conta continua somente para leitura. Cancele, resolva os itens acima e faça um novo pedido.</p>` + button("Cancelar encerramento")
	case "cancelled":
		subject = "Encerramento da conta de cobrança cancelado — CTech"
		main = `<p>O encerramento da conta de cobrança foi cancelado. Ela voltou a aceitar alterações.</p>`
	default:
		return "", ""
	}
	return subject, `<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">` +
		`<p>` + greet + `,</p>` + main + `<p style="color:#666;font-size:13px">Se não foi você, fale com o suporte.</p></body></html>`
}
