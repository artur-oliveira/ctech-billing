package app

import (
	"context"
	"time"

	"gopkg.aoctech.app/billing/api/internal/services"
)

// closureTick is how often the closure worker looks for due closures.
const closureTick = time.Minute

// RunClosureWorker advances due tenant closures until ctx ends. It runs in the
// API process, in both of an instance's processes; that is safe because every
// transition is a conditional write on the state it read, and the purge deletes
// idempotently, so two ticks racing do the work once.
// ponytail: no distributed lock; add one if closures ever run in volume.
func RunClosureWorker(ctx context.Context, svc *services.TenantClosures) {
	if svc == nil {
		return
	}
	t := time.NewTicker(closureTick)
	defer t.Stop()
	for {
		svc.ProcessDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
