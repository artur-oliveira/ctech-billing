// Package accountclient asks ctech-account for the facts it owns about who
// belongs to what (ADR 0023: reach in the platform, verbs in the product).
//
// It fails closed by construction: a nil client, a non-200, an unreadable body
// and an outage all return an error, and the resolver treats an error as a
// refusal. The same shape as ctech-dfe's reach client, and for the same reason.
package accountclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/oauth2client"
)

// Scope is what the membership route requires; this client holds nothing else.
const Scope = "internal:account:org-member"

const (
	requestTimeout = 6 * time.Second
	maxBody        = 8 << 10
)

// Config is what the client needs to reach ctech-account.
type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Cache        cache.Backend
}

// Client asks ctech-account about organization membership.
type Client struct {
	http    *http.Client
	tokens  *oauth2client.TokenManager
	baseURL string
}

// New builds a client, or returns nil when the service credential is not
// configured. A nil client is NOT permissive: Membership on nil is an error.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	hc := &http.Client{Timeout: requestTimeout}
	return &Client{
		http:    hc,
		tokens:  oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, Scope),
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
	}
}

type membershipResponse struct {
	Member bool   `json:"member"`
	Role   string `json:"role"`
}

// Membership reports whether userID belongs to organizationID and with which
// ctech-account role. Satisfies space.MembershipSource.
func (c *Client) Membership(ctx context.Context, organizationID, userID string) (string, bool, error) {
	if c == nil {
		return "", false, fmt.Errorf("ctech-account membership client is not configured")
	}
	token, err := c.tokens.Get(ctx)
	if err != nil {
		return "", false, fmt.Errorf("minting a service token: %w", err)
	}
	return c.membershipWithToken(ctx, token, organizationID, userID)
}

// membershipWithToken is the request itself, split from the token so the HTTP
// behaviour is testable without a token endpoint.
func (c *Client) membershipWithToken(ctx context.Context, token, organizationID, userID string) (string, bool, error) {
	path := fmt.Sprintf("%s/v1.0/internal/organizations/%s/members/%s",
		c.baseURL, url.PathEscape(organizationID), url.PathEscape(userID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", false, fmt.Errorf("building the membership request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("calling ctech-account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Including 403 and 404: a 403 is this client's own credential being
		// wrong (an operational fault, not a statement about the user), and the
		// route answers "not a member" with 200, so a 404 means the route is
		// missing. Reading either as a refusal would hide a broken deployment
		// behind thousands of denied requests.
		return "", false, fmt.Errorf("ctech-account answered %d", resp.StatusCode)
	}
	var out membershipResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding the membership answer: %w", err)
	}
	if !out.Member {
		return "", false, nil
	}
	return out.Role, true, nil
}
