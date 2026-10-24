package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// BackfillCustomerIndex gives every invoice row written before customer-index
// existed its customer_pk (LookupCustomerInvoicesPK). Dry run unless apply.
//
// The report is the subscription backfill's shape: LookupBackfillRow.LookupPK
// carries the customer_pk this pass would write (or found), Scanned counts
// invoice rows only — never their nested `#LINE#`, `#ATTEMPT#`, checkout or
// credit-note rows.
//
// It is an operator's migration, run from cmd/backfill-invoice-lookup with the
// operator's own credentials. The service role denies dynamodb:Scan
// (terraform/billing/iam.tf), and nothing on a request path calls this.
//
// Idempotent: each write is conditional on the row still having no
// customer_pk, so a re-run, or a run racing a new Create, writes nothing twice.
// The row's other attributes — lookup_pk and updated_at included — are left as
// they are: this adds an index key, it does not change the invoice.
func (r *InvoiceRepository) BackfillCustomerIndex(ctx context.Context, apply bool) (LookupBackfillReport, error) {
	var rep LookupBackfillReport
	var start map[string]types.AttributeValue
	for {
		out, err := r.base.ScanRaw(ctx, &dynamodb.ScanInput{ // standards:allow backfill scan
			// The INVOICE# range with a status: invoices, attempts, checkouts and
			// credit notes. The nested ones are dropped below by their sort key,
			// which a filter expression cannot express.
			FilterExpression:     aws.String("begins_with(sk, :inv) AND attribute_exists(#status)"),
			ProjectionExpression: aws.String("pk, sk, organization_id, livemode, customer_id, customer_pk"),
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":inv": &types.AttributeValueMemberS{Value: skInvoice},
			},
			ExclusiveStartKey: start,
		})
		if err != nil {
			return rep, fmt.Errorf("scan invoices: %w", err)
		}
		rows, err := DecodeItems[invoiceCustomerRow](out.Items)
		if err != nil {
			return rep, err
		}
		for _, row := range rows {
			if !isInvoiceSK(row.SK) {
				continue
			}
			rep.Scanned++
			want := LookupBackfillRow{PK: row.PK, SK: row.SK}
			if row.CustomerID == "" {
				rep.Unindexable = append(rep.Unindexable, want)
				continue
			}
			want.LookupPK = LookupCustomerInvoicesPK(row.OrganizationID, row.Livemode, row.CustomerID)
			switch {
			case row.CustomerPK == want.LookupPK:
				continue
			case row.CustomerPK != "":
				want.LookupPK = row.CustomerPK + " (want " + want.LookupPK + ")"
				rep.Mismatched = append(rep.Mismatched, want)
				continue
			}
			rep.Missing = append(rep.Missing, want)
			if !apply {
				continue
			}
			wrote, err := r.setCustomerPK(ctx, want)
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

// isInvoiceSK is true for an invoice row's own sort key ("INVOICE#" + id) and
// false for every row nested under it ("INVOICE#" + id + "#...").
func isInvoiceSK(sk string) bool {
	rest, ok := strings.CutPrefix(sk, skInvoice)
	return ok && rest != "" && !strings.Contains(rest, "#")
}

// invoiceCustomerRow is the projection the backfill reads.
type invoiceCustomerRow struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	OrganizationID string `dynamodbav:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"`
	CustomerID     string `dynamodbav:"customer_id"`
	CustomerPK     string `dynamodbav:"customer_pk"`
}

func (r *InvoiceRepository) setCustomerPK(ctx context.Context, row LookupBackfillRow) (bool, error) {
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: row.PK},
			"sk": &types.AttributeValueMemberS{Value: row.SK},
		},
		UpdateExpression:    aws.String("SET customer_pk = :c"),
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_not_exists(customer_pk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":c": &types.AttributeValueMemberS{Value: row.LookupPK},
		},
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set customer_pk on %s/%s: %w", row.PK, row.SK, err)
	}
	return true, nil
}
