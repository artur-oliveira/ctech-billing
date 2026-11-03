package repositories

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Credit is money the card gives back that is not the refund of a known
// purchase. It lands on the open statement as one negative item, and lowers the
// expense category it names. Amount is positive.
type Credit struct {
	ID, CardID, Description, CategoryID string
	Date                                brcal.Date
	Amount                              billing.Cents
}

// AddCredit records a standalone credit on the card's open statement. With
// meta.IdempotencyKey the credit's id is a function of it, so a retry answers
// with the first one instead of crediting twice.
func (r *CardRepository) AddCredit(ctx context.Context, sp space.ResolvedSpace, cardID string, c Credit, meta PostMeta, now time.Time) (Credit, error) {
	return r.addCredit(ctx, sp, cardID, c, meta, now, nil)
}

func (r *CardRepository) addCredit(ctx context.Context, sp space.ResolvedSpace, cardID string, c Credit, meta PostMeta, now time.Time, extras purchaseExtras) (Credit, error) {
	if err := sp.Require(space.Write); err != nil {
		return Credit{}, err
	}
	card, err := r.activeCard(ctx, sp, cardID)
	if err != nil {
		return Credit{}, err
	}
	if err := r.expenseCategory(ctx, sp, c.CategoryID); err != nil {
		return Credit{}, err
	}
	if c.Description == "" {
		cat, err := r.ledger.getAccount(ctx, sp, c.CategoryID)
		if err != nil {
			return Credit{}, err
		}
		c.Description = cat.Name
	}
	tx, err := finance.CardCredit(card.ID, c.CategoryID, c.Amount, c.Date)
	if err != nil {
		return Credit{}, err
	}
	c.ID, c.CardID = id.New(), card.ID
	if meta.IdempotencyKey != "" {
		c.ID = idempotentID(sp, "card-credit", meta.IdempotencyKey)
	}
	meta.Origin, meta.Memo, meta.Ref = "card_credit", "Crédito: "+c.Description, "credit:"+c.ID
	ledgerPlan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return Credit{}, err
	}
	it, err := Encode(itemRow{
		keys:       newKeys(CardPK(sp, card.ID), ItemSK(card.OpenMonth, c.ID, "credit"), RetentionPermanent, now),
		PurchaseID: c.ID, Description: "Crédito: " + c.Description, CategoryID: c.CategoryID, Date: c.Date.String(),
		Kind: "credit", Amount: -int64(c.Amount),
	})
	if err != nil {
		return Credit{}, err
	}
	items := append([]types.TransactWriteItem(nil), ledgerPlan.Items...)
	itemIdx := len(items)
	items = append(items, r.cards.BuildPutTxItemIfAbsent(it))
	cardIdx := len(items)
	items = append(items, r.cardBump(sp, card.ID, card.OpenMonth, now))
	extraIdx := len(items)
	if extras != nil {
		more, err := extras(Purchase{ID: c.ID, CardID: card.ID})
		if err != nil {
			return Credit{}, err
		}
		items = append(items, more...)
	}
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		if onlyConditionFailed(err) {
			for i := extraIdx; i < len(items) && i < len(codes); i++ {
				if codes[i] == codeConditionFailed {
					return Credit{}, errExtraFailed{index: i - extraIdx}
				}
			}
		}
		switch {
		case !onlyConditionFailed(err):
			return Credit{}, err
		case cardIdx < len(codes) && codes[cardIdx] == codeConditionFailed:
			return Credit{}, ErrCardMoved
		case meta.IdempotencyKey != "" && itemIdx < len(codes) && codes[itemIdx] == codeConditionFailed:
			return c, nil // the same request again: it is already recorded
		}
		return Credit{}, classifyPostCancel(err, ledgerPlan.MarkerIdx)
	}
	return c, nil
}
