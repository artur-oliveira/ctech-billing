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

// batch caps one pass. The pass is concurrent and an endpoint that fails once in a
// pass has the rest of its deliveries moved down the queue, so a dead endpoint
// costs one timeout, not one per queued delivery.
const batch = 500

// budget is how long one run keeps taking passes while the queue still has work.
// The timer starts a run a minute apart; staying under that is what keeps a run
// from overlapping the next, and a backlog bigger than one run drains over several.
const budget = 45 * time.Second

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

	deliverer.WithConcurrency(cfg.DeliverWorkers)

	// Passes repeat while they find work, up to the budget: a wave of events (a
	// renewal run finalizing thousands of invoices) is more than one pass of
	// `limit`, and one pass a minute would take hours to drain it.
	var live []string
	deadline := time.Now().Add(budget)
	for {
		now := time.Now()
		liveErrs, liveWork := run(ctx, deliverer, true, *limit, now)
		// Test mode is delivered too. A sandbox integration that never receives a
		// webhook cannot be built against, which is the whole point of having one.
		_, testWork := run(ctx, deliverer, false, *limit, now)
		live = append(live, liveErrs...)
		if !(liveWork || testWork) || time.Now().After(deadline) {
			break
		}
	}

	// Only live failures fail the job, matching cmd/sweep. A failing sandbox
	// endpoint is somebody's half-built integration, and paging on it teaches
	// people to ignore the alarm that matters.
	if len(live) > 0 {
		jobs.Fail(ctx, alerter, "deliver",
			fmt.Sprintf("%d webhook pass error(s) — consumers are not hearing about events that already happened", len(live)),
			live)
	}
}

// run executes both passes for one mode and returns the job-level errors and
// whether another pass is worth taking. A failed *delivery* is not one of those: an endpoint answering 500
// is the endpoint's problem, it is recorded on the row, and it will be retried.
// What counts here is this job being unable to do its work at all.
func run(ctx context.Context, d *services.Deliverer, livemode bool, limit int, now time.Time) (errs []string, worked bool) {
	mode := "test"
	if livemode {
		mode = "live"
	}

	// The second pass is fed by the first: a delivery created a moment ago is not
	// yet in the queue an index serves, and read from there it would wait a whole
	// minute for the next run (services.Deliverer.FanOutAndDeliver).
	fan, del := d.FanOutAndDeliver(ctx, livemode, limit, now)
	report(ctx, "webhook fan-out", mode, fan)
	report(ctx, "webhook delivery", mode, del)

	errs = append(append([]string{}, fan.Errors...), del.Errors...)
	// Only a pass that found work and made no job-level error is worth repeating: a
	// row this job cannot process stays in its queue, and another pass at once would
	// meet it again and spin until the budget ran out.
	return errs, fan.Examined+del.Examined > 0 && len(errs) == 0
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
