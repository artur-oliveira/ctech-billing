"use client"

import {type RefObject, useEffect, useState} from "react"

/**
 * The element's content width in CSS pixels, kept current as it resizes; 0
 * until measured (and always, where ResizeObserver does not exist). A chart
 * drawn in a fixed viewBox and scaled to fit shrinks its text with it: 11px
 * labels in a 640-wide viewBox on a 288px phone are 5px. Drawing at the real
 * width keeps the type at its size.
 */
export function useElementWidth(ref: RefObject<HTMLElement | null>): number {
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(([entry]) => setWidth(Math.round(entry.contentRect.width)))
    observer.observe(el)
    return () => observer.disconnect()
  }, [ref])
  return width
}
