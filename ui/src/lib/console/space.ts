"use client"

/**
 * The finance space the console is looking at (ADR 0025): personal, or one of
 * the person's organizations. It is a REQUEST — the `X-Billing-Space` header — and
 * the server re-authorizes it on every call, so a stale or tampered value grants
 * nothing; the worst it can do is answer 404 and fall back to personal.
 *
 * Stored per browser, like the mode, and read through the same external-store
 * pattern (useSpace) so every component sees one value per render.
 */
export type Space = {kind: "personal"} | {kind: "organization"; organizationId: string}

export const PERSONAL: Space = {kind: "personal"}

const KEY = "ctech-billing-finance-space"
const EVENT = "ctech-finance-space"

// The server's own grammar for an organization id (space.validOrganizationID):
// canonical lower-case UUID. Anything else is read as personal, never sent.
const ORG_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

/** The `X-Billing-Space` header value: "personal" or "org:{id}". */
export function spaceHeader(s: Space): string {
  return s.kind === "personal" ? "personal" : `org:${s.organizationId}`
}

export function parseSpace(raw: string | null): Space {
  if (raw?.startsWith("org:")) {
    const id = raw.slice(4)
    if (ORG_ID.test(id)) return {kind: "organization", organizationId: id}
  }
  return PERSONAL
}

export function sameSpace(a: Space, b: Space): boolean {
  return spaceHeader(a) === spaceHeader(b)
}

let cached: {raw: string | null; space: Space} = {raw: null, space: PERSONAL}

export function getSpace(): Space {
  if (typeof window === "undefined") return PERSONAL
  let raw: string | null = null
  try {
    raw = window.localStorage.getItem(KEY)
  } catch {
    // Site data blocked: the console still works, it just forgets the choice.
  }
  // useSyncExternalStore needs a stable snapshot: same raw value, same object.
  if (raw !== cached.raw) cached = {raw, space: parseSpace(raw)}
  return cached.space
}

export function setSpace(s: Space) {
  try {
    window.localStorage.setItem(KEY, spaceHeader(s))
  } catch {
    // Ignored for the reason above: the switch must still work.
  }
  window.dispatchEvent(new CustomEvent(EVENT))
}

/** Subscribes to changes, including the ones another tab makes. */
export function onSpaceChange(fn: () => void): () => void {
  window.addEventListener(EVENT, fn)
  window.addEventListener("storage", fn)
  return () => {
    window.removeEventListener(EVENT, fn)
    window.removeEventListener("storage", fn)
  }
}
