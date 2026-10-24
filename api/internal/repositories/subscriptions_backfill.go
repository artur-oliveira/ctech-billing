package repositories

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// LookupBackfillRow is one subscription row the backfill found without its
// customer lookup partition.
type LookupBackfillRow struct {
	PK, SK   string
	LookupPK string
}

// LookupBackfillReport is what one pass of BackfillCustomerLookup saw and did.
type LookupBackfillReport struct {
	// Scanned counts subscription rows (not their `#ITEM#` rows) across every
	// tenant and mode.
	Scanned int
	// Missing are the rows with no lookup_pk — what a dry run would write.
	Missing []LookupBackfillRow
	// Written is how many of Missing this pass wrote (always 0 on a dry run).
	Written int
	// Raced counts rows whose write found a lookup_pk already there (or the row
	// gone) — another writer got there first, which is the same end state.
	Raced int
	// Mismatched are rows whose lookup_pk is not the one their own tenant and
	// customer derive. Reported, never overwritten: that is a bug to look at,
	// not a gap to fill.
	Mismatched []LookupBackfillRow
	// Unindexable are subscription rows with no customer_id, which no lookup
	// partition can name.
	Unindexable []LookupBackfillRow
}

// BackfillCustomerLookup gives every subscription row written before
// lookup-index existed its lookup_pk. Dry run unless apply.
//
// It is an operator's migration, run from cmd/backfill-subscription-lookup with
// the operator's own credentials — the one place in this package that Scans.
// The service role denies dynamodb:Scan (terraform/billing/iam.tf), and nothing
// on a request path calls this.
//
// Idempotent: each write is conditional on the row still having no lookup_pk,
// so a re-run, or a run racing a new Create, writes nothing twice. The row's
// other attributes, updated_at included, are left as they are — this adds an
// index key, it does not change the subscription.
func (r *SubscriptionRepository) BackfillCustomerLookup(ctx context.Context, apply bool) (LookupBackfillReport, error) {
	var rep LookupBackfillReport
	var start map[string]types.AttributeValue
	for {
		out, err := r.base.ScanRaw(ctx, &dynamodb.ScanInput{ // standards:allow backfill scan
			// Subscription rows only: the SUB# range minus its #ITEM# rows, which
			// carry no status.
			FilterExpression:     aws.String("begins_with(sk, :sub) AND attribute_exists(#status)"),
			ProjectionExpression: aws.String("pk, sk, organization_id, livemode, customer_id, lookup_pk"),
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":sub": &types.AttributeValueMemberS{Value: skSubscription},
			},
			ExclusiveStartKey: start,
		})
		if err != nil {
			return rep, fmt.Errorf("scan subscriptions: %w", err)
		}
		rows, err := DecodeItems[subscriptionLookupRow](out.Items)
		if err != nil {
			return rep, err
		}
		for _, row := range rows {
			rep.Scanned++
			want := LookupBackfillRow{PK: row.PK, SK: row.SK}
			if row.CustomerID == "" {
				rep.Unindexable = append(rep.Unindexable, want)
				continue
			}
			want.LookupPK = LookupCustomerSubscriptionsPK(row.OrganizationID, row.Livemode, row.CustomerID)
			switch {
			case row.LookupPK == want.LookupPK:
				continue
			case row.LookupPK != "":
				want.LookupPK = row.LookupPK + " (want " + want.LookupPK + ")"
				rep.Mismatched = append(rep.Mismatched, want)
				continue
			}
			rep.Missing = append(rep.Missing, want)
			if !apply {
				continue
			}
			wrote, err := r.setCustomerLookup(ctx, want)
			if err != nil {
				return rep, err
			}
			if wrote {
				rep.Written++
			} else {
				rep.Raced++
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return rep, nil
		}
		start = out.LastEvaluatedKey
	}
}

// subscriptionLookupRow is the projection the backfill reads.
type subscriptionLookupRow struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	OrganizationID string `dynamodbav:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"`
	CustomerID     string `dynamodbav:"customer_id"`
	LookupPK       string `dynamodbav:"lookup_pk"`
}

func (r *SubscriptionRepository) setCustomerLookup(ctx context.Context, row LookupBackfillRow) (bool, error) {
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: row.PK},
			"sk": &types.AttributeValueMemberS{Value: row.SK},
		},
		UpdateExpression:    aws.String("SET lookup_pk = :l"),
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_not_exists(lookup_pk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":l": &types.AttributeValueMemberS{Value: row.LookupPK},
		},
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set lookup_pk on %s/%s: %w", row.PK, row.SK, err)
	}
	return true, nil
}
