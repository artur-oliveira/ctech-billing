"use client";

import {Button, EmptyState, Select, Skeleton} from "@aoctech/ui";

import {LEDGER_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {PanelDrawer} from "@/components/finance/PanelDrawer";
import {useInfiniteQuery, useQuery} from "@tanstack/react-query";
import {Landmark} from "lucide-react";
import Link from "next/link";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {TransferPanel, wholeSpace} from "@/components/finance/TransferPanel";
import {Amount, AmountRow} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {FormErrorText} from "@/components/finance/FormError";
import {selectCopy} from "@/lib/selectCopy";
import {type FinanceCtx, financeKeys, getStatement, listAccounts, reverseTransaction, unsettleBill} from "@/lib/api/finance";
import type {Account, Statement, StatementEntry} from "@/lib/api/financeTypes";
import {dateRange, type PresetId, PRESETS} from "@/lib/finance/periods";
import {addDaysIso, todayIso} from "@/lib/finance/today";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {useCreateRequest} from "@/lib/createRequest";
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {shortDate, signedMoney} from "@/lib/format";
import {currentLocale, t as tr} from "@/lib/i18n";
import {accountName} from "@/lib/finance/accountName";

function memoOf(e: StatementEntry): string {
  if (e.memo) return e.memo;
  switch (e.kind) {
    case "settlement": return tr(e.amount > 0 ? "finance.statement.receipt" : "finance.statement.payment");
    case "transfer": return tr("finance.statement.transfer");
    case "opening_balance": return tr("finance.statement.opening");
    default: return tr("finance.statement.entry");
  }
}

/** The accounts a statement is about: the person's own, active first, by name. */
function cashAccounts(accounts: Account[]): Account[] {
  return accounts
    .filter(a => a.class === "asset" && !a.system)
    .sort((a, b) => Number(a.archived) - Number(b.archived) || accountName(a).localeCompare(accountName(b), currentLocale()));
}

/**
 * F3 — extrato. One account's movements in a period, between the balance it
 * started with and the one it ended with. The two facts a person posts here
 * directly (a transfer, an opening balance) are reversed here; a payment is
 * undone, which also puts its bill back in the open list.
 */
export function StatementView({account: initial = ""}: {account?: string}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can, loading} = useFinanceSpaces();
  const [picked, setPicked] = useState(initial);
  const [preset, setPreset] = useState<PresetId>("this_month");
  const [transferring, setTransferring] = useState(false);
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("transfer", () => { if (loading || can("finance.write")) setTransferring(true); });

  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const all = accounts.data?.data ?? [];
  const cash = cashAccounts(all);
  const accountId = cash.some(a => a.id === picked) ? picked : (cash[0]?.id ?? "");
  const {from, to} = dateRange(preset, todayIso());

  const statement = useInfiniteQuery({
    queryKey: financeKeys.statement(ctx.mode, ctx.space, accountId, from, to),
    queryFn: ({signal, pageParam}) => getStatement(ctx, accountId, from, to, signal, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
    enabled: accountId !== "",
  });
  const names = new Map(all.map(a => [a.id, accountName(a)]));

  if (accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>;
  }
  if (accounts.error) return <ErrorBlock error={accounts.error} onRetry={() => void accounts.refetch()}/>;
  if (cash.length === 0) {
    return (
      <EmptyState
        icon={<Landmark/>}
        title={t("finance.statement.noAccounts")}
        action={<Button variant="outline" size="sm" render={<Link href="/finance/accounts"/>}>{t("finance.statement.createAccount")}</Button>}
      />
    );
  }

  // Opening, closing and the totals are the period's on every page; only the lines are the page's.
  const pages = statement.data?.pages ?? [];
  const s = pages[0] && {...pages[0], entries: pages.flatMap(p => p.entries)};
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        {/* One row on a phone too (UX batch 4): the account takes what the
            period leaves, and the labels, which the chosen values already
            say ("Conta corrente", "Este mês"), are for a screen reader there. */}
        <div className="grid w-full grid-cols-[minmax(0,1fr)_minmax(0,8.75rem)] items-end gap-2 sm:flex sm:w-auto sm:gap-3">
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="st-account" className="text-sm font-medium text-foreground max-sm:sr-only">{t("finance.statement.account")}</label>
            <Select {...selectCopy()} id="st-account" aria-label={t("finance.statement.account")} value={accountId} onValueChange={setPicked} className="sm:w-56"
              options={cash.map(a => ({value: a.id, label: a.archived ? t("finance.statement.archivedName", {name: accountName(a)}) : accountName(a)}))}/>
          </div>
          <div className="flex min-w-0 flex-col gap-1.5">
            <label htmlFor="st-period" className="text-sm font-medium text-foreground max-sm:sr-only">{t("finance.statement.period")}</label>
            <Select {...selectCopy()} id="st-period" aria-label={t("finance.statement.period")} value={preset} onValueChange={v => setPreset(v as PresetId)} className="sm:w-48" options={PRESETS.map(p => ({value: p.value, label: t(`finance.presets.${p.value}`)}))}/>
          </div>
        </div>
        {can("finance.write") && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setTransferring(true)}>{t("finance.statement.newTransfer")}</Button>
        )}
      </div>

      <PanelDrawer open={transferring} allowed={can("finance.write")} onClose={() => setTransferring(false)} title={t("finance.statement.newTransfer")}>
        <TransferPanel accounts={all} from={accountId} onDone={() => setTransferring(false)}/>
      </PanelDrawer>

      {statement.isLoading || !s ? (
        statement.error ? (
          <ErrorBlock error={statement.error} onRetry={() => void statement.refetch()}/>
        ) : (
          <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
        )
      ) : (
        <>
          <Summary s={s} from={from} to={to}/>
          <section aria-label={t("finance.statement.entries")}>
            {s.entries.length === 0 ? (
              <EmptyState title={t("finance.statement.empty")} className="py-8"/>
            ) : (
              <ul className="divide-y divide-border border-y border-border">
                {s.entries.map(e => (
                  <EntryRow key={`${e.transaction_id}-${e.reversal}`} entry={e} category={e.category_id ? names.get(e.category_id) : undefined}/>
                ))}
              </ul>
            )}
            {statement.hasNextPage && (
              <Button variant="outline" block className="mt-3" onClick={() => void statement.fetchNextPage()} disabled={statement.isFetchingNextPage}>
                {statement.isFetchingNextPage ? t("common.loading") : t("finance.statement.loadMore")}
              </Button>
            )}
          </section>
        </>
      )}
    </div>
  );
}

/**
 * The period above its lines: where it started, what came in and went out, where
 * it ended. A reversed line and its reversal cancel, so neither counts. A period
 * that reaches past today closes "hoje": nothing is posted after it.
 */
function Summary({s, from, to}: {s: Statement; from: string; to: string}) {
  const {t} = useTranslation();
  const {money_in: moneyIn, money_out: moneyOut} = s;
  const last = addDaysIso(to, -1);
  return (
    <Section title={t("finance.statement.summary")} hideTitle>
      <dl className="divide-y divide-border border-y border-border text-sm">
        <AmountRow label={<span className="text-muted-foreground">{t("finance.statement.openingOn", {date: shortDate(from)})}</span>}><Amount>{signedMoney(s.opening)}</Amount></AmountRow>
        <AmountRow label={<span className="text-muted-foreground">{t("finance.statement.in")}</span>}><Amount>{signedMoney(moneyIn, true)}</Amount></AmountRow>
        <AmountRow label={<span className="text-muted-foreground">{t("finance.statement.out")}</span>}><Amount>{signedMoney(moneyOut === 0 ? 0 : -moneyOut)}</Amount></AmountRow>
        <AmountRow strong label={last >= todayIso() ? t("finance.statement.closingToday") : t("finance.statement.closingOn", {date: shortDate(last)})}>
          <Amount strong tone={s.closing < 0 ? "danger" : "foreground"}>{signedMoney(s.closing)}</Amount>
        </AmountRow>
      </dl>
    </Section>
  );
}

type Action = "reverse" | "unsettle" | null

function EntryRow({entry: e, category}: {entry: StatementEntry; category?: string}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [action, setAction] = useState<Action>(null);
  const live = !e.reversal && !e.reversed;
  const canReverse = live && (e.kind === "transfer" || e.kind === "opening_balance") && can("finance.write");
  const canUnsettle = live && e.kind === "settlement" && !!e.bill_id && can("finance.settle");
  const toggle = (a: Exclude<Action, null>) => () => setAction(action === a ? null : a);
  return (
    // A ledger line (UX batch 4): on a phone the memo wraps beside the amount,
    // the date and category under it, the running balance under the amount; a
    // reversal or an undo is a left swipe or "⋯", each asking first.
    <LedgerRow
      title={memoOf(e)}
      meta={<>{shortDate(e.date)}{category ? ` • ${category}` : ""}</>}
      aside={(e.reversal || e.reversed) && <>
        {e.reversal && <StatusBadge status={LEDGER_STATUS.reversal}>{t("finance.statement.reversal")}</StatusBadge>}
        {e.reversed && <StatusBadge status={LEDGER_STATUS.reversed}>{t("finance.statement.reversed")}</StatusBadge>}
      </>}
      amount={<Amount struck={e.reversed}>{signedMoney(e.amount, true)}</Amount>}
      balance={<Amount>{signedMoney(e.balance)}</Amount>}
      balanceLabel={t("finance.statement.balance")}
      more={[
        ...(canReverse ? [{key: "reverse", label: t("finance.statement.reverse"), destructive: true, expanded: action === "reverse", onSelect: toggle("reverse")}] : []),
        ...(canUnsettle ? [{key: "unsettle", label: t("finance.statement.undoPayment"), destructive: true, expanded: action === "unsettle", onSelect: toggle("unsettle")}] : []),
      ]}
    >
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
    </LedgerRow>
  );
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const {t} = useTranslation();
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone);
  return (
    <InlineConfirm text={text} cancelLabel={t("finance.statement.back")} confirmLabel={t("finance.statement.confirm")} tone="brand"
      pending={m.isPending} error={m.error ? <FormErrorText error={m.error}/> : null} onCancel={onDone} onConfirm={() => m.mutate()}/>
  );
}
