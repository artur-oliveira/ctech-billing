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
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// Fiber locals keys.
const (
	ClaimsKey       = "claims"
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
}

func NewVerifier(jwksURL, audience, issuer string, backend cache.Backend) *Verifier {
	return &Verifier{jwtverify.NewVerifier(jwksURL, audience, issuer, backend)}
}

// Middleware validates the Bearer token and stores the claims in locals.
func (v *Verifier) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		if !strings.HasPrefix(header, "Bearer ") {
			return problem.Unauthorized("token ausente").Send(c)
		}
		claims, err := v.VerifyClaims(c.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil || claims.Sub == "" {
			// One message for every failure mode. Distinguishing "expired" from
			// "wrong signature" from "unknown key" tells an attacker which of
			// their guesses was closer.
			return problem.Unauthorized("credenciais inválidas").Send(c)
		}
		c.Locals(ClaimsKey, claims)
		return c.Next()
	}
}

// GetClaims returns the authenticated caller's claims.
func GetClaims(c fiber.Ctx) *Claims {
	cl, _ := c.Locals(ClaimsKey).(*Claims)
	return cl
}

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
