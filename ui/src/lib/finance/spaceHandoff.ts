/**
 * Sending somebody to ctech-account to create a space, or to manage who is in
 * one (ADR 0027). ctech-account is the only writer of tenancy: billing redirects
 * and never creates or invites anything itself.
 *
 * The create leg comes back to SPACE_CREATED_PATH with `organization_id` and our
 * `state`. Neither is authority: the state only tells a real return from a URL
 * somebody typed or replayed, and the id is selected only if the server's own
 * list contains it — and even then every request re-resolves it (ADR 0025).
 */

const ACCOUNTS = process.env.NEXT_PUBLIC_CTECH_CLIENT_URL || "https://accounts.aoctech.app"
const CLIENT_ID = process.env.NEXT_PUBLIC_CTECH_CLIENT_ID || "billing"

/** Where ctech-account sends the browser back after "Novo espaço". */
export const SPACE_CREATED_PATH = "/finance/spaces/created"

/** The state we sent, kept in sessionStorage for the return leg to check. */
export const SPACE_HANDOFF_STATE_KEY = "billing:space-handoff-state"

// The server's grammar for a workspace id (space.validOrganizationID).
const WORKSPACE_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

/** 128 random bits, hex. */
export function newHandoffState(): string {
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  return Array.from(b, x => x.toString(16).padStart(2, "0")).join("")
}

export function createSpaceURL(origin: string, state: string): string {
  const u = new URL("/account/spaces/new", ACCOUNTS)
  u.searchParams.set("client_id", CLIENT_ID)
  u.searchParams.set("return_to", origin + SPACE_CREATED_PATH)
  u.searchParams.set("state", state)
  return u.toString()
}

/**
 * Keeps a fresh state for the return leg and leaves. `go` is
 * window.location.replace by default: Back from ctech-account must not land on
 * a screen that starts a second handoff.
 */
export function startCreateSpace(
  go: (url: string) => void = url => window.location.replace(url),
  origin: string = window.location.origin,
): void {
  const state = newHandoffState()
  try {
    window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, state)
  } catch {
    // Storage refused: the return is discarded (spec § 5.2), and the new space
    // simply shows up in the list, unselected.
  }
  go(createSpaceURL(origin, state))
}

/**
 * ctech-account's people page for one space — `?id=`, since its UI is a static
 * export with no dynamic segments. Its "Voltar" link returns to Finanças. No
 * state: the return carries nothing billing acts on.
 */
export function managePeopleURL(workspaceId: string, origin: string): string {
  const u = new URL("/account/spaces/people", ACCOUNTS)
  u.searchParams.set("id", workspaceId)
  u.searchParams.set("client_id", CLIENT_ID)
  u.searchParams.set("return_to", origin + "/finance")
  return u.toString()
}

export type CreatedReturn = {kind: "created"; organizationId: string} | {kind: "cancelled"} | {kind: "discard"}

/**
 * Reads the create leg's return. The stored state is single-use: it is removed
 * whatever the outcome, so a replayed return URL is discarded. A missing or
 * different state discards silently; so does an id that is not a canonical
 * workspace id.
 */
export function readCreatedReturn(params: URLSearchParams): CreatedReturn {
  let expected: string | null = null
  try {
    expected = window.sessionStorage.getItem(SPACE_HANDOFF_STATE_KEY)
    window.sessionStorage.removeItem(SPACE_HANDOFF_STATE_KEY)
  } catch {
    // No storage, no expected state: discarded below.
  }
  const state = params.get("state")
  if (!expected || !state || state !== expected) return {kind: "discard"}
  if (params.get("cancelled") === "1") return {kind: "cancelled"}
  const id = params.get("organization_id") ?? ""
  return WORKSPACE_ID.test(id) ? {kind: "created", organizationId: id} : {kind: "discard"}
}
