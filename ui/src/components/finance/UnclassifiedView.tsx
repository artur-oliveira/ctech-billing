"use client";

import {Button, Checkbox, EmptyState, Field, PageHeader, Select, Skeleton} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {ChevronLeft, CircleCheck} from "lucide-react";
import Link from "next/link";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {UnclassifiedBulk} from "@/components/finance/UnclassifiedBulk";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {classify, connectionKeys, listUnclassified} from "@/lib/api/connections";
import type {UnclassifiedLine} from "@/lib/api/connectionsTypes";
import {messageFor, problemCode} from "@/lib/api/client";
import {financeKeys, listAccounts, listBills} from "@/lib/api/finance";
import type {Account, Bill} from "@/lib/api/financeTypes";
import {useIntent} from "@/lib/api/idempotency";
import {codeMessage} from "@/lib/errors";
import {accountName} from "@/lib/finance/accountName";
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {money, shortDate} from "@/lib/format";
import {selectCopy} from "@/lib/selectCopy";

type Kind = "category" | "bill" | "transfer"

/** Days between two ISO dates (absolute). */
function daysApart(a: string, b: string): number {
  return Math.abs(Date.parse(`${a}T12:00:00Z`) - Date.parse(`${b}T12:00:00Z`)) / 86_400_000;
}

/**
 * "A classificar" (BYOK spec § 8.3): a bank-synced account's lines still on
 * the two system categories. Each is classified into a category, an open bill
 * of the same amount (due within 5 days), or a transfer to another account.
 * The bank account's balance never moves; a classified line leaves the list.
 */
export function UnclassifiedView({account}: {account: string}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can} = useFinanceSpaces();
  const lines = useQuery({queryKey: connectionKeys.unclassified(ctx, account), queryFn: () => listUnclassified(ctx, account), enabled: account !== ""});
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const acct = accounts.data?.data.find(a => a.id === account);
  const data = lines.data?.data ?? [];
  const canClassify = can("finance.import");
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const toggle = (id: string) => setSelected(cur => { const next = new Set(cur); if (!next.delete(id)) next.add(id); return next; });

  return (
    <div className="space-y-6">
      <PageHeader
        lead={<Link href="/finance/accounts" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground touch:min-h-11"><ChevronLeft aria-hidden className="size-4"/>{t("finance.nav.accounts")}</Link>}
        title={t("connections.unclassified.title")}
        description={acct ? accountName(acct) : undefined}
      />
      {lines.isPending || accounts.isPending ? (
        <div className="space-y-2" aria-busy><Skeleton className="h-5 w-40"/><Skeleton className="h-16 w-full"/><Skeleton className="h-16 w-full"/></div>
      ) : lines.isError ? (
        <ErrorBlock error={lines.error} onRetry={() => void lines.refetch()}/>
      ) : data.length === 0 ? (
        <EmptyState icon={<CircleCheck aria-hidden/>} title={t("connections.unclassified.empty")} description={t("connections.unclassified.emptyBody")}/>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">{t("connections.unclassified.count", {count: data.length})}</p>
          {canClassify && <UnclassifiedBulk lines={data} selected={selected} onSelected={setSelected} account={account} accounts={accounts.data?.data ?? []}/>}
          <ul className="divide-y divide-border border-y border-border">
            {data.map(l => (
              <LineRow key={l.tx_id} line={l} account={account} accounts={accounts.data?.data ?? []} canClassify={canClassify}
                selected={selected.has(l.tx_id)} onToggle={() => toggle(l.tx_id)}/>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function LineRow({line, account, accounts, canClassify, selected, onToggle}: {line: UnclassifiedLine; account: string; accounts: Account[]; canClassify: boolean; selected: boolean; onToggle: () => void}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const qc = useQueryClient();
  const intent = useIntent();
  const credit = line.direction === "credit";
  const [kind, setKind] = useState<Kind>("category");
  const [target, setTarget] = useState("");
  const bills = useQuery({
    queryKey: [...financeKeys.all(ctx.mode, ctx.space), "bills", credit ? "receivable" : "payable", "open"],
    queryFn: () => listBills(ctx, credit ? "receivable" : "payable"),
    enabled: kind === "bill",
  });
  const categories = accounts.filter(a => a.class === (credit ? "income" : "expense") && !a.archived && !a.system);
  const transfers = accounts.filter(a => a.class === "asset" && !a.archived && !a.system && a.id !== account && a.source !== "byok");
  const matching = (bills.data?.data ?? []).filter((b: Bill) => b.status === "forecast" && b.amount === line.amount && daysApart(b.due_date, line.date) <= 5);
  const options = kind === "category" ? categories.map(a => ({value: a.id, label: accountName(a)}))
    : kind === "transfer" ? transfers.map(a => ({value: a.id, label: accountName(a)}))
      : matching.map(b => ({value: b.id, label: `${b.description || t("connections.unclassified.bill")} • ${shortDate(b.due_date)}`}));

  const save = useMutation({
    mutationFn: () => classify(ctx, line.tx_id,
      kind === "category" ? {category_id: target} : kind === "bill" ? {bill_id: target} : {transfer_account_id: target}, intent.key()),
    onSuccess: () => {
      intent.done();
      // The line leaves the list at once; the account's count and balances refresh.
      qc.setQueryData<{data: UnclassifiedLine[]}>(connectionKeys.unclassified(ctx, account), cur => cur && {data: cur.data.filter(x => x.tx_id !== line.tx_id)});
      void qc.invalidateQueries({queryKey: financeKeys.all(ctx.mode, ctx.space)});
      toast.success(t("connections.unclassified.done"));
    },
  });
  const code = problemCode(save.error);
  const pickLabel = kind === "category" ? t("connections.unclassified.category") : kind === "bill" ? t("connections.unclassified.bill") : t("connections.unclassified.account");

  return (
    <li className="grid gap-3 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
      <div className="flex min-w-0 items-start justify-between gap-3">
        {canClassify && (
          <Checkbox checked={selected} onCheckedChange={onToggle} className="mt-0.5 shrink-0"
            aria-label={t("connections.unclassified.bulk.selectLine", {memo: line.memo || t(credit ? "connections.unclassified.in" : "connections.unclassified.out")})}/>
        )}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-foreground">{line.memo || t(credit ? "connections.unclassified.in" : "connections.unclassified.out")}</p>
          <p className="text-xs text-muted-foreground">{shortDate(line.date)} • {t(credit ? "connections.unclassified.in" : "connections.unclassified.out")}</p>
        </div>
        <span data-numeric className={credit ? "text-sm text-success" : "text-sm text-foreground"}>{credit ? "+" : "−"}{money(line.amount)}</span>
      </div>
      {canClassify && (
        <form className="grid gap-2 sm:grid-cols-[10rem_minmax(0,14rem)_auto] sm:items-end md:min-w-[34rem]"
          onSubmit={e => { e.preventDefault(); if (target) save.mutate(); }}>
          <Field label={t("connections.unclassified.kind")} htmlFor={`k-${line.tx_id}`}>
            <Select {...selectCopy()} id={`k-${line.tx_id}`} value={kind} onValueChange={v => { setKind(v as Kind); setTarget(""); }}
              options={(["category", "bill", "transfer"] as const).map(k => ({value: k, label: t(`connections.unclassified.${k}`)}))}/>
          </Field>
          <Field label={pickLabel} htmlFor={`t-${line.tx_id}`}>
            <Select {...selectCopy()} id={`t-${line.tx_id}`} value={target} onValueChange={setTarget} options={options}
              placeholder={kind === "bill" && options.length === 0 ? t("connections.unclassified.noBills") : undefined}/>
          </Field>
          <Button type="submit" variant="brand" size="sm" disabled={!target || save.isPending} className="max-sm:w-full">{t("connections.unclassified.save")}</Button>
          {save.isError && <p role="alert" className="text-sm text-danger sm:col-span-3">{code ? codeMessage(code) : messageFor(save.error)}</p>}
        </form>
      )}
    </li>
  );
}
