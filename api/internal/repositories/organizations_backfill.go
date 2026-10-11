package repositories

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// OrganizationLookupRow is one tenant row the account-organization backfill
// looked at. From is its lookup_pk now; To the one its link derives ("" when
// unlinked).
type OrganizationLookupRow struct {
	PK, SK                string
	OrganizationID        string
	Livemode              bool
	AccountOrganizationID string
	From, To              string
}

// OrganizationLookupReport is what one pass saw and did.
type OrganizationLookupReport struct {
	// Scanned counts tenant rows, both modes.
	Scanned int
	// Missing are linked tenants whose lookup_pk is absent or the legacy owner
	// key — what a dry run would write.
	Missing []OrganizationLookupRow
	// Written is how many of Missing this pass wrote (0 on a dry run).
	Written int
	// Raced counts writes whose row changed under us: re-run to see it.
	Raced int
	// Mismatched are linked tenants carrying a key that is neither the legacy
	// owner key nor theirs. Reported, never overwritten.
	Mismatched []OrganizationLookupRow
	// Conflicts are two or more tenants linked to one account organization in
	// one mode. None of them is written: choosing is an operator's decision.
	Conflicts []OrganizationLookupRow
	// Unlinked are tenants with no link that carried a console key: after this
	// deploy they are reachable only through M2M credentials (ADR 0028).
	Unlinked []OrganizationLookupRow
}

// BackfillAccountOrganizationLookup gives every linked tenant the
// {mode}#ACCOUNT_ORG#{id} key (ADR 0028), replacing the pre-ADR-0028 owner key.
// Dry run unless apply.
//
// An operator's migration, run from cmd/backfill-organization-lookup with the
// operator's own credentials: it Scans, which the service role is denied.
// Idempotent: each write is conditional on the row still carrying the key it
// was read with and the link it was read with.
func (r *OrganizationRepository) BackfillAccountOrganizationLookup(ctx context.Context, apply bool) (OrganizationLookupReport, error) {
	var rep OrganizationLookupReport
	rows, err := r.scanLookups(ctx)
	if err != nil {
		return rep, err
	}
	rep.Scanned = len(rows)

	byKey := map[string][]OrganizationLookupRow{}
	for _, row := range rows {
		if row.AccountOrganizationID == "" {
			if row.From != "" {
				rep.Unlinked = append(rep.Unlinked, row)
			}
			continue
		}
		row.To = LookupOrganizationAccountPK(row.Livemode, row.AccountOrganizationID)
		byKey[row.To] = append(byKey[row.To], row)
	}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		group := byKey[key]
		if len(group) > 1 {
			rep.Conflicts = append(rep.Conflicts, group...)
			continue
		}
		row := group[0]
		switch {
		case row.From == row.To:
			continue
		// {mode}#OWNER#{user} is the pre-ADR-0028 console key (ADR 0011), which
		// the console no longer reads; the backfill is the only code that knows it.
		case row.From != "" && !strings.HasPrefix(row.From, Mode(row.Livemode)+"#OWNER#"):
			rep.Mismatched = append(rep.Mismatched, row)
			continue
		}
		rep.Missing = append(rep.Missing, row)
		if !apply {
			continue
		}
		wrote, err := r.setAccountLookup(ctx, row)
		if err != nil {
			return rep, err
		}
		if wrote {
			rep.Written++
		} else {
			rep.Raced++
		}
	}
	return rep, nil
}

// organizationLookupProjection is what the backfill reads of a tenant row.
type organizationLookupProjection struct {
	PK                    string `dynamodbav:"pk"`
	SK                    string `dynamodbav:"sk"`
	ID                    string `dynamodbav:"id"`
	Livemode              bool   `dynamodbav:"livemode"`
	AccountOrganizationID string `dynamodbav:"account_organization_id"`
	LookupPK              string `dynamodbav:"lookup_pk"`
}

func (r *OrganizationRepository) scanLookups(ctx context.Context) ([]OrganizationLookupRow, error) {
	var out []OrganizationLookupRow
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.ScanRaw(ctx, &dynamodb.ScanInput{
			FilterExpression:         aws.String("sk = :org"),
			ProjectionExpression:     aws.String("pk, sk, #id, livemode, account_organization_id, lookup_pk"),
			ExpressionAttributeNames: map[string]string{"#id": "id"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":org": &types.AttributeValueMemberS{Value: OrganizationSK()},
			},
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, fmt.Errorf("scan organizations: %w", err)
		}
		rows, err := DecodeItems[organizationLookupProjection](res.Items)
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			out = append(out, OrganizationLookupRow{
				PK: p.PK, SK: p.SK, OrganizationID: p.ID, Livemode: p.Livemode,
				AccountOrganizationID: p.AccountOrganizationID, From: p.LookupPK,
			})
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

func (r *OrganizationRepository) setAccountLookup(ctx context.Context, row OrganizationLookupRow) (bool, error) {
	cond := "attribute_exists(pk) AND account_organization_id = :a AND attribute_not_exists(lookup_pk)"
	values := map[string]types.AttributeValue{
		":l": &types.AttributeValueMemberS{Value: row.To},
		":a": &types.AttributeValueMemberS{Value: row.AccountOrganizationID},
	}
	if row.From != "" {
		cond = "attribute_exists(pk) AND account_organization_id = :a AND lookup_pk = :old"
		values[":old"] = &types.AttributeValueMemberS{Value: row.From}
	}
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: row.PK},
			"sk": &types.AttributeValueMemberS{Value: row.SK},
		},
		UpdateExpression:          aws.String("SET lookup_pk = :l"),
		ConditionExpression:       aws.String(cond),
		ExpressionAttributeValues: values,
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set lookup_pk on %s/%s: %w", row.PK, row.SK, err)
	}
	return true, nil
}
