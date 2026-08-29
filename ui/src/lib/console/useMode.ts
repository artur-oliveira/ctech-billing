"use client"

import {useSyncExternalStore} from "react"

import {getMode, onModeChange, type Mode} from "@/lib/console/mode"

/**
 * The current mode, as React state.
 *
 * `useSyncExternalStore` and not an effect: the mode lives in localStorage and
 * changes from another tab, which is exactly the external store this hook is
 * for — and it is what keeps the value consistent across every component in one
 * render, instead of each subscriber catching up on its own effect.
 *
 * The server snapshot is **"live"** deliberately. This app is a static export,
 * so the first render happens at build time where `localStorage` does not
 * exist; starting on the safe value means the worst case is a test-mode
 * operator seeing production for one frame, never the reverse — somebody
 * acting on live data believing it is a sandbox.
 */
export function useMode(): Mode {
  return useSyncExternalStore(onModeChange, getMode, serverSnapshot)
}

const serverSnapshot = (): Mode => "live"
