"use client"

import {Button, EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {financeKeys, getCashFlow, getProjection, listAccounts, listBills} from "@/lib/api/finance"
import type {Bill, ProjectionMonth} from "@/lib/api/financeTypes"
import {bucketLabel, directionLabel} from "@/lib/finance/labels"
import {currentLocale} from "@/lib/i18n"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate, signedMoney} from "@/lib/format"
import {accountName} from "@/lib/finance/accountName"

const WINDOWS = ["3", "6", "12"]

/**
 * F1 — the finance overview: three independent blocks separated by rules. Each
 * fetches and fails on its own, so one slow or broken request never blanks the
 * page.
 */
export function OverviewView() {
  const {t} = useTranslation()
  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.overview.title")}</h1>
      <div className="grid items-start gap-8 lg:grid-cols-3">
        <Balances/>
        <Realised/>
        <DueSoon/>
      </div>
      <Projection/>
    </div>
  )
}

function Block({title, children, action}: {title: string; children: React.ReactNode; action?: React.ReactNode}) {
  const id = `blk-${title.replace(/\W+/g, "-")}`
  return (
    <section aria-labelledby={id} className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id={id} className="text-sm font-medium text-foreground">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

function Balances() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const assets = (q.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived)
  const total = assets.reduce((s, a) => s + a.balance, 0)
  return (
    <Block title={t("finance.overview.balances")}>
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : assets.length === 0 ? (
        <EmptyState icon={<Landmark/>} title={t("finance.overview.noAccounts")}
          action={<Button variant="outline" size="sm" render={<Link href="/console/finance/accounts"/>}>{t("finance.overview.createAccount")}</Button>}/>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {assets.map(a => (
            <li key={a.id} className="flex items-center justify-between py-2">
              <span className="text-foreground">{accountName(a)}</span>
              <span data-numeric className="tabular-nums">{money(a.balance)}</span>
            </li>
          ))}
          <li className="flex items-center justify-between py-2 font-medium">
            <span>{t("finance.overview.total")}</span>
            <span data-numeric className="tabular-nums">{money(total)}</span>
          </li>
        </ul>
      )}
    </Block>
  )
}

/**
 * What actually came in and went out of the person's accounts this month: the
 * cash result. The accrual one (by competence) is the DRE, one click away.
 */
function Realised() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const month = todayIso().slice(0, 7)
  const q = useQuery({queryKey: financeKeys.cashFlow(ctx.mode, ctx.space, month, month), queryFn: () => getCashFlow(ctx, month, month)})
  const m = q.data?.months[0]
  return (
    <Block title={t("finance.overview.monthResult")} action={<Link href="/console/finance/reports?view=cash" className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">{t("finance.overview.seeReports")}</Link>}>
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error || !m ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <>
          <dl className="divide-y divide-border border-y border-border text-sm">
            <div className="flex items-center justify-between py-2"><dt>{t("finance.overview.in")}</dt><dd data-numeric className="tabular-nums">{money(m.in)}</dd></div>
            <div className="flex items-center justify-between py-2"><dt>{t("finance.overview.out")}</dt><dd data-numeric className="tabular-nums">{money(m.out)}</dd></div>
            <div className="flex items-center justify-between py-2 font-medium"><dt>{t("finance.overview.result")}</dt><dd data-numeric className="tabular-nums">{signedMoney(m.in - m.out)}</dd></div>
          </dl>
        </>
      )}
    </Block>
  )
}

const DUE_SOON = 5

function DueSoon() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const pay = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, "payable"), queryFn: () => listBills(ctx, "payable")})
  const rec = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, "receivable"), queryFn: () => listBills(ctx, "receivable")})
  const order = {overdue: 0, today: 1, upcoming: 2}
  const items: Bill[] = [...(pay.data?.data ?? []), ...(rec.data?.data ?? [])]
    .sort((a, b) => order[a.bucket ?? "upcoming"] - order[b.bucket ?? "upcoming"] || a.due_date.localeCompare(b.due_date))
    .slice(0, DUE_SOON)
  const error = pay.error ?? rec.error
  return (
    <Block title={t("finance.overview.dueSoon")} action={<Link href="/console/finance/bills" className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">{t("finance.overview.seeAll")}</Link>}>
      {pay.isLoading || rec.isLoading ? <Skeleton className="h-20 w-full"/> : error ? (
        <ErrorBlock error={error} onRetry={() => { void pay.refetch(); void rec.refetch() }}/>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("finance.overview.nothingOpen")}</p>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {items.map(b => (
            <li key={b.id} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="truncate text-foreground">{b.description || t("finance.overview.noDescription")}</p>
                <p className="text-xs text-muted-foreground">
                  {directionLabel(b.direction)} • {bucketLabel(b.bucket ?? "upcoming")} • {shortDate(b.due_date)}
                </p>
              </div>
              <span data-numeric className={`tabular-nums ${b.bucket === "overdue" ? "text-danger" : ""}`}>{money(b.amount)}</span>
            </li>
          ))}
        </ul>
      )}
    </Block>
  )
}

/** One month of the projection, with the balance it leaves. */
interface ProjectedMonth extends ProjectionMonth {
  result: number
  balance: number
  /** Money in and out, forecast bills plus recurrences not yet generated. */
  inflow: number
  outflow: number
  recIn: number
  recOut: number
}

/**
 * Today's balance plus each month's result (to receive − to pay + recurrences
 * not yet generated), accumulated: what the person will have at the end of each
 * month if everything happens as forecast.
 */
function project(start: number, data: ProjectionMonth[]): ProjectedMonth[] {
  let balance = start
  return data.map(m => {
    const result = m.receivable - m.payable + m.virtual
    balance += result
    const recIn = m.virtual_receivable ?? Math.max(m.virtual, 0)
    const recOut = m.virtual_payable ?? Math.max(-m.virtual, 0)
    return {...m, result, balance, recIn, recOut, inflow: m.receivable + recIn, outflow: m.payable + recOut}
  })
}

function Projection() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const [months, setMonths] = useState("6")
  const [asTable, setAsTable] = useState(false)
  const [view, setView] = useState<"balance" | "flow">("balance")
  const q = useQuery({queryKey: financeKeys.projection(ctx.mode, ctx.space, Number(months)), queryFn: () => getProjection(ctx, Number(months))})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const start = (accounts.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived).reduce((s, a) => s + a.balance, 0)
  const data = project(start, q.data?.data ?? [])
  return (
    <Block
      title={t("finance.overview.projection")}
      action={
        <div className="flex flex-wrap items-center justify-end gap-2">
          {!asTable && (
            <div role="group" aria-label={t("finance.overview.viewLabel")} className="flex w-full items-center gap-0.5 rounded-lg sm:w-auto border border-border bg-surface p-0.5">
              {(["balance", "flow"] as const).map(v => (
                <button
                  key={v}
                  type="button"
                  aria-pressed={view === v}
                  onClick={() => setView(v)}
                  className={`flex-1 rounded-md px-3 py-1 text-sm transition-colors sm:flex-none ${view === v ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                >
                  {t(v === "balance" ? "finance.overview.viewBalance" : "finance.overview.viewFlow")}
                </button>
              ))}
            </div>
          )}
          <div className="w-32"><Select aria-label={t("finance.overview.window")} value={months} onValueChange={setMonths} options={WINDOWS.map(w => ({value: w, label: t("finance.overview.months", {count: Number(w)})}))}/></div>
          <Button variant="ghost" size="sm" onClick={() => setAsTable(v => !v)}>{asTable ? t("finance.overview.asChart") : t("finance.overview.asTable")}</Button>
        </div>
      }
    >
      {q.isLoading || accounts.isLoading ? <Skeleton className="h-40 w-full"/> : q.error || accounts.error ? (
        <ErrorBlock error={q.error ?? accounts.error} onRetry={() => { void q.refetch(); void accounts.refetch() }}/>
      ) : asTable ? <ProjectionTable data={data}/> : view === "flow" ? <FlowChart data={data}/> : <ProjectionChart data={data}/>}
      <p className="text-xs text-muted-foreground">
        {t("finance.overview.projectionNote", {balance: signedMoney(start)})}
      </p>
    </Block>
  )
}

function ProjectionTable({data}: {data: ProjectedMonth[]}) {
  const {t} = useTranslation()
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-max text-sm tabular-nums">
        <thead>
          <tr className="border-b border-border text-left text-muted-foreground">
            <th className="py-2 pr-4 font-normal">{t("finance.overview.month")}</th>
            <th className="py-2 pl-4 text-right font-normal">{directionLabel("receivable")}</th>
            <th className="py-2 pl-4 text-right font-normal">{directionLabel("payable")}</th>
            <th className="py-2 pl-4 text-right font-normal">{t("finance.overview.recurrences")}</th>
            <th className="py-2 pl-4 text-right font-normal">{t("finance.overview.monthResult")}</th>
            <th className="py-2 pl-4 text-right font-normal">{t("finance.overview.projected")}</th>
          </tr>
        </thead>
        <tbody>
          {data.map(m => (
            <tr key={m.month} className="border-b border-border">
              <th scope="row" className="py-2 pr-4 text-left font-normal">{monthShort(m.month)}</th>
              <td className="py-2 pl-4 text-right">{money(m.receivable)}</td>
              <td className="py-2 pl-4 text-right">{money(m.payable)}</td>
              <td className="py-2 pl-4 text-right">{signedMoney(m.virtual)}</td>
              <td className="py-2 pl-4 text-right">{signedMoney(m.result)}</td>
              <td className={`py-2 pl-4 text-right font-medium ${m.balance < 0 ? "text-danger" : ""}`}>{signedMoney(m.balance)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/**
 * The projected balance at the end of each month, as bars from zero: up when
 * the person still has money, down (in the danger colour, and below the axis,
 * so not by colour alone) when the forecast leaves them in the red. The month's
 * breakdown is in each bar's title and in the table view. Inline SVG, no chart
 * library.
 */
const compact = (cents: number) => new Intl.NumberFormat(currentLocale(), {style: "currency", currency: "BRL", notation: "compact", maximumFractionDigits: 1}).format(cents / 100)

/** A round step (1, 2 or 5 × a power of ten) that cuts the span into about four. */
function niceStep(span: number): number {
  const raw = span / 4
  const pow = 10 ** Math.floor(Math.log10(raw))
  return [1, 2, 5, 10].map(f => f * pow).find(s => s >= raw) ?? 10 * pow
}

function ProjectionChart({data}: {data: ProjectedMonth[]}) {
  const {t} = useTranslation()
  const W = 640, H = 200, TOP = 10, BOTTOM = 22, LEFT = 64
  const step = niceStep(Math.max(100, Math.max(0, ...data.map(m => m.balance)) - Math.min(0, ...data.map(m => m.balance))))
  const lo = Math.floor(Math.min(0, ...data.map(m => m.balance)) / step) * step
  const hi = Math.max(step, Math.ceil(Math.max(0, ...data.map(m => m.balance)) / step) * step)
  const ticks: number[] = []
  for (let v = lo; v <= hi; v += step) ticks.push(v)
  const y = (v: number) => TOP + ((hi - v) / (hi - lo)) * (H - TOP - BOTTOM)
  const zero = y(0)
  const slot = (W - LEFT) / Math.max(1, data.length)
  const bw = Math.min(36, slot * 0.5)
  return (
    <figure className="space-y-2">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t("finance.overview.chartLabel", {count: data.length})} className="h-52 w-full">
        {ticks.map(v => (
          <g key={v}>
            <line x1={LEFT} x2={W} y1={y(v)} y2={y(v)} className={v === 0 ? "stroke-border" : "stroke-border/50"} strokeWidth={1} strokeDasharray={v === 0 ? undefined : "2 3"}/>
            {/* Centavos: the formatter takes reais. */}
            <text data-axis="y" x={LEFT - 8} y={y(v) + 4} textAnchor="end" className="fill-muted-foreground text-[11px]">{compact(v)}</text>
          </g>
        ))}
        {data.map((m, i) => {
          const x = LEFT + i * slot + slot / 2 - bw / 2
          const top = Math.min(y(m.balance), zero), h = Math.abs(y(m.balance) - zero)
          return (
            <g key={m.month}>
              <title>{t("finance.overview.barTitle", {month: monthShort(m.month), balance: signedMoney(m.balance), receivable: money(m.receivable), payable: money(m.payable), recurrences: signedMoney(m.virtual)})}</title>
              <rect x={x} y={top} width={bw} height={Math.max(h, 1)} rx={2} className={m.balance < 0 ? "fill-danger" : "fill-brand-600"}/>
              <text x={LEFT + i * slot + slot / 2} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[11px]">{monthShort(m.month)}</text>
            </g>
          )
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-brand-600"/>{t("finance.overview.legendEnd")}</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-danger"/>{t("finance.overview.legendRed")}</span>
      </figcaption>
    </figure>
  )
}

/**
 * Money in and out per month. Entradas rise from the zero line and saídas fall
 * below it, so direction carries the meaning and the green/red pair is a second
 * cue, never the only one. The lighter top of each bar is what recurrences will
 * still generate (a rule's promise, not a bill yet); the dot is the month's net.
 */
function FlowChart({data}: {data: ProjectedMonth[]}) {
  const {t} = useTranslation()
  const W = 640, H = 220, TOP = 10, BOTTOM = 22, LEFT = 64
  const maxIn = Math.max(0, ...data.map(m => m.inflow)), maxOut = Math.max(0, ...data.map(m => m.outflow))
  const step = niceStep(Math.max(100, maxIn + maxOut))
  const hi = Math.max(step, Math.ceil(maxIn / step) * step)
  const lo = -Math.max(step, Math.ceil(maxOut / step) * step)
  const ticks: number[] = []
  for (let v = lo; v <= hi; v += step) ticks.push(v)
  const y = (v: number) => TOP + ((hi - v) / (hi - lo)) * (H - TOP - BOTTOM)
  const zero = y(0)
  const slot = (W - LEFT) / Math.max(1, data.length)
  const bw = Math.min(36, slot * 0.5)
  return (
    <figure className="space-y-2">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t("finance.overview.flowChartLabel", {count: data.length})} className="h-56 w-full">
        {ticks.map(v => (
          <g key={v}>
            <line x1={LEFT} x2={W} y1={y(v)} y2={y(v)} className={v === 0 ? "stroke-border" : "stroke-border/50"} strokeWidth={1} strokeDasharray={v === 0 ? undefined : "2 3"}/>
            <text data-axis="y" x={LEFT - 8} y={y(v) + 4} textAnchor="end" className="fill-muted-foreground text-[11px]">{compact(v)}</text>
          </g>
        ))}
        {data.map((m, i) => {
          const cx = LEFT + i * slot + slot / 2, x = cx - bw / 2
          const bills = (v: number) => (v / (hi - lo)) * (H - TOP - BOTTOM)
          const inBills = bills(m.inflow - m.recIn), inRec = bills(m.recIn)
          const outBills = bills(m.outflow - m.recOut), outRec = bills(m.recOut)
          return (
            <g key={m.month}>
              <title>{t("finance.overview.flowTitle", {month: monthShort(m.month), inflow: money(m.inflow), outflow: money(m.outflow), net: signedMoney(m.result), recIn: money(m.recIn), recOut: money(m.recOut)})}</title>
              {inBills > 0 && <rect x={x} y={zero - inBills} width={bw} height={inBills} className="fill-success"/>}
              {inRec > 0 && <rect x={x} y={zero - inBills - inRec} width={bw} height={inRec} rx={2} className="fill-success opacity-45"/>}
              {outBills > 0 && <rect x={x} y={zero} width={bw} height={outBills} className="fill-danger"/>}
              {outRec > 0 && <rect x={x} y={zero + outBills} width={bw} height={outRec} rx={2} className="fill-danger opacity-45"/>}
              <circle cx={cx} cy={y(m.result)} r={3.5} className="fill-foreground stroke-background" strokeWidth={1.5}/>
              <text x={cx} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[11px]">{monthShort(m.month)}</text>
            </g>
          )
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-success"/>{t("finance.overview.legendIn")}</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-danger"/>{t("finance.overview.legendOut")}</span>
        <span className="flex items-center gap-1.5"><span aria-hidden className="inline-flex size-3 overflow-hidden rounded-sm opacity-45"><span className="w-1/2 bg-success"/><span className="w-1/2 bg-danger"/></span>{t("finance.overview.legendRecurring")}</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-2.5 rounded-full bg-foreground"/>{t("finance.overview.legendNet")}</span>
      </figcaption>
    </figure>
  )
}
