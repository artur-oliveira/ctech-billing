// Command backfill-organization-lookup gives every linked tenant the console
// lookup key {mode}#ACCOUNT_ORG#{account_organization_id} (ADR 0028), replacing
// the {mode}#OWNER#{user_id} key the console read before.
//
//	backfill-organization-lookup            # report only (dry run)
//	backfill-organization-lookup -apply     # write
//
// Both modes and every tenant in one pass. It Scans the organizations table,
// which the service role is denied, so it runs with an operator's credentials —
// run.sh beside this file. Idempotent: re-run until the dry run reports
// missing=0. Exit 1 while anything is missing after the pass, mismatched or in
// conflict. Unlinked tenants that had a console key are listed, not fatal.
//
// Deploy order: .github/workflows/README.md, "Console access by membership".
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

	apply := flag.Bool("apply", false, "write the account organization key (default: report only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	orgs, err := app.BuildOrganizations(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	rep, err := orgs.BackfillAccountOrganizationLookup(ctx, *apply)
	for _, r := range rep.Missing {
		slog.Info("missing", "tenant", r.OrganizationID, "livemode", r.Livemode, "from", r.From, "to", r.To)
	}
	for _, r := range rep.Mismatched {
		slog.Warn("mismatched", "tenant", r.OrganizationID, "livemode", r.Livemode, "lookup_pk", r.From, "want", r.To)
	}
	for _, r := range rep.Conflicts {
		slog.Warn("conflict", "tenant", r.OrganizationID, "livemode", r.Livemode, "account_organization", r.AccountOrganizationID)
	}
	for _, r := range rep.Unlinked {
		slog.Warn("unlinked", "tenant", r.OrganizationID, "livemode", r.Livemode, "lookup_pk", r.From)
	}
	if err != nil {
		slog.Error("backfill failed", "error", err, "written", rep.Written)
		os.Exit(1)
	}
	slog.Info("backfill done", "table_prefix", cfg.TablePrefix, "applied", *apply,
		"scanned", rep.Scanned, "missing", len(rep.Missing), "written", rep.Written, "raced", rep.Raced,
		"mismatched", len(rep.Mismatched), "conflicts", len(rep.Conflicts), "unlinked", len(rep.Unlinked))
	left := len(rep.Missing) - rep.Written - rep.Raced
	if left > 0 || rep.Raced > 0 || len(rep.Mismatched) > 0 || len(rep.Conflicts) > 0 {
		os.Exit(1)
	}
}
