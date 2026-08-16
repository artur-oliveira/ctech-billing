// Command seed applies a declared tenant to a deployment.
//
//	seed -file tenants/ctech.json -mode test
//	seed -file tenants/ctech.json -mode live
//
// It is the one write path with no credential behind it, and it is a binary
// rather than a route for the same reason cmd/sweep is: a process that can
// create a tenant must not be reachable over HTTP, where the only thing between
// it and the internet is a scope check.
//
// Safe to re-run. Every row is create-or-skip, so applying the same file twice
// is a no-op and applying it after adding a price creates only the price.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/provision"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	file := flag.String("file", "", "path to the tenant plan (required)")
	mode := flag.String("mode", "", "test or live (required)")
	dryRun := flag.Bool("dry-run", false, "parse and validate the plan, write nothing")
	flag.Parse()

	if *file == "" || (*mode != "test" && *mode != "live") {
		// No default for -mode, on purpose. Defaulting either way means a mistyped
		// flag writes to the wrong one silently, and "live" is the direction that
		// cannot be undone by deleting rows — an organization is referenced by
		// invoices the moment it starts billing.
		flag.Usage()
		os.Exit(2)
	}
	livemode := *mode == "live"

	f, err := os.Open(*file)
	if err != nil {
		slog.Error("opening plan", "file", *file, "error", err)
		os.Exit(2)
	}
	defer func() { _ = f.Close() }()

	plan, err := provision.Parse(f)
	if err != nil {
		slog.Error("invalid plan", "file", *file, "error", err)
		os.Exit(2)
	}

	if *dryRun {
		slog.Info("plan is valid",
			"file", *file,
			"organization", plan.Organization.ID,
			"credentials", len(plan.Credentials),
			"products", len(plan.Products),
			"prices", len(plan.Prices))
		return
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}

	ctx := context.Background()
	repos, err := app.BuildProvisioner(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	res, err := provision.Apply(ctx, repos, plan, livemode, time.Now())
	if err != nil {
		// A partial apply is reported rather than hidden. The next run finishes
		// the job — that is what create-or-skip buys — but an operator has to be
		// told which half is already there.
		slog.Error("apply failed", "mode", *mode, "organization", plan.Organization.ID, "error", err)
		report(res, *mode)
		os.Exit(1)
	}
	report(res, *mode)
}

func report(res *provision.Result, mode string) {
	if res == nil {
		return
	}
	// One line per row rather than a count: the useful question after a seed is
	// "did the thing I just added get written", and a total of six does not
	// answer it.
	for _, c := range res.Created {
		slog.Info("created", "mode", mode, "row", c)
	}
	for _, s := range res.Skipped {
		slog.Info("already present", "mode", mode, "row", s)
	}
	fmt.Printf("%s: %d created, %d already present\n", mode, len(res.Created), len(res.Skipped))
}
