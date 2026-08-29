// Package invoicepdf renders an invoice as the PDF a customer files and an
// accountant reads.
//
// **The rendered document is the invoice, not a picture of a screen.** So it
// carries only frozen facts — number, issuer, customer, period, lines, totals,
// due date — and no status: an invoice does not stop being that document by
// being paid, and one that said "PAGA" would be a receipt, which is a different
// document nobody has asked for yet.
//
// That restriction is also what makes generating it lazily safe. A finalized
// invoice is immutable — its lines are frozen and a correction is a credit note
// — so rendering it today and rendering it in a year produce the same document,
// which means the file can be produced on the first download rather than by a
// job at finalization, with no window in which an invoice exists and its
// document does not.
//
// HTML rather than the layout API, through folio's in-process converter: the
// invoice is a header and a table, the family already writes HTML for e-mail,
// and a template is the version somebody can edit without learning a layout
// engine.
package invoicepdf

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/carlos7ags/folio/document"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Issuer is who is charging. Every field beyond the name is optional — an
// organization that has not filled in its legal details still gets a usable
// document rather than a refusal.
type Issuer struct {
	Name      string
	LegalName string
	TaxID     string
	Address   string
	Email     string
}

// Customer is who is being charged. The tax id is rendered **in full**: this is
// the document, it goes to the person it identifies, and a masked id on an
// invoice is not something an accountant can file.
type Customer struct {
	Name  string
	TaxID string
	Email string
}

// Input is everything the template needs, resolved by the caller. The renderer
// reads no repository: it is a pure function of these values, which is what
// makes it testable without a database and identical on every run.
type Input struct {
	Invoice  *billing.Invoice
	Lines    []billing.InvoiceItem
	Issuer   Issuer
	Customer Customer
}

// Render produces the PDF bytes.
func Render(in Input) ([]byte, error) {
	if in.Invoice == nil {
		return nil, fmt.Errorf("invoicepdf: no invoice")
	}
	if in.Invoice.Number == 0 {
		// A draft has no number and is not a document yet. Rendering one would
		// produce a file that looks official and refers to nothing.
		return nil, fmt.Errorf("invoicepdf: invoice %s has no number — it has not been issued", in.Invoice.ID)
	}

	v := newView(in)
	var html bytes.Buffer
	if err := tmpl.Execute(&html, v); err != nil {
		return nil, fmt.Errorf("invoicepdf: rendering the template: %w", err)
	}

	doc := document.NewDocument(document.PageSizeA4)
	doc.Info.Title = "Fatura nº " + v.Number
	doc.Info.Author = v.Issuer.Name
	if err := doc.AddHTML(html.String(), nil); err != nil {
		return nil, fmt.Errorf("invoicepdf: converting to PDF: %w", err)
	}

	var out bytes.Buffer
	if _, err := doc.WriteTo(&out); err != nil {
		return nil, fmt.Errorf("invoicepdf: writing the PDF: %w", err)
	}
	return out.Bytes(), nil
}

// view is the template's vocabulary: strings, already formatted. Money and
// dates are rendered here and nowhere else, so the PDF cannot disagree with the
// screen about what an amount is.
type view struct {
	Number      string
	Period      string
	DueDate     string
	Issuer      Issuer
	Customer    Customer
	Lines       []lineView
	Subtotal    string
	Discount    string
	Total       string
	HasDiscount bool
}

type lineView struct {
	Description string
	Period      string
	Quantity    string
	UnitAmount  string
	Amount      string
	Proration   bool
}

func newView(in Input) view {
	inv := in.Invoice
	out := view{
		Number:      fmt.Sprintf("%d", inv.Number),
		Period:      formatPeriod(inv.Period.Start, inv.Period.End),
		DueDate:     formatDate(inv.DueDate),
		Issuer:      in.Issuer,
		Customer:    in.Customer,
		Subtotal:    inv.Subtotal.String(),
		Discount:    inv.Discount.String(),
		Total:       inv.Total.String(),
		HasDiscount: inv.Discount != 0,
	}
	for _, line := range in.Lines {
		out.Lines = append(out.Lines, lineView{
			Description: line.Description,
			Period:      formatPeriod(line.Period.Start, line.Period.End),
			Quantity:    fmt.Sprintf("%d", line.Quantity),
			UnitAmount:  line.UnitAmount.String(),
			Amount:      line.Amount.String(),
			Proration:   line.Proration,
		})
	}
	return out
}

var months = [...]string{
	"janeiro", "fevereiro", "março", "abril", "maio", "junho",
	"julho", "agosto", "setembro", "outubro", "novembro", "dezembro",
}

func formatDate(d brcal.Date) string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%d de %s de %d", d.Day, months[int(d.Month)-1], d.Year)
}

func formatPeriod(start, end brcal.Date) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	return fmt.Sprintf("%02d/%02d/%04d a %02d/%02d/%04d",
		start.Day, int(start.Month), start.Year, end.Day, int(end.Month), end.Year)
}

// The document. Deliberately plain: an invoice is read, filed and often printed
// in black and white, so there is no colour beyond the ink. Everything
// interpolated is escaped by html/template — a customer's name is written
// through the M2M API and is attacker-controlled in the sense that matters.
var tmpl = template.Must(template.New("invoice").Parse(`
<style>
  body { font-family: Helvetica; font-size: 9.5pt; color: #1a1a1a; }
  h1 { font-size: 17pt; margin: 0 0 2pt 0; }
  .muted { color: #595959; }
  .small { font-size: 8.5pt; }
  .rule { border-bottom: 1px solid #1a1a1a; margin: 10pt 0; }
  .hairline { border-bottom: 1px solid #cccccc; margin: 8pt 0; }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; font-size: 8pt; color: #595959; padding: 0 0 4pt 0; border-bottom: 1px solid #cccccc; }
  td { padding: 5pt 0; border-bottom: 1px solid #eeeeee; vertical-align: top; }
  .r { text-align: right; }
  .totals td { border-bottom: none; padding: 2pt 0; }
  .total-row td { border-top: 1px solid #1a1a1a; padding-top: 5pt; font-size: 11pt; }
</style>

<h1>Fatura nº {{.Number}}</h1>
<p class="muted small">Período de {{.Period}} · Vencimento em {{.DueDate}}</p>

<div class="rule"></div>

<table>
  <tr>
    <td style="width:50%; border-bottom:none; padding-top:0">
      <span class="small muted">Emitida por</span><br/>
      <strong>{{with .Issuer.LegalName}}{{.}}{{else}}{{$.Issuer.Name}}{{end}}</strong>
      {{with .Issuer.TaxID}}<br/><span class="small">CNPJ {{.}}</span>{{end}}
      {{with .Issuer.Address}}<br/><span class="small muted">{{.}}</span>{{end}}
      {{with .Issuer.Email}}<br/><span class="small muted">{{.}}</span>{{end}}
    </td>
    <td style="width:50%; border-bottom:none; padding-top:0">
      <span class="small muted">Cobrada de</span><br/>
      <strong>{{.Customer.Name}}</strong>
      {{with .Customer.TaxID}}<br/><span class="small">CPF/CNPJ {{.}}</span>{{end}}
      {{with .Customer.Email}}<br/><span class="small muted">{{.}}</span>{{end}}
    </td>
  </tr>
</table>

<div class="hairline"></div>

<table>
  <tr>
    <th>Descrição</th>
    <th>Período</th>
    <th class="r">Qtd.</th>
    <th class="r">Unitário</th>
    <th class="r">Valor</th>
  </tr>
  {{range .Lines}}
  <tr>
    <td>{{.Description}}{{if .Proration}}<br/><span class="small muted">Proporcional aos dias usados</span>{{end}}</td>
    <td class="small muted">{{.Period}}</td>
    <td class="r">{{.Quantity}}</td>
    <td class="r">{{.UnitAmount}}</td>
    <td class="r">{{.Amount}}</td>
  </tr>
  {{end}}
</table>

<!-- The totals sit in the same two right-hand columns as the line amounts, so
     the total lands directly under the numbers it sums. A centred label over a
     column of figures is the layout that makes somebody check the arithmetic by
     hand. -->
<table class="totals" style="margin-top:8pt">
  {{if .HasDiscount}}
  <tr>
    <td style="width:70%"></td>
    <td class="r" style="width:15%">Subtotal</td>
    <td class="r" style="width:15%">{{.Subtotal}}</td>
  </tr>
  <tr>
    <td></td>
    <td class="r">Desconto</td>
    <td class="r">−{{.Discount}}</td>
  </tr>
  {{end}}
  <tr class="total-row">
    <td style="width:70%; border-top:none"></td>
    <td class="r" style="width:15%"><strong>Total</strong></td>
    <td class="r" style="width:15%"><strong>{{.Total}}</strong></td>
  </tr>
</table>

<p class="small muted" style="margin-top:14pt">
  Documento gerado por CTech Billing. É o demonstrativo da cobrança, não é nota
  fiscal.
</p>
`))
