"use client"

import {Button, EmptyState, Field, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {FileUp} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {CsvMappingForm} from "@/components/finance/CsvMappingForm"
import {ImportLines} from "@/components/finance/ImportLines"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {messageFor, problemCode} from "@/lib/api/client"
import {type FinanceCtx, fileToBase64, financeKeys, listAccounts, listImports, uploadImport} from "@/lib/api/finance"
import type {ImportFormat, ImportSummary} from "@/lib/api/financeTypes"
import {accountName, byAccountName} from "@/lib/finance/accountName"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {shortDate} from "@/lib/format"
import {currentLocale, t as tr} from "@/lib/i18n"

/** OFX by its extension; anything else is read as CSV with the account's columns. */
export const formatOf = (name: string): ImportFormat => (/\.(ofx|qfx)$/i.test(name) ? "ofx" : "csv")

/** "12 lançamentos novos, 3 já importados antes, 1 linha não lida", or "nada novo". */
export function uploadSummary(s: ImportSummary): string {
  if (s.lines === 0 && s.rejected_count === 0) return tr("finance.import.nothingNew")
  const parts = [tr("finance.import.added", {count: s.lines})]
  if (s.duplicates > 0) parts.push(tr("finance.import.duplicates", {count: s.duplicates}))
  if (s.rejected_count > 0) parts.push(tr("finance.import.rejected", {count: s.rejected_count}))
  return parts.join(", ")
}

/**
 * F6 — importar extrato. One account at a time: upload its bank statement
 * (OFX, or CSV read with the columns saved for it), then go through its lines.
 * The file itself is not kept; its lines are, for 90 days.
 */
export function ImportView({account: initial = ""}: {account?: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const all = accounts.data?.data ?? []
  const cash = all.filter(a => a.class === "asset" && !a.system && !a.archived).sort(byAccountName(currentLocale()))
  const [picked, setPicked] = useState(initial)
  const account = cash.find(a => a.id === picked) ?? cash[0]

  if (accounts.isLoading) return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/></div>
  if (accounts.error) return <ErrorBlock error={accounts.error} onRetry={() => void accounts.refetch()}/>
  if (!account) {
    return <EmptyState icon={<FileUp/>} title={t("finance.import.noAccounts")} description={t("finance.import.noAccountsHint")}/>
  }
  return (
    <div className="space-y-6">
      <Field label={t("finance.import.account")} htmlFor="im-account">
        <Select id="im-account" aria-label={t("finance.import.account")} value={account.id} className="w-64"
          onValueChange={setPicked} options={cash.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <AccountImports key={account.id} accountId={account.id} canImport={can("finance.import")}/>
    </div>
  )
}

function AccountImports({accountId, canImport}: {accountId: string; canImport: boolean}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const imports = useQuery({queryKey: financeKeys.imports(ctx.mode, ctx.space, accountId), queryFn: () => listImports(ctx, accountId)})
  const [selected, setSelected] = useState<string | null>(null)
  const [mapping, setMapping] = useState(false)
  const list = imports.data?.data ?? []
  const current = list.find(i => i.id === selected) ?? list.find(i => i.pending > 0) ?? list[0]

  return (
    <>
      {canImport && (
        <Upload accountId={accountId} onImported={id => setSelected(id)} onNeedsColumns={() => setMapping(true)} onColumns={() => setMapping(m => !m)}/>
      )}
      {canImport && mapping && <CsvMappingForm accountId={accountId} onDone={() => setMapping(false)}/>}
      <section aria-label={t("finance.import.history")} className="space-y-3">
        <h2 className="text-sm font-medium text-foreground">{t("finance.import.history")}</h2>
        {imports.isLoading ? <Skeleton className="h-10 w-full"/> : imports.error ? (
          <ErrorBlock error={imports.error} onRetry={() => void imports.refetch()}/>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("finance.import.noImports")}</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {list.map(i => (
              <li key={i.id}>
                <Button size="sm" variant={i.id === current?.id ? "outline" : "ghost"} aria-pressed={i.id === current?.id} onClick={() => setSelected(i.id ?? null)}>
                  {i.from && i.to ? t("finance.import.period", {from: shortDate(i.from), to: shortDate(i.to)}) : shortDate(i.created_at.slice(0, 10))}
                  <span className="text-muted-foreground">· {i.pending > 0 ? t("finance.import.waiting", {count: i.pending}) : t("finance.import.allDone")}</span>
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
      {current?.id && <ImportLines key={current.id} importId={current.id}/>}
    </>
  )
}

function Upload({accountId, onImported, onNeedsColumns, onColumns}: {
  accountId: string; onImported: (id: string) => void; onNeedsColumns: () => void; onColumns: () => void
}) {
  const {t} = useTranslation()
  const [file, setFile] = useState<File | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const upload = useFinanceMutation(
    async (c: FinanceCtx, f: File, key) => uploadImport(c, {account_id: accountId, format: formatOf(f.name), content: await fileToBase64(f)}, key),
    c => [financeKeys.imports(c.mode, c.space, accountId)],
    result => {
      setDone(uploadSummary(result))
      if (result.id) onImported(result.id)
    },
    error => { if (problemCode(error) === "csv_mapping_required") onNeedsColumns() },
  )
  return (
    <form
      aria-label={t("finance.import.upload")}
      className="space-y-3"
      onSubmit={e => {
        e.preventDefault()
        if (!file || upload.isPending) return
        setDone(null)
        upload.mutate(file)
      }}
    >
      <Field label={t("finance.import.file")} htmlFor="im-file" hint={t("finance.import.fileHint")}>
        <input id="im-file" type="file" accept=".ofx,.qfx,.csv,.txt"
          className="block w-full max-w-md text-sm text-muted-foreground file:mr-3 file:rounded-md file:border file:border-border file:bg-surface file:px-3 file:py-1.5 file:text-sm file:text-foreground"
          onChange={e => { setFile(e.target.files?.[0] ?? null); setDone(null); upload.reset() }}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="brand" size="sm" disabled={!file || upload.isPending}>{t("finance.import.submit")}</Button>
        <Button type="button" variant="ghost" size="sm" onClick={onColumns}>{t("finance.import.columns")}</Button>
      </div>
      {done && <p role="status" className="text-sm text-foreground">{done}</p>}
      {upload.error ? <p role="alert" className="text-sm text-danger">{messageFor(upload.error)}</p> : null}
    </form>
  )
}
