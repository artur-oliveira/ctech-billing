// Command finance runs the daily finance job once and exits.
//
// Two steps, in this order, each idempotent by construction:
//
//  1. materialise: recurrences whose next occurrence has entered the horizon
//     (the current month and the next) become bills. Each occurrence is guarded
//     by a lock row written in the same transaction as its bill, so a re-run —
//     or a retry after a crash — never duplicates.
//  2. auto-settle: forecast bills flagged to auto-settle whose due date has
//     arrived are settled on that due date, for their own amount. A bill that
//     is no longer a forecast is skipped.
//
// (Closing card statements is step 3 and arrives with the cards phase.)
//
// It is a binary and not a route for the reason cmd/sweep is: it reads two
// cross-tenant schedule indexes (ADR 0002), so keeping it off the HTTP surface
// keeps "every request resolves its space" true. -date makes a missed day
// re-runnable, and because the work lists are ordered by date a later run also
// catches up whatever an earlier one missed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // finance decides "today" in America/Sao_Paulo, on any host

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dateFlag := flag.String("date", "", "civil date to run for (YYYY-MM-DD, America/Sao_Paulo); defaults to today")
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
	job, err := app.BuildFinanceJobs(ctx, cfg)
	if err != nil {
		jobs.Startup(ctx, alerter, "finance", err)
	}

	now := time.Now()
	live := run(ctx, job, true, date, now)
	// Test mode runs too, on the same schedule: a sandbox recurrence that never
	// materialises cannot be built against.
	run(ctx, job, false, date, now)

	// Only live failures fail the job; sandbox data is deliberately low-quality
	// and an alarm that fires on it is one people learn to close.
	if len(live) > 0 {
		jobs.Fail(ctx, alerter, "finance",
			fmt.Sprintf("%d finance item(s) failed for %s", len(live), date),
			jobs.Rendered(live))
	}
}

// run performs both steps for one mode and returns every error message.
func run(ctx context.Context, job *services.FinanceJobs, livemode bool, date brcal.Date, now time.Time) []error {
	mode := "test"
	if livemode {
		mode = "live"
	}
	var errs []error
	for _, step := range []struct {
		name string
		do   func() services.JobResult
	}{
		{"materialise", func() services.JobResult { return job.Materialise(ctx, livemode, date, now) }},
		{"auto-settle", func() services.JobResult { return job.AutoSettle(ctx, livemode, date, now) }},
	} {
		started := time.Now()
		res := step.do()
		level := slog.LevelInfo
		if len(res.Errors) > 0 {
			level = slog.LevelError
		}
		// One line per error: a run that failed on forty items is forty things to
		// fix, and a single truncated line is how thirty-nine of them get missed.
		for _, e := range res.Errors {
			slog.Log(ctx, level, "finance error", "mode", mode, "step", step.name, "date", date.String(), "error", e)
		}
		slog.Log(ctx, level, "finance step finished",
			"mode", mode, "step", step.name, "date", date.String(),
			"examined", res.Examined, "done", res.Done, "skipped", res.Skipped, "failed", res.Failed,
			"duration_ms", time.Since(started).Milliseconds())
		for _, e := range res.Errors {
			errs = append(errs, errors.New(e))
		}
	}
	return errs
}
