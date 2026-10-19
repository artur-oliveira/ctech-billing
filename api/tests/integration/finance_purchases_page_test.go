//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A card keeps every purchase it ever had; the list is read a page at a time, the
// cursor continues exactly where the page ended, and every purchase is seen once.
func TestPurchasesArePagedAndEachIsSeenOnce(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	want := map[string]bool{}
	for i := 0; i < 7; i++ {
		p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1+i), 10000, 1)
		want[p.ID] = true
	}

	seen := map[string]bool{}
	var start map[string]types.AttributeValue
	pages := 0
	for {
		page, err := f.cards.ListPurchasesPage(ctx, f.sp, card.ID, 3, start)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 3 {
			t.Fatalf("a page of %d with limit 3", len(page.Items))
		}
		for _, p := range page.Items {
			if seen[p.ID] {
				t.Fatalf("purchase %s appeared on two pages", p.ID)
			}
			seen[p.ID] = true
		}
		pages++
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	if len(seen) != len(want) || pages != 3 {
		t.Fatalf("saw %d purchases in %d pages, want %d in 3", len(seen), pages, len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("purchase %s was never listed", id)
		}
	}
}

// A screen that needs one purchase asks for it by id, whatever page it would be on.
func TestAPurchaseIsReadByIdAndOnlyOnItsOwnCard(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 30000, 3)

	got, err := f.cards.GetPurchase(ctx, f.sp, card.ID, p.ID)
	if err != nil || got.ID != p.ID || got.Total != 30000 || len(got.Installments) != 3 {
		t.Fatalf("GetPurchase = %+v, %v", got, err)
	}
	if _, err := f.cards.GetPurchase(ctx, f.sp, card.ID, "does-not-exist"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("unknown purchase: %v", err)
	}
	if _, err := f.cards.GetPurchase(ctx, f.sp, "another-card", p.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a purchase read through another card: %v", err)
	}
}
