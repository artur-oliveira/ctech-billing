//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// subscribeCustomer creates one subscription for customerID on the fixture's
// price.
func subscribeCustomer(t *testing.T, f *catalogFixture, price *billing.Price, customerID string) *billing.Subscription {
	t.Helper()
	sub, _, err := f.subber.Subscribe(ctxT(t), services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: customerID, Items: []services.SubscribeItem{{PriceID: price.ID}}, Actor: "t"}, now())
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// lookupSubscriptionIDs reads a customer's lookup partition on the
// subscriptions table directly — what the index holds, not what any read path
// makes of it.
func lookupSubscriptionIDs(t *testing.T, orgID, customerID string) []string {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableSubscriptions)
	res, err := base.Query(ctxT(t), repositories.QueryOpts{
		IndexName: repositories.IndexLookup,
		PKField:   "lookup_pk",
		PK:        repositories.LookupCustomerSubscriptionsPK(orgID, true, customerID),
		Limit:     100,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(res.Items))
	for _, item := range res.Items {
		v, _ := item["id"].(*types.AttributeValueMemberS)
		if v == nil {
			t.Fatalf("an index row with no id: %v", item["sk"])
		}
		out = append(out, v.Value)
	}
	return out
}

// stripLookup makes a subscription look like a row written before the index
// existed, which is what every production row is until the backfill runs.
func stripLookup(t *testing.T, orgID, subID string) {
	t.Helper()
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableSubscriptions)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(orgID, true)},
			"sk": &types.AttributeValueMemberS{Value: repositories.SubscriptionSK(subID)},
		},
		UpdateExpression: aws.String("REMOVE lookup_pk"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A new subscription is in its customer's lookup partition, and its item rows —
// which share the SUB# prefix — are not.
func TestANewSubscriptionIsIndexedByItsCustomer(t *testing.T) {
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	customer := "cus_" + id.New()
	a := subscribeCustomer(t, f, price, customer)
	b := subscribeCustomer(t, f, price, customer)
	subscribeCustomer(t, f, price, "cus_"+id.New())

	got := lookupSubscriptionIDs(t, f.org.ID, customer)
	if len(got) != 2 || !(got[0] == a.ID && got[1] == b.ID || got[0] == b.ID && got[1] == a.ID) {
		t.Fatalf("lookup partition = %v, want [%s %s]", got, a.ID, b.ID)
	}
}

// The backfill gives every pre-index subscription its lookup partition, reports
// before it writes, and finds nothing on a second run.
func TestTheBackfillIndexesLegacySubscriptionsOnce(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	customer := "cus_" + id.New()
	legacy := []*billing.Subscription{
		subscribeCustomer(t, f, price, customer),
		subscribeCustomer(t, f, price, customer),
	}
	current := subscribeCustomer(t, f, price, customer)
	for _, s := range legacy {
		stripLookup(t, f.org.ID, s.ID)
	}
	if got := lookupSubscriptionIDs(t, f.org.ID, customer); len(got) != 1 || got[0] != current.ID {
		t.Fatalf("precondition: lookup partition = %v, want only %s", got, current.ID)
	}

	dry, err := f.subs.BackfillCustomerLookup(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Missing) != len(legacy) || dry.Written != 0 {
		t.Fatalf("dry run: %+v, want %d missing and nothing written", dry, len(legacy))
	}
	if got := lookupSubscriptionIDs(t, f.org.ID, customer); len(got) != 1 {
		t.Fatalf("a dry run wrote: lookup partition = %v", got)
	}

	applied, err := f.subs.BackfillCustomerLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Written != len(legacy) {
		t.Fatalf("apply: %+v, want %d written", applied, len(legacy))
	}
	if got := lookupSubscriptionIDs(t, f.org.ID, customer); len(got) != 3 {
		t.Fatalf("after apply: lookup partition = %v, want 3 subscriptions", got)
	}

	again, err := f.subs.BackfillCustomerLookup(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Missing) != 0 {
		t.Fatalf("second run still finds %+v", again.Missing)
	}
	if again.Scanned < 3 {
		t.Fatalf("second run scanned %d subscriptions, want at least this test's 3", again.Scanned)
	}

	// The row is otherwise untouched: the backfill adds an index attribute, it
	// does not rewrite the subscription.
	got, err := f.subs.Get(context.Background(), f.org.ID, true, legacy[0].ID)
	if err != nil || got.CustomerID != customer || got.Status != legacy[0].Status {
		t.Fatalf("after backfill: %+v, %v", got, err)
	}
}
