"use client"

import {type KeyboardEvent, type MouseEvent, type PointerEvent, useCallback, useEffect, useId, useRef, useState, useSyncExternalStore} from "react"

/**
 * A row that slides left under a finger to reveal actions behind it (UX batch
 * 4): pointer events, a direction lock so a vertical drag stays the page's
 * scroll (the row sets `touch-action: pan-y`), a threshold, a snap back, and one
 * row open at a time across the page. Phones only (`max-width: 39.999rem`,
 * Tailwind's `sm`); on a laptop it never moves. Candidate for @aoctech/ui.
 */

const PHONE = "(max-width: 39.999rem)"
/** Movement before the gesture picks an axis, in px. */
const LOCK = 8
/** Share of the revealed width a release must pass to change state. */
const THRESHOLD = 0.35

// One open row per page: a tiny store every row subscribes to.
let openRow: string | null = null
const listeners = new Set<() => void>()
function setOpenRow(id: string | null) {
  if (openRow === id) return
  openRow = id
  for (const l of listeners) l()
}
const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => { listeners.delete(l) }
}

function isPhone(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(PHONE).matches
}

export interface SwipeReveal {
  open: boolean
  /** Put on the row's root as `data-swipe-row`: a press inside it does not close it. */
  rowId: string
  /** Current translation of the row's front, in px (≤ 0). */
  offset: number
  dragging: boolean
  close: () => void
  bind: {
    onPointerDown: (e: PointerEvent<HTMLElement>) => void
    onPointerMove: (e: PointerEvent<HTMLElement>) => void
    onPointerUp: (e: PointerEvent<HTMLElement>) => void
    onPointerCancel: (e: PointerEvent<HTMLElement>) => void
    onClickCapture: (e: MouseEvent<HTMLElement>) => void
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => void
  }
}

/** `width` is how far the row slides when open: the actions' total width. */
export function useSwipeReveal(width: number, enabled = true): SwipeReveal {
  const id = useId()
  const open = useSyncExternalStore(subscribe, () => openRow === id, () => false)
  const [drag, setDrag] = useState<number | null>(null)
  const g = useRef<{x: number; y: number; axis: "x" | "y" | null; base: number; pointer: number; moved: boolean} | null>(null)
  const swallowClick = useRef(false)
  const lastX = useRef<number | null>(null)

  const close = useCallback(() => { if (openRow === id) setOpenRow(null) }, [id])

  // A press anywhere outside the open row closes it (another row's own press
  // opens that one instead, through the store).
  useEffect(() => {
    if (!open) return
    const onDown = (e: Event) => {
      const t = e.target as Element | null
      if (!t?.closest?.(`[data-swipe-row="${CSS.escape(id)}"]`)) close()
    }
    document.addEventListener("pointerdown", onDown, true)
    return () => document.removeEventListener("pointerdown", onDown, true)
  }, [open, id, close])

  const end = (e: PointerEvent<HTMLElement>, cancelled: boolean) => {
    const s = g.current
    g.current = null
    if (!s || s.pointer !== e.pointerId) return
    if (s.axis !== "x") {
      setDrag(null)
      return
    }
    const final = lastX.current ?? s.base
    lastX.current = null
    const travelled = final - s.base
    let next = s.base !== 0
    if (!cancelled) {
      if (s.base === 0 && travelled < -width * THRESHOLD) next = true
      else if (s.base !== 0 && travelled > width * THRESHOLD) next = false
    }
    setDrag(null)
    setOpenRow(next ? id : openRow === id ? null : openRow)
  }

  return {
    open,
    rowId: id,
    offset: drag ?? (open ? -width : 0),
    dragging: drag !== null,
    close,
    bind: {
      onPointerDown: e => {
        if (!enabled || width <= 0 || !isPhone() || (e.pointerType === "mouse" && e.button !== 0)) return
        // Pressing a closed row while another is open closes that one.
        if (!open && openRow !== null) setOpenRow(null)
        g.current = {x: e.clientX, y: e.clientY, axis: null, base: open ? -width : 0, pointer: e.pointerId, moved: false}
      },
      onPointerMove: e => {
        const s = g.current
        if (!s || s.pointer !== e.pointerId) return
        const dx = e.clientX - s.x, dy = e.clientY - s.y
        if (s.axis === null) {
          if (Math.abs(dx) < LOCK && Math.abs(dy) < LOCK) return
          s.axis = Math.abs(dx) > Math.abs(dy) ? "x" : "y"
          if (s.axis === "x") e.currentTarget.setPointerCapture?.(e.pointerId)
        }
        if (s.axis !== "x") return
        s.moved = true
        // Rubber-band past either end, so the row never jumps.
        let x = s.base + dx
        if (x > 0) x = x / 4
        if (x < -width) x = -width + (x + width) / 4
        lastX.current = x
        setDrag(x)
      },
      onPointerUp: e => {
        const s = g.current
        if (s && s.axis === "x" && s.moved) swallowClick.current = true
        else if (s && s.axis === null && open) {
          // A tap on an open row closes it instead of acting on what it hit.
          swallowClick.current = true
          g.current = null
          setOpenRow(null)
          return
        }
        end(e, false)
      },
      onPointerCancel: e => end(e, true),
      onClickCapture: e => {
        if (!swallowClick.current) return
        swallowClick.current = false
        e.preventDefault()
        e.stopPropagation()
      },
      onKeyDown: e => {
        if (e.key === "Escape" && open) close()
      },
    },
  }
}

