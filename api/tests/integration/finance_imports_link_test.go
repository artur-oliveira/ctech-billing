//go:build integration

package integration

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// 6.6 follow-up: a statement line whose bill the recurrence's auto-settle
// already paid is linked to it, not settled again. Linking posts nothing.

// autoPaid creates an auto-settling payable in account acct, due on due, and
// settles it the way the daily job does (origin auto_settle, on the due date).
func (f importsFixture) autoPaid(t *testing.T, acct string, amount billing.Cents, due brcal.Date) finance.Bill {
	t.Helper()
	ctx := context.Background()
	b, err := f.bills.Create(ctx, f.sp, finance.Bill{
		Direction: finance.Payable, Amount: amount, AccountID: acct, CategoryID: "rent", Description: "Aluguel",
		Competence: brcal.New(2026, time.March, 1), Due: due, Origin: finance.OriginManual, AutoSettle: true,
	}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	paid, err := f.bills.Settle(ctx, f.sp, b.ID, amount, "", due, repositories.PostMeta{Origin: repositories.OriginAutoSettle, Actor: "finance-job"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return paid
}

func (f importsFixture) service() *services.FinanceImports {
	return services.NewFinanceImports(f.imports, f.bills)
}

func (f importsFixture) candidates(t *testing.T, importID string, n int) []finance.Bill {
	t.Helper()
	_, lines, err := f.service().Get(context.Background(), f.sp, importID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.N == n {
			return l.Candidates
		}
	}
	t.Fatalf("no line %d", n)
	return nil
}

func ids(bills []finance.Bill) []string {
	out := []string{}
	for _, b := range bills {
		out = append(out, b.ID)
	}
	return out
}

func (f importsFixture) books(t *testing.T) ([]billing.Cents, []repositories.SummaryRow) {
	t.Helper()
	var bal []billing.Cents
	for _, id := range []string{"bank", "rent", "sys-payables"} {
		bal = append(bal, f.bal(t, id))
	}
	sums, err := f.ledger.Summaries(context.Background(), f.sp, finance.MonthOf(brcal.New(2026, time.January, 1)), finance.MonthOf(brcal.New(2026, time.December, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return bal, sums
}

func TestALineIsLinkedToTheBillAutoSettlePaidAndPostsNothing(t *testing.T) {
	f := newImportsFixture(t)
	ctx := context.Background()
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	bill, err := f.bills.Create(ctx, f.sp, finance.Bill{
		Direction: finance.Payable, Amount: 100000, AccountID: "bank", CategoryID: "rent", Description: "Aluguel",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10), Origin: finance.OriginManual, AutoSettle: true,
	}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The real job pays it on its due date.
	_ = services.NewFinanceJobs(f.bills, recs).AutoSettle(ctx, true, brcal.New(2026, time.March, 20), time.Now())
	if got, _ := f.bills.Get(ctx, f.sp, bill.ID); got.Status != finance.BillPaid {
		t.Fatalf("the job did not pay the bill: %+v", got)
	}

	imp := f.upload(t, "k", time.Now(), threeLines) // line 1: -1000.00 on 9 March
	cands := f.candidates(t, imp.ID, 1)
	if len(cands) != 1 || cands[0].ID != bill.ID || cands[0].Status != finance.BillPaid || cands[0].PaidDate != brcal.New(2026, time.March, 10) {
		t.Fatalf("candidates = %+v, want the auto-paid bill", cands)
	}

	balBefore, sumBefore := f.books(t)
	line, linked, err := f.service().Link(ctx, f.sp, imp.ID, 1, bill.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if line.Status != repositories.LineLinked || line.BillID != bill.ID || linked.ID != bill.ID || linked.Status != finance.BillPaid {
		t.Fatalf("line %+v, bill %+v", line, linked)
	}
	balAfter, sumAfter := f.books(t)
	if !reflect.DeepEqual(balBefore, balAfter) {
		t.Fatalf("balances moved: %v → %v", balBefore, balAfter)
	}
	if !reflect.DeepEqual(sumBefore, sumAfter) {
		t.Fatalf("summaries moved: %+v → %+v", sumBefore, sumAfter)
	}
	stored, _ := f.bills.Get(ctx, f.sp, bill.ID)
	if len(stored.TransactionIDs) != len(linked.TransactionIDs) || stored.Status != finance.BillPaid {
		t.Fatalf("the bill gained a transaction: %+v", stored)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now()); got.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", got.Pending())
	}

	// Again, the same line and bill: idempotent, nothing moves.
	again, _, err := f.service().Link(ctx, f.sp, imp.ID, 1, bill.ID, time.Now())
	if err != nil || again.Status != repositories.LineLinked {
		t.Fatalf("relink = %+v, %v", again, err)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now()); got.Pending() != 2 {
		t.Fatalf("a repeated link counted twice: pending = %d", got.Pending())
	}
}

func TestALinkedBillIsNotOfferedAgain(t *testing.T) {
	f := newImportsFixture(t)
	ctx := context.Background()
	bill := f.autoPaid(t, "bank", 500, brcal.New(2026, time.March, 10))
	imp := f.upload(t, "k", time.Now(), syntheticOFX(
		ofxLine("A", "20260310", "-5.00", "Café"),
		ofxLine("B", "20260311", "-5.00", "Café"),
	))
	if got := ids(f.candidates(t, imp.ID, 2)); !reflect.DeepEqual(got, []string{bill.ID}) {
		t.Fatalf("line 2 before = %v", got)
	}
	if _, _, err := f.service().Link(ctx, f.sp, imp.ID, 1, bill.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := f.candidates(t, imp.ID, 2); len(got) != 0 {
		t.Fatalf("a linked bill is still offered: %v", ids(got))
	}
	if _, _, err := f.service().Link(ctx, f.sp, imp.ID, 2, bill.ID, time.Now()); !errors.Is(err, repositories.ErrBillLinked) {
		t.Fatalf("linking a linked bill: %v, want ErrBillLinked", err)
	}
	if l := f.line(t, imp.ID, 2); l.Status != repositories.LinePending {
		t.Fatalf("the refused line stays pending: %+v", l)
	}
}

func TestTwoLinesRacingToLinkOneBillOnlyOneWins(t *testing.T) {
	f := newImportsFixture(t)
	ctx := context.Background()
	bill := f.autoPaid(t, "bank", 500, brcal.New(2026, time.March, 10))
	imp := f.upload(t, "k", time.Now(), syntheticOFX(
		ofxLine("A", "20260310", "-5.00", "Café"),
		ofxLine("B", "20260311", "-5.00", "Café"),
	))
	balBefore, _ := f.books(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = f.service().Link(ctx, f.sp, imp.ID, i+1, bill.ID, time.Now())
		}()
	}
	close(start)
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, repositories.ErrBillLinked):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d lines linked the bill (errors %v), want exactly 1", won, errs)
	}
	l1, l2 := f.line(t, imp.ID, 1), f.line(t, imp.ID, 2)
	if (l1.Status == repositories.LineLinked) == (l2.Status == repositories.LineLinked) {
		t.Fatalf("lines = %+v, %+v: exactly one is linked", l1, l2)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now()); got.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", got.Pending())
	}
	if balAfter, _ := f.books(t); !reflect.DeepEqual(balBefore, balAfter) {
		t.Fatalf("balances moved: %v → %v", balBefore, balAfter)
	}
}

func TestAPaidBillOfAnotherAmountIsNotOffered(t *testing.T) {
	f := newImportsFixture(t)
	bill := f.autoPaid(t, "bank", 100000, brcal.New(2026, time.March, 10))
	imp := f.upload(t, "k", time.Now(), syntheticOFX(ofxLine("A", "20260310", "-1012.50", "Aluguel com juros")))
	if got := f.candidates(t, imp.ID, 1); len(got) != 0 {
		t.Fatalf("a different amount is offered: %v", ids(got))
	}
	if _, _, err := f.service().Link(context.Background(), f.sp, imp.ID, 1, bill.ID, time.Now()); !errors.Is(err, repositories.ErrLineMismatch) {
		t.Fatalf("linking another amount: %v, want ErrLineMismatch", err)
	}
}

func TestAManuallyPaidBillIsNotOffered(t *testing.T) {
	f := newImportsFixture(t)
	ctx := context.Background()
	bill := f.payable(t, 100000, brcal.New(2026, time.March, 10))
	if _, err := f.bills.Settle(ctx, f.sp, bill.ID, 100000, "", brcal.New(2026, time.March, 10), repositories.PostMeta{Origin: "manual"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	imp := f.upload(t, "k", time.Now(), threeLines)
	if got := f.candidates(t, imp.ID, 1); len(got) != 0 {
		t.Fatalf("a manually paid bill is offered: %v", ids(got))
	}
	if _, _, err := f.service().Link(ctx, f.sp, imp.ID, 1, bill.ID, time.Now()); !errors.Is(err, repositories.ErrLineMismatch) {
		t.Fatalf("linking a manual payment: %v, want ErrLineMismatch", err)
	}
}

func TestAnAutoPaidBillOfAnotherAccountOrSpaceIsNeverOfferedOrLinkable(t *testing.T) {
	f := newImportsFixture(t)
	ctx, now := context.Background(), time.Now()
	if err := f.ledger.CreateAccount(ctx, f.sp, finance.LedgerAccount{ID: "savings", Name: "Poupança", Class: finance.ClassAsset}, now); err != nil {
		t.Fatal(err)
	}
	otherAccount := f.autoPaid(t, "savings", 100000, brcal.New(2026, time.March, 10))

	// Another space, the same account ids, the same bill shape.
	stranger := importsFixture{billsFixture: newBillsFixture(t), imports: f.imports}
	otherSpace := stranger.autoPaid(t, "bank", 100000, brcal.New(2026, time.March, 10))

	imp := f.upload(t, "k", now, threeLines)
	if got := f.candidates(t, imp.ID, 1); len(got) != 0 {
		t.Fatalf("offered across accounts or spaces: %v", ids(got))
	}
	if _, _, err := f.service().Link(ctx, f.sp, imp.ID, 1, otherAccount.ID, now); !errors.Is(err, repositories.ErrLineMismatch) {
		t.Fatalf("another account's bill: %v, want ErrLineMismatch", err)
	}
	if _, _, err := f.service().Link(ctx, f.sp, imp.ID, 1, otherSpace.ID, now); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("another space's bill: %v, want ErrNotFound", err)
	}
	if l := f.line(t, imp.ID, 1); l.Status != repositories.LinePending {
		t.Fatalf("a refused link moved the line: %+v", l)
	}
	if got, _ := stranger.bills.Get(ctx, stranger.sp, otherSpace.ID); got == nil || got.Status != finance.BillPaid {
		t.Fatalf("the other space's bill = %+v", got)
	}
	// And the stranger, importing the same file, is not offered this space's bill.
	theirs := stranger.upload(t, "k", now, threeLines)
	if got := ids(stranger.candidates(t, theirs.ID, 1)); !reflect.DeepEqual(got, []string{otherSpace.ID}) {
		t.Fatalf("the stranger's own candidates = %v", got)
	}
}

func TestALineSaysWhenItExpires(t *testing.T) {
	f := newImportsFixture(t)
	now := time.Now()
	imp := f.upload(t, "k", now, threeLines)
	l := f.line(t, imp.ID, 1)
	want := now.Add(90 * 24 * time.Hour)
	if d := l.Expires.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("expires = %v, want about %v", l.Expires, want)
	}
}
