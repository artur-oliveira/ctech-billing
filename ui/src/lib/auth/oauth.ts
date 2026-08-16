import {OAuthClient, decodeIdToken as sdkDecodeIdToken} from "@aoctech/auth-client"
import type {UnverifiedIdTokenClaims} from "@aoctech/auth-client"

import {OAUTH_SCOPE} from "./scopes"

/**
 * The OAuth client, configured once.
 *
 * `@aoctech/auth-client` and not a hand-rolled flow: accounts, dfe and wallet
 * each carried their own copy and they drifted — two checked the `ctech_auth`
 * hint cookie before firing a silent refresh and one did not, so every cold
 * visit spent a guaranteed failed `POST /token` against ctech-account's shared
 * brute-force limit. This package is that bug fixed once.
 *
 * Tokens are not stored here. The refresh token lives in ctech-account's
 * HttpOnly `ctech_rt` cookie and JS never sees it; the access token is held in
 * memory by the axios client and dies with the tab. An XSS on this page cannot
 * walk away with a persistent session.
 */
const client = new OAuthClient({
  baseUrl: process.env.NEXT_PUBLIC_CTECH_URL!,
  clientId: process.env.NEXT_PUBLIC_CTECH_CLIENT_ID!,
  // Read at call time rather than at module scope: this module is imported
  // during the static export's prerender, where `window` does not exist.
  redirectUri: typeof window !== "undefined" ? `${window.location.origin}/callback` : "",
  scope: OAUTH_SCOPE,
})

export type {UnverifiedIdTokenClaims}

/** Display-only claims. The id_token's audience is this client, so reading the
 *  name from it avoids ctech-account's /userinfo audience block on the billing
 *  access token. No signature check — never authorize on this. */
export const decodeIdToken = sdkDecodeIdToken

export function startOAuthFlow(returnTo = "/dashboard"): Promise<void> {
  return client.startOAuthFlow(returnTo)
}

export async function exchangeCode(code: string, state: string) {
  const result = await client.exchangeCode(code, state)
  return {
    accessToken: result.accessToken,
    idToken: result.idToken ?? null,
    returnTo: result.returnTo,
  }
}

/** Guarded and single-flight: safe to call from app boot and a 401 retry at the
 *  same instant, and it skips the network entirely without the hint cookie. */
export async function doRefresh(): Promise<string | null> {
  const result = await client.refresh()
  return result ? result.accessToken : null
}

/**
 * Ends the SSO session at ctech-account, not just this app's local state.
 * Without it `ctech_session` survives and /authorize silently re-authenticates
 * on the next login — the reader clicks "sair", clicks "entrar", and is back in
 * without ever being asked who they are.
 */
export async function logout(): Promise<void> {
  await client.revoke()
  client.endSessionRedirect("/")
}
