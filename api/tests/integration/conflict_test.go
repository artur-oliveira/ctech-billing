//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go/middleware"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// api-commons v1.11.0 stopped reading a TransactionConflict as a failed
// condition (UX batch 5 review, I1/M1). These tests make DynamoDB Local answer
// one TransactionConflict, as a concurrent transaction on the same item would,
// and check that what used to recover through the old wide meaning still does.

// conflictOnce returns a client whose first TransactWriteItems touching a table
// whose name contains `table` is not sent: `before` runs (the racing request,
// may be nil) and the call answers TransactionCanceledException with
// TransactionConflict on its last item. Every other call goes through.
func conflictOnce(table string, before func()) (*dynamodb.Client, *atomic.Int64) {
	return conflictTimes(table, 1, before)
}

// conflictTimes is conflictOnce for the first n such calls (before runs on the first).
func conflictTimes(table string, n int64, before func()) (*dynamodb.Client, *atomic.Int64) {
	var fired atomic.Int64
	c := dynamodb.NewFromConfig(testAWSConf, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(os.Getenv("DYNAMODB_ENDPOINT"))
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("conflict-once",
				func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
					tw, ok := in.Parameters.(*dynamodb.TransactWriteItemsInput)
					if !ok || !touches(tw, table) || fired.Add(1) > n {
						return next.HandleInitialize(ctx, in)
					}
					if before != nil && fired.Load() == 1 {
						before()
					}
					reasons := make([]types.CancellationReason, len(tw.TransactItems))
					for i := range reasons {
						reasons[i] = types.CancellationReason{Code: aws.String("None")}
					}
					reasons[len(reasons)-1] = types.CancellationReason{Code: aws.String("TransactionConflict")}
					return middleware.InitializeOutput{}, middleware.Metadata{}, &types.TransactionCanceledException{
						Message:             aws.String("Transaction cancelled, please refer cancellation reasons for specific reasons [TransactionConflict]"),
						CancellationReasons: reasons,
					}
				}), middleware.Before)
		})
	})
	return c, &fired
}

func touches(tw *dynamodb.TransactWriteItemsInput, table string) bool {
	for _, it := range tw.TransactItems {
		var name *string
		switch {
		case it.Put != nil:
			name = it.Put.TableName
		case it.Update != nil:
			name = it.Update.TableName
		case it.ConditionCheck != nil:
			name = it.ConditionCheck.TableName
		case it.Delete != nil:
			name = it.Delete.TableName
		}
		if name != nil && strings.Contains(*name, table) {
			return true
		}
	}
	return false
}

// I1: a finalize whose numbering transaction (the counter update is its last
// item) is cancelled by a concurrent transaction retries, and the invoice is
// issued — not left a DRAFT that the next sweep skips as already generated.
func TestFinalizeRetriesATransactionConflictOnTheCounter(t *testing.T) {
	org := newOrg(t, true)
	inv := newDraftInvoice(t, org, "gen_conflict_"+org.ID)
	client, fired := conflictOnce("_invoices", nil)
	repo := repositories.NewInvoiceRepository(client, testCfg)
	due := brcal.New(2026, time.March, 20)
	if _, err := repo.Finalize(ctxT(t), inv, due, brcal.Date{}, billing.CauseScheduler, "scheduler", "req_conflict", now()); err != nil {
		t.Fatalf("Finalize after one TransactionConflict = %v, want a retry that issues it", err)
	}
	if fired.Load() < 1 {
		t.Fatal("the conflict was never injected")
	}
	got, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != billing.InvoiceOpen || got.Number == 0 {
		t.Fatalf("invoice is %s numbered %d, want OPEN with a number", got.Status, got.Number)
	}
}

// Audit (I1): two downloads of one invoice record the same PDF key at once; the
// one cancelled by the other's transaction is not an error, as a lost condition
// never was.
func TestRecordingThePDFKeyIgnoresATransactionConflict(t *testing.T) {
	org := newOrg(t, true)
	inv := newDraftInvoice(t, org, "gen_pdf_"+org.ID)
	client, fired := conflictOnce("_invoices", nil)
	if err := repositories.NewInvoiceRepository(client, testCfg).RecordPDFKey(ctxT(t), inv, "pdf/"+inv.ID+".pdf", now()); err != nil {
		t.Fatalf("RecordPDFKey after a TransactionConflict = %v, want nil", err)
	}
	if fired.Load() < 1 {
		t.Fatal("the conflict was never injected")
	}
}

// M1: two "Pagar" presses together. The other press wins the attempt row; ours
// gets a TransactionConflict on it. The person sees the winner's checkout —
// the same QR code — not a 500.
func TestTwoPayPressesShowTheSameChargeOnATransactionConflict(t *testing.T) {
	e := newPayEnv(t)
	inv := e.openInvoice(t)
	var winner *billing.CheckoutSession
	client, fired := conflictOnce("_invoices", func() {
		s, _, err := e.collector.Pay(ctxT(t), e.org.ID, true, inv.ID, "test", "req_winner", now())
		if err != nil {
			t.Errorf("the winning press = %v", err)
		}
		winner = s
	})
	loser := services.NewCollector(
		repositories.NewInvoiceRepository(testDB, testCfg),
		repositories.NewPaymentRepository(client, testCfg),
		repositories.NewCustomerRepository(testDB, testCfg),
		repositories.NewOrganizationRepository(testDB, testCfg),
		repositories.NewSubscriptionRepository(testDB, testCfg),
		e.collectorCharges(),
	)
	got, _, err := loser.Pay(ctxT(t), e.org.ID, true, inv.ID, "test", "req_loser", now())
	if fired.Load() < 1 {
		t.Fatal("the conflict was never injected")
	}
	if err != nil {
		t.Fatalf("the second press = %v, want the winner's checkout", err)
	}
	if winner == nil || got.ID != winner.ID || got.PixCode != winner.PixCode {
		t.Fatalf("second press shows %+v, want the winner's %+v", got, winner)
	}
}
