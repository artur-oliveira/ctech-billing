"use client"

import {DatePicker} from "@aoctech/ui"

/** `YYYY-MM-DD` as local noon: never `new Date(iso)`, which reads UTC midnight. */
function toDate(iso: string): Date | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(iso)) return undefined
  const [y, m, d] = iso.split("-").map(Number)
  return new Date(y, m - 1, d, 12)
}

function toIso(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
}

interface DateFieldProps {
  id: string
  /** A civil date, `YYYY-MM-DD`, or "" for none. */
  value: string
  onValueChange: (iso: string) => void
  min?: string
  max?: string
  placeholder?: string
  disabled?: boolean
  className?: string
}

/**
 * The design system's DatePicker (Popover + Calendar) for the civil dates every
 * finance form speaks: it takes and gives `YYYY-MM-DD`, so no screen converts a
 * date itself and none can shift one by a timezone. Labelled by a Field's
 * `htmlFor` through `id`, like any input.
 */
export function DateField({id, value, onValueChange, min, max, placeholder, disabled, className}: DateFieldProps) {
  return (
    <DatePicker
      id={id}
      value={toDate(value)}
      onValueChange={d => onValueChange(d ? toIso(d) : "")}
      min={min ? toDate(min) : undefined}
      max={max ? toDate(max) : undefined}
      placeholder={placeholder}
      disabled={disabled}
      className={className}
    />
  )
}
