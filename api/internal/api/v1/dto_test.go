package v1

import (
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/services"
)

const (
	testLinkSecret  = "dto-test-link-secret"
	testLinkBaseURL = "https://pay.test/c"
)

func testInvoice(status billing.InvoiceStatus, total billing.Cents, livemode bool) *billing.Invoice {
	return &billing.Invoice{
		ID:             "in_abc",
		OrganizationID: "ctech",
		Livemode:       livemode,
		CustomerID:     "cus_abc",
		Status:         status,
		Currency:       billing.CurrencyBRL,
		Total:          total,
		Subtotal:       total,
		DueDate:        brcal.New(2026, time.March, 20),
	}
}

// checkout_url is what an integration sends its customer to. Publishing one for
// an invoice that cannot be paid is the failure that matters: the DF-e would
// redirect somebody to a page whose only message is that there is nothing to do
// — or, for a draft, to a 404, because checkout.load refuses drafts by design.
func TestCheckoutURLIsPublishedOnlyForAPayableInvoice(t *testing.T) {
	links := services.NewPayLink(testLinkSecret, testLinkBaseURL)
	today := brcal.New(2026, time.March, 15)

	cases := []struct {
		name string
		inv  *billing.Invoice
		want bool
	}{
		{"open and owed", testInvoice(billing.InvoiceOpen, 35000, true), true},
		{"draft 404s behind the link", testInvoice(billing.InvoiceDraft, 35000, true), false},
		{"paid has nothing to collect", testInvoice(billing.InvoicePaid, 35000, true), false},
		{"void was withdrawn", testInvoice(billing.InvoiceVoid, 35000, true), false},
		{"free plan owes zero", testInvoice(billing.InvoiceOpen, 0, true), false},
		{"test mode has no rail", testInvoice(billing.InvoiceOpen, 35000, false), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := newInvoiceResponse(tc.inv, nil, today, links)
			got := out.CheckoutURL != ""
			if got != tc.want {
				t.Fatalf("checkout_url present = %v, want %v (got %q)", got, tc.want, out.CheckoutURL)
			}
		})
	}
}

// The URL has to be one the checkout route can actually parse. A response that
// carries a well-formed-looking token the server then rejects is worse than no
// token, because the failure lands on the customer rather than in CI.
func TestThePublishedCheckoutURLParsesBackToItsInvoice(t *testing.T) {
	links := services.NewPayLink(testLinkSecret, testLinkBaseURL)
	inv := testInvoice(billing.InvoiceOpen, 35000, true)

	out := newInvoiceResponse(inv, nil, brcal.New(2026, time.March, 15), links)
	if out.CheckoutURL == "" {
		t.Fatal("no checkout_url on a payable invoice")
	}

	prefix := testLinkBaseURL + "/"
	if !strings.HasPrefix(out.CheckoutURL, prefix) {
		t.Fatalf("checkout_url = %q, want it under %q", out.CheckoutURL, prefix)
	}

	org, livemode, invoiceID, err := links.Parse(strings.TrimPrefix(out.CheckoutURL, prefix))
	if err != nil {
		t.Fatalf("the URL we published does not verify: %v", err)
	}
	if org != inv.OrganizationID || livemode != inv.Livemode || invoiceID != inv.ID {
		t.Errorf("token addresses (%s, %v, %s), want (%s, %v, %s)",
			org, livemode, invoiceID, inv.OrganizationID, inv.Livemode, inv.ID)
	}
}

// Two deployments publish no URL, and they must be indistinguishable on the
// wire: one with no CHECKOUT_LINK_SECRET, and one whose routes were never
// mounted at all (router.checkoutMounted leaves handlers.links nil). A consumer
// branches on the field being absent, and that is the only branch it needs.
func TestNoCheckoutURLWithoutAConfiguredLinkSigner(t *testing.T) {
	inv := testInvoice(billing.InvoiceOpen, 35000, true)
	today := brcal.New(2026, time.March, 15)

	if out := newInvoiceResponse(inv, nil, today, nil); out.CheckoutURL != "" {
		t.Errorf("nil signer published %q", out.CheckoutURL)
	}
	if out := newInvoiceResponse(inv, nil, today, services.NewPayLink("", testLinkBaseURL)); out.CheckoutURL != "" {
		t.Errorf("empty secret published %q", out.CheckoutURL)
	}
	// Configured secret, no base URL: tokens can be signed but there is nowhere to
	// send anybody, so the field stays absent rather than becoming a bare token.
	if out := newInvoiceResponse(inv, nil, today, services.NewPayLink(testLinkSecret, "")); out.CheckoutURL != "" {
		t.Errorf("missing base URL published %q", out.CheckoutURL)
	}
}
