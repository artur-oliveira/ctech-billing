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

// cloneSubscriptions writes n more subscriptions shaped like proto for
// customerID, straight through the repository — cheaper than n Subscribes,
// which also price and invoice.
func cloneSubscriptions(t *testing.T, f *catalogFixture, proto *billing.Subscription, customerID string, n int) []string {
	t.Helper()
	items, err := f.subs.ListItems(ctxT(t), proto.OrganizationID, proto.Livemode, proto.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("items: %v, %v", items, err)
	}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s := *proto
		s.ID = id.NewWithPrefix(id.PrefixSubscription)
		s.CustomerID = customerID
		it := items[0]
		it.ID = id.NewWithPrefix(id.PrefixSubscriptionItm)
		it.SubscriptionID = s.ID
		if err := f.subs.Create(ctxT(t), &s, []billing.SubscriptionItem{it}, now()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	return ids
}

// The per-customer read is a key condition on lookup-index: a subscription
// whose row lacks lookup_pk — which only a pre-index row does — is invisible to
// it. Filtering the tenant partition would have found it.
func TestTheCustomerReadGoesThroughTheIndex(t *testing.T) {
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	customer := "cus_" + id.New()
	indexed := subscribeCustomer(t, f, price, customer)
	unindexed := subscribeCustomer(t, f, price, customer)
	stripLookup(t, f.org.ID, unindexed.ID)

	got, err := f.subs.ListByCustomer(ctxT(t), f.org.ID, true, customer, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != indexed.ID {
		t.Fatalf("ListByCustomer = %v, want only the indexed %s", subIDs(got), indexed.ID)
	}

	// Put the attribute back so no later test (or backfill run) inherits it.
	if _, err := f.subs.BackfillCustomerLookup(ctxT(t), true); err != nil {
		t.Fatal(err)
	}
}

// A customer with many subscriptions among many other customers' rows: every
// one of theirs comes back, past the index's own page, nobody else's does, in
// creation order, and the limit means what it says.
func TestACustomerWithManySubscriptionsAmongManyOthers(t *testing.T) {
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	proto := subscribeCustomer(t, f, price, "cus_"+id.New())
	for i := 0; i < 40; i++ {
		cloneSubscriptions(t, f, proto, "cus_"+id.New(), 1)
	}
	target := "cus_" + id.New()
	want := cloneSubscriptions(t, f, proto, target, 130) // more than one 100-row page
	for i := 0; i < 40; i++ {
		cloneSubscriptions(t, f, proto, "cus_"+id.New(), 1)
	}

	got, err := f.subs.ListByCustomer(ctxT(t), f.org.ID, true, target, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := subIDs(got)
	if len(ids) != len(want) {
		t.Fatalf("found %d subscriptions, want %d", len(ids), len(want))
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("[%d] = %s, want %s (creation order)", i, ids[i], want[i])
		}
	}
	for _, s := range got {
		if s.CustomerID != target || s.Status == "" {
			t.Fatalf("a row that is not this customer's subscription: %+v", s)
		}
	}

	capped, err := f.subs.ListByCustomer(ctxT(t), f.org.ID, true, target, 7)
	if err != nil || len(capped) != 7 || capped[0].ID != want[0] {
		t.Fatalf("limit 7: %v, %v", subIDs(capped), err)
	}

	none, err := f.subs.ListByCustomer(ctxT(t), f.org.ID, false, target, 0)
	if err != nil || len(none) != 0 {
		t.Fatalf("test mode reached live subscriptions: %v, %v", subIDs(none), err)
	}
}

func subIDs(subs []billing.Subscription) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.ID
	}
	return out
}
