"use client"

import type {ReactNode} from "react"

import {cn} from "@aoctech/ui"

export interface SegmentedOption<V extends string> {
  value: V
  /** What is drawn: a word, a short form ("6 m") or an icon. */
  label: ReactNode
  /**
   * The full name, when `label` is shortened or an icon. It must contain the
   * visible text (WCAG 2.5.3): "6 meses" for "6 m".
   */
  name?: string
}

interface SegmentedProps<V extends string> {
  /** Names the group for a screen reader; the options are its buttons. */
  label: string
  value: V
  onValueChange: (value: V) => void
  options: SegmentedOption<V>[]
  /** Stretch across the row, options sharing it equally (a phone's full width). */
  fill?: boolean
  className?: string
}

/**
 * A small set of mutually exclusive choices, all in view: a group of pressed
 * buttons. The one control behind every two-to-four-way switch in the console
 * (direction, projection view, period), so they size alike: 28px under a
 * mouse, a 44px target under `touch:`. Candidate for @aoctech/ui.
 */
export function Segmented<V extends string>({label, value, onValueChange, options, fill, className}: SegmentedProps<V>) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5", fill ? "w-full" : "w-max", className)}
    >
      {options.map(o => {
        const on = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            data-slot="segmented-item"
            aria-pressed={on}
            aria-label={o.name}
            onClick={() => onValueChange(o.value)}
            className={cn(
              "inline-flex h-7 min-w-7 items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-3 text-sm transition-colors duration-150 motion-reduce:transition-none",
              "touch:h-auto touch:min-h-11 touch:min-w-11",
              fill && "flex-1",
              on ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
