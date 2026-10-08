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

// ListScope is what listing a user's organizations requires — its own scope at
// ctech-account, because enumerating a person's workspaces is a wider grant than
// checking one membership. It is minted on its own token (listTokens) so a
// missing grant for it degrades the space switcher, never the membership checks
// that authorize every organization-space request.
const ListScope = "internal:account:user-organizations"

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
	http       *http.Client
	tokens     *oauth2client.TokenManager // Scope
	listTokens *oauth2client.TokenManager // ListScope
	baseURL    string
}

// New builds a client, or returns nil when the service credential is not
// configured. A nil client is NOT permissive: Membership on nil is an error.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	hc := &http.Client{Timeout: requestTimeout}
	return &Client{
		http:       hc,
		tokens:     oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, Scope),
		listTokens: oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, ListScope),
		baseURL:    strings.TrimSuffix(cfg.BaseURL, "/"),
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

// Organization is one workspace a person belongs to, as ctech-account lists it.
type Organization struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Organizations lists the organizations userID belongs to, with their role in
// each, for the console's space switcher. It is information only: every request
// is still authorized by Membership, so a stale or generous list grants nothing.
// Like Membership it fails closed — a nil client, a non-200 and an unreadable
// body are errors, never an empty list.
func (c *Client) Organizations(ctx context.Context, userID string) ([]Organization, error) {
	if c == nil {
		return nil, fmt.Errorf("ctech-account client is not configured")
	}
	token, err := c.listTokens.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("minting a service token: %w", err)
	}
	return c.organizationsWithToken(ctx, token, userID)
}

func (c *Client) organizationsWithToken(ctx context.Context, token, userID string) ([]Organization, error) {
	path := fmt.Sprintf("%s/v1.0/internal/users/%s/organizations", c.baseURL, url.PathEscape(userID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("building the organizations request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling ctech-account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ctech-account answered %d", resp.StatusCode)
	}
	var out struct {
		Organizations []Organization `json:"organizations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding the organizations answer: %w", err)
	}
	return out.Organizations, nil
}
