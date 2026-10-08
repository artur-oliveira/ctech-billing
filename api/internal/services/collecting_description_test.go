package services

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestInvoiceChargeDescription(t *testing.T) {
	if got := invoiceChargeDescription(&billing.Invoice{ID: "inv_abc", Number: 42}); got != "Fatura #42" {
		t.Fatalf("numbered invoice: %q", got)
	}
	if got := invoiceChargeDescription(&billing.Invoice{ID: "inv_abc"}); got != "Fatura inv_abc" {
		t.Fatalf("unnumbered invoice falls back to the id: %q", got)
	}
}
