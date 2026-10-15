package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/jwtverify"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Principal kinds: which surface a connection came from.
const (
	KindConsole  = "console"
	KindPortal   = "portal"
	KindCheckout = "checkout"
)

var (
	// ErrUnauthorized is every reason a connection is refused: a bad token, a
	// service token, a space that is not the caller's, a forged link. One error,
	// so one answer (Bye "unauthorized"); the wire never says which.
	ErrUnauthorized = errors.New("realtime: unauthorized")
	// ErrUnavailable is something upstream (ctech-account, a table) being down.
	// The connection fails closed, and the client retries.
	ErrUnavailable = errors.New("realtime: authorization unavailable")
)

// Principal is who a connection is and what it may hear. Scopes are derived by
// the server from a verified credential; nothing here is read from the client.
type Principal struct {
	Kind      string
	Sub       string
	Scopes    []string
	ExpiresAt time.Time

	// What Recheck needs to ask the same question again.
	selector space.Selector
	live     bool
}

// Authorizer turns a verified credential into a Principal, with the same
// resolvers the REST routes use.
type Authorizer interface {
	Authorize(ctx context.Context, a *wire.Auth) (*Principal, error)
	// Recheck answers whether the principal still holds its scopes (membership).
	Recheck(ctx context.Context, p *Principal) (bool, error)
}

// Select names the authorizer a first frame is for, from what the frame
// carries. A pay link is the narrowest credential, so it wins; a token with a
// space is a console (merchant or Finanças); a bare token is the portal.
func Select(a *wire.Auth) string {
	switch {
	case a.GetPayLink() != "":
		return KindCheckout
	case a.GetToken() != "" && a.GetSpace() != "":
		return KindConsole
	case a.GetToken() != "":
		return KindPortal
	}
	return ""
}

// TokenVerifier verifies a ctech-account access token (middleware.Verifier).
type TokenVerifier interface {
	VerifyClaims(ctx context.Context, token string) (*jwtverify.Claims, error)
}

// tokenFallbackLife is how long a verified token with no readable expiry is
// trusted: short, so the client has to Reauth rather than hold it indefinitely.
const tokenFallbackLife = 5 * time.Minute

// tokenExpiry reads exp from the payload of a token that has ALREADY been
// verified. It is not a verification: it exists because jwtverify.Claims does
// not carry the expiry (proposed for api-commons; this is the stopgap).
func tokenExpiry(token string, now time.Time) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var p struct {
				Exp int64 `json:"exp"`
			}
			if json.Unmarshal(raw, &p) == nil && p.Exp > 0 {
				return time.Unix(p.Exp, 0)
			}
		}
	}
	return now.Add(tokenFallbackLife)
}

func clockOf(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}

// session verifies a token and requires a person's session: no service token
// (no sid) ever opens a realtime connection.
func session(ctx context.Context, v TokenVerifier, token string) (*jwtverify.Claims, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	claims, err := v.VerifyClaims(ctx, token)
	if err != nil || claims == nil || claims.Sub == "" || claims.SID == "" {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

// TenantLookup is the one read the merchant side makes before it knows the
// tenant (repositories.OrganizationRepository).
type TenantLookup interface {
	GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)
}

// SpaceResolver is space.Resolver.
type SpaceResolver interface {
	Resolve(ctx context.Context, sub string, sel space.Selector, livemode bool) (space.ResolvedSpace, error)
}

// ConsoleAuthorizer authorizes the merchant console and Finanças. The space
// comes from the same resolver as X-Billing-Space (membership is checked before
// anything else), and the mode is a selector exactly like X-Billing-Mode: both
// partitions of a tenant belong to the same organization, so it cannot widen
// access.
type ConsoleAuthorizer struct {
	Verifier TokenVerifier
	Spaces   SpaceResolver
	Tenants  TenantLookup
	Now      func() time.Time
}

func modeOf(a *wire.Auth) (live bool, ok bool) {
	switch a.GetMode() {
	case "", "live":
		return true, true
	case "test":
		return false, true
	}
	return false, false
}

func (c *ConsoleAuthorizer) Authorize(ctx context.Context, a *wire.Auth) (*Principal, error) {
	claims, err := session(ctx, c.Verifier, a.GetToken())
	if err != nil {
		return nil, err
	}
	live, ok := modeOf(a)
	if !ok || a.GetSpace() == "" {
		return nil, ErrUnauthorized
	}
	sel, err := space.ParseSelector(a.GetSpace())
	if err != nil {
		return nil, ErrUnauthorized
	}
	scopes, err := c.scopes(ctx, claims.Sub, sel, live)
	if err != nil {
		return nil, err
	}
	return &Principal{
		Kind: KindConsole, Sub: claims.Sub, Scopes: scopes,
		ExpiresAt: tokenExpiry(a.GetToken(), clockOf(c.Now)), selector: sel, live: live,
	}, nil
}

// scopes is what the caller may hear in this space and mode: the space's own
// scope when they may read it, and the tenant's when the space is an
// organization they administer and that organization is a merchant.
func (c *ConsoleAuthorizer) scopes(ctx context.Context, sub string, sel space.Selector, live bool) ([]string, error) {
	sp, err := c.Spaces.Resolve(ctx, sub, sel, live)
	switch {
	case err == nil:
	case errors.Is(err, space.ErrSpaceNotFound), errors.Is(err, space.ErrInvalidSubject), errors.Is(err, space.ErrBadSelector):
		return nil, ErrUnauthorized
	default:
		return nil, ErrUnavailable
	}
	var scopes []string
	if sp.Can(space.Read) {
		scopes = append(scopes, SpaceScope(sp.PK()))
	}
	if !sp.Personal() && sp.Kind() == space.KindOrganization && space.IsMerchantRole(sp.Kind(), sp.Role()) {
		org, err := c.Tenants.GetByAccountOrganization(ctx, sp.OrganizationID(), live)
		switch {
		case err == nil:
			scopes = append(scopes, TenantScope(org.ID, live))
		case errors.Is(err, repositories.ErrNotFound):
			// Not a merchant in this mode: no tenant scope, and that is not an error.
		default:
			return nil, ErrUnavailable
		}
	}
	if len(scopes) == 0 {
		return nil, ErrUnauthorized
	}
	return scopes, nil
}

func (c *ConsoleAuthorizer) Recheck(ctx context.Context, p *Principal) (bool, error) {
	now, err := c.scopes(ctx, p.Sub, p.selector, p.live)
	switch {
	case errors.Is(err, ErrUnauthorized):
		return false, nil
	case err != nil:
		return false, err
	}
	// Still holding every scope the connection was registered under.
	for _, s := range p.Scopes {
		if !slices.Contains(now, s) {
			return false, nil
		}
	}
	return true, nil
}

// PortalAuthorizer authorizes the portal: a signed-in person hears their own
// user scope. Nothing in the first frame can name another person's.
type PortalAuthorizer struct {
	Verifier TokenVerifier
	Now      func() time.Time
}

func (p *PortalAuthorizer) Authorize(ctx context.Context, a *wire.Auth) (*Principal, error) {
	claims, err := session(ctx, p.Verifier, a.GetToken())
	if err != nil {
		return nil, err
	}
	return &Principal{
		Kind: KindPortal, Sub: claims.Sub, Scopes: []string{UserScope(claims.Sub)},
		ExpiresAt: tokenExpiry(a.GetToken(), clockOf(p.Now)),
	}, nil
}

// Recheck is true: the scope is the token's own subject, which cannot change
// while the token is valid.
func (p *PortalAuthorizer) Recheck(context.Context, *Principal) (bool, error) { return true, nil }

// PayLinkParser is services.PayLink.
type PayLinkParser interface {
	Parse(token string) (organizationID string, livemode bool, invoiceID string, err error)
}

// CheckoutAuthorizer authorizes the checkout page of one invoice by its signed
// payment link, and nothing else: the link names exactly one invoice.
type CheckoutAuthorizer struct {
	Links PayLinkParser
	// Lifetime is how long one connection lasts before the page reconnects; the
	// SSE stream it replaces had the same cap.
	Lifetime time.Duration
	Now      func() time.Time
}

func (c *CheckoutAuthorizer) Authorize(_ context.Context, a *wire.Auth) (*Principal, error) {
	if a.GetPayLink() == "" {
		return nil, ErrUnauthorized
	}
	org, live, invoiceID, err := c.Links.Parse(a.GetPayLink())
	if err != nil {
		return nil, ErrUnauthorized
	}
	return &Principal{
		Kind: KindCheckout, Scopes: []string{InvoiceScope(org, live, invoiceID)},
		ExpiresAt: clockOf(c.Now).Add(c.Lifetime),
	}, nil
}

func (c *CheckoutAuthorizer) Recheck(context.Context, *Principal) (bool, error) { return true, nil }
