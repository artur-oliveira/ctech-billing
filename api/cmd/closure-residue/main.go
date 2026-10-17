// Command closure-residue finds, and with -apply deletes, what is left under a
// closed merchant tenant's key prefix after the online purge: the usage,
// generation-key and level sub-partitions that cannot be enumerated without a
// Scan (docs/specs/2026-10-17-merchant-tenant-deletion.md).
//
//	closure-residue -tenant <billing tenant id>           # report only (dry run)
//	closure-residue -tenant <billing tenant id> -apply    # delete what it found
//
// It Scans, which the service role is denied (terraform/billing/iam.tf), so it
// runs with an operator's credentials. Refuses tenant zero. Idempotent: run it
// until the dry run reports nothing.
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
	tenant := flag.String("tenant", "", "billing tenant id (the id of the closed tenant; both modes)")
	apply := flag.Bool("apply", false, "delete what is found (default: report only)")
	flag.Parse()
	if *tenant == "" {
		slog.Error("-tenant is required")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	if cfg.PortalOrganizationID != "" && *tenant == cfg.PortalOrganizationID {
		slog.Error("refusing tenant zero")
		os.Exit(2)
	}
	purger, err := app.BuildTenantPurger(context.Background(), cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}
	rep, err := purger.SweepResidue(context.Background(), *tenant, *apply)
	for _, r := range rep.Found {
		slog.Info("residue", "table", r.Table, "pk", r.PK, "sk", r.SK)
	}
	if err != nil {
		slog.Error("sweep failed", "error", err, "found", len(rep.Found), "deleted", rep.Deleted)
		os.Exit(1)
	}
	slog.Info("residue done", "table_prefix", cfg.TablePrefix, "tenant", *tenant, "applied", *apply,
		"scanned", rep.Scanned, "found", len(rep.Found), "deleted", rep.Deleted)
	if !*apply && len(rep.Found) > 0 {
		os.Exit(1) // a dry run that found something is not "clean"
	}
}
