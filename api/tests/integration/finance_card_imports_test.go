//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var (
	march    = finance.Month{Year: 2026, Month: time.March}
	importAt = time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
)

func cl(day int, amount billing.Cents, desc string) statement.CardLine {
	return statement.CardLine{Line: statement.Line{Date: brcal.New(2026, time.March, day), Amount: amount, Description: desc}, BankCategory: "COMPRAS"}
}

func inst(day int, amount billing.Cents, desc string, k, n int) statement.CardLine {
	l := cl(day, amount, desc)
	l.K, l.N = k, n
	return l
}

func (f cardsFixture) importCard(t *testing.T, cardID, key string, lines ...statement.CardLine) repositories.Import {
	t.Helper()
	imp, err := f.imports.ImportCard(context.Background(), f.sp, cardID, march, lines, nil, key, importAt)
	if err != nil {
		t.Fatal(err)
	}
	return imp
}

func TestCardImportStoresPendingLinesAndSkipsDuplicates(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	lines := []statement.CardLine{cl(2, 1000, "MERCADO"), cl(3, 2000, "POSTO"), inst(1, 3000, "SHEIN", 3, 12)}
	imp := f.importCard(t, card.ID, "", lines...)
	if imp.ID == "" || imp.Lines != 3 || imp.Duplicates != 0 || imp.Kind != "card" || imp.StatementMonth != "2026-03" || imp.AccountID != card.ID {
		t.Fatalf("import = %+v", imp)
	}
	got, stored, err := f.imports.Get(ctx, f.sp, imp.ID, importAt)
	if err != nil || got.Pending() != 3 || len(stored) != 3 {
		t.Fatalf("get = %+v, %d lines, %v", got, len(stored), err)
	}
	for _, l := range stored {
		if l.Status != repositories.LinePending {
			t.Errorf("line %d is %s", l.N, l.Status)
		}
	}
	if l := stored[2]; l.K != 3 || l.Of != 12 || l.BankCategory != "COMPRAS" || l.Amount != 3000 {
		t.Fatalf("installment line = %+v", l)
	}
	again := f.importCard(t, card.ID, "", lines...)
	if again.ID != "" || again.Duplicates != 3 || again.Lines != 0 {
		t.Fatalf("the same file again = %+v", again)
	}
}

func TestCardImportOverlappingFileAddsOnlyTheNew(t *testing.T) {
	f, card := newCardsFixture(t)
	f.importCard(t, card.ID, "", cl(2, 1000, "X"), cl(3, 2000, "Y"))
	b := f.importCard(t, card.ID, "", cl(2, 1000, "X"), cl(3, 2000, "Y"), cl(3, 2000, "Y"), cl(4, 500, "Z"))
	if b.Lines != 2 || b.Duplicates != 2 {
		t.Fatalf("overlap = lines %d, duplicates %d; want the second Y and Z new", b.Lines, b.Duplicates)
	}
}

func TestCardImportIdenticalLinesAreTwoLines(t *testing.T) {
	f, card := newCardsFixture(t)
	imp := f.importCard(t, card.ID, "", cl(2, 650, "99APP"), cl(2, 650, "99APP"))
	if imp.Lines != 2 || imp.Duplicates != 0 {
		t.Fatalf("two real identical purchases = %+v", imp)
	}
}

func TestCardImportKeyIncludesTheInstallment(t *testing.T) {
	f, card := newCardsFixture(t)
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "SAME"), inst(2, 1000, "SAME", 2, 6))
	if imp.Lines != 2 || imp.Duplicates != 0 {
		t.Fatalf("à vista and Parcela 2/6 with the same date, amount and text = %+v", imp)
	}
}

func TestCardImportOfRejectedLinesOnlyStoresNothing(t *testing.T) {
	f, card := newCardsFixture(t)
	imp, err := f.imports.ImportCard(context.Background(), f.sp, card.ID, march, nil, []statement.Rejected{{Line: 2, Reason: statement.ReasonDate}}, "", importAt)
	if err != nil || imp.ID != "" || imp.RejectedCount != 1 {
		t.Fatalf("import = %+v, %v", imp, err)
	}
}

func TestCardImportRefusesAClosedFutureOrArchivedStatement(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	line := []statement.CardLine{cl(2, 1000, "X")}
	if _, err := f.imports.ImportCard(ctx, f.sp, card.ID, march.Add(1), line, nil, "", importAt); !errors.Is(err, repositories.ErrStatementNotOpen) {
		t.Fatalf("a future statement: %v", err)
	}
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))
	if _, err := f.imports.ImportCard(ctx, f.sp, card.ID, march, line, nil, "", importAt); !errors.Is(err, repositories.ErrStatementNotOpen) {
		t.Fatalf("a closed statement: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, card.ID, importAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imports.ImportCard(ctx, f.sp, card.ID, march.Add(1), line, nil, "", importAt); !errors.Is(err, repositories.ErrCardArchived) {
		t.Fatalf("an archived card: %v", err)
	}
}

func TestCardImportRefusesAStatementWhosePeriodHasNotBegun(t *testing.T) {
	f, _ := newCardsFixture(t)
	ctx := context.Background()
	elo, err := f.cards.CreateCard(ctx, f.sp, "Elo", finance.Card{ClosingDay: 28, DueDay: 5, PayingAccountID: "bank"}, importAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.CloseNow(ctx, f.sp, elo.ID, march, importAt); err != nil { // closed early: April starts Mar 29
		t.Fatal(err)
	}
	if _, err := f.imports.ImportCard(ctx, f.sp, elo.ID, march.Add(1), []statement.CardLine{cl(2, 1000, "X")}, nil, "", importAt); !errors.Is(err, repositories.ErrStatementNotBegun) {
		t.Fatalf("a statement that has not begun: %v", err)
	}
}

func TestCardImportRetryWithTheSameKeyResumesTheSameImport(t *testing.T) {
	f, card := newCardsFixture(t)
	lines := []statement.CardLine{cl(2, 1000, "X"), cl(3, 2000, "Y")}
	a := f.importCard(t, card.ID, "k", lines...)
	b := f.importCard(t, card.ID, "k", lines...)
	if a.ID == "" || b.ID != a.ID || b.Lines != 2 || b.Duplicates != 0 {
		t.Fatalf("first %+v, retry %+v: a retry must resume the same import and count its own lines as added", a, b)
	}
}
