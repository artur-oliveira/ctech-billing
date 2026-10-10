"use client"

import {useSyncExternalStore} from "react"

import {parseSpace, PERSONAL, type Space, spaceHeader} from "@/lib/console/space"

/**
 * Whose bills the portal is showing (ADR 0025, 2026-10-07 amendment): the
 * person's own, or an organization they own or administer. The same grammar as
 * the finance selection — "personal" or "org:{id}" — and the same rule: it is a
 * request the server re-authorizes on every call, so a stale or tampered value
 * grants nothing and answers 404.
 *
 * Stored apart from the finance space on purpose. Paying an organization's
 * invoice and keeping your own budget are two questions; answering one must
 * never move the other.
 */
const KEY = "ctech-billing-portal-space"
const EVENT = "ctech-portal-space"

let cached: {raw: string | null; space: Space} = {raw: null, space: PERSONAL}

export function getPortalSpace(): Space {
  if (typeof window === "undefined") return PERSONAL
  let raw: string | null = null
  try {
    raw = window.localStorage.getItem(KEY)
  } catch {
    // Site data blocked: the portal still works, it just forgets the choice.
  }
  // useSyncExternalStore needs a stable snapshot: same raw value, same object.
  if (raw !== cached.raw) cached = {raw, space: parseSpace(raw)}
  return cached.space
}

export function setPortalSpace(s: Space) {
  try {
    window.localStorage.setItem(KEY, spaceHeader(s))
  } catch {
    // Ignored for the reason above: the switch must still work.
  }
  window.dispatchEvent(new CustomEvent(EVENT))
}

/** Subscribes to changes, including the ones another tab makes. */
export function onPortalSpaceChange(fn: () => void): () => void {
  window.addEventListener(EVENT, fn)
  window.addEventListener("storage", fn)
  return () => {
    window.removeEventListener(EVENT, fn)
    window.removeEventListener("storage", fn)
  }
}

const serverSnapshot = (): Space => PERSONAL

/** The portal selection as React state. Personal before storage is read. */
export function usePortalSpace(): Space {
  return useSyncExternalStore(onPortalSpaceChange, getPortalSpace, serverSnapshot)
}
