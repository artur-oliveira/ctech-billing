package statement

import (
	"math/rand"
	"reflect"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func avista(day int, amount billing.Cents) CardLine {
	return CardLine{Line: Line{Date: brcal.New(2026, 10, day), Amount: amount, Description: "X"}}
}

func parcela(k, n int, amount billing.Cents) CardLine {
	return CardLine{Line: Line{Date: brcal.New(2026, 3, 20), Amount: amount, Description: "Y"}, K: k, N: n}
}

func item(id string, day int, amount billing.Cents, number, of int) CardItem {
	return CardItem{PurchaseID: id, Tag: "i1", Date: brcal.New(2026, 10, day), Amount: amount, Number: number, Of: of}
}

func pairs(s []CardSuggestion) map[int]string {
	out := map[int]string{}
	for _, x := range s {
		if x.Kind == "pair" {
			out[x.Lines[0]] = x.Item.PurchaseID
		}
	}
	return out
}

func TestPairsAVistaBySameAmountWithinTwoDays(t *testing.T) {
	lines := map[int]CardLine{1: avista(26, 7203)}
	for _, tc := range []struct {
		name string
		it   CardItem
		want bool
	}{
		{"same day", item("a", 26, 7203, 1, 1), true},
		{"one day off", item("a", 25, 7203, 1, 1), true},
		{"two days off", item("a", 24, 7203, 1, 1), true},
		{"three days off", item("a", 23, 7203, 1, 1), false},
		{"one cent off", item("a", 26, 7204, 1, 1), false},
	} {
		got := len(pairs(SuggestCard(lines, []CardItem{tc.it}))) == 1
		if got != tc.want {
			t.Errorf("%s: paired = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPairsInstallmentByRemainingAndCents(t *testing.T) {
	lines := map[int]CardLine{1: parcela(8, 12, 30946)} // 5 left
	for _, tc := range []struct {
		name string
		it   CardItem
		want bool
	}{
		{"2 cents off, same remaining", item("a", 11, 30948, 1, 5), true},
		{"5 cents off", item("a", 11, 30951, 1, 5), true},
		{"6 cents off", item("a", 11, 30952, 1, 5), false},
		{"other remaining", item("a", 11, 30948, 1, 6), false},
		{"remaining counted from the item's own number", item("a", 11, 30946, 3, 7), true},
	} {
		got := len(pairs(SuggestCard(lines, []CardItem{tc.it}))) == 1
		if got != tc.want {
			t.Errorf("%s: paired = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGroupsInstallmentLinesThatSumToOneItem(t *testing.T) {
	lines := map[int]CardLine{
		1: parcela(3, 12, 2383), 2: parcela(3, 12, 2698), 3: parcela(3, 12, 3098), 4: parcela(3, 12, 2655),
		5: parcela(3, 12, 9999), // unrelated
	}
	items := []CardItem{item("shein", 11, 10834, 1, 10)}
	got := SuggestCard(lines, items)
	if len(got) != 1 || got[0].Kind != "group" || !reflect.DeepEqual(got[0].Lines, []int{1, 2, 3, 4}) || got[0].Item.PurchaseID != "shein" {
		t.Fatalf("suggestions = %+v", got)
	}
	if g := SuggestCard(lines, []CardItem{item("shein", 11, 10840, 1, 10)}); len(g) != 0 { // 6 cents off
		t.Fatalf("a sum 6 cents off grouped: %+v", g)
	}
	mixed := map[int]CardLine{1: parcela(3, 12, 5000), 2: parcela(4, 12, 5834)} // 10 and 9 left
	if g := SuggestCard(mixed, []CardItem{item("shein", 11, 10834, 1, 10)}); len(g) != 0 {
		t.Fatalf("mixed remaining counts grouped: %+v", g)
	}
}

func TestPaymentLineIsSuggestedAsIgnore(t *testing.T) {
	pay := CardLine{Line: Line{Date: brcal.New(2026, 10, 6), Amount: -505257, Description: "PAGAMENTO ON LINE"}, Payment: true}
	got := SuggestCard(map[int]CardLine{1: pay}, []CardItem{item("a", 6, 505257, 1, 1)})
	if len(got) != 1 || got[0].Kind != "ignore" || got[0].Item != nil || got[0].Lines[0] != 1 {
		t.Fatalf("suggestions = %+v", got)
	}
}

func TestAmbiguityIsDeterministic(t *testing.T) {
	lines := map[int]CardLine{1: avista(25, 5000)}
	items := []CardItem{item("d24", 24, 5000, 1, 1), item("d25", 25, 5000, 1, 1)}
	for i := 0; i < 20; i++ {
		rand.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		if p := pairs(SuggestCard(lines, items)); p[1] != "d25" {
			t.Fatalf("picked %v, want the closest date", p)
		}
	}
	// Two lines, one item: the closer line gets it, the other none.
	two := map[int]CardLine{1: avista(23, 5000), 2: avista(25, 5000)}
	if p := pairs(SuggestCard(two, []CardItem{item("d25", 25, 5000, 1, 1)})); len(p) != 1 || p[2] != "d25" {
		t.Fatalf("pairs = %v, want line 2 to win", p)
	}
	// Equal distance: file order.
	tie := map[int]CardLine{1: avista(24, 5000), 2: avista(26, 5000)}
	if p := pairs(SuggestCard(tie, []CardItem{item("d25", 25, 5000, 1, 1)})); len(p) != 1 || p[1] != "d25" {
		t.Fatalf("pairs = %v, want line 1 on a tie", p)
	}
}

func TestRefundLineHasNoPair(t *testing.T) {
	refund := CardLine{Line: Line{Date: brcal.New(2026, 10, 26), Amount: -500, Description: "IFD"}}
	if got := SuggestCard(map[int]CardLine{1: refund}, []CardItem{item("a", 26, 500, 1, 1)}); len(got) != 0 {
		t.Fatalf("a refund paired: %+v", got)
	}
}
