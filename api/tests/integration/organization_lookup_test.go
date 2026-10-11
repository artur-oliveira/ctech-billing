//go:build integration

package integration

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
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
	setLookup(t, org, repositories.LookupOrganizationOwnerPK(org.Livemode, org.OwnerUserID), "")
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

func TestAnUnlinkedTenantWithAnOwnerHasNoConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true) // owner, no link
	if _, err := orgRepo().GetByOwner(ctx, org.OwnerUserID, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("GetByOwner = %v: a new tenant must not be reachable by its stored owner", err)
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
	if _, err := orgRepo().GetByOwner(ctx, owner, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("GetByOwner = %v", err)
	}
	got, err := orgRepo().Get(ctx, org.ID, true)
	if err != nil || got.OwnerUserID != owner {
		t.Fatalf("stored owner = %+v, %v", got, err)
	}
}
