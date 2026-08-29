// Package jobs is the small amount of behaviour the four scheduled binaries
// share: how they report a failure to a person.
//
// Each job already logs what happened, and a log is where the detail belongs.
// What a log does not do is reach anybody — nothing reads CloudWatch at 04:00 —
// so a run that failed also publishes one alert, through the account's shared
// SNS topic rather than through a CloudWatch alarm per thing that can break
// (see api-commons/alerts for why).
//
// The exit codes are the existing convention and are kept: 2 means the job never
// started, 1 means it ran and something in live mode failed. Test-mode failures
// alert nobody, for the same reason they never failed the job — sandbox data is
// deliberately broken, and an alarm that fires on it is an alarm people learn to
// close.
package jobs

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"gopkg.aoctech.app/api-commons/alerts"

	"gopkg.aoctech.app/billing/api/internal/config"
)

// Alerts builds the publisher. A deployment with no topic configured gets the
// no-op one: a job that cannot page anybody must still do its work.
func Alerts(ctx context.Context, cfg *config.Config) alerts.Publisher {
	p, err := alerts.New(ctx, cfg.AWSRegion, cfg.AlertsTopicARN, "billing", cfg.Env)
	if err != nil {
		// Refusing to run because the alerting is broken would trade a silent
		// failure for a certain one.
		slog.ErrorContext(ctx, "alerting unavailable", "error", err)
		return alerts.Nop{}
	}
	return p
}

// Startup reports a job that never ran and exits 2.
//
// It is the failure most worth an alert and the one a metric would miss: a
// binary that dies on configuration produces no counters, no errors and no
// invoices, and looks exactly like a quiet day.
func Startup(ctx context.Context, p alerts.Publisher, job string, err error) {
	slog.ErrorContext(ctx, "startup", "error", err)
	p.Alert(ctx, alerts.Alert{
		Job:     job,
		Summary: "the job did not start, so none of its work was done",
		Err:     err,
	})
	os.Exit(2)
}

// Fail reports a run that did its work badly and exits 1.
//
// The errors arrive already rendered, because that is how the sweep, the
// reconciler, the deliverer and the dunner all collect them: a per-row failure
// is a message about one subscription or one invoice, not a value anything
// upstream branches on.
func Fail(ctx context.Context, p alerts.Publisher, job, summary string, errs []string) {
	p.Alert(ctx, alerts.Alert{Job: job, Summary: summary, Detail: detail(errs)})
	os.Exit(1)
}

// Notify reports something worth a person's attention that is not a failure of
// the run — a charge wallet has never heard of, say. It does not exit: the job
// did what it was asked, and exiting non-zero here would make a systemd or cron
// failure mean two different things.
func Notify(ctx context.Context, p alerts.Publisher, job, summary, detail string) {
	p.Alert(ctx, alerts.Alert{Job: job, Summary: summary, Detail: detail})
}

// Rendered flattens errors for Fail. Two of the four jobs collect []error and
// two collect []string — a difference in how each service reports a per-row
// failure, not a distinction worth propagating into the alerting.
func Rendered(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}

// detailLimit is how many errors an alert carries.
//
// An e-mail is not a log. Five is enough to recognise whether forty failures are
// one cause or forty, which is the only decision made from the alert itself —
// everything past that is read in the logs the alert sends somebody to.
const detailLimit = 5

func detail(errs []string) string {
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, err := range errs {
		if i == detailLimit {
			b.WriteString("…\n")
			break
		}
		b.WriteString("- " + err + "\n")
	}
	return b.String()
}
