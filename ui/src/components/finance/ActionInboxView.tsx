"use client"

import {Button, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {financeKeys, listBills, listImports, listRecurrences} from "@/lib/api/finance"
import type {Bill, ImportSummary} from "@/lib/api/financeTypes"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"

const INBOX_LIMIT = 20

export function ActionInboxView() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const payable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "payable"), "inbox"], queryFn: () => listBills(ctx, "payable", undefined, INBOX_LIMIT)})
  const receivable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "receivable"), "inbox"], queryFn: () => listBills(ctx, "receivable", undefined, INBOX_LIMIT)})
  const recurrences = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)})
  const imports = useQuery({queryKey: financeKeys.imports(ctx.mode, ctx.space, ""), queryFn: () => listImports(ctx, "")})
  const error = payable.error ?? receivable.error ?? recurrences.error ?? imports.error
  const retry = () => { void payable.refetch(); void receivable.refetch(); void recurrences.refetch(); void imports.refetch() }
  const bills = [...(payable.data?.data ?? []), ...(receivable.data?.data ?? [])]
    .filter(bill => bill.status === "forecast")
    .sort((a, b) => (a.bucket === "overdue" ? 0 : 1) - (b.bucket === "overdue" ? 0 : 1) || a.due_date.localeCompare(b.due_date))
  const needsAttention = (recurrences.data?.data ?? []).filter(recurrence => recurrence.needs_attention && !recurrence.archived)
  const pendingImports = (imports.data?.data ?? []).filter(imp => imp.pending > 0)
  const loading = payable.isLoading || receivable.isLoading || recurrences.isLoading || imports.isLoading

  return (
    <main className="space-y-6">
      <header>
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.inbox.title")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("finance.inbox.description")}</p>
      </header>
      {error ? <ErrorBlock error={error} onRetry={retry}/> : null}
      {loading ? <Skeleton className="h-24 w-full"/> : (
        <div className="space-y-8">
          <section aria-labelledby="inbox-bills" className="space-y-3">
            <h2 id="inbox-bills" className="text-sm font-medium">{t("finance.inbox.bills", {count: bills.length})}</h2>
            {bills.length ? <ul className="divide-y divide-border border-y border-border">
              {bills.map(bill => <BillAction key={bill.id} bill={bill}/>) }
            </ul> : <p className="text-sm text-muted-foreground">{t("finance.inbox.noBills")}</p>}
          </section>
          <section aria-labelledby="inbox-imports" className="space-y-3">
            <h2 id="inbox-imports" className="text-sm font-medium">{t("finance.inbox.imports", {count: pendingImports.length})}</h2>
            {pendingImports.length ? <ul className="divide-y divide-border border-y border-border">
              {pendingImports.map(imp => <ImportAction key={imp.id} item={imp}/>) }
            </ul> : <p className="text-sm text-muted-foreground">{t("finance.inbox.noImports")}</p>}
          </section>
          <section aria-labelledby="inbox-recurrences" className="space-y-3">
            <h2 id="inbox-recurrences" className="text-sm font-medium">{t("finance.inbox.recurrences", {count: needsAttention.length})}</h2>
            {needsAttention.length ? <ul className="divide-y divide-border border-y border-border">
              {needsAttention.map(recurrence => <li key={recurrence.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                <div><p className="text-sm font-medium">{recurrence.description || t("finance.inbox.unnamedRecurrence")}</p><p className="text-sm text-muted-foreground">{t(`finance.inbox.attention.${recurrence.needs_attention}`)}</p></div>
                <Button size="sm" variant="outline" render={<Link href={`/finance/recurrences?recurrence=${encodeURIComponent(recurrence.id)}`}/>}>{t("finance.inbox.fix")}</Button>
              </li>)}
            </ul> : <p className="text-sm text-muted-foreground">{t("finance.inbox.noRecurrences")}</p>}
          </section>
        </div>
      )}
    </main>
  )
}

function ImportAction({item}: {item: ImportSummary}) {
  const {t} = useTranslation()
  return <li className="flex flex-wrap items-center justify-between gap-3 py-3">
    <div><p className="text-sm font-medium">{t("finance.inbox.pendingImport", {count: item.pending})}</p><p className="text-sm text-muted-foreground">{item.from && item.to ? `${shortDate(item.from)} – ${shortDate(item.to)}` : shortDate(item.created_at.slice(0, 10))}</p></div>
    <Button size="sm" variant="outline" render={<Link href={`/finance/import?account=${encodeURIComponent(item.account_id)}`}/>}>{t("finance.inbox.reconcile")}</Button>
  </li>
}

function BillAction({bill}: {bill: Bill}) {
  const {t} = useTranslation()
  const overdue = bill.bucket === "overdue"
  return <li className="flex flex-wrap items-center justify-between gap-3 py-3">
    <div className="min-w-0"><p className="truncate text-sm font-medium">{bill.description || t("finance.inbox.unnamedBill")}</p><p className={`text-sm ${overdue ? "text-danger" : "text-muted-foreground"}`}>{t(overdue ? "finance.inbox.overdue" : "finance.inbox.due", {date: shortDate(bill.due_date)})}</p></div>
    <div className="flex items-center gap-3"><span className="text-sm tabular-nums">{money(bill.amount)}</span><Button size="sm" variant="outline" render={<Link href={`/finance/bills?direction=${bill.direction}&bill=${encodeURIComponent(bill.id)}`}/>}>{t(bill.direction === "payable" ? "finance.inbox.openPayable" : "finance.inbox.openReceivable")}</Button></div>
  </li>
}
