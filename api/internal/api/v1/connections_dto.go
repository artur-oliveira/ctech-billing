package v1

import (
	"math"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

// connectionDTO is a bank connection as the console and Finanças show it (BYOK
// spec § 11.2). It never carries the sealed bundle, its version, the webhook
// hook id or who authorized it.
type connectionDTO struct {
	Provider        string            `json:"provider"`
	State           string            `json:"state"`  // onboarding | active | expired | disconnected
	Status          string            `json:"status"` // onboarding | active | degraded | expired | disconnected
	Kind            string            `json:"kind,omitempty"`
	Livemode        bool              `json:"livemode"`
	ClientID        string            `json:"client_id,omitempty"`
	AccountLast4    string            `json:"account_last4,omitempty"`
	PixKey          string            `json:"pix_key,omitempty"`
	CertSubject     string            `json:"cert_subject,omitempty"`
	CertFingerprint string            `json:"cert_fingerprint,omitempty"`
	CertNotAfter    *time.Time        `json:"cert_not_after,omitempty"`
	CertDaysLeft    *int              `json:"cert_days_left,omitempty"`
	Capabilities    []byok.Capability `json:"capabilities"`
	MissingScopes   []string          `json:"missing_scopes"`
	WebhookStatus   string            `json:"webhook_status,omitempty"`
	LastSyncOKAt    *time.Time        `json:"last_sync_ok_at,omitempty"`
	LastErrorCode   string            `json:"last_error_code,omitempty"`
	LastErrorAt     *time.Time        `json:"last_error_at,omitempty"`
	OnboardingStep  string            `json:"onboarding_step,omitempty"`
	OnboardingSince *time.Time        `json:"onboarding_since,omitempty"`
	LedgerAccountID string            `json:"ledger_account_id,omitempty"`
	BackfillDone    bool              `json:"backfill_done"`
	FirstDay        *brcal.Date       `json:"first_day,omitempty"`
	ConnectedAt     *time.Time        `json:"connected_at,omitempty"`
	NotifyEmail     string            `json:"notify_email,omitempty"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toConnectionDTO(c *byokconn.Connection, now time.Time) connectionDTO {
	d := connectionDTO{
		Provider: c.Provider, State: string(c.State), Status: string(c.State), Kind: string(c.Kind), Livemode: c.Livemode,
		ClientID: c.ClientID, PixKey: c.PixKey, CertSubject: c.CertSubject, CertFingerprint: c.CertFingerprint,
		Capabilities: c.Capabilities, MissingScopes: c.MissingScopes, WebhookStatus: string(c.WebhookStatus),
		LastSyncOKAt: timePtr(c.LastSyncOKAt), LastErrorCode: c.LastErrorCode, LastErrorAt: timePtr(c.LastErrorAt),
		OnboardingStep: c.OnboardingStep, OnboardingSince: timePtr(c.OnboardingSince), LedgerAccountID: c.LedgerAccountID,
		BackfillDone: c.BackfillDone, ConnectedAt: timePtr(c.CreatedAt), NotifyEmail: c.NotifyEmail,
	}
	if c.State == "" {
		d.State, d.Status, d.ConnectedAt = "onboarding", "onboarding", nil
	}
	if c.Degraded() {
		d.Status = "degraded"
	}
	if n := len(c.AccountNumber); n > 0 {
		d.AccountLast4 = c.AccountNumber[max(0, n-4):]
	}
	if !c.CertNotAfter.IsZero() {
		d.CertNotAfter = timePtr(c.CertNotAfter)
		days := int(math.Ceil(c.CertNotAfter.Sub(now).Hours() / 24))
		d.CertDaysLeft = &days
	}
	if !c.FirstDay.IsZero() {
		fd := c.FirstDay
		d.FirstDay = &fd
	}
	if d.Capabilities == nil {
		d.Capabilities = []byok.Capability{}
	}
	if d.MissingScopes == nil {
		d.MissingScopes = []string{}
	}
	return d
}

type providerField struct {
	Name     string `json:"name"`
	Secret   bool   `json:"secret,omitempty"`
	File     bool   `json:"file,omitempty"`
	Required bool   `json:"required"`
}

type providerInfo struct {
	Provider string          `json:"provider"`
	Fields   []providerField `json:"fields"`
	Guide    providerGuide   `json:"guide"`
}

// providerGuide is what the person ticks at the bank, in the bank's own terms:
// activities that hold permissions, as the provider's web wizard shows them.
// Each permission lists the API scopes it grants, so a missing scope found at
// connect time is told back as the permission to tick. A provider with no
// activities (or no scopes at all) simply has an empty guide. A permission with
// no scopes is one the integration should hold for products not built yet.
type providerGuide struct {
	NameMax        int             `json:"name_max,omitempty"`
	DescriptionMax int             `json:"description_max,omitempty"`
	SuggestedName  string          `json:"suggested_name,omitempty"`
	AcceptTerms    bool            `json:"accept_terms"`
	Activities     []guideActivity `json:"activities"`
}

type guideActivity struct {
	Code        string            `json:"code"`
	Permissions []guidePermission `json:"permissions"`
}

type guidePermission struct {
	Key    string   `json:"key"`
	Scopes []string `json:"scopes,omitempty"`
}

// providerCatalog is what the onboarding asks for and which permissions to tick
// at the bank, per provider. It mirrors inter.Provider.CredentialFields without
// importing api-commons/inter (only the outbound Lambda imports it). The labels
// are the UI's (connections.guide.*), keyed by activity code and permission key.
func providerCatalog() []providerInfo {
	return []providerInfo{{
		Provider: "inter",
		Fields: []providerField{
			{Name: "client_id", Required: true}, {Name: "client_secret", Secret: true, Required: true},
			{Name: "cert", File: true, Required: true}, {Name: "key", File: true, Secret: true, Required: true},
			{Name: "account", Required: true}, {Name: "pix_key"},
		},
		Guide: providerGuide{
			NameMax: 50, DescriptionMax: 250, SuggestedName: "CTech Billing", AcceptTerms: true,
			Activities: []guideActivity{
				{Code: "pix", Permissions: []guidePermission{
					{Key: "due_charge_write", Scopes: []string{"cobv.write"}},
					{Key: "due_charge_read", Scopes: []string{"cobv.read"}},
					{Key: "pix_refund", Scopes: []string{"pix.write"}},
					{Key: "pix_read", Scopes: []string{"pix.read"}},
					{Key: "webhook_write", Scopes: []string{"webhook.write"}},
					{Key: "webhook_read", Scopes: []string{"webhook.read"}},
					// Ticked now so a later product never needs a second integration.
					{Key: "charge_write"}, {Key: "charge_read"},
					{Key: "due_batch_write"}, {Key: "due_batch_read"},
					{Key: "payload_write"}, {Key: "payload_read"},
				}},
				{Code: "recurring", Permissions: []guidePermission{
					{Key: "recurrence_read"}, {Key: "recurrence_write"},
					{Key: "recurrence_request_read"}, {Key: "recurrence_request_write"},
					{Key: "recurring_charge_read"}, {Key: "recurring_charge_write"},
					{Key: "recurrence_payload_read"}, {Key: "recurrence_payload_write"},
					{Key: "recurrence_webhook_read"}, {Key: "recurrence_webhook_write"},
					{Key: "recurring_charge_webhook_read"}, {Key: "recurring_charge_webhook_write"},
				}},
				{Code: "statement", Permissions: []guidePermission{
					{Key: "statement_read", Scopes: []string{"extrato.read"}},
				}},
			},
		},
	}}
}
