// Command byok is the hourly BYOK job (spec 2026-10-11 § 5): for every bank
// connection, statement sync and certificate warnings (part A); the charge
// sweep, the daily webhook audit (part B) and usage, once per live run (part C).
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

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	modeFlag := flag.String("mode", "both", "live, test or both")
	_ = flag.String("date", "", "civil date for the steps that take one (parts B and C); the statement sync ignores it")
	retry := flag.Bool("retry-callbacks", false, "operator tool: ask each live connection's provider to resend the webhook deliveries that failed since -since (not a cron step)")
	since := flag.String("since", "", "RFC 3339 instant for -retry-callbacks")
	rail := flag.String("tenant-rail", "", "operator tool (spec § 12.6): move tenant zero's collection rail to byok (CTech's own bank connection) or back to wallet; needs -mode=live and -operator")
	operator := flag.String("operator", "", "who is moving the rail, recorded in the audit trail")
	dryRun := flag.Bool("dry-run", false, "with -tenant-rail: print the state and change nothing")
	invoiceFor := flag.String("tenant-invoice", "", "operator tool: issue a one-off invoice in tenant zero for this customer id (needs -mode=live, -operator, -invoice-amount-cents, -invoice-reference, -invoice-description); prints the checkout link")
	invoiceCents := flag.Int64("invoice-amount-cents", 0, "with -tenant-invoice: the amount in centavos")
	invoiceRef := flag.String("invoice-reference", "", "with -tenant-invoice: the caller's name for it; repeating it returns the same invoice")
	invoiceText := flag.String("invoice-description", "", "with -tenant-invoice: the line the customer reads")
	creditFor := flag.String("tenant-credit", "", "operator tool: record that money for this tenant-zero invoice was returned outside Billing (a credit note marked refunded externally; needs -mode=live, -operator, -credit-amount-cents, -credit-reason; -credit-ref optional)")
	creditCents := flag.Int64("credit-amount-cents", 0, "with -tenant-credit: the amount in centavos")
	creditReason := flag.String("credit-reason", "", "with -tenant-credit: why, written for whoever reads the invoice later")
	creditRef := flag.String("credit-ref", "", "with -tenant-credit: the bank's reference for the return, when there is one")
	refresh := flag.Bool("tenant-refresh", false, "operator tool: re-test tenant zero's stored bank credentials and record what they can do now (needs -mode=live, -operator)")
	payoutCPF := flag.String("tenant-payout", "", "operator tool: pay a Pix out of tenant zero's account to this CPF's key (live proof of payouts; needs -mode=live, -operator, -payout-amount-cents, -payout-reference; sends only with -yes)")
	payoutCents := flag.Int64("payout-amount-cents", 0, "with -tenant-payout: the amount in centavos")
	payoutRef := flag.String("payout-reference", "", "with -tenant-payout: the caller's name for it; repeating it returns the same payout, never a second payment")
	yes := flag.Bool("yes", false, "with -tenant-payout: really send it")
	fine := flag.Int("fine-bp", -1, "with -tenant-rail=byok: the late fine in basis points (200 = 2 %); -1 keeps the current one")
	interest := flag.Int("interest-bp", -1, "with -tenant-rail=byok: the monthly late interest in basis points (100 = 1 %); -1 keeps the current one")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	if *refresh {
		os.Exit(tenantRefresh(ctx, cfg, *modeFlag, *operator))
	}
	if *payoutCPF != "" {
		os.Exit(tenantPayout(ctx, cfg, *modeFlag, *operator, *payoutCPF, *payoutRef, *payoutCents, *yes))
	}
	if *creditFor != "" {
		os.Exit(tenantCredit(ctx, cfg, *modeFlag, *operator, *creditFor, *creditReason, *creditRef, *creditCents))
	}
	if *invoiceFor != "" {
		os.Exit(tenantInvoice(ctx, cfg, *modeFlag, *operator, *invoiceFor, *invoiceText, *invoiceRef, *invoiceCents))
	}
	if *rail != "" {
		os.Exit(tenantRail(ctx, cfg, *rail, *modeFlag, *operator, *fine, *interest, *dryRun))
	}
	alerter := jobs.Alerts(ctx, cfg)
	conns, statement, repo, guard, sweep, err := app.BuildByokJob(ctx, cfg)
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
	if *retry {
		os.Exit(retryCallbacks(ctx, repo, sweep, *since))
	}
	job := services.NewByokJob(repo, conns, statement, mailer, alerter, spaceFor).
		WithConsoleURL(consoleURL(cfg.CheckoutBaseURL)).
		WithSandboxCertificate(conns.SandboxCertNotAfter, guard)
	if sweep != nil {
		sweep.WithMail(mailer).WithAlerts(alerter).Register(job)
	}

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

// retryCallbacks is the recovery tool after a webhook outage (spec § 5): per
// live connection, the failed deliveries since -since are resent. It prints
// counts only.
func retryCallbacks(ctx context.Context, repo *repositories.ByokRepository, sweep *services.ByokSweep, sinceArg string) int {
	since, err := time.Parse(time.RFC3339, sinceArg)
	if err != nil || sweep == nil {
		fmt.Fprintln(os.Stderr, "usage: byok -retry-callbacks -since=2026-10-10T00:00:00Z")
		return 2
	}
	var start map[string]types.AttributeValue
	total, failed := 0, 0
	for {
		page, err := repo.ConnectionsForJob(ctx, true, start, 100)
		if err != nil {
			fmt.Fprintln(os.Stderr, "reading connections:", err)
			return 1
		}
		for i := range page.Items {
			c := &page.Items[i]
			if c.Kind != byokconn.KindMerchant || c.State != byokconn.StateActive {
				continue
			}
			if err := repo.LoadSealed(ctx, c); err != nil {
				failed++
				continue
			}
			n, err := sweep.RetryCallbacks(ctx, c, since, time.Sleep)
			total += n
			if err != nil {
				failed++
				slog.WarnContext(ctx, "byok retry callbacks", "connection_id", c.ID, "code", byok.ErrorCode(err))
			}
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		start = page.LastEvaluatedKey
	}
	fmt.Printf("callbacks resent: %d; connections failed: %d\n", total, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
