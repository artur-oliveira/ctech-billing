// Command byok is the hourly BYOK job (spec 2026-10-11 § 5): for every bank
// connection, statement sync and certificate warnings (part A); the charge
// sweep, the daily webhook audit and usage come with parts B and C.
//
// It is cross-organization by construction (ADR 0002), so it has no HTTP
// surface, and every step is re-runnable: a line posts once (its FITID# lock),
// a warning is marked once sent, a sync runs under a per-connection lock.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"
	_ "time/tzdata" // warnings and the degraded notice are per São Paulo day

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	modeFlag := flag.String("mode", "both", "live, test or both")
	_ = flag.String("date", "", "civil date for the steps that take one (parts B and C); the statement sync ignores it")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	alerter := jobs.Alerts(ctx, cfg)
	conns, statement, repo, guard, err := app.BuildByokJob(ctx, cfg)
	if err == nil && conns == nil {
		err = fmt.Errorf("BYOK_OUTBOUND_FUNCTION is not set")
	}
	if err != nil {
		jobs.Startup(ctx, alerter, "byok", err)
	}

	var mailer services.ByokMailer
	if cfg.EmailFrom != "" {
		if m, merr := email.New(ctx, cfg.AWSRegion, cfg.EmailFrom); merr != nil {
			slog.Warn("byok notices go to the operator only: SES client", "error", merr)
		} else {
			mailer = m
		}
	}
	// The space of each connection is rebuilt from its own row (the organization
	// and the mode), never from a request: the reason ForJob exists.
	spaceFor := func(c *byokconn.Connection) (space.ResolvedSpace, error) {
		return space.ForJob(c.AccountOrganizationID, c.Livemode)
	}
	job := services.NewByokJob(repo, conns, statement, mailer, alerter, spaceFor).
		WithConsoleURL(consoleURL(cfg.CheckoutBaseURL)).
		WithSandboxCertificate(conns.SandboxCertNotAfter, guard)

	now := time.Now()
	var errs []error
	if *modeFlag == "live" || *modeFlag == "both" {
		res := job.Run(ctx, true, now)
		slog.InfoContext(ctx, "byok live", "connections", res.Connections, "errors", len(res.Errors))
		errs = append(errs, res.Errors...)
	}
	if *modeFlag == "test" || *modeFlag == "both" {
		// Test-mode failures are logged, never paged: the shared sandbox is
		// Inter's, and an alarm on sandbox data is one people learn to close.
		res := job.Run(ctx, false, now)
		slog.InfoContext(ctx, "byok test", "connections", res.Connections, "errors", len(res.Errors))
		for _, e := range res.Errors {
			slog.WarnContext(ctx, "byok test connection", "error", e)
		}
	}
	if len(errs) > 0 {
		jobs.Fail(ctx, alerter, "byok", fmt.Sprintf("%d bank connection(s) could not be synced or checked", len(errs)), jobs.Rendered(errs))
	}
}

// consoleURL is the connections screen on the app's origin.
func consoleURL(checkoutBase string) string {
	u, err := url.Parse(checkoutBase)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/console/connections"
}
