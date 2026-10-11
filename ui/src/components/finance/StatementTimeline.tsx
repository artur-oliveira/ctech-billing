"use client"

import {Badge, Button, Skeleton} from "@aoctech/ui"
import {useInfiniteQuery} from "@tanstack/react-query"
import {ChevronLeft, ChevronRight} from "lucide-react"
import {useEffect, useLayoutEffect, useRef, useState} from "react"
import {useTranslation} from "react-i18next"

import {STATUS, statementState} from "@/components/finance/statementStatus"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {financeKeys, listCardStatements} from "@/lib/api/finance"
import type {Card, StatementSummary} from "@/lib/api/financeTypes"
import {
  edges, flatten, initialParam, keepVisible, nextParam, type PageParam, previousParam, shiftMonth, stepWindow, type TimelinePage, windowAt,
} from "@/lib/finance/statementTimeline"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {dayMonth, signedMoney} from "@/lib/format"
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery"

/** Under lg the row shows five statements; from lg, seven (spec D5: 3 + open + 3). */
const MID = "(max-width: 63.999rem)"

/**
 * The card's statements as a row of summaries (spec § 4.2): on opening, the open
 * one selected with three before and three after; previous/next step a page and
 * load the next page beyond what is loaded, prefetching as the window reaches an
 * edge. On a phone, one summary per screen in a strip that is swiped.
 */
export function StatementTimeline({card, selected, onSelect}: {card: Card; selected: string; onSelect: (month: string) => void}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const phone = useMediaQuery(NARROW)
  const mid = useMediaQuery(MID)
  const size = phone ? 1 : mid ? 5 : 7
  const q = useInfiniteQuery({
    queryKey: financeKeys.cardStatements(ctx.mode, ctx.space, card.id, card.open_month),
    queryFn: async ({pageParam}): Promise<TimelinePage> =>
      ({...(await listCardStatements(ctx, card.id, pageParam.from, pageParam.count, pageParam.direction)), param: pageParam}),
    initialPageParam: initialParam(card.open_month) as PageParam,
    getNextPageParam: nextParam,
    getPreviousPageParam: previousParam,
  })
  const all = flatten(q.data?.pages ?? [])
  const months = all.map(s => s.month)
  const [start, setStart] = useState(() => shiftMonth(card.open_month, -3))
  const {from, to} = windowAt(months, phone ? selected : start, size)
  const {before, after} = edges(months, phone ? selected : start, size)

  // Prefetch: when the window touches the loaded edge, load the page beyond it.
  // One page in flight at a time: a second fetch would cancel the first (TanStack's
  // cancelRefetch) and ask for its months again.
  useEffect(() => {
    if (!q.isSuccess || q.isFetching) return
    if (before === 0 && q.hasPreviousPage) void q.fetchPreviousPage({cancelRefetch: false})
    else if (after === 0 && q.hasNextPage) void q.fetchNextPage({cancelRefetch: false})
  }, [q, before, after])

  const select = (month: string) => {
    onSelect(month)
    setStart(s => keepVisible(months, s, size, month))
  }
  const step = (dir: -1 | 1) => {
    if (phone) {
      const i = months.indexOf(selected) + dir
      if (months[i]) select(months[i])
      return
    }
    setStart(s => stepWindow(months, s, size, dir))
  }

  // Phone: the strip follows the selection, and the selection follows a swipe.
  const strip = useRef<HTMLOListElement>(null)
  const firstShown = useRef<string | undefined>(undefined)
  useLayoutEffect(() => {
    const el = strip.current
    if (!phone || !el) return
    const prev = firstShown.current
    firstShown.current = months[0]
    if (prev && months[0] !== prev) {
      // Older statements were prepended: keep the card under the finger. Set
      // absolutely, from the selection (onScroll keeps it the card on screen):
      // Chrome may already have re-snapped to it, and adding the shift would
      // count it twice.
      const i = months.indexOf(selected)
      if (i >= 0) el.scrollLeft = i * el.clientWidth
    }
  }, [phone, months[0]]) // eslint-disable-line react-hooks/exhaustive-deps
  useLayoutEffect(() => {
    const el = strip.current
    if (!phone || !el) return
    const i = months.indexOf(selected)
    if (i >= 0 && Math.round(el.scrollLeft / (el.clientWidth || 1)) !== i) el.scrollLeft = i * el.clientWidth
    // months.length: the strip mounts only once the first page arrives, after the selection was set.
  }, [phone, selected, months.length > 0]) // eslint-disable-line react-hooks/exhaustive-deps
  const onScroll = () => {
    const el = strip.current
    if (!phone || !el || !el.clientWidth) return
    const month = months[Math.round(el.scrollLeft / el.clientWidth)]
    if (month && month !== selected) onSelect(month)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return
    e.preventDefault()
    const next = months[months.indexOf(selected) + (e.key === "ArrowRight" ? 1 : -1)]
    if (!next) return
    select(next)
    requestAnimationFrame(() => strip.current?.querySelector<HTMLButtonElement>(`[data-month="${next}"]`)?.focus())
  }

  if (q.isLoading) return <div className="flex gap-2" aria-busy>{Array.from({length: 3}, (_, i) => <Skeleton key={i} className="h-24 flex-1"/>)}</div>
  if (q.error) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>

  const atStart = phone ? months.indexOf(selected) <= 0 : before === 0
  const atEnd = phone ? months.indexOf(selected) >= months.length - 1 : after === 0
  return (
    <div className="flex items-stretch gap-1">
      <Button variant="ghost" size="sm" aria-label={t("finance.cards.earlier")} className="self-center"
        disabled={atStart && !q.hasPreviousPage} aria-busy={q.isFetchingPreviousPage || undefined} onClick={() => step(-1)}>
        <ChevronLeft aria-hidden className="size-4"/>
      </Button>
      <ol ref={strip} aria-label={t("finance.cards.timeline")} onKeyDown={onKeyDown} onScroll={onScroll}
        className="flex min-w-0 flex-1 gap-2 max-sm:gap-0 max-sm:snap-x max-sm:snap-mandatory max-sm:overflow-x-auto max-sm:[scrollbar-width:none]">
        {all.map((s, i) => (
          <li key={s.month} data-in-window={i >= from && i < to ? "" : undefined}
            className={`max-sm:w-full max-sm:shrink-0 max-sm:snap-center max-sm:px-0.5 sm:min-w-0 sm:flex-1 ${i >= from && i < to ? "" : "sm:hidden"}`}>
            <SummaryCard summary={s} pressed={s.month === selected} onPress={() => select(s.month)}/>
          </li>
        ))}
      </ol>
      <Button variant="ghost" size="sm" aria-label={t("finance.cards.later")} className="self-center"
        disabled={atEnd && !q.hasNextPage} aria-busy={q.isFetchingNextPage || undefined} onClick={() => step(1)}>
        <ChevronRight aria-hidden className="size-4"/>
      </Button>
    </div>
  )
}

/**
 * One statement in the row: month and state, the total, then when it closes and
 * is due (day and month: the year is the month's), and how many purchases. A
 * hairline card; the selected one takes the brand border and the selected-row
 * tint, the only colour besides its status badge.
 */
function SummaryCard({summary: s, pressed, onPress}: {summary: StatementSummary; pressed: boolean; onPress: () => void}) {
  const {t} = useTranslation()
  const state = statementState(s, todayIso())
  const {tone, icon: Icon} = STATUS[state]
  const closed = s.status === "closed" || s.status === "paid"
  return (
    <button type="button" data-month={s.month} aria-pressed={pressed} onClick={onPress}
      aria-label={t("finance.cards.summaryLabel", {month: monthShort(s.month), state: t(`finance.cards.status.${state}`), total: signedMoney(s.total)})}
      className={`flex h-full w-full flex-col items-start gap-1.5 rounded-lg border p-3 text-left outline-none transition-colors duration-150 ease-out
        focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 motion-reduce:transition-none
        ${pressed ? "border-brand-600 bg-brand-50" : "border-border hover:bg-surface"}`}>
      <span className="flex w-full flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <span className="text-sm font-semibold tabular-nums text-foreground">{monthShort(s.month)}</span>
        <Badge tone={tone}><Icon aria-hidden/>{t(`finance.cards.status.${state}`)}</Badge>
      </span>
      <span data-numeric className="text-base font-semibold tabular-nums text-foreground [overflow-wrap:anywhere]">{signedMoney(s.total)}</span>
      <span className="grid gap-0.5 text-xs text-muted-foreground tabular-nums">
        <span>{t(closed ? "finance.cards.closedShort" : "finance.cards.closesShort", {date: dayMonth(s.closing_date)})}</span>
        <span>{t("finance.cards.dueShort", {date: dayMonth(s.due_date)})}</span>
        <span>{t("finance.cards.itemsCount", {count: s.items_count})}</span>
      </span>
    </button>
  )
}
