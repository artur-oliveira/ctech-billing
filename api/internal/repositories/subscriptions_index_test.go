package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// A subscription row carries its customer's lookup partition from the moment it
// is written, so the per-customer read can be a key condition on lookup-index
// instead of a filtered walk of the tenant's whole SUB# range.
func TestASubscriptionRowIsInItsCustomersLookupPartition(t *testing.T) {
	s := &billing.Subscription{ID: "sub_1", OrganizationID: "org_1", Livemode: true,
		CustomerID: "cus_1", Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Anchor:     brcal.New(2026, time.March, 10)}
	row := subscriptionRowOf(s, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	if row.LookupPK != LookupCustomerSubscriptionsPK("org_1", true, "cus_1") {
		t.Fatalf("lookup_pk = %q", row.LookupPK)
	}
	if row.LookupPK != "org_1#live#CUSTOMER_SUBS#cus_1" {
		t.Fatalf("lookup_pk layout changed: %q — existing rows and the backfill depend on it", row.LookupPK)
	}
	if LookupCustomerSubscriptionsPK("org_1", true, "cus_1") == LookupCustomerSubscriptionsPK("org_1", false, "cus_1") {
		t.Fatal("a test-mode read must not reach live subscriptions")
	}
}

func TestTheSubscriptionsTableHasTheLookupIndex(t *testing.T) {
	s, ok := Schemas()[TableSubscriptions]
	if !ok {
		t.Fatal("no subscriptions schema")
	}
	for _, idx := range s.Indexes {
		if idx.Name == IndexLookup {
			if idx.HashKey != "lookup_pk" || idx.RangeKey != "sk" {
				t.Fatalf("lookup-index keys = %s/%s, want lookup_pk/sk", idx.HashKey, idx.RangeKey)
			}
			return
		}
	}
	t.Fatal("subscriptions declares no lookup-index")
}
