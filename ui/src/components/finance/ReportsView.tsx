"use client"

import {Field, Select, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {segmentHit} from "@/components/ui/segmentHit"
import {financeKeys, getCardSpending, getCashFlow, getDRE, listAccounts, listCards} from "@/lib/api/finance"
import type {Account, Card, CardSpending, CashFlow, DRE, DREGroup, SpendRow} from "@/lib/api/financeTypes"
import {dreGroupLabel} from "@/lib/finance/labels"
import {monthRange, type PresetId, PRESETS} from "@/lib/finance/periods"
import {monthShort, todayIso} from "@/lib/finance/today"
import {selectCopy} from "@/lib/selectCopy"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, signedMoney} from "@/lib/format"
import {currentLocale, t as tr} from "@/lib/i18n"
import {accountName} from "@/lib/finance/accountName"

export type ReportView = "dre" | "cash" | "cards" | "review"

const TABS: ReportView[] = ["dre", "cash", "cards", "review"]

/** The subtotal a DRE reader expects after each of these groups, cumulative. */
const SUBTOTAL_AFTER: Partial<Record<DREGroup, "netRevenue" | "grossProfit" | "operatingResult">> = {
  deductions: "netRevenue",
  costs: "grossProfit",
  operating_expenses: "operatingResult",
}

const sum = (xs: number[]) => xs.reduce((acc, x) => acc + x, 0)

function nameOf(accounts: Account[], id: string): string {
  if (id === "") return tr("finance.reports.uncategorized")
  // Paying a card's statement: one cash-flow line per card (6.5).
  if (id.startsWith("card:")) return tr("finance.reports.statementOf", {name: (() => {const c = accounts.find(a => a.id === id.slice(5)); return c ? accountName(c) : undefined})() ?? tr("finance.reports.theCard")})
  const a = accounts.find(x => x.id === id)
  if (!a) return tr("finance.reports.removed")
  return a.archived ? tr("finance.reports.archivedName", {name: accountName(a)}) : accountName(a)
}

/**
 * F7 — the two reports read side by side. The DRE is accrual (each income and
 * expense in the month it belongs to); the cash flow is cash (the month the
 * money moved). One line under the tabs says so, because the difference between
 * the two is the first question anybody asks.
 */
export function ReportsView({view: initial = "dre"}: {view?: ReportView}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const [view, setView] = useState<ReportView>(initial)
  const [preset, setPreset] = useState<PresetId>("this_year")
  const latestComplete = shiftMonth(todayIso().slice(0, 7), -1)
  const [reviewMonth, setReviewMonth] = useState(latestComplete)
  const selectedReviewMonth = reviewMonth > latestComplete ? latestComplete : reviewMonth
  const {from, to} = monthRange(preset, todayIso())
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const names = accounts.data?.data ?? []

  const choose = (v: ReportView) => {
    setView(v)
    // A link to this page opens the same tab; no navigation, no refetch.
    window.history.replaceState(null, "", `?view=${v}`)
  }

  return (
    <div className="space-y-4">
      <div className="space-y-3">
        <div role="tablist" aria-label={t("finance.reports.report")} className="flex w-full min-w-0 items-center gap-0.5 overflow-x-auto whitespace-nowrap rounded-lg border border-border bg-surface p-0.5 [scrollbar-width:none]">
          {TABS.map(tab => (
            <button
              key={tab}
              type="button"
              role="tab"
              data-slot="segmented-item"
              id={`tab-${tab}`}
              aria-selected={view === tab}
              aria-controls="report-panel"
              onClick={() => choose(tab)}
              className={`${segmentHit} shrink-0 rounded-md px-3 py-1 text-sm transition-colors ${view === tab ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
            >
              {t(`finance.reports.tabs.${tab}`)}
            </button>
          ))}
        </div>
        <div className="flex min-h-10 justify-end">
          {view === "review" ? (
            <Field label={t("finance.reports.review.month")} htmlFor="review-month">
              <input id="review-month" type="month" lang={currentLocale()} max={latestComplete} value={selectedReviewMonth} onChange={event => setReviewMonth(event.target.value)} className="h-9 rounded-md border border-border bg-background px-3 text-sm text-foreground"/>
            </Field>
          ) : (
            <Field label={t("finance.reports.period")} htmlFor="rp-period">
              <Select {...selectCopy()} id="rp-period" aria-label={t("finance.reports.period")} value={preset} onValueChange={v => setPreset(v as PresetId)} className="w-48" options={PRESETS.map(p => ({value: p.value, label: t(`finance.presets.${p.value}`)}))}/>
            </Field>
          )}
        </div>
      </div>
      <div role="tabpanel" id="report-panel" aria-labelledby={`tab-${view}`}>
        {view === "dre" ? <DRETable from={from} to={to} accounts={names}/>
          : view === "cash" ? <CashTable from={from} to={to} accounts={names}/>
            : view === "cards" ? <CardSpendingView key={`${ctx.mode}-${ctx.space}`} from={from} to={to} accounts={names}/>
              : <MonthlyReview month={selectedReviewMonth}/>}
      </div>
    </div>
  )
}

function Loading() {
  return <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
}

function Empty() {
  const {t} = useTranslation()
  return <p className="py-6 text-center text-sm text-muted-foreground">{t("finance.reports.empty")}</p>
}

function shiftMonth(month: string, offset: number): string {
  const [year, number] = month.split("-").map(Number)
  const index = year * 12 + number - 1 + offset
  return `${Math.floor(index / 12)}-${String(index % 12 + 1).padStart(2, "0")}`
}

function MonthlyReview({month}: {month: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const from = shiftMonth(month, -1)
  const dre = useQuery({queryKey: financeKeys.dre(ctx.mode, ctx.space, from, month), queryFn: () => getDRE(ctx, from, month)})
  const cash = useQuery({queryKey: financeKeys.cashFlow(ctx.mode, ctx.space, from, month), queryFn: () => getCashFlow(ctx, from, month)})
  const cards = useQuery({queryKey: financeKeys.cardSpending(ctx.mode, ctx.space, from, month, ""), queryFn: () => getCardSpending(ctx, from, month)})
  const failed = dre.error ?? cash.error ?? cards.error
  const retry = () => { void dre.refetch(); void cash.refetch(); void cards.refetch() }
  const resultAt = (key: string) => { const i = dre.data?.months.indexOf(key) ?? -1; return i < 0 ? 0 : dre.data?.result[i] ?? 0 }
  const cashAt = (key: string) => cash.data?.months.find(row => row.month === key)
  const cardAt = (key: string) => { const i = cards.data?.months.indexOf(key) ?? -1; return i < 0 ? 0 : cards.data?.by_month[i] ?? 0 }
  const previous = from
  const metrics = [
    {key: "result", current: resultAt(month), prior: resultAt(previous)},
    {key: "in", current: cashAt(month)?.in ?? 0, prior: cashAt(previous)?.in ?? 0},
    {key: "out", current: cashAt(month)?.out ?? 0, prior: cashAt(previous)?.out ?? 0},
    {key: "cards", current: cardAt(month), prior: cardAt(previous)},
  ]
  return <section className="space-y-4" aria-label={t("finance.reports.review.title")}>
    <header className="space-y-1"><h2 className="text-base font-medium">{t("finance.reports.review.title")}</h2><p className="text-sm text-muted-foreground">{t("finance.reports.review.description")}</p></header>
    {failed ? <ErrorBlock error={failed} onRetry={retry}/> : dre.isLoading || cash.isLoading || cards.isLoading ? <Loading/> : (
      <div className="grid gap-3 sm:grid-cols-2">
        {metrics.map(metric => <article key={metric.key} className="rounded-lg border border-border p-4">
          <h3 className="text-sm text-muted-foreground">{t(`finance.reports.review.${metric.key}`)}</h3>
          <p className="mt-2 text-lg font-semibold tabular-nums">{money(metric.current)}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t("finance.reports.review.previous", {month: monthShort(previous), amount: money(metric.prior), delta: signedMoney(metric.current - metric.prior)})}</p>
        </article>)}
      </div>
    )}
  </section>
}

/** Wide tables scroll inside their box on a phone; the label column stays put. */
function Table({months, children}: {months: string[]; children: React.ReactNode}) {
  const {t} = useTranslation()
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-max text-sm tabular-nums">
        <thead>
          <tr className="border-b border-border text-muted-foreground">
            <th className="sticky left-0 bg-background py-2 pr-4 text-left font-normal">&nbsp;</th>
            {months.map(m => <th key={m} className="py-2 pl-4 text-right font-normal">{monthShort(m)}</th>)}
            <th className="py-2 pl-4 text-right font-normal">{t("finance.reports.total")}</th>
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}

type RowKind = "group" | "item" | "subtotal" | "result"

function Row({label, amounts, total, kind, show = signedMoney}: {label: string; amounts: number[]; total: number; kind: RowKind; show?: (n: number) => string}) {
  const head = {
    group: "font-medium text-foreground",
    item: "pl-4 text-muted-foreground font-normal",
    subtotal: "font-medium text-foreground italic",
    result: "font-semibold text-foreground",
  }[kind]
  const line = kind === "subtotal" || kind === "result" ? "border-t border-border" : ""
  return (
    <tr className={line}>
      <th scope="row" className={`sticky left-0 bg-background py-1.5 pr-4 text-left ${head}`}>{label}</th>
      {amounts.map((a, i) => <td key={i} className={`py-1.5 pl-4 text-right ${kind === "item" ? "text-muted-foreground" : "text-foreground"} ${kind === "result" ? "font-semibold" : ""}`}>{show(a)}</td>)}
      <td className={`py-1.5 pl-4 text-right text-foreground ${kind === "result" ? "font-semibold" : "font-medium"}`}>{show(total)}</td>
    </tr>
  )
}

function DRETable({from, to, accounts}: {from: string; to: string; accounts: Account[]}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.dre(ctx.mode, ctx.space, from, to), queryFn: () => getDRE(ctx, from, to)})
  if (q.isLoading) return <Loading/>
  if (q.error || !q.data) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
  const r: DRE = q.data
  if (r.groups.length === 0) return <Empty/>
  const running = r.months.map(() => 0)
  const rows: React.ReactNode[] = []
  for (const g of r.groups) {
    g.amounts.forEach((a, i) => { running[i] += a })
    rows.push(<Row key={g.group} label={dreGroupLabel(g.group)} amounts={g.amounts} total={g.total} kind="group"/>)
    for (const c of g.categories) {
      rows.push(<Row key={`${g.group}-${c.category_id}`} label={nameOf(accounts, c.category_id)} amounts={c.amounts} total={c.total} kind="item"/>)
    }
    const subtotal = SUBTOTAL_AFTER[g.group]
    if (subtotal) rows.push(<Row key={`sub-${g.group}`} label={t(`finance.reports.${subtotal}`)} amounts={[...running]} total={sum(running)} kind="subtotal"/>)
  }
  rows.push(<Row key="result" label={t("finance.reports.result")} amounts={r.result} total={r.total} kind="result"/>)
  return <Table months={r.months}>{rows}</Table>
}

function CashTable({from, to, accounts}: {from: string; to: string; accounts: Account[]}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.cashFlow(ctx.mode, ctx.space, from, to), queryFn: () => getCashFlow(ctx, from, to)})
  if (q.isLoading) return <Loading/>
  if (q.error || !q.data) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
  const r: CashFlow = q.data
  const months = r.months.map(m => m.month)
  if (r.months.every(m => m.in === 0 && m.out === 0 && m.openings === 0)) {
    return <><Figure label={t("finance.reports.openingBalance")} amount={r.opening_cash}/><Empty/></>
  }
  // A category's net in a month sits under Entradas when positive and under
  // Saídas when negative; a category can be on both sides in different months.
  const ids = [...new Set(r.months.flatMap(m => m.lines.map(l => l.category_id)))]
    .sort((a, b) => nameOf(accounts, a).localeCompare(nameOf(accounts, b), currentLocale()))
  const side = (sign: 1 | -1) => ids.flatMap(id => {
    const amounts = r.months.map(m => {
      const a = m.lines.find(l => l.category_id === id)?.amount ?? 0
      return Math.sign(a) === sign ? Math.abs(a) : 0
    })
    return sum(amounts) === 0 ? [] : [<Row key={`${sign}-${id || "none"}`} label={nameOf(accounts, id)} amounts={amounts} total={sum(amounts)} kind="item" show={money}/>]
  })
  const ins = r.months.map(m => m.in), outs = r.months.map(m => m.out), openings = r.months.map(m => m.openings)
  const result = r.months.map(m => m.in - m.out)
  return (
    <div className="space-y-3">
      <Figure label={t("finance.reports.openingBalance")} amount={r.opening_cash}/>
      <Table months={months}>
        <Row label={t("finance.reports.in")} amounts={ins} total={sum(ins)} kind="group" show={money}/>
        {side(1)}
        <Row label={t("finance.reports.out")} amounts={outs} total={sum(outs)} kind="group" show={money}/>
        {side(-1)}
        {openings.some(o => o !== 0) && <Row label={t("finance.reports.openings")} amounts={openings} total={sum(openings)} kind="group"/>}
        <Row label={t("finance.reports.monthResult")} amounts={result} total={sum(result)} kind="result"/>
      </Table>
      <Figure label={t("finance.reports.closingBalance")} amount={r.closing_cash}/>
    </div>
  )
}

function Figure({label, amount}: {label: string; amount: number}) {
  return (
    <div className="flex items-baseline justify-between text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span data-numeric className="font-medium tabular-nums text-foreground">{signedMoney(amount)}</span>
    </div>
  )
}

const ALL_CARDS = "all"

function cardName(cards: Card[], id: string): string {
  const c = cards.find(x => x.id === id)
  if (!c) return tr("finance.reports.spending.unknownCard")
  return c.archived ? tr("finance.reports.spending.archivedCard", {name: c.name}) : c.name
}

/**
 * What the cards billed, per category and month, by statement month (the
 * statement that bills each installment): what the person pays, not the
 * accrual the DRE shows. All cards or one; each row's share of the total.
 */
function CardSpendingView({from, to, accounts}: {from: string; to: string; accounts: Account[]}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const [card, setCard] = useState(ALL_CARDS)
  const cardId = card === ALL_CARDS ? undefined : card
  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)})
  const q = useQuery({
    queryKey: financeKeys.cardSpending(ctx.mode, ctx.space, from, to, card),
    queryFn: () => getCardSpending(ctx, from, to, cardId),
  })
  const list = [...(cards.data?.data ?? [])].sort((a, b) => Number(a.archived) - Number(b.archived) || a.name.localeCompare(b.name, currentLocale()))
  const r: CardSpending | undefined = q.data
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <Field label={t("finance.reports.spending.card")} htmlFor="rp-card">
          <Select {...selectCopy()} id="rp-card" aria-label={t("finance.reports.spending.card")} value={card} onValueChange={setCard} className="w-56"
            options={[{value: ALL_CARDS, label: t("finance.reports.spending.all")}, ...list.map(c => ({value: c.id, label: cardName(list, c.id)}))]}/>
        </Field>
      </div>
      <p className="max-w-prose text-xs text-muted-foreground">{t("finance.reports.spending.basis")}</p>
      {q.isLoading ? <Loading/> : q.error || !r ? <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/> : r.categories.length === 0 ? <Empty/> : (
        <>
          <SpendTable months={r.months} rows={r.categories} label={row => nameOf(accounts, row.category_id ?? "")} byMonth={r.by_month} total={r.total}
            head={t("finance.reports.spending.category")} listLabel={t("finance.reports.spending.byCategoryList")}/>
          {!cardId && r.cards.length > 1 && (
            <section aria-label={t("finance.reports.spending.byCard")} className="space-y-2 pt-2">
              <h2 className="text-sm font-medium text-foreground">{t("finance.reports.spending.byCard")}</h2>
              <SpendTable months={r.months} rows={r.cards} label={row => cardName(list, row.card_id ?? "")} byMonth={r.by_month} total={r.total}
                head={t("finance.reports.spending.card")} listLabel={t("finance.reports.spending.byCardList")}/>
            </section>
          )}
        </>
      )}
    </div>
  )
}

function SpendTable({months, rows, label, byMonth, total, head, listLabel}: {
  months: string[]; rows: SpendRow[]; label: (r: SpendRow) => string; byMonth: number[]; total: number; head: string; listLabel: string
}) {
  const {t} = useTranslation()
  const pct = new Intl.NumberFormat(currentLocale(), {style: "percent", maximumFractionDigits: 0})
  const share = (n: number) => (total > 0 ? pct.format(n / total) : "")
  return (
    <>
    {/* A phone reads the ranking first: each row's total and share, then the months below. */}
    <ul data-ranking aria-label={listLabel} className="divide-y divide-border border-y border-border sm:hidden">
      {rows.map(row => (
        <li key={row.category_id ?? row.card_id} className="flex items-baseline justify-between gap-3 py-2 text-sm">
          <span className="min-w-0 [overflow-wrap:anywhere] text-foreground">{label(row)}</span>
          <span className="shrink-0 text-right tabular-nums">
            <span className="font-medium text-foreground">{money(row.total)}</span>
            <span className="ml-2 text-muted-foreground">{share(row.total)}</span>
          </span>
        </li>
      ))}
    </ul>
    <div className="overflow-x-auto">
      <table className="w-full min-w-max text-sm tabular-nums">
        <thead>
          <tr className="border-b border-border text-muted-foreground">
            <th className="sticky left-0 bg-background py-2 pr-4 text-left font-normal"><span className="sr-only">{head}</span></th>
            {months.map(m => <th key={m} className="py-2 pl-4 text-right font-normal">{monthShort(m)}</th>)}
            <th className="py-2 pl-4 text-right font-normal">{t("finance.reports.total")}</th>
            <th className="py-2 pl-4 text-right font-normal">{t("finance.reports.spending.share")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.category_id ?? row.card_id}>
              <th scope="row" className="sticky left-0 bg-background py-1.5 pr-4 text-left font-normal text-foreground">
                <span className="block max-w-[45vw] truncate sm:max-w-none" title={label(row)}>{label(row)}</span>
              </th>
              {row.amounts.map((a, i) => <td key={i} className="py-1.5 pl-4 text-right text-muted-foreground">{money(a)}</td>)}
              <td className="py-1.5 pl-4 text-right font-medium text-foreground">{money(row.total)}</td>
              <td className="py-1.5 pl-4 text-right text-muted-foreground">{share(row.total)}</td>
            </tr>
          ))}
          <tr className="border-t border-border">
            <th scope="row" className="sticky left-0 bg-background py-1.5 pr-4 text-left font-semibold text-foreground">{t("finance.reports.total")}</th>
            {byMonth.map((a, i) => <td key={i} className="py-1.5 pl-4 text-right font-semibold text-foreground">{money(a)}</td>)}
            <td className="py-1.5 pl-4 text-right font-semibold text-foreground">{money(total)}</td>
            <td className="py-1.5 pl-4 text-right text-muted-foreground">{share(total)}</td>
          </tr>
        </tbody>
      </table>
    </div>
    </>
  )
}
