package repositories

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// A bill recognised and settled in one write touches payables twice; the two
// ADDs become one, and the sum is what each would have added.
func TestMergeAddsFoldsTheSameItem(t *testing.T) {
	r := &LedgerRepository{
		accounts: Base{TableName: "accounts"}, txs: Base{TableName: "txs"}, audit: Base{TableName: "audit"},
	}
	sp, _ := space.ForJob("USER#u1", true)
	sys, _ := finance.DefaultSystemAccounts()
	date := brcal.New(2026, time.March, 10)
	facts := finance.BillFacts{Direction: finance.Payable, Amount: 1000, CategoryID: "food", AccountID: "bank"}
	rec, _ := finance.RecognizeBill(sys, facts, date)
	set, _ := finance.SettleBill(sys, facts, 1000, "", date)
	a, err := r.planPost(sp, rec, PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.planPost(sp, set, PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	merged := mergeAdds(append(append([]types.TransactWriteItem(nil), a.Items...), b.Items...))

	seen := map[string]int{}
	var payables *types.Update
	for _, it := range merged {
		var k string
		switch {
		case it.Put != nil:
			k = aws.ToString(it.Put.TableName) + keyString(it.Put.Item)
		case it.Update != nil:
			k = aws.ToString(it.Update.TableName) + keyString(it.Update.Key)
			if keyString(it.Update.Key) == keyString(map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(LedgerAccountSK(sys.Payables))}) {
				payables = it.Update
			}
		}
		if seen[k]++; seen[k] > 1 {
			t.Fatalf("item %q is written twice", k)
		}
	}
	if payables == nil || numberOf(payables.ExpressionAttributeValues[":amt"]) != 0 {
		t.Fatalf("payables nets to zero in one ADD, got %+v", payables)
	}
	// 2 headers + 4 entries + 2 audits, then balances: food, payables, bank (3)
	// and summaries: food, payables, bank (3).
	if len(merged) != 14 {
		t.Fatalf("%d items, want 14", len(merged))
	}
	// The originals are untouched: the plans can still be sent on their own.
	for _, it := range a.Items {
		if it.Update != nil && keyString(it.Update.Key) == keyString(payables.Key) && numberOf(it.Update.ExpressionAttributeValues[":amt"]) != -1000 {
			t.Fatal("mergeAdds changed the plan it was given")
		}
	}
}

func TestMergeAddsLeavesOtherWritesAlone(t *testing.T) {
	sk := "X"
	set := types.TransactWriteItem{Update: &types.Update{TableName: aws.String("t"), Key: map[string]types.AttributeValue{"pk": str("p"), "sk": str(sk)},
		UpdateExpression: aws.String("SET a = :a"), ExpressionAttributeValues: map[string]types.AttributeValue{":a": numberValue(1)}}}
	if got := mergeAdds([]types.TransactWriteItem{set, set}); len(got) != 2 {
		t.Fatalf("a SET was folded: %d items", len(got))
	}
}
