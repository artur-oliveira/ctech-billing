"use client"

import {Badge, Button, Field, Input, Skeleton} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {wholeSpace} from "@/components/finance/TransferPanel"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {financeKeys, getImport, ignoreLine, listAccounts, matchLine, newFromLine, reopenLine} from "@/lib/api/finance"
import type {Account, ImportLine} from "@/lib/api/financeTypes"
import {accountName} from "@/lib/finance/accountName"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {shortDate, signedMoney} from "@/lib/format"
import limits from "@/lib/limits.json"

const TABS = ["pending", "done", "ignored"] as const
type Tab = (typeof TABS)[number]

const tabOf = (l: ImportLine): Tab => (l.status === "pending" ? "pending" : l.status === "ignored" ? "ignored" : "done")

/**
 * One import's lines, in three tabs: what still waits for a decision, what was
 * reconciled (a bill settled, or a bill created and settled), what was ignored.
 * Nothing settles without the person's click.
 */
export function ImportLines({importId}: {importId: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const q = useQuery({queryKey: financeKeys.importDetail(ctx.mode, ctx.space, importId), queryFn: () => getImport(ctx, importId)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const [tab, setTab] = useState<Tab>("pending")

  if (q.isLoading) return <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/></div>
  if (q.error || !q.data) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
  const lines = q.data.lines
  const shown = lines.filter(l => tabOf(l) === tab)
  const rejected = q.data.import.rejected

  return (
    <section aria-label={t("finance.import.lines")} className="space-y-3">
      <div role="tablist" aria-label={t("finance.import.lines")} className="flex max-w-full flex-wrap items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5">
        {TABS.map(x => (
          <button key={x} type="button" role="tab" id={`im-tab-${x}`} aria-selected={tab === x} aria-controls="im-panel" onClick={() => setTab(x)}
            className={`rounded-md px-3 py-1 text-sm transition-colors ${tab === x ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
            {t(`finance.import.tabs.${x}`)} <span className="tabular-nums">({lines.filter(l => tabOf(l) === x).length})</span>
          </button>
        ))}
      </div>
      <div role="tabpanel" id="im-panel" aria-labelledby={`im-tab-${tab}`}>
        {shown.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">{t(`finance.import.empty.${tab}`)}</p>
        ) : (
          <ul className="divide-y divide-border border-y border-border">
            {shown.map(l => <LineRow key={l.n} importId={importId} line={l} accounts={accounts.data?.data ?? []}/>)}
          </ul>
        )}
      </div>
      {rejected.length > 0 && (
        <details className="text-sm text-muted-foreground">
          <summary>{t("finance.import.rejected", {count: q.data.import.rejected_count})}</summary>
          <ul className="mt-1 space-y-0.5">
            {rejected.map(r => <li key={r.line}>{t("finance.import.rejectedLine", {line: r.line, reason: t(`finance.import.reasons.${r.reason}`)})}</li>)}
          </ul>
        </details>
      )}
    </section>
  )
}

function LineRow({importId, line, accounts}: {importId: string; line: ImportLine; accounts: Account[]}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const client = useQueryClient()
  const {can} = useFinanceSpaces()
  const canImport = can("finance.import")
  const canSettle = canImport && can("finance.write") && can("finance.settle")
  const [creating, setCreating] = useState(false)
  const [billId, setBillId] = useState(line.candidates[0]?.id ?? "")
  // A refusal (another tab decided this line, or the bill was paid elsewhere)
  // is shown, and the import is read again so the row tells the truth.
  const refresh = () => void client.invalidateQueries({queryKey: financeKeys.importDetail(ctx.mode, ctx.space, importId)})
  const match = useFinanceMutation((c, _: void, key) => matchLine(c, importId, line.n, {bill_id: billId}, key), wholeSpace, undefined, refresh)
  const ignore = useFinanceMutation((c, _: void, key) => ignoreLine(c, importId, line.n, key), wholeSpace, undefined, refresh)
  const reopen = useFinanceMutation((c, _: void, key) => reopenLine(c, importId, line.n, key), wholeSpace, undefined, refresh)
  const error = match.error ?? ignore.error ?? reopen.error
  const busy = match.isPending || ignore.isPending || reopen.isPending
  const candidate = line.candidates.find(b => b.id === billId)

  return (
    <li className="py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-foreground">{line.description || t("finance.import.noDescription")}</p>
          <p className="text-xs text-muted-foreground">{shortDate(line.date)}</p>
        </div>
        <span data-numeric className="w-28 text-right text-sm tabular-nums">{signedMoney(line.amount)}</span>
        {line.status === "matched" && <Badge tone="neutral">{t("finance.import.matched")}</Badge>}
        {line.status === "created" && <Badge tone="neutral">{t("finance.import.created")}</Badge>}
        {line.status === "ignored" && canImport && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => reopen.mutate()}>{t("finance.import.reopen")}</Button>
        )}
      </div>
      {line.status === "pending" && (
        <div className="mt-2 space-y-2">
          {line.candidates.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t("finance.import.noCandidate")}</p>
          ) : line.candidates.length === 1 && candidate ? (
            <p className="text-sm text-foreground">{t("finance.import.candidate", {description: candidate.description || t("finance.import.noDescription"), date: shortDate(candidate.due_date)})}</p>
          ) : (
            <Field label={t("finance.import.which")} htmlFor={`im-bill-${line.n}`}>
              <Select id={`im-bill-${line.n}`} aria-label={t("finance.import.which")} value={billId} onValueChange={setBillId} className="w-72"
                options={line.candidates.map(b => ({value: b.id, label: t("finance.import.candidate", {description: b.description || t("finance.import.noDescription"), date: shortDate(b.due_date)})}))}/>
            </Field>
          )}
          {(canImport || canSettle) && !creating && (
            <div className="flex flex-wrap gap-1">
              {canSettle && candidate && <Button size="sm" variant="brand" disabled={busy} onClick={() => match.mutate()}>{t("finance.import.match")}</Button>}
              {canSettle && <Button size="sm" variant="outline" disabled={busy} onClick={() => setCreating(true)}>{t("finance.import.new")}</Button>}
              {canImport && <Button size="sm" variant="ghost" disabled={busy} onClick={() => ignore.mutate()}>{t("finance.import.ignore")}</Button>}
            </div>
          )}
          {creating && <NewBillForm importId={importId} line={line} accounts={accounts} onDone={() => setCreating(false)} onRefused={refresh}/>}
        </div>
      )}
      {error ? <p role="alert" className="mt-1 text-sm text-danger">{messageFor(error)}</p> : null}
    </li>
  )
}

/** A line nobody forecast becomes a bill created and settled at once, under a
 *  category of its direction: an expense for money out, an income for money in. */
function NewBillForm({importId, line, accounts, onDone, onRefused}: {
  importId: string; line: ImportLine; accounts: Account[]; onDone: () => void; onRefused: () => void
}) {
  const {t} = useTranslation()
  const cls = line.amount < 0 ? "expense" : "income"
  const cats = accounts.filter(a => a.class === cls && !a.system && !a.archived)
  const [category, setCategory] = useState("")
  const [description, setDescription] = useState(line.description)
  const create = useFinanceMutation(
    (c, body: {category_id: string; description?: string}, key) => newFromLine(c, importId, line.n, body, key),
    wholeSpace, onDone, onRefused,
  )
  return (
    <form
      aria-label={t("finance.import.new")}
      className="grid items-start gap-x-4 gap-y-3 rounded-lg bg-surface p-3 sm:grid-cols-2 [&>*]:min-w-0"
      onSubmit={e => {
        e.preventDefault()
        if (!category || create.isPending) return
        create.mutate({category_id: category, description: description.trim() || undefined})
      }}
    >
      <Field label={t("finance.import.category")} htmlFor={`im-cat-${line.n}`} hint={cats.length === 0 ? t(`finance.import.noCategory.${cls}`) : undefined}>
        <Select id={`im-cat-${line.n}`} aria-label={t("finance.import.category")} value={category} onValueChange={setCategory}
          options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <Field label={t("finance.import.description")} htmlFor={`im-desc-${line.n}`}>
        <Input id={`im-desc-${line.n}`} maxLength={limits.text.description} value={description} onChange={e => setDescription(e.target.value)}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!category || create.isPending}>{t("finance.import.create")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("finance.import.back")}</Button>
        {create.error ? <p role="alert" className="text-sm text-danger">{messageFor(create.error)}</p> : null}
      </div>
    </form>
  )
}
