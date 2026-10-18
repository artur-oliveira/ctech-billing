"use client";

import limits from "@/lib/limits.json";
import {Button, EmptyState, Field, Input, Segmented, Select, Skeleton, Switch} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {ChevronDown, Repeat} from "lucide-react";
import Link from "next/link";
import React, {useEffect, useId, useRef, useState} from "react";
import {useTranslation} from "react-i18next";

import {CardBrandMark, cardLabel} from "@/components/finance/CardBrandMark";
import {AssetAccountField, categoriesFor, CategoryField} from "@/components/finance/CategoryAccountFields";
import {ExceptionsFields, PatternFields} from "@/components/finance/ExpressionEditor";
import {FormError, FormErrorText} from "@/components/finance/FormError";
import {type HistoryEntry, SuggestingInput} from "@/components/finance/HistorySuggestions";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {PendingOccurrenceAction} from "@/components/finance/PendingOccurrenceAction";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {PanelDrawer} from "@/components/finance/PanelDrawer";
import {RECURRENCE_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {OccurrenceTimeline, type TimelineEntry} from "@/components/finance/OccurrenceTimeline";
import {installmentOptions, installmentsPreview} from "@/components/finance/PurchasePanel";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {DateField} from "@/components/ui/DateField";
import {messageFor, problemCode} from "@/lib/api/client";
import {
  archiveRecurrence, createRecurrence, type FinanceCtx, financeKeys, getRecurrenceOccurrences, listAccounts, listCards, listPendingOccurrences, listRecurrences,
  patchRecurrence, previewRecurrence,
} from "@/lib/api/finance";
import type {Account, Adjust, Card, Direction, ExpressionJSON, NewRecurrence, Occurrence, PendingOccurrence, Recurrence, RecurrencePatch} from "@/lib/api/financeTypes";
import {defaultModel, describeModel, type EditorModel, fromExpression, toExpression, validate} from "@/lib/finance/expression";
import {currentLocale, t} from "@/lib/i18n";
import {isMonthly, monthlyEffect} from "@/lib/finance/recurrenceEffect";
import {addDaysIso, addYearsIso, dueLabel, monthShort, todayIso} from "@/lib/finance/today";
import {selectCopy} from "@/lib/selectCopy";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {useCreateRequest} from "@/lib/createRequest";
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {dayMonth, money, shortDate, signedMoney} from "@/lib/format";
import {useFieldErrors} from "@/lib/useFieldErrors";
import {formatMoneyInput, parseMoney} from "@/lib/money";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {accountName} from "@/lib/finance/accountName";

const PREVIEW_COUNT = 6;
const DEBOUNCE_MS = 300;
const adjustOptions = (): {value: Adjust; label: string}[] => [
  {value: "roll_forward", label: t("bills.rec.adjust.roll_forward")},
  {value: "none", label: t("bills.rec.adjust.none")},
];
const touched = (c: FinanceCtx) => [financeKeys.recurrences(c.mode, c.space), [...financeKeys.all(c.mode, c.space), "projection"], financeKeys.cardForecasts(c.mode, c.space)];
// Ending and archiving also cancels bills and reverses their recognition (UX
// batch 5): Agenda, balances, statements and reports all move, so the whole space.
const touchedByArchive = (c: FinanceCtx) => [financeKeys.all(c.mode, c.space)];
const touchedByEdit = (c: FinanceCtx, _: unknown, body: RecurrencePatch) =>
  body.archive || body.cancel_after_end ? [financeKeys.all(c.mode, c.space)] : touched(c);

function ruleOf(r: Recurrence): string {
  const model = fromExpression(r.expression);
  return model ? describeModel(model) : t("bills.rec.customRule");
}

/** What the list leads with: what needs the person first (attention, a value to confirm), then what comes soonest. */
function byUrgency(pending: Map<string, PendingOccurrence[]>) {
  const rank = (r: Recurrence) => (r.needs_attention ? 0 : pending.has(r.id) ? 1 : 2);
  return (a: Recurrence, b: Recurrence) => rank(a) - rank(b) || (a.next_due ?? "9999").localeCompare(b.next_due ?? "9999");
}

/**
 * F4 — recorrências: the future, as a list ("quanto vou ter?"): each one with its
 * next date, its amount (fixed or an estimate), what it weighs on an average
 * month and what waits for the real amount, above a total of the month. A side
 * panel creates or edits one; its rule editor previews the next occurrences
 * while it is edited, and the preview IS the confirmation, before anything is saved.
 */
export function RecurrencesView({focus}: {focus?: string} = {}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can, loading} = useFinanceSpaces();
  const [panel, setPanel] = useState<{mode: "new"} | {mode: "edit"; rec: Recurrence} | null>(null);
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("recurrence", () => { if (loading || can("finance.write")) setPanel({mode: "new"}); });
  const recs = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)});
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const pendingQ = useQuery({queryKey: financeKeys.pendingOccurrences(ctx.mode, ctx.space), queryFn: () => listPendingOccurrences(ctx)});
  const cards = useQuery({
    queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx),
    enabled: panel !== null || (recs.data?.data ?? []).some(r => !!r.card_id),
  });
  const cardList = cards.data?.data ?? [];
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, accountName(a)]));
  const pending = new Map<string, PendingOccurrence[]>();
  for (const p of pendingQ.data?.data ?? []) pending.set(p.recurrence_id, [...(pending.get(p.recurrence_id) ?? []), p]);
  const active = (recs.data?.data ?? []).filter(r => !r.archived).sort(byUrgency(pending));
  // The empty state carries the create button itself; two of the same would crowd it.
  const showEmpty = !recs.isLoading && !recs.error && active.length === 0;
  const waiting = active.reduce((n, r) => n + (pending.get(r.id)?.length ?? 0), 0);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("bills.rec.title")}</h1>
        {can("finance.write") && !showEmpty && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setPanel({mode: "new"})}>{t("bills.rec.new")}</Button>
        )}
      </div>

      <PanelDrawer open={panel !== null} allowed={panel?.mode !== "new" || can("finance.write")} onClose={() => setPanel(null)} size="lg"
        title={panel?.mode === "edit" ? t("bills.rec.edit") : t("bills.rec.new")}>
        {panel && (
          <RecurrencePanel
            key={panel.mode === "edit" ? panel.rec.id : "new"}
            editing={panel.mode === "edit" ? panel.rec : undefined}
            accounts={accounts.data?.data ?? []}
            cards={cardList}
            history={recs.data?.data ?? []}
            onDone={() => setPanel(null)}
          />
        )}
      </PanelDrawer>

      {recs.isLoading ? (
        <div className="space-y-3" role="status" aria-busy aria-label={t("bills.rec.loading")}>
          <Skeleton className="h-12 w-full"/>
          {[0, 1, 2].map(i => <Skeleton key={i} className="h-11 w-full"/>)}
        </div>
      ) : recs.error ? (
        <ErrorBlock error={recs.error} onRetry={() => void recs.refetch()}/>
      ) : active.length === 0 ? (
        <EmptyState
          icon={<Repeat/>}
          title={t("bills.rec.empty")}
          description={t("bills.rec.emptyHint")}
          action={can("finance.write") ? <Button variant="brand" size="sm" onClick={() => setPanel({mode: "new"})}>{t("bills.rec.new")}</Button> : undefined}
        />
      ) : (
        <>
          <MonthSummary recs={active} waiting={waiting}/>
          <Section title={t("bills.rec.listTitle")} hideTitle>
            <ul className="divide-y divide-border border-y border-border">
              {active.map(r => (
                <RecurrenceRow key={r.id} rec={r} account={names.get(r.account_id)} card={cardList.find(c => c.id === r.card_id)}
                  pending={pending.get(r.id) ?? []} focused={r.id === focus} onEdit={() => setPanel({mode: "edit", rec: r})}/>
              ))}
            </ul>
          </Section>
        </>
      )}
    </div>
  );
}

/**
 * What the recurrences weigh on an average month, in and out, and what still
 * needs the real amount. An estimate (a weekly rule is 4.3 a month), said so.
 */
function MonthSummary({recs, waiting}: {recs: Recurrence[]; waiting: number}) {
  const {t} = useTranslation();
  let inflow = 0, outflow = 0;
  for (const r of recs) {
    const m = monthlyEffect(r) ?? 0;
    if (r.direction === "receivable") inflow += m; else outflow += m;
  }
  return (
    <Section title={t("bills.rec.summary.title")} action={waiting > 0 && (
      <Link href="/finance/inbox" className="inline-flex items-center touch-target">
        <StatusBadge status={RECURRENCE_STATUS.pending}>{t("bills.rec.pendingBadge", {count: waiting})}</StatusBadge>
      </Link>
    )}>
      <dl className="grid grid-cols-3 gap-x-3 sm:flex sm:flex-wrap sm:gap-x-8 sm:gap-y-2">
        <div><dt className="text-xs text-muted-foreground">{t("bills.rec.summary.in")}</dt><dd><Amount tone="success" strong className="text-base sm:text-lg">{money(inflow)}</Amount></dd></div>
        <div><dt className="text-xs text-muted-foreground">{t("bills.rec.summary.out")}</dt><dd><Amount strong className="text-base sm:text-lg">{money(outflow)}</Amount></dd></div>
        <div><dt className="text-xs text-muted-foreground">{t("bills.rec.summary.net")}</dt><dd><Amount strong tone={inflow - outflow < 0 ? "danger" : "default"} className="text-base sm:text-lg">{signedMoney(inflow - outflow)}</Amount></dd></div>
      </dl>
      <p className="text-xs text-muted-foreground">{t("bills.rec.summary.hint")}</p>
    </Section>
  );
}

function RecurrenceRow({rec, account, card, pending = [], focused = false, onEdit}: {rec: Recurrence; account?: string; card?: Card; pending?: PendingOccurrence[]; focused?: boolean; onEdit: () => void}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [confirming, setConfirming] = useState(false);
  // A disclosure: the detail opens in place under the row and the focus stays
  // on the button, which says whether it is open (aria-expanded). A link from a
  // statement item (?recurrence=) opens it and brings it on screen.
  const [open, setOpen] = useState(focused);
  const detailId = useId();
  const anchor = useRef<HTMLSpanElement>(null);
  useEffect(() => { if (focused) anchor.current?.scrollIntoView?.({block: "center"}); }, [focused]);
  const target = account ?? (card ? cardLabel(card) : "");
  const archive = useFinanceMutation((c, _: void, key) => archiveRecurrence(c, rec.id, key), touchedByArchive, () => setConfirming(false));
  const expected = rec.amount_mode === "expected";
  const today = todayIso();
  const rel = rec.next_due ? dueLabel(rec.next_due, today) : null;
  const next = rec.next_due ? t("bills.rec.nextOn", {when: rel ? `${shortDate(rec.next_due)} • ${rel}` : shortDate(rec.next_due)}) : t("bills.rec.noNext");
  const monthly = isMonthly(rec.expression) ? null : monthlyEffect(rec);
  const sign = rec.direction === "receivable" ? 1 : -1;
  return (
    <LedgerRow
      title={rec.description || t("bills.common.noDescription")}
      meta={<>
        {/* The future first: when it is next, what it weighs on a month. */}
        <span ref={anchor}/><span className="block font-medium text-foreground">{next}{monthly ? ` • ${t("bills.rec.monthly", {amount: money(monthly)})}` : ""}</span>
        {/* A phone has no column for the direction, so the line says it. */}
        <span className="sm:hidden">{t(`bills.direction.${rec.direction}`)} • </span>
        {ruleOf(rec)}{target ? ` • ${target}` : ""}{rec.installments && rec.installments > 1 ? ` • ${rec.installments}×` : ""}{rec.auto_settle ? ` • ${t(`bills.row.autoNote.${rec.direction}`)}` : ""}
      </>}
      aside={<>
        <span className="text-xs text-muted-foreground max-sm:hidden">{t(`bills.direction.${rec.direction}`)}</span>
        {expected && <StatusBadge status={RECURRENCE_STATUS.estimated}>{t("bills.rec.estimatedBadge")}</StatusBadge>}
        {pending.length > 0 && <StatusBadge status={RECURRENCE_STATUS.pending}>{t("bills.rec.pendingBadge", {count: pending.length})}</StatusBadge>}
      </>}
      amount={<Amount tone={rec.direction === "receivable" ? "success" : "default"}>{signedMoney(sign * rec.amount)}</Amount>}
      actions={
        <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={detailId} onClick={() => setOpen(v => !v)}>
          {t("bills.rec.view")}
          <ChevronDown aria-hidden className={`size-4 transition-transform duration-200 ease-out motion-reduce:transition-none ${open ? "rotate-180" : ""}`}/>
        </Button>
      }
      more={can("finance.write") ? [
        {key: "edit", label: t("bills.common.edit"), onSelect: onEdit},
        {key: "end", label: t("bills.rec.end"), destructive: true, expanded: confirming, onSelect: () => setConfirming(v => !v)},
      ] : []}
    >
      {rec.needs_attention && (
        <p role="status" className="mt-1 flex flex-wrap items-center gap-2 text-sm text-foreground">
          <StatusBadge status={RECURRENCE_STATUS.attention}>{t(`bills.rec.attention.${rec.needs_attention}`)}</StatusBadge>
          {can("finance.write") && <Button size="sm" variant="ghost" onClick={onEdit}>{t("bills.rec.attention.fix")}</Button>}
        </p>
      )}
      {/* The real amount is asked here, where the rule is: the same one-tap form the inbox draws. */}
      {can("finance.write") && pending.length > 0 && (
        <div className="mt-2 space-y-3 rounded-lg bg-surface p-3">
          {pending.map(p => <PendingOccurrenceAction key={p.nominal} item={p}/>)}
        </div>
      )}
      {open && <RecurrenceDetail id={detailId} rec={rec}/>}
      {confirming && (
        <InlineConfirm className="mt-2" cancelLabel={t("bills.common.keep")} confirmLabel={t("bills.rec.confirm")}
          text={<>{t("bills.rec.endConfirm")}<EndCancels rec={rec} end={today}/></>}
          pending={archive.isPending} error={archive.error ? <FormErrorText error={archive.error}/> : null} onCancel={() => setConfirming(false)} onConfirm={() => archive.mutate()}/>
      )}
    </LedgerRow>
  );
}

/**
 * F4's inline detail (UX batch 3): a timeline of what is to come (the bills
 * already made and not yet due, then the dates the rule will make) and of what
 * happened (paid, overdue with a way to it, skipped), most recent first.
 */
function RecurrenceDetail({id, rec}: {id: string; rec: Recurrence}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.recurrenceOccurrences(ctx.mode, ctx.space, rec.id), queryFn: () => getRecurrenceOccurrences(ctx, rec.id)});
  const name = rec.description || t("bills.common.noDescription");
  const made = (o: NonNullable<typeof q.data>["history"][number]): TimelineEntry => ({
    key: o.bill_id ?? o.purchase_id ?? o.nominal, nominal: o.nominal, due: o.due, kind: o.state, amount: money(o.amount),
    state: o.state === "pending_confirmation" ? t("bills.rec.expectedPending")
      : o.state === "posted" && o.statement_month ? t("bills.rec.state.posted", {month: monthShort(o.statement_month)})
      : o.state === "paid" && o.paid_date ? t("bills.rec.state.paidOn", {date: shortDate(o.paid_date)}) : t(`bills.rec.state.${o.state}`),
    action: o.state === "pending_confirmation"
      ? <Link href="/finance/inbox" className="inline-flex items-center text-sm text-foreground underline underline-offset-4 hover:text-brand-700 touch-target">{t("bills.rec.confirmAmount")}</Link>
      : o.state === "overdue" && o.bill_id
      ? <Link href={`/finance/bills?direction=${rec.direction}&bill=${encodeURIComponent(o.bill_id)}`} className="inline-flex items-center text-sm text-foreground underline underline-offset-4 hover:text-brand-700 touch-target">{t(`bills.rec.openBill.${rec.direction}`)}</Link>
      : undefined,
  });
  const next: TimelineEntry[] = [
    ...(q.data?.history ?? []).filter(o => o.state === "forecast").map(made),
    ...(q.data?.upcoming ?? []).map(o => ({key: `u-${o.nominal}`, nominal: o.nominal, due: o.due, kind: "upcoming" as const,
      state: o.statement_month ? `${t("bills.rec.state.upcoming")} • ${t("bills.rec.onStatement", {month: monthShort(o.statement_month)})}` : t("bills.rec.state.upcoming"),
      amount: money(rec.amount)})),
  ];
  const past = (q.data?.history ?? []).filter(o => o.state !== "forecast").reverse().map(made);
  return (
    <section id={id} aria-label={t("bills.rec.detail", {name})}
      className="mt-3 grid gap-5 rounded-lg bg-surface p-3 sm:grid-cols-2 sm:gap-6 sm:p-4 motion-safe:animate-in motion-safe:fade-in">
      {q.isLoading ? (
        <div className="space-y-2 sm:col-span-2" aria-busy><Skeleton className="h-4 w-48"/><Skeleton className="h-4 w-56"/><Skeleton className="h-4 w-40"/></div>
      ) : q.error ? (
        <div className="sm:col-span-2"><ErrorBlock error={q.error} onRetry={() => void q.refetch()}/></div>
      ) : (
        <>
          <div className="min-w-0 space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">{t("bills.rec.upcoming")}</h3>
            {next.length > 0
              ? <OccurrenceTimeline label={t("bills.rec.upcoming")} entries={next}/>
              : <p className="text-sm text-muted-foreground">{t("bills.rec.nothingToCome")}</p>}
          </div>
          <div className="min-w-0 space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">{t("bills.rec.history")}</h3>
            {past.length > 0
              ? <OccurrenceTimeline label={t("bills.rec.history")} entries={past}/>
              : <p className="text-sm text-muted-foreground">{t("bills.rec.noHistory")}</p>}
          </div>
        </>
      )}
    </section>
  );
}

/**
 * Ending and archiving a recurrence cancels the bills it already made for dates
 * after the new end that are not paid (UX batch 5, spec § 3.5); a paid one stays,
 * since the money moved and the statement shows it. The confirmation names both
 * before anything is sent, from the recurrence's own history (the latest bills
 * it made, which are the ones after any end).
 */
function EndCancels({rec, end}: {rec: Recurrence; end: string}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.recurrenceOccurrences(ctx.mode, ctx.space, rec.id), queryFn: () => getRecurrenceOccurrences(ctx, rec.id)});
  const after = (q.data?.history ?? []).filter(o => o.nominal > end);
  const canceled = after.filter(o => o.state === "forecast" || o.state === "overdue");
  const pending = after.filter(o => o.state === "pending_confirmation");
  const paid = after.filter(o => o.state === "paid");
  if (canceled.length === 0 && pending.length === 0 && paid.length === 0) return null;
  const list = (xs: typeof after) => new Intl.ListFormat(currentLocale(), {type: "conjunction"}).format(xs.map(o => dayMonth(o.due)));
  return (
    <div className="mt-1 w-full space-y-1">
      {canceled.length > 0 && <p className="text-foreground">{t("bills.rec.endCancels", {count: canceled.length, dates: list(canceled)})}</p>}
      {pending.length > 0 && <p className="text-foreground">{t("bills.rec.endCancelsPending", {count: pending.length, dates: list(pending)})}</p>}
      {paid.length > 0 && <p className="text-muted-foreground">{t("bills.rec.endKeepsPaid", {count: paid.length, dates: list(paid)})}</p>}
    </div>
  );
}

/**
 * The next occurrences for a valid model, debounced and last-answer-wins: a
 * slow reply to an older edit never overwrites the preview of a newer one.
 */
function usePreview(ctx: FinanceCtx, expression: ExpressionJSON, start: string, end: string, adjust: Adjust, from: string, valid: boolean) {
  const [state, setState] = useState<{occ: Occurrence[]; loading: boolean; error: unknown; done: boolean}>({occ: [], loading: false, error: null, done: false});
  const seq = useRef(0);
  const body = JSON.stringify({expression, start, end, adjust, from});
  useEffect(() => {
    if (!valid || !start) return undefined;
    const mine = ++seq.current;
    const abort = new AbortController();
    const t = setTimeout(() => {
      setState(s => ({...s, loading: true}));
      const parsed = JSON.parse(body) as {expression: ExpressionJSON; start: string; end: string; adjust: Adjust; from: string};
      previewRecurrence(ctx, {
        expression: parsed.expression, start: parsed.start, end: parsed.end || undefined,
        business_day_adjust: parsed.adjust, from: parsed.from > parsed.start ? parsed.from : parsed.start, count: PREVIEW_COUNT,
      }, abort.signal)
        .then(r => { if (mine === seq.current) setState({occ: r.data, loading: false, error: null, done: true}); })
        .catch(e => { if (mine === seq.current && !abort.signal.aborted) setState({occ: [], loading: false, error: e, done: true}); });
    }, DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      abort.abort();
    };
  }, [ctx, body, valid, start]);
  return state;
}

function RecurrencePanel({editing, accounts, cards, history, onDone}: {editing?: Recurrence; accounts: Account[]; cards: Card[]; history: Recurrence[]; onDone: () => void}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can} = useFinanceSpaces();
  const [direction, setDirection] = useState<Direction>(editing?.direction ?? "payable");
  const [description, setDescription] = useState(editing?.description ?? "");
  const [amountText, setAmountText] = useState(editing ? formatMoneyInput(editing.amount) : "");
  const [amountMode, setAmountMode] = useState<"fixed" | "expected">(editing?.amount_mode ?? "fixed");
  const [category, setCategory] = useState(editing?.category_id ?? "");
  // The account or the card it is paid with (spec D1): one select, both kinds.
  const [target, setTarget] = useState(editing?.card_id || editing?.account_id || "");
  const [installments, setInstallments] = useState(editing?.installments ?? 1);
  const [start, setStart] = useState(editing?.start ?? todayIso());
  const [end, setEnd] = useState(editing?.end ?? "");
  const [adjust, setAdjust] = useState<Adjust>(editing?.business_day_adjust ?? "roll_forward");
  const [autoSettle, setAutoSettle] = useState(editing?.auto_settle ?? false);
  const [model, setModel] = useState<EditorModel>(() => (editing && fromExpression(editing.expression)) || defaultModel(todayIso()));

  const errors = editing ? [] : validate(model);
  // Creating: the dates from today. Editing: the rule is fixed, so the preview
  // shows what is left after today under the end being edited, which is what
  // tells the person that an end will close the recurrence.
  const preview = usePreview(ctx, editing ? editing.expression : toExpression(model), start, end,
    editing ? editing.business_day_adjust : adjust, editing ? addDaysIso(todayIso(), 1) : todayIso(), errors.length === 0);
  // An end the server asked about — it cancels open bills after it (422
  // end_cancels_bills, UX batch 5 review) or leaves nothing to come (422
  // recurrence_would_end): saving it again asks first instead of repeating the
  // request. `archive` is whether the confirmed request must also archive.
  const [asked, setAsked] = useState<{end: string; archive: boolean} | null>(null);
  const [confirming, setConfirming] = useState(false);
  // A confirmed end whose cancellations did not all happen (409
  // recurrence_end_incomplete): the end is saved; the same request finishes it.
  const [incomplete, setIncomplete] = useState<RecurrencePatch | null>(null);
  const lastPatch = useRef<RecurrencePatch>({});
  const sent = useRef<RecurrencePatch>({});
  // The preview of an edit lists the dates left after today: none means this
  // end also ends the rule, so the one confirmation archives it too.
  const endsByPreview = !!editing && preview.done && !preview.loading && !preview.error && preview.occ.length === 0;
  const amount = parseMoney(amountText);
  const cats = categoriesFor(accounts, direction);
  // A card pays expenses only; an archived one is never offered (the one a
  // recurrence still names shows by its id until the person picks another).
  const payCards = direction === "payable" ? cards.filter(c => !c.archived) : [];
  const isCard = cards.some(c => c.id === target) || (!!editing?.card_id && target === editing.card_id);
  const cardOptions = payCards.map(c => ({value: c.id, label: cardLabel(c), icon: <CardBrandMark brand={c.brand ?? "other"}/>}));
  // What was set up before suggests itself: its name brings its amount, category and target.
  const suggestions: HistoryEntry[] = history.filter(r => r.description).map(r => ({
    description: r.description!, amount: r.amount, categoryId: r.category_id, accountId: r.card_id || r.account_id,
  }));

  const [more, setMore] = useState(false);
  const fe = useFieldErrors(["description", "amount", "category_id", "account_id", "card_id", "installments", ...(editing ? [] : ["start"]), "end"]);
  const create = useFinanceMutation((c, body: NewRecurrence, key) => createRecurrence(c, body, key), touched, onDone, fe.set);
  const patch = useFinanceMutation((c, body: RecurrencePatch, key) => patchRecurrence(c, editing!.id, body, key), touchedByEdit, onDone, e => {
    const code = problemCode(e);
    const end = lastPatch.current.end;
    if ((code === "recurrence_would_end" || code === "end_cancels_bills") && end) {
      setAsked(a => ({end, archive: code === "recurrence_would_end" || endsByPreview || (a?.end === end && a.archive)}));
      setConfirming(true);
      return;
    }
    if (code === "recurrence_end_incomplete") {
      setIncomplete(sent.current);
      return;
    }
    fe.set(e);
  }, {invalidateOnError: true}); // a half-done end moved bills: show what is true now
  const send = (body: RecurrencePatch) => {
    sent.current = body;
    setIncomplete(null);
    setConfirming(false);
    patch.mutate(body);
  };
  const pending = create.isPending || patch.isPending;
  const ready = amount !== null && category !== "" && target !== "" && errors.length === 0 && !pending;
  // A card's statement is what gets paid: no auto-settle on a card.
  const showAuto = amountMode === "fixed" && !isCard && (can("finance.settle") || (editing?.auto_settle ?? false));

  function submit(e: React.SyntheticEvent) {
    e.preventDefault();
    if (!ready) return;
    fe.reset();
    if (!editing) {
      create.mutate({
        direction, amount: amount!, amount_mode: amountMode, category_id: category, description: description || undefined,
        expression: toExpression(model), start, end: end || undefined, business_day_adjust: adjust,
        ...(isCard ? {card_id: target, installments} : {account_id: target, auto_settle: autoSettle || undefined}),
      });
      return;
    }
    const body: RecurrencePatch = {};
    if (amount !== editing.amount) body.amount = amount!;
    if (category !== editing.category_id) body.category_id = category;
    if (target !== (editing.card_id || editing.account_id)) {
      if (isCard) body.card_id = target;
      else body.account_id = target;
    }
    if (isCard && installments !== (editing.installments ?? 1)) body.installments = installments;
    if (description !== (editing.description ?? "")) body.description = description;
    if (!isCard && autoSettle !== editing.auto_settle) body.auto_settle = autoSettle;
    // Emptied is sent as "": the API layer turns it into null, which removes the end.
    if (end !== (editing.end ?? "")) body.end = end;
    if (Object.keys(body).length === 0) {
      onDone();
      return;
    }
    lastPatch.current = body;
    // An end the server already asked about: show the confirmation again from
    // the same refusal. Sending here would repeat the request the person has not
    // confirmed (and, once the API accepts it, end and archive the recurrence).
    if (body.end && body.end === asked?.end) {
      setConfirming(true);
      return;
    }
    send(body);
  }

  const nextDates = preview.occ.slice(0, PREVIEW_COUNT);
  const previewList = (
    <div className="space-y-2" aria-live="polite">
      <h3 id="rc-next" className="text-sm font-medium text-foreground">{t("bills.rec.next")}</h3>
      {errors.length > 0 ? (
        <p className="text-sm text-muted-foreground">{t("bills.rec.fixRule")}</p>
      ) : preview.error ? (
        <p role="alert" className="text-sm text-danger">{messageFor(preview.error)}</p>
      ) : nextDates.length > 0 ? (
        <OccurrenceTimeline label={t("bills.rec.next")}
          entries={nextDates.map(o => ({key: o.nominal, nominal: o.nominal, due: o.due, kind: "upcoming" as const}))}/>
      ) : preview.done && !preview.loading ? (
        <p className="text-sm text-muted-foreground">{editing ? t("bills.rec.noneLeft") : t("bills.rec.noneAtAll")}</p>
      ) : (
        <p className="text-sm text-muted-foreground">{t("bills.rec.calculating")}</p>
      )}
    </div>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">

        {!editing && (
          <Segmented
            label={t("bills.direction.label")}
            value={direction}
            onValueChange={d => { setDirection(d); setCategory(""); if (isCard) setTarget(""); }}
            options={(["payable", "receivable"] as Direction[]).map(d => ({value: d, label: t(`bills.direction.${d}`)}))}
          />
        )}
      </div>
      <form className="space-y-4" onSubmit={submit}>
        <div className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0">
          <Field label={t("bills.common.description")} htmlFor="rc-desc" error={fe.of("description")} className="sm:col-span-2">
            {editing
              ? <Input id="rc-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "rc-desc")} onChange={e => { setDescription(e.target.value); fe.clear("description"); }}/>
              : <SuggestingInput id="rc-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "rc-desc")} autoFocus
                onValueChange={v => { setDescription(v); fe.clear("description"); }} entries={suggestions}
                onSelect={entry => {
                  setDescription(entry.description);
                  setAmountText(formatMoneyInput(entry.amount));
                  if (cats.some(a => a.id === entry.categoryId)) setCategory(entry.categoryId);
                  if (entry.accountId && [...accounts.map(a => a.id), ...payCards.map(c => c.id)].includes(entry.accountId)) setTarget(entry.accountId);
                }}/>}
          </Field>
          {!editing && <AmountModeField value={amountMode} onChange={setAmountMode}/>}
          <Field label={t(amountMode === "expected" ? "bills.rec.expectedAmount" : "bills.common.amount")} htmlFor="rc-amount" required error={fe.of("amount")}><MoneyInput id="rc-amount" value={amountText} {...fe.props("amount", "rc-amount")} onValueChange={v => { setAmountText(v); fe.clear("amount"); }}/></Field>
          <CategoryField id="rc-cat" label={t("bills.common.category")} accounts={accounts} direction={direction} value={category} required
            error={fe.of("category_id")} controlProps={fe.props("category_id", "rc-cat")} onValueChange={v => { setCategory(v); fe.clear("category_id"); }}
            emptyHint={t(`bills.noCategory.${direction === "payable" ? "expense" : "income"}`)}/>
          <AssetAccountField id="rc-acct" label={t(`bills.common.payWith.${direction}`)} accounts={accounts} extraOptions={cardOptions} value={target} required
            error={fe.of("account_id") ?? fe.of("card_id")} controlProps={fe.props("account_id", "rc-acct")}
            onValueChange={v => { setTarget(v); fe.clear("account_id"); fe.clear("card_id"); }} emptyHint={t("bills.noAccount")}/>
          {isCard && (
            <>
              <Field label={t("bills.rec.installments")} htmlFor="rc-inst" error={fe.of("installments")}
                hint={amount ? installmentsPreview(amount, installments) : undefined}>
                <Select {...selectCopy()} id="rc-inst" value={String(installments)} onValueChange={v => { setInstallments(Number(v)); fe.clear("installments"); }} options={installmentOptions()}/>
              </Field>
              <p className="text-sm text-muted-foreground sm:col-span-2">{t("bills.rec.cardNote")}</p>
            </>
          )}
          {!editing && (
            <>
              <PatternFields model={model} start={start} errors={errors} onChange={setModel}/>
              <Field label={t("bills.rec.startsOn")} htmlFor="rc-start" error={fe.of("start")}><DateField id="rc-start" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={start} invalid={!!fe.of("start")} onValueChange={v => { setStart(v); fe.clear("start"); }}/></Field>
            </>
          )}
          {/* The end is a property of the recurrence, not an option: it is always on screen. */}
          <Field label={t("bills.rec.endsOn")} htmlFor="rc-end" error={fe.of("end")}><DateField id="rc-end" min={start || limits.minDate} max={addYearsIso(start || todayIso(), limits.maxRecurrenceYears)} value={end} invalid={!!fe.of("end")} onValueChange={v => { setEnd(v); fe.clear("end"); }} placeholder={t("bills.rec.noEnd")} clearLabel={t("bills.rec.clearEnd")}/></Field>
        </div>

        {editing ? (
          <>
            <p className="text-sm text-muted-foreground">
              {t("bills.rec.editNote", {rule: ruleOf(editing)})}
            </p>
            {/* An end that had passed, cleared or moved later: the server resumes
                the rule from the current month, never backfilling the gap. */}
            {editing.end && editing.end < todayIso() && (end === "" || end > editing.end) && (
              <p role="status" className="text-sm text-foreground">{t("bills.rec.reopenNote")}</p>
            )}
            {previewList}
          </>
        ) : (
          <>
            {previewList}

            <div>
              <button type="button" aria-expanded={more} onClick={() => setMore(v => !v)}
                className="inline-flex items-center text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline touch-target">
                {more ? t("bills.rec.lessOptions") : t("bills.rec.moreOptions")}
              </button>
              {more && (
                <div className="mt-3 grid items-start gap-4 border-t border-border pt-4 sm:grid-cols-2">
                  <Field label={t("bills.rec.weekend")} htmlFor="rc-adjust">
                    <Select {...selectCopy()} id="rc-adjust" value={adjust} onValueChange={v => setAdjust(v as Adjust)} options={adjustOptions()}/>
                  </Field>
                  <ExceptionsFields model={model} errors={errors} onChange={setModel}/>
                </div>
              )}
            </div>
          </>
        )}

        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          {showAuto && (
            <label className="flex min-h-11 items-center gap-2 text-sm">
              <Switch checked={autoSettle} onCheckedChange={setAutoSettle} disabled={!autoSettle && !can("finance.settle")}
                aria-label={t(`bills.common.auto.${direction}`)}/>
              {t(`bills.common.auto.${direction}`)}
            </label>
          )}
          <div className="flex gap-2">
            <Button type="submit" variant="brand" size="sm" disabled={!ready}>{editing ? t("bills.common.save") : t("bills.rec.create")}</Button>
            <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
          </div>
        </div>
        {confirming && asked && editing && (
          <InlineConfirm announce className="mt-0" cancelLabel={t("bills.rec.back")} confirmLabel={asked.archive ? t("bills.rec.endAndArchive") : t("bills.rec.saveAndCancel")}
            text={<>{asked.archive && <span className="mb-1 block">{t("bills.rec.endsConfirm")}</span>}<EndCancels rec={editing} end={asked.end}/></>}
            pending={patch.isPending} onCancel={() => setConfirming(false)}
            onConfirm={() => send({...lastPatch.current, cancel_after_end: true, ...(asked.archive ? {archive: true} : {})})}/>
        )}
        {incomplete && (
          <InlineConfirm announce className="mt-0" tone="brand" confirmLabel={t("bills.rec.retry")}
            text={t(`bills.rec.endIncomplete.${incomplete.archive ? "archived" : "saved"}`)}
            pending={patch.isPending} onConfirm={() => send(incomplete)}/>
        )}
        {/* A 409 that is the half-done end has its own panel above. */}
        <FormError error={incomplete ? undefined : patch.error ?? create.error} fe={fe} className=""/>
      </form>
    </div>
  );
}


/**
 * How the amount behaves, as a row of the form: a label, the two choices across
 * the whole width, and a line saying what the chosen one means. It was a bare
 * toggle in a grid cell, which put it beside the amount with no label to align to.
 */
function AmountModeField({value, onChange}: {value: "fixed" | "expected"; onChange: (mode: "fixed" | "expected") => void}) {
  const {t} = useTranslation();
  return (
    <div data-slot="mode-field" className="grid gap-1.5 sm:col-span-2">
      <span className="text-sm font-medium text-foreground">{t("bills.rec.amountMode.label")}</span>
      <Segmented fill label={t("bills.rec.amountMode.label")} value={value} onValueChange={v => onChange(v as "fixed" | "expected")}
        options={(["fixed", "expected"] as const).map(v => ({value: v, label: t(`bills.rec.amountMode.${v}`)}))}/>
      <p className="text-sm text-muted-foreground">{t(`bills.rec.amountMode.${value}Hint`)}</p>
    </div>
  );
}
