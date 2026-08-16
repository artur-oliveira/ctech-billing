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

export const OAUTH_SCOPE = [...IDENTITY_SCOPES, ...PORTAL_SCOPES].join(" ")
