"use client"

import {Button, EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {financeKeys, getProjection, listAccounts, listBills} from "@/lib/api/finance"
import type {Bill, ProjectionMonth} from "@/lib/api/financeTypes"
import {BUCKET_LABEL} from "@/lib/finance/labels"
import {monthShort} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"

const WINDOWS = [
  {value: "3", label: "3 meses"},
  {value: "6", label: "6 meses"},
  {value: "12", label: "12 meses"},
]

/**
 * F1 — the finance overview: three independent blocks separated by rules. Each
 * fetches and fails on its own, so one slow or broken request never blanks the
 * page. There is deliberately no "resultado realizado" tile: it ships with the
 * cash read in 6.4.
 */
export function OverviewView() {
  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Visão geral</h1>
      <div className="grid items-start gap-8 lg:grid-cols-2">
        <Balances/>
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
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const assets = (q.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived)
  const total = assets.reduce((s, a) => s + a.balance, 0)
  return (
    <Block title="Saldos">
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : assets.length === 0 ? (
        <EmptyState icon={<Landmark/>} title="Nenhuma conta ainda" description="Crie suas contas em Contas para ver os saldos aqui."
          action={<Button variant="outline" size="sm" render={<Link href="/console/finance/accounts"/>}>Ir para Contas</Button>}/>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {assets.map(a => (
            <li key={a.id} className="flex items-center justify-between py-2">
              <span className="text-foreground">{a.name}</span>
              <span data-numeric className="tabular-nums">{money(a.balance)}</span>
            </li>
          ))}
          <li className="flex items-center justify-between py-2 font-medium">
            <span>Total</span>
            <span data-numeric className="tabular-nums">{money(total)}</span>
          </li>
        </ul>
      )}
    </Block>
  )
}

const DUE_SOON = 5

function DueSoon() {
  const ctx = useFinanceCtx()
  const pay = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, "payable"), queryFn: () => listBills(ctx, "payable")})
  const rec = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, "receivable"), queryFn: () => listBills(ctx, "receivable")})
  const order = {overdue: 0, today: 1, upcoming: 2}
  const items: Bill[] = [...(pay.data?.data ?? []), ...(rec.data?.data ?? [])]
    .sort((a, b) => order[a.bucket ?? "upcoming"] - order[b.bucket ?? "upcoming"] || a.due_date.localeCompare(b.due_date))
    .slice(0, DUE_SOON)
  const error = pay.error ?? rec.error
  return (
    <Block title="Vencidas e próximas" action={<Link href="/console/finance/bills" className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Ver todas</Link>}>
      {pay.isLoading || rec.isLoading ? <Skeleton className="h-20 w-full"/> : error ? (
        <ErrorBlock error={error} onRetry={() => { void pay.refetch(); void rec.refetch() }}/>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nada em aberto.</p>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {items.map(b => (
            <li key={b.id} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="truncate text-foreground">{b.description || "Sem descrição"}</p>
                <p className="text-xs text-muted-foreground">
                  {b.direction === "payable" ? "A pagar" : "A receber"} • {BUCKET_LABEL[b.bucket ?? "upcoming"]} • {shortDate(b.due_date)}
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

function Projection() {
  const ctx = useFinanceCtx()
  const [months, setMonths] = useState("6")
  const [asTable, setAsTable] = useState(false)
  const q = useQuery({queryKey: financeKeys.projection(ctx.mode, ctx.space, Number(months)), queryFn: () => getProjection(ctx, Number(months))})
  const data = q.data?.data ?? []
  return (
    <Block
      title="Projeção"
      action={
        <div className="flex items-center gap-2">
          <div className="w-32"><Select aria-label="Período" value={months} onValueChange={setMonths} options={WINDOWS}/></div>
          <Button variant="ghost" size="sm" onClick={() => setAsTable(v => !v)}>{asTable ? "Ver como gráfico" : "Ver como tabela"}</Button>
        </div>
      }
    >
      {q.isLoading ? <Skeleton className="h-40 w-full"/> : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : asTable ? <ProjectionTable data={data}/> : <ProjectionChart data={data}/>}
      <p className="text-xs text-muted-foreground">Contas vencidas entram no primeiro mês. Recorrências ainda não geradas aparecem com contorno.</p>
    </Block>
  )
}

function ProjectionTable({data}: {data: ProjectionMonth[]}) {
  return (
    <table className="w-full text-sm tabular-nums">
      <thead>
        <tr className="border-b border-border text-left text-muted-foreground">
          <th className="py-2 font-normal">Mês</th>
          <th className="py-2 text-right font-normal">A receber</th>
          <th className="py-2 text-right font-normal">A pagar</th>
          <th className="py-2 text-right font-normal">Recorrências ainda não geradas</th>
        </tr>
      </thead>
      <tbody>
        {data.map(m => (
          <tr key={m.month} className="border-b border-border">
            <th scope="row" className="py-2 text-left font-normal capitalize">{monthShort(m.month)}</th>
            <td className="py-2 text-right">{money(m.receivable)}</td>
            <td className="py-2 text-right">{money(m.payable)}</td>
            <td className="py-2 text-right">{money(m.virtual)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/**
 * Diverging bars per month: receivables above the axis, payables below. Bills
 * are solid; recurrences not yet generated are outline only, so the difference
 * is shape, not hue (DESIGN.md). Inline SVG, no chart library.
 */
function ProjectionChart({data}: {data: ProjectionMonth[]}) {
  const W = 640, H = 200, MID = H / 2, PAD = 4
  const up = (m: ProjectionMonth) => m.receivable + Math.max(m.virtual, 0)
  const down = (m: ProjectionMonth) => m.payable + Math.max(-m.virtual, 0)
  const max = Math.max(1, ...data.map(up), ...data.map(down))
  const scale = (v: number) => (v / max) * (MID - 18)
  const slot = W / Math.max(1, data.length)
  const bw = Math.min(28, slot * 0.4)
  return (
    <figure className="space-y-2">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`Projeção de ${data.length} meses em reais: a receber acima do eixo, a pagar abaixo`} className="h-52 w-full">
        <line x1={0} x2={W} y1={MID} y2={MID} className="stroke-border" strokeWidth={1}/>
        {data.map((m, i) => {
          const x = i * slot + slot / 2 - bw / 2
          const rec = scale(m.receivable), pay = scale(m.payable)
          const vUp = scale(Math.max(m.virtual, 0)), vDown = scale(Math.max(-m.virtual, 0))
          return (
            <g key={m.month}>
              <title>{`${monthShort(m.month)}: a receber ${money(m.receivable)}, a pagar ${money(m.payable)}, recorrências ${money(m.virtual)}`}</title>
              {rec > 0 && <rect x={x} y={MID - rec} width={bw} height={rec} className="fill-success"/>}
              {vUp > 0 && <rect x={x + 0.5} y={MID - rec - vUp} width={bw - 1} height={vUp} className="fill-none stroke-success" strokeWidth={1} strokeDasharray="3 2"/>}
              {pay > 0 && <rect x={x} y={MID} width={bw} height={pay} className="fill-foreground/70"/>}
              {vDown > 0 && <rect x={x + 0.5} y={MID + pay} width={bw - 1} height={vDown} className="fill-none stroke-foreground/70" strokeWidth={1} strokeDasharray="3 2"/>}
              <text x={i * slot + slot / 2} y={H - PAD} textAnchor="middle" className="fill-muted-foreground text-[11px] capitalize">{monthShort(m.month)}</text>
            </g>
          )
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 bg-success"/>A receber</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 bg-foreground/70"/>A pagar</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 border border-dashed border-foreground/70"/>Recorrências ainda não geradas</span>
      </figcaption>
    </figure>
  )
}
