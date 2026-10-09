"use client"

import {useEffect, useRef} from "react"

/**
 * The phone's central "create" button lives in the shell; the drawer it opens
 * lives in the screen. This is the one wire between them.
 *
 * A request is held until the screen that owns that kind takes it, so it
 * survives a navigation: on Resumo the action goes to A pagar/receber and the
 * request is waiting when BillsView mounts. Held for one kind at a time, taken
 * once, and only briefly: a request nobody took within a few seconds is
 * dropped, and the shell drops it as soon as the person lands anywhere but
 * the screen it was meant for. Either way a later visit never opens a drawer
 * nobody is asking for now.
 *
 * Deliberately not a URL parameter: a `?novo=1` left in the address bar
 * reopens the drawer on every reload and on every shared link.
 */
export type CreateKind = "bill" | "purchase" | "recurrence" | "transfer" | "account"

/** Long enough for a client-side navigation, short enough to never be "later". */
const TTL_MS = 3_000

let pending: {kind: CreateKind; at: number; target: string | null} | null = null
const listeners = new Set<() => void>()

/** Asks for `kind`; `target` is the section path the screen that answers lives at, when it is another one. */
export function requestCreate(kind: CreateKind, target: string | null = null): void {
  pending = {kind, at: Date.now(), target}
  for (const listener of [...listeners]) listener()
}

/** Takes a pending request for `kind`, if there is a fresh one. True means "open it". */
export function takePendingCreate(kind: CreateKind): boolean {
  if (!pending) return false
  if (Date.now() - pending.at > TTL_MS) {
    pending = null
    return false
  }
  if (pending.kind !== kind) return false
  pending = null
  return true
}

/** Called by the shell on every navigation: a request bound for elsewhere is abandoned. */
export function dropCreateUnlessAt(section: string): void {
  if (pending?.target && pending.target !== section) pending = null
}

export function clearPendingCreate(): void {
  pending = null
}

/**
 * Opens the screen's create drawer when the shell asks for `kind`. The screen
 * still decides whether its role may: a request is a wish, not a permission.
 */
export function useCreateRequest(kind: CreateKind, open: () => void): void {
  const ref = useRef(open)
  useEffect(() => {
    ref.current = open
  })
  useEffect(() => {
    const check = () => {
      if (takePendingCreate(kind)) ref.current()
    }
    listeners.add(check)
    check()
    return () => {
      listeners.delete(check)
    }
  }, [kind])
}
