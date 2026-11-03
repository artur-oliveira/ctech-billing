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
)

func (f cardsFixture) credit(t *testing.T, cardID, key string, amount int64) repositories.Credit {
	t.Helper()
	c, err := f.cards.AddCredit(context.Background(), f.sp, cardID, repositories.Credit{
		Description: "Estorno", CategoryID: "food", Date: brcal.New(2026, time.March, 2), Amount: billing.Cents(amount),
	}, repositories.PostMeta{Actor: "u", IdempotencyKey: key}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestACreditLowersTheOpenStatementAndTheCard(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	march := finance.Month{Year: 2026, Month: time.March}
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 10000, 1)
	c := f.credit(t, card.ID, "k1", 500)
	if c.ID == "" || c.CardID != card.ID {
		t.Fatalf("credit = %+v", c)
	}
	st, _ := f.cards.GetStatement(ctx, f.sp, card.ID, march)
	if st.Total != 9500 || len(st.Items) != 2 {
		t.Fatalf("statement = total %d, %d items", st.Total, len(st.Items))
	}
	var found bool
	for _, it := range st.Items {
		if it.Kind == "credit" && it.Amount == -500 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no credit item of -500 in %+v", st.Items)
	}
	if f.ledgerBal(t, card.ID) != -9500 || f.ledgerBal(t, "food") != 9500 {
		t.Fatalf("card %d, food %d; want -9500 and 9500", f.ledgerBal(t, card.ID), f.ledgerBal(t, "food"))
	}
}

func TestACreditIsIdempotent(t *testing.T) {
	f, card := newCardsFixture(t)
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 10000, 1)
	a, b := f.credit(t, card.ID, "same", 500), f.credit(t, card.ID, "same", 500)
	if a.ID != b.ID {
		t.Fatalf("ids %s and %s", a.ID, b.ID)
	}
	st, _ := f.cards.GetStatement(context.Background(), f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	if st.Total != 9500 || f.ledgerBal(t, "food") != 9500 {
		t.Fatalf("total %d, food %d: the retry credited twice", st.Total, f.ledgerBal(t, "food"))
	}
}

func TestACreditRefusesAnArchivedCardAndAnUnusableCategory(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx, now := context.Background(), time.Now()
	if err := f.ledger.CreateAccount(ctx, f.sp, finance.LedgerAccount{ID: "salary", Name: "Salário", Class: finance.ClassIncome, Group: finance.GroupGrossRevenue}, now); err != nil {
		t.Fatal(err)
	}
	c := repositories.Credit{Description: "x", CategoryID: "salary", Date: brcal.New(2026, time.March, 2), Amount: 100}
	if _, err := f.cards.AddCredit(ctx, f.sp, card.ID, c, repositories.PostMeta{Actor: "u"}, now); !errors.Is(err, repositories.ErrCategoryUnusable) {
		t.Fatalf("an income category: %v", err)
	}
	c.CategoryID = "food"
	if err := f.ledger.ArchiveAccount(ctx, f.sp, card.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cards.AddCredit(ctx, f.sp, card.ID, c, repositories.PostMeta{Actor: "u"}, now); !errors.Is(err, repositories.ErrCardArchived) {
		t.Fatalf("an archived card: %v", err)
	}
}

func TestACreditLargerThanTheStatementCarriesToTheNextOne(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.credit(t, card.ID, "k", 500)
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))
	mar, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March})
	apr, _ := f.cards.GetStatement(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.April})
	if mar.Total != -500 || mar.BillID != "" || apr.Total != -500 {
		t.Fatalf("march %d (bill %q), april %d; want -500 with no bill and the credit carried", mar.Total, mar.BillID, apr.Total)
	}
}
