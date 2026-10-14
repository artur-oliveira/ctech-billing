package byokrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

var ErrSandboxBusy = errors.New("byok: the shared sandbox is busy")

const (
	bankingPerMinute = 8  // Inter banking: 10/min (parecer § 2.4)
	payoutPerMinute  = 30 // Inter payments: 60/min, one a second
	sandboxPerMinute = 8  // Inter sandbox Pix: 10/min for every test tenant together (§ 9)
)

type Limits struct{ counter ratelimit.Counter }

func NewLimits(counter ratelimit.Counter) *Limits { return &Limits{counter: counter} }

func (l *Limits) take(ctx context.Context, key string, max int64, over error) error {
	n, err := l.counter.Incr(ctx, key, time.Minute)
	if err != nil {
		return fmt.Errorf("%w: limiter", byok.ErrUnavailable)
	}
	if n > max {
		return over
	}
	return nil
}

func (l *Limits) Banking(ctx context.Context, conn *byokconn.Connection) error {
	if conn.Kind == byokconn.KindSandboxShared {
		return l.take(ctx, "byok:rate:sandbox:"+conn.Provider+":banking", sandboxPerMinute, ErrSandboxBusy)
	}
	return l.take(ctx, "byok:rate:"+conn.Provider+":"+conn.ID+":banking", bankingPerMinute, byok.ErrRateLimited)
}

// Payout keeps below Inter's payment limit (60 a minute, one a second).
func (l *Limits) Payout(ctx context.Context, conn *byokconn.Connection) error {
	return l.take(ctx, "byok:rate:"+conn.Provider+":"+conn.ID+":payout", payoutPerMinute, byok.ErrRateLimited)
}

func (l *Limits) Pix(ctx context.Context, conn *byokconn.Connection) error {
	if conn.Kind != byokconn.KindSandboxShared {
		return nil // 120/min per integration at Inter; billing's volume per merchant stays far below
	}
	return l.take(ctx, "byok:rate:sandbox:"+conn.Provider+":pix", sandboxPerMinute, ErrSandboxBusy)
}
