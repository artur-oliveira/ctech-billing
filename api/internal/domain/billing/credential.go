package billing

import (
	"errors"
	"fmt"
)

// ErrCredentialInactive reports a credential that exists but may not be used.
var ErrCredentialInactive = errors.New("credential is not active")

// APICredential maps an OAuth client issued by ctech-account to the tenant it
// acts for.
//
// It is a **reference, never a copy**: billing stores the client id and the
// tenant it belongs to, and nothing about the client itself — no secret, no
// scopes, no rotation state. Those live in ctech-account, which is where API
// keys and OAuth clients are already implemented and where they belong
// (assessment § 14.6). Copying any of it here creates a second place that can
// disagree about whether a credential is still valid.
//
// It is also how test and live modes stay separated without a claim in the
// token: **the credential decides the mode**. A client provisioned for test
// resolves to the test partition and physically cannot address live data
// (ADR 0003). That is why the mode is not a request parameter — a parameter can
// be sent by mistake, a credential cannot.
type APICredential struct {
	// ClientID is ctech-account's OAuth client identifier (the token's azp).
	ClientID       string `dynamodbav:"client_id"       json:"client_id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`
	// Description is for humans reading the console: which integration is this.
	Description string `dynamodbav:"description,omitempty" json:"description,omitempty"`
	// Active is billing's own kill switch. Revoking the client in ctech-account
	// is the real revocation; this is the one billing can flip immediately
	// without a cross-service call, for the case where a merchant's integration
	// is misbehaving right now.
	Active bool `dynamodbav:"active" json:"active"`
}

// Validate checks the credential can be used to act for a tenant.
func (c *APICredential) Validate() error {
	if c.ClientID == "" || c.OrganizationID == "" {
		return fmt.Errorf("%w: incomplete credential", ErrCredentialInactive)
	}
	if !c.Active {
		return fmt.Errorf("%w: %s", ErrCredentialInactive, c.ClientID)
	}
	return nil
}
