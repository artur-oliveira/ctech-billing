//go:build integration

package integration

import (
	"context"
	"reflect"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A statement or a cash flow reads entries only through the end of the window and
// takes the balance as of that day from the monthly summaries. The numbers must be
// exactly those of the unbounded read, which is what the old code did.
func TestWindowedReadsGiveTheUnboundedNumbers(t *testing.T) {
	r := ledgerFor(t, testDB)
	sp := jobSpace(t, newSpaceOrgID(), true)
	seedSpace(t, r, sp)
	ctx, now := context.Background(), time.Now()
	meta := repositories.PostMeta{Actor: "u"}

	if _, err := r.PostOpeningBalance(ctx, sp, "bank", 100000, brcal.New(2026, time.January, 5), meta, now); err != nil {
		t.Fatal(err)
	}
	// Movements in every month from January to June, 31/28-day edges included.
	for i, d := range []brcal.Date{
		brcal.New(2026, time.January, 31), brcal.New(2026, time.February, 1), brcal.New(2026, time.February, 28),
		brcal.New(2026, time.March, 1), brcal.New(2026, time.March, 31), brcal.New(2026, time.April, 1),
		brcal.New(2026, time.May, 15), brcal.New(2026, time.June, 30),
	} {
		if _, err := r.PostTransfer(ctx, sp, "bank", "cash", billing.Cents(1000+i*137), d, meta, now); err != nil {
			t.Fatal(err)
		}
	}

	acct, err := r.GetAccount(ctx, sp, "bank")
	if err != nil {
		t.Fatal(err)
	}
	cash, err := r.GetAccount(ctx, sp, "cash")
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := r.ReversedIDs(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}

	// Statement of February (to is exclusive): Feb 1 .. Mar 1.
	from, to := brcal.New(2026, time.February, 1), brcal.New(2026, time.March, 1)
	full, err := r.EntriesFrom(ctx, sp, "bank", from)
	if err != nil {
		t.Fatal(err)
	}
	want := finance.BuildStatement(acct.Balance, full, from, to)

	cut := finance.MonthOf(to.AddDays(-1))
	through, err := r.BalancesThrough(ctx, sp, map[string]billing.Cents{"bank": acct.Balance}, cut)
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := r.EntriesThrough(ctx, sp, "bank", from, cut.Last(), reversed)
	if err != nil {
		t.Fatal(err)
	}
	got := finance.BuildStatement(through["bank"], bounded, from, to)

	if got.Opening != want.Opening || got.Closing != want.Closing || !reflect.DeepEqual(got.Lines, want.Lines) {
		t.Fatalf("windowed statement differs:\n got  %+v\n want %+v", got, want)
	}
	if len(bounded) >= len(full) {
		t.Fatalf("read %d entries, the unbounded read has %d: the window did not bound the read", len(bounded), len(full))
	}

	// Cash flow of February..March over both cash accounts.
	fm, tm := finance.MonthOf(from), finance.Month{Year: 2026, Month: time.March}
	allFull, allBounded := []finance.Entry{}, []finance.Entry{}
	current := map[string]billing.Cents{"bank": acct.Balance, "cash": cash.Balance}
	for _, id := range []string{"bank", "cash"} {
		f, err := r.EntriesFrom(ctx, sp, id, fm.First())
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.EntriesThrough(ctx, sp, id, fm.First(), tm.Last(), reversed)
		if err != nil {
			t.Fatal(err)
		}
		allFull, allBounded = append(allFull, f...), append(allBounded, b...)
	}
	wantFlow := finance.CashFlow(current, allFull, fm, tm)
	balances, err := r.BalancesThrough(ctx, sp, current, tm)
	if err != nil {
		t.Fatal(err)
	}
	gotFlow := finance.CashFlow(balances, allBounded, fm, tm)
	if gotFlow.OpeningCash != wantFlow.OpeningCash || gotFlow.ClosingCash != wantFlow.ClosingCash || !reflect.DeepEqual(gotFlow.Months, wantFlow.Months) {
		t.Fatalf("windowed cash flow differs:\n got  %+v\n want %+v", gotFlow, wantFlow)
	}
}
