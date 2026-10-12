package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ByokStep names one per-connection step of cmd/byok (spec § 5). Part A runs
// sync and certificates; part B adds sweep and audit, part C usage.
type ByokStep string

const (
	StepSweep        ByokStep = "sweep"
	StepSync         ByokStep = "sync"
	StepCertificates ByokStep = "certificates"
	StepAudit        ByokStep = "audit"
	StepUsage        ByokStep = "usage"
)

// stepOrder is the order steps run in for one connection, whatever the order
// they were registered in.
// Certificates first: an expired certificate makes the bank refuse every call,
// so its expiry and notice must never wait for a sync that cannot succeed.
var stepOrder = []ByokStep{StepCertificates, StepSweep, StepSync, StepAudit, StepUsage}

// ByokStepFunc runs one step for one connection, in that connection's space.
type ByokStepFunc func(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) error

// ByokMailer sends the organization's connection notices.
type ByokMailer interface {
	SendByokNotice(ctx context.Context, to string, n email.ByokNotice) error
}

type byokJobRepo interface {
	ConnectionsForJob(ctx context.Context, livemode bool, startKey map[string]types.AttributeValue, limit int) (*repositories.Page[byokconn.Connection], error)
	SaveConnection(ctx context.Context, c *byokconn.Connection, actor, requestID, action string, now time.Time) error
	// LoadSealed adds the sealed bundle the job index does not carry.
	LoadSealed(ctx context.Context, c *byokconn.Connection) error
}

// ByokJob is cmd/byok's work: every connection of a mode, through a bounded
// worker pool, each step idempotent. Cross-organization by design (ADR 0002);
// the space of each connection is built by the caller (spaceFor: space.ForJob
// in cmd/byok, never a request).
type ByokJob struct {
	repo       byokJobRepo
	mail       ByokMailer
	alerts     alerts.Publisher
	spaceFor   func(*byokconn.Connection) (space.ResolvedSpace, error)
	steps      map[ByokStep]ByokStepFunc
	consoleURL string
	// sandboxCert reads the shared sandbox integration's certificate expiry
	// (the wallet's Inter integration, spec amendment 2026-10-11); guard keeps
	// the operator alert to once per threshold.
	sandboxCert func(context.Context, *byokconn.Connection) (time.Time, error)
	guard       cache.Backend
}

const (
	byokWorkers  = 16
	byokPageSize = 100
)

func NewByokJob(repo byokJobRepo, conns *ByokConnections, st *ByokStatement, mail ByokMailer, p alerts.Publisher,
	spaceFor func(*byokconn.Connection) (space.ResolvedSpace, error)) *ByokJob {
	if p == nil {
		p = alerts.Nop{}
	}
	j := &ByokJob{repo: repo, mail: mail, alerts: p, spaceFor: spaceFor, steps: map[ByokStep]ByokStepFunc{}}
	if st != nil {
		j.Register(StepSync, func(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) error {
			_, err := st.Sync(ctx, sp, conn, now)
			return err
		})
	}
	j.Register(StepCertificates, j.certificates)
	return j
}

// WithConsoleURL sets the link the notices carry (the connection screen).
func (j *ByokJob) WithConsoleURL(u string) *ByokJob { j.consoleURL = u; return j }

// WithSandboxCertificate turns on the operator's warning about the shared
// sandbox integration's certificate (D-30, D-7, D-1; spec § 9).
func (j *ByokJob) WithSandboxCertificate(read func(context.Context, *byokconn.Connection) (time.Time, error), guard cache.Backend) *ByokJob {
	j.sandboxCert, j.guard = read, guard
	return j
}

// Register adds or replaces a step.
func (j *ByokJob) Register(step ByokStep, fn ByokStepFunc) { j.steps[step] = fn }

type ByokRunResult struct {
	Connections int
	Errors      []error
}

func (j *ByokJob) Run(ctx context.Context, livemode bool, now time.Time) ByokRunResult {
	var res ByokRunResult
	var mu sync.Mutex
	record := func(err error) {
		mu.Lock()
		res.Errors = append(res.Errors, err)
		mu.Unlock()
	}
	var sandbox *byokconn.Connection
	work := make(chan byokconn.Connection)
	var wg sync.WaitGroup
	for range byokWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for conn := range work {
				if err := j.runOne(ctx, &conn, now); err != nil {
					record(err)
				}
			}
		}()
	}
	var start map[string]types.AttributeValue
	for {
		page, err := j.repo.ConnectionsForJob(ctx, livemode, start, byokPageSize)
		if err != nil {
			record(err)
			break
		}
		for _, c := range page.Items {
			if sandbox == nil && c.Kind == byokconn.KindSandboxShared {
				sb := c
				sandbox = &sb
			}
			mu.Lock()
			res.Connections++
			mu.Unlock()
			work <- c
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		start = page.LastEvaluatedKey
	}
	close(work)
	wg.Wait()
	if sandbox != nil && j.sandboxCert != nil {
		if err := j.sandboxCertificate(ctx, sandbox, now); err != nil {
			res.Errors = append(res.Errors, err)
		}
	}
	return res
}

// sandboxCertificate warns the operator, once per threshold, that the shared
// sandbox integration (CTech's own, the wallet's) expires soon. Test mode only.
func (j *ByokJob) sandboxCertificate(ctx context.Context, conn *byokconn.Connection, now time.Time) error {
	notAfter, err := j.sandboxCert(ctx, conn)
	if err != nil || notAfter.IsZero() {
		return err
	}
	days := int(notAfter.Sub(now).Hours()/24) + 1
	threshold := ""
	switch {
	case days <= 1:
		threshold = "1"
	case days <= 7:
		threshold = "7"
	case days <= 30:
		threshold = "30"
	default:
		return nil
	}
	key := "byok:sandbox-cert-warned:" + notAfter.UTC().Format("20060102") + ":" + threshold
	if _, seen, _ := j.guard.Get(ctx, key); seen {
		return nil
	}
	j.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "the shared sandbox integration's certificate expires soon",
		Detail: fmt.Sprintf("The Inter integration test mode uses (the wallet's) expires on %s (D-%s). Renewing it for the wallet renews test mode too.",
			notAfter.UTC().Format("2006-01-02"), threshold)})
	return j.guard.Set(ctx, key, []byte("1"), 40*24*3600)
}

// runOne runs the steps in order; the first failure stops this connection only.
func (j *ByokJob) runOne(ctx context.Context, conn *byokconn.Connection, now time.Time) error {
	sp, err := j.spaceFor(conn)
	if err != nil {
		return fmt.Errorf("%s %s: space: %w", conn.AccountOrganizationID, conn.Provider, err)
	}
	if err := j.repo.LoadSealed(ctx, conn); err != nil {
		return fmt.Errorf("%s %s: sealed: %w", conn.AccountOrganizationID, conn.Provider, err)
	}
	for _, step := range stepOrder {
		fn, ok := j.steps[step]
		if !ok {
			continue
		}
		if err := fn(ctx, sp, conn, now); err != nil {
			return fmt.Errorf("%s %s %s: %w", conn.AccountOrganizationID, conn.Provider, step, err)
		}
	}
	return nil
}

// certificates is spec § 3.4 and § 3.5: expiry, warnings at D-90/D-30/D-7 and
// on expiry, each once (send, then mark: a crash in between sends twice, never
// zero times), and a degraded connection reported once per São Paulo day.
// The shared sandbox is CTech's own integration: its certificate is the
// wallet's, renewed by the operator, so it gets no merchant mail.
func (j *ByokJob) certificates(ctx context.Context, _ space.ResolvedSpace, conn *byokconn.Connection, now time.Time) error {
	if conn.Kind == byokconn.KindSandboxShared {
		return nil
	}
	if conn.ExpireIfPast(now) {
		if err := j.repo.SaveConnection(ctx, conn, "system:byok", "", "byok.expired", now); err != nil {
			return err
		}
	}
	if threshold, due := conn.CertWarning(now); due {
		kind := "cert_" + threshold
		if err := j.notify(ctx, conn, kind); err != nil {
			return err
		}
		conn.MarkWarned(threshold)
		if err := j.repo.SaveConnection(ctx, conn, "system:byok", "", "", now); err != nil {
			return err
		}
	}
	today := brcal.FromTime(now).String()
	if conn.Degraded() && conn.DegradedAlertedOn != today {
		j.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "a bank connection is degraded",
			Detail: fmt.Sprintf("connection %s (%s, organization %s): %d consecutive failures, last %s",
				conn.ID, conn.Provider, conn.AccountOrganizationID, conn.ConsecutiveFailures, conn.LastErrorCode)})
		if err := j.notify(ctx, conn, "degraded"); err != nil {
			slog.WarnContext(ctx, "byok degraded notice", "connection_id", conn.ID, "error", err)
		}
		conn.DegradedAlertedOn = today
		if err := j.repo.SaveConnection(ctx, conn, "system:byok", "", "", now); err != nil {
			return err
		}
	}
	return nil
}

var errNoMailer = errors.New("byok: no mailer configured")

func (j *ByokJob) notify(ctx context.Context, conn *byokconn.Connection, kind string) error {
	n := email.ByokNotice{Kind: kind, Provider: conn.Provider, NotAfter: conn.CertNotAfter}
	if j.consoleURL != "" {
		n.ConsoleURL = j.consoleURL + "/" + conn.Provider
	}
	if j.mail == nil || conn.NotifyEmail == "" {
		// No mail transport (EMAIL_FROM empty) or no address: the operator is told
		// instead, so a warning is never lost silently.
		j.alerts.Alert(ctx, alerts.Alert{Job: "byok", Summary: "a bank connection notice could not be e-mailed",
			Detail: fmt.Sprintf("%s for connection %s (%s, organization %s)", kind, conn.ID, conn.Provider, conn.AccountOrganizationID),
			Err:    errNoMailer})
		return nil
	}
	return j.mail.SendByokNotice(ctx, conn.NotifyEmail, n)
}
