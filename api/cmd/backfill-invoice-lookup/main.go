// Command backfill-invoice-lookup gives every invoice written before the
// invoices table had customer-index its customer partition
// (repositories.LookupCustomerInvoicesPK, stored as customer_pk), so the
// per-customer read can be a key condition instead of a filtered walk of the
// tenant's INVOICE# range.
//
//	backfill-invoice-lookup            # report only (dry run)
//	backfill-invoice-lookup -apply     # write
//
// Both modes and every tenant in one pass: it Scans the invoices table, which
// the service role is denied (terraform/billing/iam.tf), so it runs with an
// operator's credentials — run.sh beside this file reads the secrets from SSM.
// Idempotent: re-run until the dry run reports missing=0. Exit 1 while
// anything is missing after the pass, mismatched, or unindexable.
//
// Deploy order: .github/workflows/README.md, "Adding an index to a live table".
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

	apply := flag.Bool("apply", false, "write customer_pk on the invoice rows that lack it (default: report only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	invoices, err := app.BuildInvoices(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	rep, err := invoices.BackfillCustomerIndex(ctx, *apply)
	for _, r := range rep.Missing {
		slog.Info("missing", "pk", r.PK, "sk", r.SK, "customer_pk", r.LookupPK)
	}
	for _, r := range rep.Mismatched {
		slog.Warn("mismatched", "pk", r.PK, "sk", r.SK, "customer_pk", r.LookupPK)
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
