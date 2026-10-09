package invoicepdf

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func issued() Input {
	period := billing.Period{
		Start: brcal.New(2026, time.March, 1),
		End:   brcal.New(2026, time.March, 31),
	}
	return Input{
		Invoice: &billing.Invoice{
			ID:       "in_01",
			Number:   1042,
			Status:   billing.InvoiceOpen,
			Period:   period,
			DueDate:  brcal.New(2026, time.April, 10),
			Currency: billing.CurrencyBRL,
			Subtotal: 11300,
			Total:    11300,
		},
		Lines: []billing.InvoiceItem{
			{Description: "Plano Essencial · mensal", Period: period, Quantity: 1, UnitAmount: 8900, Amount: 8900},
			{Description: "Emissões adicionais", Period: period, Quantity: 120, UnitAmount: 20, Amount: 2400, Proration: true},
		},
		Issuer:   Issuer{Name: "CTech", LegalName: "A O CARVALHO TECH LTDA", TaxID: "12.345.678/0001-90"},
		Customer: Customer{Name: "Ana Ribeiro", TaxID: "123.456.789-09", Email: "ana@exemplo.com.br"},
	}
}

func TestRenderProducesAPDF(t *testing.T) {
	out, err := Render(issued())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("output does not start with a PDF header: %q", out[:min(8, len(out))])
	}
	if len(out) < 500 {
		t.Fatalf("PDF is %d bytes, which is too small to contain an invoice", len(out))
	}
}

// A draft has no number and is not a document. Rendering one would produce a
// file that looks official and refers to nothing.
func TestRenderRefusesAnUnissuedInvoice(t *testing.T) {
	in := issued()
	in.Invoice.Number = 0
	if _, err := Render(in); err == nil {
		t.Fatal("rendered an invoice with no number")
	}
	if _, err := Render(Input{}); err == nil {
		t.Fatal("rendered nothing at all")
	}
}

// The whole point of lazy generation: the same invoice renders to the same
// document however many times it is asked for, so producing it on first
// download is safe.
func TestRenderIsDeterministic(t *testing.T) {
	first, err := Render(issued())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(issued())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of the same invoice produced different documents")
	}
}

// The status is deliberately absent: an invoice does not stop being that
// document by being paid, and one stamped PAGA would be a receipt.
func TestRenderNeverStampsAStatus(t *testing.T) {
	in := issued()
	in.Invoice.Status = billing.InvoicePaid
	in.Invoice.AmountPaid = in.Invoice.Total
	paid, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	open, err := Render(issued())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(paid, open) {
		t.Fatal("paying an invoice changed its document")
	}
}

// A customer's name is written through the M2M API, so it is attacker-
// controlled in the sense that matters: it must not be able to close the
// template's markup and inject its own.
func TestRenderEscapesTheNamesItIsGiven(t *testing.T) {
	in := issued()
	in.Customer.Name = `<script>alert(1)</script>`
	in.Issuer.LegalName = `</table><h1>injected`
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Fatal("a customer name reached the document as markup")
	}
}

// The key names the tenant and the mode before the id, so one organization's
// documents are addressable as a prefix and a test-mode document can never
// land under live.
func TestKeySeparatesTenantAndMode(t *testing.T) {
	live := Key("org_1", true, "in_9")
	test := Key("org_1", false, "in_9")
	if live == test {
		t.Fatal("the two modes share a key")
	}
	if !strings.HasPrefix(live, "invoices/org_1/live/") {
		t.Fatalf("key = %q", live)
	}
	if Key("org_2", true, "in_9") == live {
		t.Fatal("two organizations share a key")
	}
}

func TestParseLangFallsBackToPortuguese(t *testing.T) {
	cases := map[string]Lang{
		"": PTBR, "pt-BR": PTBR, "pt": PTBR, "fr": PTBR, "garbage": PTBR,
		"en": EN, "EN": EN, "en-US": EN, "en-GB": EN, " en ": EN,
	}
	for in, want := range cases {
		if got := ParseLang(in); got != want {
			t.Errorf("ParseLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderHTMLPortuguese(t *testing.T) {
	html, _, err := renderHTML(issued())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Fatura nº 1042", "Período de 01/03/2026 a 31/03/2026", "Vencimento em 10 de abril de 2026",
		"Emitida por", "Cobrada de", "Descrição", "Unitário", "Proporcional aos dias usados",
		"R$ 89,00", "R$ 113,00", "Total", "não é nota",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("pt-BR document is missing %q", want)
		}
	}
}

func TestRenderHTMLEnglish(t *testing.T) {
	in := issued()
	in.Lang = EN
	in.Invoice.Subtotal = 123456789
	in.Invoice.Discount = 1000
	in.Invoice.Total = 123455789
	html, _, err := renderHTML(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Invoice no. 1042", "March 1, 2026 to March 31, 2026", "Due on April 10, 2026",
		"Issued by", "Billed to", "Description", "Unit price", "Prorated for the days used",
		"Subtotal", "Discount", "R$ 1,234,567.89", "R$ 89.00", "not a tax invoice",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("en document is missing %q", want)
		}
	}
	for _, leak := range []string{"Fatura", "Vencimento", "Emitida", "Descrição", "Desconto", "R$ 89,00"} {
		if strings.Contains(html, leak) {
			t.Errorf("en document still contains Portuguese %q", leak)
		}
	}
}

func TestRenderBothLanguagesProducePDFs(t *testing.T) {
	pt, err := Render(issued())
	if err != nil {
		t.Fatal(err)
	}
	in := issued()
	in.Lang = EN
	en, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(en, []byte("%PDF-")) || bytes.Equal(pt, en) {
		t.Fatal("the English render is not a distinct PDF")
	}
}

// The default language keeps the original key so documents stored before
// languages existed stay reachable; other languages must not overwrite it.
func TestKeyForKeepsDefaultKeyAndSuffixesOthers(t *testing.T) {
	base := Key("org_1", true, "in_9")
	if got := KeyFor("org_1", true, "in_9", PTBR); got != base {
		t.Errorf("pt-BR key = %q, want the legacy %q", got, base)
	}
	if got := KeyFor("org_1", true, "in_9", ""); got != base {
		t.Errorf("empty-language key = %q, want %q", got, base)
	}
	en := KeyFor("org_1", true, "in_9", EN)
	if en == base || !strings.HasSuffix(en, "/in_9.en.pdf") {
		t.Errorf("en key = %q", en)
	}
}

func TestFilenameIsLocalized(t *testing.T) {
	if got := Filename(PTBR, 1042); got != "fatura-1042.pdf" {
		t.Errorf("pt-BR filename = %q", got)
	}
	if got := Filename(EN, 1042); got != "invoice-1042.pdf" {
		t.Errorf("en filename = %q", got)
	}
}
