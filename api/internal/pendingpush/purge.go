package pendingpush

import (
	"context"
	"errors"
)

// UserPurger is pushsvc.Store: PurgeUser deletes every push row of a person.
type UserPurger interface {
	PurgeUser(ctx context.Context, sub string) (int, error)
}

// Purger erases a person's Web Push data (account erasure): every browser subscription, the count row and every reminder.
type Purger struct{ store UserPurger }

// NewPurger returns a Purger.
func NewPurger(s UserPurger) *Purger { return &Purger{store: s} }

// Purge deletes the person's push rows and returns how many there were. Idempotent, so the saga can retry it.
func (p *Purger) Purge(ctx context.Context, sub string) (int, error) {
	if sub == "" {
		return 0, errors.New("pendingpush: empty subject")
	}
	return p.store.PurgeUser(ctx, sub)
}
