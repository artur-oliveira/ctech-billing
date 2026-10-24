package app

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/supervisor"

	"gopkg.aoctech.app/billing/api/internal/services"
)

// closureTick is how often the closure worker looks for due closures.
const closureTick = time.Minute

// RunClosureWorker is the supervisor worker that advances due tenant closures
// until ctx ends. It runs in the API process, in both of an instance's
// processes; that is safe because every transition is a conditional write on the
// state it read, and the purge deletes idempotently, so two ticks racing do the
// work once. A pass runs at once, then every closureTick.
// ponytail: no distributed lock; add one if closures ever run in volume.
func RunClosureWorker(svc *services.TenantClosures) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if svc == nil {
			return nil
		}
		svc.ProcessDue(ctx)
		return supervisor.Every(closureTick, func(ctx context.Context) error {
			svc.ProcessDue(ctx)
			return nil
		})(ctx)
	}
}
