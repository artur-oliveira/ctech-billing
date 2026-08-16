// Command dunning performs one day's dunning steps and exits.
//
//	dunning                     # today, in America/Sao_Paulo
//	dunning -date=2026-03-05    # a day the job missed
//
// A one-shot binary rather than a route, like cmd/sweep and for the same reason
// (ADR 0002): it reads the settlement partition, whose key carries no tenant.
//
// Safe to re-run. Each invoice stores the policy step it has reached, so
// replaying a day performs every step exactly once — the second run finds the
// invoices already advanced and does nothing.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // "today" is a civil date in São Paulo, on any host

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dateFlag := flag.String("date", "", "civil date to run (YYYY-MM-DD, America/Sao_Paulo); defaults to today")
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
	dunner, err := app.BuildDunner(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	now := time.Now()
	live := run(ctx, dunner, true, date, now)
	// Test mode is dunned too, and it is the only way a merchant can see what
	// their customers will receive before it is sent to a real one. The mail
	// still goes out — to whatever address the sandbox customer carries, which
	// is theirs.
	run(ctx, dunner, false, date, now)

	if len(live.Errors) > 0 {
		os.Exit(1)
	}
}

func run(ctx context.Context, d *services.Dunner, livemode bool, date brcal.Date, now time.Time) services.DunningResult {
	mode := "test"
	if livemode {
		mode = "live"
	}
	res := d.Run(ctx, livemode, date, now)

	level := slog.LevelInfo
	if len(res.Errors) > 0 {
		level = slog.LevelError
	}
	for _, e := range res.Errors {
		slog.Log(ctx, level, "dunning error", "mode", mode, "date", date.String(), "error", e)
	}
	slog.Log(ctx, level, "dunning finished",
		"mode", mode,
		"date", date.String(),
		"examined", res.Examined,
		"reminded", res.Reminded,
		"escalated", res.Escalated,
		"abandoned", res.Abandoned,
		"skipped", res.Skipped)
	return res
}
