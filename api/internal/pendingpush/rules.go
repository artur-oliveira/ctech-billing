package pendingpush

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/pushsvc"
)

const (
	// PendingCap is the largest count the push states exactly; past it the text says "mais de 99" (the read is bounded
	// the same way, one more than this).
	PendingCap = 99
	// inboxPath is the screen that lists the recurrences waiting for confirmation.
	inboxPath = "/finance/inbox"
)

// Counter says how many recurrences wait for a person's confirmation. The tick implements it over the repository (the
// space of a job is built in cmd/, see space.ForJob), bounded to PendingCap+1.
type Counter interface {
	Pending(ctx context.Context, sub string) (int, error)
}

// Rules decides, for one due reminder, whether a push goes out and what it says.
type Rules struct{ counter Counter }

// NewRules returns the Billing reminder rules.
func NewRules(c Counter) *Rules { return &Rules{counter: c} }

// Decide returns the push for a due reminder, or nil when nothing waits. A failed read is an error, never "nothing
// pending": the day stays open for the catch-up run. The text carries a count only, never a name or an amount (a lock
// screen shows it).
func (r *Rules) Decide(ctx context.Context, now time.Time, rem pushsvc.Reminder) (*pushsvc.Job, error) {
	if rem.Kind != Kind {
		return nil, nil
	}
	n, err := r.counter.Pending(ctx, rem.Sub)
	if err != nil || n == 0 {
		return nil, err
	}
	title := fmt.Sprintf("%d contas aguardando confirmação", n)
	switch {
	case n > PendingCap:
		title = fmt.Sprintf("Mais de %d contas aguardando confirmação", PendingCap)
	case n == 1:
		title = "1 conta aguardando confirmação"
	}
	day := pushsvc.Today(now)
	return &pushsvc.Job{
		EventID: Kind + ":" + rem.Sub + ":" + day, Type: Kind, Title: title, Body: "Toque para conferir.", Navigate: inboxPath,
		Tag: Kind + "-" + day, Topic: Kind, TTLSeconds: max(60, int(pushsvc.UntilMidnight(now).Seconds())),
		Urgency: "normal", Recipients: []string{rem.Sub},
	}, nil
}
