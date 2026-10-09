//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/smithy-go/middleware"

	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// failingNthTransaction is a client that fails its nth TransactWriteItems call
// as a network error would, after the earlier ones committed.
func failingNthTransaction(t *testing.T, n int32) *dynamodb.Client {
	t.Helper()
	var calls atomic.Int32
	return dynamodb.New(testDB.Options(), func(o *dynamodb.Options) {
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("fail-nth",
				func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
					if awsmiddleware.GetOperationName(ctx) == "TransactWriteItems" && calls.Add(1) == n {
						return middleware.InitializeOutput{}, middleware.Metadata{}, errors.New("injected: connection reset")
					}
					return next.HandleInitialize(ctx, in)
				}), middleware.Before)
		})
	})
}

func manyLines(n int, prefix string) statement.Parsed {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, ofxLine(fmt.Sprintf("%s%d", prefix, i), "20260301", fmt.Sprintf("-%d.00", i), "Compra "+prefix))
	}
	p, err := statement.ParseOFX(syntheticOFX(lines...))
	if err != nil {
		panic(err)
	}
	return p
}

// An upload that fails halfway leaves a header that counts the lines it holds,
// so the list says how many wait; the same retry then completes it.
func TestAFailedUploadKeepsAnHonestHeader(t *testing.T) {
	f := newImportsFixture(t)
	ctx, now := context.Background(), time.Now()
	file := manyLines(120, "A")
	// Calls: 1 the header, 2 and 3 two chunks, 4 fails.
	broken := repositories.NewImportRepository(failingNthTransaction(t, 4), testCfg)
	if _, err := broken.Import(ctx, f.sp, "bank", statement.FormatOFX, file, "same-key", now); err == nil {
		t.Fatal("the injected failure did not surface")
	}
	list, err := f.imports.List(ctx, f.sp, "bank", now)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	_, lines, _ := f.imports.Get(ctx, f.sp, list[0].ID, now)
	if list[0].Lines != len(lines) || len(lines) == 0 {
		t.Fatalf("header says %d lines, %d are stored", list[0].Lines, len(lines))
	}
	retry, err := f.imports.Import(ctx, f.sp, "bank", statement.FormatOFX, file, "same-key", now)
	if err != nil || retry.ID != list[0].ID || retry.Lines != 120 {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, retry.ID, now); got.Lines != 120 {
		t.Fatalf("header after the retry = %d lines, want 120", got.Lines)
	}
}

// A different file sent under the same idempotency key (the person picked
// another file after a failure) is another import: it never overwrites the
// first one's lines.
func TestAnotherFileUnderTheSameKeyIsAnotherImport(t *testing.T) {
	f := newImportsFixture(t)
	ctx, now := context.Background(), time.Now()
	a, err := f.imports.Import(ctx, f.sp, "bank", statement.FormatOFX, manyLines(3, "A"), "same-key", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.imports.Import(ctx, f.sp, "bank", statement.FormatOFX, manyLines(3, "B"), "same-key", now)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == a.ID || b.Lines != 3 {
		t.Fatalf("b = %+v (a = %s)", b, a.ID)
	}
	_, lines, _ := f.imports.Get(ctx, f.sp, a.ID, now)
	for _, l := range lines {
		if l.Description != "Compra A" {
			t.Fatalf("file A's line %d was overwritten: %q", l.N, l.Description)
		}
	}
}
