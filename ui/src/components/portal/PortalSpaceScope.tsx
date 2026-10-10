"use client"

import {Fragment, type ReactNode} from "react"

import {spaceHeader} from "@/lib/console/space"
import {usePortalSpace} from "@/lib/portal/space"

/**
 * Remounts the portal's screens whenever the selection changes — from the
 * switch, another tab, or the 404 fallback alike. With the query keys carrying
 * the space (portalKeys), a remount reads under the new space's keys, so the
 * previous space's bills are never on screen under the new name, not even for
 * one render.
 */
export function PortalSpaceScope({children}: {children: ReactNode}) {
  const space = usePortalSpace()
  return <Fragment key={spaceHeader(space)}>{children}</Fragment>
}
