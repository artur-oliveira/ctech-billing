"use client";

import {useQueries, useQuery} from "@tanstack/react-query";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {CardBrandMark, cardLabel} from "@/components/finance/CardBrandMark";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {Amount} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {financeKeys, getCardStatement, listCards} from "@/lib/api/finance";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {money, shortDate} from "@/lib/format";

/**
 * Agenda's "Faturas provisórias": each active card's OPEN statement, what is
 * accumulating and not yet a bill. Informational (nothing to pay until it
 * closes), so it sits below the bills and is left out of their totals.
 *
 * The API has no endpoint with every card's open total, so this is one list
 * call plus one statement read per active card. Bounded by the space's card
 * count (a handful), and the reads share their cache keys with Cartões. A card
 * whose read fails is simply not listed: a missing estimate must not break the
 * Agenda.
 */
export function ProvisionalStatements() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)});
  const active = (cards.data?.data ?? []).filter(c => !c.archived && c.open_month);
  const statements = useQueries({
    queries: active.map(c => ({
      queryKey: financeKeys.cardStatement(ctx.mode, ctx.space, c.id, c.open_month),
      queryFn: () => getCardStatement(ctx, c.id, c.open_month),
    })),
  });
  const rows = active.flatMap((card, i) => {
    const s = statements[i]?.data;
    return s && s.status === "open" && s.total > 0 ? [{card, s}] : [];
  });
  if (rows.length === 0) return null;
  const total = rows.reduce((sum, r) => sum + r.s.total, 0);
  return (
    <Section title={t("bills.provisional.title")} action={<Amount tone="muted" className="text-sm">{money(total)}</Amount>}>
      <ul className="divide-y divide-border border-y border-border">
        {rows.map(({card, s}) => (
          <LedgerRow
            key={card.id}
            leading={<CardBrandMark brand={card.brand ?? "other"} className="text-muted-foreground"/>}
            title={cardLabel(card)}
            meta={<>
              {t("bills.provisional.meta", {date: shortDate(s.due_date)})} •{" "}
              <Link href={`/finance/cards?card=${encodeURIComponent(card.id)}&month=${s.month}`} className="underline-offset-4 hover:underline">{t("bills.row.viewStatement")}</Link>
            </>}
            amount={<Amount tone="muted">{money(s.total)}</Amount>}
          />
        ))}
      </ul>
    </Section>
  );
}
