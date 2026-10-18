"use client";

import {Button, EmptyState, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {CircleCheck} from "lucide-react";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {LedgerRow} from "@/components/finance/LedgerRow";
import {PendingOccurrenceAction} from "@/components/finance/PendingOccurrenceAction";
import {BUCKET_STATUS, RECURRENCE_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {financeKeys, listBills, listImports, listPendingOccurrences, listRecurrences} from "@/lib/api/finance";
import type {Bill, ImportSummary} from "@/lib/api/financeTypes";
import {bucketLabel} from "@/lib/finance/labels";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {money, shortDate} from "@/lib/format";

const INBOX_LIMIT = 20;

export function ActionInboxView() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const payable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "payable"), "inbox"], queryFn: () => listBills(ctx, "payable", undefined, INBOX_LIMIT)});
  const receivable = useQuery({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, "receivable"), "inbox"], queryFn: () => listBills(ctx, "receivable", undefined, INBOX_LIMIT)});
  const recurrences = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)});
  const imports = useQuery({queryKey: financeKeys.imports(ctx.mode, ctx.space, ""), queryFn: () => listImports(ctx, "")});
  const pending = useQuery({queryKey: financeKeys.pendingOccurrences(ctx.mode, ctx.space), queryFn: () => listPendingOccurrences(ctx)});
  const error = payable.error ?? receivable.error ?? recurrences.error ?? imports.error ?? pending.error;
  const retry = () => { void payable.refetch(); void receivable.refetch(); void recurrences.refetch(); void imports.refetch(); void pending.refetch(); };
  const bills = [...(payable.data?.data ?? []), ...(receivable.data?.data ?? [])]
    .filter(bill => bill.status === "forecast")
    .sort((a, b) => (a.bucket === "overdue" ? 0 : 1) - (b.bucket === "overdue" ? 0 : 1) || a.due_date.localeCompare(b.due_date));
  // The agenda holds more than the inbox's page: say so instead of dropping them silently.
  const moreBills = Boolean(payable.data?.has_more || receivable.data?.has_more);
  const needsAttention = (recurrences.data?.data ?? []).filter(recurrence => recurrence.needs_attention && !recurrence.archived);
  const pendingImports = (imports.data?.data ?? []).filter(imp => imp.pending > 0);
  const pendingOccurrences = (pending.data?.data ?? []).slice().sort((a, b) => a.due.localeCompare(b.due));
  const loading = payable.isLoading || receivable.isLoading || recurrences.isLoading || imports.isLoading || pending.isLoading;
  const total = bills.length + pendingImports.length + pendingOccurrences.length + needsAttention.length;
  const list = "divide-y divide-border border-y border-border";

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.nav.inbox")}</h1>
        <p className="mt-1 text-sm text-muted-foreground" aria-live="polite">{loading || error ? t("finance.inbox.description") : total > 0 ? t("finance.inbox.total", {count: total}) : t("finance.inbox.description")}</p>
      </header>
      {error ? <ErrorBlock error={error} onRetry={retry}/> : null}
      {loading ? <div className="space-y-2" aria-busy><Skeleton className="h-5 w-40"/><Skeleton className="h-16 w-full"/><Skeleton className="h-16 w-full"/></div> : total === 0 ? (
        error ? null : <EmptyState icon={<CircleCheck aria-hidden/>} title={t("finance.inbox.allClear")} description={t("finance.inbox.allClearBody")}/>
      ) : (
        <div className="space-y-8">
          {/* Only what waits is drawn: a section that is empty is one line less to read. */}
          {pendingOccurrences.length > 0 && <Section title={t("finance.inbox.confirmations", {count: pendingOccurrences.length})}>
            <ul className={list}>
              {pendingOccurrences.map(item => (
                <LedgerRow key={`${item.recurrence_id}-${item.nominal}`} title={item.description || t("finance.inbox.unnamedRecurrence")}
                  aside={<StatusBadge status={RECURRENCE_STATUS.pending}>{t("finance.inbox.waitingValue")}</StatusBadge>}
                  amount={<Amount tone={item.direction === "receivable" ? "success" : "default"}>{money(item.expected_amount)}</Amount>}>
                  <PendingOccurrenceAction item={item}/>
                </LedgerRow>
              ))}
            </ul>
          </Section>}
          {bills.length > 0 && <Section title={t("finance.inbox.bills", {count: bills.length})}
            action={moreBills ? <Link href="/finance/bills" className="inline-flex items-center text-sm text-foreground underline underline-offset-4 touch-target">{t("finance.inbox.seeAll")}</Link> : undefined}>
            <ul className={list}>{bills.map(bill => <BillAction key={bill.id} bill={bill}/>)}</ul>
          </Section>}
          {pendingImports.length > 0 && <Section title={t("finance.inbox.imports", {count: pendingImports.length})}>
            <ul className={list}>{pendingImports.map(imp => <ImportAction key={imp.id} item={imp}/>)}</ul>
          </Section>}
          {needsAttention.length > 0 && <Section title={t("finance.inbox.recurrences", {count: needsAttention.length})}>
            <ul className={list}>
              {needsAttention.map(recurrence => (
                <LedgerRow key={recurrence.id} title={recurrence.description || t("finance.inbox.unnamedRecurrence")}
                  meta={t(`finance.inbox.attention.${recurrence.needs_attention}`)} amount={null}
                  actions={<Button size="sm" variant="outline" render={<Link href={`/finance/recurrences?recurrence=${encodeURIComponent(recurrence.id)}`}/>}>{t("finance.inbox.fix")}</Button>}/>
              ))}
            </ul>
          </Section>}
        </div>
      )}
    </div>
  );
}

function ImportAction({item}: {item: ImportSummary}) {
  const {t} = useTranslation();
  return (
    <LedgerRow title={t("finance.inbox.pendingImport", {count: item.pending})}
      meta={item.from && item.to ? `${shortDate(item.from)} – ${shortDate(item.to)}` : shortDate(item.created_at.slice(0, 10))} amount={null}
      actions={<Button size="sm" variant="outline" render={<Link href={`/finance/import?account=${encodeURIComponent(item.account_id)}`}/>}>{t("finance.inbox.reconcile")}</Button>}/>
  );
}

function BillAction({bill}: {bill: Bill}) {
  const {t} = useTranslation();
  const overdue = bill.bucket === "overdue";
  return (
    <LedgerRow title={bill.description || t("finance.inbox.unnamedBill")}
      meta={t(overdue ? "finance.inbox.overdue" : "finance.inbox.due", {date: shortDate(bill.due_date)})}
      aside={overdue ? <StatusBadge status={BUCKET_STATUS.overdue}>{bucketLabel("overdue")}</StatusBadge> : undefined}
      amount={<Amount tone={bill.direction === "receivable" ? "success" : "default"}>{money(bill.amount)}</Amount>}
      actions={<Button size="sm" variant="outline" render={<Link href={`/finance/bills?direction=${bill.direction}&bill=${encodeURIComponent(bill.id)}`}/>}>{t(bill.direction === "payable" ? "finance.inbox.openPayable" : "finance.inbox.openReceivable")}</Button>}/>
  );
}
