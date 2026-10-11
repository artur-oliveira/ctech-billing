package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// An invoice row carries its customer's partition from the moment it is
// written, so the per-customer read can be a key condition on customer-index
// instead of a filtered walk of the tenant's whole INVOICE# range — and it
// keeps its subscription's lookup_pk, which is a different partition on a
// different index.
func TestAnInvoiceRowIsInItsCustomersPartition(t *testing.T) {
	inv := &billing.Invoice{ID: "inv_1", OrganizationID: "org_1", Livemode: true,
		CustomerID: "cus_1", SubscriptionID: "sub_1", Status: billing.InvoiceDraft,
		Period: billing.Period{Start: brcal.New(2026, time.March, 1), End: brcal.New(2026, time.April, 1)}}
	row := invoiceRowOf(inv, "si_1:2026-03-01", time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	if row.CustomerPK != LookupCustomerInvoicesPK("org_1", true, "cus_1") {
		t.Fatalf("customer_pk = %q", row.CustomerPK)
	}
	if row.CustomerPK != "org_1#live#CUSTOMER_INVOICES#cus_1" {
		t.Fatalf("customer_pk layout changed: %q — existing rows and the backfill depend on it", row.CustomerPK)
	}
	if row.LookupPK != LookupSubscriptionInvoicesPK("org_1", true, "sub_1") {
		t.Fatalf("lookup_pk = %q: the subscription's partition must survive", row.LookupPK)
	}
	if LookupCustomerInvoicesPK("org_1", true, "cus_1") == LookupCustomerInvoicesPK("org_1", false, "cus_1") {
		t.Fatal("a test-mode read must not reach live invoices")
	}
}

// A one-off invoice has no subscription partition but still has its customer's.
func TestAOneOffInvoiceIsStillInItsCustomersPartition(t *testing.T) {
	inv := &billing.Invoice{ID: "inv_2", OrganizationID: "org_1", Livemode: false,
		CustomerID: "cus_1", Status: billing.InvoiceDraft}
	row := invoiceRowOf(inv, "", time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	if row.LookupPK != "" {
		t.Fatalf("lookup_pk = %q on a one-off invoice", row.LookupPK)
	}
	if row.CustomerPK != "org_1#test#CUSTOMER_INVOICES#cus_1" {
		t.Fatalf("customer_pk = %q", row.CustomerPK)
	}
}

// The backfill writes customer_pk on invoice rows only: everything nested
// under an invoice shares its INVOICE# prefix.
func TestTheBackfillTellsAnInvoiceFromWhatIsNestedUnderIt(t *testing.T) {
	cases := map[string]bool{
		InvoiceSK("in_1"):            true,
		InvoiceItemSK("in_1", 0):     false,
		PaymentAttemptSK("in_1", 1):  false,
		CheckoutSK("in_1", "cs_1"):   false,
		CreditNoteSK("in_1", "cn_1"): false,
		skInvoice:                    false,
		SubscriptionSK("sub_1"):      false,
		InvoiceCounterSK(2026):       false,
	}
	for sk, want := range cases {
		if got := isInvoiceSK(sk); got != want {
			t.Errorf("isInvoiceSK(%q) = %v, want %v", sk, got, want)
		}
	}
}

// customer-index is its own index on its own attribute: lookup_pk on an
// invoice row is already its subscription's partition, and one attribute holds
// one partition.
func TestTheInvoicesTableHasTheCustomerIndex(t *testing.T) {
	s, ok := Schemas()[TableInvoices]
	if !ok {
		t.Fatal("no invoices schema")
	}
	for _, idx := range s.Indexes {
		if idx.Name == IndexCustomer {
			if idx.HashKey != "customer_pk" || idx.RangeKey != "sk" {
				t.Fatalf("customer-index keys = %s/%s, want customer_pk/sk", idx.HashKey, idx.RangeKey)
			}
			return
		}
	}
	t.Fatal("invoices declares no customer-index")
}
