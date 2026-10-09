"use client"

import {Button, DatePicker} from "@aoctech/ui"
import {X} from "lucide-react"
import {useTranslation} from "react-i18next"

import {normalizeLocale} from "@/lib/locale"

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
  /** Marks the trigger as failing validation (the message itself is the Field's). */
  invalid?: boolean
  /**
   * Makes the date optional: a "Limpar" button beside it, while it has a value,
   * gives back "". This is its accessible name, naming the date and holding the
   * visible word ("Limpar data de término").
   */
  clearLabel?: string
}

/**
 * The design system's DatePicker (Popover + Calendar) for the civil dates every
 * finance form speaks: it takes and gives `YYYY-MM-DD`, so no screen converts a
 * date itself and none can shift one by a timezone. Labelled by a Field's
 * `htmlFor` through `id`, like any input.
 */
export function DateField({id, value, onValueChange, min, max, placeholder, disabled, className, invalid, clearLabel}: DateFieldProps) {
  const {t, i18n} = useTranslation()
  const picker = (
    <DatePicker
      id={id}
      locale={normalizeLocale(i18n.language)}
      value={toDate(value)}
      onValueChange={d => onValueChange(d ? toIso(d) : "")}
      min={min ? toDate(min) : undefined}
      max={max ? toDate(max) : undefined}
      placeholder={placeholder ?? t("auth.date.placeholder")}
      disabled={disabled}
      // The DatePicker's trigger carries no data-slot, so the global touch rule
      // cannot find it by slot: `touch-target` asks for the compact look and
      // the 44px target under touch (globals.css).
      className={["touch-target", className, invalid && "border-danger ring-3 ring-danger/20"].filter(Boolean).join(" ")}
    />
  )
  if (!clearLabel) return picker
  // An optional date (UX batch 4): once it has a value, a visible way back to
  // none. The calendar has no "no date" day, and a saved end date that could
  // not be removed was the bug that asked for this.
  return (
    <div className="flex min-w-0 items-center gap-2 [&>:first-child]:min-w-0 [&>:first-child]:flex-1">
      {picker}
      {value !== "" && !disabled && (
        <Button type="button" variant="ghost" size="sm" aria-label={clearLabel} onClick={() => onValueChange("")}
          className="shrink-0 text-muted-foreground">
          <X aria-hidden className="size-3.5"/>{t("auth.date.clear")}
        </Button>
      )}
    </div>
  )
}
