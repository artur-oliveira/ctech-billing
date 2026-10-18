"use client";

import {Button, EmptyState, Field, Input, Select, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {CreditCard, Repeat} from "lucide-react";
import Link from "next/link";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {SettleForm} from "@/components/finance/BillsView";
import {CardBrandMark, cardLabel, maskedLast4} from "@/components/finance/CardBrandMark";
import {CardForm} from "@/components/finance/CardForm";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {PanelActions, PanelForm} from "@/components/finance/PanelForm";
import {PanelDrawer} from "@/components/finance/PanelDrawer";
import {StatusBadge} from "@/components/finance/StatusBadge";
import {PurchasePanel} from "@/components/finance/PurchasePanel";
import {StatementTotal} from "@/components/finance/StatementTotal";
import {StatementTimeline, useOpeningPage} from "@/components/finance/StatementTimeline";
import {STATUS, statementState} from "@/components/finance/statementStatus";
import {FormError, FormErrorText} from "@/components/finance/FormError";
import {wholeSpace} from "@/components/finance/TransferPanel";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {
  advancePurchase, closeStatement, type FinanceCtx, financeKeys, getBill, getCardStatement, getCardStatementForecast, listAccounts, listCards, listPurchases,
  patchPurchase, refundPurchase,
} from "@/lib/api/finance";
import type {Account, Card, CardStatement, Purchase, PurchasePatch, StatementItem, StatementSummary} from "@/lib/api/financeTypes";
import limits from "@/lib/limits.json";
import {useFieldErrors} from "@/lib/useFieldErrors";
import {currentLocale} from "@/lib/i18n";
import {flatten, outlook} from "@/lib/finance/statementTimeline";
import {dueLabel, monthShort, todayIso} from "@/lib/finance/today";
import {selectCopy} from "@/lib/selectCopy";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {useCreateRequest} from "@/lib/createRequest";
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {dayMonth, money, shortDate, signedMoney} from "@/lib/format";
import {accountName} from "@/lib/finance/accountName";

export {statementState} from "@/components/finance/statementStatus";

type Panel = "new-card" | "edit-card" | "purchase" | null

/**
 * F5 — cartões. One card at a time, one statement at a time: the open one
 * first, then back to what was billed and forward to what will be. A purchase
 * is the whole expense on its date; the statements only show where each
 * installment is billed.
 */
export function CardsView({card: initial = ""}: {card?: string}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can, loading: verbsLoading} = useFinanceSpaces();
  const [picked, setPicked] = useState(initial);
  const [month, setMonth] = useState<string | null>(null);
  const [chosen, setChosen] = useState<Panel>(null);
  const [asked, setAsked] = useState(false);
  const [loadPurchases, setLoadPurchases] = useState(false);
  useCreateRequest("purchase", () => setAsked(true));

  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)});
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const active = (cards.data?.data ?? []).filter(c => !c.archived).sort((a, b) => a.name.localeCompare(b.name, currentLocale()));
  const card = active.find(c => c.id === picked) ?? active[0];
  const purchases = useQuery({
    queryKey: financeKeys.purchases(ctx.mode, ctx.space, card?.id ?? ""),
    queryFn: () => listPurchases(ctx, card!.id),
    enabled: !!card && (chosen === "purchase" || (asked && !cards.isLoading && !accounts.isLoading && can("finance.write")) || loadPurchases),
  });
  const shown = month ?? card?.open_month ?? "";
  const all = accounts.data?.data ?? [];
  // The central action on a phone: a purchase on the card on screen, or the
  // card itself when there is none to buy with yet.
  const loaded = !cards.isLoading && !accounts.isLoading && !verbsLoading;
  const requested: Panel = asked && loaded
    ? (card ? (can("finance.write") ? "purchase" : null) : can("finance.configure") ? "new-card" : null)
    : null;
  const panel = chosen ?? requested;
  const setPanel = (p: Panel) => { setChosen(p); setAsked(false); };

  // One new-card drawer for both the empty state and the card on screen.
  const newCardDrawer = (
    <PanelDrawer open={panel === "new-card"} allowed={can("finance.configure")} onClose={() => setPanel(null)} title={t("finance.cards.new")}>
      <CardForm accounts={all} onDone={id => { setPanel(null); if (id) { setPicked(id); setMonth(null); } }}/>
    </PanelDrawer>
  );

  if (cards.isLoading || accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/></div>;
  }
  if (cards.error || accounts.error) {
    return <ErrorBlock error={cards.error ?? accounts.error} onRetry={() => { void cards.refetch(); void accounts.refetch(); }}/>;
  }
  if (!card) {
    return (
      <div className="space-y-4">
        {newCardDrawer}
        {(
          <EmptyState
            icon={<CreditCard/>}
            title={t("finance.cards.empty")}
            description={can("finance.configure") ? undefined : t("finance.cards.emptyAsk")}
            action={can("finance.configure") ? <Button variant="brand" size="sm" onClick={() => setPanel("new-card")}>{t("finance.cards.new")}</Button> : undefined}
          />
        )}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <Field label={t("finance.cards.card")} htmlFor="cd-pick" className="max-sm:w-full">
          <Select {...selectCopy()} id="cd-pick" aria-label={t("finance.cards.card")} value={card.id} className="w-full sm:w-64"
            onValueChange={v => { setPicked(v); setMonth(null); setPanel(null); }}
            options={active.map(c => ({value: c.id, label: cardLabel(c), icon: <CardBrandMark brand={c.brand ?? "other"}/>}))}/>
        </Field>
        <div className="flex flex-wrap gap-2">
          {can("finance.write") && <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setPanel("purchase")}>{t("finance.cards.newPurchase")}</Button>}
          {can("finance.configure") && (
            <>
              <Button variant="ghost" size="sm" onClick={() => setPanel("edit-card")}>{t("finance.cards.edit")}</Button>
              <Button variant="ghost" size="sm" onClick={() => setPanel("new-card")}>{t("finance.cards.new")}</Button>
            </>
          )}
        </div>
      </div>

      <PanelDrawer open={panel === "purchase"} allowed={can("finance.write")} onClose={() => setPanel(null)} title={t("finance.cards.newPurchase")} description={card.name}>
        <PurchasePanel cardId={card.id} accounts={all} history={purchases.data?.data ?? []} onDone={() => setPanel(null)}/>
      </PanelDrawer>
      <PanelDrawer open={panel === "edit-card"} allowed={can("finance.configure")} onClose={() => setPanel(null)} title={t("finance.cards.editNamed", {name: card.name})}>
        <CardForm key={card.id} card={card} accounts={all} onDone={() => setPanel(null)}/>
      </PanelDrawer>
      {newCardDrawer}

      <CardHeader card={card} selected={shown} onSelect={setMonth}/>

      <StatementTimeline key={financeKeys.cardStatements(ctx.mode, ctx.space, card.id, card.open_month).join("|")} card={card} selected={shown} onSelect={setMonth}/>

      <StatementBlock key={`${card.id}-${shown}`} card={card} month={shown} accounts={all}
        purchases={purchases.data?.data ?? []} purchasesError={purchases.error} retryPurchases={() => void purchases.refetch()}
        onNeedPurchases={() => setLoadPurchases(true)}/>
    </div>
  );
}


/**
 * The card on screen: its mark, name and last digits, the cycle days, and what it
 * owes next: the statement to pay or else the open one, with its total (forecast
 * included), when it closes and when it is due in a person's words.
 */
function CardHeader({card, selected, onSelect}: {card: Card; selected: string; onSelect: (month: string) => void}) {
  const {t} = useTranslation();
  const opening = useOpeningPage(card);
  const {next} = outlook(flatten(opening.data ? [opening.data] : []), card.open_month);
  return (
    <div data-card-header className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3 border-b border-border pb-3">
      <div className="flex min-w-0 items-center gap-3">
        <CardBrandMark brand={card.brand ?? "other"} className="text-muted-foreground"/>
        <div className="min-w-0">
          <h2 className="flex flex-wrap items-baseline gap-x-2 text-base font-semibold tracking-[-0.01em] text-foreground">
            <span className="min-w-0 [overflow-wrap:anywhere]">{card.name}</span>
            {card.last4 && <span className="text-sm font-normal tabular-nums text-muted-foreground">{maskedLast4(card.last4)}</span>}
          </h2>
          <p className="text-xs text-muted-foreground">{t("finance.cards.cycle", {closing: card.closing_day, due: card.due_day})}</p>
        </div>
      </div>
      {next && <NextStatement statement={next} owed={next.status === "closed"} open={selected === next.month} onSelect={() => onSelect(next.month)}/>}
    </div>
  );
}

/** What is due next, as one block: the figure first, then the dates. Pressing it shows that statement. */
function NextStatement({statement: s, owed, open, onSelect}: {statement: StatementSummary; owed: boolean; open: boolean; onSelect: () => void}) {
  const {t} = useTranslation();
  const rel = !owed && s.due_date < todayIso() ? null : dueLabel(s.due_date, todayIso());
  const late = owed && s.due_date < todayIso();
  const body = (
    <>
      <span className="text-xs text-muted-foreground">{t(owed ? "finance.cards.next.owed" : "finance.cards.next.open", {month: monthShort(s.month)})}</span>
      <StatementTotal posted={s.total} forecast={s.forecast_total ?? 0} estimated={s.forecast_estimated} size="lg"/>
      <span className="text-xs tabular-nums text-muted-foreground">
        {!owed && <>{t("finance.cards.closesShort", {date: dayMonth(s.closing_date)})}{" · "}</>}
        <span className={rel ? `font-medium ${late ? "text-danger" : "text-foreground"}` : undefined}>
          {rel ? `${rel} · ${dayMonth(s.due_date)}` : t("finance.cards.dueShort", {date: dayMonth(s.due_date)})}
        </span>
      </span>
    </>
  );
  const cls = "flex min-w-0 flex-col items-start gap-0.5 text-left";
  // Selected, from `sm` the statement's own summary below the strip says the same: it is not repeated.
  return open
    ? <div data-next-statement className={`${cls} sm:hidden`}>{body}</div>
    : <button type="button" data-next-statement onClick={onSelect} className={`${cls} touch-target -m-1 rounded-lg p-1 outline-none hover:bg-surface focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2`}>{body}</button>;
}

function StatementBlock({card, month, accounts, purchases, purchasesError, retryPurchases, onNeedPurchases}: {
  card: Card; month: string; accounts: Account[]; purchases: Purchase[]; purchasesError: unknown;
  retryPurchases: () => void; onNeedPurchases: () => void
}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can} = useFinanceSpaces();
  const q = useQuery({queryKey: financeKeys.cardStatement(ctx.mode, ctx.space, card.id, month), queryFn: () => getCardStatement(ctx, card.id, month)});
  const [action, setAction] = useState<"close" | "pay" | null>(null);
  const forecast = useQuery({
    queryKey: financeKeys.cardForecast(ctx.mode, ctx.space, card.id, month),
    queryFn: () => getCardStatementForecast(ctx, card.id, month),
  });
  const names = new Map(accounts.map(a => [a.id, accountName(a)]));
  const byId = new Map(purchases.map(p => [p.id, p]));
  const s = q.data;
  const forecastSum = (forecast.data?.data ?? []).reduce((sum, item) => sum + item.amount, 0);

  return (
    <section aria-label={`${t("finance.cards.statement")} ${monthShort(month)}`} className="space-y-3">
      {q.isLoading ? <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/></div> : q.error || !s ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <>
          <Summary statement={s} forecast={forecastSum} estimated={(forecast.data?.data ?? []).some(item => item.pending_confirmation)}
            forecastState={forecast.isLoading ? "loading" : forecast.error ? "error" : undefined}/>
          {s.status === "open" && can("finance.write") && action !== "close" && (
            <Button variant="outline" size="sm" onClick={() => setAction("close")}>{t("finance.cards.closeNow")}</Button>
          )}
          {s.status === "closed" && s.bill_id && can("finance.settle") && action !== "pay" && (
            <Button variant="brand" size="sm" onClick={() => setAction("pay")}>{t("finance.cards.pay")}</Button>
          )}
          {action === "close" && (
            <Confirm
              text={t("finance.cards.closeConfirm", {month: monthShort(month)})}
              run={(c, key) => closeStatement(c, card.id, month, key)}
              onDone={() => setAction(null)}
            />
          )}
          {action === "pay" && s.bill_id && <PayStatement billId={s.bill_id} accounts={accounts} onDone={() => setAction(null)}/>}
          <Section title={t("finance.cards.recurringForecast")} className="space-y-2 border-t border-border pt-3">
            {forecast.isLoading ? <Skeleton className="h-12 w-full"/> : forecast.error ? <ErrorBlock error={forecast.error} onRetry={() => void forecast.refetch()}/> : (forecast.data?.data.length ?? 0) === 0 ? (
              <p className="text-sm text-muted-foreground">{t("finance.cards.noRecurringForecast")}</p>
            ) : <>
              <ul className="divide-y divide-border border-y border-border">
                {forecast.data!.data.map((item, index) => <li key={`${item.recurrence_id}-${item.nominal}-${item.installment}-${index}`} className="flex items-start justify-between gap-3 py-2 text-sm">
                  <span className="min-w-0">
                    <span className="block [overflow-wrap:anywhere]">{item.description || t("finance.cards.recurringPurchase")}</span>
                    <span className="block text-xs text-muted-foreground">
                      {shortDate(item.nominal)}{item.of > 1 ? ` · ${item.installment}/${item.of}` : ""}{item.pending_confirmation ? ` · ${t("finance.cards.awaitingConfirmation")}` : ""}
                    </span>
                  </span>
                  <Amount>{money(item.amount)}</Amount>
                </li>)}
              </ul>
              <dl className="grid gap-1 text-sm">
                <div className="flex items-baseline justify-between gap-3">
                  <dt className="text-muted-foreground">{t("finance.cards.forecastSubtotal")}</dt>
                  <dd data-numeric className="tabular-nums">{money(forecastSum)}</dd>
                </div>
              </dl>
              <p className="text-xs text-muted-foreground">{t("finance.cards.forecastInTotal")}</p>
            </>}
          </Section>
          {s.items.length === 0 ? (
            <EmptyState title={t("finance.cards.noPurchases")} className="py-8"/>
          ) : (
            <ul className="divide-y divide-border border-y border-border">
              {s.items.map(it => (
                <ItemRow key={`${it.purchase_id}-${it.kind}-${it.number ?? 0}`} item={it} card={card} accounts={accounts}
                  purchase={byId.get(it.purchase_id)} category={it.category_id ? names.get(it.category_id) : undefined}
                  purchasesError={purchasesError} retryPurchases={retryPurchases} onNeedPurchases={onNeedPurchases}/>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function Summary({statement: s, forecast, estimated, forecastState}: {statement: CardStatement; forecast: number; estimated: boolean; forecastState?: "loading" | "error"}) {
  const {t} = useTranslation();
  const closed = s.status === "closed" || s.status === "paid";
  const today = todayIso();
  const state = statementState(s, today);
  // Words only while it is still to be paid: a paid or future statement's "vence em" is noise.
  // An open statement's due date is still ahead; a "venceu há" belongs only to an overdue one.
  const rel = (s.status === "open" && s.due_date >= today) || s.status === "closed" ? dueLabel(s.due_date, today) : null;
  return (
    // On a phone the summary card above says all of this, one per screen.
    <div className="flex flex-wrap items-baseline justify-between gap-3 text-sm max-sm:hidden">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <StatusBadge status={STATUS[state]}>{t(`finance.cards.status.${state}`)}</StatusBadge>
        <span className="text-muted-foreground">{t(closed ? "finance.cards.closedOn" : "finance.cards.closesOn", {date: shortDate(s.closing_date)})}</span>
        <span className="text-muted-foreground">{t("finance.cards.dueOn", {date: shortDate(s.due_date)})}</span>
        {rel && <span className={`font-medium ${state === "overdue" ? "text-danger" : "text-foreground"}`}>{rel}</span>}
      </div>
      <StatementTotal posted={s.total} forecast={forecast} estimated={estimated} forecastState={forecastState} size="lg" className="items-end text-right"/>
    </div>
  );
}

function PayStatement({billId, accounts, onDone}: {billId: string; accounts: Account[]; onDone: () => void}) {
  const ctx = useFinanceCtx();
  const bill = useQuery({queryKey: financeKeys.bill(ctx.mode, ctx.space, billId), queryFn: () => getBill(ctx, billId)});
  if (bill.isLoading) return <Skeleton className="h-16 w-full"/>;
  if (bill.error || !bill.data) return <ErrorBlock error={bill.error} onRetry={() => void bill.refetch()}/>;
  return <SettleForm bill={bill.data} accounts={accounts} onDone={onDone}/>;
}

function ItemRow({item, card, accounts, purchase, category, purchasesError, retryPurchases, onNeedPurchases}: {
  item: StatementItem; card: Card; accounts: Account[]; purchase?: Purchase; category?: string;
  purchasesError: unknown; retryPurchases: () => void; onNeedPurchases: () => void
}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [action, setAction] = useState<"edit" | "refund" | "advance" | null>(null);
  // Its name and category can be corrected from any of its lines, an advance's too.
  const editable = (item.kind === "installment" || item.kind === "advance") && !purchase?.refunded && can("finance.write");
  const actionable = item.kind === "installment" && !purchase?.refunded && can("finance.write");
  const canAdvance = actionable && (!purchase || purchase.installments.some(i => i.month > card.open_month));
  return (
    // UX batch 4: a purchase's actions are a left swipe or "⋯" on a phone, each asking first.
    <LedgerRow
      title={item.description}
      meta={<>
        {shortDate(item.date)}{item.number && item.of ? ` • ${item.number}/${item.of}` : ""}{category ? ` • ${category}` : ""}
        {item.recurrence_id && (
          <>
            {" • "}
            <Link href={`/finance/recurrences?recurrence=${encodeURIComponent(item.recurrence_id)}`}
              aria-label={t("finance.cards.openRecurrence", {name: item.description})}
              className="inline-flex items-center gap-1 text-foreground underline-offset-4 hover:underline touch-target">
              <Repeat aria-hidden className="size-3"/>{t("finance.cards.recurring")}
            </Link>
          </>
        )}
      </>}
      amount={<Amount>{signedMoney(item.amount)}</Amount>}
      more={[
        ...(editable ? [{key: "edit", label: t("finance.cards.editPurchase"), expanded: action === "edit", onSelect: () => { onNeedPurchases(); setAction("edit"); }}] : []),
        ...(canAdvance ? [{key: "advance", label: t("finance.cards.advance"), expanded: action === "advance", onSelect: () => { onNeedPurchases(); setAction("advance"); }}] : []),
        ...(actionable ? [{key: "refund", label: t("finance.cards.refund"), destructive: true, expanded: action === "refund", onSelect: () => { onNeedPurchases(); setAction("refund"); }}] : []),
      ]}
    >
      {action === "edit" && purchase && <PurchaseEditForm card={card} purchase={purchase} accounts={accounts} onDone={() => setAction(null)}/>}
      {action && !purchase && (purchasesError ? <div className="mt-3"><ErrorBlock error={purchasesError} onRetry={retryPurchases}/></div> : <Skeleton className="mt-3 h-12 w-full"/>)}
      {action === "refund" && purchase && (
        <Confirm
          text={t("finance.cards.refundConfirm")}
          run={(c, key) => refundPurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
      {action === "advance" && purchase && (
        <Confirm
          text={t("finance.cards.advanceConfirm")}
          run={(c, key) => advancePurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
    </LedgerRow>
  );
}

/**
 * A purchase's safe fields, in place under its line: the name every statement
 * shows and the category its expense is in. The amount, the date and the
 * installments are what was charged, so they are not offered: a wrong amount
 * is a refund and a new purchase.
 */
function PurchaseEditForm({card, purchase, accounts, onDone}: {card: Card; purchase: Purchase; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation();
  const [description, setDescription] = useState(purchase.description);
  const [category, setCategory] = useState(purchase.category_id);
  const fe = useFieldErrors(["description", "category_id"]);
  const edit = useFinanceMutation((c, body: PurchasePatch, key) => patchPurchase(c, card.id, purchase.id, body, key), wholeSpace, onDone, fe.set);
  // Active expense categories, and the current one even if it was archived since.
  const cats = accounts.filter(a => a.class === "expense" && !a.system && (!a.archived || a.id === purchase.category_id))
    .sort((a, b) => accountName(a).localeCompare(accountName(b), currentLocale()));
  const id = `pe-${purchase.id}`;
  const moved = category !== purchase.category_id;
  return (
    <PanelForm
      aria-label={t("finance.cards.editPurchase")}
      onSubmit={e => {
        e.preventDefault();
        const body: PurchasePatch = {};
        const name = description.trim();
        if (name !== purchase.description) body.description = name;
        if (moved) body.category_id = category;
        if (Object.keys(body).length === 0) return onDone();
        fe.reset();
        edit.mutate(body);
        return () => {};
      }}
    >
      <Field label={t("bills.common.description")} htmlFor={`${id}-d`} required error={fe.of("description")}>
        <Input id={`${id}-d`} maxLength={limits.text.description} value={description} {...fe.props("description", `${id}-d`)}
          onChange={e => { setDescription(e.target.value); fe.clear("description"); }} autoFocus/>
      </Field>
      <Field label={t("bills.common.category")} htmlFor={`${id}-c`} required error={fe.of("category_id")}
        hint={moved ? t("finance.cards.recategorizeHint") : undefined}>
        <Select {...selectCopy()} id={`${id}-c`} aria-label={t("bills.common.category")} value={category} {...fe.props("category_id", `${id}-c`)}
          onValueChange={v => { setCategory(v); fe.clear("category_id"); }} options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={description.trim() === "" || edit.isPending}>{t("bills.common.save")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("finance.cards.back")}</Button>
        <FormError error={edit.error} fe={fe} className=""/>
      </PanelActions>
    </PanelForm>
  );
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const {t} = useTranslation();
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone);
  return (
    <InlineConfirm text={text} cancelLabel={t("finance.cards.back")} confirmLabel={t("finance.cards.confirm")} tone="brand"
      pending={m.isPending} error={m.error ? <FormErrorText error={m.error}/> : null} onCancel={onDone} onConfirm={() => m.mutate()}/>
  );
}
