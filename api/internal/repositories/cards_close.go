package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrNothingToAdvance is an advance of a purchase with no installment after
// the open statement.
var ErrNothingToAdvance = errors.New("cards: no installment is left to advance")

// Advance (antecipação) brings every installment billed after the open month
// onto the open statement, as one item. It changes allocation only: the ledger
// already holds the whole purchase.
func (r *CardRepository) Advance(ctx context.Context, sp space.ResolvedSpace, cardID, purchaseID string, now time.Time) (Purchase, error) {
	if err := sp.Require(space.Write); err != nil {
		return Purchase{}, err
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return Purchase{}, err
	}
	row, err := r.getPurchaseItem(ctx, sp, cardID, purchaseID)
	if err != nil {
		return Purchase{}, err
	}
	if row.Refunded {
		return Purchase{}, ErrPurchaseRefunded
	}
	p, err := row.purchase(cardID)
	if err != nil {
		return Purchase{}, err
	}
	moved := finance.Advance(p.Installments, card.OpenMonth)
	advanced := map[int]bool{}
	for _, i := range row.Installments {
		advanced[i.Number] = i.Advanced
	}
	pk := CardPK(sp, cardID)
	var items []types.TransactWriteItem
	var sum billing.Cents
	for i, inst := range p.Installments {
		if inst.Statement == moved[i].Statement {
			continue
		}
		items = append(items, r.cards.BuildDeleteTxItem(pk, ItemSK(inst.Statement, p.ID, installmentTag(inst.Number))))
		sum += inst.Amount
		advanced[inst.Number] = true
	}
	if sum == 0 {
		return Purchase{}, ErrNothingToAdvance
	}
	adv, err := Encode(itemRow{
		keys:       newKeys(pk, ItemSK(card.OpenMonth, p.ID, "adv"), RetentionPermanent, now),
		PurchaseID: p.ID, Description: "Antecipação: " + p.Description, CategoryID: p.CategoryID,
		Date: brcal.FromTime(now).String(), Kind: "advance", Amount: int64(sum),
	})
	if err != nil {
		return Purchase{}, err
	}
	items = append(items, r.cards.BuildPutTxItemIfAbsent(adv))
	plan, err := attributevalue.Marshal(installmentItems(moved, advanced))
	if err != nil {
		return Purchase{}, err
	}
	sk := PurchaseSK(p.ID)
	purchaseIdx := len(items)
	items = append(items, r.cards.BuildRawUpdateTxItem(pk, &sk, "SET installments = :plan, updated_at = :now", "attribute_not_exists(refunded)", nil,
		map[string]types.AttributeValue{":plan": plan, ":now": str(now.UTC().Format(time.RFC3339Nano))}))
	cardIdx := len(items)
	items = append(items, r.cardBump(sp, cardID, card.OpenMonth, now))
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		switch {
		case !onlyConditionFailed(err):
			return Purchase{}, err
		case purchaseIdx < len(codes) && codes[purchaseIdx] == codeConditionFailed:
			return Purchase{}, ErrPurchaseRefunded
		case cardIdx < len(codes) && codes[cardIdx] == codeConditionFailed:
			return Purchase{}, ErrCardMoved
		}
		return Purchase{}, ErrNothingToAdvance
	}
	p.Installments = moved
	return p, nil
}

// CloseAt closes read.OpenMonth: one transaction freezes the total, creates the
// statement bill (positive) or carries the credit to the next month (negative),
// and moves the open month — guarded by the open month AND the version read, so
// a purchase written after the items were summed makes this fail with
// ErrCardMoved instead of being left out of a frozen total. Exported for the
// race test; callers use CloseDue or CloseNow.
func (r *CardRepository) CloseAt(ctx context.Context, sp space.ResolvedSpace, read CardRow, today brcal.Date, now time.Time) error {
	m := read.OpenMonth
	s, err := r.statement(ctx, sp, read, m)
	if err != nil {
		return err
	}
	closing, due := finance.ClosingDate(m, read.ClosingDay), finance.DueDate(m, read.ClosingDay, read.DueDay)
	var items []types.TransactWriteItem
	billID := ""
	switch {
	case s.Total > 0:
		ref := finance.StatementRef(read.ID, m)
		billID = idempotentID(sp, "statement", ref)
		bill, err := r.bills.statementBillItem(sp, finance.Bill{
			ID: billID, Direction: finance.Payable, Amount: s.Total, AccountID: read.PayingAccountID, CategoryID: read.ID,
			Description: "Fatura " + read.Name + " " + m.String(), Competence: closing, Due: due,
			Status: finance.BillForecast, Origin: finance.OriginCardStatement, OriginRef: ref, PaymentGroup: ref,
		}, now)
		if err != nil {
			return err
		}
		items = append(items, bill)
	case s.Total < 0:
		carry, err := Encode(itemRow{
			keys:       newKeys(CardPK(sp, read.ID), ItemSK(m.Add(1), "carry", m.String()), RetentionPermanent, now),
			PurchaseID: "carry", Description: "Crédito da fatura anterior", Date: closing.String(), Kind: "carry", Amount: int64(s.Total),
		})
		if err != nil {
			return err
		}
		items = append(items, r.cards.BuildPutTxItemIfAbsent(carry))
	}
	row, err := Encode(statementRow{
		keys:  newKeys(CardPK(sp, read.ID), StatementSK(m), RetentionPermanent, now),
		Total: int64(s.Total), ClosedOn: today.String(), BillID: billID,
	})
	if err != nil {
		return err
	}
	items = append(items, r.cards.BuildPutTxItemIfAbsent(row))
	next := read.Card
	next.OpenMonth = m.Add(1)
	sk := CardSK(read.ID)
	items = append(items, r.cards.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET open_month = :next, schedule_sk = :ssk, updated_at = :now", "open_month = :m AND version = :v", nil,
		map[string]types.AttributeValue{
			":next": str(next.OpenMonth.String()), ":m": str(m.String()), ":v": numberValue(read.Version),
			":ssk": str(closeScheduleSK(sp, next)), ":now": str(now.UTC().Format(time.RFC3339Nano)),
		}))
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return ErrCardMoved
		}
		return err
	}
	return nil
}

// closeRetries bounds the retries of one close that keeps losing to purchases
// on the same card; the next run catches up whatever is left.
const closeRetries = 3

// CloseDue closes, in order, every statement of the card whose closing date is
// on or before today (a job that missed days catches up), and says how many.
func (r *CardRepository) CloseDue(ctx context.Context, sp space.ResolvedSpace, cardID string, today brcal.Date, now time.Time) (int, error) {
	if err := sp.Require(space.Write); err != nil {
		return 0, err
	}
	closed, misses := 0, 0
	for {
		card, err := r.loadCard(ctx, sp, cardID)
		if err != nil {
			return closed, err
		}
		if finance.ClosingDate(card.OpenMonth, card.ClosingDay).After(today) {
			return closed, nil
		}
		switch err := r.CloseAt(ctx, sp, card, today, now); {
		case err == nil:
			closed, misses = closed+1, 0
		case errors.Is(err, ErrCardMoved) && misses < closeRetries:
			misses++
		default:
			return closed, err
		}
	}
}

// CloseNow closes the open statement today, before its closing day (the
// console's "Fechar fatura agora"), and returns it closed.
func (r *CardRepository) CloseNow(ctx context.Context, sp space.ResolvedSpace, cardID string, now time.Time) (Statement, error) {
	if err := sp.Require(space.Write); err != nil {
		return Statement{}, err
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return Statement{}, err
	}
	if err := r.CloseAt(ctx, sp, card, brcal.FromTime(now), now); err != nil {
		return Statement{}, err
	}
	return r.GetStatement(ctx, sp, cardID, card.OpenMonth)
}

// DueCard is a card whose open statement's closing day has arrived.
type DueCard struct {
	Space  space.ResolvedSpace
	CardID string
}

// DueToClose reads the close schedule: the job's only cross-tenant read here.
func (r *CardRepository) DueToClose(ctx context.Context, livemode bool, today brcal.Date, limit int) ([]DueCard, int, error) {
	entries, skipped, err := scanSchedule(ctx, r.cards, ClosePK(livemode), livemode, today, limit)
	if err != nil {
		return nil, skipped, err
	}
	out := make([]DueCard, len(entries))
	for i, e := range entries {
		out[i] = DueCard{Space: e.Space, CardID: e.ID}
	}
	return out, skipped, nil
}
