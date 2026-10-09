"use client"

import limits from "@/lib/limits.json"
import {Badge, Button, EmptyState, Field, Input, Skeleton, Switch} from "@aoctech/ui"
import {Drawer} from "@/components/ui/ConsoleOverlay"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {AlertCircle, CalendarClock, Clock, Receipt} from "lucide-react"
import Link from "next/link"
import {useEffect, useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {LedgerRow} from "@/components/finance/LedgerRow"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Segmented} from "@/components/ui/Segmented"
import {Select} from "@/components/ui/Select"
import {messageFor, statusOf} from "@/lib/api/client"
import {cancelBill, createBill, financeKeys, listAccounts, listBills, patchBill, settleBill} from "@/lib/api/finance"
import type {Account, Bill, BillPatch, Bucket, Direction, NewBill, Settlement} from "@/lib/api/financeTypes"
import {bucketLabel} from "@/lib/finance/labels"
import {addYearsIso, monthLabel, todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {type FinanceCtx} from "@/lib/api/finance"
import {useCreateRequest} from "@/lib/finance/createRequest"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {formatMoneyInput, maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"
import {accountName} from "@/lib/finance/accountName"

const GROUPS: Bucket[] = ["overdue", "today", "upcoming"]

const BADGE: Record<Bucket, {tone: "urgent" | "attention" | "neutral"; icon: typeof Clock}> = {
  overdue: {tone: "urgent", icon: AlertCircle},
  today: {tone: "attention", icon: Clock},
  upcoming: {tone: "neutral", icon: CalendarClock},
}

/** Everything a write here changes: the list, the balances it moves, the projection. */
const touched = (c: FinanceCtx) => [
  financeKeys.bills(c.mode, c.space, "payable"),
  financeKeys.bills(c.mode, c.space, "receivable"),
  financeKeys.accounts(c.mode, c.space),
  ["finance", c.mode, ...financeKeys.all(c.mode, c.space).slice(2), "projection"],
]

/**
 * F2 — a pagar e a receber.
 *
 * One table per direction, grouped overdue / today / upcoming with a subtotal,
 * in the order the API returns (earliest due first, so overdue leads). A row's
 * actions open in place; the list never disappears behind a modal.
 */
export function BillsView({direction: initial = "payable", focus}: {direction?: Direction; focus?: string} = {}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can, loading} = useFinanceSpaces()
  // `?direction=&bill=` (a recurrence's overdue occurrence links here): open on
  // that side and mark that bill.
  const [direction, setDirection] = useState<Direction>(initial)
  const [creating, setCreating] = useState(false)
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("bill", () => { if (loading || can("finance.write")) setCreating(true) })

  const bills = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, direction), queryFn: () => listBills(ctx, direction)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const rows = bills.data?.data ?? []
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, accountName(a)]))

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

      <div>
        <div className="min-w-0 space-y-6">
          {bills.isLoading ? (
            <div className="space-y-2" aria-busy><Skeleton className="h-5 w-32"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
          ) : bills.error ? (
            <ErrorBlock error={bills.error} onRetry={() => void bills.refetch()}/>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={<Receipt/>}
              title={t(`bills.list.empty.${direction}`)}
            />
          ) : (
            GROUPS.map(g => {
              const group = rows.filter(b => (b.bucket ?? "upcoming") === g)
              if (group.length === 0) return null
              const total = group.reduce((sum, b) => sum + b.amount, 0)
              return (
                <section key={g} aria-labelledby={`grp-${g}`} className="space-y-2">
                  <div className="flex items-baseline justify-between">
                    <h2 id={`grp-${g}`} className="text-sm font-medium text-foreground">{t(`bills.list.group.${g}`)}</h2>
                    <span data-numeric className="text-sm tabular-nums text-muted-foreground">{money(total)}</span>
                  </div>
                  <ul className="divide-y divide-border border-y border-border">
                    {group.map(b => (
                      <BillRow key={b.id} bill={b} current={b.id === focus} accountName={names.get(b.account_id)} accounts={accounts.data?.data ?? []}/>
                    ))}
                  </ul>
                </section>
              )
            })
          )}
        </div>

      </div>
      <Drawer open={creating && can("finance.write")} onClose={() => setCreating(false)} title={t("bills.list.newTitle")}>
        <NewBillPanel direction={direction} accounts={accounts.data?.data ?? []} onDone={() => setCreating(false)}/>
      </Drawer>
    </div>
  )
}

type Panel = "settle" | "edit" | "cancel" | null

function BillRow({bill, current, accountName, accounts}: {bill: Bill; current?: boolean; accountName?: string; accounts: Account[]}) {
  const {t} = useTranslation()
  const {can} = useFinanceSpaces()
  const [panel, setPanel] = useState<Panel>(null)
  const statement = bill.origin === "card_statement"
  const bucket = bill.bucket ?? "upcoming"
  const Icon = BADGE[bucket].icon
  return (
    <LedgerRow
      current={current}
      title={bill.description || t("bills.common.noDescription")}
      meta={<>
        {t("bills.row.due", {date: shortDate(bill.due_date)})}{accountName ? ` • ${accountName}` : ""}{bill.auto_settle ? ` • ${t(`bills.row.autoNote.${bill.direction}`)}` : ""}
        {statement && <> • {t("bills.row.statement")} • <Link href={`/console/finance/cards?card=${encodeURIComponent(bill.category_id)}`} className="underline-offset-4 hover:underline">{t("bills.row.viewStatement")}</Link></>}
      </>}
      // On a phone the group heading (Vencidas, A vencer) already says it.
      aside={<Badge tone={BADGE[bucket].tone}><Icon aria-hidden className="size-3"/>{bucketLabel(bucket)}</Badge>}
      asideOnPhone={false}
      amount={<span data-numeric>{money(bill.amount)}</span>}
      actions={(can("finance.settle") || can("finance.write")) && <>
        {can("finance.settle") && (
          <Button size="sm" variant={panel === "settle" ? "outline" : "ghost"} aria-expanded={panel === "settle"} onClick={() => setPanel(panel === "settle" ? null : "settle")}>
            {t(`bills.row.settle.${bill.direction}`)}
          </Button>
        )}
        {can("finance.write") && (
          <>
            <Button size="sm" variant="ghost" aria-expanded={panel === "edit"} onClick={() => setPanel(panel === "edit" ? null : "edit")}>{t("bills.common.edit")}</Button>
            {/* A statement is closed: corrected by a refund on the card, never canceled. */}
            {!statement && <Button size="sm" variant="ghost" aria-expanded={panel === "cancel"} onClick={() => setPanel(panel === "cancel" ? null : "cancel")}>{t("bills.row.delete")}</Button>}
          </>
        )}
      </>}
    >
      {panel === "settle" && <SettleForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "edit" && <EditForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "cancel" && <CancelConfirm bill={bill} onDone={() => setPanel(null)}/>}
    </LedgerRow>
  )
}

function useConflictReload(error: unknown) {
  const client = useQueryClient()
  const ctx = useFinanceCtx()
  const conflict = statusOf(error) === 409
  useEffect(() => {
    if (conflict) for (const key of touched(ctx)) void client.invalidateQueries({queryKey: key})
  }, [conflict, client, ctx])
  return conflict
}

/** `fe` carries the validation messages the form did not place on a control. */
function FormError({error, fe}: {error: unknown; fe?: {general?: string}}) {
  const {t} = useTranslation()
  const conflict = useConflictReload(error)
  if (!error) return null
  const text = conflict ? t("bills.conflict") : fe ? fe.general : messageFor(error)
  if (!text) return null
  return (
    <p role="alert" className="w-full text-sm text-danger">
      {text}
    </p>
  )
}

// items-start: each Field is itself a grid; stretched to the row's height, the
// spare space goes to its label row and pushes the input down out of line with
// the field beside it (the one with a hint is taller).
const inPanel = "mt-3 grid items-start gap-3 rounded-lg bg-surface p-3 sm:grid-cols-2 motion-safe:animate-in motion-safe:fade-in"

export function SettleForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation()
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount))
  const [date, setDate] = useState(todayIso())
  const [category, setCategory] = useState("")
  const fe = useFieldErrors(["paid_date", "paid_amount", "difference_category_id"])
  const settle = useFinanceMutation((c, body: Settlement, key) => settleBill(c, bill.id, body, key), touched, onDone, fe.set)

  // The amount is always visible and starts at the bill's own: paying exactly
  // what was owed is the common case, and a different amount is just an edit
  // of the same field — no extra toggle to find.
  const paid = parseMoney(amountText)
  const gap = paid === null ? 0 : paid - bill.amount
  const categories = accounts.filter(a => (a.class === "income" || a.class === "expense") && !a.system && !a.archived)
  const ready = paid !== null && (gap === 0 || category !== "") && !settle.isPending

  return (
    <form
      className={inPanel}
      onSubmit={e => {
        e.preventDefault()
        if (!ready) return
        fe.reset()
        const body: Settlement = {paid_date: date}
        if (gap !== 0) {
          body.paid_amount = paid!
          body.difference_category_id = category
        }
        settle.mutate(body)
      }}
    >
      <Field label={t(`bills.settle.date.${bill.direction}`)} htmlFor={`d-${bill.id}`} error={fe.of("paid_date")}>
        <DateField id={`d-${bill.id}`} min={limits.minDate} max={todayIso()} value={date} invalid={!!fe.of("paid_date")} onValueChange={v => { setDate(v); fe.clear("paid_date") }}/>
      </Field>
      <Field label={t(`bills.settle.amount.${bill.direction}`)} htmlFor={`v-${bill.id}`} error={fe.of("paid_amount")}>
        <Input id={`v-${bill.id}`} inputMode="decimal" value={amountText} aria-invalid={paid === null || !!fe.of("paid_amount")} aria-describedby={fe.of("paid_amount") ? `v-${bill.id}-error` : undefined} onChange={e => { setAmountText(maskMoney(e.target.value)); fe.clear("paid_amount") }}/>
      </Field>
      {gap !== 0 && (
        <>
          <Field label={t("bills.settle.diffCategory")} htmlFor={`c-${bill.id}`} error={fe.of("difference_category_id")}>
            <Select id={`c-${bill.id}`} value={category} {...fe.props("difference_category_id", `c-${bill.id}`)} onValueChange={v => { setCategory(v); fe.clear("difference_category_id") }} options={categories.map(a => ({value: a.id, label: accountName(a)}))}/>
          </Field>
          <p className="text-sm text-muted-foreground sm:pt-7">
            {t(gap > 0 ? "bills.settle.more" : "bills.settle.less", {amount: money(Math.abs(gap)), bill: money(bill.amount)})}
          </p>
        </>
      )}
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{t(`bills.settle.confirm.${bill.direction}`)}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        <FormError error={settle.error} fe={fe}/>
      </div>
    </form>
  )
}

function EditForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation()
  const {can} = useFinanceSpaces()
  const [description, setDescription] = useState(bill.description ?? "")
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount))
  const [due, setDue] = useState(bill.due_date)
  const [category, setCategory] = useState(bill.category_id)
  const [account, setAccount] = useState(bill.account_id)
  const [autoSettle, setAutoSettle] = useState(bill.auto_settle)
  const fe = useFieldErrors(["description", "amount", "due_date", "category_id", "account_id", "auto_settle"])
  const edit = useFinanceMutation((c, body: BillPatch, key) => patchBill(c, bill.id, body, key), touched, onDone, fe.set)
  // A card statement's amount is its purchases' and its "category" is the card.
  const statement = bill.origin === "card_statement"
  const amount = parseMoney(amountText)
  const cats = accounts.filter(a => a.class === (bill.direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)
  // Turning auto-settle ON is settling; turning it off only removes power.
  const showAuto = can("finance.settle") || bill.auto_settle

  return (
    <form
      className={inPanel}
      onSubmit={e => {
        e.preventDefault()
        if (amount === null) return
        const body: BillPatch = {}
        if (description !== (bill.description ?? "")) body.description = description
        if (amount !== bill.amount) body.amount = amount
        if (due !== bill.due_date) body.due_date = due
        if (category !== bill.category_id) body.category_id = category
        if (account !== bill.account_id) body.account_id = account
        if (autoSettle !== bill.auto_settle) body.auto_settle = autoSettle
        if (Object.keys(body).length === 0) return onDone()
        fe.reset()
        edit.mutate(body)
      }}
    >
      <Field label={t("bills.common.description")} htmlFor={`ed-${bill.id}`} error={fe.of("description")}><Input id={`ed-${bill.id}`} maxLength={limits.text.description} value={description} {...fe.props("description", `ed-${bill.id}`)} onChange={e => { setDescription(e.target.value); fe.clear("description") }}/></Field>
      {!statement && <Field label={t("bills.common.amount")} htmlFor={`ev-${bill.id}`} error={fe.of("amount")}><Input id={`ev-${bill.id}`} inputMode="decimal" value={amountText} aria-invalid={amount === null || !!fe.of("amount")} aria-describedby={fe.of("amount") ? `ev-${bill.id}-error` : undefined} onChange={e => { setAmountText(maskMoney(e.target.value)); fe.clear("amount") }}/></Field>}
      <Field label={t("bills.common.due")} htmlFor={`eu-${bill.id}`} error={fe.of("due_date")}><DateField id={`eu-${bill.id}`} min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={due} invalid={!!fe.of("due_date")} onValueChange={v => { setDue(v); fe.clear("due_date") }}/></Field>
      {!statement && (
        <Field label={t("bills.common.category")} htmlFor={`ec-${bill.id}`} error={fe.of("category_id")}>
          <Select id={`ec-${bill.id}`} value={category} {...fe.props("category_id", `ec-${bill.id}`)} onValueChange={v => { setCategory(v); fe.clear("category_id") }} options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
        </Field>
      )}
      <Field label={t(`bills.common.payWith.${bill.direction}`)} htmlFor={`ea-${bill.id}`} error={fe.of("account_id")}>
        <Select id={`ea-${bill.id}`} value={account} {...fe.props("account_id", `ea-${bill.id}`)} onValueChange={v => { setAccount(v); fe.clear("account_id") }} options={assets.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      {showAuto && (
        <label className="flex items-center gap-2 self-end text-sm">
          <Switch checked={autoSettle} onCheckedChange={setAutoSettle} disabled={!autoSettle && !can("finance.settle")} aria-label={t(`bills.common.auto.${bill.direction}`)}/>
          {t(`bills.common.auto.${bill.direction}`)}
        </label>
      )}
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={amount === null || edit.isPending}>{t("bills.common.save")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        <FormError error={edit.error} fe={fe}/>
      </div>
    </form>
  )
}

function CancelConfirm({bill, onDone}: {bill: Bill; onDone: () => void}) {
  const {t} = useTranslation()
  const cancel = useFinanceMutation((c, _: void, key) => cancelBill(c, bill.id, key), touched, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">
        {t("bills.delete.confirm", {name: bill.description || t("bills.delete.fallbackName"), month: monthLabel(bill.competence_date)})}
      </p>
      <Button size="sm" variant="outline" onClick={onDone}>{t("bills.common.keep")}</Button>
      <Button size="sm" variant="danger" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{t("bills.row.delete")}</Button>
      <FormError error={cancel.error}/>
    </div>
  )
}

function NewBillPanel({direction, accounts, onDone}: {direction: Direction; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation()
  const {can} = useFinanceSpaces()
  const [description, setDescription] = useState("")
  const [amountText, setAmountText] = useState("")
  const [due, setDue] = useState(todayIso())
  const [competence, setCompetence] = useState("")
  const cats = accounts.filter(a => a.class === (direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)
  const [category, setCategory] = useState("")
  const [account, setAccount] = useState("")
  const [autoSettle, setAutoSettle] = useState(false)
  // Paid already (cash received at the counter, a bill paid on the spot): the
  // bill is created and settled right after, each with its own intent; if only
  // the second fails, the bill exists and the form offers to retry the payment.
  const [paidNow, setPaidNow] = useState(false)
  const [paidOn, setPaidOn] = useState(todayIso())
  //
  // The list is refreshed once, after the LAST step: a create that a settle
  // follows invalidates nothing, or the refetch in between shows the bill open
  // under "A pagar" for as long as the settle takes and then drops it. If the
  // settle fails the bill really is open, so the list is refreshed then too,
  // and the person is told: the panel (and its inline retry) may already be
  // closed, and they would believe the bill was paid.
  const settle = useFinanceMutation(
    (c, v: {id: string; body: Settlement}, key) => settleBill(c, v.id, v.body, key), touched,
    () => { toast.success(t(`bills.new.settled.${direction}`)); onDone() },
    () => { toast.error(t(`bills.new.settleError.${direction}`)) },
    {invalidateOnError: true},
  )
  const fe = useFieldErrors(["description", "amount", "due_date", "competence_date", "category_id", "account_id"])
  const create = useFinanceMutation(
    (c, v: {body: NewBill; paidOn?: string}, key) => createBill(c, v.body, key),
    (c, _created, v) => (v.paidOn ? [] : touched(c)),
    (created, v) => (v.paidOn ? settle.mutate({id: created.id, body: {paid_date: v.paidOn}}) : onDone()),
    fe.set,
  )
  const amount = parseMoney(amountText)
  const ready = amount !== null && category !== "" && account !== "" && due !== "" && !create.isPending && !create.isSuccess

  return (
    <div className="space-y-3">
      <form
        className="space-y-3"
        onSubmit={e => {
          e.preventDefault()
          if (!ready) return
          fe.reset()
          create.mutate({
            body: {
              direction, amount: amount!, category_id: category, account_id: account, description: description || undefined,
              due_date: due, competence_date: competence || undefined, auto_settle: (autoSettle && !paidNow) || undefined,
            },
            paidOn: paidNow ? paidOn : undefined,
          })
        }}
      >
        <Field label={t("bills.common.description")} htmlFor="nb-desc" error={fe.of("description")}><Input id="nb-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "nb-desc")} onChange={e => { setDescription(e.target.value); fe.clear("description") }} autoFocus/></Field>
        <Field label={t("bills.common.amount")} htmlFor="nb-amount" required error={fe.of("amount")}><Input id="nb-amount" inputMode="decimal" placeholder={moneyPlaceholder()} value={amountText} {...fe.props("amount", "nb-amount")} onChange={e => { setAmountText(maskMoney(e.target.value)); fe.clear("amount") }}/></Field>
        <Field label={t("bills.common.due")} htmlFor="nb-due" required error={fe.of("due_date")}><DateField id="nb-due" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={due} invalid={!!fe.of("due_date")} onValueChange={v => { setDue(v); fe.clear("due_date") }}/></Field>
        <Field label={t("bills.new.competence")} htmlFor="nb-comp" error={fe.of("competence_date")}>
          <DateField id="nb-comp" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={competence} invalid={!!fe.of("competence_date")} onValueChange={v => { setCompetence(v); fe.clear("competence_date") }} placeholder={t("bills.new.competencePlaceholder")}/>
        </Field>
        <Field label={t("bills.common.category")} htmlFor="nb-cat" required error={fe.of("category_id")} hint={cats.length === 0 ? t(`bills.noCategory.${direction === "payable" ? "expense" : "income"}`) : undefined}>
          <Select id="nb-cat" value={category} {...fe.props("category_id", "nb-cat")} onValueChange={v => { setCategory(v); fe.clear("category_id") }} options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
        </Field>
        {can("finance.settle") && (
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <Switch checked={paidNow} onCheckedChange={setPaidNow} aria-label={t(`bills.new.paidNow.${direction}`)}/>
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
          <Field label={t(paidNow ? `bills.new.paidWith.${direction}` : `bills.common.payWith.${direction}`)} htmlFor="nb-acct" required error={fe.of("account_id")} hint={assets.length === 0 ? t("bills.noAccount") : undefined}>
            <Select id="nb-acct" value={account} {...fe.props("account_id", "nb-acct")} onValueChange={v => { setAccount(v); fe.clear("account_id") }} options={assets.map(a => ({value: a.id, label: accountName(a)}))}/>
          </Field>
          {can("finance.settle") && !paidNow && (
            <label className="flex min-h-11 items-center gap-2 text-sm">
              <Switch checked={autoSettle} onCheckedChange={setAutoSettle} aria-label={t(`bills.common.auto.${direction}`)}/>
              {t(`bills.common.auto.${direction}`)}
            </label>
          )}
        </fieldset>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="brand" size="sm" disabled={!ready}>{t("bills.new.create")}</Button>
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
  )
}
