"use client"

import {Drawer as BaseDrawer, type DrawerProps, Modal as BaseModal, type ModalProps} from "@aoctech/ui"

/**
 * The console's Drawer and Modal: @aoctech/ui's, plus the marker that lets the
 * touch rule in globals.css reach them. A dialog is portaled out of the
 * console's `[data-density=compact]` root, so it opts in by holding
 * `[data-touch-compact]`; a dialog without it (the bottom bar's Mais sheet,
 * the portal's modals) keeps its own sizes. The marker is an empty, hidden
 * element: it adds nothing to the layout.
 */
const marker = <span hidden data-touch-compact=""/>

export function Drawer({children, ...props}: DrawerProps) {
  return <BaseDrawer {...props}>{marker}{children}</BaseDrawer>
}

/** A modal with no body (a bare confirmation) keeps no marker: the marker would
 *  give it an empty padded body, so its two buttons keep their own size. */
export function Modal({children, ...props}: ModalProps) {
  return <BaseModal {...props}>{children == null ? undefined : <>{marker}{children}</>}</BaseModal>
}
