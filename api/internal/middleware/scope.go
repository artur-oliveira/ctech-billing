package middleware

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// Billing scopes, issued by ctech-account per M2M client.
//
// Split by resource **and** by direction, not one blanket `billing:*`: a
// product that only needs to report usage should not hold a credential that can
// cancel subscriptions. That separation costs nothing to define now and cannot
// be retrofitted onto credentials already handed out.
const (
	ScopeSubscriptionsRead  = "billing:subscriptions:read"
	ScopeSubscriptionsWrite = "billing:subscriptions:write"
	ScopeInvoicesRead       = "billing:invoices:read"
	ScopeInvoicesWrite      = "billing:invoices:write"
	ScopeUsageWrite         = "billing:usage:write"
	ScopeCustomersRead      = "billing:customers:read"
	ScopeCustomersWrite     = "billing:customers:write"
	ScopeEntitlementsRead   = "billing:entitlements:read"
	// ScopeProductsRead and ScopeOrganizationRead are read by the console
	// (ADR 0011). They are in the same manifest as the M2M scopes, not a parallel
	// `billing:console:*` set: the resource is the same resource, and a second
	// naming scheme for the same data is a second thing to keep in agreement.
	ScopeProductsRead = "billing:products:read"
	// ScopeProductsWrite creates products and prices, and archives a price
	// (C8–C9). Separate from the invoice write scope on purpose: an operator who
	// may issue a credit note against one bill is not thereby somebody who may
	// change what every future customer pays.
	ScopeProductsWrite    = "billing:products:write"
	ScopeOrganizationRead = "billing:organization:read"
	// The portal's scopes are `me`, not a resource, and that is the point
	// (ADR 0012): `billing:my-invoices:read` reads **my** invoices, while
	// `billing:invoices:read` reads the organization's. A consumer token must not
	// be one scope away from a merchant's customer list, and naming them apart is
	// what makes that structural instead of careful.
	ScopeMyInvoicesRead      = "billing:my-invoices:read"
	ScopeMySubscriptionsRead = "billing:my-subscriptions:read"
	// The two portal writes a consumer can perform on their own account: pay a
	// bill, and stop a subscription. Split from the reads for the same reason
	// every other scope here is — a token that renders the screen should not be
	// the token that can cancel from it.
	ScopeMyInvoicesWrite      = "billing:my-invoices:write"
	ScopeMySubscriptionsWrite = "billing:my-subscriptions:write"
)

// AllScopes is the manifest ctech-account must know about. Keeping it in one
// exported slice means the list a client can be granted and the list this
// service enforces cannot drift apart silently.
var AllScopes = []string{
	ScopeSubscriptionsRead,
	ScopeSubscriptionsWrite,
	ScopeInvoicesRead,
	ScopeInvoicesWrite,
	ScopeUsageWrite,
	ScopeCustomersRead,
	ScopeCustomersWrite,
	ScopeEntitlementsRead,
	ScopeProductsRead,
	ScopeProductsWrite,
	ScopeOrganizationRead,
	ScopeMyInvoicesRead,
	ScopeMySubscriptionsRead,
	ScopeMyInvoicesWrite,
	ScopeMySubscriptionsWrite,
}

// RequireM2MScope gates a route on a client_credentials token carrying the
// scope.
//
// A non-empty SID means a user/session token, and those are rejected here even
// if they somehow carry the scope. Blurring the two would mean a user's browser
// token could act as an integration — the trust boundary is the point, not the
// scope string.
func RequireM2MScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID != "" {
			return problem.Forbidden("esta rota exige token de serviço (client_credentials)").Send(c)
		}
		if !cl.HasScope(scope) {
			return problem.Forbidden("escopo insuficiente: " + scope).Send(c)
		}
		return c.Next()
	}
}
