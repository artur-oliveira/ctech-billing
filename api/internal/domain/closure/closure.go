// Package closure is the lifecycle of closing a merchant tenant
// (docs/specs/2026-10-17-merchant-tenant-deletion.md): request, e-mail
// confirmation, a read-only grace period, blockers, then the purge. It is pure
// state: persistence and side effects live in repositories and services.
package closure

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"
)

const (
	// GracePeriod is how long a confirmed closure stays cancelable. The tenant is
	// read-only meanwhile (spec T2).
	GracePeriod = 7 * 24 * time.Hour
	// ConfirmWindow is how long the e-mail link is valid.
	ConfirmWindow = 24 * time.Hour
	// Phrase must be typed verbatim by the owner (spec T2).
	Phrase = "ENCERRAR CONTA DE COBRANÇA"
)

type State string

const (
	AwaitingConfirmation State = "awaiting_confirmation"
	// Closing is the read-only grace period.
	Closing State = "closing"
	// Blocked is a closure whose grace ended with a blocker still there.
	Blocked State = "blocked"
	// ReadyToPurge is a closure whose grace ended clean; the purge worker takes it.
	ReadyToPurge State = "ready_to_purge"
	Purging      State = "purging"
	Closed       State = "closed"
	Cancelled    State = "cancelled"
	Expired      State = "expired"
)

var (
	ErrWrongState     = errors.New("closure: the request is not in a state that allows this")
	ErrExpired        = errors.New("closure: the confirmation window has passed")
	ErrNotCancellable = errors.New("closure: the request can no longer be cancelled")
	ErrOpenClosure    = errors.New("closure: this tenant already has an open closure")
	ErrNotFound       = errors.New("closure: request not found")
	ErrTenantZero     = errors.New("closure: tenant zero cannot be closed")
	ErrNotOwner       = errors.New("closure: only an owner of the workspace can close its billing account")
	ErrInvalidToken   = errors.New("closure: link is invalid or expired")
)

// Open reports whether the state holds the tenant's single open-closure marker.
func (s State) Open() bool {
	switch s {
	case Cancelled, Expired, Closed:
		return false
	}
	return true
}

// Closure is one request to close the billing tenants of a ctech-account
// workspace (live and test together).
type Closure struct {
	ID                    string
	AccountOrganizationID string
	RequestedBy           string // ctech-account sub of the owner
	State                 State
	ConfirmTokenHash      string
	CancelTokenHashes     []string
	Blockers              []string // codes found at the end of grace
	RequestedAt           time.Time
	ConfirmBy             time.Time
	ConfirmedAt           time.Time
	GraceUntil            time.Time
	CancelledAt           time.Time
	ClosedAt              time.Time
	UpdatedAt             time.Time
}

func New(id, accountOrgID, requestedBy string, now time.Time) *Closure {
	return &Closure{
		ID: id, AccountOrganizationID: accountOrgID, RequestedBy: requestedBy, State: AwaitingConfirmation,
		RequestedAt: now, ConfirmBy: now.Add(ConfirmWindow), UpdatedAt: now,
	}
}

// ReadOnly reports whether the tenant refuses writes: from confirmation until
// the closure is cancelled. A closed tenant has no row left to protect.
func (c *Closure) ReadOnly() bool {
	switch c.State {
	case Closing, Blocked, ReadyToPurge, Purging:
		return true
	}
	return false
}

// Confirm starts the grace period. A link used after its window expires the
// request instead.
func (c *Closure) Confirm(now time.Time) error {
	if c.State != AwaitingConfirmation {
		return ErrWrongState
	}
	if now.After(c.ConfirmBy) {
		c.State, c.UpdatedAt = Expired, now
		return ErrExpired
	}
	c.State, c.ConfirmedAt, c.GraceUntil, c.UpdatedAt = Closing, now, now.Add(GracePeriod), now
	return nil
}

// Cancel ends the request until the purge starts.
func (c *Closure) Cancel(now time.Time) error {
	switch c.State {
	case Closing, Blocked, ReadyToPurge:
		c.State, c.CancelledAt, c.UpdatedAt = Cancelled, now, now
		return nil
	}
	return ErrNotCancellable
}

// GraceOver reports whether the grace period has ended.
func (c *Closure) GraceOver(now time.Time) bool {
	return c.State == Closing && !now.Before(c.GraceUntil)
}

// EndGrace moves a closing request to Blocked or ReadyToPurge, by whether the
// blockers re-checked at the end of grace are empty.
func (c *Closure) EndGrace(now time.Time, blockers []string) {
	c.UpdatedAt = now
	if len(blockers) > 0 {
		c.State, c.Blockers = Blocked, blockers
		return
	}
	c.State, c.Blockers = ReadyToPurge, nil
}

func (c *Closure) StartPurge(now time.Time) error {
	if c.State != ReadyToPurge {
		return ErrWrongState
	}
	c.State, c.UpdatedAt = Purging, now
	return nil
}

func (c *Closure) Finish(now time.Time) {
	c.State, c.ClosedAt, c.UpdatedAt = Closed, now, now
}

// NewToken returns a random link token and the hash that is stored.
func NewToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	return raw, HashToken(raw), nil
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func TokenMatches(raw, hash string) bool {
	return hash != "" && subtle.ConstantTimeCompare([]byte(HashToken(raw)), []byte(hash)) == 1
}
