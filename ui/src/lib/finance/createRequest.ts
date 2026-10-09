"use client"

import {useEffect, useRef} from "react"

/**
 * The phone's central "create" button lives in the shell; the drawer it opens
 * lives in the screen. This is the one wire between them.
 *
 * A request is held until the screen that owns that kind takes it, so it
 * survives a navigation: on Resumo the action goes to A pagar/receber and the
 * request is waiting when BillsView mounts. Held for one kind at a time, and
 * taken once, so a later visit never reopens a drawer nobody asked for.
 *
 * Deliberately not a URL parameter: a `?novo=1` left in the address bar
 * reopens the drawer on every reload and on every shared link.
 */
export type CreateKind = "bill" | "purchase" | "recurrence" | "transfer" | "account"

let pending: CreateKind | null = null
const listeners = new Set<() => void>()

export function requestCreate(kind: CreateKind): void {
  pending = kind
  for (const listener of [...listeners]) listener()
}

/** Takes a pending request for `kind`, if there is one. True means "open it". */
export function takePendingCreate(kind: CreateKind): boolean {
  if (pending !== kind) return false
  pending = null
  return true
}

/** Opens the screen's create drawer when the shell asks for `kind`. */
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
