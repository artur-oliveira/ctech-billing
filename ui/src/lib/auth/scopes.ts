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
 *
 * The finance scopes are asked for by the same login and hold nothing alone:
 * the API resolves the space (personal or an organization) and the verbs the
 * person has in it on every request (ADR 0025).
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
  "billing:finance:read",
  "billing:finance:write",
] as const

export const OAUTH_SCOPE = [...IDENTITY_SCOPES, ...PORTAL_SCOPES, ...CONSOLE_SCOPES].join(" ")

const IDENTITY = new Set<string>(IDENTITY_SCOPES)

/**
 * The scopes this app asks for that `accessToken` was not granted. ctech-account
 * clamps a refresh to what the ORIGINAL authorization granted, so a session
 * that signed in before a scope was added here keeps refreshing without it —
 * and every route behind that scope answers 403 — until it authorizes again.
 * Reads the payload unverified: it only decides whether to ask again, never
 * whether anything is allowed (the API checks the signed token).
 */
export function missingScopes(accessToken: string): string[] {
  try {
    const payload = accessToken.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")
    const scope: unknown = JSON.parse(atob(payload)).scope
    const granted = new Set(typeof scope === "string" ? scope.split(" ") : [])
    return OAUTH_SCOPE.split(" ").filter(s => !IDENTITY.has(s) && !granted.has(s))
  } catch {
    return []
  }
}

const UPGRADE_RETRY_MS = 10 * 60_000

/**
 * Whether to send the browser through /authorize again for missing scopes. At
 * most once per ten minutes: if the client itself lacks a scope, ctech-account
 * cannot grant it and re-asking on every load would be a redirect loop.
 */
export function shouldUpgradeScopes(missing: string[], lastAttempt: number | null, now: number): boolean {
  return missing.length > 0 && (lastAttempt === null || now - lastAttempt > UPGRADE_RETRY_MS)
}
