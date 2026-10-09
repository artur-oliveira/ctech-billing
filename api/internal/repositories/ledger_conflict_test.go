package repositories

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func cancelled(codes ...string) error {
	rs := make([]types.CancellationReason, len(codes))
	for i, c := range codes {
		rs[i] = types.CancellationReason{Code: aws.String(c)}
	}
	return &types.TransactionCanceledException{CancellationReasons: rs}
}

// The production log: a space-creation transaction whose reasons mix lost
// conditions (the rows exist) with conflicts (another request is writing them).
// Nothing was decided, so it is retryable and never "already exists".
func TestRetryableCancel(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"conflicts and conditions", cancelled("TransactionConflict", "ConditionalCheckFailed", "TransactionConflict", "TransactionConflict"), true},
		{"all conflicts", cancelled("TransactionConflict", "TransactionConflict"), true},
		{"only conditions", cancelled("ConditionalCheckFailed", "ConditionalCheckFailed"), false},
		{"throttle is not a retry here", cancelled("ThrottlingError", "TransactionConflict"), false},
		{"not a cancellation", errors.New("x"), false},
	}
	for _, c := range cases {
		if got := retryableCancel(c.err); got != c.want {
			t.Errorf("%s: retryableCancel = %v, want %v", c.name, got, c.want)
		}
	}
}
