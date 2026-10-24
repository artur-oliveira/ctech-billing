// Package middleware provides the Fiber layer every billing route shares:
// JWT verification against ctech-account, scope gating, tenant resolution, and
// idempotency.
//
// The ordering is not decorative. Auth runs before tenant resolution because
// the tenant comes from the credential in the token, never from the request;
// scope gating runs before the handler because a 403 must not depend on what a
// handler happens to check.
package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/authguard"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// Fiber locals keys.
const (
	ClaimsKey       = authguard.LocalsClaims
	CredentialKey   = "credential"
	TenantKey       = "tenant"
	OrganizationKey = "organization"
	RequestIDKey    = "request_id"
)

// Claims are ctech-account's access-token fields. An empty SID marks an M2M
// client_credentials token.
type Claims = jwtverify.Claims

// Verifier validates RS256 access tokens issued by ctech-account against its
// JWKS. The fetching and parsing live in the shared api-commons/jwtverify;
// this only adds the Fiber wiring and the RFC 7807 responses.
type Verifier struct {
	*jwtverify.Verifier
	// lock is RefuseLocked. Nil when no erasure state is wired (unit tests that
	// build a bare verifier).
	lock    fiber.Handler
	blocked func(ctx context.Context, sub string) (bool, error)
}

func NewVerifier(jwksURL, audience, issuer string, backend cache.Backend) *Verifier {
	return &Verifier{Verifier: jwtverify.NewVerifier(jwksURL, audience, issuer, backend)}
}

// WithErasureLock refuses session tokens of a locked or erased subject on every
// route this verifier guards (RefuseLocked).
func (v *Verifier) WithErasureLock(blocked func(ctx context.Context, sub string) (bool, error)) *Verifier {
	v.lock = RefuseLocked(blocked)
	v.blocked = blocked
	return v
}

// ErrAccountLocked is VerifyClaims refusing a person whose account is being deleted.
var ErrAccountLocked = errors.New("account locked")

// VerifyClaims is the embedded verification plus the erasure lock, for callers
// that do not go through Middleware (the realtime gateway opens its sockets from
// a token it verifies itself). Without it a locked person could still open a
// websocket with an unexpired token.
func (v *Verifier) VerifyClaims(ctx context.Context, token string) (*jwtverify.Claims, error) {
	claims, err := v.Verifier.VerifyClaims(ctx, token)
	if err != nil || v.blocked == nil || claims == nil || claims.SID == "" {
		return claims, err
	}
	locked, err := v.blocked(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrAccountLocked
	}
	return claims, nil
}

// Middleware validates the Bearer token and stores the claims in locals.
func (v *Verifier) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		if !strings.HasPrefix(header, "Bearer ") {
			return problem.Unauthorized("token ausente").Send(c)
		}
		claims, err := v.Verifier.VerifyClaims(c.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil || claims.Sub == "" {
			// One message for every failure mode. Distinguishing "expired" from
			// "wrong signature" from "unknown key" tells an attacker which of
			// their guesses was closer.
			return problem.Unauthorized("invalid credentials").WithCode("invalid_credentials").Send(c)
		}
		authguard.SetClaims(c, claims)
		if v.lock != nil {
			return v.lock(c)
		}
		return c.Next()
	}
}

// GetClaims returns the authenticated caller's claims.
func GetClaims(c fiber.Ctx) *Claims { return authguard.GetClaims(c) }

// GetCredential returns the resolved tenant credential.
func GetCredential(c fiber.Ctx) *billing.APICredential {
	cred, _ := c.Locals(CredentialKey).(*billing.APICredential)
	return cred
}

// GetRequestID returns the correlation id for this request.
func GetRequestID(c fiber.Ctx) string {
	rid, _ := c.Locals(RequestIDKey).(string)
	return rid
}
