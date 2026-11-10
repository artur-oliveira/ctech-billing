"use client";

import {useQuery} from "@tanstack/react-query";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {BillAction, OccurrenceAction} from "@/components/finance/ActionInboxView";
import {quietLink} from "@/components/ui/quietLink";
import {Section} from "@/components/ui/Section";
import {financeKeys, listBills, listPendingOccurrences} from "@/lib/api/finance";
import {pendingToday} from "@/lib/finance/pending";
import {todayIso} from "@/lib/finance/today";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/** Rows shown before "Ver todas": the rest is one tap away in the inbox. */
const SHOWN = 3;
/** The inbox's page size: the same queries, so the two screens share one cache entry. */
const INBOX_LIMIT = 20;

/**
 * "Pendências de hoje": what is overdue or due today (bills, card statements,
 * recurring values to confirm), at the top of the overview. Drawn only when
 * something is pending, and silent while loading or on error: the blocks below
 * carry their own states, and an empty banner is noise. Each row keeps the
 * inbox's own primary action; the full list is the inbox.
 */
export function PendingToday() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const bill = (dir: "payable" | "receivable") => ({queryKey: [...financeKeys.billPreview(ctx.mode, ctx.space, dir), "inbox"], queryFn: () => listBills(ctx, dir, undefined, INBOX_LIMIT)});
  const payable = useQuery(bill("payable"));
  const receivable = useQuery(bill("receivable"));
  const pending = useQuery({queryKey: financeKeys.pendingOccurrences(ctx.mode, ctx.space), queryFn: () => listPendingOccurrences(ctx)});
  const items = pendingToday([...(payable.data?.data ?? []), ...(receivable.data?.data ?? [])], pending.data?.data ?? [], todayIso());
  if (items.length === 0) return null;
  const more = items.length > SHOWN || Boolean(payable.data?.has_more || receivable.data?.has_more);
  return (
    <Section title={t("finance.overview.pendingToday", {count: items.length})}
      action={more ? <Link href="/finance/inbox" className={quietLink}>{t("finance.overview.pendingAll")}</Link> : undefined}>
      <ul className="divide-y divide-border border-y border-border">
        {items.slice(0, SHOWN).map(i => i.kind === "bill" ? <BillAction key={i.key} bill={i.bill}/> : <OccurrenceAction key={i.key} item={i.item}/>)}
      </ul>
    </Section>
  );
}
