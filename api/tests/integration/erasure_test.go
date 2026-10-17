//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// lockSubject applies a user.locked for sub, exactly as the saga consumer does.
func lockSubject(t *testing.T, sub string) {
	t.Helper()
	store := erasure.NewStore(testDB, testCfg.TablePrefix, 0)
	if _, err := store.Apply(ctxT(t), erasure.SubKey(sub), erasure.Message{
		Version: erasure.Version, Type: erasure.TypeLocked, RequestID: "req_" + id.New(), Sub: sub,
		Scope: erasure.ScopeAccount, Services: []string{"billing"}, IssuedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 3.
func TestALockedPersonIsRefusedWithATokenThatIsStillValid(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)
	if res := e.console(t, "/v1.0/portal/session", token, ""); res.status != http.StatusOK {
		t.Fatalf("before the lock: %d %s", res.status, res.body)
	}

	lockSubject(t, e.userID)

	res := e.console(t, "/v1.0/portal/session", token, "")
	if res.status != http.StatusForbidden || problemType(t, res.body) != problem.TypeAccountLocked {
		t.Fatalf("after the lock: %d %s, want 403 %s", res.status, res.body, problem.TypeAccountLocked)
	}
	// A service token is not a person: a user's erasure does not lock the M2M surface.
	m2m := e.token(t, e.client, "", middleware.ScopeProductsRead)
	if res := e.do(t, http.MethodGet, "/v1.0/products", m2m, "", ""); res.status != http.StatusOK {
		t.Fatalf("M2M after a user's lock: %d %s", res.status, res.body)
	}
}

func newSubscriptionWithMetadata(t *testing.T, org *billing.Organization, customerID, priceID string) *billing.Subscription {
	t.Helper()
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: org.Livemode,
		CustomerID: customerID, Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
		Anchor:     brcal.New(2026, time.March, 1),
		Metadata:   billing.Metadata{"crm_contact": "pessoa@example.com"},
	}
	item := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: org.Livemode,
		SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1,
	}
	if err := repositories.NewSubscriptionRepository(testDB, testCfg).
		Create(ctxT(t), sub, []billing.SubscriptionItem{item}, now()); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestListByCustomerReadsOneCustomerOnlyAndMetadataClears(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	other := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Outra Pessoa", Email: "outra@example.com",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, other, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	mine := newSubscriptionWithMetadata(t, e.org, e.customer.ID, e.priceID)
	newSubscriptionIn(t, e.org, other.ID, e.priceID, billing.SubscriptionActive)
	newSubscriptionInvoice(t, e.org, mine)
	newInvoiceFor(t, e.org, e.customer.ID)
	newInvoiceFor(t, e.org, other.ID)

	invoices, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices = %d, %v; want this customer's 2 and none of the other's", len(invoices), err)
	}
	for _, inv := range invoices {
		if inv.CustomerID != e.customer.ID {
			t.Fatalf("invoice %s belongs to %s", inv.ID, inv.CustomerID)
		}
	}

	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	got, err := subs.ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(got) != 1 || got[0].ID != mine.ID || len(got[0].Metadata) == 0 {
		t.Fatalf("subscriptions = %+v, %v; want only %s, with its metadata", got, err, mine.ID)
	}
	if err := subs.ClearMetadata(ctx, &got[0], now()); err != nil {
		t.Fatal(err)
	}
	after, err := subs.Get(ctx, e.org.ID, true, mine.ID)
	if err != nil || len(after.Metadata) != 0 {
		t.Fatalf("metadata after clearing = %v, %v", after.Metadata, err)
	}
}

// R22: the indexes are sparse. A row written before customer-index existed and
// not backfilled is invisible to ListByCustomer, so an open invoice on it would
// not block an erasure. This test pins that fact; the control is the backfill
// gate in Task 11, not code.
func TestAnInvoiceWithoutItsCustomerKeyIsInvisibleToTheIndex(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	removeAttr(t, "invoices", repositories.TenantPK(e.org.ID, true), repositories.InvoiceSK(inv.ID), "customer_pk")

	got, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("invoices = %d, %v; a row without customer_pk must not appear (that is what the backfill fixes)", len(got), err)
	}
}

// removeAttr drops one attribute from an item, to reproduce a row written before
// an index's key existed.
func removeAttr(t *testing.T, logicalTable, pk, sk, attr string) {
	t.Helper()
	if _, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, logicalTable)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:         aws.String("REMOVE #a"),
		ExpressionAttributeNames: map[string]string{"#a": attr},
	}); err != nil {
		t.Fatal(err)
	}
}
