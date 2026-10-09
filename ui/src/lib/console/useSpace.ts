"use client"

import {useSyncExternalStore} from "react"

import {getSpace, onSpaceChange, PERSONAL, type Space} from "@/lib/console/space"

/**
 * The selected finance space, as React state — the same external-store pattern
 * as useMode. The server snapshot is PERSONAL: the static export renders first
 * without storage, and personal is the space everyone is entitled to.
 */
export function useSpace(): Space {
  return useSyncExternalStore(onSpaceChange, getSpace, serverSnapshot)
}

const serverSnapshot = (): Space => PERSONAL
