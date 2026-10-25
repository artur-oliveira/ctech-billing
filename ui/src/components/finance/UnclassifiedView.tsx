"use client";

import {Button, EmptyState, Field, PageHeader, Select, Skeleton, Checkbox} from "@aoctech/ui";
import {useQuery, useQueryClient} from "@tanstack/react-query";
import {ChevronLeft, CircleCheck} from "lucide-react";
import Link from "next/link";
import {useRef, useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {UnclassifiedBulk} from "@/components/finance/UnclassifiedBulk";
import {FormError} from "@/components/finance/FormError";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {ContainerSwitch} from "@/components/ui/ContainerSwitch";
import {classify, connectionKeys, listUnclassified} from "@/lib/api/connections";
import type {ClassifyBody, UnclassifiedLine} from "@/lib/api/connectionsTypes";
import {financeKeys, listAccounts, listBills} from "@/lib/api/finance";
import type {Account, Bill} from "@/lib/api/financeTypes";
import {accountName} from "@/lib/finance/accountName";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
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
  // A classified line leaves the list under the finger: focus goes to the count, not to <body>.
  const count = useRef<HTMLParagraphElement>(null);
  const focusCount = () => requestAnimationFrame(() => count.current?.focus());
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
          <p ref={count} tabIndex={-1} aria-live="polite" className="text-sm font-medium text-foreground outline-none">{t("connections.unclassified.count", {count: data.length})}</p>
          {canClassify && <UnclassifiedBulk lines={data} selected={selected} onSelected={setSelected} account={account} accounts={accounts.data?.data ?? []} onFinished={focusCount}/>}
          {/* The side-by-side row needs ~50rem of its own box: by the box, not the window (a sidebar takes a quarter of a tablet). */}
          <ContainerSwitch min={800}>
            {narrow => (
              <ul className="divide-y divide-border border-y border-border">
                {data.map(l => (
                  <LineRow key={l.tx_id} line={l} account={account} accounts={accounts.data?.data ?? []} canClassify={canClassify} wide={!narrow}
                    selected={selected.has(l.tx_id)} onToggle={() => toggle(l.tx_id)} onClassified={focusCount}/>
                ))}
              </ul>
            )}
          </ContainerSwitch>
        </>
      )}
    </div>
  );
}

function LineRow({line, account, accounts, canClassify, wide, selected, onToggle, onClassified}: {line: UnclassifiedLine; account: string; accounts: Account[]; canClassify: boolean; wide: boolean; selected: boolean; onToggle: () => void; onClassified: () => void}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const qc = useQueryClient();
  const credit = line.direction === "credit";
  const [kind, setKind] = useState<Kind>("category");
  const [picked, setTarget] = useState("");
  // A phone sees the line first and opens the choice on demand; a laptop always has it open.
  const [open, setOpen] = useState(false);
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

  // The one bill of the same amount close to the line is the likely answer: it is already picked.
  const target = picked || (kind === "bill" && options.length === 1 ? options[0].value : "");
  const save = useFinanceMutation(
    (c, body: ClassifyBody, key) => classify(c, line.tx_id, body, key),
    // The account's count and balances refresh.
    c => [financeKeys.all(c.mode, c.space)],
    () => {
      // The line leaves the list at once.
      qc.setQueryData<{data: UnclassifiedLine[]}>(connectionKeys.unclassified(ctx, account), cur => cur && {data: cur.data.filter(x => x.tx_id !== line.tx_id)});
      toast.success(t("connections.unclassified.done"));
      onClassified();
    },
  );
  const pickLabel = kind === "category" ? t("connections.unclassified.category") : kind === "bill" ? t("connections.unclassified.bill") : t("connections.unclassified.account");

  return (
    <li className={`grid gap-3 py-3${wide ? " grid-cols-[minmax(0,1fr)_auto] items-start" : ""}`}>
      <div className="flex min-w-0 items-start justify-between gap-3">
        {canClassify && (
          // The box stays small to the eye; under a finger its label is a 44px square.
          <label className="flex size-6 shrink-0 items-center justify-center touch:-m-2.5 touch:size-11">
            <Checkbox checked={selected} onCheckedChange={onToggle}
              aria-label={t("connections.unclassified.bulk.selectLine", {memo: line.memo || t(credit ? "connections.unclassified.in" : "connections.unclassified.out")})}/>
          </label>
        )}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-foreground">{line.memo || t(credit ? "connections.unclassified.in" : "connections.unclassified.out")}</p>
          <p className="text-xs text-muted-foreground">{shortDate(line.date)} • {t(credit ? "connections.unclassified.in" : "connections.unclassified.out")}</p>
        </div>
        <Amount tone={credit ? "success" : "foreground"} className="text-sm">{credit ? "+" : "−"}{money(line.amount)}</Amount>
      </div>
      {canClassify && (
        <div className={`space-y-2${wide ? " min-w-[34rem]" : ""}`}>
          <Button type="button" variant="outline" size="sm" aria-expanded={open} className={`w-full${wide || open ? " hidden" : ""}`} onClick={() => setOpen(true)}>
            {t("connections.unclassified.choose")}
          </Button>
          <form aria-label={line.memo || t(credit ? "connections.unclassified.in" : "connections.unclassified.out")}
            className={`${wide || open ? "grid" : "hidden"} gap-2${wide ? " grid-cols-[10rem_minmax(0,14rem)_auto] items-end" : ""}`}
            onSubmit={e => { e.preventDefault(); if (target && !save.isPending) save.mutate(kind === "category" ? {category_id: target} : kind === "bill" ? {bill_id: target} : {transfer_account_id: target}); }}>
          <Field label={t("connections.unclassified.kind")} htmlFor={`k-${line.tx_id}`}>
            <Select {...selectCopy()} id={`k-${line.tx_id}`} value={kind} onValueChange={v => { setKind(v as Kind); setTarget(""); }}
              options={(["category", "bill", "transfer"] as const).map(k => ({value: k, label: t(`connections.unclassified.${k}`)}))}/>
          </Field>
          <Field label={pickLabel} htmlFor={`t-${line.tx_id}`}>
            <Select {...selectCopy()} id={`t-${line.tx_id}`} value={target} onValueChange={setTarget} options={options}
              placeholder={kind === "bill" && options.length === 0 ? t("connections.unclassified.noBills") : undefined}/>
          </Field>
          <Button type="submit" variant="brand" size="sm" disabled={!target || save.isPending} className={wide ? "" : "w-full"}>{t("connections.unclassified.save")}</Button>
          <FormError error={save.error} className={wide ? "col-span-3" : ""}/>
          </form>
        </div>
      )}
    </li>
  );
}
