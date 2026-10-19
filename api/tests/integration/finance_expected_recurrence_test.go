//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A recurrence of an expected (variable) amount waits for the person to confirm
// each occurrence. Its pending row is written under a condition on the
// recurrence, and DynamoDB refuses `end` (and other words) used as a bare name:
// production's daily materialise failed every such recurrence until this ran.
func TestAnExpectedRecurrenceGetsItsPendingOccurrenceAndIsConfirmed(t *testing.T) {
	for _, end := range []brcal.Date{{}, brcal.New(2026, time.December, 31)} {
		f := newBillsFixture(t)
		ctx, now := context.Background(), time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
		recs := repositories.NewRecurrenceRepository(testDB, testCfg)
		rec, err := recs.Create(ctx, f.sp, finance.Recurrence{
			Direction: finance.Payable, Amount: 15000, AmountMode: finance.AmountExpected, CategoryID: "rent", AccountID: "bank", Description: "Luz",
			Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.January, 1), End: end, Adjust: finance.AdjustNone},
		}, repositories.PostMeta{}, now)
		if err != nil {
			t.Fatal(err)
		}
		nominal, due := brcal.New(2026, time.March, 10), brcal.New(2026, time.March, 10)
		created, err := recs.CreatePendingOccurrence(ctx, f.sp, rec, nominal, due, now)
		if err != nil || !created {
			t.Fatalf("end %v: pending occurrence created=%v err=%v", end, created, err)
		}
		if again, err := recs.CreatePendingOccurrence(ctx, f.sp, rec, nominal, due, now); err != nil || again {
			t.Fatalf("a second run must find it: created=%v err=%v", again, err)
		}
		pending, err := recs.GetPendingOccurrence(ctx, f.sp, rec.ID, nominal)
		if err != nil {
			t.Fatal(err)
		}
		made, bill, err := f.bills.ConfirmExpectedOccurrence(ctx, f.sp, pending, 17250, repositories.PostMeta{}, now)
		if err != nil || !made || bill.Amount != 17250 {
			t.Fatalf("confirm: made=%v bill=%+v err=%v", made, bill, err)
		}
	}
}
