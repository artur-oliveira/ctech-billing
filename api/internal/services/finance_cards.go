package services

import (
	"context"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type cardCloser interface {
	DueToClose(ctx context.Context, livemode bool, today brcal.Date, limit int) ([]repositories.DueCard, int, error)
	CloseDue(ctx context.Context, sp space.ResolvedSpace, cardID string, today brcal.Date, now time.Time) (int, error)
	ListCards(ctx context.Context, sp space.ResolvedSpace) ([]repositories.CardRow, error)
	GetStatement(ctx context.Context, sp space.ResolvedSpace, cardID string, m finance.Month) (repositories.Statement, error)
}

// projectCards adds each card's open and future statements to the payables of
// the month they fall due: the card has to be paid. Closed statements are
// already bills and counted with them. One Query per statement month, for the
// few months the window covers.
func (j *FinanceJobs) projectCards(ctx context.Context, sp space.ResolvedSpace, windowEnd brcal.Date, add func(due brcal.Date, amount billing.Cents)) error {
	if j.cards == nil {
		return nil
	}
	cards, err := j.cards.ListCards(ctx, sp)
	if err != nil {
		return err
	}
	for _, c := range cards {
		if c.Archived {
			continue
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
			if s.Total > 0 {
				add(due, s.Total)
			}
		}
	}
	return nil
}

// WithCards gives the job the card statements to close (6.5).
func (j *FinanceJobs) WithCards(c cardCloser) *FinanceJobs {
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
	due, skipped, err := j.cards.DueToClose(ctx, livemode, today, jobBatch)
	res.Skipped += skipped
	if err != nil {
		res.fail("reading the close list: %v", err)
		return res
	}
	for _, d := range due {
		res.Examined++
		n, err := j.cards.CloseDue(ctx, d.Space, d.CardID, today, now)
		res.Done += n
		if err != nil {
			res.fail("card %s: %v", d.CardID, err)
		}
	}
	return res
}
