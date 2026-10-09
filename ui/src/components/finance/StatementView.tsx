"use client"

import {Badge, Button, EmptyState, Field, Skeleton} from "@aoctech/ui"
import {Drawer} from "@/components/ui/ConsoleOverlay"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {TransferPanel, wholeSpace} from "@/components/finance/TransferPanel"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {type FinanceCtx, financeKeys, getStatement, listAccounts, reverseTransaction, unsettleBill} from "@/lib/api/finance"
import type {Account, StatementEntry} from "@/lib/api/financeTypes"
import {dateRange, type PresetId, PRESETS} from "@/lib/finance/periods"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useCreateRequest} from "@/lib/finance/createRequest"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {shortDate, signedMoney} from "@/lib/format"
import {currentLocale, t as tr} from "@/lib/i18n"
import {accountName} from "@/lib/finance/accountName"

/** The day before a civil date, without reading it as UTC. */
function dayBefore(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number)
  const t = new Date(y, m - 1, d - 1, 12)
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, "0")}-${String(t.getDate()).padStart(2, "0")}`
}

function memoOf(e: StatementEntry): string {
  if (e.memo) return e.memo
  switch (e.kind) {
    case "settlement": return tr(e.amount > 0 ? "finance.statement.receipt" : "finance.statement.payment")
    case "transfer": return tr("finance.statement.transfer")
    case "opening_balance": return tr("finance.statement.opening")
    default: return tr("finance.statement.entry")
  }
}

/** The accounts a statement is about: the person's own, active first, by name. */
function cashAccounts(accounts: Account[]): Account[] {
  return accounts
    .filter(a => a.class === "asset" && !a.system)
    .sort((a, b) => Number(a.archived) - Number(b.archived) || accountName(a).localeCompare(accountName(b), currentLocale()))
}

/**
 * F3 — extrato. One account's movements in a period, between the balance it
 * started with and the one it ended with. The two facts a person posts here
 * directly (a transfer, an opening balance) are reversed here; a payment is
 * undone, which also puts its bill back in the open list.
 */
export function StatementView({account: initial = ""}: {account?: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can, loading} = useFinanceSpaces()
  const [picked, setPicked] = useState(initial)
  const [preset, setPreset] = useState<PresetId>("this_month")
  const [transferring, setTransferring] = useState(false)
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("transfer", () => { if (loading || can("finance.write")) setTransferring(true) })

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
  const names = new Map(all.map(a => [a.id, accountName(a)]))

  if (accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
  }
  if (accounts.error) return <ErrorBlock error={accounts.error} onRetry={() => void accounts.refetch()}/>
  if (cash.length === 0) {
    return (
      <EmptyState
        icon={<Landmark/>}
        title={t("finance.statement.noAccounts")}
        action={<Button variant="outline" size="sm" render={<Link href="/console/finance/accounts"/>}>{t("finance.statement.createAccount")}</Button>}
      />
    )
  }

  const s = statement.data
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <Field label={t("finance.statement.account")} htmlFor="st-account">
            <Select id="st-account" aria-label={t("finance.statement.account")} value={accountId} onValueChange={setPicked} className="w-56"
              options={cash.map(a => ({value: a.id, label: a.archived ? t("finance.statement.archivedName", {name: accountName(a)}) : accountName(a)}))}/>
          </Field>
          <Field label={t("finance.statement.period")} htmlFor="st-period">
            <Select id="st-period" aria-label={t("finance.statement.period")} value={preset} onValueChange={v => setPreset(v as PresetId)} className="w-48" options={PRESETS.map(p => ({value: p.value, label: t(`finance.presets.${p.value}`)}))}/>
          </Field>
        </div>
        {can("finance.write") && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setTransferring(true)}>{t("finance.statement.newTransfer")}</Button>
        )}
      </div>

      <Drawer open={transferring && can("finance.write")} onClose={() => setTransferring(false)} title={t("finance.statement.newTransfer")}>
        <TransferPanel accounts={all} from={accountId} onDone={() => setTransferring(false)}/>
      </Drawer>

      {statement.isLoading || !s ? (
        statement.error ? (
          <ErrorBlock error={statement.error} onRetry={() => void statement.refetch()}/>
        ) : (
          <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
        )
      ) : (
        <section aria-label={t("finance.statement.entries")} className="space-y-2">
          <BalanceLine label={t("finance.statement.balanceOn", {date: shortDate(from)})} amount={s.opening}/>
          {s.entries.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">{t("finance.statement.empty")}</p>
          ) : (
            <ul className="divide-y divide-border border-y border-border">
              {s.entries.map(e => (
                <EntryRow key={`${e.transaction_id}-${e.reversal}`} entry={e} category={e.category_id ? names.get(e.category_id) : undefined}/>
              ))}
            </ul>
          )}
          <BalanceLine label={t("finance.statement.balanceOn", {date: shortDate(dayBefore(to))})} amount={s.closing}/>
        </section>
      )}
    </div>
  )
}

function BalanceLine({label, amount}: {label: string; amount: number}) {
  return (
    <div className="flex items-baseline justify-between text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span data-numeric className="font-medium tabular-nums text-foreground">{signedMoney(amount)}</span>
    </div>
  )
}

type Action = "reverse" | "unsettle" | null

function EntryRow({entry: e, category}: {entry: StatementEntry; category?: string}) {
  const {t} = useTranslation()
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
        {e.reversal && <Badge tone="neutral">{t("finance.statement.reversal")}</Badge>}
        {e.reversed && <Badge tone="neutral">{t("finance.statement.reversed")}</Badge>}
        <span data-numeric className={`w-28 text-right text-sm tabular-nums ${e.reversed ? "text-muted-foreground line-through" : "text-foreground"}`}>
          {signedMoney(e.amount)}
        </span>
        <span data-numeric className="w-28 text-right text-sm tabular-nums text-muted-foreground">{signedMoney(e.balance)}</span>
        <div className="flex gap-1">
          {canReverse && (
            <Button size="sm" variant="ghost" aria-expanded={action === "reverse"} onClick={() => setAction(action === "reverse" ? null : "reverse")}>{t("finance.statement.reverse")}</Button>
          )}
          {canUnsettle && (
            <Button size="sm" variant="ghost" aria-expanded={action === "unsettle"} onClick={() => setAction(action === "unsettle" ? null : "unsettle")}>{t("finance.statement.undoPayment")}</Button>
          )}
        </div>
      </div>
      {action === "reverse" && canReverse && (
        <Confirm
          text={t("finance.statement.reverseConfirm")}
          run={(c, key) => reverseTransaction(c, e.transaction_id, key)}
          onDone={() => setAction(null)}
        />
      )}
      {action === "unsettle" && canUnsettle && (
        <Confirm
          text={t("finance.statement.undoConfirm")}
          run={(c, key) => unsettleBill(c, e.bill_id!, key)}
          onDone={() => setAction(null)}
        />
      )}
    </li>
  )
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const {t} = useTranslation()
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">{text}</p>
      <Button size="sm" variant="outline" onClick={onDone}>{t("finance.statement.back")}</Button>
      <Button size="sm" variant="brand" disabled={m.isPending} onClick={() => m.mutate()}>{t("finance.statement.confirm")}</Button>
      {m.error ? <p role="alert" className="w-full text-sm text-danger">{messageFor(m.error)}</p> : null}
    </div>
  )
}
