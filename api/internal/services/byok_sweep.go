package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Sweep cadence (spec § 5 step 1): an attempt opened in the last 48 hours is
// re-queried when its last check is over an hour old, an older one daily.
const (
	sweepRecent      = 48 * time.Hour
	sweepEvery       = time.Hour
	sweepEveryOld    = 24 * time.Hour
	auditFromHour    = 6 // São Paulo
	retryBatch       = 50
	retryCallsMinute = 5
)

// payoutMinAge keeps the sweep from racing the request that just created a
// payout; payoutStuckAfter is when a payout still processing is the operator's.
const (
	payoutMinAge     = time.Minute
	payoutStuckAfter = 6 * time.Hour
)

// ByokSweep is cmd/byok's part B (spec § 5): the charge sweep, the daily
// webhook audit, refunds in flight, the sandbox webhook and the callback
// retry tool.
type ByokSweep struct {
	collector *ByokCollector
	payments  *repositories.PaymentRepository
	payouts   *Payouts
	orgs      *repositories.OrganizationRepository
	setup     *ByokCollectionSetup
	repo      *repositories.ByokRepository
	conns     *ByokConnections
	alerts    alerts.Publisher
	mail      ByokMailer
	usage     *ByokUsage
}

// WithUsage adds part C's usage step (spec § 5 step 6) to the job.
func (s *ByokSweep) WithUsage(u *ByokUsage) *ByokSweep { s.usage = u; return s }

// WithPayouts makes the sweep finish the payouts still processing.
func (s *ByokSweep) WithPayouts(p *Payouts) *ByokSweep { s.payouts = p; return s }

func NewByokSweep(collector *ByokCollector, payments *repositories.PaymentRepository, orgs *repositories.OrganizationRepository,
	setup *ByokCollectionSetup, repo *repositories.ByokRepository, conns *ByokConnections, p alerts.Publisher) *ByokSweep {
	if p == nil {
		p = alerts.Nop{}
	}
	return &ByokSweep{collector: collector, payments: payments, orgs: orgs, setup: setup, repo: repo, conns: conns, alerts: p}
}

func (s *ByokSweep) WithMail(m ByokMailer) *ByokSweep { s.mail = m; return s }

// WithAlerts routes the sweep's operator alerts.
func (s *ByokSweep) WithAlerts(p alerts.Publisher) *ByokSweep {
	if p != nil {
		s.alerts = p
	}
	return s
}

// Register adds the steps to the job.
func (s *ByokSweep) Register(j *ByokJob) {
	j.Register(StepSweep, s.Sweep)
	j.Register(StepAudit, s.Audit)
	j.WithFinally(s.collector.FollowRefunds)
	if s.payouts != nil {
		j.WithFinally(s.FollowPayouts)
	}
	if s.usage != nil {
		// Once per run, after every connection; test-mode charges are never
		// reported (they have no usage row).
		j.WithFinally(func(ctx context.Context, livemode bool, now time.Time) []error {
			if !livemode {
				return nil
			}
			reported, skipped, errs := s.usage.Report(ctx, now)
			slog.InfoContext(ctx, "byok usage", "reported", reported, "skipped", skipped, "errors", len(errs))
			return errs
		})
	}
	j.WithSandboxHook(s.Bootstrap)
}

// Sweep re-queries the tenant's open charges on this connection's provider
// and retries the creations that failed.
func (s *ByokSweep) Sweep(ctx context.Context, _ space.ResolvedSpace, conn *byokconn.Connection, now time.Time) error {
	if conn.State != byokconn.StateActive || !conn.Has(byok.CapabilityCollection) {
		return nil
	}
	tenant, err := s.orgs.GetByAccountOrganization(ctx, conn.AccountOrganizationID, conn.Livemode)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var start map[string]types.AttributeValue
	for {
		page, err := s.payments.OpenByok(ctx, tenant.ID, tenant.Livemode, start, 100)
		if err != nil {
			return err
		}
		for i := range page.Items {
			a := &page.Items[i]
			if a.Provider != conn.Provider {
				continue
			}
			// One charge the bank refuses never stops this connection's
			// statement sync or audit: logged (and alerted in live), not
			// returned. Only a failure to list is the step's error.
			if err := s.sweepOne(ctx, tenant, a, now); err != nil {
				slog.WarnContext(ctx, "byok sweep: attempt", "attempt_id", a.ID, "code", byok.ErrorCode(err))
				if conn.Livemode && !errors.Is(err, byok.ErrUnavailable) {
					s.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "a BYOK charge could not be swept", Err: err,
						Detail: fmt.Sprintf("attempt %s of invoice %s (tenant %s)", a.ID, a.InvoiceID, a.OrganizationID)})
				}
			}
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		start = page.LastEvaluatedKey
	}
	return nil
}

func (s *ByokSweep) sweepOne(ctx context.Context, tenant *billing.Organization, a *billing.PaymentAttempt, now time.Time) error {
	if a.Payable == "" {
		inv, err := s.collector.invoices.Get(ctx, a.OrganizationID, a.Livemode, a.InvoiceID)
		if err != nil {
			return err
		}
		if inv.Status != billing.InvoiceOpen {
			return nil
		}
		return s.collector.createCharge(ctx, tenant, inv, a, now)
	}
	every := sweepEvery
	if now.Sub(a.CreatedAt) > sweepRecent {
		every = sweepEveryOld
	}
	if !a.CheckedAt.IsZero() && now.Sub(a.CheckedAt) < every {
		return nil
	}
	// A credit note the bank did not take when it was issued (spec § 7.3).
	if inv, err := s.collector.invoices.Get(ctx, a.OrganizationID, a.Livemode, a.InvoiceID); err == nil &&
		inv.Status == billing.InvoiceOpen && inv.Collectable() != a.Amount {
		if err := s.collector.Credited(ctx, inv, now); err != nil {
			return err
		}
	}
	_, err := s.collector.confirmAttempt(ctx, tenant, a, WakeSweep, now)
	if errors.Is(err, ErrAmountMismatch) {
		s.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "a BYOK charge was paid below the amount due", Err: err,
			Detail: fmt.Sprintf("attempt %s of invoice %s (tenant %s)", a.ID, a.InvoiceID, a.OrganizationID)})
		return nil
	}
	return err
}

// Audit is the daily webhook check (spec § 5 step 4), first run at or after
// 06:00 São Paulo: our URL overwritten → overwritten + notice; callbacks that
// failed in the last 24 hours → alert and an immediate re-query.
func (s *ByokSweep) Audit(ctx context.Context, _ space.ResolvedSpace, conn *byokconn.Connection, now time.Time) error {
	if conn.Kind != byokconn.KindMerchant || conn.State != byokconn.StateActive || !conn.Has(byok.CapabilityCollection) ||
		conn.HookID == "" || s.setup == nil || s.setup.host == "" {
		return nil
	}
	local := now.In(brcal.Location)
	today := brcal.FromTime(now).String()
	if local.Hour() < auditFromHour || conn.AuditedOn == today {
		return nil
	}
	sess := s.conns.Session(conn)
	current, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (string, error) {
		return sess.P.GetWebhook(ctx, cr, t, byok.ProductPix)
	})
	if err != nil {
		return err
	}
	if conn.WebhookStatus == byokconn.WebhookRegistered && !IsOurHookURL(current, s.setup.host, conn.HookID) {
		conn.WebhookStatus = byokconn.WebhookOverwritten
		s.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "a merchant's Pix webhook was overwritten",
			Detail: fmt.Sprintf("connection %s (%s, organization %s): payments are confirmed by the hourly sweep", conn.ID, conn.Provider, conn.AccountOrganizationID)})
		if s.mail != nil && conn.NotifyEmail != "" {
			if err := s.mail.SendByokNotice(ctx, conn.NotifyEmail, email.ByokNotice{Kind: "webhook_overwritten", Provider: conn.Provider}); err != nil {
				slog.WarnContext(ctx, "byok overwritten notice", "connection_id", conn.ID, "error", err)
			}
		}
	}
	calls, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) ([]byok.CallbackAttempt, error) {
		return sess.P.ListCallbacks(ctx, cr, t, now.Add(-24*time.Hour))
	})
	if err != nil {
		return err
	}
	failed := map[string]bool{}
	for _, c := range calls {
		if c.HTTPStatus < 200 || c.HTTPStatus > 299 {
			failed[c.ChargeRef] = true
		}
	}
	if len(failed) > 0 {
		s.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "webhook deliveries failed in the last 24 hours",
			Detail: fmt.Sprintf("connection %s (%s): %d charge(s); re-queried now", conn.ID, conn.Provider, len(failed))})
		for ref := range failed {
			ptr, err := s.payments.ChargeRef(ctx, conn.Provider, conn.Livemode, ref)
			if err != nil || ptr.AccountOrganizationID != conn.AccountOrganizationID {
				continue
			}
			if _, err := s.collector.Confirm(ctx, *ptr, WakeSweep, now); err != nil {
				slog.WarnContext(ctx, "byok audit re-query", "connection_id", conn.ID, "error", err)
			}
		}
	}
	conn.AuditedOn = today
	return s.repo.SaveConnection(ctx, conn, "system:byok", "", "", now)
}

// Bootstrap registers the shared sandbox webhook once (spec § 9): its hook
// row is created on the first test-mode run through a sandbox connection.
func (s *ByokSweep) Bootstrap(ctx context.Context, conn *byokconn.Connection, now time.Time) error {
	if s.setup == nil || s.setup.host == "" || conn.Kind != byokconn.KindSandboxShared {
		return nil
	}
	if _, err := s.repo.GetHook(ctx, repositories.SandboxHookID); err == nil {
		return nil
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return err
	}
	// The run's copy may be older than this run's own saves (sync, health).
	fresh, err := s.repo.GetConnection(ctx, conn.AccountOrganizationID, conn.Livemode, conn.Provider)
	if err != nil {
		return err
	}
	conn = fresh
	err = s.setup.register(ctx, conn, false, "system:byok", "", now)
	if wc := (*ErrWebhookConflict)(nil); errors.As(err, &wc) {
		s.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "the sandbox Pix key has another webhook",
			Detail: "host " + wc.ExistingHost + "; test-mode payments are confirmed by the hourly sweep"})
		return nil
	}
	return err
}

// RetryCallbacks is the recovery tool after a webhook outage (`cmd/byok
// -retry-callbacks -since=…`): the failed deliveries since then are resent in
// batches of 50, at most 5 calls a minute. It returns the references asked.
func (s *ByokSweep) RetryCallbacks(ctx context.Context, conn *byokconn.Connection, since time.Time, sleep func(time.Duration)) (int, error) {
	sess := s.conns.Session(conn)
	calls, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) ([]byok.CallbackAttempt, error) {
		return sess.P.ListCallbacks(ctx, cr, t, since)
	})
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var refs []string
	for _, c := range calls {
		if (c.HTTPStatus < 200 || c.HTTPStatus > 299) && !seen[c.ChargeRef] {
			seen[c.ChargeRef] = true
			refs = append(refs, c.ChargeRef)
		}
	}
	for i := 0; i < len(refs); i += retryBatch {
		if i > 0 {
			sleep(time.Minute / retryCallsMinute)
		}
		batch := refs[i:min(i+retryBatch, len(refs))]
		if _, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (struct{}, error) {
			return struct{}{}, sess.P.RetryCallbacks(ctx, cr, t, batch)
		}); err != nil {
			return i, err
		}
	}
	return len(refs), nil
}

// FollowPayouts asks the bank about every payout still processing, live only.
// A payout the bank has not settled after payoutStuckAfter is returned as an
// error so the run alerts: money that left the account and is not accounted for
// is never left quiet.
func (s *ByokSweep) FollowPayouts(ctx context.Context, livemode bool, now time.Time) []error {
	if !livemode || s.payouts == nil {
		return nil
	}
	var errs []error
	var start map[string]types.AttributeValue
	for {
		page, err := s.payments.ProcessingPayouts(ctx, livemode, start, 100)
		if err != nil {
			return append(errs, err)
		}
		for i := range page.Items {
			p := page.Items[i]
			if now.Sub(p.UpdatedAt) < payoutMinAge {
				continue
			}
			if err := s.payouts.Resolve(ctx, &p, now); err != nil {
				errs = append(errs, fmt.Errorf("payout %s: %w", p.ID, err))
			}
			if !p.Final() && now.Sub(p.CreatedAt) > payoutStuckAfter {
				errs = append(errs, fmt.Errorf("payout %s has been processing since %s", p.ID, p.CreatedAt.Format(time.RFC3339)))
			}
		}
		if page.LastEvaluatedKey == nil {
			return errs
		}
		start = page.LastEvaluatedKey
	}
}
