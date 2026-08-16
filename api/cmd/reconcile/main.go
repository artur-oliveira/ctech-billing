// Command reconcile asks wallet about every charge billing is still waiting on,
// once, and exits.
//
// It exists because a webhook is a delivery, and deliveries fail. A notify-back
// can be lost by a deploy, a partition, a 502 from an instance that looks
// healthy, or a secret rotated mid-flight — and none of that is visible from
// inside billing. What is visible is a customer who paid looking at an invoice
// that says they did not, some hours later, usually by e-mail.
//
// It is a separate binary from cmd/sweep for two reasons, and neither is
// tidiness:
//
//   - It is cross-tenant (ADR 0002), so it must have no HTTP surface to
//     mis-scope, exactly like the invoice sweep.
//   - It runs on a different clock. Invoicing is a daily fact about a calendar;
//     an unanswered charge is an hourly fact about an integration, and folding
//     the second into the first would make a lost payment wait until 04:00.
//
// Safe to re-run at any frequency: every decision is idempotent, and settlement
// goes through Collector.Confirm, which re-reads the charge from wallet rather
// than trusting anything it was handed.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // the sweep partitions are keyed on civil dates in America/Sao_Paulo

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// lookbackDays is how many days of attempts each run examines.
//
// One, not zero: an attempt opened at 23:55 belongs to yesterday's partition by
// 00:05, and a job that only ever reads today would never see it again. Not
// more, because a PENDING attempt is removed from the sweep the moment it is
// decided, so anything older than the lookback has either been answered or is a
// row the -date flag exists to recover.
const lookbackDays = 1

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dateFlag := flag.String("date", "", "civil date to reconcile (YYYY-MM-DD, America/Sao_Paulo); defaults to today and the day before")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}

	ctx := context.Background()
	collector, err := app.BuildCollector(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	dates := recentDates(brcal.Today())
	if *dateFlag != "" {
		parsed, parseErr := brcal.Parse(*dateFlag)
		if parseErr != nil {
			slog.Error("invalid -date", "value", *dateFlag, "error", parseErr)
			os.Exit(2)
		}
		dates = []brcal.Date{parsed}
	}

	now := time.Now()
	failed := false
	for _, date := range dates {
		// Live and test both, and for the same reason the invoice sweep does it: a
		// sandbox checkout that never reaches PAID cannot be built against. Only
		// live failures fail the job — a test-mode charge is not worth a page at
		// 03:00, and an alarm that fires on sandbox data is an alarm people learn
		// to close.
		if len(run(ctx, collector, true, date, now).Errors) > 0 {
			failed = true
		}
		run(ctx, collector, false, date, now)
	}
	if failed {
		os.Exit(1)
	}
}

// recentDates is today and the days before it, oldest first.
func recentDates(today brcal.Date) []brcal.Date {
	out := make([]brcal.Date, 0, lookbackDays+1)
	for i := lookbackDays; i >= 0; i-- {
		out = append(out, today.AddDays(-i))
	}
	return out
}

func run(ctx context.Context, collector *services.Collector, livemode bool, date brcal.Date, now time.Time) services.ReconcileResult {
	mode := "test"
	if livemode {
		mode = "live"
	}
	started := time.Now()
	res := collector.Reconcile(ctx, livemode, date, now)

	level := slog.LevelInfo
	if len(res.Errors) > 0 {
		level = slog.LevelError
	}
	// One line per error. A run that failed on forty charges is forty things to
	// look at, and a single joined line is how thirty-nine of them are missed.
	for _, e := range res.Errors {
		slog.Log(ctx, level, "reconcile error", "mode", mode, "date", date.String(), "error", e)
	}
	slog.Log(ctx, level, "reconcile finished",
		"mode", mode,
		"date", date.String(),
		"examined", res.Examined,
		// settled > 0 is the headline: every one of these is a payment the webhook
		// never reported, and a sustained non-zero count is an integration problem
		// to fix rather than a job doing its job.
		"settled", res.Settled,
		"failed", res.Failed,
		"abandoned", res.Abandoned,
		"waiting", res.Waiting,
		"duration_ms", time.Since(started).Milliseconds())
	return res
}
