"use client"

import {Button, Field, Input, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {confirmExpectedOccurrence, financeKeys, listBills, listImports, listPendingOccurrences, listRecurrences, skipExpectedOccurrence, type FinanceCtx} from "@/lib/api/finance"
import type {Bill, ImportSummary, PendingOccurrence} from "@/lib/api/financeTypes"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {formatMoneyInput, maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"
import {money, shortDate} from "@/lib/format"

const INBOX_LIMIT = 20

export function ActionInboxView() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const payable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "payable"), "inbox"], queryFn: () => listBills(ctx, "payable", undefined, INBOX_LIMIT)})
  const receivable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "receivable"), "inbox"], queryFn: () => listBills(ctx, "receivable", undefined, INBOX_LIMIT)})
  const recurrences = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)})
  const imports = useQuery({queryKey: financeKeys.imports(ctx.mode, ctx.space, ""), queryFn: () => listImports(ctx, "")})
  const pending = useQuery({queryKey: financeKeys.pendingOccurrences(ctx.mode, ctx.space), queryFn: () => listPendingOccurrences(ctx)})
  const error = payable.error ?? receivable.error ?? recurrences.error ?? imports.error ?? pending.error
  const retry = () => { void payable.refetch(); void receivable.refetch(); void recurrences.refetch(); void imports.refetch(); void pending.refetch() }
  const bills = [...(payable.data?.data ?? []), ...(receivable.data?.data ?? [])]
    .filter(bill => bill.status === "forecast")
    .sort((a, b) => (a.bucket === "overdue" ? 0 : 1) - (b.bucket === "overdue" ? 0 : 1) || a.due_date.localeCompare(b.due_date))
  const needsAttention = (recurrences.data?.data ?? []).filter(recurrence => recurrence.needs_attention && !recurrence.archived)
  const pendingImports = (imports.data?.data ?? []).filter(imp => imp.pending > 0)
  const pendingOccurrences = (pending.data?.data ?? []).slice().sort((a,b) => a.due.localeCompare(b.due))
  const loading = payable.isLoading || receivable.isLoading || recurrences.isLoading || imports.isLoading || pending.isLoading

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.nav.inbox")}</h1>
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
          <section aria-labelledby="inbox-confirmations" className="space-y-3">
            <h2 id="inbox-confirmations" className="text-sm font-medium">{t("finance.inbox.confirmations", {count: pendingOccurrences.length})}</h2>
            {pendingOccurrences.length ? <ul className="divide-y divide-border border-y border-border">
              {pendingOccurrences.map(item => <PendingAction key={`${item.recurrence_id}-${item.nominal}`} item={item}/>)}
            </ul> : <p className="text-sm text-muted-foreground">{t("finance.inbox.noConfirmations")}</p>}
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
    </div>
  )
}

function PendingAction({item}: {item: PendingOccurrence}) {
  const {t} = useTranslation()
  const [amountText, setAmountText] = useState(formatMoneyInput(item.expected_amount))
  const [askingSkip, setAskingSkip] = useState(false)
  const amount = parseMoney(amountText)
  const invalidate = (c: FinanceCtx) => [financeKeys.all(c.mode,c.space)]
  const confirm = useFinanceMutation((c, value: number, key) => confirmExpectedOccurrence(c,item.recurrence_id,item.nominal,value,key), invalidate)
  const skip = useFinanceMutation((c, _: void, key) => skipExpectedOccurrence(c,item.recurrence_id,item.nominal,key), invalidate, () => setAskingSkip(false))
  return <li className="flex flex-wrap items-end justify-between gap-3 py-3">
    <div className="min-w-0"><p className="truncate text-sm font-medium">{item.description || t("finance.inbox.unnamedRecurrence")}</p><p className="text-sm text-muted-foreground">{t("finance.inbox.expectedOccurrence", {date:shortDate(item.due),amount:money(item.expected_amount)})}</p></div>
    <div className="flex flex-wrap items-end gap-2">
      <Field label={t("finance.inbox.actualAmount")} htmlFor={`pending-${item.recurrence_id}-${item.nominal}`}>
        <Input id={`pending-${item.recurrence_id}-${item.nominal}`} inputMode="decimal" placeholder={moneyPlaceholder()} value={amountText} onChange={event=>setAmountText(maskMoney(event.target.value))} className="w-32"/>
      </Field>
      <Button size="sm" variant="brand" disabled={amount === null || amount <= 0 || confirm.isPending} onClick={()=>amount !== null && confirm.mutate(amount)}>{t("finance.inbox.confirmAmount")}</Button>
      {!askingSkip ? <Button size="sm" variant="ghost" onClick={()=>setAskingSkip(true)}>{t("finance.inbox.skip")}</Button> : <>
        <span className="text-xs text-muted-foreground">{t("finance.inbox.skipConfirm")}</span>
        <Button size="sm" variant="danger" disabled={skip.isPending} onClick={()=>skip.mutate()}>{t("finance.inbox.skipYes")}</Button>
        <Button size="sm" variant="ghost" onClick={()=>setAskingSkip(false)}>{t("finance.inbox.skipNo")}</Button>
      </>}
      {(confirm.error || skip.error) && <p role="alert" className="w-full text-sm text-danger">{t("finance.inbox.actionFailed")}</p>}
    </div>
  </li>
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
