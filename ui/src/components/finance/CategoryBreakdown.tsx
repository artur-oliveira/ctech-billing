"use client";

import {EmptyState, Skeleton} from "@aoctech/ui";
import {PieChart} from "lucide-react";
import {useTranslation} from "react-i18next";

import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {ContainerSwitch} from "@/components/ui/ContainerSwitch";
import type {CategoryAmounts, CategoryMonth} from "@/lib/api/financeTypes";
import {type BreakdownSide, booked, isEmptyMonth, netOf, share, sidesOf} from "@/lib/finance/categoryBreakdown";
import {money, signedMoney} from "@/lib/format";

/** What the table needs, in px: `min-w-[48rem]` below. */
const TABLE_MIN = 768;

interface CategoryBreakdownProps {
  /** The month by category; undefined while loading or after an error. */
  data?: CategoryMonth
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  /** A category id's label ("" is none); the screen owns the accounts it reads from. */
  nameOf: (categoryId: string) => string
}

/**
 * One month by category, income and expense apart: what is realised, what is
 * scheduled (open bills) and what is forecast (recurrences not yet made), with
 * each category's share of its side. A table from `sm` up, a list on a phone.
 * The share is a number as well as a bar, and the forecast part of the bar is
 * hatched, so neither share nor firmness depends on colour.
 */
export function CategoryBreakdown(props: CategoryBreakdownProps) {
  // The table needs TABLE_MIN px and the container, not the window, says whether it has them.
  return <ContainerSwitch min={TABLE_MIN}>{narrow => <Breakdown {...props} narrow={narrow}/>}</ContainerSwitch>;
}

function Breakdown({data, loading, error, onRetry, nameOf, narrow}: CategoryBreakdownProps & {narrow: boolean}) {
  const {t} = useTranslation();
  if (loading) return <div aria-busy className="space-y-2"><Skeleton className="h-5 w-40"/><Skeleton className="h-24 w-full"/></div>;
  if (error || !data) return <ErrorBlock error={error} onRetry={onRetry}/>;
  if (isEmptyMonth(data)) return <EmptyState icon={<PieChart/>} title={t("finance.categoryMonth.empty")}/>;
  const net = netOf(data);
  return (
    <div className="space-y-6">
      {sidesOf(data).filter(s => s.rows.length > 0).map(side => narrow
        ? <SideList key={side.key} side={side} nameOf={nameOf}/>
        : <SideTable key={side.key} side={side} nameOf={nameOf}/>)}
      <dl className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-t border-border pt-3 text-sm font-medium">
        <dt>{t("finance.categoryMonth.net")}</dt>
        <dd><Amount>{signedMoney(net.total)}</Amount></dd>
        <dd className="w-full text-xs font-normal text-muted-foreground">
          {t("finance.categoryMonth.netParts", {booked: signedMoney(booked(net)), forecast: signedMoney(net.forecast)})}
        </dd>
      </dl>
      <p className="text-xs text-muted-foreground">{t("finance.categoryMonth.legend")}</p>
    </div>
  );
}

/** The share bar: booked part solid, forecast part hatched; the percentage beside it says the same in numbers. */
function ShareBar({row, side}: {row: CategoryAmounts; side: CategoryAmounts}) {
  const {t} = useTranslation();
  const bookedPct = share(booked(row), side.total) * 100;
  const forecastPct = Math.min(100 - bookedPct, share(row.forecast, side.total) * 100);
  const pct = Math.round(share(row.total, side.total) * 100);
  return (
    <span className="flex items-center gap-2" title={t("finance.categoryMonth.share", {pct})}>
      <span aria-hidden className="flex h-2 w-full min-w-16 overflow-hidden rounded-full bg-border/60">
        <span className="h-full bg-foreground/70" style={{width: `${bookedPct}%`}}/>
        <span className="h-full border-l border-dashed border-foreground/60 bg-[repeating-linear-gradient(135deg,transparent_0_2px,currentColor_2px_3px)] text-foreground/50" style={{width: `${forecastPct}%`}}/>
      </span>
      <span data-numeric className="w-10 shrink-0 text-right text-xs tabular-nums text-muted-foreground">{pct}%</span>
    </span>
  );
}

function SideTable({side, nameOf}: {side: BreakdownSide; nameOf: (id: string) => string}) {
  const {t} = useTranslation();
  const th = "py-2 pl-4 text-right font-normal";
  const td = "py-2 pl-4 text-right align-middle tabular-nums";
  const cell = (n: number) => n === 0 ? "—" : money(n);
  return (
    <section aria-labelledby={`cm-${side.key}`}>
      <h3 id={`cm-${side.key}`} className="mb-1 text-sm font-medium text-foreground">{t(`finance.categoryMonth.${side.key}`)}</h3>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[48rem] table-fixed text-sm">
          {/* One set of widths for every side, so Receitas and Despesas line up. */}
          <colgroup><col/><col className="w-28"/><col className="w-28"/><col className="w-28"/><col className="w-28"/><col className="w-40"/></colgroup>
          <thead>
            <tr className="border-b border-border text-xs text-muted-foreground">
              <th scope="col" className="py-2 text-left font-normal">{t("finance.categoryMonth.category")}</th>
              <th scope="col" className={th}>{t("finance.categoryMonth.realised")}</th>
              <th scope="col" className={th}>{t("finance.categoryMonth.scheduled")}</th>
              <th scope="col" className={th}>{t("finance.categoryMonth.forecast")}</th>
              <th scope="col" className={th}>{t("finance.categoryMonth.total")}</th>
              <th scope="col" className={th}>{t("finance.categoryMonth.shareHead")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {side.rows.map(r => (
              <tr key={r.category_id ?? ""}>
                <th scope="row" className="py-2 text-left font-normal text-foreground">{nameOf(r.category_id ?? "")}</th>
                <td data-numeric className={td}>{cell(r.realised)}</td>
                <td data-numeric className={td}>{cell(r.scheduled)}</td>
                <td data-numeric className={td}>{cell(r.forecast)}</td>
                <td data-numeric className={`${td} font-medium`}>{money(r.total)}</td>
                <td className="py-2 pl-4"><ShareBar row={r} side={side.totals}/></td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="border-t border-border font-medium">
              <th scope="row" className="py-2 text-left font-medium">{t("finance.categoryMonth.total")}</th>
              <td data-numeric className={td}>{cell(side.totals.realised)}</td>
              <td data-numeric className={td}>{cell(side.totals.scheduled)}</td>
              <td data-numeric className={td}>{cell(side.totals.forecast)}</td>
              <td data-numeric className={td}>{money(side.totals.total)}</td>
              <td/>
            </tr>
          </tfoot>
        </table>
      </div>
    </section>
  );
}

function SideList({side, nameOf}: {side: BreakdownSide; nameOf: (id: string) => string}) {
  const {t} = useTranslation();
  return (
    <section aria-labelledby={`cm-${side.key}`}>
      <div className="mb-1 flex items-baseline justify-between gap-3">
        <h3 id={`cm-${side.key}`} className="text-sm font-medium text-foreground">{t(`finance.categoryMonth.${side.key}`)}</h3>
        <Amount strong className="text-sm">{money(side.totals.total)}</Amount>
      </div>
      <ul className="divide-y divide-border border-y border-border">
        {side.rows.map(r => {
          // Only the parts that exist: "realizado R$ 300,00 · previsto R$ 200,00".
          const parts = ([["realised", r.realised], ["scheduled", r.scheduled], ["forecast", r.forecast]] as const)
            .filter(([, v]) => v !== 0)
            .map(([k, v]) => `${t(`finance.categoryMonth.${k}Lower`)} ${money(v)}`);
          return (
            <li key={r.category_id ?? ""} className="space-y-1 py-2">
              <div className="flex items-baseline justify-between gap-3">
                <span className="min-w-0 truncate text-sm text-foreground">{nameOf(r.category_id ?? "")}</span>
                <Amount strong className="shrink-0 text-sm">{money(r.total)}</Amount>
              </div>
              <ShareBar row={r} side={side.totals}/>
              {parts.length > 1 && <p className="text-xs text-muted-foreground">{parts.join(" · ")}</p>}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
