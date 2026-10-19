// Package accountclient asks ctech-account about an organization's companies
// (ADR 0022: ctech-account owns company identity; billing reads it).
//
// It mirrors api-commons' accountorgs client (token per scope, capped bodies,
// any non-200 is an error) and ctech-dfe's accountclient.Company. Both copies
// belong in api-commons; proposed there, not done here.
package accountclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/oauth2client"
	"gopkg.aoctech.app/api-commons/resilient"
)

// CompanyScope is the internal scope both company routes sit behind.
const CompanyScope = "internal:account:company-actor"

const (
	requestTimeout = 5 * time.Second
	maxBody        = 256 << 10
)

// ErrCompanyNotFound is a company that is not one of the organization's (or
// whose workspace is personal). It is the answer to a form field, not an outage.
var ErrCompanyNotFound = errors.New("company not found in the organization")

// ErrOrganizationNotFound is ctech-account's 404 on the list route (it does not
// know the organization) or a list for a workspace that is not an organization.
// An answer, not an outage: the caller maps it to "not found", never to 503.
var ErrOrganizationNotFound = errors.New("organization not found in ctech-account")

// CompanyList is the organization's companies, at most ListLimit by legal name;
// Truncated says ctech-account had more and cut the list.
type CompanyList struct {
	Companies []Company
	Truncated bool
}

// ListLimit is ctech-account's cap on the list route (handler.InternalCompanyListLimit).
const ListLimit = 200

// isOrganizationKind mirrors accountorgs.IsOrganizationKind: an absent kind is
// an organization (older ctech-account), "personal" is not.
func isOrganizationKind(kind string) bool { return kind == "" || kind == "organization" }

type Config struct {
	BaseURL, TokenURL, ClientID, ClientSecret string
	Cache                                     cache.Backend
}

// Company is what billing needs to put a company on a merchant's invoices.
type Company struct {
	ID        string `json:"company_id"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name"`
	TaxID     string `json:"tax_id"`
	TaxIDKind string `json:"tax_id_kind"`
}

type Client struct {
	http    *http.Client
	tokens  *oauth2client.TokenManager
	baseURL string
}

// New returns nil when the credential is incomplete. A nil client is not
// permissive: every call errors.
func New(cfg Config) *Client { return newWithScope(cfg, CompanyScope) }

func newWithScope(cfg Config, scope string) *Client {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	// A pooled transport and a circuit per host: while ctech-account is down a call
	// answers at once with resilient.ErrCircuitOpen instead of holding a goroutine
	// for the whole timeout.
	hc := resilient.Client(requestTimeout, resilient.Config{})
	return &Client{
		http:    hc,
		tokens:  oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, scope),
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
	}
}

func (c *Client) token(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ctech-account companies client is not configured")
	}
	tok, err := c.tokens.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("minting a service token: %w", err)
	}
	return tok, nil
}

// Companies lists the organization's companies (ctech-account route
// GET /v1.0/internal/organizations/{id}/companies): at most ListLimit by legal
// name, Truncated when there were more. ErrOrganizationNotFound when
// ctech-account does not know the organization or it is not an organization.
func (c *Client) Companies(ctx context.Context, organizationID string) (CompanyList, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return CompanyList{}, err
	}
	return c.companiesWithToken(ctx, tok, organizationID)
}

func (c *Client) companiesWithToken(ctx context.Context, token, organizationID string) (CompanyList, error) {
	var out struct {
		OrganizationKind string    `json:"organization_kind"`
		Companies        []Company `json:"companies"`
		Truncated        bool      `json:"truncated"`
	}
	status, err := c.getJSON(ctx, token, fmt.Sprintf("%s/v1.0/internal/organizations/%s/companies",
		c.baseURL, url.PathEscape(organizationID)), &out)
	switch {
	case err != nil:
		return CompanyList{}, err
	case status == http.StatusNotFound:
		return CompanyList{}, ErrOrganizationNotFound
	case status != http.StatusOK:
		return CompanyList{}, fmt.Errorf("ctech-account answered %d listing companies", status)
	case !isOrganizationKind(out.OrganizationKind):
		return CompanyList{}, ErrOrganizationNotFound
	}
	if out.Companies == nil {
		out.Companies = []Company{}
	}
	return CompanyList{Companies: out.Companies, Truncated: out.Truncated}, nil
}

// Company reads one company of the organization through the identity route.
func (c *Client) Company(ctx context.Context, organizationID, companyID string) (*Company, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	return c.companyWithToken(ctx, tok, organizationID, companyID)
}

func (c *Client) companyWithToken(ctx context.Context, token, organizationID, companyID string) (*Company, error) {
	var out struct {
		Company
		OrganizationKind string `json:"organization_kind"`
	}
	status, err := c.getJSON(ctx, token, fmt.Sprintf("%s/v1.0/internal/organizations/%s/companies/%s",
		c.baseURL, url.PathEscape(organizationID), url.PathEscape(companyID)), &out)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusNotFound:
		return nil, ErrCompanyNotFound
	case status != http.StatusOK:
		return nil, fmt.Errorf("ctech-account answered %d reading a company", status)
	case !isOrganizationKind(out.OrganizationKind):
		return nil, ErrCompanyNotFound
	}
	out.Company.ID = companyID
	return &out.Company, nil
}

// getJSON returns the status, decoding the body only on 200.
func (c *Client) getJSON(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calling ctech-account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return 0, fmt.Errorf("decoding the answer: %w", err)
	}
	return resp.StatusCode, nil
}
