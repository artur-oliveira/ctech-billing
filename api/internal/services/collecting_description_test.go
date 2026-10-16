package services

import (
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestInvoiceChargeDescription(t *testing.T) {
	if got := invoiceChargeDescription(&billing.Invoice{ID: "inv_abc", Number: 42}, nil); got != "Fatura #42" {
		t.Fatalf("numbered invoice: %q", got)
	}
	if got := invoiceChargeDescription(&billing.Invoice{ID: "inv_abc"}, nil); got != "Fatura inv_abc" {
		t.Fatalf("unnumbered invoice falls back to the id: %q", got)
	}
}

func TestThePayerReadsWhatTheServiceWroteElseTheInvoiceAndThePlanOfASubscription(t *testing.T) {
	plan := []billing.InvoiceItem{{Description: "DF-e Ilimitado"}}
	cases := []struct {
		name  string
		inv   *billing.Invoice
		items []billing.InvoiceItem
		want  string
	}{
		{"subscription invoice names the plan", &billing.Invoice{ID: "i", Number: 18, SubscriptionID: "sub_1"}, plan, "Fatura #18 - Assinatura DF-e Ilimitado"},
		{"subscription invoice without lines keeps the number alone", &billing.Invoice{ID: "i", Number: 18, SubscriptionID: "sub_1"}, nil, "Fatura #18"},
		{"a one-off invoice is not a subscription", &billing.Invoice{ID: "i", Number: 19}, plan, "Fatura #19"},
		{"what the service wrote wins, even over the plan", &billing.Invoice{ID: "i", Number: 20, SubscriptionID: "sub_1", PayerMessage: " Pacote de 10.000 fichas "}, plan, "Pacote de 10.000 fichas"},
	}
	for _, c := range cases {
		if got := invoiceChargeDescription(c.inv, c.items); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	long := &billing.Invoice{PayerMessage: strings.Repeat("ã", 200)}
	if got := invoiceChargeDescription(long, nil); len([]rune(got)) != billing.MaxPayerMessage {
		t.Errorf("a message over the Bacen limit is cut to %d characters, got %d", billing.MaxPayerMessage, len([]rune(got)))
	}
}
