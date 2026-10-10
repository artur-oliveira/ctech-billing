// Command backfill-subscription-lookup gives every subscription written before
// the subscriptions table had lookup-index its customer lookup partition
// (repositories.LookupCustomerSubscriptionsPK), so the per-customer read can be
// a key condition instead of a filtered walk of the tenant's SUB# range.
//
//	backfill-subscription-lookup            # report only (dry run)
//	backfill-subscription-lookup -apply     # write
//
// Both modes and every tenant in one pass: it Scans the subscriptions table,
// which the service role is denied (terraform/billing/iam.tf), so it runs with
// an operator's credentials — run.sh beside this file reads the secrets from
// SSM. Idempotent: re-run until the dry run reports missing=0. Exit 1 while
// anything is missing after the pass, mismatched, or unindexable.
//
// Deploy order: .github/workflows/README.md, "Subscriptions by customer".
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

	apply := flag.Bool("apply", false, "write lookup_pk on the rows that lack it (default: report only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	subs, err := app.BuildSubscriptions(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	rep, err := subs.BackfillCustomerLookup(ctx, *apply)
	for _, r := range rep.Missing {
		slog.Info("missing", "pk", r.PK, "sk", r.SK, "lookup_pk", r.LookupPK)
	}
	for _, r := range rep.Mismatched {
		slog.Warn("mismatched", "pk", r.PK, "sk", r.SK, "lookup_pk", r.LookupPK)
	}
	for _, r := range rep.Unindexable {
		slog.Warn("no customer_id", "pk", r.PK, "sk", r.SK)
	}
	if err != nil {
		slog.Error("backfill failed", "error", err, "written", rep.Written)
		os.Exit(1)
	}
	slog.Info("backfill done", "table_prefix", cfg.TablePrefix, "applied", *apply,
		"scanned", rep.Scanned, "missing", len(rep.Missing), "written", rep.Written, "raced", rep.Raced,
		"mismatched", len(rep.Mismatched), "unindexable", len(rep.Unindexable))
	left := len(rep.Missing) - rep.Written - rep.Raced
	if left > 0 || len(rep.Mismatched) > 0 || len(rep.Unindexable) > 0 {
		os.Exit(1)
	}
}
