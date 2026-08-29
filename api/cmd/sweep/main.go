// Command sweep runs the daily invoice generation sweep once and exits.
//
// It is a separate binary rather than a route on the API because the sweep is
// cross-tenant by design (ADR 0002): it reads `schedule-index`, the one index
// whose partition key does not begin with a tenant prefix. Every other read
// path in this service resolves its tenant from a credential and cannot express
// a cross-tenant query. Keeping the sweep off the HTTP surface keeps that true —
// there is no route to mis-scope, and a scheduler that can run the job does not
// thereby hold a token that can read anybody's data.
//
// It is safe to re-run for the same date. Each invoice is written under a
// generation key, so a second run for a period that is already billed is
// counted as skipped, not billed twice. That is what makes a missed day
// recoverable by simply running it again with -date.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // billing decides "today" in America/Sao_Paulo, on any host

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// actor is the audit actor recorded on every transition this job causes. It
// matches billing.CauseScheduler so an audit reader can tell a scheduled
// renewal from an operator's action without joining anything.
const actor = "scheduler"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dateFlag := flag.String("date", "", "civil date to sweep (YYYY-MM-DD, America/Sao_Paulo); defaults to today")
	flag.Parse()

	date := brcal.Today()
	if *dateFlag != "" {
		parsed, err := brcal.Parse(*dateFlag)
		if err != nil {
			slog.Error("invalid -date", "value", *dateFlag, "error", err)
			os.Exit(2)
		}
		date = parsed
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}

	ctx := context.Background()
	alerter := jobs.Alerts(ctx, cfg)
	invoicer, err := app.BuildInvoicer(ctx, cfg)
	if err != nil {
		jobs.Startup(ctx, alerter, "sweep", err)
	}

	now := time.Now()
	live := run(ctx, invoicer, true, date, now)
	// Test mode is swept too, and on the same schedule: a sandbox subscription
	// that never produces an invoice cannot be built against, which defeats the
	// point of having a test mode at all.
	run(ctx, invoicer, false, date, now)

	// Only live failures fail the job. Sandbox data is deliberately
	// low-quality — merchants create broken plans there on purpose — so paging
	// someone at 04:00 for a test-mode subscription would train them to ignore
	// the alarm that matters.
	if len(live.Errors) > 0 {
		jobs.Fail(ctx, alerter, "sweep",
			fmt.Sprintf("%d subscription(s) were not invoiced for %s", len(live.Errors), date),
			jobs.Rendered(live.Errors))
	}
}

func run(ctx context.Context, invoicer *services.Invoicer, livemode bool, date brcal.Date, now time.Time) services.SweepResult {
	mode := "test"
	if livemode {
		mode = "live"
	}
	started := time.Now()
	res := invoicer.RunDailySweep(ctx, livemode, date, actor, now)

	level := slog.LevelInfo
	if len(res.Errors) > 0 {
		level = slog.LevelError
	}
	// Errors are logged one per line rather than joined: a sweep that failed on
	// forty subscriptions is forty things to fix, and a single truncated line is
	// how thirty-nine of them get missed.
	for _, e := range res.Errors {
		slog.Log(ctx, level, "sweep error", "mode", mode, "date", date.String(), "error", e)
	}
	slog.Log(ctx, level, "sweep finished",
		"mode", mode,
		"date", date.String(),
		"examined", res.Examined,
		"invoiced", res.Invoiced,
		"skipped", res.Skipped,
		"failed", res.Failed,
		"duration_ms", time.Since(started).Milliseconds())
	return res
}
