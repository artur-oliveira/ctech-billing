package repositories

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// MaxStatementPage bounds one range read (spec § 4.1).
const MaxStatementPage = 12

// rangeHorizon is how far past the open month the forward bound reaches when no
// item lies further.
const rangeHorizon = 12

// StatementPage is a run of statements in the order asked, and whether another
// page exists past its far end.
type StatementPage struct {
	Statements []Statement
	HasMore    bool
}

// StatementRange reads up to count statements from `from` (inclusive), forward
// or backward, with ONE Query on the card's partition: sort keys between
// STATEMENT#lo and STATEMENT#hi$ hold every statement row and item of those
// months ('$' sorts after '#', so hi's items are inside). Totals are computed
// exactly as GetStatement computes them.
func (r *CardRepository) StatementRange(ctx context.Context, sp space.ResolvedSpace, cardID string, from finance.Month, count int, backward bool) (StatementPage, error) {
	if err := sp.Require(space.Read); err != nil {
		return StatementPage{}, err
	}
	if count < 1 || count > MaxStatementPage {
		return StatementPage{}, fmt.Errorf("%w: a page holds 1..%d statements", finance.ErrInvalidCard, MaxStatementPage)
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return StatementPage{}, err
	}
	first, last := card.FirstMonth, card.OpenMonth.Add(rangeHorizon)
	// The last item's month only matters once the page reaches open + 12.
	extend := func(m finance.Month) error {
		if m.Compare(last) < 0 {
			return nil
		}
		var err error
		last, err = r.lastItemMonth(ctx, sp, card, last)
		return err
	}
	var lo, hi finance.Month
	var page StatementPage
	if backward {
		hi = from
		if err := extend(hi); err != nil {
			return StatementPage{}, err
		}
		if hi.Compare(last) > 0 {
			hi = last
		}
		if hi.Compare(first) < 0 {
			return StatementPage{}, nil
		}
		lo = hi.Add(-(count - 1))
		if lo.Compare(first) < 0 {
			lo = first
		}
		page.HasMore = lo.Compare(first) > 0
	} else {
		lo = from
		if lo.Compare(first) < 0 {
			lo = first
		}
		hi = lo.Add(count - 1)
		if err := extend(hi); err != nil {
			return StatementPage{}, err
		}
		if lo.Compare(last) > 0 {
			return StatementPage{}, nil
		}
		if hi.Compare(last) > 0 {
			hi = last
		}
		page.HasMore = hi.Compare(last) < 0
	}
	raw, err := r.ledger.queryRange(ctx, r.cards, CardPK(sp, card.ID), StatementSK(lo), StatementSK(hi)+"$")
	if err != nil {
		return StatementPage{}, err
	}
	byMonth := map[finance.Month][]map[string]types.AttributeValue{}
	for _, it := range raw {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if m, ok := statementMonthOf(sk.Value); ok {
				byMonth[m] = append(byMonth[m], it)
			}
		}
	}
	for m := lo; m.Compare(hi) <= 0; m = m.Add(1) {
		s, err := buildStatement(card, m, byMonth[m])
		if err != nil {
			return StatementPage{}, err
		}
		page.Statements = append(page.Statements, s)
	}
	if backward {
		slices.Reverse(page.Statements)
	}
	return page, nil
}

// lastItemMonth is the later of floor and the month of the card's last
// STATEMENT# row (a statement or an item): one reverse Query of one row.
func (r *CardRepository) lastItemMonth(ctx context.Context, sp space.ResolvedSpace, card CardRow, floor finance.Month) (finance.Month, error) {
	res, err := r.cards.Query(ctx, QueryOpts{PK: CardPK(sp, card.ID), SKPrefix: "STATEMENT#", ScanIndexForward: false, Limit: 1, ConsistentRead: true})
	if err != nil {
		return floor, err
	}
	for _, it := range res.Items {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if m, ok := statementMonthOf(sk.Value); ok && m.Compare(floor) > 0 {
				return m, nil
			}
		}
	}
	return floor, nil
}

// statementMonthOf reads the month of a STATEMENT#YYYY-MM[#ITEM#…] sort key.
func statementMonthOf(sk string) (finance.Month, bool) {
	rest, ok := strings.CutPrefix(sk, "STATEMENT#")
	if !ok || len(rest) < 7 {
		return finance.Month{}, false
	}
	m, err := finance.ParseMonth(rest[:7])
	return m, err == nil
}
