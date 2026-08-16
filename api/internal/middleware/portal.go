package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// CustomerKey holds the portal session's customer record.
const CustomerKey = "customer"

// ResolvePortalIdentity turns a signed-in person into the customer record that
// is them, inside tenant zero (ADR 0012).
//
// The organization is configuration, not a request parameter and not a lookup:
// there is exactly one portal organization and it must never be resolvable from
// anything a caller sends. The mode is always live — test mode exists so an
// integration cannot touch real data, and a consumer does not integrate.
//
// A person who is not a customer of tenant zero gets 403, whether they own an
// organization, own nothing, or simply bought from somebody else. The portal is
// not a place to discover that an account exists.
func ResolvePortalIdentity(customers *repositories.CustomerRepository, organizationID string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if organizationID == "" {
			// No tenant zero configured. Not an error the caller caused, and not
			// something to explain: an unconfigured portal is a portal that does
			// not exist here.
			return problem.NotFound("recurso não encontrado").Send(c)
		}
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("esta rota exige sessão de usuário").Send(c)
		}

		customer, err := customers.GetByUser(c.Context(), organizationID, true, cl.Sub)
		if err != nil {
			if errors.Is(err, repositories.ErrNotFound) {
				return problem.Forbidden("nenhuma conta de cobrança para este usuário").Send(c)
			}
			return problem.Internal("erro ao resolver conta").Send(c)
		}
		if customer.Anonymized {
			// An erased customer keeps their invoices as documents (ADR 0009), but
			// the person asked to be forgotten. Signing back in must not undo that.
			return problem.Forbidden("conta encerrada").Send(c)
		}

		c.Locals(CustomerKey, customer)
		c.Locals(TenantKey, Tenant{OrganizationID: organizationID, Livemode: true})
		return c.Next()
	}
}

// GetCustomer returns the portal session's customer.
func GetCustomer(c fiber.Ctx) *billing.Customer {
	customer, _ := c.Locals(CustomerKey).(*billing.Customer)
	return customer
}
