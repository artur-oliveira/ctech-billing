package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/closure"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Blocker codes of a tenant closure (spec T3), stable and translated by the
// console. Live mode only: test money is not owed.
const (
	BlockerClosureSubscriptions = "billing.tenant_active_subscriptions"
	BlockerClosurePayments      = "billing.tenant_payment_in_flight"
	BlockerClosurePayouts       = "billing.tenant_payout_processing"
	BlockerClosureRefunds       = "billing.tenant_refund_open"
)

// ClosureBlocker is one reason a tenant cannot be closed yet.
type ClosureBlocker struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// ClosureBlockedError lists what the owner must resolve first.
type ClosureBlockedError struct{ Blockers []ClosureBlocker }

func (e *ClosureBlockedError) Error() string {
	return fmt.Sprintf("closure: %d blocker(s)", len(e.Blockers))
}

// ClosureFacts counts what blocks a closure for one live billing tenant row.
type ClosureFacts interface {
	ActiveSubscriptions(ctx context.Context, tenantID string) (int64, error)
	PaymentsInFlight(ctx context.Context, tenantID string) (int64, error)
	PayoutsProcessing(ctx context.Context, tenantID string) (int64, error)
	RefundsOpen(ctx context.Context, tenantID string) (int64, error)
}

// ClosureOwners answers whether a ctech-account user owns a workspace, live.
type ClosureOwners interface {
	IsOwner(ctx context.Context, sub, accountOrgID string) (bool, error)
}

// ClosureTenants finds the billing tenant rows (live and test) of a workspace.
type ClosureTenants interface {
	TenantsOf(ctx context.Context, accountOrgID string) ([]*billing.Organization, error)
}

// ClosureMailer sends the owner's closure e-mails. product-agnostic copy lives in
// the implementation.
type ClosureMailer interface {
	SendClosureConfirm(ctx context.Context, sub, requestID, token string) error
	SendClosureScheduled(ctx context.Context, sub, requestID, cancelToken string, graceUntil time.Time) error
	SendClosureBlocked(ctx context.Context, sub, requestID, cancelToken string, blockers []string) error
	SendClosureCancelled(ctx context.Context, sub string) error
}

// ClosurePurger erases a closed tenant. Nil until the purge phase ships: a
// closure that reaches the end of grace then waits as ready_to_purge.
type ClosurePurger interface {
	Purge(ctx context.Context, c *closure.Closure, tenants []*billing.Organization) error
}

// TenantClosures is the lifecycle of closing a merchant tenant
// (docs/specs/2026-10-17-merchant-tenant-deletion.md).
type TenantClosures struct {
	repo       *repositories.ClosureRepository
	tenants    ClosureTenants
	owners     ClosureOwners
	facts      ClosureFacts
	mail       ClosureMailer
	purger     ClosurePurger
	tenantZero string
	now        func() time.Time
	newID      func() string
}

func NewTenantClosures(repo *repositories.ClosureRepository, tenants ClosureTenants, owners ClosureOwners, facts ClosureFacts, mail ClosureMailer, tenantZero string) *TenantClosures {
	return &TenantClosures{repo: repo, tenants: tenants, owners: owners, facts: facts, mail: mail, tenantZero: tenantZero,
		now: time.Now, newID: func() string { return id.NewWithPrefix("clo") }}
}

// WithClock replaces the clock (tests pin the grace period with it).
func (s *TenantClosures) WithClock(now func() time.Time) *TenantClosures { s.now = now; return s }

// WithPurger enables the purge step of the worker.
func (s *TenantClosures) WithPurger(p ClosurePurger) *TenantClosures { s.purger = p; return s }

// tenantsOf is the workspace's tenant rows, refusing tenant zero.
func (s *TenantClosures) tenantsOf(ctx context.Context, acct string) ([]*billing.Organization, error) {
	ts, err := s.tenants.TenantsOf(ctx, acct)
	if err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, closure.ErrNotFound
	}
	for _, t := range ts {
		if s.tenantZero != "" && t.ID == s.tenantZero {
			return nil, closure.ErrTenantZero
		}
	}
	return ts, nil
}

// Blockers counts what blocks closing the workspace's tenants (live rows only).
func (s *TenantClosures) Blockers(ctx context.Context, acct string) ([]ClosureBlocker, error) {
	ts, err := s.tenantsOf(ctx, acct)
	if err != nil {
		return nil, err
	}
	var out []ClosureBlocker
	for _, t := range ts {
		if !t.Livemode {
			continue
		}
		for _, check := range []struct {
			code string
			fn   func(context.Context, string) (int64, error)
		}{
			{BlockerClosureSubscriptions, s.facts.ActiveSubscriptions},
			{BlockerClosurePayments, s.facts.PaymentsInFlight},
			{BlockerClosurePayouts, s.facts.PayoutsProcessing},
			{BlockerClosureRefunds, s.facts.RefundsOpen},
		} {
			n, err := check.fn(ctx, t.ID)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				out = append(out, ClosureBlocker{Code: check.code, Count: n})
			}
		}
	}
	return out, nil
}

// Request opens a closure awaiting the owner's e-mail confirmation. An earlier
// unconfirmed request is replaced, so a lost e-mail never blocks the owner.
func (s *TenantClosures) Request(ctx context.Context, sub, acct string) (*closure.Closure, error) {
	if owner, err := s.owners.IsOwner(ctx, sub, acct); err != nil {
		return nil, err
	} else if !owner {
		return nil, closure.ErrNotOwner
	}
	blockers, err := s.Blockers(ctx, acct)
	if err != nil {
		return nil, err
	}
	if len(blockers) > 0 {
		return nil, &ClosureBlockedError{Blockers: blockers}
	}
	raw, hash, err := closure.NewToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	c := closure.New(s.newID(), acct, sub, now)
	c.ConfirmTokenHash = hash
	err = s.repo.Create(ctx, c, now)
	if errors.Is(err, closure.ErrOpenClosure) {
		if err = s.expireUnconfirmed(ctx, acct); err == nil {
			err = s.repo.Create(ctx, c, now)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.mail.SendClosureConfirm(ctx, sub, c.ID, raw); err != nil {
		// Without the e-mail the request can never be confirmed: release it.
		c.State, c.UpdatedAt = closure.Expired, now
		if saveErr := s.repo.Save(ctx, c, closure.AwaitingConfirmation, now); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, fmt.Errorf("closure: sending the confirmation e-mail: %w", err)
	}
	return c, nil
}

func (s *TenantClosures) expireUnconfirmed(ctx context.Context, acct string) error {
	open, err := s.repo.Open(ctx, acct)
	if err != nil {
		return err
	}
	if open.State != closure.AwaitingConfirmation {
		return closure.ErrOpenClosure
	}
	now := s.now()
	open.State, open.UpdatedAt = closure.Expired, now
	return s.repo.Save(ctx, open, closure.AwaitingConfirmation, now)
}

// Status is the workspace's open closure, or closure.ErrNotFound.
func (s *TenantClosures) Status(ctx context.Context, acct string) (*closure.Closure, error) {
	return s.repo.Open(ctx, acct)
}

// Confirm starts the read-only grace: the state is saved first, then the flags
// are raised. Clicking the link again on a closing request raises them again
// (idempotent), so a crash between the two is repaired by the owner or a retry.
func (s *TenantClosures) Confirm(ctx context.Context, requestID, token string) (*closure.Closure, error) {
	c, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return nil, closure.ErrInvalidToken
	}
	if !closure.TokenMatches(token, c.ConfirmTokenHash) {
		return nil, closure.ErrInvalidToken
	}
	ts, err := s.tenantsOf(ctx, c.AccountOrganizationID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if c.State == closure.Closing {
		return c, s.repo.SetReadOnly(ctx, tenantIDs(ts), c.ID, true, now)
	}
	if err := c.Confirm(now); err != nil {
		if errors.Is(err, closure.ErrExpired) {
			_ = s.repo.Save(ctx, c, closure.AwaitingConfirmation, now)
		}
		return nil, err
	}
	raw, hash, err := closure.NewToken()
	if err != nil {
		return nil, err
	}
	c.CancelTokenHashes = append(c.CancelTokenHashes, hash)
	if err := s.repo.Save(ctx, c, closure.AwaitingConfirmation, now); err != nil {
		return nil, err
	}
	if err := s.repo.SetReadOnly(ctx, tenantIDs(ts), c.ID, true, now); err != nil {
		return nil, err
	}
	if err := s.mail.SendClosureScheduled(ctx, c.RequestedBy, c.ID, raw, c.GraceUntil); err != nil {
		slog.ErrorContext(ctx, "closure: scheduled e-mail failed", "closure", c.ID, "error", err)
	}
	return c, nil
}

// Cancel ends the closure and lifts the read-only flags.
func (s *TenantClosures) Cancel(ctx context.Context, requestID, token string) (*closure.Closure, error) {
	c, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return nil, closure.ErrInvalidToken
	}
	ok := false
	for _, h := range c.CancelTokenHashes {
		ok = ok || closure.TokenMatches(token, h)
	}
	if !ok {
		return nil, closure.ErrInvalidToken
	}
	from := c.State
	now := s.now()
	if err := c.Cancel(now); err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, c, from, now); err != nil {
		return nil, err
	}
	s.liftFlags(ctx, c)
	if err := s.mail.SendClosureCancelled(ctx, c.RequestedBy); err != nil {
		slog.ErrorContext(ctx, "closure: cancelled e-mail failed", "closure", c.ID, "error", err)
	}
	return c, nil
}

func (s *TenantClosures) liftFlags(ctx context.Context, c *closure.Closure) {
	ts, err := s.tenants.TenantsOf(ctx, c.AccountOrganizationID)
	if err != nil {
		slog.ErrorContext(ctx, "closure: reading tenants to lift the read-only flag", "closure", c.ID, "error", err)
		return
	}
	if err := s.repo.SetReadOnly(ctx, tenantIDs(ts), c.ID, false, s.now()); err != nil {
		slog.ErrorContext(ctx, "closure: lifting the read-only flag", "closure", c.ID, "error", err)
	}
}

func tenantIDs(ts []*billing.Organization) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// ProcessDue advances every closure whose next action is due: an unconfirmed
// request expires, a closing one ends its grace (re-checking the blockers), a
// ready one is purged (when a purger is wired). One failing closure never
// blocks the others; it stays due and is retried next tick.
func (s *TenantClosures) ProcessDue(ctx context.Context) {
	due, err := s.repo.Due(ctx, s.now())
	if err != nil {
		slog.ErrorContext(ctx, "closure: reading the due list", "error", err)
		return
	}
	for _, d := range due {
		if err := s.step(ctx, d.Closure); err != nil {
			slog.ErrorContext(ctx, "closure: step failed", "closure", d.Closure.ID, "state", d.Closure.State, "error", err)
		}
	}
}

func (s *TenantClosures) step(ctx context.Context, c *closure.Closure) error {
	now := s.now()
	from := c.State
	switch c.State {
	case closure.AwaitingConfirmation:
		c.State, c.UpdatedAt = closure.Expired, now
		return s.repo.Save(ctx, c, from, now)
	case closure.Closing:
		if !c.GraceOver(now) {
			return nil
		}
		blockers, err := s.Blockers(ctx, c.AccountOrganizationID)
		if err != nil {
			return err
		}
		codes := make([]string, 0, len(blockers))
		for _, b := range blockers {
			codes = append(codes, b.Code)
		}
		c.EndGrace(now, codes)
		if err := s.repo.Save(ctx, c, from, now); err != nil {
			return err
		}
		if c.State == closure.Blocked {
			raw, hash, err := closure.NewToken()
			if err != nil {
				return err
			}
			c.CancelTokenHashes = append(c.CancelTokenHashes, hash)
			if err := s.repo.Save(ctx, c, closure.Blocked, now); err != nil {
				return err
			}
			if err := s.mail.SendClosureBlocked(ctx, c.RequestedBy, c.ID, raw, codes); err != nil {
				slog.ErrorContext(ctx, "closure: blocked e-mail failed", "closure", c.ID, "error", err)
			}
		}
		return nil
	case closure.ReadyToPurge, closure.Purging:
		if s.purger == nil {
			return nil // the purge phase is not wired: the closure waits
		}
		return s.purge(ctx, c)
	}
	return nil
}

// purge runs the purger for a ready or purging closure and records the end.
func (s *TenantClosures) purge(ctx context.Context, c *closure.Closure) error {
	now := s.now()
	if c.State == closure.ReadyToPurge {
		if err := c.StartPurge(now); err != nil {
			return err
		}
		if err := s.repo.Save(ctx, c, closure.ReadyToPurge, now); err != nil {
			return err
		}
	}
	ts, err := s.tenantsOf(ctx, c.AccountOrganizationID)
	if err != nil {
		return err
	}
	if err := s.purger.Purge(ctx, c, ts); err != nil {
		return err
	}
	c.Finish(s.now())
	return s.repo.Save(ctx, c, closure.Purging, s.now())
}
