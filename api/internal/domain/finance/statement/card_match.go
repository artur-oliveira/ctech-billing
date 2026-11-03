package statement

import (
	"slices"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// CardItem is an installment-kind item of the card statement being imported
// into that no line has claimed yet: what a line may be paired with.
type CardItem struct {
	PurchaseID, Tag, Description string
	Date                         brcal.Date
	Amount                       billing.Cents
	Number, Of                   int
}

// Remaining is how many installments are left counting this one.
func (i CardItem) Remaining() int { return i.Of - i.Number + 1 }

func (i CardItem) key() string { return i.PurchaseID + "#" + i.Tag }

type SuggestionKind string

const (
	SuggestPair   SuggestionKind = "pair"
	SuggestGroup  SuggestionKind = "group"
	SuggestIgnore SuggestionKind = "ignore"
)

// CardSuggestion is what the import offers for one or more lines: confirm them
// against Item, or ignore them. Lines are 1-based file line numbers.
type CardSuggestion struct {
	Kind  SuggestionKind
	Lines []int
	Item  *CardItem // pair and group; nil for ignore
}

const (
	// CardDateWindow is how many days a purchase date may be from a line's date.
	CardDateWindow = 2
	// CardCentsTolerance is how far an installment, or a group's sum, may be from
	// the item it pairs with (the bank rounds and adds IOF; the person rounds).
	CardCentsTolerance = 5
	// MaxGroupLines and MaxGroupCandidates bound the subset search of a group.
	MaxGroupLines      = 6
	MaxGroupCandidates = 20
)

// SuggestCard offers a pairing for every line it can, deterministically: a line
// pairs with an item of the same remaining count — à vista by exact amount and a
// date within CardDateWindow, an installment by an amount within
// CardCentsTolerance and any date (the person keys an old installment purchase
// on the day they typed it). The closest candidate wins, then file order, then
// item key, and an item is used once. Installment lines left over that sum to
// one item are offered as a group. The previous statement's payment is offered
// as ignore; a refund is offered nothing.
func SuggestCard(lines map[int]CardLine, items []CardItem) []CardSuggestion {
	ns := make([]int, 0, len(lines))
	for n := range lines {
		ns = append(ns, n)
	}
	slices.Sort(ns)

	type edge struct {
		n    int
		item int
		rank int // 0: à vista, with date evidence; 1: installment, by amount alone
		dist int
	}
	var edges []edge
	var out []CardSuggestion
	for _, n := range ns {
		l := lines[n]
		if l.Payment {
			out = append(out, CardSuggestion{Kind: SuggestIgnore, Lines: []int{n}})
			continue
		}
		if l.Amount <= 0 {
			continue
		}
		for i, it := range items {
			if it.Remaining() != l.Remaining() {
				continue
			}
			if l.N > 0 {
				if d := abs(l.Amount - it.Amount); d <= CardCentsTolerance {
					edges = append(edges, edge{n, i, 1, int(d)})
				}
				continue
			}
			if l.Amount != it.Amount {
				continue
			}
			if d := abs64(l.Date.DaysBetween(it.Date)); d <= CardDateWindow {
				edges = append(edges, edge{n, i, 0, d})
			}
		}
	}
	slices.SortFunc(edges, func(a, b edge) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if a.dist != b.dist {
			return a.dist - b.dist
		}
		if a.n != b.n {
			return a.n - b.n
		}
		return strings.Compare(items[a.item].key(), items[b.item].key())
	})
	paired, taken := map[int]bool{}, map[int]bool{}
	for _, e := range edges {
		if paired[e.n] || taken[e.item] {
			continue
		}
		paired[e.n], taken[e.item] = true, true
		it := items[e.item]
		out = append(out, CardSuggestion{Kind: SuggestPair, Lines: []int{e.n}, Item: &it})
	}

	// Groups: unpaired installment lines of one remaining count that sum to one
	// unclaimed item of that count.
	free := map[int][]int{} // remaining -> line numbers, file order
	for _, n := range ns {
		if l := lines[n]; !paired[n] && !l.Payment && l.Amount > 0 && l.N > 0 && l.Remaining() > 1 {
			free[l.Remaining()] = append(free[l.Remaining()], n)
		}
	}
	order := make([]int, 0, len(items))
	for i := range items {
		if !taken[i] && items[i].Remaining() > 1 {
			order = append(order, i)
		}
	}
	slices.SortFunc(order, func(a, b int) int { return strings.Compare(items[a].key(), items[b].key()) })
	for _, i := range order {
		it := items[i]
		cand := free[it.Remaining()]
		if len(cand) > MaxGroupCandidates {
			cand = cand[:MaxGroupCandidates]
		}
		if g := findGroup(cand, lines, it.Amount); g != nil {
			in := map[int]bool{}
			for _, n := range g {
				in[n] = true
			}
			rest := free[it.Remaining()][:0:0]
			for _, n := range free[it.Remaining()] {
				if !in[n] {
					rest = append(rest, n)
				}
			}
			free[it.Remaining()] = rest
			out = append(out, CardSuggestion{Kind: SuggestGroup, Lines: g, Item: &it})
		}
	}
	slices.SortFunc(out, func(a, b CardSuggestion) int { return a.Lines[0] - b.Lines[0] })
	return out
}

// findGroup returns the first set of 2..MaxGroupLines of cand (smallest sets
// first, then file order) whose amounts sum to within CardCentsTolerance of
// target.
func findGroup(cand []int, lines map[int]CardLine, target billing.Cents) []int {
	for size := 2; size <= min(MaxGroupLines, len(cand)); size++ {
		var pick func(start, left int, sum billing.Cents, chosen []int) []int
		pick = func(start, left int, sum billing.Cents, chosen []int) []int {
			if left == 0 {
				if abs(sum-target) <= CardCentsTolerance {
					return slices.Clone(chosen)
				}
				return nil
			}
			for i := start; i <= len(cand)-left; i++ {
				next := sum + lines[cand[i]].Amount
				if next > target+CardCentsTolerance {
					continue
				}
				if g := pick(i+1, left-1, next, append(chosen, cand[i])); g != nil {
					return g
				}
			}
			return nil
		}
		if g := pick(0, size, 0, nil); g != nil {
			return g
		}
	}
	return nil
}

func abs64(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
