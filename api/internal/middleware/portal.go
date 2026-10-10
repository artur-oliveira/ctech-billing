package middleware

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// CustomerKey holds the portal session's customer record.
const CustomerKey = "customer"

// PortalCustomers is what the portal reads to find "the signed-in customer".
type PortalCustomers interface {
	GetByUser(ctx context.Context, organizationID string, livemode bool, userID string) (*billing.Customer, error)
	GetByOrganization(ctx context.Context, organizationID string, livemode bool, accountOrganizationID string) (*billing.Customer, error)
}

// ResolvePortalIdentity turns a signed-in person into the customer record that
// is them, inside tenant zero (ADR 0012) — or, with X-Billing-Space: org:{id},
// into an organization they own or administer (ADR 0025, 2026-10-07
// amendment). The selector goes through the console's resolver first: no table
// is read before membership, and every refusal (not a member, member or viewer,
// a personal workspace, no such organization, a malformed id) is the same 404.
//
// The organization is configuration, not a request parameter and not a lookup:
// there is exactly one portal organization and it must never be resolvable from
// anything a caller sends. The mode is always live — test mode exists so an
// integration cannot touch real data, and a consumer does not integrate.
//
// A person who is not a customer of tenant zero gets 403, whether they own an
// organization, own nothing, or simply bought from somebody else. The portal is
// not a place to discover that an account exists.
func ResolvePortalIdentity(customers PortalCustomers, organizationID string, spaces *space.Resolver) fiber.Handler {
	return func(c fiber.Ctx) error {
		if organizationID == "" {
			// No tenant zero configured. Not an error the caller caused, and not
			// something to explain: an unconfigured portal is a portal that does
			// not exist here.
			return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
		}
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
		}

		lookup := func() (*billing.Customer, error) {
			return customers.GetByUser(c.Context(), organizationID, true, cl.Sub)
		}
		if raw := c.Get(SpaceHeader); raw != "" && raw != "personal" {
			sel, err := space.ParseSelector(raw)
			if err != nil {
				if errors.Is(err, space.ErrSpaceNotFound) {
					return spaceNotFound().Send(c)
				}
				return problem.BadRequest("informe " + SpaceHeader + ": personal ou org:{id}").Send(c)
			}
			if spaces == nil {
				return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable, "Space unavailable", "could not verify access right now").Send(c)
			}
			sp, err := spaces.Resolve(c.Context(), cl.Sub, sel, true)
			switch {
			case errors.Is(err, space.ErrSpaceNotFound):
				return spaceNotFound().Send(c)
			case errors.Is(err, space.ErrInvalidSubject):
				return problem.Unauthorized("invalid credentials").WithCode("invalid_credentials").Send(c)
			case err != nil:
				return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable, "Space unavailable", "could not verify access right now").Send(c)
			}
			// owner and admin are the organization roles that hold Configure
			// (space.VerbsFor); a personal workspace has no CTech subscription.
			if sp.Kind() != space.KindOrganization || !sp.Can(space.Configure) {
				return spaceNotFound().Send(c)
			}
			lookup = func() (*billing.Customer, error) {
				return customers.GetByOrganization(c.Context(), organizationID, true, sel.OrganizationID)
			}
		}

		customer, err := lookup()
		if err != nil {
			if errors.Is(err, repositories.ErrNotFound) {
				// Typed, and the only 403 here that is: this is somebody who signed
				// in and simply has not bought anything yet, which is a beginning
				// rather than a failure. The portal renders it as an empty state, and
				// it can only tell it apart from "conta encerrada" by the type.
				//
				// Still a 403, and still the same refusal for all three cases the
				// doc comment lists — owning nothing, owning an organization, and
				// buying from somebody else are indistinguishable from out here.
				return problem.New(fiber.StatusForbidden, problem.TypeNoBillingAccount, "Forbidden",
					"no billing account for this user").Send(c)
			}
			return problem.Internal("could not resolve account").WithCode("account_resolve_failed").Send(c)
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
