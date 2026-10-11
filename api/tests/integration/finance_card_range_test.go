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

func months(p repositories.StatementPage) []string {
	out := make([]string, len(p.Statements))
	for i, s := range p.Statements {
		out[i] = s.Month.String()
	}
	return out
}

func TestTheRangeStopsAtTheCardsFirstMonthGoingBack(t *testing.T) {
	f, card := newCardsFixture(t) // first and open month: March 2026
	ctx := context.Background()
	p, err := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.April}, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(months(p)); got != "[2026-04 2026-03]" || p.HasMore {
		t.Fatalf("backward from April = %s has_more %v, want [2026-04 2026-03] and no more", got, p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.February}, 3, true)
	if len(p.Statements) != 0 || p.HasMore {
		t.Fatalf("before the first month = %v %v", months(p), p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2025, Month: time.December}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2026-03 2026-04 2026-05]" || !p.HasMore {
		t.Fatalf("forward from before the card = %s %v", got, p.HasMore)
	}
}

func TestTheRangeReachesTheLastFutureItemOrOpenPlusTwelve(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	// No item beyond: forward stops at open + 12 (2027-03).
	p, _ := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2027, Month: time.January}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2027-01 2027-02 2027-03]" || p.HasMore {
		t.Fatalf("to open+12 = %s %v", got, p.HasMore)
	}
	// 24× from March: the last installment bills on 2028-02, past open + 12.
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 240000, 24)
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2027, Month: time.January}, 3, false)
	if !p.HasMore {
		t.Fatal("an installment beyond open+12 must keep the range open")
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2028, Month: time.January}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2028-01 2028-02]" || p.HasMore {
		t.Fatalf("to the last item = %s %v", got, p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2028, Month: time.March}, 3, false)
	if len(p.Statements) != 0 || p.HasMore {
		t.Fatalf("past the last item = %v %v", months(p), p.HasMore)
	}
}

// Review Focus 1: the last month of a range carries an item and its total.
func TestRangeTotalsEqualTheSingleMonthEndpoint(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // Mar, Apr, May
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 5000, 1)  // Mar
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))       // March closed: a frozen total and a bill
	p, err := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March}, 3, false)
	if err != nil || len(p.Statements) != 3 {
		t.Fatalf("range = %v, %v", months(p), err)
	}
	for _, s := range p.Statements {
		one, err := f.cards.GetStatement(ctx, f.sp, card.ID, s.Month)
		if err != nil {
			t.Fatal(err)
		}
		if s.Total != one.Total || s.Status != one.Status || len(s.Items) != len(one.Items) || s.BillID != one.BillID || s.DueDate != one.DueDate {
			t.Errorf("%s: range %+v vs single %+v", s.Month, s, one)
		}
	}
	if last := p.Statements[2]; last.Month.Month != time.May || len(last.Items) != 1 || last.Total != 30000 {
		t.Fatalf("May (the range's last month) = %+v: its items must be inside the BETWEEN", last)
	}
}

func TestTheRangeEndpointAnswersSummariesAndRefusesNonsense(t *testing.T) {
	f := newFinanceEnv(t) // pinned clock 2026-03-10: the card opens in March
	var bank, food, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &card)
	f.must(t, 201, "POST", "/cards/"+card.ID+"/purchases", fmt.Sprintf(`{"date":"2026-03-09","description":"Geladeira","category_id":%q,"total":30000,"installments":3}`, food.ID), nil)
	f.must(t, 200, "POST", "/cards/"+card.ID+"/close", `{"month":"2026-03"}`, nil)

	var page struct {
		Data []struct {
			Month      string
			Status     string
			Total      int64
			BillID     string `json:"bill_id"`
			ItemsCount int    `json:"items_count"`
			Items      any
		}
		HasMore bool `json:"has_more"`
	}
	f.must(t, 200, "GET", "/cards/"+card.ID+"/statements?from=2026-03&count=3&direction=forward", "", &page)
	if len(page.Data) != 3 || page.Data[0].Status != "closed" || page.Data[0].BillID == "" || page.Data[0].ItemsCount != 1 || page.Data[1].Status != "open" || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	if page.Data[0].Items != nil {
		t.Fatal("a summary carries no items")
	}
	f.must(t, 200, "GET", "/cards/"+card.ID+"/statements?from=2026-03&count=3&direction=backward", "", &page)
	if len(page.Data) != 1 || page.HasMore {
		t.Fatalf("backward from the first month = %+v", page)
	}
	for _, q := range []string{"from=2026-3", "from=2026-03&count=0", "from=2026-03&count=13", "from=2026-03&direction=sideways", ""} {
		if res := f.call(t, "GET", "/cards/"+card.ID+"/statements?"+q, ""); res.status != 422 {
			t.Errorf("?%s = %d %s, want 422", q, res.status, res.body)
		}
	}
	other := newFinanceEnv(t)
	if res := other.call(t, "GET", "/cards/"+card.ID+"/statements?from=2026-03", ""); res.status != 404 {
		t.Fatalf("another space's card = %d, want 404", res.status)
	}
}
