//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// billsFixture is a fresh space with a bank account, an expense category "rent",
// an income category "sales" and an expense "interest" for settlement gaps.
type billsFixture struct {
	sp     space.ResolvedSpace
	ledger *repositories.LedgerRepository
	bills  *repositories.BillRepository
}

func newBillsFixture(t *testing.T) billsFixture {
	t.Helper()
	sp := jobSpace(t, newSpaceOrgID(), true)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx, now := context.Background(), time.Now()
	if err := ledger.EnsureSpace(ctx, sp, now); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "rent", Name: "Aluguel", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
		{ID: "food", Name: "Alimentação", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
		{ID: "interest", Name: "Juros", Class: finance.ClassExpense, Group: finance.GroupFinancialResult},
		{ID: "sales", Name: "Vendas", Class: finance.ClassIncome, Group: finance.GroupGrossRevenue},
	} {
		if err := ledger.CreateAccount(ctx, sp, a, now); err != nil {
			t.Fatal(err)
		}
	}
	return billsFixture{sp: sp, ledger: ledger, bills: repositories.NewBillRepository(testDB, testCfg)}
}

func (f billsFixture) payable(t *testing.T, amount billing.Cents, due brcal.Date) finance.Bill {
	t.Helper()
	b, err := f.bills.Create(context.Background(), f.sp, finance.Bill{
		Direction: finance.Payable, Amount: amount, AccountID: "bank", CategoryID: "rent", Description: "Aluguel",
		Competence: brcal.New(2026, time.March, 1), Due: due, Origin: finance.OriginManual,
	}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f billsFixture) bal(t *testing.T, id string) billing.Cents {
	return balance(t, f.ledger, f.sp, id)
}

func TestCreatingABillRecognisesItAtomically(t *testing.T) {
	f := newBillsFixture(t)
	b := f.payable(t, 100000, brcal.New(2026, time.March, 10))

	if got := f.bal(t, "sys-payables"); got != -100000 {
		t.Fatalf("payables balance = %d, want -100000 (a credit)", got)
	}
	if got := f.bal(t, "rent"); got != 100000 {
		t.Fatalf("rent balance = %d, want 100000 (the expense, by competence)", got)
	}
	if f.bal(t, "bank") != 0 {
		t.Fatal("recognition moved cash")
	}
	got, err := f.bills.Get(context.Background(), f.sp, b.ID)
	if err != nil || got.Status != finance.BillForecast || len(got.TransactionIDs) != 1 {
		t.Fatalf("bill = %+v, %v", got, err)
	}
	page, err := f.bills.ListOpen(context.Background(), f.sp, finance.Payable, 10, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != b.ID {
		t.Fatalf("open list = %+v, %v", page, err)
	}
}

func TestACreateThatNamesAnAccountOfTheWrongClassIsRefused(t *testing.T) {
	f := newBillsFixture(t)
	for name, mutate := range map[string]func(*finance.Bill){
		"an income category on a payable":  func(b *finance.Bill) { b.CategoryID = "sales" },
		"a system account as category":     func(b *finance.Bill) { b.CategoryID = "sys-payables" },
		"an unknown category":              func(b *finance.Bill) { b.CategoryID = "nope" },
		"an expense as the paying account": func(b *finance.Bill) { b.AccountID = "rent" },
	} {
		b := finance.Bill{Direction: finance.Payable, Amount: 100, AccountID: "bank", CategoryID: "rent",
			Competence: brcal.New(2026, 3, 1), Due: brcal.New(2026, 3, 10), Origin: finance.OriginManual}
		mutate(&b)
		if _, err := f.bills.Create(context.Background(), f.sp, b, repositories.PostMeta{}, time.Now()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if f.bal(t, "sys-payables") != 0 {
		t.Fatal("a refused bill moved a balance")
	}
}

// Review Focus 2.
func TestOnlyOneSettlementWins(t *testing.T) {
	f := newBillsFixture(t)
	b := f.payable(t, 50000, brcal.New(2026, time.March, 10))

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.bills.Settle(context.Background(), f.sp, b.ID, 50000, "", brcal.New(2026, time.March, 10), repositories.PostMeta{}, time.Now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, losses := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, finance.ErrBillState) || strings.Contains(err.Error(), "TransactionCanceled"):
			// A loser either sees the settled status or collides with the winner
			// in flight (TransactionConflict, retryable); neither moved cash.
			losses++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d settlements won, want exactly 1 (losses %d)", wins, losses)
	}
	if got := f.bal(t, "bank"); got != -50000 {
		t.Fatalf("bank = %d, want -50000: cash moved once", got)
	}
	if got := f.bal(t, "sys-payables"); got != 0 {
		t.Fatalf("payables = %d, want 0 (cleared)", got)
	}
}

func TestSettlingForADifferentAmountPostsTheDifference(t *testing.T) {
	f := newBillsFixture(t)
	b := f.payable(t, 100000, brcal.New(2026, time.March, 10))
	if _, err := f.bills.Settle(context.Background(), f.sp, b.ID, 102000, "", brcal.New(2026, time.March, 12), repositories.PostMeta{}, time.Now()); err == nil {
		t.Fatal("a different amount without a category for the gap was accepted")
	}
	if _, err := f.bills.Settle(context.Background(), f.sp, b.ID, 102000, "interest", brcal.New(2026, time.March, 12), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.bal(t, "bank") != -102000 || f.bal(t, "interest") != 2000 || f.bal(t, "sys-payables") != 0 {
		t.Fatalf("bank %d interest %d payables %d", f.bal(t, "bank"), f.bal(t, "interest"), f.bal(t, "sys-payables"))
	}
	if _, err := f.bills.Settle(context.Background(), f.sp, b.ID, 1, "", brcal.New(2026, time.March, 12), repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrBillState) {
		t.Fatalf("settling a paid bill: %v", err)
	}
}

func TestACancelledBillCannotBeSettledAndAPaidOneCannotBeCancelled(t *testing.T) {
	f := newBillsFixture(t)
	cancelled := f.payable(t, 1000, brcal.New(2026, time.March, 10))
	if _, err := f.bills.Cancel(context.Background(), f.sp, cancelled.ID, brcal.Date{}, repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.bal(t, "rent") != 0 || f.bal(t, "sys-payables") != 0 {
		t.Fatalf("a cancelled bill left rent %d payables %d", f.bal(t, "rent"), f.bal(t, "sys-payables"))
	}
	if _, err := f.bills.Settle(context.Background(), f.sp, cancelled.ID, 1000, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrBillState) {
		t.Fatalf("settling a cancelled bill: %v", err)
	}

	paid := f.payable(t, 2000, brcal.New(2026, time.March, 10))
	if _, err := f.bills.Settle(context.Background(), f.sp, paid.ID, 2000, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bills.Cancel(context.Background(), f.sp, paid.ID, brcal.Date{}, repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrBillState) {
		t.Fatalf("cancelling a paid bill: %v", err)
	}
}

// Review Focus 3.
func TestEditingTheAmountReplacesTheRecognition(t *testing.T) {
	f := newBillsFixture(t)
	b := f.payable(t, 100000, brcal.New(2026, time.March, 10))
	newAmount, newCat := billing.Cents(150000), "food"

	edited, err := f.bills.Edit(context.Background(), f.sp, b.ID, repositories.BillEdit{Amount: &newAmount}, brcal.Date{}, repositories.PostMeta{}, time.Now())
	if err != nil || edited.Amount != 150000 {
		t.Fatalf("edit amount: %+v, %v", edited, err)
	}
	if f.bal(t, "sys-payables") != -150000 || f.bal(t, "rent") != 150000 {
		t.Fatalf("after the amount edit: payables %d rent %d", f.bal(t, "sys-payables"), f.bal(t, "rent"))
	}
	if _, err := f.bills.Edit(context.Background(), f.sp, b.ID, repositories.BillEdit{CategoryID: &newCat}, brcal.Date{}, repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.bal(t, "rent") != 0 || f.bal(t, "food") != 150000 || f.bal(t, "sys-payables") != -150000 {
		t.Fatalf("after the category edit: rent %d food %d payables %d", f.bal(t, "rent"), f.bal(t, "food"), f.bal(t, "sys-payables"))
	}
	drift, err := f.ledger.Rebuild(context.Background(), f.sp, false, time.Now())
	if err != nil || len(drift) != 0 {
		t.Fatalf("an edited bill left drift: %+v, %v", drift, err)
	}
}

func TestEditingTheDueDateMovesTheBillInTheOpenList(t *testing.T) {
	f := newBillsFixture(t)
	a := f.payable(t, 100, brcal.New(2026, time.March, 20))
	b := f.payable(t, 200, brcal.New(2026, time.March, 25))
	earlier := brcal.New(2026, time.March, 5)
	if _, err := f.bills.Edit(context.Background(), f.sp, b.ID, repositories.BillEdit{Due: &earlier}, brcal.Date{}, repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, _ := f.bills.ListOpen(context.Background(), f.sp, finance.Payable, 10, nil)
	if len(page.Items) != 2 || page.Items[0].ID != b.ID || page.Items[1].ID != a.ID {
		t.Fatalf("open order = %+v", page.Items)
	}
}

func TestAnIdFromAnotherSpaceIsNotFoundForBills(t *testing.T) {
	a, b := newBillsFixture(t), newBillsFixture(t)
	bill := a.payable(t, 100, brcal.New(2026, 3, 10))
	if _, err := b.bills.Get(context.Background(), b.sp, bill.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("B read A's bill: %v", err)
	}
	if _, err := b.bills.Settle(context.Background(), b.sp, bill.ID, 100, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("B settled A's bill: %v", err)
	}
}

func TestTheOpenIndexHoldsOnlyForecastBills(t *testing.T) {
	f := newBillsFixture(t)
	settled := f.payable(t, 100, brcal.New(2026, 3, 10))
	cancelled := f.payable(t, 200, brcal.New(2026, 3, 11))
	kept := f.payable(t, 300, brcal.New(2026, 3, 12))
	if _, err := f.bills.Settle(context.Background(), f.sp, settled.ID, 100, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bills.Cancel(context.Background(), f.sp, cancelled.ID, brcal.Date{}, repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, _ := f.bills.ListOpen(context.Background(), f.sp, finance.Payable, 10, nil)
	if len(page.Items) != 1 || page.Items[0].ID != kept.ID {
		t.Fatalf("open list = %+v, want only %s", page.Items, kept.ID)
	}
}

// A settlement or a cancellation is built from the bill as it was read. An edit
// landing in between changes what was recognised, so acting on the stale facts
// would leave a residue in payables. Both writes are conditioned on the bill
// being exactly as read, so of the two racing operations at most one commits.
func TestSettleAndEditRacingNeverLeaveAResidue(t *testing.T) {
	for i := 0; i < 15; i++ {
		f := newBillsFixture(t)
		b := f.payable(t, 100000, brcal.New(2026, time.March, 10))
		bigger := billing.Cents(130000)

		var wg sync.WaitGroup
		var settleErr, editErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, settleErr = f.bills.Settle(context.Background(), f.sp, b.ID, 100000, "", brcal.New(2026, 3, 10), repositories.PostMeta{}, time.Now())
		}()
		go func() {
			defer wg.Done()
			_, editErr = f.bills.Edit(context.Background(), f.sp, b.ID, repositories.BillEdit{Amount: &bigger}, brcal.Date{}, repositories.PostMeta{}, time.Now())
		}()
		wg.Wait()

		final, err := f.bills.Get(context.Background(), f.sp, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		payables := f.bal(t, "sys-payables")
		switch final.Status {
		case finance.BillPaid:
			if payables != 0 {
				t.Fatalf("iteration %d: paid bill left payables at %d (settle %v, edit %v)", i, payables, settleErr, editErr)
			}
		case finance.BillForecast:
			if payables != -final.Amount {
				t.Fatalf("iteration %d: forecast bill of %d has payables at %d (settle %v, edit %v)", i, final.Amount, payables, settleErr, editErr)
			}
		default:
			t.Fatalf("iteration %d: unexpected status %s", i, final.Status)
		}
	}
}

func TestCancelAndEditRacingNeverLeaveAResidue(t *testing.T) {
	for i := 0; i < 15; i++ {
		f := newBillsFixture(t)
		b := f.payable(t, 100000, brcal.New(2026, time.March, 10))
		bigger := billing.Cents(130000)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = f.bills.Cancel(context.Background(), f.sp, b.ID, brcal.Date{}, repositories.PostMeta{}, time.Now())
		}()
		go func() {
			defer wg.Done()
			_, _ = f.bills.Edit(context.Background(), f.sp, b.ID, repositories.BillEdit{Amount: &bigger}, brcal.Date{}, repositories.PostMeta{}, time.Now())
		}()
		wg.Wait()

		final, _ := f.bills.Get(context.Background(), f.sp, b.ID)
		payables, rent := f.bal(t, "sys-payables"), f.bal(t, "rent")
		switch final.Status {
		case finance.BillCanceled:
			if payables != 0 || rent != 0 {
				t.Fatalf("iteration %d: a cancelled bill left payables %d rent %d", i, payables, rent)
			}
		case finance.BillForecast:
			if payables != -final.Amount || rent != final.Amount {
				t.Fatalf("iteration %d: forecast %d has payables %d rent %d", i, final.Amount, payables, rent)
			}
		default:
			t.Fatalf("iteration %d: unexpected status %s", i, final.Status)
		}
	}
}
