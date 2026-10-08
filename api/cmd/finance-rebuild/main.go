// Command finance-rebuild recomputes a finance space's cached balances and
// monthly summaries from its immutable transactions and reports (or fixes) any
// drift (spec § 10: "cache equals derivation").
//
//	finance-rebuild -space 0190... -mode live             # report only
//	finance-rebuild -space USER#sub -mode test -apply     # fix
//
// -space is an operator's decision, not a request: the binary builds its space
// with space.ForJob, which the request path may not call. Run it when the space
// is quiet and repeat until the report is empty: exit 1 while drift was found.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	spaceFlag := flag.String("space", "", "organization id or USER#{sub} (required)")
	mode := flag.String("mode", "", "test or live (required)")
	apply := flag.Bool("apply", false, "overwrite drifted cache rows with the derived values")
	flag.Parse()

	if *spaceFlag == "" || (*mode != "test" && *mode != "live") {
		// No default for -mode, on purpose, for the same reason as cmd/seed: a
		// mistyped flag must not silently act on the other world.
		flag.Usage()
		os.Exit(2)
	}
	sp, err := space.ForJob(*spaceFlag, *mode == "live")
	if err != nil {
		slog.Error("invalid space", "space", *spaceFlag, "error", err)
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	ledger, err := app.BuildLedger(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	drift, err := ledger.Rebuild(ctx, sp, *apply, time.Now())
	for _, d := range drift {
		slog.Warn("drift", "kind", d.Kind, "account", d.Account, "month", d.Month,
			"stored", d.Stored, "derived", d.Derived,
			"stored_debits", d.StoredDebits, "stored_credits", d.StoredCredits,
			"derived_debits", d.DerivedDebits, "derived_credits", d.DerivedCredits)
	}
	if err != nil {
		slog.Error("rebuild failed", "error", err)
		os.Exit(1)
	}
	slog.Info("rebuild done", "space", sp.PK(), "drift", len(drift), "applied", *apply)
	if len(drift) > 0 {
		os.Exit(1)
	}
}
