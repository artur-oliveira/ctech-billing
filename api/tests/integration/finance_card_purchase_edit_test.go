//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func strp(s string) *string { return &s }

// addCategory creates another expense category in the fixture's space.
func (f cardsFixture) addCategory(t *testing.T, a finance.LedgerAccount) {
	t.Helper()
	if err := f.ledger.CreateAccount(context.Background(), f.sp, a, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// summaryNet is an account's debits minus credits in one month, from the
// SUMMARY rows the DRE folds.
func (f cardsFixture) summaryNet(t *testing.T, id string, m finance.Month) billing.Cents {
	t.Helper()
	rows, err := f.ledger.Summaries(context.Background(), f.sp, m, m)
	if err != nil {
		t.Fatal(err)
	}
	var net billing.Cents
	for _, r := range rows {
		if r.AccountID == id {
			net += r.Totals.Debits - r.Totals.Credits
		}
	}
	return net
}

func (f cardsFixture) itemsOf(t *testing.T, cardID, purchaseID string, months ...finance.Month) []repositories.StatementItem {
	t.Helper()
	var out []repositories.StatementItem
	for _, m := range months {
		s, err := f.cards.GetStatement(context.Background(), f.sp, cardID, m)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range s.Items {
			if it.PurchaseID == purchaseID {
				out = append(out, it)
			}
		}
	}
	return out
}

var mar, apr, may = finance.Month{Year: 2026, Month: time.March}, finance.Month{Year: 2026, Month: time.April}, finance.Month{Year: 2026, Month: time.May}

// A description is the purchase's and every statement line's, closed ones too
// (a label, not money); the ledger is not written.
func TestEditingAPurchaseDescriptionRenamesEveryStatementLine(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // March, April, May
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))            // March closed
	got, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{Description: strp("Geladeira nova")}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil || got.Description != "Geladeira nova" || got.TxID != p.TxID || got.CategoryID != "food" {
		t.Fatalf("edit = %+v, %v", got, err)
	}
	items := f.itemsOf(t, card.ID, p.ID, mar, apr, may)
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	for _, it := range items {
		if it.Description != "Geladeira nova" || it.Amount != 30000 {
			t.Fatalf("item = %+v", it)
		}
	}
	list, _ := f.cards.ListPurchases(ctx, f.sp, card.ID)
	if len(list) != 1 || list[0].Description != "Geladeira nova" {
		t.Fatalf("list = %+v", list)
	}
	if f.ledgerBal(t, "food") != 90000 || f.ledgerBal(t, card.ID) != -90000 {
		t.Fatal("a description change must not move money")
	}
}

// A category change moves the whole expense to the new category in the
// purchase's month: the original is reversed and a replacement posted, both on
// the purchase date, in one write; a later refund reverses the replacement.
func TestEditingAPurchaseCategoryMovesTheExpenseInTheDRE(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.addCategory(t, finance.LedgerAccount{ID: "home", Name: "Casa", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses})
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	got, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{CategoryID: strp("home")}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil || got.CategoryID != "home" || got.TxID == p.TxID || got.TxID == "" {
		t.Fatalf("edit = %+v, %v", got, err)
	}
	if f.ledgerBal(t, "food") != 0 || f.ledgerBal(t, "home") != 90000 || f.ledgerBal(t, card.ID) != -90000 {
		t.Fatalf("balances food %d home %d card %d", f.ledgerBal(t, "food"), f.ledgerBal(t, "home"), f.ledgerBal(t, card.ID))
	}
	if f.summaryNet(t, "food", mar) != 0 || f.summaryNet(t, "home", mar) != 90000 {
		t.Fatalf("March: food %d home %d", f.summaryNet(t, "food", mar), f.summaryNet(t, "home", mar))
	}
	if f.summaryNet(t, "food", apr) != 0 || f.summaryNet(t, "home", apr) != 0 {
		t.Fatal("nothing may move outside the purchase's month")
	}
	for _, it := range f.itemsOf(t, card.ID, p.ID, mar, apr, may) {
		if it.CategoryID != "home" {
			t.Fatalf("item = %+v", it)
		}
	}
	// A second change replaces the replacement.
	if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{CategoryID: strp("food")}, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.ledgerBal(t, "food") != 90000 || f.ledgerBal(t, "home") != 0 {
		t.Fatal("back to food")
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.ledgerBal(t, "food") != 0 || f.ledgerBal(t, "home") != 0 || f.ledgerBal(t, card.ID) != 0 {
		t.Fatalf("after refund: food %d home %d card %d", f.ledgerBal(t, "food"), f.ledgerBal(t, "home"), f.ledgerBal(t, card.ID))
	}
}

// An advanced purchase's single "Antecipação" line follows the new description.
func TestEditingAnAdvancedPurchaseRenamesItsAdvanceLine(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	if _, err := f.cards.Advance(ctx, f.sp, card.ID, p.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{Description: strp("Sofá")}, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	items := f.itemsOf(t, card.ID, p.ID, mar)
	want := map[string]bool{"Sofá": true, "Antecipação: Sofá": true}
	if len(items) != 2 || !want[items[0].Description] || !want[items[1].Description] || items[0].Description == items[1].Description {
		t.Fatalf("March items = %+v", items)
	}
}

func TestEditingAPurchaseRefusesWhatItCannotTake(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.addCategory(t, finance.LedgerAccount{ID: "salary", Name: "Salário", Class: finance.ClassIncome, Group: finance.GroupGrossRevenue})
	f.addCategory(t, finance.LedgerAccount{ID: "old", Name: "Antiga", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses})
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "old", time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	meta := repositories.PostMeta{Actor: "u"}
	for _, cat := range []string{"salary", "old", "bank", card.ID} {
		if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{CategoryID: strp(cat)}, meta, time.Now()); !errors.Is(err, finance.ErrInvalidTransaction) {
			t.Fatalf("category %s: %v", cat, err)
		}
	}
	if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{CategoryID: strp("nope")}, meta, time.Now()); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("unknown category: %v", err)
	}
	if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, "missing", repositories.PurchaseEdit{Description: strp("x")}, meta, time.Now()); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("unknown purchase: %v", err)
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, meta, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{Description: strp("x")}, meta, time.Now()); !errors.Is(err, repositories.ErrPurchaseRefunded) {
		t.Fatalf("refunded purchase: %v", err)
	}
	if f.ledgerBal(t, "food") != 0 {
		t.Fatal("nothing refused may post")
	}
}

// The route: PATCH with description and/or category_id, validated like a
// purchase; anything else is an unknown field.
func TestPatchingACardPurchase(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, food, home, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/accounts", `{"name":"Casa","class":"expense","dre_group":"operating_expenses"}`, &home)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &card)
	var p struct {
		ID          string
		Description string
		CategoryID  string `json:"category_id"`
		Total       int64
	}
	f.must(t, 201, "POST", "/cards/"+card.ID+"/purchases",
		fmt.Sprintf(`{"date":"2026-03-09","description":"Geladeira","category_id":%q,"total":30000,"installments":3}`, food.ID), &p)
	path := "/cards/" + card.ID + "/purchases/" + p.ID
	f.must(t, 200, "PATCH", path, fmt.Sprintf(`{"description":"  Sofá ","category_id":%q}`, home.ID), &p)
	if p.Description != "Sofá" || p.CategoryID != home.ID || p.Total != 30000 {
		t.Fatalf("patched = %+v", p)
	}
	var dre struct {
		Groups []struct {
			Categories []struct {
				CategoryID string `json:"category_id"`
				Total      int64
			}
		}
	}
	f.must(t, 200, "GET", "/reports/dre?from=2026-03&to=2026-03", "", &dre)
	byCat := map[string]int64{}
	for _, g := range dre.Groups {
		for _, c := range g.Categories {
			byCat[c.CategoryID] = c.Total
		}
	}
	if byCat[home.ID] != -30000 || byCat[food.ID] != 0 {
		t.Fatalf("DRE by category = %+v", byCat)
	}
	for _, bad := range []string{`{"description":""}`, `{"description":null}`, `{"category_id":null}`, `{"total":100}`, `{"category_id":""}`} {
		if res := f.call(t, "PATCH", path, bad); res.status != 422 && res.status != 400 {
			t.Fatalf("%s = %d %s", bad, res.status, res.body)
		}
	}
	if res := f.call(t, "PATCH", path, fmt.Sprintf(`{"category_id":%q}`, bank.ID)); res.status != 422 {
		t.Fatalf("an asset as category = %d %s", res.status, res.body)
	}
	if res := f.call(t, "PATCH", "/cards/"+card.ID+"/purchases/nope", `{"description":"x"}`); res.status != 404 {
		t.Fatalf("unknown purchase = %d %s", res.status, res.body)
	}
}

// The largest purchase (limits.MaxInstallments) fits one write even with its
// category replaced: the lines, the purchase and the ledger replacement.
func TestRecategorisingTheLargestPurchaseFitsOneWrite(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.addCategory(t, finance.LedgerAccount{ID: "home", Name: "Casa", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses})
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 480000, limits.MaxInstallments)
	got, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{Description: strp("TV"), CategoryID: strp("home")}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil || got.CategoryID != "home" {
		t.Fatalf("edit = %+v, %v", got, err)
	}
	if f.ledgerBal(t, "home") != 480000 || f.ledgerBal(t, "food") != 0 {
		t.Fatal("the expense must move whole")
	}
}

// A refund that read the purchase's transaction before an edit replaced it
// (here: the transaction reversed under it) is told to try again, not that the
// purchase was refunded; the retry refunds what is there now.
func TestARefundRacingACategoryEditIsToldToRetry(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	if _, err := f.ledger.Reverse(ctx, f.sp, p.TxID, p.Date, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); !errors.Is(err, repositories.ErrCardMoved) {
		t.Fatalf("refund = %v, want ErrCardMoved", err)
	}
}

// A statement line missing under the purchase is data drift: an error, never
// "unknown account" (which would tell the person to pick another category).
func TestEditingAPurchaseWithAMissingLineIsNotAnUnknownAccount(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	base := repositories.NewBase(testDB, testCfg, repositories.TableCards)
	if _, err := base.DeleteItem(ctx, repositories.CardPK(f.sp, card.ID), repositories.ItemSK(p.Installments[1].Statement, p.ID, "02")); err != nil {
		t.Fatal(err)
	}
	_, err := f.cards.EditPurchase(ctx, f.sp, card.ID, p.ID, repositories.PurchaseEdit{Description: strp("x")}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err == nil || errors.Is(err, repositories.ErrUnknownAccount) || errors.Is(err, repositories.ErrCardMoved) {
		t.Fatalf("edit with a missing line = %v", err)
	}
}
