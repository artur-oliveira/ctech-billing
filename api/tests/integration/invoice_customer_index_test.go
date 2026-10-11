//go:build integration

package integration

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// createInvoice writes a DRAFT invoice with two lines for customerID, through
// the repository's own Create.
func createInvoice(t *testing.T, invoices *repositories.InvoiceRepository, orgID string, livemode bool, invoiceID, customerID, subscriptionID string) *billing.Invoice {
	t.Helper()
	inv := &billing.Invoice{
		ID:             invoiceID,
		OrganizationID: orgID,
		Livemode:       livemode,
		CustomerID:     customerID,
		SubscriptionID: subscriptionID,
		Status:         billing.InvoiceDraft,
		Period:         marchPeriod(),
		Currency:       billing.CurrencyBRL,
		Total:          4990,
		Subtotal:       4990,
	}
	items := []billing.InvoiceItem{
		{Description: "Plano", Period: marchPeriod(), Quantity: 1, UnitAmount: 2990, Amount: 2990},
		{Description: "Extra", Period: marchPeriod(), Quantity: 1, UnitAmount: 2000, Amount: 2000},
	}
	if err := invoices.Create(ctxT(t), inv, items, "", now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

// customerInvoicePartition reads a customer's partition on the invoices
// table's customer-index directly — what the index holds, not what any read
// path makes of it.
func customerInvoicePartition(t *testing.T, orgID string, livemode bool, customerID string) []map[string]types.AttributeValue {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableInvoices)
	res, err := base.Query(ctxT(t), repositories.QueryOpts{
		IndexName: repositories.IndexCustomer,
		PKField:   "customer_pk",
		PK:        repositories.LookupCustomerInvoicesPK(orgID, livemode, customerID),
		Limit:     500,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Items
}

func customerInvoiceIDs(t *testing.T, orgID string, livemode bool, customerID string) []string {
	t.Helper()
	items := customerInvoicePartition(t, orgID, livemode, customerID)
	out := make([]string, 0, len(items))
	for _, item := range items {
		v, _ := item["id"].(*types.AttributeValueMemberS)
		if v == nil {
			t.Fatalf("an index row with no id: %v", item["sk"])
		}
		out = append(out, v.Value)
	}
	return out
}

// stripCustomerPK makes an invoice look like a row written before
// customer-index existed, which is what every production row is until the
// backfill runs.
func stripCustomerPK(t *testing.T, orgID string, livemode bool, invoiceID string) {
	t.Helper()
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableInvoices)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(orgID, livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.InvoiceSK(invoiceID)},
		},
		UpdateExpression: aws.String("REMOVE customer_pk"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A new invoice is in its customer's partition; its line rows — which share
// the INVOICE# prefix — are not; and an invoice of a subscription is still in
// its subscription's lookup partition too.
func TestANewInvoiceIsIndexedByItsCustomer(t *testing.T) {
	org := newOrg(t, true)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	customer := "cus_" + id.New()
	sub := "sub_" + id.New()
	a := createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), customer, sub)
	b := createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), customer, "")
	createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), "cus_"+id.New(), "")

	got := customerInvoiceIDs(t, org.ID, true, customer)
	if len(got) != 2 || !(got[0] == a.ID && got[1] == b.ID || got[0] == b.ID && got[1] == a.ID) {
		t.Fatalf("customer partition = %v, want [%s %s] and no line rows", got, a.ID, b.ID)
	}
	if none := customerInvoiceIDs(t, org.ID, false, customer); len(none) != 0 {
		t.Fatalf("test-mode partition holds live invoices: %v", none)
	}
	bySub, err := invoices.ListBySubscription(ctxT(t), org.ID, true, sub, 10)
	if err != nil || len(bySub) != 1 || bySub[0].ID != a.ID {
		t.Fatalf("subscription partition = %v, %v", bySub, err)
	}
}

// The backfill gives every pre-index invoice its customer partition, reports
// before it writes, touches no line row, and finds nothing on a second run.
func TestTheBackfillIndexesLegacyInvoicesOnce(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	customer := "cus_" + id.New()
	sub := "sub_" + id.New()
	legacy := []*billing.Invoice{
		createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), customer, sub),
		createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), customer, ""),
	}
	current := createInvoice(t, invoices, org.ID, true, id.NewWithPrefix(id.PrefixInvoice), customer, "")
	for _, inv := range legacy {
		stripCustomerPK(t, org.ID, true, inv.ID)
	}
	if got := customerInvoiceIDs(t, org.ID, true, customer); len(got) != 1 || got[0] != current.ID {
		t.Fatalf("precondition: customer partition = %v, want only %s", got, current.ID)
	}

	dry, err := invoices.BackfillCustomerIndex(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Written != 0 || len(dry.Mismatched) != 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	mine := 0
	for _, m := range dry.Missing {
		if m.PK != repositories.TenantPK(org.ID, true) {
			continue
		}
		mine++
		if m.LookupPK != repositories.LookupCustomerInvoicesPK(org.ID, true, customer) {
			t.Fatalf("dry run would write %q on %s", m.LookupPK, m.SK)
		}
	}
	if mine != len(legacy) {
		t.Fatalf("dry run found %d of this tenant's invoices missing, want %d: %+v", mine, len(legacy), dry.Missing)
	}
	if got := customerInvoiceIDs(t, org.ID, true, customer); len(got) != 1 {
		t.Fatalf("a dry run wrote: customer partition = %v", got)
	}

	applied, err := invoices.BackfillCustomerIndex(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Written+applied.Raced != len(applied.Missing) || applied.Written < len(legacy) {
		t.Fatalf("apply: %+v, want at least %d written", applied, len(legacy))
	}
	if got := customerInvoiceIDs(t, org.ID, true, customer); len(got) != 3 {
		t.Fatalf("after apply: customer partition = %v, want 3 invoices and no line rows", got)
	}

	again, err := invoices.BackfillCustomerIndex(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Missing) != 0 {
		t.Fatalf("second run still finds %+v", again.Missing)
	}
	if again.Scanned < 3 {
		t.Fatalf("second run scanned %d invoices, want at least this test's 3", again.Scanned)
	}

	// The row is otherwise untouched, its subscription partition included.
	got, err := invoices.Get(ctx, org.ID, true, legacy[0].ID)
	if err != nil || got.CustomerID != customer || got.Status != billing.InvoiceDraft || got.Total != 4990 {
		t.Fatalf("after backfill: %+v, %v", got, err)
	}
	bySub, err := invoices.ListBySubscription(ctx, org.ID, true, sub, 10)
	if err != nil || len(bySub) != 1 || bySub[0].ID != legacy[0].ID {
		t.Fatalf("subscription partition after backfill = %v, %v", bySub, err)
	}
}
