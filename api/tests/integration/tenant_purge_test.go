//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func putRaw(t *testing.T, logical string, attrs map[string]string) {
	t.Helper()
	item := map[string]types.AttributeValue{}
	for k, v := range attrs {
		item[k] = &types.AttributeValueMemberS{Value: v}
	}
	if _, err := testDB.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(repositories.TableName(testCfg, logical)), Item: item,
	}); err != nil {
		t.Fatal(err)
	}
}

func rowExists(t *testing.T, logical, pk, sk string) bool {
	t.Helper()
	out, err := testDB.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(repositories.TableName(testCfg, logical)), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(out.Item) > 0
}

var tenantPartitionTables = []string{"credentials", "customers", "products", "prices", "subscriptions", "invoices", "webhooks", "audit", "idempotency"}

func tenantRows(t *testing.T, org *billing.Organization) int {
	t.Helper()
	pk := repositories.TenantPK(org.ID, org.Livemode)
	n := rowsUnder(t, "organizations", pk)
	for _, tb := range tenantPartitionTables {
		n += rowsUnder(t, tb, pk)
	}
	return n
}

// seedTenantExtras adds what an API-driven tenant has not got in a test: a BYOK
// charge attempt with its reference pointer, a payout with its reference marker,
// and the BYOK counters.
func seedTenantExtras(t *testing.T, org *billing.Organization) {
	t.Helper()
	pk := repositories.TenantPK(org.ID, org.Livemode)
	putRaw(t, "invoices", map[string]string{"pk": pk, "sk": "INVOICE#inv_x#ATTEMPT#0001", "provider": "inter", "charge_ref": "ref-" + org.ID})
	putRaw(t, "byok", map[string]string{"pk": repositories.ChargeRefPK("inter", org.Livemode, "ref-"+org.ID), "sk": "CHARGEREF"})
	putRaw(t, "invoices", map[string]string{"pk": pk, "sk": "PAYOUT#po_x", "reference": "wd-" + org.ID})
	putRaw(t, "invoices", map[string]string{"pk": repositories.LookupPayoutReferencePK(org.ID, org.Livemode, "wd-"+org.ID), "sk": "REF"})
	putRaw(t, "byok", map[string]string{"pk": "BYOKCOUNT#" + org.ID, "sk": "2026-10"})
	putRaw(t, "byok", map[string]string{"pk": "BYOKSUBS#" + org.ID, "sk": "COUNT"})
}

func TestPurgeTenantErasesEveryPartitionAndPointerAndTouchesNoOtherTenant(t *testing.T) {
	ctx := ctxT(t)
	a, b := newAPI(t), newAPI(t) // two merchants, each with a live tenant, a credential, a price
	newInvoiceFor(t, a.org, "cus_a")
	newInvoiceFor(t, b.org, "cus_b")
	seedTenantExtras(t, a.org)
	seedTenantExtras(t, b.org)
	purger := repositories.NewTenantPurger(testDB, testCfg)
	guard := "CLIENT#" + a.client
	if !rowExists(t, "credentials", guard, "GUARD") {
		t.Fatalf("setup: %s has no guard row", a.client)
	}
	beforeB := tenantRows(t, b.org)

	dry, err := purger.PurgeTenant(ctx, a.org, true)
	if err != nil || dry[repositories.TableOrganizations] != 1 || dry["charge_refs"] != 1 || dry["payout_refs"] != 1 || dry["credential_guards"] != 1 {
		t.Fatalf("dry run = %v, %v", dry, err)
	}
	if tenantRows(t, a.org) == 0 {
		t.Fatal("a dry run deleted something")
	}

	rep, err := purger.PurgeTenant(ctx, a.org, false)
	if err != nil {
		t.Fatalf("purge: %v (%v)", err, rep)
	}
	if n := tenantRows(t, a.org); n != 0 {
		t.Fatalf("%d rows left under the tenant partition", n)
	}
	for name, ok := range map[string]bool{
		"the credential guard": rowExists(t, "credentials", guard, "GUARD"),
		"the charge reference": rowExists(t, "byok", repositories.ChargeRefPK("inter", a.org.Livemode, "ref-"+a.org.ID), "CHARGEREF"),
		"the payout marker":    rowExists(t, "invoices", repositories.LookupPayoutReferencePK(a.org.ID, a.org.Livemode, "wd-"+a.org.ID), "REF"),
		"the charge counter":   rowExists(t, "byok", "BYOKCOUNT#"+a.org.ID, "2026-10"),
		"the subs counter":     rowExists(t, "byok", "BYOKSUBS#"+a.org.ID, "COUNT"),
	} {
		if ok {
			t.Errorf("%s survived", name)
		}
	}
	if got := tenantRows(t, b.org); got != beforeB {
		t.Fatalf("another tenant has %d rows, had %d: the purge reached into it", got, beforeB)
	}
	if !rowExists(t, "byok", repositories.ChargeRefPK("inter", b.org.Livemode, "ref-"+b.org.ID), "CHARGEREF") {
		t.Fatal("another tenant's charge reference was deleted")
	}

	again, err := purger.PurgeTenant(ctx, a.org, false)
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range again {
		if n != 0 {
			t.Errorf("second run deleted %d from %s: not idempotent", n, k)
		}
	}
}

func TestDeletingAnOrganizationsApplicationsLeavesOthersAlone(t *testing.T) {
	tz, a, b := "org_"+newSpaceOrgID(), newSpaceOrgID(), newSpaceOrgID()
	r := appRepo(t, tz)
	if _, _, err := apply(t, r, tz, a, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := apply(t, r, tz, b, true); err != nil {
		t.Fatal(err)
	}
	n, err := r.DeleteForOrganization(ctxT(t), a)
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v; want one application", n, err)
	}
	if cur, _ := r.Current(ctxT(t), a); cur != nil {
		t.Fatalf("the application of %s survived: %+v", a, cur)
	}
	if cur, err := r.Current(ctxT(t), b); err != nil || cur == nil {
		t.Fatalf("another workspace's application was touched: %+v %v", cur, err)
	}
}

// What the online purge cannot enumerate (usage, generation-key and level
// sub-partitions) is found and removed by the operator sweep, and a neighbour
// tenant's sub-partitions are not touched.
func TestTheResidueSweepFindsSubPartitionsUnderTheClosedTenantOnly(t *testing.T) {
	ctx := ctxT(t)
	a, b := newAPI(t), newAPI(t)
	purger := repositories.NewTenantPurger(testDB, testCfg)
	seed := func(org *billing.Organization) []string {
		base := repositories.TenantPK(org.ID, org.Livemode)
		pks := []string{base + "#USAGE#si_1#2026-10-01", base + "#GEN#sub_1:2026-10", base + "#LEVEL_LATEST#ref-1#finance_spaces", base + "#LEVEL_KEY#k1"}
		tables := []string{"usage", "invoices", "usage", "usage"}
		for i, pk := range pks {
			putRaw(t, tables[i], map[string]string{"pk": pk, "sk": "x"})
		}
		return pks
	}
	aPKs, bPKs := seed(a.org), seed(b.org)

	dry, err := purger.SweepResidue(ctx, a.org.ID, false)
	if err != nil || len(dry.Found) < len(aPKs) {
		t.Fatalf("dry run found %d, want at least %d: %v", len(dry.Found), len(aPKs), err)
	}
	if !rowExists(t, "usage", aPKs[0], "x") {
		t.Fatal("a dry run deleted something")
	}
	if _, err := purger.SweepResidue(ctx, a.org.ID, true); err != nil {
		t.Fatal(err)
	}
	for i, pk := range aPKs {
		tb := []string{"usage", "invoices", "usage", "usage"}[i]
		if rowExists(t, tb, pk, "x") {
			t.Errorf("%s survived the sweep", pk)
		}
	}
	for i, pk := range bPKs {
		tb := []string{"usage", "invoices", "usage", "usage"}[i]
		if !rowExists(t, tb, pk, "x") {
			t.Errorf("another tenant's %s was deleted", pk)
		}
	}
}

// The purge walks the invoice partition a page (1000 rows) at a time: a tenant
// with more than one page of attempts and payouts loses every row and every
// pointer they name, none skipped at a page edge.
func TestPurgeTenantWorksAPartitionLargerThanOnePage(t *testing.T) {
	a := newAPI(t)
	pk := repositories.TenantPK(a.org.ID, a.org.Livemode)
	const n = 1100
	for i := 0; i < n; i++ {
		ref := fmt.Sprintf("ref-%s-%04d", a.org.ID, i)
		putRaw(t, "invoices", map[string]string{"pk": pk, "sk": fmt.Sprintf("INVOICE#inv_%04d#ATTEMPT#0001", i), "provider": "inter", "charge_ref": ref})
		putRaw(t, "byok", map[string]string{"pk": repositories.ChargeRefPK("inter", a.org.Livemode, ref), "sk": "CHARGEREF"})
	}
	rep, err := repositories.NewTenantPurger(testDB, testCfg).PurgeTenant(ctxT(t), a.org, false)
	if err != nil || rep["charge_refs"] != n {
		t.Fatalf("charge_refs = %d, want %d (%v)", rep["charge_refs"], n, err)
	}
	if left := tenantRows(t, a.org); left != 0 {
		t.Fatalf("%d rows left", left)
	}
	for _, i := range []int{0, 999, 1000, n - 1} {
		if rowExists(t, "byok", repositories.ChargeRefPK("inter", a.org.Livemode, fmt.Sprintf("ref-%s-%04d", a.org.ID, i)), "CHARGEREF") {
			t.Fatalf("pointer %d survived", i)
		}
	}
}
