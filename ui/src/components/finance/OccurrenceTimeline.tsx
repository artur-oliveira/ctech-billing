"use client"

import type {ReactNode} from "react"
import {useTranslation} from "react-i18next"

import {shortDate, weekdayShort} from "@/lib/format"

/**
 * paid, forecast, overdue and skipped are bills a recurrence made; `upcoming`
 * is a date the rule will make (no bill yet), drawn as a faint outline: the
 * same "outline is not made yet" the projection uses for recurrences. A made
 * forecast is a firm outline; paid and overdue are filled.
 */
export type TimelineKind = "paid" | "forecast" | "overdue" | "skipped" | "upcoming"

export interface TimelineEntry {
  key: string
  /** `YYYY-MM-DD` */
  nominal: string
  due: string
  kind: TimelineKind
  /** The state in words ("Paga em 10/07/2026", "Vencida"): never colour alone. */
  state?: ReactNode
  amount?: ReactNode
  action?: ReactNode
}

const MARKER: Record<TimelineKind, string> = {
  paid: "border-success bg-success",
  overdue: "border-danger bg-danger",
  forecast: "border-foreground bg-background",
  upcoming: "border-muted-foreground/45 bg-background",
  skipped: "border-border bg-border",
}

const STATE: Record<TimelineKind, string> = {
  paid: "text-success",
  overdue: "font-medium text-danger",
  forecast: "text-foreground",
  upcoming: "text-muted-foreground",
  skipped: "text-muted-foreground",
}

/**
 * A recurrence's dates on a hairline rail: F4's inline detail and the editor's
 * "Próximas datas" preview speak this one visual language. Each line is the
 * nominal date with its weekday, the state in words, and, when a weekend or
 * holiday moved it, the day it is paid ("paga em 03/11/2026 (seg.), próximo
 * dia útil"), since both dates matter (DESIGN.md, F4).
 */
export function OccurrenceTimeline({label, entries}: {label: string; entries: TimelineEntry[]}) {
  const {t} = useTranslation()
  return (
    <ol aria-label={label} className="ml-1.5 space-y-2.5 border-l border-border pl-4">
      {entries.map(e => (
        <li key={e.key} className="relative">
          <span aria-hidden className={`absolute top-1.5 left-[calc(-1rem-5.5px)] size-2.5 rounded-full border-2 ring-2 ring-background ${MARKER[e.kind]}`}/>
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5">
            <p className="min-w-0 text-sm">
              <span className={`tabular-nums ${e.kind === "skipped" ? "text-muted-foreground line-through decoration-muted-foreground/60" : "text-foreground"}`}>{shortDate(e.nominal)}</span>
              <span className="text-muted-foreground"> {weekdayShort(e.nominal)}</span>
              {e.state && <span className={STATE[e.kind]}> • {e.state}</span>}
            </p>
            {(e.amount || e.action) && (
              <div className="flex shrink-0 items-baseline gap-3">
                {e.action}
                {e.amount && <span data-numeric className={`text-sm tabular-nums ${e.kind === "upcoming" || e.kind === "skipped" ? "text-muted-foreground" : "text-foreground"}`}>{e.amount}</span>}
              </div>
            )}
          </div>
          {e.due !== e.nominal && (
            <p className="text-xs text-muted-foreground">{t("bills.rec.rolled", {date: shortDate(e.due), weekday: weekdayShort(e.due)})}</p>
          )}
        </li>
      ))}
    </ol>
  )
}
