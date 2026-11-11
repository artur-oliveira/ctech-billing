package services

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type cardStore interface {
	DueToClosePage(ctx context.Context, livemode bool, today brcal.Date, limit int, start map[string]types.AttributeValue) ([]repositories.DueCard, int, map[string]types.AttributeValue, error)
	CloseDue(ctx context.Context, sp space.ResolvedSpace, cardID string, today brcal.Date, now time.Time) (int, error)
	ListCards(ctx context.Context, sp space.ResolvedSpace) ([]repositories.CardRow, error)
	GetStatement(ctx context.Context, sp space.ResolvedSpace, cardID string, m finance.Month) (repositories.Statement, error)
	GetCard(ctx context.Context, sp space.ResolvedSpace, cardID string) (*repositories.CardRow, error)
	PostOccurrence(ctx context.Context, sp space.ResolvedSpace, ch finance.CardCharge, meta repositories.PostMeta, now time.Time) (bool, repositories.Purchase, error)
}

// activeCards is the space's active cards by id, read once per projection.
func (j *FinanceJobs) activeCards(ctx context.Context, sp space.ResolvedSpace) (map[string]repositories.CardRow, error) {
	if j.cards == nil {
		return nil, nil
	}
	rows, err := j.cards.ListCards(ctx, sp)
	if err != nil {
		return nil, err
	}
	out := make(map[string]repositories.CardRow, len(rows))
	for _, c := range rows {
		if !c.Archived {
			out[c.ID] = c
		}
	}
	return out, nil
}

// unplanned is what a statement holds that nothing scheduled: single-installment
// purchases not posted by a recurrence. Installments of an older purchase and
// recurrence charges are projected on their own. The projection never guesses
// this part: it only reports how much of each statement it is, so a person who
// chooses to forecast one-off spending can subtract what is already there.
func unplanned(items []repositories.StatementItem) (sum billing.Cents) {
	for _, it := range items {
		if it.Kind == "installment" && it.Of <= 1 && it.RecurrenceID == "" {
			sum += it.Amount
		}
	}
	return sum
}

// projectCards adds each card's open and future statements to the payables of
// the month they fall due: the card has to be paid. Closed statements are
// already bills and counted with them. One Query per statement month.
func (j *FinanceJobs) projectCards(ctx context.Context, sp space.ResolvedSpace, cards map[string]repositories.CardRow, windowEnd brcal.Date, add func(c repositories.CardRow, due brcal.Date, total, unplanned billing.Cents)) error {
	for _, c := range cards {
		for m := c.OpenMonth; ; m = m.Add(1) {
			due := finance.DueDate(m, c.ClosingDay, c.DueDay)
			if due.After(windowEnd) {
				break
			}
			s, err := j.cards.GetStatement(ctx, sp, c.ID, m)
			if err != nil {
				return err
			}
			add(c, due, s.Total, unplanned(s.Items))
		}
	}
	return nil
}

// WithCards gives the job the card statements to close (6.5).
func (j *FinanceJobs) WithCards(c cardStore) *FinanceJobs {
	j.cards = c
	return j
}

// CloseStatements is the job's third step (spec § 5): every card whose open
// statement's closing day has arrived closes it — and any it missed, in order.
// Re-runnable: a closed month is past the card's open month, and the statement
// bill's id is a function of (space, card, month). One card's failure is
// reported and the run carries on.
func (j *FinanceJobs) CloseStatements(ctx context.Context, livemode bool, today brcal.Date, now time.Time) JobResult {
	var res JobResult
	if j.cards == nil {
		return res
	}
	runDue(ctx, j.workers, &res, "close",
		func(d repositories.DueCard) string { return d.Space.PK() },
		func(d repositories.DueCard) string { return d.CardID },
		func(start map[string]types.AttributeValue) ([]repositories.DueCard, int, map[string]types.AttributeValue, error) {
			return j.cards.DueToClosePage(ctx, livemode, today, jobPage, start)
		},
		func(d repositories.DueCard, res *JobResult) {
			if j.skip(ctx, d.Space, res, "card "+d.CardID) {
				return
			}
			n, err := j.cards.CloseDue(ctx, d.Space, d.CardID, today, now)
			res.Done += n
			if err != nil {
				res.fail("card %s: %v", d.CardID, err)
			}
		})
	return res
}
