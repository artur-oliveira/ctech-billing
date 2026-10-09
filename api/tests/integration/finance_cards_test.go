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
func TestAdvanceKeepsTheSum(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 100000, 4) // Mar 25.000 ×4
	if _, err := f.cards.Advance(ctx, f.sp, card.ID, p.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	mar, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	if mar.Total != 100000 {
		t.Fatalf("March after advance = %d, want the whole purchase", mar.Total)
	}
	apr, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.April})
	if len(apr.Items) != 0 {
		t.Fatalf("April still bills %+v", apr.Items)
	}
	if _, err := f.cards.Advance(ctx, f.sp, card.ID, p.ID, time.Now()); !errors.Is(err, repositories.ErrNothingToAdvance) {
		t.Fatalf("second advance: %v", err)
	}
	if f.ledgerBal(t, "food") != 100000 {
		t.Fatal("an advance changes no ledger figure")
	}
}

func TestClosingTwiceMakesOneBill(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 30000, 1)
	for i := 0; i < 3; i++ {
		f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))
	}
	mar, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	if mar.Status != "closed" || mar.Total != 30000 || mar.BillID == "" {
		t.Fatalf("March = %+v", mar)
	}
	b, err := f.bills.Get(ctx, f.sp, mar.BillID)
	if err != nil || b.Origin != finance.OriginCardStatement || b.Amount != 30000 || b.Due != brcal.New(2026, time.March, 10) || b.AccountID != "bank" {
		t.Fatalf("statement bill = %+v, %v", b, err)
	}
	page, _ := f.bills.ListOpen(ctx, f.sp, finance.Payable, 10, nil)
	if len(page.Items) != 1 {
		t.Fatalf("open payables = %d, want one statement bill", len(page.Items))
	}
	if f.ledgerBal(t, "sys-payables") != 0 {
		t.Fatal("a statement bill must not be recognised: the purchase already was")
	}
}

func TestAMissedMonthIsClosedInOrder(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 20000, 2) // March and April
	if n, err := f.cards.CloseDue(ctx, f.sp, card.ID, brcal.New(2026, time.April, 20), time.Now()); err != nil || n != 2 {
		t.Fatalf("closed %d, %v; want March and April", n, err)
	}
	got, _ := f.cards.GetCard(ctx, f.sp, card.ID)
	if got.OpenMonth != (finance.Month{Year: 2026, Month: time.May}) {
		t.Fatalf("open month = %v", got.OpenMonth)
	}
}

func TestALatePurchaseLandsOnTheOpenStatement(t *testing.T) {
	f, card := newCardsFixture(t)
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3)) // March closed (empty)
	p := f.purchase(t, card.ID, brcal.New(2026, time.February, 20), 5000, 1)
	if p.Installments[0].Statement != (finance.Month{Year: 2026, Month: time.April}) {
		t.Fatalf("a purchase dated in a closed month landed on %v", p.Installments[0].Statement)
	}
}

func TestAPurchaseAfterTheCloseReadIsRefused(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	// The close read the card at version v; a purchase lands; the close's write
	// must fail and the next run must count the purchase.
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 1000, 1)
	read, _ := f.cards.GetCard(ctx, f.sp, card.ID)
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 2000, 1)
	if err := f.cards.CloseAt(ctx, f.sp, *read, brcal.New(2026, time.March, 3), time.Now()); !errors.Is(err, repositories.ErrCardMoved) {
		t.Fatalf("close against a stale read: %v", err)
	}
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))
	mar, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	if mar.Total != 3000 {
		t.Fatalf("March = %d, want both purchases", mar.Total)
	}
}

func TestANegativeStatementCarriesToTheNext(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 30000, 1)
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.closeThrough(t, card.ID, brcal.New(2026, time.April, 3)) // April: −300,00
	apr, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.April})
	may, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.May})
	if apr.BillID != "" || apr.Total != -30000 || may.Total != -30000 || may.Items[0].Kind != "carry" {
		t.Fatalf("April = %+v, May = %+v", apr, may)
	}
}

// closeThrough closes every statement whose closing date is on or before d.
func (f cardsFixture) closeThrough(t *testing.T, cardID string, d brcal.Date) {
	t.Helper()
	if _, err := f.cards.CloseDue(context.Background(), f.sp, cardID, d, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRefundCreditPlusRemovedIsTheTotal(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 100000, 4) // March, April, May, June
	f.closeThrough(t, card.ID, brcal.New(2026, time.April, 3))             // March and April closed
	got, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil || !got.Refunded {
		t.Fatalf("refund = %+v, %v", got, err)
	}
	may, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.May})
	if may.Total != -50000 || len(may.Items) != 1 || may.Items[0].Kind != "credit" {
		t.Fatalf("May after refund = %+v (credit for March and April, May and June removed)", may)
	}
	june, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.June})
	if len(june.Items) != 0 {
		t.Fatalf("June still bills %+v", june.Items)
	}
	if f.ledgerBal(t, "food") != 0 {
		t.Fatal("the refund must reverse the expense")
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); !errors.Is(err, repositories.ErrPurchaseRefunded) {
		t.Fatalf("second refund: %v", err)
	}
}

// "Fechar fatura agora" names the month it closes: a second click, a second tab
// or a replay must not close the next statements early, and a statement whose
// period has not begun (the previous one closed in the future) cannot close.
func TestClosingNowClosesOnlyTheNamedOpenMonth(t *testing.T) {
	f, _ := newCardsFixture(t)
	ctx, now := context.Background(), time.Now()
	card, err := f.cards.CreateCard(ctx, f.sp, "Elo", finance.Card{ClosingDay: 28, DueDay: 5, PayingAccountID: "bank"}, now)
	if err != nil {
		t.Fatal(err)
	}
	open := card.OpenMonth
	if _, err := f.cards.CloseNow(ctx, f.sp, card.ID, open.Add(1), now); !errors.Is(err, repositories.ErrNotClosable) {
		t.Fatalf("closing a month that is not open: %v", err)
	}
	if _, err := f.cards.CloseNow(ctx, f.sp, card.ID, open, now); err != nil {
		t.Fatal(err)
	}
	// The next statement's period starts after this month's closing day, which
	// is still ahead (the card closes on the 28th).
	if finance.ClosingDate(open, 28).After(brcal.FromTime(now)) {
		if _, err := f.cards.CloseNow(ctx, f.sp, card.ID, open.Add(1), now); !errors.Is(err, repositories.ErrNotClosable) {
			t.Fatalf("closing a statement whose period has not begun: %v", err)
		}
	}
}

func TestRefundAfterAdvanceLeavesNothingBilled(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3)
	if _, err := f.cards.Advance(ctx, f.sp, card.ID, p.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	mar, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	if mar.Total != 0 || len(mar.Items) != 0 {
		t.Fatalf("March after advance then refund = %+v, want nothing billed", mar)
	}
}
