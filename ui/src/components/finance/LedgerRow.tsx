"use client"

import {Button, cn, RowMenu, SwipeRow} from "@aoctech/ui"
import {type ReactNode, useEffect, useRef} from "react"
import {useTranslation} from "react-i18next"

/** A row's secondary or destructive action: inline on a laptop; on a phone behind a swipe and in "⋯". */
export interface RowAction {
  key: string
  label: string
  /** Opens the row's own step for it: a confirmation for anything destructive, a form for an edit. */
  onSelect: () => void
  destructive?: boolean
  /** The inline button's aria-expanded, when its step opens under the row. */
  expanded?: boolean
}

interface LedgerRowProps {
  /** The description. Wraps to two lines on a phone, one line (truncated) from `sm`. */
  title: string
  /** Drawn before the title: a card's brand mark. Decoration. */
  leading?: ReactNode
  /** The small line under it: date, account, rule. Wraps freely. */
  meta?: ReactNode
  /** The figure, already formatted; right-aligned, never squeezed. */
  amount: ReactNode
  /** A second figure (a statement's running balance): its own column from `sm`, under the amount on a phone. */
  balance?: ReactNode
  /** Names the second figure on a phone, where it has no column heading ("Saldo"). */
  balanceLabel?: string
  /** Badges or a direction: beside the amount from `sm`, under the meta on a phone. */
  aside?: ReactNode
  /** False when the aside repeats what a phone already says (a group heading). */
  asideOnPhone?: boolean
  /** The row's primary actions (Pagar, Ver): inline everywhere. */
  actions?: ReactNode
  /** Secondary and destructive actions: inline from `sm`; on a phone a left swipe and the "⋯" menu. */
  more?: RowAction[]
  /** A row that is out of use (archived): drawn muted. */
  muted?: boolean
  /** What opens in place under the row (a settle form, a confirmation). */
  children?: ReactNode
  /** The row a link pointed at (a recurrence's overdue bill): marked and scrolled to. */
  current?: boolean
}

/**
 * One row of a finance list: bills, recurrences, statement lines, imported
 * lines, accounts.
 *
 * From `sm` up it is a single line, as it always was: description, aside,
 * amount (and a second figure) in fixed columns, actions. Under `sm` it is not
 * that line squeezed (a 320px phone left a description 25px wide). It becomes a
 * small card in rules: the description and the amount share the first line,
 * the description wrapping onto a second rather than being cut, and the aside
 * and the primary actions get lines of their own. Secondary and destructive
 * actions leave the phone's line (UX batch 4): a left swipe reveals them, and
 * the "⋯" button beside the amount lists them for a keyboard or a screen
 * reader. Either way, choosing one opens the row's own confirmation. The
 * gesture, the revealed strip and the menu are @aoctech/ui's `SwipeRow` and
 * `RowMenu` (UX batch 5): this row only lays out what slides.
 */
export function LedgerRow({
  title, leading, meta, amount, balance, balanceLabel, aside, asideOnPhone = true, actions, more = [], muted = false, children, current = false,
}: LedgerRowProps) {
  const {t} = useTranslation()
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (current) ref.current?.scrollIntoView?.({block: "center"})
  }, [current])
  const hasMore = more.length > 0
  return (
    // The pointed-at row takes the selected-row tint (brand-50), the brand's one
    // home in a list.
    <li ref={ref} aria-current={current || undefined} className={cn(current && "-mx-2 rounded-md bg-brand-50 px-2")}>
      {/* The front is opaque and covers what a left swipe uncovers (a phone
          only; SwipeRow renders no strip elsewhere). */}
      <SwipeRow actions={more} frontClassName={cn("py-2.5", current && "bg-brand-50")}>
        {/* A finger's pointer is implicitly captured by what it landed on; when
            the swipe locks, SwipeRow captures it on the front and that child
            gets `lostpointercapture`, which bubbles. @aoctech/ui 0.4.0's front
            reads any of them as "the browser took the gesture" and cancels the
            swipe (measured in Chrome, UX batch 5). Only the front's own loss
            means that, so a child's stops here. Remove once ctech-ui checks
            `e.target === e.currentTarget`. */}
        <div onLostPointerCapture={e => e.stopPropagation()} className={cn(
          "grid items-start gap-x-3 gap-y-1.5 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4 sm:gap-y-1",
          hasMore ? "grid-cols-[minmax(0,1fr)_auto_auto]" : "grid-cols-[minmax(0,1fr)_auto]",
        )}>
          <div className="col-start-1 row-start-1 flex min-w-0 items-start gap-3 sm:flex-1 sm:basis-40 sm:items-center">
            {leading && <div aria-hidden className="mt-0.5 shrink-0 sm:mt-0">{leading}</div>}
            <div className="min-w-0">
              <p title={title} className={cn("line-clamp-2 text-sm [overflow-wrap:anywhere] sm:line-clamp-1", muted ? "text-muted-foreground" : "text-foreground")}>{title}</p>
              {meta && <div className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{meta}</div>}
            </div>
          </div>
          {aside && <div className={cn("col-span-full flex-wrap items-center gap-1.5 sm:flex", asideOnPhone ? "flex" : "hidden")}>{aside}</div>}
          <div className="col-start-2 row-start-1 shrink-0 text-right text-sm tabular-nums text-foreground sm:w-28">
            {amount}
            {balance !== undefined && (
              <p className="text-xs text-muted-foreground sm:hidden">{balanceLabel ? `${balanceLabel} ` : ""}{balance}</p>
            )}
          </div>
          {balance !== undefined && (
            <div className="hidden shrink-0 text-right text-sm tabular-nums text-muted-foreground sm:block sm:w-28">
              {balanceLabel && <span className="sr-only">{balanceLabel} </span>}{balance}
            </div>
          )}
          {hasMore && (
            <div className="col-start-3 row-start-1 -my-1.5 sm:hidden">
              <RowMenu label={t("finance.row.more", {name: title})} items={more}/>
            </div>
          )}
          {(actions || hasMore) && (
            <div className={cn("col-span-full flex-wrap gap-2 sm:flex sm:basis-auto", actions ? "flex" : "hidden")}>
              {actions}
              {more.map(a => (
                <Button key={a.key} size="sm" variant="ghost" aria-expanded={a.expanded} onClick={a.onSelect} className="max-sm:hidden">
                  {a.label}
                </Button>
              ))}
            </div>
          )}
        </div>
      </SwipeRow>
      {/* What opens under the row keeps the row's bottom rhythm; nothing open, nothing drawn. */}
      <div className="pb-2.5 empty:hidden">{children}</div>
    </li>
  )
}
