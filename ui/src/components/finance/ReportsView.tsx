"use client"

import {Field, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {financeKeys, getCashFlow, getDRE, listAccounts} from "@/lib/api/finance"
import type {Account, CashFlow, DRE, DREGroup} from "@/lib/api/financeTypes"
import {dreGroupLabel} from "@/lib/finance/labels"
import {monthRange, type PresetId, PRESETS} from "@/lib/finance/periods"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, signedMoney} from "@/lib/format"
import {currentLocale, t as tr} from "@/lib/i18n"
import {accountName} from "@/lib/finance/accountName"

export type ReportView = "dre" | "cash"

const TABS: ReportView[] = ["dre", "cash"]

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
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div role="tablist" aria-label={t("finance.reports.report")} className="flex max-w-full items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5">
          {TABS.map(tab => (
            <button
              key={tab}
              type="button"
              role="tab"
              id={`tab-${tab}`}
              aria-selected={view === tab}
              aria-controls="report-panel"
              onClick={() => choose(tab)}
              className={`rounded-md px-3 py-1 text-sm transition-colors ${view === tab ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
            >
              {t(`finance.reports.tabs.${tab}`)}
            </button>
          ))}
        </div>
        <Field label={t("finance.reports.period")} htmlFor="rp-period">
          <Select id="rp-period" aria-label={t("finance.reports.period")} value={preset} onValueChange={v => setPreset(v as PresetId)} className="w-48" options={PRESETS.map(p => ({value: p.value, label: t(`finance.presets.${p.value}`)}))}/>
        </Field>
      </div>
      <div role="tabpanel" id="report-panel" aria-labelledby={`tab-${view}`}>
        {view === "dre" ? <DRETable from={from} to={to} accounts={names}/> : <CashTable from={from} to={to} accounts={names}/>}
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
