"use client"

import {useCallback, useSyncExternalStore} from "react"

/** Under Tailwind's `sm`: the width at which the console lays out for a phone. */
export const NARROW = "(max-width: 39.999rem)"

/**
 * Whether a media query matches, kept in sync as the window changes. The server
 * render and any environment without `matchMedia` (jsdom) answer false, the
 * desktop layout; the client corrects it on hydration, before data has loaded.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback((notify: () => void) => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return () => undefined
    const list = window.matchMedia(query)
    list.addEventListener("change", notify)
    return () => list.removeEventListener("change", notify)
  }, [query])
  return useSyncExternalStore(
    subscribe,
    () => typeof window.matchMedia === "function" && window.matchMedia(query).matches,
    () => false,
  )
}
