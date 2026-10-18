"use client";

import limits from "@/lib/limits.json";
import {Button, EmptyState, Field, Input, Segmented, Select, Skeleton, Switch} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {AlertCircle, Receipt} from "lucide-react";
import Link from "next/link";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {AssetAccountField, assetsOf, categoriesFor, CategoryField} from "@/components/finance/CategoryAccountFields";
import {FormError, FormErrorText} from "@/components/finance/FormError";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {PanelActions, PanelForm} from "@/components/finance/PanelForm";
import {PanelDrawer} from "@/components/finance/PanelDrawer";
import {BUCKET_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {type HistoryEntry, SuggestingInput} from "@/components/finance/HistorySuggestions";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {DateField} from "@/components/ui/DateField";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {Section} from "@/components/ui/Section";
import {cancelBill, createBill, financeKeys, listAccounts, listBills, patchBill, restoreCancelledBill, settleBill, unsettleBill} from "@/lib/api/finance";
import type {Account, Bill, BillPatch, Bucket, Direction, NewBill, Settlement} from "@/lib/api/financeTypes";
import {bucketLabel} from "@/lib/finance/labels";
import {addYearsIso, dueLabel, monthLabel, todayIso} from "@/lib/finance/today";
import {selectCopy} from "@/lib/selectCopy";
import {accountName} from "@/lib/finance/accountName";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {type FinanceCtx} from "@/lib/api/finance";
import {useCreateRequest} from "@/lib/createRequest";
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {money, shortDate} from "@/lib/format";
import {useFieldErrors} from "@/lib/useFieldErrors";
import {formatMoneyInput, parseMoney} from "@/lib/money";

const GROUPS: Bucket[] = ["overdue", "today", "upcoming"];


/** Everything a write here changes: the list, the balances it moves, the projection. */
const touched = (c: FinanceCtx) => [
  financeKeys.bills(c.mode, c.space, "payable"),
  financeKeys.bills(c.mode, c.space, "receivable"),
  financeKeys.accounts(c.mode, c.space),
  ["finance", c.mode, ...financeKeys.all(c.mode, c.space).slice(2), "projection"],
];

/**
 * F2 — a pagar e a receber.
 *
 * One table per direction, grouped overdue / today / upcoming with a subtotal,
 * in the order the API returns (earliest due first, so overdue leads). A row's
 * actions open in place; the list never disappears behind a modal.
 */
export function BillsView({direction: initial = "payable", focus}: {direction?: Direction; focus?: string} = {}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const {can, loading} = useFinanceSpaces();
  // `?direction=&bill=` (a recurrence's overdue occurrence links here): open on
  // that side and mark that bill.
  const [direction, setDirection] = useState<Direction>(initial);
  const [creating, setCreating] = useState(false);
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("bill", () => { if (loading || can("finance.write")) setCreating(true); });

  const bills = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, direction), queryFn: () => listBills(ctx, direction)});
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const rows = bills.data?.data ?? [];
  const overdue = rows.filter(b => b.bucket === "overdue").reduce((sum, b) => sum + b.amount, 0);
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, accountName(a)]));

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Segmented
          label={t("bills.direction.label")}
          value={direction}
          onValueChange={setDirection}
          options={(["payable", "receivable"] as Direction[]).map(d => ({value: d, label: t(`bills.direction.${d}`)}))}
        />
        {can("finance.write") && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setCreating(true)}>{t("bills.list.new")}</Button>
        )}
      </div>

      <div className="min-w-0 space-y-6">
        {bills.isLoading ? (
          <div className="space-y-3" role="status" aria-busy aria-label={t("bills.list.loading")}>
            <Skeleton className="h-5 w-32"/>
            {[0, 1, 2].map(i => <Skeleton key={i} className="h-11 w-full"/>)}
          </div>
        ) : bills.error ? (
          <ErrorBlock error={bills.error} onRetry={() => void bills.refetch()}/>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<Receipt/>}
            title={t(`bills.list.empty.${direction}`)}
            action={can("finance.write") ? <Button variant="brand" size="sm" onClick={() => setCreating(true)}>{t(`bills.list.emptyAction.${direction}`)}</Button> : undefined}
          />
        ) : (
          <>
            <dl className="flex flex-wrap items-baseline gap-x-8 gap-y-1">
              <div>
                <dt className="text-xs text-muted-foreground">{t("bills.list.total")}</dt>
                <dd><Amount strong className="text-lg">{money(rows.reduce((sum, b) => sum + b.amount, 0))}</Amount></dd>
              </div>
              {overdue > 0 && (
                <div>
                  <dt className="flex items-center gap-1 text-xs text-danger"><AlertCircle aria-hidden className="size-3"/>{t("bills.list.overdueTotal")}</dt>
                  <dd><Amount strong tone="danger" className="text-lg">{money(overdue)}</Amount></dd>
                </div>
              )}
            </dl>
            {GROUPS.map(g => {
              const group = rows.filter(b => (b.bucket ?? "upcoming") === g);
              if (group.length === 0) return null;
              const total = group.reduce((sum, b) => sum + b.amount, 0);
              return (
                <Section key={g} title={t(`bills.list.group.${g}`)} action={<Amount tone={g === "overdue" ? "danger" : "muted"} className="text-sm">{money(total)}</Amount>}>
                  <ul className="divide-y divide-border border-y border-border">
                    {group.map(b => (
                      <BillRow key={b.id} bill={b} current={b.id === focus} accountName={names.get(b.account_id)} accounts={accounts.data?.data ?? []}/>
                    ))}
                  </ul>
                </Section>
              );
            })}
          </>
        )}
      </div>
      <PanelDrawer open={creating} allowed={can("finance.write")} onClose={() => setCreating(false)} title={t("bills.list.newTitle")}>
        <NewBillPanel direction={direction} accounts={accounts.data?.data ?? []} history={rows} onDone={() => setCreating(false)}/>
      </PanelDrawer>
    </div>
  );
}

type Panel = "settle" | "edit" | "cancel" | null

function BillRow({bill, current, accountName, accounts}: {bill: Bill; current?: boolean; accountName?: string; accounts: Account[]}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [panel, setPanel] = useState<Panel>(null);
  const statement = bill.origin === "card_statement";
  const bucket = bill.bucket ?? "upcoming";
  // "Venceu há 5 dias · 10/03": the size of the lateness first, the date for the record.
  const rel = dueLabel(bill.due_date, todayIso());
  const dueText = (d: string) => rel ? `${rel} (${shortDate(d)})` : t("bills.row.due", {date: shortDate(d)});
  return (
    <LedgerRow
      current={current}
      title={bill.description || t("bills.common.noDescription")}
      meta={<>
        {dueText(bill.due_date)}{accountName ? ` • ${accountName}` : ""}{bill.auto_settle ? ` • ${t(`bills.row.autoNote.${bill.direction}`)}` : ""}
        {statement && <> • {t("bills.row.statement")} • <Link href={`/finance/cards?card=${encodeURIComponent(bill.category_id)}`} className="underline-offset-4 hover:underline">{t("bills.row.viewStatement")}</Link></>}
      </>}
      // On a phone the group heading (Vencidas, A vencer) already says it.
      aside={<StatusBadge status={BUCKET_STATUS[bucket]}>{bucketLabel(bucket)}</StatusBadge>}
      asideOnPhone={false}
      amount={<Amount>{money(bill.amount)}</Amount>}
      actions={can("finance.settle") && (
        <Button size="sm" variant={panel === "settle" ? "outline" : "ghost"} aria-expanded={panel === "settle"} onClick={() => setPanel(panel === "settle" ? null : "settle")}>
          {t(`bills.row.settle.${bill.direction}`)}
        </Button>
      )}
      more={can("finance.write") ? [
        {key: "edit", label: t("bills.common.edit"), expanded: panel === "edit", onSelect: () => setPanel(panel === "edit" ? null : "edit")},
        // A statement is closed: corrected by a refund on the card, never canceled.
        ...(statement ? [] : [{key: "cancel", label: t("bills.row.delete"), destructive: true, expanded: panel === "cancel", onSelect: () => setPanel(panel === "cancel" ? null : "cancel")}]),
      ] : []}
    >
      {panel === "settle" && <SettleForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "edit" && <EditForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "cancel" && <CancelConfirm bill={bill} onDone={() => setPanel(null)}/>}
    </LedgerRow>
  );
}

export function SettleForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation();
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount));
  const [date, setDate] = useState(todayIso());
  const [category, setCategory] = useState("");
  const fe = useFieldErrors(["paid_date", "paid_amount", "difference_category_id"]);
  const settle = useFinanceMutation((c, body: Settlement, key) => settleBill(c, bill.id, body, key), touched, onDone, fe.set, {
    undo: {
      message: t("bills.settle.undoAvailable"), actionLabel: t("bills.settle.undoAction"), done: t("bills.settle.undone"), failed: t("bills.settle.undoFailed"),
      run: (c, _v, _r, key) => unsettleBill(c, bill.id, key),
    },
  });

  // The amount is always visible and starts at the bill's own: paying exactly
  // what was owed is the common case, and a different amount is just an edit
  // of the same field — no extra toggle to find.
  const paid = parseMoney(amountText);
  const gap = paid === null ? 0 : paid - bill.amount;
  const categories = accounts.filter(a => (a.class === "income" || a.class === "expense") && !a.system && !a.archived);
  const [tried, setTried] = useState(false);
  const amountErr = fe.of("paid_amount") ?? (tried && paid === null ? t("bills.common.error.amount") : undefined);
  const categoryErr = fe.of("difference_category_id") ?? (tried && gap !== 0 && category === "" ? t("bills.common.error.required") : undefined);

  return (
    <PanelForm
      onSubmit={e => {
        e.preventDefault();
        if (settle.isPending) return;
        if (paid === null || (gap !== 0 && category === "")) { setTried(true); return; }
        fe.reset();
        const body: Settlement = {paid_date: date};
        if (gap !== 0) {
          body.paid_amount = paid;
          body.difference_category_id = category;
        }
        settle.mutate(body);
      }}
    >
      <Field label={t(`bills.settle.date.${bill.direction}`)} htmlFor={`d-${bill.id}`} error={fe.of("paid_date")}>
        <DateField id={`d-${bill.id}`} min={limits.minDate} max={todayIso()} value={date} invalid={!!fe.of("paid_date")} onValueChange={v => { setDate(v); fe.clear("paid_date"); }}/>
      </Field>
      <Field label={t(`bills.settle.amount.${bill.direction}`)} htmlFor={`v-${bill.id}`} error={amountErr}>
        <MoneyInput id={`v-${bill.id}`} value={amountText} {...fe.props("paid_amount", `v-${bill.id}`, amountErr)} onValueChange={v => { setAmountText(v); fe.clear("paid_amount"); }}/>
      </Field>
      {gap !== 0 && (
        <>
          <Field label={t("bills.settle.diffCategory")} htmlFor={`c-${bill.id}`} error={categoryErr}>
            <Select {...selectCopy()} id={`c-${bill.id}`} value={category} {...fe.props("difference_category_id", `c-${bill.id}`, categoryErr)} onValueChange={v => { setCategory(v); fe.clear("difference_category_id"); }} options={categories.map(a => ({value: a.id, label: accountName(a)}))}/>
          </Field>
          <p className="text-sm text-muted-foreground sm:pt-7">
            {t(gap > 0 ? "bills.settle.more" : "bills.settle.less", {amount: money(Math.abs(gap)), bill: money(bill.amount)})}
          </p>
        </>
      )}
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={settle.isPending}>{t(`bills.settle.confirm.${bill.direction}`)}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        <FormError error={settle.error} fe={fe}/>
      </PanelActions>
    </PanelForm>
  );
}

function EditForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [description, setDescription] = useState(bill.description ?? "");
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount));
  const [due, setDue] = useState(bill.due_date);
  const [category, setCategory] = useState(bill.category_id);
  const [account, setAccount] = useState(bill.account_id);
  const [autoSettle, setAutoSettle] = useState(bill.auto_settle);
  const fe = useFieldErrors(["description", "amount", "due_date", "category_id", "account_id", "auto_settle"]);
  const edit = useFinanceMutation((c, body: BillPatch, key) => patchBill(c, bill.id, body, key), touched, onDone, fe.set);
  // A card statement's amount is its purchases' and its "category" is the card.
  const statement = bill.origin === "card_statement";
  const amount = parseMoney(amountText);
  const [tried, setTried] = useState(false);
  const amountErr = fe.of("amount") ?? (tried && !statement && amount === null ? t("bills.common.error.amount") : undefined);
  // Turning auto-settle ON is settling; turning it off only removes power.
  const showAuto = can("finance.settle") || bill.auto_settle;

  return (
    <PanelForm
      onSubmit={e => {
        e.preventDefault();
        if (edit.isPending) return;
        if (!statement && amount === null) { setTried(true); return; }
        const body: BillPatch = {};
        if (description !== (bill.description ?? "")) body.description = description;
        if (!statement && amount !== null && amount !== bill.amount) body.amount = amount;
        if (due !== bill.due_date) body.due_date = due;
        if (category !== bill.category_id) body.category_id = category;
        if (account !== bill.account_id) body.account_id = account;
        if (autoSettle !== bill.auto_settle) body.auto_settle = autoSettle;
        if (Object.keys(body).length === 0) { onDone(); return; }
        fe.reset();
        edit.mutate(body);
      }}
    >
      <Field label={t("bills.common.description")} htmlFor={`ed-${bill.id}`} error={fe.of("description")}><Input id={`ed-${bill.id}`} maxLength={limits.text.description} value={description} {...fe.props("description", `ed-${bill.id}`)} onChange={e => { setDescription(e.target.value); fe.clear("description"); }}/></Field>
      {!statement && <Field label={t("bills.common.amount")} htmlFor={`ev-${bill.id}`} error={amountErr}><MoneyInput id={`ev-${bill.id}`} value={amountText} {...fe.props("amount", `ev-${bill.id}`, amountErr)} onValueChange={v => { setAmountText(v); fe.clear("amount"); }}/></Field>}
      <Field label={t("bills.common.due")} htmlFor={`eu-${bill.id}`} error={fe.of("due_date")}><DateField id={`eu-${bill.id}`} min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={due} invalid={!!fe.of("due_date")} onValueChange={v => { setDue(v); fe.clear("due_date"); }}/></Field>
      {!statement && (
        <CategoryField id={`ec-${bill.id}`} label={t("bills.common.category")} accounts={accounts} direction={bill.direction} value={category}
          error={fe.of("category_id")} controlProps={fe.props("category_id", `ec-${bill.id}`)} onValueChange={v => { setCategory(v); fe.clear("category_id"); }}/>
      )}
      <AssetAccountField id={`ea-${bill.id}`} label={t(`bills.common.payWith.${bill.direction}`)} accounts={accounts} value={account}
        error={fe.of("account_id")} controlProps={fe.props("account_id", `ea-${bill.id}`)} onValueChange={v => { setAccount(v); fe.clear("account_id"); }}/>
      {showAuto && (
        <label className="flex items-center gap-2 self-end text-sm">
          <Switch checked={autoSettle} onCheckedChange={setAutoSettle} disabled={!autoSettle && !can("finance.settle")}/>
          {t(`bills.common.auto.${bill.direction}`)}
        </label>
      )}
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={edit.isPending}>{t("bills.common.save")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        <FormError error={edit.error} fe={fe}/>
      </PanelActions>
    </PanelForm>
  );
}

function CancelConfirm({bill, onDone}: {bill: Bill; onDone: () => void}) {
  const {t} = useTranslation();
  const cancel = useFinanceMutation((c, _: void, key) => cancelBill(c, bill.id, key), touched, onDone, undefined, {
    undo: {
      message: t("bills.delete.undoAvailable"), actionLabel: t("bills.settle.undoAction"), done: t("bills.delete.undone"), failed: t("bills.delete.undoFailed"),
      run: (c, _v, _r, key) => restoreCancelledBill(c, bill.id, key),
    },
  });
  return (
    <InlineConfirm
      text={t("bills.delete.confirm", {name: bill.description || t("bills.delete.fallbackName"), month: monthLabel(bill.competence_date)})}
      cancelLabel={t("bills.common.keep")} confirmLabel={t("bills.row.delete")}
      pending={cancel.isPending} error={cancel.error ? <FormErrorText error={cancel.error}/> : null} onCancel={onDone} onConfirm={() => cancel.mutate()}/>
  );
}

function NewBillPanel({direction, accounts, history, onDone}: {direction: Direction; accounts: Account[]; history: Bill[]; onDone: () => void}) {
  const {t} = useTranslation();
  const {can} = useFinanceSpaces();
  const [description, setDescription] = useState("");
  const [amountText, setAmountText] = useState("");
  const [due, setDue] = useState(todayIso());
  const [competence, setCompetence] = useState("");
  const cats = categoriesFor(accounts, direction);
  const assets = assetsOf(accounts);
  const [category, setCategory] = useState("");
  const [account, setAccount] = useState("");
  const [autoSettle, setAutoSettle] = useState(false);
  // Paid already (cash received at the counter, a bill paid on the spot): the
  // bill is created and settled right after, each with its own intent; if only
  // the second fails, the bill exists and the form offers to retry the payment.
  const [paidNow, setPaidNow] = useState(false);
  const [paidOn, setPaidOn] = useState(todayIso());
  //
  // The list is refreshed once, after the LAST step: a create that a settle
  // follows invalidates nothing, or the refetch in between shows the bill open
  // under "A pagar" for as long as the settle takes and then drops it. If the
  // settle fails the bill really is open, so the list is refreshed then too,
  // and the person is told: the panel (and its inline retry) may already be
  // closed, and they would believe the bill was paid.
  const settle = useFinanceMutation(
    (c, v: {id: string; body: Settlement}, key) => settleBill(c, v.id, v.body, key), touched,
    () => { toast.success(t(`bills.new.settled.${direction}`)); onDone(); },
    () => { toast.error(t(`bills.new.settleError.${direction}`)); },
    {invalidateOnError: true},
  );
  const fe = useFieldErrors(["description", "amount", "due_date", "competence_date", "category_id", "account_id"]);
  const create = useFinanceMutation(
    (c, v: {body: NewBill; paidOn?: string}, key) => createBill(c, v.body, key),
    (c, _created, v) => (v.paidOn ? [] : touched(c)),
    (created, v) => (v.paidOn ? settle.mutate({id: created.id, body: {paid_date: v.paidOn}}) : onDone()),
    fe.set,
  );
  const amount = parseMoney(amountText);
  const [tried, setTried] = useState(false);
  const missing = (empty: boolean) => tried && empty ? t("bills.common.error.required") : undefined;
  const amountErr = fe.of("amount") ?? (tried && amount === null ? t("bills.common.error.amount") : undefined);
  const dueErr = fe.of("due_date") ?? missing(due === "");
  const categoryErr = fe.of("category_id") ?? missing(category === "");
  const accountErr = fe.of("account_id") ?? missing(account === "");
  const suggestions: HistoryEntry[] = history.map(b => ({description: b.description ?? "", amount: b.amount, categoryId: b.category_id, accountId: b.account_id}));

  return (
    <div className="space-y-3">
      <form
        className="space-y-3"
        onSubmit={e => {
          e.preventDefault();
          if (create.isPending || create.isSuccess) return;
          if (amount === null || category === "" || account === "" || due === "") { setTried(true); return; }
          fe.reset();
          create.mutate({
            body: {
              direction, amount, category_id: category, account_id: account, description: description || undefined,
              due_date: due, competence_date: competence || undefined, auto_settle: (autoSettle && !paidNow) || undefined,
            },
            paidOn: paidNow ? paidOn : undefined,
          });
        }}
      >
        <Field label={t("bills.common.description")} htmlFor="nb-desc" error={fe.of("description")}>
          <SuggestingInput id="nb-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "nb-desc")} onValueChange={v => { setDescription(v); fe.clear("description"); }} autoFocus
            entries={suggestions}
            onSelect={entry => {
              setDescription(entry.description);
              setAmountText(formatMoneyInput(entry.amount));
              if (cats.some(a => a.id === entry.categoryId)) setCategory(entry.categoryId);
              if (assets.some(a => a.id === entry.accountId)) setAccount(entry.accountId!);
            }}/>
        </Field>
        <Field label={t("bills.common.amount")} htmlFor="nb-amount" required error={amountErr}><MoneyInput id="nb-amount" value={amountText} {...fe.props("amount", "nb-amount", amountErr)} onValueChange={v => { setAmountText(v); fe.clear("amount"); }}/></Field>
        <Field label={t("bills.common.due")} htmlFor="nb-due" required error={dueErr}><DateField id="nb-due" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={due} invalid={!!dueErr} onValueChange={v => { setDue(v); fe.clear("due_date"); }}/></Field>
        <Field label={t("bills.new.competence")} htmlFor="nb-comp" error={fe.of("competence_date")}>
          <DateField id="nb-comp" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={competence} invalid={!!fe.of("competence_date")} onValueChange={v => { setCompetence(v); fe.clear("competence_date"); }} placeholder={t("bills.new.competencePlaceholder")} clearLabel={t("bills.new.clearCompetence")}/>
        </Field>
        <CategoryField id="nb-cat" label={t("bills.common.category")} accounts={accounts} direction={direction} value={category} required
          error={categoryErr} controlProps={fe.props("category_id", "nb-cat", categoryErr)} onValueChange={v => { setCategory(v); fe.clear("category_id"); }}
          emptyHint={t(`bills.noCategory.${direction === "payable" ? "expense" : "income"}`)}/>
        {can("finance.settle") && (
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <Switch checked={paidNow} onCheckedChange={setPaidNow}/>
            {t(`bills.new.paidNow.${direction}`)}
          </label>
        )}
        {/* What happens to the money: planned (which account, paid by itself on
            the due date or not) or already done, and then the section is the
            payment itself, named by direction, with its date and account. */}
        {/* The rule sits on a wrapper: on the fieldset itself the legend is
            drawn into its border, and floating the legend out of it pushes the
            grid fields beside it, off a phone's screen. */}
        <div className="border-t border-border pt-4">
        <fieldset className="space-y-3">
          <legend className="mb-3 text-sm font-medium text-foreground">
            {paidNow ? t(`bills.new.section.${direction}`) : t("bills.new.section.plan")}
          </legend>
          {paidNow && (
            <Field label={t(`bills.settle.date.${direction}`)} htmlFor="nb-paid">
              <DateField id="nb-paid" min={limits.minDate} max={todayIso()} value={paidOn} onValueChange={setPaidOn}/>
            </Field>
          )}
          <AssetAccountField id="nb-acct" label={t(paidNow ? `bills.new.paidWith.${direction}` : `bills.common.payWith.${direction}`)} accounts={accounts} value={account} required
            error={accountErr} controlProps={fe.props("account_id", "nb-acct", accountErr)} onValueChange={v => { setAccount(v); fe.clear("account_id"); }}
            emptyHint={t("bills.noAccount")}/>
          {can("finance.settle") && !paidNow && (
            <label className="flex min-h-11 items-center gap-2 text-sm">
              <Switch checked={autoSettle} onCheckedChange={setAutoSettle}/>
              {t(`bills.common.auto.${direction}`)}
            </label>
          )}
        </fieldset>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="brand" size="sm" disabled={create.isPending || create.isSuccess}>{t("bills.new.create")}</Button>
          <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        </div>
        <FormError error={create.error} fe={fe}/>
        {settle.error && settle.variables ? (
          <div role="alert" className="flex flex-wrap items-center gap-2 text-sm">
            <p className="text-danger">{t(`bills.new.settleFailed.${direction}`)}</p>
            <Button type="button" size="sm" variant="outline" disabled={settle.isPending} onClick={() => settle.mutate(settle.variables!)}>{t("bills.new.retry")}</Button>
            <Button type="button" size="sm" variant="ghost" onClick={onDone}>{t("bills.new.leaveOpen")}</Button>
          </div>
        ) : null}
      </form>
    </div>
  );
}
