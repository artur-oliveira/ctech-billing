package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// PostOccurrence posts one occurrence of a card recurrence as a purchase on its
// own date (spec § 3.2). The purchase id comes from the occurrence ref, and the
// transaction also writes the OCCURRENCE# lock row the bill path writes and a
// ConditionCheck on the recurrence as the job read it. So an occurrence posts
// once however often or concurrently the job runs, on whichever card, and never
// as both a bill and a purchase. created is false when it was already posted.
func (r *CardRepository) PostOccurrence(ctx context.Context, sp space.ResolvedSpace, ch finance.CardCharge, meta PostMeta, now time.Time) (bool, Purchase, error) {
	ref := finance.OccurrenceRef(ch.RecurrenceID, ch.Nominal)
	meta.IdempotencyKey = ref
	in := Purchase{Date: ch.Nominal, Description: ch.Description, CategoryID: ch.CategoryID, Total: ch.Amount,
		Installments: make([]finance.Installment, ch.Installments), RecurrenceID: ch.RecurrenceID, OriginRef: ref}
	p, err := r.addPurchase(ctx, sp, ch.CardID, in, meta, now, func(p Purchase) ([]types.TransactWriteItem, error) {
		lock, err := Encode(occurrenceLockRow{
			keys:       newKeys(sp.PK(), OccurrenceSK(ch.RecurrenceID, ch.Nominal), RetentionPermanent, now),
			PurchaseID: p.ID, CardID: p.CardID, Statement: p.Installments[0].Statement.String(), Amount: int64(p.Total),
		})
		if err != nil {
			return nil, err
		}
		return []types.TransactWriteItem{r.bills.bills.BuildPutTxItemIfAbsent(lock), r.bills.cardRecurrenceUnchanged(sp, ch)}, nil
	})
	var extra errExtraFailed
	switch {
	case errors.As(err, &extra) && extra.index == 0:
		return false, Purchase{}, nil // already posted: a re-run, a race, or before a switch of card
	case errors.As(err, &extra):
		return false, Purchase{}, ErrRecurrenceChanged
	case err != nil:
		return false, Purchase{}, err
	}
	return true, p, nil
}

// cardRecurrenceUnchanged ties a card posting to the recurrence the job read:
// still there, not archived, same card, amount, category and installments.
func (r *BillRepository) cardRecurrenceUnchanged(sp space.ResolvedSpace, ch finance.CardCharge) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: aws.String(r.recs.TableName),
		Key:       map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(RecurrenceSK(ch.RecurrenceID))},
		ConditionExpression: aws.String("attribute_exists(pk) AND (attribute_not_exists(#arch) OR #arch = :f) AND " +
			"#card = :card AND #amt = :amt AND #cat = :cat AND #n = :n"),
		ExpressionAttributeNames: map[string]string{"#arch": "archived", "#card": "card_id", "#amt": "amount", "#cat": "category_id", "#n": "installments"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":f": &types.AttributeValueMemberBOOL{Value: false}, ":card": str(ch.CardID),
			":amt": numberValue(int64(ch.Amount)), ":cat": str(ch.CategoryID), ":n": numberValue(int64(ch.Installments)),
		},
	}}
}
