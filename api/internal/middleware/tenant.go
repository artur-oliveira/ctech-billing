package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ResolveTenant turns the token's OAuth client into the organization and mode
// the request acts for.
//
// This is the single place the tenant is decided, and it decides it from the
// **credential**, never from anything the caller sent. There is no
// `organization_id` parameter and no `livemode` flag on any route, deliberately:
// a parameter can be changed by a client, a credential cannot. Combined with the
// partition key (ADR 0003), that makes cross-tenant access not merely forbidden
// but unexpressible.
//
// The mode comes from the credential for the same reason. A client provisioned
// for test resolves to the test partition, so a test integration cannot reach
// live data even by accident — which is what makes test mode a real isolation
// boundary rather than a display flag (ADR 0003).
// Tenant is the organization and mode a request acts for, whatever decided it.
//
// It exists so a handler cannot tell an M2M call from a console call by what it
// reads: both resolvers put the same value here, and a handler that reached the
// tenant from somewhere else would have to say so in its own code.
type Tenant struct {
	OrganizationID string
	Livemode       bool
}

// GetTenant returns the resolved tenant. A zero value means no resolver ran,
// which is a routing mistake rather than a request the handler should serve.
func GetTenant(c fiber.Ctx) Tenant {
	t, _ := c.Locals(TenantKey).(Tenant)
	return t
}

func ResolveTenant(creds *repositories.CredentialRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		// AZP is the OAuth client id. For a client_credentials token Sub is the
		// client too, but AZP is the field that means it — falling back keeps a
		// token issued with only one of them working.
		clientID := cl.AZP
		if clientID == "" {
			clientID = cl.Sub
		}

		cred, err := creds.Resolve(c.Context(), clientID)
		if err != nil {
			switch {
			case errors.Is(err, repositories.ErrNotFound), errors.Is(err, billing.ErrCredentialInactive):
				// A valid token whose client billing does not know is a
				// configuration gap, not an authentication failure — but the
				// response must not confirm which of the two it is.
				return problem.Forbidden("credencial não habilitada para o billing").Send(c)
			default:
				return problem.Internal("erro ao resolver credencial").Send(c)
			}
		}
		c.Locals(CredentialKey, cred)
		c.Locals(TenantKey, Tenant{OrganizationID: cred.OrganizationID, Livemode: cred.Livemode})
		return c.Next()
	}
}
