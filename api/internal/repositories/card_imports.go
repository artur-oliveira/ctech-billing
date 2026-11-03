package repositories

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	// ErrStatementNotOpen is a card import into a statement that is not the card's
	// open one: a closed statement is reopened first, a future one is not due yet.
	ErrStatementNotOpen = errors.New("imports: the card's statement is not open")
	// ErrStatementNotBegun is a card import into an open statement whose period
	// has not begun (the previous one was closed before its closing day).
	ErrStatementNotBegun = errors.New("imports: the statement period has not begun")
)

// ImportCard stores a card statement file's lines, pending, under a new import
// for the card's open statement `month`. Each line claims a lock scoped to
// (card, month), so the same file again, or an overlapping export, adds every
// purchase once; two real identical purchases stay two lines. A line's key
// includes its installment ("Parcela 2/6"), so an installment and a purchase
// that share date, amount and text are two lines.
func (r *ImportRepository) ImportCard(ctx context.Context, sp space.ResolvedSpace, cardID string, month finance.Month, lines []statement.CardLine, rejected []statement.Rejected, idempotencyKey string, now time.Time) (Import, error) {
	if err := sp.Require(space.Import); err != nil {
		return Import{}, err
	}
	card, err := r.cards.activeCard(ctx, sp, cardID)
	if err != nil {
		return Import{}, err
	}
	if month != card.OpenMonth {
		return Import{}, ErrStatementNotOpen
	}
	if !brcal.FromTime(now).After(finance.ClosingDate(month.Add(-1), card.ClosingDay)) {
		return Import{}, ErrStatementNotBegun
	}
	scope := cardID + "#" + month.String()
	forKeys := make([]statement.Line, len(lines))
	extra := make(map[int]cardLineExtra, len(lines))
	for i, l := range lines {
		forKeys[i] = l.Line
		if l.N > 0 {
			forKeys[i].Description += " [" + strconv.Itoa(l.K) + "/" + strconv.Itoa(l.N) + "]"
		}
		extra[i+1] = cardLineExtra{K: l.K, Of: l.N, BankCategory: l.BankCategory}
	}
	keyed := statement.Keys(scope, forKeys)
	for i := range keyed {
		keyed[i].Description = lines[i].Description // the key hashed the installment; the line shows the bank's text
	}
	imp := Import{AccountID: cardID, Format: statement.FormatInterCard, Kind: "card", StatementMonth: month.String()}
	return r.store(ctx, sp, imp, scope, keyed, rejected, idempotencyKey, extra, now)
}
