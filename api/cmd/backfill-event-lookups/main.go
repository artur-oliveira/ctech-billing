// Command backfill-event-lookups gives the events and webhook deliveries written
// before the history pointers existed their pointers, so "what happened to this
// invoice" and "what was this endpoint sent" can be answered for them too.
//
//	backfill-event-lookups            # report only (dry run)
//	backfill-event-lookups -apply     # write
//
// It Scans the webhook table, which the service role is denied
// (terraform/billing/iam.tf), so it runs with an operator's credentials: run.sh
// beside this file reads the secrets from SSM. Idempotent: a pointer that exists
// is left alone, so it can be re-run.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	apply := flag.Bool("apply", false, "write the pointers (default: report only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	hooks, err := app.BuildWebhooks(context.Background(), cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}
	rep, err := hooks.BackfillHistoryLookups(context.Background(), *apply)
	slog.Info("event history lookups", "events", rep.Events, "deliveries", rep.Deliveries, "written", rep.Written, "apply", *apply)
	if err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}
}
