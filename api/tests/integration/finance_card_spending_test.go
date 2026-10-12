//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A card's spending by statement month: its statement lines in the range, one
// key-condition Query on the card's partition. A carried credit is not
// spending; a refund's credit lowers the month it is billed on.
func TestCardSpendLinesAreItsStatementLines(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // Mar, Apr, May 30.000
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 5000, 1)       // Mar
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))            // March closed
	if _, err := f.cards.Refund(ctx, f.sp, card.ID, p.ID, repositories.PostMeta{Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err) // April: credit -30.000; April and May installments removed
	}
	mar, may := finance.Month{Year: 2026, Month: time.March}, finance.Month{Year: 2026, Month: time.May}
	lines, err := f.cards.SpendLines(ctx, f.sp, card.ID, mar, may)
	if err != nil {
		t.Fatal(err)
	}
	r := finance.CardSpending(lines, mar, may)
	if r.ByMonth[0] != 35000 || r.ByMonth[1] != -30000 || r.ByMonth[2] != 0 || r.Total != 5000 {
		t.Fatalf("by month = %v, total %d", r.ByMonth, r.Total)
	}
	for _, l := range lines {
		if l.CardID != card.ID || l.CategoryID != "food" {
			t.Fatalf("line = %+v", l)
		}
	}
}

// An advance bills the rest on the open statement: the whole amount counts
// there, and nothing on the statements it left.
func TestAnAdvanceCountsOnTheStatementItMovedTo(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	p := f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // Mar, Apr, May
	if _, err := f.cards.Advance(ctx, f.sp, card.ID, p.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	mar, may := finance.Month{Year: 2026, Month: time.March}, finance.Month{Year: 2026, Month: time.May}
	lines, err := f.cards.SpendLines(ctx, f.sp, card.ID, mar, may)
	if err != nil {
		t.Fatal(err)
	}
	r := finance.CardSpending(lines, mar, may)
	if r.ByMonth[0] != 90000 || r.ByMonth[1] != 0 || r.ByMonth[2] != 0 {
		t.Fatalf("by month = %v", r.ByMonth)
	}
}

func TestTheCardSpendingReport(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, food, home, visa, master struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/accounts", `{"name":"Casa","class":"expense","dre_group":"operating_expenses"}`, &home)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &visa)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Master","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &master)
	f.must(t, 201, "POST", "/cards/"+visa.ID+"/purchases", fmt.Sprintf(`{"date":"2026-03-09","description":"Geladeira","category_id":%q,"total":30000,"installments":3}`, food.ID), nil)
	f.must(t, 201, "POST", "/cards/"+master.ID+"/purchases", fmt.Sprintf(`{"date":"2026-03-09","description":"Sofá","category_id":%q,"total":50000,"installments":1}`, home.ID), nil)
	type row struct {
		CategoryID string `json:"category_id"`
		CardID     string `json:"card_id"`
		Amounts    []int64
		Total      int64
	}
	var all struct {
		Basis      string
		Months     []string
		ByMonth    []int64 `json:"by_month"`
		Total      int64
		Categories []row
		Cards      []row
	}
	f.must(t, 200, "GET", "/reports/card-spending?from=2026-03&to=2026-04", "", &all)
	if all.Basis != "statement" || len(all.Months) != 2 || all.Total != 70000 || all.ByMonth[0] != 60000 || all.ByMonth[1] != 10000 {
		t.Fatalf("all cards = %+v", all)
	}
	if len(all.Categories) != 2 || all.Categories[0].CategoryID != home.ID || all.Categories[1].Total != 20000 ||
		len(all.Cards) != 2 || all.Cards[0].CardID != master.ID {
		t.Fatalf("rows = %+v / %+v", all.Categories, all.Cards)
	}
	f.must(t, 200, "GET", "/reports/card-spending?from=2026-03&to=2026-04&card_id="+visa.ID, "", &all)
	if all.Total != 20000 || len(all.Cards) != 1 || len(all.Categories) != 1 || all.Categories[0].CategoryID != food.ID {
		t.Fatalf("one card = %+v", all)
	}
	if res := f.call(t, "GET", "/reports/card-spending?from=2026-03&to=2026-04&card_id=nope", ""); res.status != 404 {
		t.Fatalf("unknown card = %d %s", res.status, res.body)
	}
	if res := f.call(t, "GET", "/reports/card-spending?from=2026-05&to=2026-04", ""); res.status != 422 {
		t.Fatalf("bad range = %d %s", res.status, res.body)
	}
}
