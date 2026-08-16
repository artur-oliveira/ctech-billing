// Package wallet is billing's client for ctech-wallet's charge contract.
//
// **The route exists in wallet as of 2026-08-16**
// (`ctech-wallet/api/internal/services/charge_amount.go`, mounted at
// `api/internal/api/v1/router.go`). This package was written first, against
// docs/specs/2026-08-15-wallet-invoice-charge.md, so the field names below are
// the spec's rather than a copy of wallet's structs — they were verified against
// the shipped handler, and the spec is what both sides answer to if they drift.
//
// What is still not code, and blocks the first real payment: billing's entry in
// wallet's `/ctech-wallet/{env}/m2m-clients` needs a `WebhookURL`, or wallet
// confirms the charge and notifies nobody, leaving settlement to the hourly
// reconcile sweep. A deployment without WALLET_BASE_URL has no checkout routes
// at all rather than routes that fail at the last step.
//
// Two things in here are not negotiable and are the reason this is a package
// rather than three inline HTTP calls:
//
//   - A webhook is a wake-up signal, never payment authority. VerifySignature
//     proves who sent it; GetCharge is what says whether it is true. That is
//     wallet's own posture toward its provider, and it must not get weaker one
//     layer up.
//   - The idempotency key is the caller's, and it is deterministic. A retried
//     request must return the charge that already exists rather than open a
//     second one against the same invoice.
package wallet

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/oauth2client"
)

// HeaderSignature is what wallet signs its notify-back with
// (ctech-wallet/api/internal/services/m2m_webhook.go:26).
const HeaderSignature = "X-Wallet-Signature"

// ScopeChargeAmount is the M2M scope the charge route requires. It is deliberately
// not wallet's `internal:wallet:product-purchase`: that one is bounded by a fixed
// catalogue, and a caller who may name their own amount must not be reachable
// through it (spec § 2.1).
const ScopeChargeAmount = "internal:wallet:charge-amount"

// Charge statuses, as wallet reports them
// (ctech-wallet/api/internal/domain/wallet/model.go:410).
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
)

// ErrChargeRejected reports that wallet refused to open the charge — over the
// client's ceiling, or the same idempotency key with a different amount. Both are
// permanent for this request: retrying the identical call cannot succeed.
var ErrChargeRejected = errors.New("wallet refused the charge")

// ErrChargeNotFound reports a charge id wallet does not know.
//
// It is separated from every other failure because reconciliation acts on it and
// on nothing else: an unreachable wallet is an outage to retry, while a charge
// wallet cannot account for is an integration fault to alarm on. Collapsing the
// two would either abandon attempts during an outage or stay silent through a
// real bug.
var ErrChargeNotFound = errors.New("wallet does not know this charge")

// Charge is what wallet returns when a charge is opened or read.
type Charge struct {
	ID string `json:"purchase_id"`
	// Reference is the caller-owned label; billing sends the invoice id. Wallet
	// stores it where the SKU is stored today, so the row shape and the webhook
	// payload stay identical to the product-purchase flow (spec § 2.2).
	Reference string `json:"sku"`
	UserID    string `json:"user_id"`
	Amount    int64  `json:"amount_expected"`
	Status    string `json:"status"`
	PixCode   string `json:"pix_copia_e_cola"`
	ExpiresAt string `json:"expires_at"`
}

// Paid reports a charge the provider has actually settled.
func (c *Charge) Paid() bool { return c.Status == StatusConfirmed }

// Notification is wallet's notify-back body
// (ctech-wallet/api/internal/services/m2m_webhook.go:36).
//
// Nothing in it is trusted. It is parsed only far enough to learn which charge
// to go and ask wallet about.
type Notification struct {
	ChargeID string `json:"purchase_id"`
	Kind     string `json:"kind"`
}

// Config is what the client needs to reach wallet.
type Config struct {
	BaseURL       string
	TokenURL      string
	ClientID      string
	ClientSecret  string
	WebhookSecret string
	Cache         cache.Backend
}

// Enabled reports a configuration complete enough to collect money. An
// incomplete one disables the checkout routes rather than failing at the last
// step, in front of a customer holding a bill.
func (c Config) Enabled() bool {
	return c.BaseURL != "" && c.TokenURL != "" && c.ClientID != "" && c.ClientSecret != "" && c.WebhookSecret != ""
}

// Client calls wallet's charge routes.
type Client struct {
	http    *http.Client
	tokens  *oauth2client.TokenManager
	baseURL string
	secret  []byte
}

// New builds a client. The token manager is api-commons', not a fourth copy of
// client-credentials caching — kycclient and walletclient in ctech-wallet were
// already merged into it.
func New(cfg Config) *Client {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	return &Client{
		http:    httpClient,
		tokens:  oauth2client.New(httpClient, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, ScopeChargeAmount),
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		secret:  []byte(cfg.WebhookSecret),
	}
}

// OpenChargeInput opens one charge.
type OpenChargeInput struct {
	// UserID is the payer's ctech-account subject. Wallet's purchase path is keyed
	// on it end to end, which is why a merchant's own customer cannot pay through
	// this route at all (spec § 4). Billing refuses before calling rather than
	// inventing one.
	UserID string `json:"user_id"`
	// Amount is in centavos, and it is the whole point of the contract: an invoice
	// total is arbitrary because of proration and metered usage, so it cannot come
	// from a catalogue.
	Amount int64 `json:"amount_cents"`
	// Reference is the invoice id.
	Reference string `json:"reference"`
	// IdempotencyKey is {invoice_id}:{attempt_number}.
	IdempotencyKey string `json:"idempotency_key"`
	// PayerTaxID is an optional CPF the rail uses to match the payer. Sent when
	// billing has one, never stored by billing for this purpose.
	PayerTaxID string `json:"payer_tax_id,omitempty"`
}

// OpenCharge opens a PIX charge for an arbitrary amount.
func (c *Client) OpenCharge(ctx context.Context, in OpenChargeInput) (*Charge, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/v1.0/internal/wallet/charge", body)
}

// GetCharge re-reads a charge from wallet.
//
// Every confirmation goes through here. The webhook body says a charge reached a
// terminal status; this says whether wallet agrees, and only wallet's answer is
// allowed to move money in billing.
func (c *Client) GetCharge(ctx context.Context, chargeID string) (*Charge, error) {
	return c.do(ctx, http.MethodGet, "/v1.0/internal/wallet/charge/"+chargeID, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*Charge, error) {
	token, err := c.tokens.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("wallet: token: %w", err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wallet: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrChargeNotFound, path)
	case resp.StatusCode == http.StatusUnprocessableEntity, resp.StatusCode == http.StatusConflict:
		// The two refusals the contract defines: over the ceiling, and the same
		// idempotency key with a different amount. Both are wrong requests, not
		// outages, so they must not be retried and must not read as "wallet is down".
		return nil, fmt.Errorf("%w (%d): %s", ErrChargeRejected, resp.StatusCode, truncate(raw))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("wallet: %s %s: status %d: %s", method, path, resp.StatusCode, truncate(raw))
	}

	var charge Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		return nil, fmt.Errorf("wallet: %s %s: %w", method, path, err)
	}
	if charge.ID == "" {
		return nil, fmt.Errorf("wallet: %s %s: response carries no charge id", method, path)
	}
	return &charge, nil
}

// VerifySignature checks wallet's HMAC over the exact bytes received.
//
// Over the raw body, never over a re-marshalled struct: re-marshalling reorders
// keys and drops unknown fields, so the bytes signed and the bytes checked stop
// being the same bytes and every signature fails — or worse, someone "fixes" it
// by not checking.
func (c *Client) VerifySignature(body []byte, header string) bool {
	if len(c.secret) == 0 || header == "" {
		return false
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header))
}

func truncate(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max])
	}
	return string(b)
}
