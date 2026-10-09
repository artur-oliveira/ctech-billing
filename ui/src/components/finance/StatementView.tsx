"use client"

import {Badge, Button, EmptyState, Field, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {TransferPanel, wholeSpace} from "@/components/finance/TransferPanel"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {type FinanceCtx, financeKeys, getStatement, listAccounts, reverseTransaction, unsettleBill} from "@/lib/api/finance"
import type {Account, StatementEntry} from "@/lib/api/financeTypes"
import {dateRange, type PresetId, PRESETS} from "@/lib/finance/periods"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"

/** The day before a civil date, without reading it as UTC. */
function dayBefore(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number)
  const t = new Date(y, m - 1, d - 1, 12)
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, "0")}-${String(t.getDate()).padStart(2, "0")}`
}

/** "−R$ 300,00" for money out (a real minus sign), "R$ 300,00" for money in. */
const signed = (cents: number) => (cents < 0 ? `−${money(-cents)}` : money(cents))

function memoOf(e: StatementEntry): string {
  if (e.memo) return e.memo
  switch (e.kind) {
    case "settlement": return e.amount > 0 ? "Recebimento" : "Pagamento"
    case "transfer": return "Transferência"
    case "opening_balance": return "Saldo inicial"
    default: return "Lançamento"
  }
}

/** The accounts a statement is about: the person's own, active first, by name. */
function cashAccounts(accounts: Account[]): Account[] {
  return accounts
    .filter(a => a.class === "asset" && !a.system)
    .sort((a, b) => Number(a.archived) - Number(b.archived) || a.name.localeCompare(b.name, "pt-BR"))
}

/**
 * F3 — extrato. One account's movements in a period, between the balance it
 * started with and the one it ended with. The two facts a person posts here
 * directly (a transfer, an opening balance) are reversed here; a payment is
 * undone, which also puts its bill back in the open list.
 */
export function StatementView({account: initial = ""}: {account?: string}) {
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [picked, setPicked] = useState(initial)
  const [preset, setPreset] = useState<PresetId>("this_month")
  const [transferring, setTransferring] = useState(false)

  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const all = accounts.data?.data ?? []
  const cash = cashAccounts(all)
  const accountId = cash.some(a => a.id === picked) ? picked : (cash[0]?.id ?? "")
  const {from, to} = dateRange(preset, todayIso())

  const statement = useQuery({
    queryKey: financeKeys.statement(ctx.mode, ctx.space, accountId, from, to),
    queryFn: ({signal}) => getStatement(ctx, accountId, from, to, signal),
    enabled: accountId !== "",
  })
  const names = new Map(all.map(a => [a.id, a.name]))

  if (accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
  }
  if (accounts.error) return <ErrorBlock error={accounts.error} onRetry={() => void accounts.refetch()}/>
  if (cash.length === 0) {
    return (
      <EmptyState
        icon={<Landmark/>}
        title="Nenhuma conta ainda"
        description="Crie uma conta em Contas para ver o extrato."
        action={<Button variant="outline" size="sm" render={<Link href="/console/finance/accounts"/>}>Ir para Contas</Button>}
      />
    )
  }

  const s = statement.data
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Conta" htmlFor="st-account">
            <Select id="st-account" aria-label="Conta" value={accountId} onValueChange={setPicked} className="w-56"
              options={cash.map(a => ({value: a.id, label: a.archived ? `${a.name} (arquivada)` : a.name}))}/>
          </Field>
          <Field label="Período" htmlFor="st-period">
            <Select id="st-period" aria-label="Período" value={preset} onValueChange={v => setPreset(v as PresetId)} className="w-48" options={PRESETS}/>
          </Field>
        </div>
        {can("finance.write") && !transferring && (
          <Button variant="brand" size="sm" onClick={() => setTransferring(true)}>Nova transferência</Button>
        )}
      </div>

      {transferring && <TransferPanel accounts={all} from={accountId} onDone={() => setTransferring(false)}/>}

      {statement.isLoading || !s ? (
        statement.error ? (
          <ErrorBlock error={statement.error} onRetry={() => void statement.refetch()}/>
        ) : (
          <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
        )
      ) : (
        <section aria-label="Lançamentos" className="space-y-2">
          <BalanceLine label={`Saldo em ${shortDate(from)}`} amount={s.opening}/>
          {s.entries.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Nenhum lançamento neste período.</p>
          ) : (
            <ul className="divide-y divide-border border-y border-border">
              {s.entries.map(e => (
                <EntryRow key={`${e.transaction_id}-${e.reversal}`} entry={e} category={e.category_id ? names.get(e.category_id) : undefined}/>
              ))}
            </ul>
          )}
          <BalanceLine label={`Saldo em ${shortDate(dayBefore(to))}`} amount={s.closing}/>
        </section>
      )}
    </div>
  )
}

function BalanceLine({label, amount}: {label: string; amount: number}) {
  return (
    <div className="flex items-baseline justify-between text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span data-numeric className="font-medium tabular-nums text-foreground">{signed(amount)}</span>
    </div>
  )
}

type Action = "reverse" | "unsettle" | null

function EntryRow({entry: e, category}: {entry: StatementEntry; category?: string}) {
  const {can} = useFinanceSpaces()
  const [action, setAction] = useState<Action>(null)
  const live = !e.reversal && !e.reversed
  const canReverse = live && (e.kind === "transfer" || e.kind === "opening_balance") && can("finance.write")
  const canUnsettle = live && e.kind === "settlement" && !!e.bill_id && can("finance.settle")
  return (
    <li className="py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-foreground">{memoOf(e)}</p>
          <p className="text-xs text-muted-foreground">{shortDate(e.date)}{category ? ` · ${category}` : ""}</p>
        </div>
        {e.reversal && <Badge tone="neutral">Estorno</Badge>}
        {e.reversed && <Badge tone="neutral">Estornado</Badge>}
        <span data-numeric className={`w-28 text-right text-sm tabular-nums ${e.reversed ? "text-muted-foreground line-through" : "text-foreground"}`}>
          {signed(e.amount)}
        </span>
        <span data-numeric className="w-28 text-right text-sm tabular-nums text-muted-foreground">{signed(e.balance)}</span>
        <div className="flex gap-1">
          {canReverse && (
            <Button size="sm" variant="ghost" aria-expanded={action === "reverse"} onClick={() => setAction(action === "reverse" ? null : "reverse")}>Estornar</Button>
          )}
          {canUnsettle && (
            <Button size="sm" variant="ghost" aria-expanded={action === "unsettle"} onClick={() => setAction(action === "unsettle" ? null : "unsettle")}>Desfazer pagamento</Button>
          )}
        </div>
      </div>
      {action === "reverse" && canReverse && (
        <Confirm
          text="Estornar este lançamento? O saldo volta ao que era."
          run={(c, key) => reverseTransaction(c, e.transaction_id, key)}
          onDone={() => setAction(null)}
        />
      )}
      {action === "unsettle" && canUnsettle && (
        <Confirm
          text="A conta volta para A pagar e a receber e o pagamento automático fica desligado."
          run={(c, key) => unsettleBill(c, e.bill_id!, key)}
          onDone={() => setAction(null)}
        />
      )}
    </li>
  )
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">{text}</p>
      <Button size="sm" variant="outline" onClick={onDone}>Voltar</Button>
      <Button size="sm" variant="brand" disabled={m.isPending} onClick={() => m.mutate()}>Confirmar</Button>
      {m.error ? <p role="alert" className="w-full text-sm text-danger">{messageFor(m.error)}</p> : null}
    </div>
  )
}
