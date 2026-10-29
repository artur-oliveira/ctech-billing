// Command backfill-created-at writes created_at on the credit note rows and
// audit rows stored before the attribute was written at all (the encoder
// dropped it; see repositories.creditNoteRow). The value is the time inside the
// row's own ULID id, the same one the console already shows for such a row.
//
//	backfill-created-at            # report only (dry run)
//	backfill-created-at -apply     # write
//
// Both modes and every tenant in one pass: it Scans the invoices and audit
// tables, which the service role is denied (terraform/billing/iam.tf), so it
// runs with an operator's credentials: run.sh beside this file reads the
// secrets from SSM. Idempotent: re-run until the dry run reports missing=0.
// Exit 1 while anything is missing after the pass or has no ULID time.
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

	apply := flag.Bool("apply", false, "write created_at on the rows that lack it (default: report only)")
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

	credits, audits, err := invoices.BackfillCreatedAt(ctx, *apply)
	for _, p := range credits.Unparseable {
		slog.Warn("credit note without ULID time", "row", p)
	}
	for _, p := range audits.Unparseable {
		slog.Warn("audit row without ULID time", "row", p)
	}
	if err != nil {
		slog.Error("backfill failed", "error", err,
			"credit_notes_written", credits.Written, "audit_written", audits.Written)
		os.Exit(1)
	}
	slog.Info("backfill done", "table_prefix", cfg.TablePrefix, "applied", *apply,
		"credit_notes_missing", credits.Missing, "credit_notes_written", credits.Written, "credit_notes_raced", credits.Raced,
		"audit_missing", audits.Missing, "audit_written", audits.Written, "audit_raced", audits.Raced)
	left := (credits.Missing - credits.Written - credits.Raced) + (audits.Missing - audits.Written - audits.Raced)
	if left > 0 || len(credits.Unparseable) > 0 || len(audits.Unparseable) > 0 {
		os.Exit(1)
	}
}
