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

// cardBaselineMonths is how many closed statements the unplanned-spending
// estimate averages.
const cardBaselineMonths = 3

// unplanned is what a statement holds that nothing scheduled: single-installment
// purchases not posted by a recurrence. Installments of an older purchase and
// recurrence charges are already projected on their own.
func unplanned(items []repositories.StatementItem) (sum billing.Cents) {
	for _, it := range items {
		if it.Kind == "installment" && it.Of <= 1 && it.RecurrenceID == "" {
			sum += it.Amount
		}
	}
	return sum
}

// cardBaseline is the card's usual unplanned spending per statement: the mean of
// its last closed statements' unplanned total. Zero when the card has no closed
// statement yet, so a new card projects nothing it cannot back.
func (j *FinanceJobs) cardBaseline(ctx context.Context, sp space.ResolvedSpace, c repositories.CardRow) (billing.Cents, error) {
	var total billing.Cents
	var n int
	for m := c.OpenMonth.Add(-1); n < cardBaselineMonths && m.Compare(c.FirstMonth) >= 0; m = m.Add(-1) {
		s, err := j.cards.GetStatement(ctx, sp, c.ID, m)
		if err != nil {
			return 0, err
		}
		total += unplanned(s.Items)
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return total / billing.Cents(n), nil
}

// projectCards adds each card's open and future statements to the payables of
// the month they fall due: the card has to be paid. Closed statements are
// already bills and counted with them. `estimate` is what the card usually
// takes beyond what is posted: its baseline less the unplanned spending already
// on that statement, never negative. One Query per statement month plus
// cardBaselineMonths per card.
func (j *FinanceJobs) projectCards(ctx context.Context, sp space.ResolvedSpace, cards map[string]repositories.CardRow, windowEnd brcal.Date, add func(due brcal.Date, total, estimate billing.Cents)) error {
	for _, c := range cards {
		baseline, err := j.cardBaseline(ctx, sp, c)
		if err != nil {
			return err
		}
		for m := c.OpenMonth; ; m = m.Add(1) {
			due := finance.DueDate(m, c.ClosingDay, c.DueDay)
			if due.After(windowEnd) {
				break
			}
			s, err := j.cards.GetStatement(ctx, sp, c.ID, m)
			if err != nil {
				return err
			}
			add(due, s.Total, max(baseline-unplanned(s.Items), 0))
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
