// Package byokconn is a BYOK connection: an organization's own bank or PSP
// integration (spec 2026-10-11 § 3). The sealed credentials live here only as
// ciphertext; the key is the outbound Lambda's.
package byokconn

import (
	"slices"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

type State string

const (
	StateActive       State = "active"
	StateExpired      State = "expired"
	StateDisconnected State = "disconnected"
)

type WebhookStatus string

const (
	WebhookNotRegistered WebhookStatus = "not_registered"
	WebhookRegistered    WebhookStatus = "registered"
	WebhookConflict      WebhookStatus = "conflict"
	WebhookOverwritten   WebhookStatus = "overwritten"
)

type Kind string

const (
	KindMerchant      Kind = "merchant"
	KindSandboxShared Kind = "sandbox_shared"
)

type SyncState string

const (
	SyncActive       SyncState = "active"
	SyncDisconnected SyncState = "disconnected"
)

// AuthorizationVersion is the version of the "I authorize CTech" wording the
// connect form shows (spec § 3.2). Bump it with the wording.
const AuthorizationVersion = "2026-10-11"

// DegradedAfter is the consecutive-failure count shown as "Atenção" (§ 3.4).
const DegradedAfter = 3

type Connection struct {
	AccountOrganizationID string            `dynamodbav:"account_organization_id" json:"-"`
	Livemode              bool              `dynamodbav:"livemode"               json:"livemode"`
	Provider              string            `dynamodbav:"provider"               json:"provider"`
	ID                    string            `dynamodbav:"id"                     json:"id"`
	Kind                  Kind              `dynamodbav:"kind"                   json:"kind"`
	State                 State             `dynamodbav:"state"                  json:"state"`
	ClientID              string            `dynamodbav:"client_id,omitempty"    json:"client_id,omitempty"`
	AccountNumber         string            `dynamodbav:"account_number,omitempty" json:"account_number,omitempty"`
	PixKey                string            `dynamodbav:"pix_key,omitempty"      json:"pix_key,omitempty"`
	Sealed                string            `dynamodbav:"sealed,omitempty"       json:"-"`
	Version               int               `dynamodbav:"version"                json:"-"`
	Revision              int               `dynamodbav:"revision"               json:"-"`
	CertSubject           string            `dynamodbav:"cert_subject,omitempty"     json:"cert_subject,omitempty"`
	CertFingerprint       string            `dynamodbav:"cert_fingerprint,omitempty" json:"cert_fingerprint,omitempty"`
	CertNotAfter          time.Time         `dynamodbav:"cert_not_after,omitempty"   json:"cert_not_after,omitzero"`
	Capabilities          []byok.Capability `dynamodbav:"capabilities"           json:"capabilities"`
	ScopesGranted         []string          `dynamodbav:"scopes_granted,omitempty" json:"scopes_granted,omitempty"`
	MissingScopes         []string          `dynamodbav:"missing_scopes,omitempty" json:"missing_scopes,omitempty"`
	HookID                string            `dynamodbav:"hook_id,omitempty"      json:"-"`
	WebhookStatus         WebhookStatus     `dynamodbav:"webhook_status,omitempty" json:"webhook_status,omitempty"`
	LastTokenOKAt         time.Time         `dynamodbav:"last_token_ok_at,omitempty" json:"last_token_ok_at,omitzero"`
	LastSyncOKAt          time.Time         `dynamodbav:"last_sync_ok_at,omitempty"  json:"last_sync_ok_at,omitzero"`
	LastErrorCode         string            `dynamodbav:"last_error_code,omitempty"  json:"last_error_code,omitempty"`
	LastErrorAt           time.Time         `dynamodbav:"last_error_at,omitempty"    json:"last_error_at,omitzero"`
	ConsecutiveFailures   int               `dynamodbav:"consecutive_failures"   json:"consecutive_failures"`
	DegradedAlertedOn     string            `dynamodbav:"degraded_alerted_on,omitempty" json:"-"`
	Warned90              bool              `dynamodbav:"warned_90,omitempty"    json:"-"`
	Warned30              bool              `dynamodbav:"warned_30,omitempty"    json:"-"`
	Warned7               bool              `dynamodbav:"warned_7,omitempty"     json:"-"`
	WarnedExpired         bool              `dynamodbav:"warned_expired,omitempty" json:"-"`
	AuthorizedBy          string            `dynamodbav:"authorized_by,omitempty" json:"-"`
	AuthorizedAt          time.Time         `dynamodbav:"authorized_at,omitempty" json:"authorized_at,omitzero"`
	AuthorizationVersion  string            `dynamodbav:"authorization_version,omitempty" json:"-"`
	NotifyEmail           string            `dynamodbav:"notify_email,omitempty" json:"notify_email,omitempty"`
	OnboardingStep        string            `dynamodbav:"onboarding_step,omitempty" json:"onboarding_step,omitempty"`
	OnboardingSince       time.Time         `dynamodbav:"onboarding_since,omitempty" json:"onboarding_since,omitzero"`
	LedgerAccountID       string            `dynamodbav:"ledger_account_id,omitempty" json:"ledger_account_id,omitempty"`
	SyncState             SyncState         `dynamodbav:"sync_state,omitempty"   json:"sync_state,omitempty"`
	FirstDay              brcal.Date        `dynamodbav:"first_day,omitempty"    json:"first_day,omitzero"`
	SyncedThrough         brcal.Date        `dynamodbav:"synced_through,omitempty" json:"synced_through,omitzero"`
	BackfillFrom          brcal.Date        `dynamodbav:"backfill_from,omitempty" json:"backfill_from,omitzero"`
	BackfillDone          bool              `dynamodbav:"backfill_done,omitempty" json:"backfill_done"`
	LastManualSyncAt      time.Time         `dynamodbav:"last_manual_sync_at,omitempty" json:"-"`
	// AuditedOn is the São Paulo day of the last daily webhook audit (§ 5 step 4).
	AuditedOn string    `dynamodbav:"audited_on,omitempty" json:"-"`
	CreatedAt time.Time `dynamodbav:"connected_at"           json:"connected_at"`
}

func (c *Connection) Has(cap byok.Capability) bool { return slices.Contains(c.Capabilities, cap) }

func (c *Connection) Degraded() bool {
	return c.State == StateActive && c.ConsecutiveFailures >= DegradedAfter
}

func (c *Connection) Ref() byok.ConnectionRef {
	if c.Kind == KindSandboxShared {
		return byok.ConnectionRef{ID: "sandbox", AccountOrganizationID: c.AccountOrganizationID}
	}
	return byok.ConnectionRef{AccountOrganizationID: c.AccountOrganizationID, ID: c.ID, Version: c.Version,
		Sealed: c.Sealed, Account: c.AccountNumber, PixKey: c.PixKey}
}

// CertWarning returns the threshold to warn about now, if not warned yet. Only
// the most recent threshold passed is returned, so a job down for weeks never
// sends D-90 after D-30.
func (c *Connection) CertWarning(now time.Time) (string, bool) {
	if c.CertNotAfter.IsZero() || c.State == StateDisconnected {
		return "", false
	}
	left := c.CertNotAfter.Sub(now)
	day := 24 * time.Hour
	var threshold string
	var warned bool
	switch {
	case left <= 0:
		threshold, warned = "expired", c.WarnedExpired
	case left <= 7*day:
		threshold, warned = "7", c.Warned7
	case left <= 30*day:
		threshold, warned = "30", c.Warned30
	case left <= 90*day:
		threshold, warned = "90", c.Warned90
	default:
		return "", false
	}
	if warned {
		return "", false
	}
	return threshold, true
}

// MarkWarned marks threshold and every earlier one.
func (c *Connection) MarkWarned(threshold string) {
	switch threshold {
	case "expired":
		c.WarnedExpired = true
		fallthrough
	case "7":
		c.Warned7 = true
		fallthrough
	case "30":
		c.Warned30 = true
		fallthrough
	case "90":
		c.Warned90 = true
	}
}

// ResetWarnings is called by a renewal.
func (c *Connection) ResetWarnings() {
	c.Warned90, c.Warned30, c.Warned7, c.WarnedExpired = false, false, false, false
}

// ExpireIfPast moves an active connection past notAfter to expired.
func (c *Connection) ExpireIfPast(now time.Time) bool {
	if c.State != StateActive || c.CertNotAfter.IsZero() || now.Before(c.CertNotAfter) {
		return false
	}
	c.State = StateExpired
	return true
}
