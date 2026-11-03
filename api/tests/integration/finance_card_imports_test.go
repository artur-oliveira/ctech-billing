//go:build integration

package integration

import (
	"context"
	"errors"
	"strconv"
	"strings"
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

var meta = repositories.PostMeta{Actor: "u"}

func (f cardsFixture) suggestions(t *testing.T, importID string) []statement.CardSuggestion {
	t.Helper()
	_, _, s, err := f.imports.CardSuggestions(context.Background(), f.sp, importID, importAt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f cardsFixture) line(t *testing.T, importID string, n int) repositories.ImportLine {
	t.Helper()
	_, lines, err := f.imports.Get(context.Background(), f.sp, importID, importAt)
	if err != nil {
		t.Fatal(err)
	}
	return lines[n-1]
}

func (f cardsFixture) marchTotal(t *testing.T, cardID string) billing.Cents {
	t.Helper()
	st, err := f.cards.GetStatement(context.Background(), f.sp, cardID, march)
	if err != nil {
		t.Fatal(err)
	}
	return st.Total
}

func TestCardImportSuggestsOnlyUnclaimedInstallmentItems(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	if _, err := f.cards.AddCredit(ctx, f.sp, card.ID, repositories.Credit{Description: "c", CategoryID: "food", Date: brcal.New(2026, time.March, 2), Amount: 300}, meta, importAt); err != nil {
		t.Fatal(err)
	}
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"), cl(2, 1000, "A"), cl(2, 300, "B"))
	s := f.suggestions(t, imp.ID)
	if len(s) != 1 || s[0].Kind != statement.SuggestPair || s[0].Lines[0] != 1 || s[0].Item.PurchaseID != p.ID {
		t.Fatalf("suggestions = %+v; want line 1 paired with the purchase, nothing for line 2 (item used once) or line 3 (a credit is never a target)", s)
	}
	if _, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, []int{1}, s[0].Item.PurchaseID, s[0].Item.Tag, importAt); err != nil {
		t.Fatal(err)
	}
	if left := f.suggestions(t, imp.ID); len(left) != 0 {
		t.Fatalf("after confirming, suggestions = %+v; a claimed item must not be offered again", left)
	}
}

func TestCardImportConfirmPairLinksAndPostsNothing(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"))
	foodBefore, cardBefore := f.ledgerBal(t, "food"), f.ledgerBal(t, card.ID)
	s := f.suggestions(t, imp.ID)
	lines, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, []int{1}, s[0].Item.PurchaseID, s[0].Item.Tag, importAt)
	if err != nil || len(lines) != 1 || lines[0].Status != repositories.LineLinked || lines[0].BillID != p.ID {
		t.Fatalf("lines = %+v, %v", lines, err)
	}
	if f.ledgerBal(t, "food") != foodBefore || f.ledgerBal(t, card.ID) != cardBefore {
		t.Fatal("confirming a pair moved the ledger")
	}
	got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, importAt)
	if got.Resolved != 1 || got.Pending() != 0 {
		t.Fatalf("import = %+v", got)
	}
}

func TestCardImportConfirmPairTwiceOnOneItemIsPairTaken(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"), cl(2, 1000, "A"))
	s := f.suggestions(t, imp.ID)
	it := s[0].Item
	if _, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, []int{1}, it.PurchaseID, it.Tag, importAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, []int{2}, it.PurchaseID, it.Tag, importAt); !errors.Is(err, repositories.ErrPairTaken) {
		t.Fatalf("second claim: %v", err)
	}
	if f.line(t, imp.ID, 1).Status != repositories.LineLinked || f.line(t, imp.ID, 2).Status != repositories.LinePending {
		t.Fatal("the first stays linked and the second pending")
	}
}

func TestCardImportConfirmGroupResolvesAllLinesWithOneClaim(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 108340, 10) // 10.834 on March
	imp := f.importCard(t, card.ID, "",
		inst(1, 2383, "SHEIN", 3, 12), inst(1, 2698, "SHEIN", 3, 12), inst(1, 3098, "SHEIN", 3, 12), inst(1, 2655, "SHEIN", 3, 12))
	s := f.suggestions(t, imp.ID)
	if len(s) != 1 || s[0].Kind != statement.SuggestGroup || len(s[0].Lines) != 4 || s[0].Item.PurchaseID != p.ID {
		t.Fatalf("suggestions = %+v", s)
	}
	lines, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, s[0].Lines, p.ID, s[0].Item.Tag, importAt)
	if err != nil || len(lines) != 4 {
		t.Fatalf("lines = %+v, %v", lines, err)
	}
	got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, importAt)
	if got.Resolved != 4 {
		t.Fatalf("resolved = %d, want 4", got.Resolved)
	}
}

func TestCardImportConfirmAllConfirmsEverySuggestedPairAndGroup(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 154740, 5) // 5 × 309,48
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"), inst(1, 30946, "ALI", 8, 12), cl(3, 777, "NOPAIR"))
	n, err := f.imports.ConfirmAll(ctx, f.sp, imp.ID, importAt)
	if err != nil || n != 2 {
		t.Fatalf("confirmed = %d, %v; want 2", n, err)
	}
	if again, err := f.imports.ConfirmAll(ctx, f.sp, imp.ID, importAt); err != nil || again != 0 {
		t.Fatalf("second confirm-all = %d, %v; want 0", again, err)
	}
	if f.line(t, imp.ID, 3).Status != repositories.LinePending {
		t.Fatal("a line with no pair stays pending")
	}
}

func TestCardImportManualPurchaseKeyedAfterTheImportIsFoundOnReview(t *testing.T) {
	f, card := newCardsFixture(t)
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"))
	if s := f.suggestions(t, imp.ID); len(s) != 0 {
		t.Fatalf("suggestions before the purchase = %+v", s)
	}
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	if s := f.suggestions(t, imp.ID); len(s) != 1 || s[0].Kind != statement.SuggestPair {
		t.Fatalf("suggestions after the purchase = %+v", s)
	}
}

func TestCardImportCreateInstallmentLineKeysRemainingInstallments(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	imp := f.importCard(t, card.ID, "", inst(20, 30946, "DL ALIEXPRESS", 8, 12))
	line, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "food", "", meta, importAt)
	if err != nil || line.Status != repositories.LineCreated || line.BillID == "" {
		t.Fatalf("line = %+v, %v", line, err)
	}
	p, err := f.cards.GetPurchase(ctx, f.sp, card.ID, line.BillID)
	if err != nil || p.Total != 154730 || len(p.Installments) != 5 {
		t.Fatalf("purchase = %+v, %v; want 5 × 309,46 = 1.547,30", p, err)
	}
	if p.Installments[0].Statement != march || p.Installments[0].Amount != 30946 {
		t.Fatalf("first installment = %+v; want 309,46 on the imported statement", p.Installments[0])
	}
	if !strings.Contains(p.Description, "8/12") {
		t.Fatalf("description %q lost the bank's installment", p.Description)
	}
	if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "food", "", meta, importAt); !errors.Is(err, repositories.ErrLineResolved) {
		t.Fatalf("a second create: %v", err)
	}
	if got := f.marchTotal(t, card.ID); got != 30946 {
		t.Fatalf("statement total = %d, want one installment", got)
	}
}

func TestCardImportCreateAVistaLineUsesTheFileDate(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "MERCADO"))
	line, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "food", "Mercado do bairro", meta, importAt)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := f.cards.GetPurchase(ctx, f.sp, card.ID, line.BillID)
	if p.Date != brcal.New(2026, time.March, 2) || p.Total != 1000 || p.Description != "Mercado do bairro" || len(p.Installments) != 1 {
		t.Fatalf("purchase = %+v", p)
	}
}

func TestCardImportCreateRefundLineMakesACredit(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	imp := f.importCard(t, card.ID, "", cl(2, -500, "IFD"))
	if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "food", "", meta, importAt); err != nil {
		t.Fatal(err)
	}
	if got := f.marchTotal(t, card.ID); got != 500 {
		t.Fatalf("statement total = %d, want 1000 − 500", got)
	}
	if f.ledgerBal(t, "food") != 500 {
		t.Fatalf("food = %d, want 500", f.ledgerBal(t, "food"))
	}
}

func TestCardImportCreateRefusesThePaymentLineAndAMissingCategory(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	imp := f.importCard(t, card.ID, "", cl(2, -505257, "PAGAMENTO ON LINE"), cl(3, 1000, "X"))
	if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "food", "", meta, importAt); !errors.Is(err, repositories.ErrPaymentLine) {
		t.Fatalf("payment line: %v", err)
	}
	if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 2, "", "", meta, importAt); !errors.Is(err, repositories.ErrCategoryUnmapped) {
		t.Fatalf("no category: %v", err)
	}
}

func TestCardImportResolvedStaysResolvedWhenThePurchaseIsRefunded(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 1000, 1)
	lines := []statement.CardLine{cl(2, 1000, "A")}
	imp := f.importCard(t, card.ID, "", lines...)
	s := f.suggestions(t, imp.ID)
	if _, err := f.imports.ConfirmPair(ctx, f.sp, imp.ID, []int{1}, p.ID, s[0].Item.Tag, importAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, meta, importAt); err != nil {
		t.Fatal(err)
	}
	if f.line(t, imp.ID, 1).Status != repositories.LineLinked {
		t.Fatal("the line must stay resolved")
	}
	if again := f.importCard(t, card.ID, "", lines...); again.ID != "" || again.Duplicates != 1 {
		t.Fatalf("re-import = %+v; a resolved line is never offered again", again)
	}
}

func TestCardImportIgnoreAndReopenReuseTheExistingFlow(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "X"))
	if l, err := f.imports.Ignore(ctx, f.sp, imp.ID, 1, importAt); err != nil || l.Status != repositories.LineIgnored {
		t.Fatalf("ignore = %+v, %v", l, err)
	}
	if l, err := f.imports.Reopen(ctx, f.sp, imp.ID, 1, importAt); err != nil || l.Status != repositories.LinePending {
		t.Fatalf("reopen = %+v, %v", l, err)
	}
}

func TestCardImportTotalAfterCreatingEverythingMatchesTheFileExcludingIgnored(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "A"), inst(1, 3000, "B", 3, 12), cl(3, -500, "C"), cl(3, -505257, "PAGAMENTO ON LINE"))
	for n := 1; n <= 3; n++ {
		if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, n, "food", "", meta, importAt); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
	}
	if _, err := f.imports.Ignore(ctx, f.sp, imp.ID, 4, importAt); err != nil {
		t.Fatal(err)
	}
	if got := f.marchTotal(t, card.ID); got != 1000+3000-500 {
		t.Fatalf("statement total = %d, want the file sum without the ignored payment (3.500)", got)
	}
}

func TestCardBankCategoriesPutThenGetRoundTrips(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	if got, err := f.imports.GetBankCategories(ctx, f.sp, card.ID); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
	if err := f.imports.PutBankCategories(ctx, f.sp, card.ID, map[string]string{"SUPERMERCADO": "food", "COMPRAS": "food"}, importAt); err != nil {
		t.Fatal(err)
	}
	got, err := f.imports.GetBankCategories(ctx, f.sp, card.ID)
	if err != nil || len(got) != 2 || got["SUPERMERCADO"] != "food" {
		t.Fatalf("got = %v, %v", got, err)
	}
}

func TestCardBankCategoriesRefuseAnArchivedOrIncomeCategoryAndTooMany(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	for _, a := range []finance.LedgerAccount{
		{ID: "salary", Name: "Salário", Class: finance.ClassIncome, Group: finance.GroupGrossRevenue},
		{ID: "old", Name: "Antiga", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := f.ledger.CreateAccount(ctx, f.sp, a, importAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "old", importAt); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"salary", "old", "nope"} {
		if err := f.imports.PutBankCategories(ctx, f.sp, card.ID, map[string]string{"X": id}, importAt); err == nil {
			t.Errorf("category %q was accepted", id)
		}
	}
	many := map[string]string{}
	for i := 0; i <= 50; i++ {
		many[strconv.Itoa(i)] = "food"
	}
	if err := f.imports.PutBankCategories(ctx, f.sp, card.ID, many, importAt); !errors.Is(err, repositories.ErrInvalidBankCategories) {
		t.Fatalf("51 entries: %v", err)
	}
	if err := f.imports.PutBankCategories(ctx, f.sp, card.ID, map[string]string{"": "food"}, importAt); !errors.Is(err, repositories.ErrInvalidBankCategories) {
		t.Fatalf("an empty bank category: %v", err)
	}
}

func TestCardImportCreateWithoutCategoryUsesTheMapping(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	if err := f.imports.PutBankCategories(ctx, f.sp, card.ID, map[string]string{"COMPRAS": "food"}, importAt); err != nil {
		t.Fatal(err)
	}
	other := cl(3, 500, "Y")
	other.BankCategory = "OUTROS" // mapped nowhere
	imp := f.importCard(t, card.ID, "", cl(2, 1000, "X"), other)
	line, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 1, "", "", meta, importAt)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := f.cards.GetPurchase(ctx, f.sp, card.ID, line.BillID); p.CategoryID != "food" {
		t.Fatalf("category = %q, want the mapped one", p.CategoryID)
	}
	if _, err := f.imports.CreateFromCardLine(ctx, f.sp, imp.ID, 2, "", "", meta, importAt); !errors.Is(err, repositories.ErrCategoryUnmapped) {
		t.Fatalf("an unmapped bank category: %v", err)
	}
}
