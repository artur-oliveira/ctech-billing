"use client";

import {Button, EmptyState, Segmented, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {BarChart3, ChevronRight, Landmark, List, Table2} from "lucide-react";
import Link from "next/link";
import type {TFunction} from "i18next";
import {Fragment, useRef, useState} from "react";
import {useTranslation} from "react-i18next";

import {cardLabel} from "@/components/finance/CardBrandMark";
import {CategoryMonthPanel} from "@/components/finance/CategoryMonthPanel";
import {PendingToday} from "@/components/finance/PendingToday";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount, AmountRow} from "@/components/ui/Amount";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {ContainerSwitch} from "@/components/ui/ContainerSwitch";
import {quietLink} from "@/components/ui/quietLink";
import {Section} from "@/components/ui/Section";
import {SelectStepper} from "@/components/ui/SelectStepper";
import {financeKeys, getCashFlow, listAccounts, listBills, listCards} from "@/lib/api/finance";
import type {Bill} from "@/lib/api/financeTypes";
import {useCardBudget} from "@/lib/finance/cardBudget";
import {bucketLabel, directionLabel} from "@/lib/finance/labels";
import {currentLocale} from "@/lib/i18n";
import {aggregate, HORIZONS, maxBars, PROJECTION_HORIZON, type ProjectedPeriod, project, useProjection} from "@/lib/finance/projection";
import {monthLabel, monthShort, todayIso} from "@/lib/finance/today";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {useElementWidth} from "@/lib/hooks/useElementWidth";
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery";
import {money, shortDate, signedMoney} from "@/lib/format";
import {formatMoneyInput, parseMoney} from "@/lib/money";
import {accountName} from "@/lib/finance/accountName";

/** "1a 2a" scale: under a year in months, from a year in years (60 months is the API's reach). */
const horizonLabels = (months: number, t: TFunction) => months < 12
  ? t("finance.overview.horizonM", {count: months})
  : t("finance.overview.horizonY", {count: months / 12});

/** One bar's or row's month, or the span it folds ("Nov/26 – Jan/27"). */
const periodLabel = (m: ProjectedPeriod) => (m.count === 1 ? monthShort(m.month) : `${monthShort(m.month)} – ${monthShort(m.last)}`);

/**
 * F1 — the finance overview: three independent blocks separated by rules. Each
 * fetches and fails on its own, so one slow or broken request never blanks the
 * page.
 */
export function OverviewView() {
  const {t} = useTranslation();
  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.overview.title")}</h1>
      <PendingToday/>
      <Future/>
      <Lower/>
      <div className="grid items-start gap-8 xl:grid-cols-3">
        <Balances/>
        <Realised/>
        <DueSoon/>
      </div>
    </div>
  );
}

/** The accounts' summed balance: what the person has today. */
function useStart() {
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const start = (q.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived).reduce((s, a) => s + a.balance, 0);
  return {q, start};
}

/**
 * "Quanto vou ter?" first: the balance the forecast leaves at the end of this
 * month and at the end of the window, with today's balance beside them so the
 * present is one glance away, not gone. The page's one large figure.
 */
function Future() {
  const {t} = useTranslation();
  const proj = useProjection(PROJECTION_HORIZON);
  const {q: accounts, start} = useStart();
  const data = project(start, proj.data?.data ?? []);
  const first = data[0], last = data[data.length - 1];
  const tone = (cents: number) => (cents < 0 ? "text-danger" : "text-foreground");
  return (
    <Section title={t("finance.overview.future")}>
      {proj.isLoading || accounts.isLoading ? <Skeleton className="h-16 w-full"/> : proj.error || accounts.error || !first || !last ? (
        <ErrorBlock error={proj.error ?? accounts.error} onRetry={() => { void proj.refetch(); void accounts.refetch(); }}/>
      ) : (
        <div className="flex flex-wrap items-end justify-between gap-x-8 gap-y-3">
          <div>
            <p data-numeric className={`text-3xl font-semibold tabular-nums tracking-[-0.02em] ${tone(first.balance)}`}>{signedMoney(first.balance)}</p>
            <p className="text-sm text-muted-foreground">{t("finance.overview.futureEnd", {month: monthLabel(`${first.month}-01`)})}</p>
          </div>
          <dl className="flex gap-x-8 text-sm">
            <div>
              <dt className="text-muted-foreground">{t("finance.overview.today")}</dt>
              <dd data-numeric className="tabular-nums">{signedMoney(start)}</dd>
            </div>
            {data.length > 1 && (
              <div>
                <dt className="text-muted-foreground">{t("finance.overview.futureLater", {month: monthShort(last.month)})}</dt>
                <dd data-numeric className={`tabular-nums ${tone(last.balance)}`}>{signedMoney(last.balance)}</dd>
              </div>
            )}
          </dl>
        </div>
      )}
    </Section>
  );
}

type LowerTab = "projection" | "category"

/**
 * The block under the three: the projection (what will I have?) or this month by
 * category, actual and forecast. A Segmented, not routes: both read the same
 * month window and the choice is a view of it, not a place.
 */
function Lower() {
  const {t} = useTranslation();
  const [tab, setTab] = useState<LowerTab>("projection");
  const month = todayIso().slice(0, 7);
  const narrow = useMediaQuery(NARROW);
  return (
    <div className="space-y-4">
      <Segmented
        label={t("finance.overview.lowerLabel")}
        value={tab}
        onValueChange={setTab}
        fill={narrow}
        options={[
          {value: "projection", label: t("finance.overview.projection")},
          {value: "category", label: t("finance.categoryMonth.title")},
        ]}
      />
      {tab === "projection" ? <Projection/> : (
        <Section title={t("finance.categoryMonth.title")} hideTitle action={<Link href="/finance/reports?view=category" className={quietLink}>{t("finance.overview.seeReports")}</Link>}>
          <p className="text-sm text-muted-foreground">{t("finance.overview.categoryNote", {month: monthLabel(`${month}-01`)})}</p>
          <CategoryMonthPanel month={month}/>
        </Section>
      )}
    </div>
  );
}

function Balances() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const assets = (q.data?.data ?? []).filter(a => a.class === "asset" && !a.system && !a.archived);
  const total = assets.reduce((s, a) => s + a.balance, 0);
  return (
    <Section title={t("finance.overview.balances")}>
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : assets.length === 0 ? (
        <EmptyState icon={<Landmark/>} title={t("finance.overview.noAccounts")}
          action={<Button variant="outline" size="sm" render={<Link href="/finance/accounts"/>}>{t("finance.overview.createAccount")}</Button>}/>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {assets.map(a => (
            <AmountRow as="li" key={a.id} label={<span className="text-foreground">{accountName(a)}</span>}><Amount>{money(a.balance)}</Amount></AmountRow>
          ))}
          <AmountRow as="li" strong label={t("finance.overview.total")}><Amount>{money(total)}</Amount></AmountRow>
        </ul>
      )}
    </Section>
  );
}

/**
 * What actually came in and went out of the person's accounts this month: the
 * cash result. The accrual one (by competence) is the DRE, one click away.
 */
function Realised() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const month = todayIso().slice(0, 7);
  const q = useQuery({queryKey: financeKeys.cashFlow(ctx.mode, ctx.space, month, month), queryFn: () => getCashFlow(ctx, month, month)});
  const m = q.data?.months[0] ?? {in: 0, out: 0};
  return (
    <Section title={t("finance.overview.monthResult")} action={<Link href="/finance/reports?view=cash" className={quietLink}>{t("finance.overview.seeReports")}</Link>}>
      {q.isLoading ? <Skeleton className="h-20 w-full"/> : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <dl className="divide-y divide-border border-y border-border text-sm">
          <AmountRow label={t("finance.overview.in")}><Amount>{money(m.in)}</Amount></AmountRow>
          <AmountRow label={t("finance.overview.out")}><Amount>{money(m.out)}</Amount></AmountRow>
          <AmountRow strong label={t("finance.overview.result")}><Amount>{signedMoney(m.in - m.out)}</Amount></AmountRow>
        </dl>
      )}
    </Section>
  );
}

const DUE_SOON = 5;

function DueSoon() {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const pay = useQuery({queryKey: financeKeys.billPreview(ctx.mode, ctx.space, "payable"), queryFn: () => listBills(ctx, "payable", undefined, DUE_SOON)});
  const rec = useQuery({queryKey: financeKeys.billPreview(ctx.mode, ctx.space, "receivable"), queryFn: () => listBills(ctx, "receivable", undefined, DUE_SOON)});
  const order = {overdue: 0, today: 1, upcoming: 2};
  const items: Bill[] = [...(pay.data?.data ?? []), ...(rec.data?.data ?? [])]
    .sort((a, b) => order[a.bucket ?? "upcoming"] - order[b.bucket ?? "upcoming"] || a.due_date.localeCompare(b.due_date))
    .slice(0, DUE_SOON);
  const error = pay.error ?? rec.error;
  return (
    <Section title={t("finance.overview.dueSoon")} action={<Link href="/finance/bills" className={quietLink}>{t("finance.overview.seeAll")}</Link>}>
      {pay.isLoading || rec.isLoading ? <Skeleton className="h-20 w-full"/> : error ? (
        <ErrorBlock error={error} onRetry={() => { void pay.refetch(); void rec.refetch(); }}/>
      ) : items.length === 0 ? (
        <EmptyState title={t("finance.overview.nothingOpen")} className="py-8"/>
      ) : (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {items.map(b => (
            <li key={b.id} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="truncate text-foreground">{b.description || t("finance.overview.noDescription")}</p>
                <p className="text-xs text-muted-foreground">
                  {directionLabel(b.direction)} • {bucketLabel(b.bucket ?? "upcoming")} • {shortDate(b.due_date)}
                </p>
              </div>
              <Amount tone={b.bucket === "overdue" ? "danger" : "default"} className="shrink-0">{money(b.amount)}</Amount>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

type View = "balance" | "flow"
type Display = "chart" | "rows"

/**
 * The projection, in the Saldo or the Entradas e saídas view, drawn as a chart
 * or written out as rows. The view and the display are independent, so the
 * rows always say what the chart would have: the same mode, in numbers.
 *
 * Rows are a table from `sm` up and a month list on a phone, where six columns
 * do not fit and a 12-month chart's labels would collide. A phone opens on the
 * list and a laptop on the chart; either can switch, so the two can be
 * compared on the same screen.
 */
function Projection() {
  const {t} = useTranslation();
  const narrow = useMediaQuery(NARROW);
  const [months, setMonths] = useState(String(PROJECTION_HORIZON));
  const [chosen, setDisplay] = useState<Display | null>(null);
  const display: Display = chosen ?? (narrow ? "rows" : "chart");
  const [view, setView] = useState<View>("balance");
  const q = useProjection(Number(months));
  const {q: accounts, start} = useStart();
  const [budget, setBudget] = useCardBudget();
  const name = useCardNames();
  const all = project(start, q.data?.data ?? [], budget);
  const hasCards = (q.data?.data ?? []).some(m => (m.by_card?.length ?? 0) > 0);
  // Rows keep up to a year's months; the chart is capped by the screen (12 bars, 6 on a phone).
  const rows = aggregate(all, 12), data = aggregate(all, maxBars(narrow));
  return (
    <Section title={t("finance.overview.projection")} hideTitle>
      <div className="flex flex-wrap items-center gap-2">
        <Segmented
          label={t("finance.overview.viewLabel")}
          value={view}
          onValueChange={setView}
          fill={narrow}
          options={[
            {value: "balance", label: t("finance.overview.viewBalance")},
            {value: "flow", label: t("finance.overview.viewFlow")},
          ]}
        />
        <SelectStepper
          label={t("finance.overview.window")}
          prevLabel={t("finance.overview.horizonPrev")}
          nextLabel={t("finance.overview.horizonNext")}
          value={months}
          onValueChange={setMonths}
          className="w-full sm:ml-auto sm:w-auto"
          options={HORIZONS.map(h => ({value: String(h), label: horizonLabels(h, t)}))}
        />
        <Segmented
          label={t("finance.overview.display")}
          value={display}
          onValueChange={setDisplay}
          className="ml-auto sm:ml-0"
          options={[
            {value: "chart", label: <BarChart3 aria-hidden className="size-4"/>, name: t("finance.overview.asChart")},
            {value: "rows", label: narrow ? <List aria-hidden className="size-4"/> : <Table2 aria-hidden className="size-4"/>, name: t(narrow ? "finance.overview.asList" : "finance.overview.asTable")},
          ]}
        />
      </div>
      {q.isLoading || accounts.isLoading ? <Skeleton className="h-40 w-full"/> : q.error || accounts.error ? (
        <ErrorBlock error={q.error ?? accounts.error} onRetry={() => { void q.refetch(); void accounts.refetch(); }}/>
      ) : display === "rows" ? (
        <ContainerSwitch min={640}>{small => small ? <MonthList data={rows} view={view} name={name}/> : <ProjectionTable data={rows} view={view} name={name}/>}</ContainerSwitch>
      ) : view === "flow" ? <FlowChart data={data}/> : <ProjectionChart data={data}/>}
      <p className="text-xs text-muted-foreground">
        {t("finance.overview.projectionNote", {balance: signedMoney(start)})}
      </p>
      {all.some(m => m.estimate > 0) && <p className="text-xs text-muted-foreground">{t("finance.overview.estimateNote")}</p>}
      {(hasCards || budget !== null) && <CardBudgetControl budget={budget} onChange={setBudget}/>}
    </Section>
  );
}

/** Card id to its label, from the cards the Cartões screen already reads (same cache entry). */
function useCardNames() {
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)});
  return (id: string) => {
    const c = q.data?.data.find(x => x.id === id);
    return c ? cardLabel(c) : id;
  };
}

const hasCardDetail = (m: ProjectedPeriod) => m.cards.length > 0 && (m.card > 0 || m.estimate > 0);

/** The month's month label, a button that opens the card detail when there is any. */
function MonthToggle({m, open, onToggle, className = ""}: {m: ProjectedPeriod; open: boolean; onToggle: () => void; className?: string}) {
  const {t} = useTranslation();
  if (!hasCardDetail(m)) return <span className={className}>{periodLabel(m)}</span>;
  return (
    <button type="button" aria-expanded={open} onClick={onToggle} title={t(open ? "finance.overview.hideCards" : "finance.overview.showCards", {month: periodLabel(m)})}
      className={`inline-flex min-h-8 items-center gap-1 rounded-sm text-left focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50 ${className}`}>
      <ChevronRight aria-hidden className={`size-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}/>
      {periodLabel(m)}
      <span className="sr-only">{t(open ? "finance.overview.hideCards" : "finance.overview.showCards", {month: periodLabel(m)})}</span>
    </button>
  );
}

/** Where a month's card money goes, card by card, then the estimate that no statement holds yet. */
function CardDetail({m, name}: {m: ProjectedPeriod; name: (id: string) => string}) {
  const {t} = useTranslation();
  const cards = m.cards.filter(c => c.posted + c.expected > 0);
  return (
    <dl data-card-detail className="space-y-1.5 text-sm tabular-nums">
      {cards.map(c => (
        <div key={c.card_id} className="flex items-baseline justify-between gap-3">
          <dt>{name(c.card_id)}<span className="block text-xs text-muted-foreground">{t("finance.overview.detailSplit", {posted: money(c.posted), expected: money(c.expected)})}</span></dt>
          <dd>{money(c.posted + c.expected)}</dd>
        </div>
      ))}
      {m.estimate > 0 && (
        <div className="flex items-baseline justify-between gap-3 border-t border-dashed border-border pt-1.5">
          <dt>{t("finance.overview.detailEstimate")}<span className="block text-xs text-muted-foreground">{t("finance.overview.detailEstimateHint")}</span></dt>
          <dd>{money(m.estimate)}</dd>
        </div>
      )}
    </dl>
  );
}

/**
 * An opt-in forecast of one-off card spending. The projection holds only what is
 * registered (statements, installments, recurrences) and never guesses; a person
 * who wants to see the effect of buying beyond that can type a monthly figure for
 * all their cards. It counts for what the statements do not hold of it yet, lives
 * in this browser, and is one click to remove.
 */
function CardBudgetControl({budget, onChange}: {budget: number | null; onChange: (cents: number | null) => void}) {
  const {t} = useTranslation();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState("");
  const cents = parseMoney(text);
  const apply = () => {
    if (cents === null || cents < 0) return;
    onChange(cents);
    setEditing(false);
  };
  if (!editing) {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span>{budget === null ? t("finance.overview.budgetNone") : t("finance.overview.budgetMine", {amount: money(budget)})}</span>
        <Button type="button" variant="outline" size="sm" onClick={() => { setText(budget === null ? "" : formatMoneyInput(budget)); setEditing(true); }}>
          {t(budget === null ? "finance.overview.budgetSet" : "finance.overview.budgetChange")}
        </Button>
        {budget !== null && <Button type="button" variant="ghost" size="sm" onClick={() => onChange(null)}>{t("finance.overview.budgetReset")}</Button>}
      </div>
    );
  }
  return (
    <form className="space-y-2" onSubmit={e => { e.preventDefault(); apply(); }}>
      <label htmlFor="card-budget" className="block text-sm">{t("finance.overview.budgetField")}</label>
      <div className="flex flex-wrap items-center gap-2">
        <MoneyInput id="card-budget" value={text} onValueChange={setText} aria-invalid={cents === null || undefined} className="w-40" autoFocus onFocus={e => e.currentTarget.select()}/>
        <Button type="submit" variant="brand" size="sm" disabled={cents === null}>{t("finance.overview.budgetApply")}</Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => setEditing(false)}>{t("finance.overview.budgetCancel")}</Button>
      </div>
      <p className="text-xs text-muted-foreground">{t("finance.overview.budgetHint")}</p>
    </form>
  );
}

/** "R$ 1.000,00 de recorrências" under an amount that includes them; nothing when there are none. */
function OfRecurrences({cents}: {cents: number}) {
  const {t} = useTranslation();
  if (cents <= 0) return null;
  return <span className="block text-xs text-muted-foreground">{t("finance.overview.ofRecurrences", {amount: money(cents)})}</span>;
}

/** What part of an outflow is on a card, and what part of it is only an estimate; nothing when neither. */
function OfCard({card, estimate}: {card: number; estimate: number}) {
  const {t} = useTranslation();
  return (
    <>
      {card > 0 && <span className="block text-xs text-muted-foreground">{t("finance.overview.ofCard", {amount: money(card)})}</span>}
      {estimate > 0 && <span className="block text-xs text-muted-foreground">{t("finance.overview.ofEstimate", {amount: money(estimate)})}</span>}
    </>
  );
}

function ProjectionTable({data, view, name}: {data: ProjectedPeriod[]; view: View; name: (id: string) => string}) {
  const {t} = useTranslation();
  const [open, setOpen] = useState<string | null>(null);
  const th = "py-2 pl-4 text-right font-normal";
  const td = "py-2 pl-4 text-right align-top";
  const estimates = data.some(m => m.estimate > 0);
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-max text-sm tabular-nums">
        <thead>
          <tr className="border-b border-border text-left text-muted-foreground">
            <th className="py-2 pr-4 font-normal">{t("finance.overview.month")}</th>
            {view === "balance" ? (
              <>
                <th className={th}>{directionLabel("receivable")}</th>
                <th className={th}>{directionLabel("payable")}</th>
                <th className={th}>{t("finance.overview.recurrencesNet")}</th>
                {estimates && <th className={th}>{t("finance.overview.cardEstimate")}</th>}
                <th className={th}>{t("finance.overview.monthResult")}</th>
                <th className={th}>{t("finance.overview.projected")}</th>
              </>
            ) : (
              <>
                <th className={th}>{t("finance.overview.legendIn")}</th>
                <th className={th}>{t("finance.overview.legendOut")}</th>
                <th className={th}>{t("finance.overview.monthResult")}</th>
                <th className={th}>{t("finance.overview.projected")}</th>
              </>
            )}
          </tr>
        </thead>
        <tbody>
          {data.map(m => (
            <Fragment key={m.month}>
            <tr className={open === m.month ? "" : "border-b border-border"}>
              <th scope="row" className="py-2 pr-4 text-left align-top font-normal"><MonthToggle m={m} open={open === m.month} onToggle={() => setOpen(open === m.month ? null : m.month)}/></th>
              {view === "balance" ? (
                <>
                  <td className={td}>{money(m.receivable)}</td>
                  <td className={td}>{money(m.payable)}</td>
                  <td className={td}>{signedMoney(m.virtual)}</td>
                  {estimates && <td className={td}>{m.estimate > 0 ? signedMoney(-m.estimate) : money(0)}</td>}
                  <td className={td}>{signedMoney(m.result)}</td>
                  <td className={`${td} font-medium ${m.balance < 0 ? "text-danger" : ""}`}>{signedMoney(m.balance)}</td>
                </>
              ) : (
                <>
                  <td className={td}>{money(m.inflow)}<OfRecurrences cents={m.recIn}/></td>
                  <td className={td}>{money(m.outflow)}<OfRecurrences cents={m.recOut}/><OfCard card={m.card} estimate={m.estimate}/></td>
                  <td className={`${td} ${m.result < 0 ? "text-danger" : ""}`}>{signedMoney(m.result)}</td>
                  {/* The accumulated balance stays in both views: the view
                      changes how a month is broken down, not whether the
                      projection says where the money ends up. */}
                  <td className={`${td} font-medium ${m.balance < 0 ? "text-danger" : ""}`}>{signedMoney(m.balance)}</td>
                </>
              )}
            </tr>
            {open === m.month && hasCardDetail(m) && (
              <tr className="border-b border-border"><td colSpan={8} className="pb-3 pl-5 pr-1"><CardDetail m={m} name={name}/></td></tr>
            )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * The projection on a phone: one row per month, the month's headline figure
 * (the balance it ends on, or its result in the Entradas e saídas view) and a
 * pair of thin bars for what comes in and goes out, on one scale across all
 * the months so a heavy month reads as one. The bars keep the chart's green in,
 * red out, so switching to the chart changes the form and not the language.
 * They are decoration, hidden from a screen reader: the figures beside them
 * carry the numbers, in ink, and each says in words how much of it is
 * recurrences, as the table does.
 */
function MonthList({data, view, name}: {data: ProjectedPeriod[]; view: View; name: (id: string) => string}) {
  const {t} = useTranslation();
  const [open, setOpen] = useState<string | null>(null);
  const max = Math.max(1, ...data.map(m => Math.max(m.inflow, m.outflow)));
  const pct = (cents: number) => `${(cents / max) * 100}%`;
  return (
    <ol aria-label={t("finance.overview.monthList")} className="divide-y divide-border border-y border-border">
      {data.map(m => {
        const value = view === "balance" ? m.balance : m.result;
        return (
          <li key={m.month} className="space-y-1.5 py-2.5">
            <div className="flex items-baseline justify-between gap-3">
              <span data-month className="text-sm text-foreground"><MonthToggle m={m} open={open === m.month} onToggle={() => setOpen(open === m.month ? null : m.month)}/></span>
              <span data-value data-numeric className={`text-sm font-medium tabular-nums ${value < 0 ? "text-danger" : "text-foreground"}`}>{signedMoney(value)}</span>
            </div>
            {view === "flow" && (
              // The headline is the month's result here; the balance it ends on
              // is still the projection's point, so it stays, one line down.
              <p data-balance data-numeric className="flex justify-between gap-3 text-xs tabular-nums text-muted-foreground">
                <span>{t("finance.overview.projected")}</span>{" "}
                <span className={m.balance < 0 ? "text-danger" : ""}>{signedMoney(m.balance)}</span>
              </p>
            )}
            <div data-numeric className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1 text-xs tabular-nums text-muted-foreground">
              <Bar total={m.inflow} width={pct(m.inflow)} tone="bg-success"/>
              <span className="whitespace-nowrap text-right">{t("finance.overview.legendIn")} {money(m.inflow)}<OfRecurrences cents={m.recIn}/></span>
              <Bar total={m.outflow} width={pct(m.outflow)} tone="bg-danger"/>
              <span className="whitespace-nowrap text-right">{t("finance.overview.legendOut")} {money(m.outflow)}<OfRecurrences cents={m.recOut}/><OfCard card={m.card} estimate={m.estimate}/></span>
            </div>
            {open === m.month && hasCardDetail(m) && <CardDetail m={m} name={name}/>}
          </li>
        );
      })}
    </ol>
  );
}

/**
 * One thin bar from the row's left edge, aligned with its figure's first line.
 * Decoration only: the figure beside it says the amount and, under it, how much
 * of it is recurrences. The chart's lighter recurrences segment is not repeated
 * here, where it had no legend and only its opacity to tell it apart.
 */
function Bar({total, width, tone}: {total: number; width: string; tone: string}) {
  if (total <= 0) return <span aria-hidden className="mt-1.5 h-1.5"/>;
  return <span aria-hidden className={`mt-1.5 h-1.5 min-w-0.5 rounded-full ${tone}`} style={{width}}/>;
}

/**
 * The projected balance at the end of each month, as bars from zero: up when
 * the person still has money, down (in the danger colour, and below the axis,
 * so not by colour alone) when the forecast leaves them in the red. The month's
 * breakdown is in each bar's title and in the table view. Inline SVG, no chart
 * library.
 */
const compact = (cents: number) => new Intl.NumberFormat(currentLocale(), {style: "currency", currency: "BRL", notation: "compact", maximumFractionDigits: 1}).format(cents / 100);

/**
 * The chart's drawing box at its real width (640 until measured), so 11px
 * labels stay 11px. Narrow: month labels thinned to every second or third when "Out/26" (about 36px) no longer fits its slot;
 * each bar's title still names its month.
 */
function frame(measured: number, months: number) {
  const W = measured > 0 ? measured : 640;
  // "R$ 30 mil" at 11px needs the full gutter at any width.
  const LEFT = 64;
  const slot = (W - LEFT) / Math.max(1, months);
  return {W, LEFT, every: Math.max(1, Math.ceil(40 / slot))};
}

/** A round step (1, 2 or 5 × a power of ten) that cuts the span into about four. */
function niceStep(span: number): number {
  const raw = span / 4;
  const pow = 10 ** Math.floor(Math.log10(raw));
  return [1, 2, 5, 10].map(f => f * pow).find(s => s >= raw) ?? 10 * pow;
}

/**
 * What a screen reader gets of a chart. The per-bar <title>s sit inside an
 * svg role="img", whose children are presentational, so they are never read;
 * this is the same facts as text: the ending balance, then one line per period.
 * The visible list/table toggle stays for everyone else.
 */
function ChartSummary({data, line}: {data: ProjectedPeriod[]; line: (m: ProjectedPeriod) => string}) {
  const {t} = useTranslation();
  const last = data[data.length - 1];
  if (!last) return null;
  return (
    <div className="sr-only">
      <p>{t("finance.overview.chartEnding", {month: periodLabel(last), balance: signedMoney(last.balance)})}</p>
      <ul>{data.map(m => <li key={m.month}>{line(m)}</li>)}</ul>
    </div>
  );
}

function ProjectionChart({data}: {data: ProjectedPeriod[]}) {
  const {t} = useTranslation();
  const ref = useRef<HTMLElement>(null);
  const {W, LEFT, every} = frame(useElementWidth(ref), data.length);
  const H = 200, TOP = 10, BOTTOM = 22;
  const step = niceStep(Math.max(100, Math.max(0, ...data.map(m => m.balance)) - Math.min(0, ...data.map(m => m.balance))));
  const lo = Math.floor(Math.min(0, ...data.map(m => m.balance)) / step) * step;
  const hi = Math.max(step, Math.ceil(Math.max(0, ...data.map(m => m.balance)) / step) * step);
  const ticks: number[] = [];
  for (let v = lo; v <= hi; v += step) ticks.push(v);
  const y = (v: number) => TOP + ((hi - v) / (hi - lo)) * (H - TOP - BOTTOM);
  const zero = y(0);
  const slot = (W - LEFT) / Math.max(1, data.length);
  const bw = Math.min(36, slot * 0.5);
  const barTitle = (m: ProjectedPeriod) => t("finance.overview.barTitle", {month: periodLabel(m), balance: signedMoney(m.balance), receivable: money(m.receivable), payable: money(m.payable), recurrences: signedMoney(m.virtual)});
  return (
    <figure ref={ref} className="space-y-2">
      <ChartSummary data={data} line={barTitle}/>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t("finance.overview.chartLabel", {count: data.length})} className="h-auto w-full">
        {ticks.map(v => (
          <g key={v}>
            <line x1={LEFT} x2={W} y1={y(v)} y2={y(v)} className={v === 0 ? "stroke-border" : "stroke-border/50"} strokeWidth={1} strokeDasharray={v === 0 ? undefined : "2 3"}/>
            {/* Centavos: the formatter takes reais. */}
            <text data-axis="y" x={LEFT - 8} y={y(v) + 4} textAnchor="end" className="fill-muted-foreground text-[11px]">{compact(v)}</text>
          </g>
        ))}
        {data.map((m, i) => {
          const x = LEFT + i * slot + slot / 2 - bw / 2;
          const top = Math.min(y(m.balance), zero), h = Math.abs(y(m.balance) - zero);
          return (
            <g key={m.month}>
              <title>{barTitle(m)}</title>
              <rect x={x} y={top} width={bw} height={Math.max(h, 1)} rx={2} className={m.balance < 0 ? "fill-danger" : "fill-brand-600"}/>
              {i % every === 0 && <text x={LEFT + i * slot + slot / 2} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[11px]">{monthShort(m.month)}</text>}
            </g>
          );
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-4 text-xs text-muted-foreground">
        {data[0]?.count > 1 && <span>{t("finance.overview.barSpan", {count: data[0].count})}</span>}
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-brand-600"/>{t("finance.overview.legendEnd")}</span>
        {data.some(m => m.balance < 0) && <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-danger"/>{t("finance.overview.legendRed")}</span>}
      </figcaption>
    </figure>
  );
}

/**
 * Money in and out per month. Entradas rise from the zero line and saídas fall
 * below it, so direction carries the meaning and the green/red pair is a second
 * cue, never the only one. The lighter top of each bar is what recurrences will
 * still generate (a rule's promise, not a bill yet); the dot is the month's net.
 */
function FlowChart({data}: {data: ProjectedPeriod[]}) {
  const {t} = useTranslation();
  const ref = useRef<HTMLElement>(null);
  const {W, LEFT, every} = frame(useElementWidth(ref), data.length);
  const H = 220, TOP = 10, BOTTOM = 22;
  const maxIn = Math.max(0, ...data.map(m => m.inflow)), maxOut = Math.max(0, ...data.map(m => m.outflow));
  const step = niceStep(Math.max(100, maxIn + maxOut));
  const hi = Math.max(step, Math.ceil(maxIn / step) * step);
  const lo = -Math.max(step, Math.ceil(maxOut / step) * step);
  const ticks: number[] = [];
  for (let v = lo; v <= hi; v += step) ticks.push(v);
  const y = (v: number) => TOP + ((hi - v) / (hi - lo)) * (H - TOP - BOTTOM);
  const zero = y(0);
  const slot = (W - LEFT) / Math.max(1, data.length);
  const bw = Math.min(36, slot * 0.5);
  const flowTitle = (m: ProjectedPeriod) => t("finance.overview.flowTitle", {month: periodLabel(m), inflow: money(m.inflow), outflow: money(m.outflow), net: signedMoney(m.result), recIn: money(m.recIn), recOut: money(m.recOut), card: money(m.card), estimate: money(m.estimate)});
  return (
    <figure ref={ref} className="space-y-2">
      <ChartSummary data={data} line={flowTitle}/>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t("finance.overview.flowChartLabel", {count: data.length})} className="h-auto w-full">
        {ticks.map(v => (
          <g key={v}>
            <line x1={LEFT} x2={W} y1={y(v)} y2={y(v)} className={v === 0 ? "stroke-border" : "stroke-border/50"} strokeWidth={1} strokeDasharray={v === 0 ? undefined : "2 3"}/>
            <text data-axis="y" x={LEFT - 8} y={y(v) + 4} textAnchor="end" className="fill-muted-foreground text-[11px]">{compact(v)}</text>
          </g>
        ))}
        {data.map((m, i) => {
          const cx = LEFT + i * slot + slot / 2, x = cx - bw / 2;
          const bills = (v: number) => (v / (hi - lo)) * (H - TOP - BOTTOM);
          const inBills = bills(m.inflow - m.recIn), inRec = bills(m.recIn);
          // Out is split by where it is paid, not by how it is known: other, on a card, and the card's estimate.
          const outOther = bills(Math.max(m.outflow - m.card - m.estimate, 0)), outCard = bills(m.card), outEst = bills(m.estimate);
          return (
            <g key={m.month}>
              <title>{flowTitle(m)}</title>
              {inBills > 0 && <rect x={x} y={zero - inBills} width={bw} height={inBills} className="fill-success"/>}
              {inRec > 0 && <rect x={x} y={zero - inBills - inRec} width={bw} height={inRec} rx={2} className="fill-success opacity-45"/>}
              {outOther > 0 && <rect x={x} y={zero} width={bw} height={outOther} className="fill-danger"/>}
              {outCard > 0 && <rect x={x} y={zero + outOther} width={bw} height={outCard} className="fill-warning"/>}
              {outEst > 0 && <rect x={x} y={zero + outOther + outCard} width={bw} height={outEst} rx={2} className="fill-warning/40 stroke-warning" strokeWidth={1} strokeDasharray="3 2"/>}
              <circle cx={cx} cy={y(m.result)} r={3.5} className="fill-foreground stroke-background" strokeWidth={1.5}/>
              {i % every === 0 && <text x={cx} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[11px]">{monthShort(m.month)}</text>}
            </g>
          );
        })}
      </svg>
      <figcaption className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {data[0]?.count > 1 && <span>{t("finance.overview.barSpan", {count: data[0].count})}</span>}
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-success"/>{t("finance.overview.legendIn")}</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-danger"/>{t(data.some(m => m.card > 0 || m.estimate > 0) ? "finance.overview.legendOther" : "finance.overview.legendOut")}</span>
        {data.some(m => m.card > 0) && <span className="flex items-center gap-1.5"><span className="inline-block size-3 rounded-sm bg-warning"/>{t("finance.overview.legendCard")}</span>}
        {data.some(m => m.estimate > 0) && <span className="flex items-center gap-1.5"><span aria-hidden className="inline-block size-3 rounded-sm border border-dashed border-warning bg-warning/40"/>{t("finance.overview.legendEstimate")}</span>}
        <span className="flex items-center gap-1.5"><span aria-hidden className="inline-flex size-3 overflow-hidden rounded-sm opacity-45"><span className="w-1/2 bg-success"/><span className="w-1/2 bg-danger"/></span>{t("finance.overview.legendRecurring")}</span>
        <span className="flex items-center gap-1.5"><span className="inline-block size-2.5 rounded-full bg-foreground"/>{t("finance.overview.legendNet")}</span>
      </figcaption>
    </figure>
  );
}
