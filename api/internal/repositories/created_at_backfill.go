package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// CreatedAtBackfillReport is what one pass of BackfillCreatedAt saw and did, per
// table.
type CreatedAtBackfillReport struct {
	// Missing are the rows with no created_at: what a dry run would write.
	Missing int
	// Written is how many of Missing this pass wrote (always 0 on a dry run).
	Written int
	// Raced counts rows whose write found a created_at already there, or the
	// row gone: another writer got there first, which is the same end state.
	Raced int
	// Unparseable are rows whose id carries no ULID time. Reported, never
	// guessed at.
	Unparseable []string
}

// BackfillCreatedAt writes created_at on the credit note rows and audit rows
// stored before it was written at all (see creditNoteRow). Dry run unless apply.
//
// The value is the one the read path already shows for such a row: the time
// inside the row's own ULID id (createdAtOrID). So the console does not change;
// the attribute just stops depending on the id being parseable.
//
// It is an operator's migration, run from cmd/backfill-created-at with the
// operator's own credentials: the service role denies dynamodb:Scan, and nothing
// on a request path calls this.
//
// Idempotent: each write is conditional on the row still having no created_at.
func (r *InvoiceRepository) BackfillCreatedAt(ctx context.Context, apply bool) (credits, audits CreatedAtBackfillReport, err error) {
	// Credit notes live under their invoice: INVOICE#<id>#CREDIT#<id>.
	credits, err = backfillCreatedAt(ctx, r.base, apply, "begins_with(sk, :p) AND contains(sk, :c)",
		map[string]types.AttributeValue{
			":p": &types.AttributeValueMemberS{Value: skInvoice},
			":c": &types.AttributeValueMemberS{Value: "#" + skCreditNote},
		}, rowIDFromSK("#"+skCreditNote))
	if err != nil {
		return credits, audits, fmt.Errorf("credit notes: %w", err)
	}
	audits, err = backfillCreatedAt(ctx, r.audit, apply, "begins_with(sk, :p)",
		map[string]types.AttributeValue{
			":p": &types.AttributeValueMemberS{Value: skAudit},
		}, rowIDFromSK(skAudit))
	if err != nil {
		return credits, audits, fmt.Errorf("audit: %w", err)
	}
	return credits, audits, nil
}

// rowIDFromSK is the row id after the last marker in the sort key: the part
// createdAtOrID reads the ULID from.
func rowIDFromSK(marker string) func(sk string) string {
	return func(sk string) string {
		_, id, _ := strings.Cut(sk, marker)
		return id
	}
}

func backfillCreatedAt(
	ctx context.Context,
	base Base,
	apply bool,
	skFilter string,
	values map[string]types.AttributeValue,
	rowID func(sk string) string,
) (CreatedAtBackfillReport, error) {
	var rep CreatedAtBackfillReport
	var start map[string]types.AttributeValue
	for {
		out, err := base.ScanRaw(ctx, &dynamodb.ScanInput{ // standards:allow backfill scan
			FilterExpression:          aws.String(skFilter + " AND attribute_not_exists(created_at)"),
			ProjectionExpression:      aws.String("pk, sk"),
			ExpressionAttributeValues: values,
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return rep, fmt.Errorf("scan: %w", err)
		}
		rows, err := DecodeItems[createdAtKey](out.Items)
		if err != nil {
			return rep, err
		}
		for _, row := range rows {
			at := createdAtOrID(time.Time{}, rowID(row.SK))
			if at.IsZero() {
				rep.Unparseable = append(rep.Unparseable, row.PK+" "+row.SK)
				continue
			}
			rep.Missing++
			if !apply {
				continue
			}
			wrote, err := setCreatedAt(ctx, base, row, at)
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

// createdAtKey is the projection the backfill reads.
type createdAtKey struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
}

func setCreatedAt(ctx context.Context, base Base, row createdAtKey, at time.Time) (bool, error) {
	// Encoded the way the row types encode it, so the stored format is the one
	// every other row has.
	item, err := Encode(struct {
		CreatedAt time.Time `dynamodbav:"created_at"`
	}{CreatedAt: at})
	if err != nil {
		return false, err
	}
	_, err = base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: row.PK},
			"sk": &types.AttributeValueMemberS{Value: row.SK},
		},
		UpdateExpression:    aws.String("SET created_at = :t"),
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_not_exists(created_at)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t": item["created_at"],
		},
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set created_at on %s/%s: %w", row.PK, row.SK, err)
	}
	return true, nil
}
