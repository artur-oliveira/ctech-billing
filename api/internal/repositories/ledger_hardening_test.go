package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func canceled(codes ...string) error {
	rs := make([]types.CancellationReason, len(codes))
	for i, c := range codes {
		rs[i] = types.CancellationReason{Code: aws.String(c)}
	}
	return errors.Join(errors.New("dynamodb"), &types.TransactionCanceledException{CancellationReasons: rs})
}

// Post is exported and takes a Transaction, so the caller must not be able to
// choose the label that decides which verb applies, or forge a reversal.
func TestPostRefusesAForgedReversal(t *testing.T) {
	r := &LedgerRepository{}
	sp, _ := space.ForJob("USER#u1", true)
	date := brcal.New(2026, time.March, 2)
	legs := []finance.Leg{{AccountID: "a", Amount: 1}, {AccountID: "b", Amount: -1}}

	withAdjusts, _ := finance.NewTransaction(finance.KindTransfer, date, legs...)
	withAdjusts.Adjusts = "someone-elses-tx"
	if _, err := r.Post(context.Background(), sp, withAdjusts, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidTransaction) {
		t.Fatalf("Post with Adjusts = %v, want ErrInvalidTransaction", err)
	}
	asReversal, _ := finance.NewTransaction(finance.KindReversal, date, legs...)
	if _, err := r.Post(context.Background(), sp, asReversal, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidTransaction) {
		t.Fatalf("Post of kind reversal = %v, want ErrInvalidTransaction", err)
	}
}

// A cancelled transaction is not automatically a failed condition: conflicts and
// throttling are retryable and must not be reported as client errors.
func TestPostCancelClassification(t *testing.T) {
	// legs at 0..2, marker at 3
	cases := []struct {
		name      string
		err       error
		markerIdx int
		want      error // nil means "the original error comes back unchanged"
	}{
		{"marker condition failed", canceled("None", "None", "None", "ConditionalCheckFailed"), 3, ErrAlreadyReversed},
		{"balance condition failed", canceled("None", "ConditionalCheckFailed", "None", "None"), 3, ErrUnknownAccount},
		{"balance condition failed, no marker", canceled("None", "ConditionalCheckFailed"), -1, ErrUnknownAccount},
		{"conflict, no marker", canceled("None", "TransactionConflict"), -1, nil},
		{"throttled with a marker", canceled("None", "ThrottlingError", "None", "None"), 3, nil},
	}
	for _, c := range cases {
		got := classifyPostCancel(c.err, c.markerIdx)
		if c.want == nil {
			if errors.Is(got, ErrUnknownAccount) || errors.Is(got, ErrAlreadyReversed) || !errors.Is(got, c.err) {
				t.Errorf("%s: got %v, want the original (retryable) error", c.name, got)
			}
		} else if !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOnlyConditionFailed(t *testing.T) {
	if !onlyConditionFailed(canceled("None", "ConditionalCheckFailed")) {
		t.Error("a plain condition failure was not recognised")
	}
	for _, err := range []error{canceled("TransactionConflict"), canceled("ConditionalCheckFailed", "ThrottlingError"), errors.New("boom")} {
		if onlyConditionFailed(err) {
			t.Errorf("%v was read as a condition failure", err)
		}
	}
}

func TestStatementWithAnInvertedRangeIsEmptyNotAnError(t *testing.T) {
	r := &LedgerRepository{} // a nil client would panic if a query were attempted
	sp, _ := space.ForJob("USER#u1", true)
	got, err := r.Statement(context.Background(), sp, "a", brcal.New(2026, 3, 2), brcal.New(2026, 3, 1))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
