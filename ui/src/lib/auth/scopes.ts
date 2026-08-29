/**
 * What the portal asks ctech-account for, and nothing more.
 *
 * These are the four `me` scopes and only the four. `billing:invoices:read`
 * reads the *organization's* invoices and `billing:my-invoices:read` reads
 * mine; a consumer's browser token must not be one scope away from a merchant's
 * customer list, which is why the two families are named apart rather than
 * distinguished by care (ADR 0012, api/internal/middleware/scope.go:30-42).
 *
 * Keep this list in exact sync with the public active `billing:me:*` entries in
 * api/internal/oauthresource/scope-manifest.json. ctech-account clamps the
 * authorization request to what the client is granted, so a scope named here
 * and missing there fails the flow rather than silently downgrading it.
 */
const IDENTITY_SCOPES = ["openid", "profile"] as const

const PORTAL_SCOPES = [
  "billing:my-invoices:read",
  "billing:my-invoices:write",
  "billing:my-subscriptions:read",
  "billing:my-subscriptions:write",
] as const

/**
 * What the console needs, requested by the same login.
 *
 * One authorization for both shells, because they are one account and one app
 * (PRODUCT.md): asking a person to sign in again to open "minha cobrança" would
 * be asking them which of the two they are, which is exactly what this product
 * does not do. Holding these scopes is not permission to use the console —
 * every console route resolves an organization from the signed-in owner and
 * answers 403 without one, so a customer who has never been provisioned one
 * carries scopes that open nothing.
 *
 * **The `billing` OAuth client at ctech-account must be granted all of them.**
 * ctech-account clamps the request to what the client holds and fails the flow
 * rather than downgrading it, so a scope named here and missing there breaks
 * sign-in for everybody — the portal included.
 */
const CONSOLE_SCOPES = [
  "billing:organization:read",
  "billing:invoices:read",
  "billing:invoices:write",
  "billing:subscriptions:read",
  "billing:subscriptions:write",
  "billing:customers:read",
  "billing:customers:write",
  "billing:products:read",
  "billing:products:write",
] as const

export const OAUTH_SCOPE = [...IDENTITY_SCOPES, ...PORTAL_SCOPES, ...CONSOLE_SCOPES].join(" ")
