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
