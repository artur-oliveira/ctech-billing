package repositories

import (
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// mergeAdds folds the ledger's ADD-only updates that touch the same item into
// one. DynamoDB refuses two operations on one item in a TransactWriteItems, and
// two facts written together — an imported line recognised and settled in one
// write — both ADD to the payables balance and to its month's summary. ADD is
// commutative, so one update carrying the sum is the same write.
//
// Only updates whose expression begins with "ADD " and whose values are all
// numbers are folded; anything else that repeats an item is left for DynamoDB
// to refuse, because folding it would change its meaning.
func mergeAdds(items []types.TransactWriteItem) []types.TransactWriteItem {
	out := make([]types.TransactWriteItem, 0, len(items))
	at := map[string]int{}
	for _, it := range items {
		u := it.Update
		if u == nil || !strings.HasPrefix(aws.ToString(u.UpdateExpression), "ADD ") || !allNumbers(u.ExpressionAttributeValues) {
			out = append(out, it)
			continue
		}
		k := aws.ToString(u.TableName) + "\x00" + keyString(u.Key) + "\x00" +
			aws.ToString(u.UpdateExpression) + "\x00" + aws.ToString(u.ConditionExpression)
		i, seen := at[k]
		if !seen {
			at[k] = len(out)
			copied := *u
			copied.ExpressionAttributeValues = make(map[string]types.AttributeValue, len(u.ExpressionAttributeValues))
			for name, v := range u.ExpressionAttributeValues {
				copied.ExpressionAttributeValues[name] = v
			}
			out = append(out, types.TransactWriteItem{Update: &copied})
			continue
		}
		dst := out[i].Update.ExpressionAttributeValues
		for name, v := range u.ExpressionAttributeValues {
			dst[name] = numberValue(numberOf(dst[name]) + numberOf(v))
		}
	}
	return out
}

func allNumbers(values map[string]types.AttributeValue) bool {
	for _, v := range values {
		if _, ok := v.(*types.AttributeValueMemberN); !ok {
			return false
		}
	}
	return true
}

func numberOf(v types.AttributeValue) int64 {
	n, ok := v.(*types.AttributeValueMemberN)
	if !ok {
		return 0
	}
	out, _ := strconv.ParseInt(n.Value, 10, 64)
	return out
}

func keyString(key map[string]types.AttributeValue) string {
	s := func(name string) string {
		if v, ok := key[name].(*types.AttributeValueMemberS); ok {
			return v.Value
		}
		return ""
	}
	return s("pk") + "\x00" + s("sk")
}
