"use client"

import {Button, EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {financeKeys, getCashFlow, getProjection, listAccounts, listBills} from "@/lib/api/finance"
import type {Bill, ProjectionMonth} from "@/lib/api/financeTypes"
import {BUCKET_LABEL} from "@/lib/finance/labels"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate, signedMoney} from "@/lib/format"

const WINDOWS = [
  {value: "3", label: "3 meses"},
  {value: "6", label: "6 meses"},
  {value: "12", label: "12 meses"},
]

/**
 * F1 — the finance overview: three independent blocks separated by rules. Each
 * fetches and fails on its own, so one slow or broken request never blanks the
 * page.
 */
export function OverviewView() {
  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Resumo</h1>
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

/**
 * What actually came in and went out of the person's accounts this month: the
 * cash result. The accrual one (by competence) is the DRE, one click away.
 */
function Realised() {
  const ctx = useFinanceCtx()
  const month = todayIso().slice(0, 7)
  const q = useQuery({queryKey: financeKeys.cashFlow(ctx.mode, ctx.space, month, month), queryFn: () => getCashFlow(ctx, month, month)})
  const m = q.data?.months[0]
  return (
    <Block title="Resultado do mês" action={<Link href="/console/finance/reports?view=cash" className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Ver relatórios</Link>}>
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error || !m ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <>
          <dl className="divide-y divide-border border-y border-border text-sm">
            <div className="flex items-center justify-between py-2"><dt>Entrou</dt><dd data-numeric className="tabular-nums">{money(m.in)}</dd></div>
            <div className="flex items-center justify-between py-2"><dt>Saiu</dt><dd data-numeric className="tabular-nums">{money(m.out)}</dd></div>
            <div className="flex items-center justify-between py-2 font-medium"><dt>Resultado</dt><dd data-numeric className="tabular-nums">{signedMoney(m.in - m.out)}</dd></div>
          </dl>
          <p className="text-xs text-muted-foreground">Pelo caixa: o que de fato entrou e saiu das suas contas neste mês.</p>
        </>
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

/** One month of the projection, with the balance it leaves. */
interface ProjectedMonth extends ProjectionMonth {
  result: number
  balance: number
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
    return {...m, result, balance}
  })
}

function Projection() {
  const ctx = useFinanceCtx()
  const [months, setMonths] = useState("6")
  const [asTable, setAsTable] = useState(false)
  const q = useQuery({queryKey: financeKeys.projection(ctx.mode, ctx.space, Number(months)), queryFn: () => getProjection(ctx, Number(months))})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const start = (accounts.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived).reduce((s, a) => s + a.balance, 0)
  const data = project(start, q.data?.data ?? [])
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
      {q.isLoading || accounts.isLoading ? <Skeleton className="h-40 w-full"/> : q.error || accounts.error ? (
        <ErrorBlock error={q.error ?? accounts.error} onRetry={() => { void q.refetch(); void accounts.refetch() }}/>
      ) : asTable ? <ProjectionTable data={data}/> : <ProjectionChart data={data}/>}
      <p className="text-xs text-muted-foreground">
        Saldo projetado: o saldo de hoje ({signedMoney(start)}) mais, a cada mês, o que vence a receber, menos o que vence a pagar, mais as recorrências ainda não geradas. Contas vencidas entram no primeiro mês.
      </p>
    </Block>
  )
}

function ProjectionTable({data}: {data: ProjectedMonth[]}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-max text-sm tabular-nums">
        <thead>
          <tr className="border-b border-border text-left text-muted-foreground">
            <th className="py-2 pr-4 font-normal">Mês</th>
            <th className="py-2 pl-4 text-right font-normal">A receber</th>
            <th className="py-2 pl-4 text-right font-normal">A pagar</th>
            <th className="py-2 pl-4 text-right font-normal">Recorrências ainda não geradas</th>
            <th className="py-2 pl-4 text-right font-normal">Resultado do mês</th>
            <th className="py-2 pl-4 text-right font-normal">Saldo projetado</th>
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
function ProjectionChart({data}: {data: ProjectedMonth[]}) {
  const W = 640, H = 200, TOP = 18, BOTTOM = 22
  const hi = Math.max(0, ...data.map(m => m.balance))
  const lo = Math.min(0, ...data.map(m => m.balance))
  const span = Math.max(1, hi - lo)
  const y = (v: number) => TOP + ((hi - v) / span) * (H - TOP - BOTTOM)
  const zero = y(0)
  const slot = W / Math.max(1, data.length)
  const bw = Math.min(36, slot * 0.5)
  return (
    <figure className="space-y-2">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`Saldo projetado ao fim de cada um dos próximos ${data.length} meses`} className="h-52 w-full">
        <line x1={0} x2={W} y1={zero} y2={zero} className="stroke-border" strokeWidth={1}/>
        {data.map((m, i) => {
          const x = i * slot + slot / 2 - bw / 2
          const top = Math.min(y(m.balance), zero), h = Math.abs(y(m.balance) - zero)
          return (
            <g key={m.month}>
              <title>{`${monthShort(m.month)}: saldo projetado ${signedMoney(m.balance)} (a receber ${money(m.receivable)}, a pagar ${money(m.payable)}, recorrências ${signedMoney(m.virtual)})`}</title>
              <rect x={x} y={top} width={bw} height={Math.max(h, 1)} rx={2} className={m.balance < 0 ? "fill-danger" : "fill-brand-600"}/>
              <text x={i * slot + slot / 2} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[11px]">{monthShort(m.month)}</text>
            </g>
          )
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-brand-600"/>Saldo projetado ao fim do mês</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-danger"/>No vermelho</span>
      </figcaption>
    </figure>
  )
}
