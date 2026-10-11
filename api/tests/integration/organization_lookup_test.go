//go:build integration

package integration

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The console finds a tenant by the ctech-account organization it is linked to
// (ADR 0028), one per mode, and never chooses between two.

func orgRepo() *repositories.OrganizationRepository {
	return repositories.NewOrganizationRepository(testDB, testCfg)
}

// setLookup overwrites a tenant row's lookup_pk (and, when acct is not empty,
// its link) directly, the way rows written before this deploy look — or the
// way a mistaken manual fix would leave them.
func setLookup(t *testing.T, org *billing.Organization, lookup, acct string) {
	t.Helper()
	expr := "SET lookup_pk = :l"
	values := map[string]types.AttributeValue{":l": &types.AttributeValueMemberS{Value: lookup}}
	if acct != "" {
		expr += ", account_organization_id = :a"
		values[":a"] = &types.AttributeValueMemberS{Value: acct}
	}
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableOrganizations)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, org.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.OrganizationSK()},
		},
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeValues: values,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// legacyLookup gives a tenant the pre-ADR-0028 owner key, which is what every
// production row carries before the backfill.
func legacyLookup(t *testing.T, org *billing.Organization) {
	t.Helper()
	setLookup(t, org, repositories.Mode(org.Livemode)+"#OWNER#"+org.OwnerUserID, "")
}

func TestATenantIsFoundByItsAccountOrganizationInItsModeOnly(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)

	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID {
		t.Fatalf("live = %v, %v; want %s", got, err, org.ID)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, false); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("test mode: err = %v, want ErrNotFound", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, newSpaceOrgID(), true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("unknown organization: err = %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, "", true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("empty id: err = %v", err)
	}
}

func TestLinkingWritesTheAccountOrganizationKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true) // unlinked, owner key (until Task 7)
	acct := newSpaceOrgID()
	if err := orgRepo().LinkAccountOrganization(ctx, org, acct, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID {
		t.Fatalf("after link = %v, %v", got, err)
	}
}

func TestAnAccountOrganizationNamesOneTenantPerMode(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	newLinkedOrg(t, true, acct)

	second := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "Second", Livemode: true,
		PayoutStatus: billing.PayoutEnabled, AccountOrganizationID: acct,
	}
	if err := orgRepo().Create(ctx, second, now()); !errors.Is(err, repositories.ErrAccountOrganizationTaken) {
		t.Fatalf("create on a taken key: err = %v", err)
	}
	other := newOrg(t, true)
	if err := orgRepo().LinkAccountOrganization(ctx, other, acct, "test", "req_1", now()); !errors.Is(err, repositories.ErrAccountOrganizationTaken) {
		t.Fatalf("link to a taken key: err = %v", err)
	}
	// The same account organization in the other mode is a different key.
	if newLinkedOrg(t, false, acct) == nil {
		t.Fatal("unreachable")
	}
}

func TestTwoTenantsOnOneKeyAreRefusedNotChosen(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	newLinkedOrg(t, true, acct)
	stray := newOrg(t, true)
	setLookup(t, stray, repositories.LookupOrganizationAccountPK(true, acct), acct)

	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrAccountOrganizationAmbiguous) {
		t.Fatalf("err = %v, want ErrAccountOrganizationAmbiguous", err)
	}
}

func TestSetOwnerNeverReplacesTheAccountOrganizationKey(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "API only", Livemode: true,
		PayoutStatus: billing.PayoutEnabled, AccountOrganizationID: acct,
	}
	if err := orgRepo().Create(ctx, org, now()); err != nil {
		t.Fatal(err)
	}
	owner := "usr_" + id.New()
	if err := orgRepo().SetOwner(ctx, org, owner, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID || got.OwnerUserID != owner {
		t.Fatalf("after SetOwner = %+v, %v", got, err)
	}
}

func TestSetOwnerWritesNoConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "API only", Livemode: true, PayoutStatus: billing.PayoutEnabled,
	}
	if err := orgRepo().Create(ctx, org, now()); err != nil {
		t.Fatal(err)
	}
	owner := "usr_" + id.New()
	if err := orgRepo().SetOwner(ctx, org, owner, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := orgRepo().Get(ctx, org.ID, true)
	if err != nil || got.OwnerUserID != owner {
		t.Fatalf("stored owner = %+v, %v", got, err)
	}
}

// rowOf finds one tenant's row in a backfill report list.
func rowOf(rows []repositories.OrganizationLookupRow, orgID string, live bool) *repositories.OrganizationLookupRow {
	for i := range rows {
		if rows[i].OrganizationID == orgID && rows[i].Livemode == live {
			return &rows[i]
		}
	}
	return nil
}

func TestBackfillRewritesALegacyOwnerKeyToTheAccountOrganization(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)
	legacyLookup(t, org)
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("before: %v", err)
	}

	dry, err := orgRepo().BackfillAccountOrganizationLookup(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	r := rowOf(dry.Missing, org.ID, true)
	if r == nil || r.From != "live#OWNER#"+org.OwnerUserID ||
		r.To != repositories.LookupOrganizationAccountPK(true, acct) {
		t.Fatalf("dry run missing row = %+v", r)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatal("a dry run wrote")
	}

	if _, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := orgRepo().GetByAccountOrganization(ctx, acct, true); err != nil || got.ID != org.ID {
		t.Fatalf("after apply = %v, %v", got, err)
	}
	again, err := orgRepo().BackfillAccountOrganizationLookup(ctx, false)
	if err != nil || rowOf(again.Missing, org.ID, true) != nil {
		t.Fatalf("re-run still lists the row: %v", err)
	}
}

func TestBackfillReportsTwoTenantsOnOneOrganizationAndWritesNeither(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	a, b := newOrg(t, true), newOrg(t, true)
	setLookup(t, a, "live#OWNER#"+a.OwnerUserID, acct)
	setLookup(t, b, "live#OWNER#"+b.OwnerUserID, acct)

	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Conflicts, a.ID, true) == nil || rowOf(rep.Conflicts, b.ID, true) == nil {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a conflicting key was written: %v", err)
	}
}

func TestBackfillReportsAForeignKeyAndLeavesIt(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)
	setLookup(t, org, "live#SOMETHING#else", "")
	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Mismatched, org.ID, true) == nil || rowOf(rep.Missing, org.ID, true) != nil {
		t.Fatalf("mismatched = %+v", rep.Mismatched)
	}
}

func TestBackfillListsUnlinkedTenantsThatHadAConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	legacyLookup(t, org)
	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Unlinked, org.ID, true) == nil {
		t.Fatalf("unlinked = %+v", rep.Unlinked)
	}
}

// Spec § 10.6, first half: tenant zero is reachable by its owner and admins
// after the backfill; before it, by nobody (the owner fallback is gone).
func TestTenantZeroIsReachableByItsOwnerAndAdminsAfterTheBackfill(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	legacyLookup(t, e.org) // production before the backfill
	admin, member := "usr_"+id.New(), "usr_"+id.New()
	e.account.set(e.accountOrg, admin, "organization", "admin")
	e.account.set(e.accountOrg, member, "organization", "member")
	read := func(sub string) apiResponse {
		return e.console(t, "/v1.0/console/settings", e.token(t, sub, "sess_"+id.New(), middleware.ScopeOrganizationRead), "live")
	}

	if res := read(e.org.OwnerUserID); res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("owner before the backfill: %d %s", res.status, res.body)
	}
	if res := read(admin); res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("admin before the backfill: %d %s", res.status, res.body)
	}

	if _, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{e.org.OwnerUserID, admin} {
		if res := read(sub); res.status != http.StatusOK || !strings.Contains(string(res.body), e.org.ID) {
			t.Fatalf("%s after the backfill: %d %s", sub, res.status, res.body)
		}
	}
	if res := read(member); res.status != http.StatusForbidden {
		t.Fatalf("member after the backfill: %d %s", res.status, res.body)
	}
}

// Nobody reaches a tenant by owner_user_id alone: not as an owner of its
// organization while the row carries only the old key, and not as a stored
// owner who is no member of the organization. Two envs, so no membership
// answer is cached across the two halves.
func TestNobodyReachesATenantByOwnerUserIDAlone(t *testing.T) {
	read := func(e *apiEnv) apiResponse {
		token := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead)
		return e.console(t, "/v1.0/console/settings", token, "live")
	}

	e := newAPI(t)
	legacyLookup(t, e.org) // an un-backfilled row
	if res := read(e); res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("owner on a legacy-only row: %d %s", res.status, res.body)
	}

	f := newAPI(t)
	f.account.mu.Lock()
	delete(f.account.members, f.accountOrg+"|"+f.org.OwnerUserID)
	f.account.mu.Unlock()
	if res := read(f); res.status != http.StatusNotFound || !strings.Contains(string(res.body), "/problems/space-not-found") {
		t.Fatalf("stored owner who is not a member: %d %s", res.status, res.body)
	}
}
