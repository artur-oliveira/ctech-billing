"use client"

import limits from "@/lib/limits.json"
import {Button, EmptyState, Field, Input, Skeleton, Switch} from "@aoctech/ui"
import {Drawer} from "@/components/ui/ConsoleOverlay"
import {useQuery} from "@tanstack/react-query"
import {ChevronDown, Repeat} from "lucide-react"
import Link from "next/link"
import {useEffect, useId, useRef, useState} from "react"
import {useTranslation} from "react-i18next"

import {ExceptionsFields, PatternFields} from "@/components/finance/ExpressionEditor"
import {LedgerRow} from "@/components/finance/LedgerRow"
import {OccurrenceTimeline, type TimelineEntry} from "@/components/finance/OccurrenceTimeline"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Segmented} from "@/components/ui/Segmented"
import {Select} from "@/components/ui/Select"
import {messageFor, problemCode} from "@/lib/api/client"
import {
  archiveRecurrence, createRecurrence, type FinanceCtx, financeKeys, getRecurrenceOccurrences, listAccounts, listRecurrences,
  patchRecurrence, previewRecurrence,
} from "@/lib/api/finance"
import type {Account, Adjust, Direction, ExpressionJSON, NewRecurrence, Occurrence, Recurrence, RecurrencePatch} from "@/lib/api/financeTypes"
import {defaultModel, describeModel, type EditorModel, fromExpression, toExpression, validate} from "@/lib/finance/expression"
import {currentLocale, t} from "@/lib/i18n"
import {addYearsIso, todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useCreateRequest} from "@/lib/finance/createRequest"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {dayMonth, money, shortDate} from "@/lib/format"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {formatMoneyInput, maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"
import {accountName} from "@/lib/finance/accountName"

const PREVIEW_COUNT = 6
const DEBOUNCE_MS = 300
const adjustOptions = (): {value: Adjust; label: string}[] => [
  {value: "roll_forward", label: t("bills.rec.adjust.roll_forward")},
  {value: "none", label: t("bills.rec.adjust.none")},
]
const touched = (c: FinanceCtx) => [financeKeys.recurrences(c.mode, c.space), [...financeKeys.all(c.mode, c.space), "projection"]]

function ruleOf(r: Recurrence): string {
  const model = fromExpression(r.expression)
  return model ? describeModel(model) : t("bills.rec.customRule")
}

/**
 * F4 — recorrências: a list, and a side panel to create or edit one. The rule
 * editor previews the next occurrences while it is edited; the preview IS the
 * confirmation, before anything is saved.
 */
export function RecurrencesView() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can, loading} = useFinanceSpaces()
  const [panel, setPanel] = useState<{mode: "new"} | {mode: "edit"; rec: Recurrence} | null>(null)
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("recurrence", () => { if (loading || can("finance.write")) setPanel({mode: "new"}) })
  const shown = panel?.mode === "new" && !can("finance.write") ? null : panel
  const recs = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, accountName(a)]))
  const active = (recs.data?.data ?? []).filter(r => !r.archived)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("bills.rec.title")}</h1>
        {can("finance.write") && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setPanel({mode: "new"})}>{t("bills.rec.new")}</Button>
        )}
      </div>

      <Drawer open={shown !== null} onClose={() => setPanel(null)} size="lg"
        title={shown?.mode === "edit" ? t("bills.rec.edit") : t("bills.rec.new")}>
        {shown && (
          <RecurrencePanel
            key={shown.mode === "edit" ? shown.rec.id : "new"}
            editing={shown.mode === "edit" ? shown.rec : undefined}
            accounts={accounts.data?.data ?? []}
            onDone={() => setPanel(null)}
          />
        )}
      </Drawer>

      {recs.isLoading ? (
        <div className="space-y-2" aria-busy><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
      ) : recs.error ? (
        <ErrorBlock error={recs.error} onRetry={() => void recs.refetch()}/>
      ) : active.length === 0 ? (
        <EmptyState
          icon={<Repeat/>}
          title={t("bills.rec.empty")}
          description={t("bills.rec.emptyHint")}
        />
      ) : (
        <ul className="divide-y divide-border border-y border-border">
          {active.map(r => (
            <RecurrenceRow key={r.id} rec={r} account={names.get(r.account_id)} onEdit={() => setPanel({mode: "edit", rec: r})}/>
          ))}
        </ul>
      )}
    </div>
  )
}

function RecurrenceRow({rec, account, onEdit}: {rec: Recurrence; account?: string; onEdit: () => void}) {
  const {t} = useTranslation()
  const {can} = useFinanceSpaces()
  const [confirming, setConfirming] = useState(false)
  // A disclosure: the detail opens in place under the row and the focus stays
  // on the button, which says whether it is open (aria-expanded).
  const [open, setOpen] = useState(false)
  const detailId = useId()
  const archive = useFinanceMutation((c, _: void, key) => archiveRecurrence(c, rec.id, key), touched, () => setConfirming(false))
  return (
    <LedgerRow
      title={rec.description || t("bills.common.noDescription")}
      meta={<>
        {/* A phone has no column for the direction, so the line says it. */}
        <span className="sm:hidden">{t(`bills.direction.${rec.direction}`)} • </span>
        {ruleOf(rec)}{account ? ` • ${account}` : ""}{rec.auto_settle ? ` • ${t(`bills.row.autoNote.${rec.direction}`)}` : ""}
      </>}
      aside={<span className="text-xs text-muted-foreground">{t(`bills.direction.${rec.direction}`)}</span>}
      asideOnPhone={false}
      amount={<span data-numeric>{money(rec.amount)}</span>}
      actions={<>
        <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={detailId} onClick={() => setOpen(v => !v)}>
          {t("bills.rec.view")}
          <ChevronDown aria-hidden className={`size-4 transition-transform duration-200 ease-out motion-reduce:transition-none ${open ? "rotate-180" : ""}`}/>
        </Button>
        {can("finance.write") && <>
          <Button size="sm" variant="ghost" onClick={onEdit}>{t("bills.common.edit")}</Button>
          <Button size="sm" variant="ghost" aria-expanded={confirming} onClick={() => setConfirming(v => !v)}>{t("bills.rec.end")}</Button>
        </>}
      </>}
    >
      {open && <RecurrenceDetail id={detailId} rec={rec}/>}
      {confirming && (
        <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
          <p className="text-muted-foreground">{t("bills.rec.endConfirm")}</p>
          <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>{t("bills.common.keep")}</Button>
          <Button size="sm" variant="danger" disabled={archive.isPending} onClick={() => archive.mutate()}>{t("bills.rec.confirm")}</Button>
          {archive.error && <p role="alert" className="w-full text-danger">{messageFor(archive.error)}</p>}
        </div>
      )}
    </LedgerRow>
  )
}

/**
 * F4's inline detail (UX batch 3): a timeline of what is to come (the bills
 * already made and not yet due, then the dates the rule will make) and of what
 * happened (paid, overdue with a way to it, skipped), most recent first.
 */
function RecurrenceDetail({id, rec}: {id: string; rec: Recurrence}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.recurrenceOccurrences(ctx.mode, ctx.space, rec.id), queryFn: () => getRecurrenceOccurrences(ctx, rec.id)})
  const name = rec.description || t("bills.common.noDescription")
  const made = (o: NonNullable<typeof q.data>["history"][number]): TimelineEntry => ({
    key: o.bill_id, nominal: o.nominal, due: o.due, kind: o.state, amount: money(o.amount),
    state: o.state === "paid" && o.paid_date ? t("bills.rec.state.paidOn", {date: shortDate(o.paid_date)}) : t(`bills.rec.state.${o.state}`),
    action: o.state === "overdue"
      ? <Link href={`/console/finance/bills?direction=${rec.direction}&bill=${encodeURIComponent(o.bill_id)}`} className="inline-flex items-center text-sm text-foreground underline underline-offset-4 hover:text-brand-700 touch-target">{t(`bills.rec.openBill.${rec.direction}`)}</Link>
      : undefined,
  })
  const next: TimelineEntry[] = [
    ...(q.data?.history ?? []).filter(o => o.state === "forecast").map(made),
    ...(q.data?.upcoming ?? []).map(o => ({key: `u-${o.nominal}`, nominal: o.nominal, due: o.due, kind: "upcoming" as const, state: t("bills.rec.state.upcoming"), amount: money(rec.amount)})),
  ]
  const past = (q.data?.history ?? []).filter(o => o.state !== "forecast").reverse().map(made)
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
  )
}

/**
 * Ending a recurrence keeps the bills it already made after the new end (they
 * exist and can be edited or cancelled one by one); the confirmation says which,
 * and that one set to auto-settle is still paid on its date by the daily job.
 */
function StillGoing({rec, end}: {rec: Recurrence; end: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.recurrenceOccurrences(ctx.mode, ctx.space, rec.id), queryFn: () => getRecurrenceOccurrences(ctx, rec.id)})
  const going = (q.data?.history ?? []).filter(o => o.nominal > end && (o.state === "forecast" || o.state === "overdue"))
  if (going.length === 0) return null
  const dates = new Intl.ListFormat(currentLocale(), {type: "conjunction"}).format(going.map(o => dayMonth(o.due)))
  return (
    <div className="w-full space-y-1 text-muted-foreground">
      <p>
        {t("bills.rec.stillGoing", {count: going.length, dates})}{" "}
        <Link href={`/console/finance/bills?direction=${rec.direction}`} className="inline-flex items-center text-foreground underline underline-offset-4 hover:text-brand-700 touch-target">
          {t(`bills.rec.openBill.${rec.direction}`)}
        </Link>
      </p>
      {going.some(o => o.auto_settle) && <p>{t(`bills.rec.stillAuto.${rec.direction}`)}</p>}
    </div>
  )
}

/**
 * The next occurrences for a valid model, debounced and last-answer-wins: a
 * slow reply to an older edit never overwrites the preview of a newer one.
 */
function usePreview(ctx: FinanceCtx, expression: ExpressionJSON, start: string, end: string, adjust: Adjust, from: string, valid: boolean) {
  const [state, setState] = useState<{occ: Occurrence[]; loading: boolean; error: unknown; done: boolean}>({occ: [], loading: false, error: null, done: false})
  const seq = useRef(0)
  const body = JSON.stringify({expression, start, end, adjust, from})
  useEffect(() => {
    if (!valid || !start) return
    const mine = ++seq.current
    const abort = new AbortController()
    const t = setTimeout(() => {
      setState(s => ({...s, loading: true}))
      const parsed = JSON.parse(body) as {expression: ExpressionJSON; start: string; end: string; adjust: Adjust; from: string}
      previewRecurrence(ctx, {
        expression: parsed.expression, start: parsed.start, end: parsed.end || undefined,
        business_day_adjust: parsed.adjust, from: parsed.from > parsed.start ? parsed.from : parsed.start, count: PREVIEW_COUNT,
      }, abort.signal)
        .then(r => { if (mine === seq.current) setState({occ: r.data, loading: false, error: null, done: true}) })
        .catch(e => { if (mine === seq.current && !abort.signal.aborted) setState({occ: [], loading: false, error: e, done: true}) })
    }, DEBOUNCE_MS)
    return () => {
      clearTimeout(t)
      abort.abort()
    }
  }, [ctx, body, valid, start])
  return state
}

/** The day after a civil date. */
function nextDay(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number)
  const next = new Date(y, m - 1, d + 1, 12)
  return `${next.getFullYear()}-${String(next.getMonth() + 1).padStart(2, "0")}-${String(next.getDate()).padStart(2, "0")}`
}

function RecurrencePanel({editing, accounts, onDone}: {editing?: Recurrence; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [direction, setDirection] = useState<Direction>(editing?.direction ?? "payable")
  const [description, setDescription] = useState(editing?.description ?? "")
  const [amountText, setAmountText] = useState(editing ? formatMoneyInput(editing.amount) : "")
  const [category, setCategory] = useState(editing?.category_id ?? "")
  const [account, setAccount] = useState(editing?.account_id ?? "")
  const [start, setStart] = useState(editing?.start ?? todayIso())
  const [end, setEnd] = useState(editing?.end ?? "")
  const [adjust, setAdjust] = useState<Adjust>(editing?.business_day_adjust ?? "roll_forward")
  const [autoSettle, setAutoSettle] = useState(editing?.auto_settle ?? false)
  const [model, setModel] = useState<EditorModel>(() => (editing && fromExpression(editing.expression)) || defaultModel(todayIso()))

  const errors = editing ? [] : validate(model)
  // Creating: the dates from today. Editing: the rule is fixed, so the preview
  // shows what is left after today under the end being edited, which is what
  // tells the person that an end will close the recurrence.
  const preview = usePreview(ctx, editing ? editing.expression : toExpression(model), start, end,
    editing ? editing.business_day_adjust : adjust, editing ? nextDay(todayIso()) : todayIso(), errors.length === 0)
  // An end the server said leaves nothing to come (422 recurrence_would_end):
  // saving it again asks for confirmation instead of repeating the request.
  const [endsAt, setEndsAt] = useState<string | null>(null)
  const [confirmEnd, setConfirmEnd] = useState(false)
  const lastPatch = useRef<RecurrencePatch>({})
  const amount = parseMoney(amountText)
  const cats = accounts.filter(a => a.class === (direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)

  const [more, setMore] = useState(false)
  // "end" only has a control on screen when editing or when more options is open;
  // otherwise its error is shown as the general message.
  const fe = useFieldErrors(["description", "amount", "category_id", "account_id", ...(editing ? [] : ["start"]), ...(editing || more ? ["end"] : [])])
  const create = useFinanceMutation((c, body: NewRecurrence, key) => createRecurrence(c, body, key), touched, onDone, fe.set)
  const patch = useFinanceMutation((c, body: RecurrencePatch, key) => patchRecurrence(c, editing!.id, body, key), touched, onDone, e => {
    if (problemCode(e) === "recurrence_would_end" && lastPatch.current.end) {
      setEndsAt(lastPatch.current.end)
      setConfirmEnd(true)
      return
    }
    fe.set(e)
  })
  const pending = create.isPending || patch.isPending
  const ready = amount !== null && category !== "" && account !== "" && errors.length === 0 && !pending
  const showAuto = can("finance.settle") || (editing?.auto_settle ?? false)

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!ready) return
    fe.reset()
    if (!editing) {
      create.mutate({
        direction, amount: amount!, category_id: category, account_id: account, description: description || undefined,
        expression: toExpression(model), start, end: end || undefined, business_day_adjust: adjust, auto_settle: autoSettle || undefined,
      })
      return
    }
    const body: RecurrencePatch = {}
    if (amount !== editing.amount) body.amount = amount!
    if (category !== editing.category_id) body.category_id = category
    if (account !== editing.account_id) body.account_id = account
    if (description !== (editing.description ?? "")) body.description = description
    if (autoSettle !== editing.auto_settle) body.auto_settle = autoSettle
    if (end && end !== (editing.end ?? "")) body.end = end
    if (Object.keys(body).length === 0) return onDone()
    lastPatch.current = body
    if (body.end && body.end === endsAt) {
      setConfirmEnd(true)
      return
    }
    patch.mutate(body)
  }

  const nextDates = preview.occ.slice(0, PREVIEW_COUNT)
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
  )

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">

        {!editing && (
          <Segmented
            label={t("bills.direction.label")}
            value={direction}
            onValueChange={d => { setDirection(d); setCategory("") }}
            options={(["payable", "receivable"] as Direction[]).map(d => ({value: d, label: t(`bills.direction.${d}`)}))}
          />
        )}
      </div>
      <form className="space-y-4" onSubmit={submit}>
        <div className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0">
          <Field label={t("bills.common.description")} htmlFor="rc-desc" error={fe.of("description")} className="sm:col-span-2"><Input id="rc-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "rc-desc")} onChange={e => { setDescription(e.target.value); fe.clear("description") }}/></Field>
          <Field label={t("bills.common.amount")} htmlFor="rc-amount" required error={fe.of("amount")}><Input id="rc-amount" inputMode="decimal" placeholder={moneyPlaceholder()} value={amountText} {...fe.props("amount", "rc-amount")} onChange={e => { setAmountText(maskMoney(e.target.value)); fe.clear("amount") }}/></Field>
          <Field label={t("bills.common.category")} htmlFor="rc-cat" required error={fe.of("category_id")} hint={cats.length === 0 ? t(`bills.noCategory.${direction === "payable" ? "expense" : "income"}`) : undefined}>
            <Select id="rc-cat" value={category} {...fe.props("category_id", "rc-cat")} onValueChange={v => { setCategory(v); fe.clear("category_id") }} options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
          </Field>
          <Field label={t(`bills.common.payWith.${direction}`)} htmlFor="rc-acct" required error={fe.of("account_id")} hint={assets.length === 0 ? t("bills.noAccount") : undefined}>
            <Select id="rc-acct" value={account} {...fe.props("account_id", "rc-acct")} onValueChange={v => { setAccount(v); fe.clear("account_id") }} options={assets.map(a => ({value: a.id, label: accountName(a)}))}/>
          </Field>
          {!editing && (
            <>
              <PatternFields model={model} start={start} errors={errors} onChange={setModel}/>
              <Field label={t("bills.rec.startsOn")} htmlFor="rc-start" error={fe.of("start")}><DateField id="rc-start" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxFutureYears)} value={start} invalid={!!fe.of("start")} onValueChange={v => { setStart(v); fe.clear("start") }}/></Field>
            </>
          )}
          {editing && (
            <Field label={t("bills.rec.endsOn")} htmlFor="rc-end-edit" error={fe.of("end")}><DateField id="rc-end-edit" min={start || limits.minDate} max={addYearsIso(start || todayIso(), limits.maxRecurrenceYears)} value={end} invalid={!!fe.of("end")} onValueChange={v => { setEnd(v); fe.clear("end") }} placeholder={t("bills.rec.noEnd")}/></Field>
          )}
        </div>

        {editing ? (
          <>
            <p className="text-sm text-muted-foreground">
              {t("bills.rec.editNote", {rule: ruleOf(editing)})}
            </p>
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
                  <Field label={t("bills.rec.endsOn")} htmlFor="rc-end" error={fe.of("end")}><DateField id="rc-end" min={start || limits.minDate} max={addYearsIso(start || todayIso(), limits.maxRecurrenceYears)} value={end} invalid={!!fe.of("end")} onValueChange={v => { setEnd(v); fe.clear("end") }} placeholder={t("bills.rec.noEnd")}/></Field>
                  <Field label={t("bills.rec.weekend")} htmlFor="rc-adjust">
                    <Select id="rc-adjust" value={adjust} onValueChange={v => setAdjust(v as Adjust)} options={adjustOptions()}/>
                  </Field>
                  <ExceptionsFields model={model} errors={errors} onChange={setModel}/>
                </div>
              )}
            </div>
          </>
        )}

        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          {showAuto && (
            <label className="flex items-center gap-2 text-sm">
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
        {confirmEnd && editing && (
          <div className="flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
            <p role="alert" className="w-full text-foreground">{t("bills.rec.endsConfirm")}</p>
            <StillGoing rec={editing} end={endsAt ?? ""}/>
            <Button type="button" size="sm" variant="outline" onClick={() => setConfirmEnd(false)}>{t("bills.rec.back")}</Button>
            <Button type="button" size="sm" variant="danger" disabled={patch.isPending}
              onClick={() => patch.mutate({...lastPatch.current, archive: true})}>{t("bills.rec.endAndArchive")}</Button>
          </div>
        )}
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      </form>
    </div>
  )
}
