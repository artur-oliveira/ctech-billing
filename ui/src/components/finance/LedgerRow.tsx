"use client"

import {type ReactNode, useEffect, useRef} from "react"

interface LedgerRowProps {
  /** The description. Wraps to two lines on a phone, one line (truncated) from `sm`. */
  title: string
  /** The small line under it: date, account, rule. Wraps freely. */
  meta?: ReactNode
  /** The figure, already formatted; right-aligned, never squeezed. */
  amount: ReactNode
  /** Badges or a direction: beside the amount from `sm`, under the meta on a phone. */
  aside?: ReactNode
  /** False when the aside repeats what a phone already says (a group heading). */
  asideOnPhone?: boolean
  actions?: ReactNode
  /** What opens in place under the row (a settle form, a confirmation). */
  children?: ReactNode
  /** The row a link pointed at (a recurrence's overdue bill): marked and scrolled to. */
  current?: boolean
}

/**
 * One row of a finance list: bills, recurrences, imported lines.
 *
 * From `sm` up it is a single line, as it always was: description, aside,
 * amount in a fixed column, actions. Under `sm` it is not that line squeezed
 * (a 320px phone left a description 25px wide). It becomes a small card in
 * rules: the description and the amount share the first line, the description
 * wrapping onto a second rather than being cut, and the aside and actions get
 * lines of their own, full width, so every action is a 44px target in reach.
 */
export function LedgerRow({title, meta, amount, aside, asideOnPhone = true, actions, children, current = false}: LedgerRowProps) {
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (current) ref.current?.scrollIntoView?.({block: "center"})
  }, [current])
  return (
    // The pointed-at row takes the selected-row tint (brand-50), the brand's one
    // home in a list.
    <li ref={ref} aria-current={current || undefined} className={`py-2.5 ${current ? "-mx-2 rounded-md bg-brand-50 px-2" : ""}`}>
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4 sm:gap-y-1">
        <div className="col-start-1 row-start-1 min-w-0 sm:flex-1 sm:basis-40">
          <p title={title} className="line-clamp-2 text-sm text-foreground [overflow-wrap:anywhere] sm:line-clamp-1">{title}</p>
          {meta && <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{meta}</p>}
        </div>
        {aside && <div className={`col-span-2 flex-wrap items-center gap-1.5 sm:flex ${asideOnPhone ? "flex" : "hidden"}`}>{aside}</div>}
        <div className="col-start-2 row-start-1 shrink-0 text-right text-sm tabular-nums text-foreground sm:w-28">{amount}</div>
        {actions && <div className="col-span-2 flex flex-wrap gap-1 sm:basis-auto">{actions}</div>}
      </div>
      {children}
    </li>
  )
}
