package main

import (
	"context"

	"gopkg.aoctech.app/billing/api/internal/pendingpush"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// pendingLister is the slice of repositories.RecurrenceRepository the counter reads.
type pendingLister interface {
	ListPendingOccurrences(ctx context.Context, sp space.ResolvedSpace, limit int) ([]repositories.PendingOccurrence, error)
}

// counter implements pendingpush.Counter over a person's own live space. A job builds its space from an operator's decision,
// not a request (space.ForJob, allowed under cmd/); here the decision is "the space of the person who set the reminder".
//
// ponytail: personal space only. Organization spaces need the person's memberships from ctech-account (an M2M client in
// this Lambda); add them when asked, summing per space the same way.
type counter struct{ recs pendingLister }

func newCounter(r pendingLister) *counter { return &counter{recs: r} }

// Pending counts the recurrences waiting for the person's confirmation, reading at most PendingCap+1.
func (c *counter) Pending(ctx context.Context, sub string) (int, error) {
	sp, err := space.ForJob("USER#"+sub, true)
	if err != nil {
		return 0, err
	}
	rows, err := c.recs.ListPendingOccurrences(ctx, sp, pendingpush.PendingCap+1)
	return len(rows), err
}
