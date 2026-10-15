// Command reconcile replays the finance postings a settlement or a credit note could not
// write (spec § 3.8), once, and exits. It runs hourly and is safe to re-run at any
// frequency. It used to also chase the wallet's charges; BYOK is the only rail now and
// its own sweep settles what a webhook missed.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // the sweep partitions are keyed on civil dates in America/Sao_Paulo

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}

	ctx := context.Background()
	alerter := jobs.Alerts(ctx, cfg)
	finance, err := app.BuildFinance(ctx, cfg)
	if err != nil {
		jobs.Startup(ctx, alerter, "reconcile", err)
	}

	now := time.Now()
	// A posting still pending for want of a receiving account is not an error; a write that
	// failed is, in live mode.
	liveReplay := replay(ctx, finance, true, now)
	replay(ctx, finance, false, now)
	if len(liveReplay.Errors) > 0 {
		jobs.Fail(ctx, alerter, "reconcile",
			fmt.Sprintf("%d finance posting(s) could not be written", len(liveReplay.Errors)),
			jobs.Rendered(liveReplay.Errors))
	}
}

func replay(ctx context.Context, finance *services.FinanceInvoices, livemode bool, now time.Time) services.ReplayResult {
	mode := "test"
	if livemode {
		mode = "live"
	}
	res := finance.Replay(ctx, livemode, now)
	level := slog.LevelInfo
	if len(res.Errors) > 0 {
		level = slog.LevelError
	}
	for _, e := range res.Errors {
		slog.Log(ctx, level, "finance replay error", "mode", mode, "error", e)
	}
	slog.Log(ctx, level, "finance replay finished", "mode", mode,
		"examined", res.Examined, "done", res.Done, "pending", res.Pending, "given_up", res.GivenUp)
	return res
}
