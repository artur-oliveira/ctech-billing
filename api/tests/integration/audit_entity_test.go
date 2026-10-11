//go:build integration

package integration

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ListForEntity filters the tenant's whole audit range by entity_id. DynamoDB
// applies Limit to the rows it evaluates, before that filter, so an entity
// whose entries came after the first Limit rows of other entities' activity
// got an empty trail. Here 300 other entries precede the entity's 3 — more
// than one filtered Query reads in its page budget (Limit 20 x 10 calls).
func TestTheAuditTrailOfAnEntityPastTheFirstPageIsFound(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	pk := repositories.TenantPK(org.ID, true)
	table := aws.String(repositories.TableName(testCfg, repositories.TableAudit))
	put := func(sk, entityID string) {
		t.Helper()
		_, err := testDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: table, Item: map[string]types.AttributeValue{
			"pk":              &types.AttributeValueMemberS{Value: pk},
			"sk":              &types.AttributeValueMemberS{Value: sk},
			"id":              &types.AttributeValueMemberS{Value: sk[len("AUDIT#"):]},
			"organization_id": &types.AttributeValueMemberS{Value: org.ID},
			"livemode":        &types.AttributeValueMemberBOOL{Value: true},
			"entity_type":     &types.AttributeValueMemberS{Value: "invoice"},
			"entity_id":       &types.AttributeValueMemberS{Value: entityID},
			"action":          &types.AttributeValueMemberS{Value: "invoice.paid"},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 300 {
		put(fmt.Sprintf("AUDIT#a%04d", i), fmt.Sprintf("in_other_%d", i))
	}
	var want []string
	for i := range 3 {
		sk := fmt.Sprintf("AUDIT#z%04d", i)
		want = append(want, sk[len("AUDIT#"):])
		put(sk, "in_target")
	}

	trail, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctx, org.ID, true, "in_target", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(trail), len(want), trail)
	}
	for i, e := range trail {
		if e.ID != want[i] || e.EntityID != "in_target" {
			t.Fatalf("entry %d = %s/%s, want %s/in_target", i, e.ID, e.EntityID, want[i])
		}
	}

	// limit caps the trail at the newest entries, still oldest first.
	two, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctx, org.ID, true, "in_target", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 || two[0].ID != want[1] || two[1].ID != want[2] {
		t.Fatalf("limit 2 = %+v, want the last two of %v", two, want)
	}
}

// An entity whose entries are scattered through older tenant activity, with
// more entries than the limit: the newest limit come back, oldest first, even
// when one read returns more matches than the limit.
func TestTheAuditTrailKeepsTheNewestEntriesWhenScattered(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	pk := repositories.TenantPK(org.ID, true)
	table := aws.String(repositories.TableName(testCfg, repositories.TableAudit))
	var mine []string
	for i := range 400 {
		entity := fmt.Sprintf("in_other_%d", i)
		if i%7 == 3 {
			entity = "in_scattered"
			mine = append(mine, fmt.Sprintf("a%04d", i))
		}
		sk := fmt.Sprintf("AUDIT#a%04d", i)
		if _, err := testDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: table, Item: map[string]types.AttributeValue{
			"pk":        &types.AttributeValueMemberS{Value: pk},
			"sk":        &types.AttributeValueMemberS{Value: sk},
			"id":        &types.AttributeValueMemberS{Value: sk[len("AUDIT#"):]},
			"entity_id": &types.AttributeValueMemberS{Value: entity},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	trail, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctx, org.ID, true, "in_scattered", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := mine[len(mine)-10:]
	if len(trail) != len(want) {
		t.Fatalf("got %d entries, want %d", len(trail), len(want))
	}
	for i, e := range trail {
		if e.ID != want[i] {
			t.Fatalf("entry %d = %s, want %s (all: %v)", i, e.ID, want[i], want)
		}
	}
}
