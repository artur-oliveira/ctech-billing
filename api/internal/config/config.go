// Package config holds the 12-Factor environment configuration.
//
// It is deliberately small: billing has no HTTP server, no OIDC verification and
// no wallet client yet, so it has no configuration for them. Each block arrives
// with the code that reads it — a config struct full of fields nothing uses is a
// list of things a reader has to check are unused.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"

	"gopkg.aoctech.app/billing/api/internal/crypto"
)

// Config is the environment configuration for the billing service.
type Config struct {
	AppVersion string `env:"APP_VERSION" envDefault:"0.0.1"`
	Env        string `env:"ENVIRONMENT" envDefault:"dev"`

	AWSRegion string `env:"AWS_REGION" envDefault:"us-east-1"`
	// TablePrefix namespaces the physical table per environment
	// ({prefix}_{table}). Required, with no default: a service that silently
	// falls back to a shared prefix writes production rows from a dev machine.
	TablePrefix string `env:"TABLE_PREFIX,required"`
	// DynamoDBEndpoint overrides the AWS endpoint for DynamoDB Local in tests.
	DynamoDBEndpoint string `env:"DYNAMODB_ENDPOINT"`

	// HTTP
	Port         int   `env:"PORT"          envDefault:"8004"`
	ReadTimeout  int64 `env:"READ_TIMEOUT"  envDefault:"10"`
	WriteTimeout int64 `env:"WRITE_TIMEOUT" envDefault:"10"`
	IdleTimeout  int64 `env:"IDLE_TIMEOUT"  envDefault:"60"`

	// CorsAllowedOrigins are the exact browser origins the portal and the
	// checkout are served from — same name and same env var as ctech-wallet and
	// ctech-poker.
	//
	// The portal stopped being same-origin with this API on 2026-08-16
	// (ADR 0013's amendment): the pages call `billing-api[-env].aoctech.app`
	// directly instead of being proxied back through their own CloudFront
	// distribution to Cloudflare and on to HAProxy.
	//
	// Exact origins, never patterns — scheme, host and port, no trailing slash.
	// Empty is allowed outside production and means wildcard-without-credentials
	// (see newFiber); in production Load refuses to boot without it.
	CorsAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`

	// Auth (ctech-account). ServiceAudience is the `aud` this service accepts;
	// a token minted for another CTech service must not be usable here.
	CtechIssuerURL  string `env:"CTECH_ISSUER_URL"`
	CtechJWKSURL    string `env:"CTECH_JWKS_URL"`
	ServiceAudience string `env:"SERVICE_AUDIENCE" envDefault:"https://billing.aoctech.app"`

	// PortalOrganizationID names tenant zero: the organization whose customers
	// the portal serves (ADR 0012). It is CTech's own organization, and it is
	// configuration rather than a lookup because there is exactly one and it must
	// never be resolvable from a request.
	//
	// Empty disables the portal entirely — every portal route answers 404. That is
	// the right default for an environment where tenant zero has not been
	// provisioned yet: a portal pointed at no organization must not fall back to
	// pointing at some organization.
	PortalOrganizationID string `env:"PORTAL_ORGANIZATION_ID"`

	// RedisURL backs the JWKS cache. Absent means an in-process cache, which is
	// correct for one instance and merely wasteful for several — each fetches
	// the JWKS itself.
	RedisURL string `env:"VALKEY_URL"`

	// Collection (ADR 0004, docs/specs/2026-08-15-wallet-invoice-charge.md).
	//
	// Billing does not move money; it asks ctech-wallet to. An incomplete block
	// here disables every checkout route rather than letting them fail at the last
	// step, in front of somebody holding a bill.
	WalletBaseURL      string `env:"WALLET_BASE_URL"`
	WalletTokenURL     string `env:"WALLET_TOKEN_URL"`
	WalletClientID     string `env:"WALLET_CLIENT_ID"`
	WalletClientSecret string `env:"WALLET_CLIENT_SECRET"`
	// WalletWebhookSecret verifies wallet's notify-back HMAC. It authenticates the
	// sender and nothing else: the charge is always re-read before an invoice moves.
	WalletWebhookSecret string `env:"WALLET_WEBHOOK_SECRET"`

	// CheckoutLinkSecret signs public payment links. Empty means no public
	// checkout at all — a forgeable link is worse than no link, so there is no
	// default and no fallback.
	CheckoutLinkSecret string `env:"CHECKOUT_LINK_SECRET"`
	// CheckoutBaseURL is where the hosted page lives, used to render the whole URL
	// for an email. Absent only means callers get the token instead.
	CheckoutBaseURL string `env:"CHECKOUT_BASE_URL"`

	// FieldEncryptionKey encrypts the stored values that are personal data on
	// their own — today the customer's CPF/CNPJ (internal/crypto). 32 bytes as
	// base64 or hex, the same shape as ctech-account's SECRET_ENC_KEY.
	//
	// **Required.** Load refuses a deployment without it rather than starting one
	// that writes tax ids in the clear, because that failure is invisible: every
	// screen works, and the damage is only discovered by whoever reads the table.
	// There is no development default on purpose — a constant key in the
	// repository is a published key.
	//
	// Rotating it is not yet a supported operation: values carry a `v1.` marker
	// so a second key can be introduced additively, but nothing reads a second
	// key today. Changing this value makes existing tax ids unreadable.
	FieldEncryptionKey string `env:"FIELD_ENCRYPTION_KEY"`

	// EmailFrom is the verified SES identity dunning reminders are sent from.
	// Empty disables sending: cmd/dunning refuses to start rather than escalating
	// invoices to PAST_DUE without ever having told the customer, which is the
	// worst half of the policy running on its own.
	EmailFrom string `env:"EMAIL_FROM"`
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	// Validated here rather than at first use. A key that is missing or the wrong
	// length is a deployment mistake, and a deployment mistake should stop the
	// process at boot — not surface on the first customer somebody creates.
	if !crypto.NewSealer(cfg.FieldEncryptionKey).Enabled() {
		return nil, fmt.Errorf("config: FIELD_ENCRYPTION_KEY must decode to exactly 32 bytes (base64 or hex)")
	}
	// Fail closed, mirroring ctech-wallet's guard. Empty origins mean a wildcard
	// with no credentials — a reasonable default on a laptop and the wrong one in
	// front of customers, where it would let any page on the internet read every
	// unauthenticated response this API serves.
	if len(cfg.CorsAllowedOrigins) == 0 && cfg.Env == "prod" {
		return nil, fmt.Errorf("config: CORS_ALLOWED_ORIGINS must be set in production")
	}
	return &cfg, nil
}
