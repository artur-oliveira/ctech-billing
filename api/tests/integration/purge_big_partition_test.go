//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// seedBills writes n bill-shaped rows into a space's partition, 25 to a batch.
func seedBills(t *testing.T, pk string, n int) {
	t.Helper()
	table := fmt.Sprintf("%s_%s", testCfg.TablePrefix, repositories.TableBills)
	pad := strings.Repeat("x", 300)
	var batch []types.WriteRequest
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if _, err := testDB.BatchWriteItem(context.Background(), &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{table: batch}}); err != nil {
			t.Fatal(err)
		}
		batch = batch[:0]
	}
	for i := 0; i < n; i++ {
		batch = append(batch, types.WriteRequest{PutRequest: &types.PutRequest{Item: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: fmt.Sprintf("BILL#%08d", i)},
			"title": &types.AttributeValueMemberS{Value: "bill " + pad},
		}}})
		if len(batch) == 25 {
			flush()
		}
	}
	flush()
}

// A partition larger than several pages is deleted whole: the purge reads keys a
// page at a time and deletes as it goes, and the continuation key survives the
// rows deleted behind it.
func TestPurgeSpaceDeletesAPartitionLargerThanSeveralPages(t *testing.T) {
	const n = 3100 // three pages of 1000 and a remainder
	sp := jobSpace(t, newSpaceOrgID(), true)
	seedBills(t, sp.PK(), n)

	counts, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(context.Background(), sp)
	if err != nil {
		t.Fatal(err)
	}
	if counts[repositories.TableBills] != n {
		t.Fatalf("deleted %d bills, want %d", counts[repositories.TableBills], n)
	}
	// Nothing is left, and a second run is a clean no-op.
	again, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(context.Background(), sp)
	if err != nil || again[repositories.TableBills] != 0 {
		t.Fatalf("second run: %v, %d deleted", err, again[repositories.TableBills])
	}
}
