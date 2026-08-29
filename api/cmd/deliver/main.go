// Command deliver runs the outbound webhook passes once and exits.
//
// Two passes per mode: fan-out matches queued events to endpoints, delivery
// makes one HTTP attempt each. It is a one-shot binary on a timer rather than a
// long-running worker for the same reason cmd/sweep and cmd/reconcile are — it
// needs the service's configuration and role, it has no HTTP surface to
// mis-scope, and a job that crashes is restarted by the next tick instead of
// leaving a dead goroutine nobody notices.
//
// Both queues are cross-tenant. That is deliberate and it is the same asymmetry
// ADR 0002 already accepts for the sweep: a delivery backlog spans every tenant
// that has one, and this process holds no credential to scope it by.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// batch caps one pass. Small enough that a run finishes inside its timer
// interval even when every delivery times out — 200 × 10s of timeout would not,
// which is how a job starts overlapping itself.
const batch = 50

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	limit := flag.Int("limit", batch, "maximum rows per pass")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}

	ctx := context.Background()
	alerter := jobs.Alerts(ctx, cfg)
	deliverer, err := app.BuildDeliverer(ctx, cfg)
	if err != nil {
		jobs.Startup(ctx, alerter, "deliver", err)
	}

	now := time.Now()
	live := run(ctx, deliverer, true, *limit, now)
	// Test mode is delivered too. A sandbox integration that never receives a
	// webhook cannot be built against, which is the whole point of having one.
	run(ctx, deliverer, false, *limit, now)

	// Only live failures fail the job, matching cmd/sweep. A failing sandbox
	// endpoint is somebody's half-built integration, and paging on it teaches
	// people to ignore the alarm that matters.
	if len(live) > 0 {
		jobs.Fail(ctx, alerter, "deliver",
			fmt.Sprintf("%d webhook pass error(s) — consumers are not hearing about events that already happened", len(live)),
			live)
	}
}

// run executes both passes for one mode and returns how many job-level errors
// occurred. A failed *delivery* is not one of those: an endpoint answering 500
// is the endpoint's problem, it is recorded on the row, and it will be retried.
// What counts here is this job being unable to do its work at all.
func run(ctx context.Context, d *services.Deliverer, livemode bool, limit int, now time.Time) []string {
	mode := "test"
	if livemode {
		mode = "live"
	}

	fan := d.FanOut(ctx, livemode, limit, now)
	report(ctx, "webhook fan-out", mode, fan)

	del := d.Deliver(ctx, livemode, limit, now)
	report(ctx, "webhook delivery", mode, del)

	return append(append([]string{}, fan.Errors...), del.Errors...)
}

func report(ctx context.Context, what, mode string, res services.PassResult) {
	level := slog.LevelInfo
	if len(res.Errors) > 0 {
		level = slog.LevelError
	}
	// One line per error rather than a joined string: a pass that failed on
	// twelve tenants is twelve things to look at, and a truncated line is how
	// eleven of them go unread.
	for _, e := range res.Errors {
		slog.Log(ctx, level, what+" error", "mode", mode, "error", e)
	}
	slog.Log(ctx, level, what+" finished",
		"mode", mode,
		"examined", res.Examined,
		"handled", res.Handled,
		"deferred", res.Deferred,
		"failed", res.Failed)
}
