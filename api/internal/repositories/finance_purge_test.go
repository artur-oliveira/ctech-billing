package repositories

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/space"
)

// notSpaceScoped is every table that PurgeSpace does not clear as a finance
// table, with the reason. ADR 0026: "a missing TTL or a missing purge path" is
// a review failure, so a new table must land in exactly one of the two lists.
// A table whose rows live in partitions other than S (cards: S#CARD#{id},
// imports: S#IMPORT#{id}, byok: HOOK#{id}) also needs its own step in
// PurgeSpace, as the S#ACCOUNT# entries have.
var notSpaceScoped = map[string]string{
	TableOrganizations:  "merchant tenants (ADR 0007) and applications; a workspace with a tenant blocks erasure (billing.merchant_tenant), so none is purged until a tenant deletion flow exists",
	TableCredentials:    "belongs to a merchant tenant, which blocks erasure",
	TableCustomers:      "tenant-zero customers are anonymized in place by services.Eraser (ADR 0009); merchants' customers are their controller's",
	TableProducts:       "catalogue, no person",
	TablePrices:         "catalogue, no person",
	TableSubscriptions:  "canceled by services.Eraser and retained: it explains invoices (ADR 0009)",
	TableInvoices:       "fiscal documents, retained (ADR 0009)",
	TableUsage:          "level rows of USER_/ORG_ refs are deleted by LevelRepository.EraseRef; raw usage records expire in 24 months and carry no person",
	TableWebhooks:       "tenant endpoints belong to a merchant tenant (blocks erasure); events and deliveries expire in 90 days",
	TableErasureState:   "the saga's own state and tombstones",
	TableTenantClosures: "closure requests and flags of a merchant tenant; not a finance space",
	TableAudit:          "purged under S by PurgeSpace, outside financeTables; a surviving space's audit keeps ADR 0009's five-year TTL",
	TableIdempotency:    "purged under S by PurgeSpace, outside financeTables",
}

func TestEveryTableIsClassifiedForThePurge(t *testing.T) {
	for _, name := range TableNames() {
		_, other := notSpaceScoped[name]
		purged := slices.Contains(financeTables, name)
		if other == purged {
			t.Errorf("table %q must be in exactly one of financeTables (purged under the space) or notSpaceScoped (with the reason it is not)", name)
		}
	}
	for _, name := range financeTables {
		if !slices.Contains(TableNames(), name) {
			t.Errorf("financeTables names %q, which is not a table", name)
		}
	}
}

func TestPurgeSpaceRefusesTheZeroSpace(t *testing.T) {
	// A nil client would panic on any call, so reaching DynamoDB fails loudly.
	if _, err := (&SpacePurger{}).PurgeSpace(context.Background(), space.ResolvedSpace{}); !errors.Is(err, space.ErrNoSpace) {
		t.Fatalf("err = %v, want space.ErrNoSpace", err)
	}
}
