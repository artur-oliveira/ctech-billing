//go:build integration

package integration

import (
	"context"
	"errors"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type cardsFixture struct {
	sp     space.ResolvedSpace
	ledger *repositories.LedgerRepository
	bills  *repositories.BillRepository
	cards  *repositories.CardRepository
}

// newCardsFixture: a space with bank (asset), food (expense) and a card "Visa"
// closing on the 3rd, due on the 10th, opened in March 2026.
func newCardsFixture(t *testing.T) (cardsFixture, repositories.CardRow) {
	t.Helper()
	f := cardsFixture{sp: jobSpace(t, newSpaceOrgID(), true), ledger: repositories.NewLedgerRepository(testDB, testCfg),
		bills: repositories.NewBillRepository(testDB, testCfg), cards: repositories.NewCardRepository(testDB, testCfg)}
	ctx := context.Background()
	march := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	if err := f.ledger.EnsureSpace(ctx, f.sp, march); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "food", Name: "Mercado", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := f.ledger.CreateAccount(ctx, f.sp, a, march); err != nil {
			t.Fatal(err)
		}
	}
	card, err := f.cards.CreateCard(ctx, f.sp, "Visa", finance.Card{ClosingDay: 3, DueDay: 10, PayingAccountID: "bank"}, march)
	if err != nil {
		t.Fatal(err)
	}
	return f, card
}

func TestACardIsALiabilityAccountWithItsSettings(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	acct, err := f.ledger.GetAccount(ctx, f.sp, card.ID)
	if err != nil || acct.Class != finance.ClassLiability || acct.Name != "Visa" {
		t.Fatalf("account = %+v, %v", acct, err)
	}
	if card.OpenMonth != (finance.Month{Year: 2026, Month: time.March}) {
		t.Fatalf("open month = %v", card.OpenMonth)
	}
	list, err := f.cards.ListCards(ctx, f.sp)
	if err != nil || len(list) != 1 || list[0].Name != "Visa" || list[0].DueDay != 10 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := f.cards.CreateCard(ctx, f.sp, "Ruim", finance.Card{ClosingDay: 3, DueDay: 10, PayingAccountID: "food"}, time.Now()); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("a category as the paying account: %v", err)
	}
	other := jobSpace(t, newSpaceOrgID(), true)
	if _, err := f.cards.GetCard(ctx, other, card.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("another space's card: %v", err)
	}
	due := 15
	up, err := f.cards.UpdateCard(ctx, f.sp, card.ID, repositories.CardPatch{DueDay: &due}, time.Now())
	if err != nil || up.DueDay != 15 || up.ClosingDay != 3 {
		t.Fatalf("update = %+v, %v", up, err)
	}
	_ = brcal.Date{}
}
func TestAPurchaseInInstallmentsPostsOnceAndBillsMonthly(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p, err := f.cards.AddPurchase(ctx, f.sp, card.ID, repositories.Purchase{
		Description: "Geladeira", CategoryID: "food", Date: brcal.New(2026, time.March, 5), Total: 120000,
		Installments: make([]finance.Installment, 3),
	}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// After the closing day (3rd): first installment on April.
	if p.Installments[0].Statement != (finance.Month{Year: 2026, Month: time.April}) {
		t.Fatalf("first statement = %v", p.Installments[0].Statement)
	}
	if f.ledgerBal(t, "food") != 120000 || f.ledgerBal(t, card.ID) != -120000 {
		t.Fatal("the purchase must post its full amount once")
	}
	s, err := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.May})
	if err != nil || s.Status != "future" || s.Total != 40000 || len(s.Items) != 1 || s.Items[0].Number != 2 || s.Items[0].Of != 3 {
		t.Fatalf("May = %+v, %v", s, err)
	}
	if s.DueDate != brcal.New(2026, time.May, 10) || s.ClosingDate != brcal.New(2026, time.May, 3) {
		t.Fatalf("May dates = %s / %s", s.ClosingDate, s.DueDate)
	}
}

func TestRefundingAnUnbilledPurchaseRemovesItsInstallments(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // March, April, May; nothing closed
	got, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil || !got.Refunded {
		t.Fatalf("refund = %+v, %v", got, err)
	}
	for _, m := range []time.Month{time.March, time.April, time.May} {
		s, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: m})
		if len(s.Items) != 0 {
			t.Fatalf("%s still bills %+v (nothing was billed, so no credit either)", m, s.Items)
		}
	}
	if f.ledgerBal(t, "food") != 0 || f.ledgerBal(t, card.ID) != 0 {
		t.Fatal("the refund must reverse the purchase")
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); !errors.Is(err, repositories.ErrPurchaseRefunded) {
		t.Fatalf("second refund: %v", err)
	}
}
func (f cardsFixture) ledgerBal(t *testing.T, id string) billing.Cents {
	return balance(t, f.ledger, f.sp, id)
}

func (f cardsFixture) purchase(t *testing.T, cardID string, d brcal.Date, total billing.Cents, n int) repositories.Purchase {
	t.Helper()
	p, err := f.cards.AddPurchase(context.Background(), f.sp, cardID, repositories.Purchase{
		Description: "Compra", CategoryID: "food", Date: d, Total: total, Installments: make([]finance.Installment, n),
	}, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
