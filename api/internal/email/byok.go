package email

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"
)

// ByokNotice is a message to an organization about its bank connection (BYOK
// spec § 3.4, § 3.5, § 3.6): certificate warnings, a degraded connection, a
// disconnect. pt-BR, the language of the company's owners.
type ByokNotice struct {
	// Kind: cert_90, cert_30, cert_7, cert_expired, degraded, disconnected,
	// excess_payment, webhook_overwritten (part B).
	Kind             string
	Provider         string // "inter"
	OrganizationName string
	ConsoleURL       string // the connection screen
	NotAfter         time.Time
	// excess_payment: the amount above what the invoice owed, and its number.
	Amount        int64
	InvoiceNumber int64
}

func providerName(p string) string {
	if p == "" {
		return "banco"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// byokSubject and the lead paragraph per kind. No em dash anywhere (UI copy rule).
func byokText(n ByokNotice) (subject, lead string) {
	bank := providerName(n.Provider)
	when := n.NotAfter.In(time.FixedZone("BRT", -3*3600)).Format("02/01/2006")
	renew := `Para renovar, entre no Internet Banking do ` + bank + `, abra Integrar, escolha a integração usada pela CTech e ` +
		`baixe o novo certificado e a nova chave. Depois envie os dois arquivos na tela da conexão.`
	switch n.Kind {
	case "cert_90", "cert_30", "cert_7":
		days := strings.TrimPrefix(n.Kind, "cert_")
		return "O certificado da integração " + bank + " vence em " + days + " dias",
			`<p>O certificado da integração com o ` + bank + ` vence em ` + when + `.</p><p>` + renew + `</p>`
	case "cert_expired":
		return "O certificado da integração " + bank + " venceu",
			`<p>O certificado da integração com o ` + bank + ` venceu em ` + when + `. O extrato parou de sincronizar.</p><p>` + renew + `</p>`
	case "degraded":
		return "A conexão com o " + bank + " precisa de atenção",
			`<p>As últimas tentativas de falar com o ` + bank + ` falharam. Abra a tela da conexão e use Testar conexão.</p>` +
				`<p>Se a integração foi cancelada no ` + bank + `, conecte de novo ou desconecte.</p>`
	case "excess_payment":
		return "Pagamento em duplicidade recebido pelo " + bank,
			`<p>Recebemos ` + brl(n.Amount) + ` a mais na fatura #` + fmt.Sprint(n.InvoiceNumber) + `. O dinheiro está na sua conta no ` + bank + `.</p>` +
				`<p>Nada é devolvido automaticamente. Se for o caso, abra a fatura no console e use Devolver.</p>`
	case "webhook_overwritten":
		return "Outro sistema passou a receber os avisos de pagamento do " + bank,
			`<p>O aviso de pagamentos Pix que a CTech registrou no ` + bank + ` foi substituído por outro sistema.</p>` +
				`<p>As faturas continuam sendo confirmadas, em até uma hora. Para voltar a confirmar na hora, abra a conexão e use Registrar aviso novamente.</p>`
	case "disconnected":
		return "A conexão com o " + bank + " foi desconectada",
			`<p>A CTech não guarda mais as credenciais da integração com o ` + bank + `.</p>` +
				`<p>Só você pode cancelar a integração no ` + bank + `: Internet Banking, Integrar, a integração, Cancelar.</p>`
	}
	return "", ""
}

func (c *Client) SendByokNotice(ctx context.Context, to string, n ByokNotice) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	subject, lead := byokText(n)
	if subject == "" {
		return fmt.Errorf("email: unknown byok notice %q", n.Kind)
	}
	link := ""
	if n.ConsoleURL != "" {
		link = `<p><a href="` + html.EscapeString(n.ConsoleURL) + `">Abrir a conexão</a></p>`
	}
	org := ""
	if n.OrganizationName != "" {
		org = `<p style="color:#666;font-size:13px">` + html.EscapeString(n.OrganizationName) + `</p>`
	}
	return c.tx.Send(ctx, to, subject,
		`<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">`+lead+link+org+`</body></html>`)
}

// brl renders centavos as "R$ 1.234,56".
func brl(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole, frac := cents/100, cents%100
	digits := fmt.Sprint(whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("R$ %s,%02d", b.String(), frac)
	if neg {
		out = "-" + out
	}
	return out
}
