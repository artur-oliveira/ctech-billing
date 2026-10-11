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
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// cardRecFixture: a space with bank (asset), subs and food (expense) and a card
// "Visa" closing on the 25th, due on the 5th, added on 2026-03-01 (first and
// open month: March 2026).
type cardRecFixture struct {
	sp     space.ResolvedSpace
	ledger *repositories.LedgerRepository
	bills  *repositories.BillRepository
	cards  *repositories.CardRepository
	recs   *repositories.RecurrenceRepository
	card   repositories.CardRow
}

var cardRecNow = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func newCardRecFixture(t *testing.T) cardRecFixture {
	t.Helper()
	f := cardRecFixture{sp: jobSpace(t, newSpaceOrgID(), true), ledger: repositories.NewLedgerRepository(testDB, testCfg),
		bills: repositories.NewBillRepository(testDB, testCfg), cards: repositories.NewCardRepository(testDB, testCfg),
		recs: repositories.NewRecurrenceRepository(testDB, testCfg)}
	ctx := context.Background()
	if err := f.ledger.EnsureSpace(ctx, f.sp, cardRecNow); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "subs", Name: "Assinaturas", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
		{ID: "food", Name: "Mercado", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := f.ledger.CreateAccount(ctx, f.sp, a, cardRecNow); err != nil {
			t.Fatal(err)
		}
	}
	card, err := f.cards.CreateCard(ctx, f.sp, "Visa", finance.Card{ClosingDay: 25, DueDay: 5, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	f.card = card
	return f
}

// cardRecurrence: monthly on `day`, since 2026-03-01, `n` installments.
func (f cardRecFixture) cardRecurrence(t *testing.T, day int, amount billing.Cents, n int) finance.Recurrence {
	t.Helper()
	rec, err := f.recs.Create(context.Background(), f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: amount, CategoryID: "subs", CardID: f.card.ID, Installments: n, Description: "Streaming",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestACardRecurrenceIsStoredAndReadBack(t *testing.T) {
	f := newCardRecFixture(t)
	rec := f.cardRecurrence(t, 28, 3990, 2)
	got, cursor, err := f.recs.GetWithCursor(context.Background(), f.sp, rec.ID)
	if err != nil || got.CardID != f.card.ID || got.AccountID != "" || got.Installments != 2 || !cursor.IsZero() {
		t.Fatalf("read back %+v cursor %s, %v", got, cursor, err)
	}
}

func TestACardRecurrenceRefusesAnArchivedCardAndAnUnusableCategory(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	base := finance.Recurrence{Direction: finance.Payable, Amount: 3990, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}}

	other := base
	other.CardID = "bank" // an asset account is not a card
	if _, err := f.recs.Create(ctx, f.sp, other, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("an account id as the card: %v", err)
	}
	wrongCat := base
	wrongCat.CategoryID = "bank"
	if _, err := f.recs.Create(ctx, f.sp, wrongCat, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an asset as the category: %v", err)
	}
	foreign := jobSpace(t, newSpaceOrgID(), true)
	if err := f.ledger.EnsureSpace(ctx, foreign, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, foreign, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("another space's card: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, f.sp, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an archived card: %v", err)
	}
}

func TestSwitchingARecurrenceBetweenAccountAndCardIsAPatch(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 3990, CategoryID: "subs", AccountID: "bank", AutoSettle: true,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	card := f.card.ID
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.CardID != card || got.AccountID != "" || got.AutoSettle || got.Installments != 1 {
		t.Fatalf("after switching to the card: %+v (auto_settle cleared, installments 1)", got)
	}
	bank, three := "bank", 3
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AccountID: &bank}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("both targets in one patch: %v", err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Installments: &three}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{AccountID: &bank}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ = f.recs.Get(ctx, f.sp, rec.ID)
	if got.AccountID != "bank" || got.CardID != "" || got.Installments != 0 {
		t.Fatalf("after switching back: %+v", got)
	}
	yes := true
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AutoSettle: &yes}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("auto_settle sent with a card: %v", err)
	}
}

func TestNeedsAttentionIsSetByTheJobAndClearedByAnEditOrARun(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	if err := f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCardArchived, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.NeedsAttention != finance.AttentionCardArchived {
		t.Fatalf("needs_attention = %q", got.NeedsAttention)
	}
	desc := "Streaming HD"
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Description: &desc}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("an edit must clear it, got %q", got.NeedsAttention)
	}
	_ = f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCategoryArchived, cardRecNow)
	if err := f.recs.MarkMaterialised(ctx, f.sp, rec.ID, brcal.New(2026, time.March, 28), cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("a run that moved the cursor must clear it, got %q", got.NeedsAttention)
	}
}
