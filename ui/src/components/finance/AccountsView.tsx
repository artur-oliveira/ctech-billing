"use client"

import limits from "@/lib/limits.json"
import {Button, Drawer, EmptyState, Field, Input, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {archiveAccount, createAccount, financeKeys, getSettings, listAccounts, postOpeningBalance, setDefaultReceivingAccount} from "@/lib/api/finance"
import type {Account, AccountClass, DREGroup, OpeningBalance} from "@/lib/api/financeTypes"
import {classLabel, dreGroupLabel, groupsForClass} from "@/lib/finance/labels"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money} from "@/lib/format"
import {todayIso} from "@/lib/finance/today"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {maskMoney, moneyPlaceholder, parseSignedMoney} from "@/lib/money"
import {accountName} from "@/lib/finance/accountName"

const SECTIONS: {key: "accounts" | "income" | "expense" | "cards"; classes: AccountClass[]}[] = [
  {key: "accounts", classes: ["asset"]},
  {key: "income", classes: ["income"]},
  {key: "expense", classes: ["expense"]},
  {key: "cards", classes: ["liability"]},
]

/**
 * F8 — accounts, categories and the default receiving account.
 *
 * Three ruled groups, never cards. System accounts (contas a pagar/receber,
 * saldos iniciais) are the ledger's own and are never shown. Nothing is deleted:
 * archiving hides an account from new activity and keeps its history.
 */
export function AccountsView() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const configure = can("finance.configure")
  const [showArchived, setShowArchived] = useState(false)
  const [creating, setCreating] = useState(false)

  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const accounts = (q.data?.data ?? []).filter(a => !a.system)
  const visible = accounts.filter(a => showArchived || !a.archived)
  const hasArchived = accounts.some(a => a.archived)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.accounts.title")}</h1>
        {configure && (
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>{t("finance.accounts.new")}</Button>
        )}
      </div>

      <Drawer open={creating} onClose={() => setCreating(false)} title={t("finance.accounts.new")}>
        <AccountForm onDone={() => setCreating(false)}/>
      </Drawer>

      {q.isLoading ? (
        <div className="space-y-2" aria-busy><Skeleton className="h-5 w-40"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-5/6"/></div>
      ) : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : accounts.length === 0 ? (
        <EmptyState
          icon={<Landmark/>}
          title={t("finance.accounts.empty")}
        />
      ) : (
        <>
          {SECTIONS.map(section => {
            const rows = visible.filter(a => section.classes.includes(a.class))
            if (rows.length === 0) return null
            return (
              <section key={section.key} className="space-y-2" aria-labelledby={`sec-${section.key}`}>
                <h2 id={`sec-${section.key}`} className="text-sm font-medium text-foreground">{t(`finance.accounts.sections.${section.key}`)}</h2>
                <ul className="divide-y divide-border border-y border-border">
                  {rows.map(a => <AccountRow key={a.id} account={a} configure={configure}/>)}
                </ul>
              </section>
            )
          })}
          {hasArchived && (
            <Button variant="ghost" size="sm" onClick={() => setShowArchived(v => !v)}>
              {showArchived ? t("finance.accounts.hideArchived") : t("finance.accounts.showArchived")}
            </Button>
          )}
          {configure && <DefaultReceiving accounts={accounts}/>}
        </>
      )}
    </div>
  )
}

function AccountRow({account, configure}: {account: Account; configure: boolean}) {
  const {t} = useTranslation()
  const [confirming, setConfirming] = useState(false)
  const [opening, setOpening] = useState(false)
  const archive = useFinanceMutation(
    (c, id: string, key) => archiveAccount(c, id, key),
    c => [financeKeys.accounts(c.mode, c.space)],
    () => setConfirming(false),
  )
  return (
    <li className={`py-2.5 ${account.archived ? "text-muted-foreground" : ""}`}>
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <p className="truncate text-sm text-foreground">{accountName(account)}</p>
          {account.dre_group && <p className="text-xs text-muted-foreground">{dreGroupLabel(account.dre_group)}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {account.class === "asset" && <span data-numeric className="text-sm tabular-nums">{money(account.balance)}</span>}
          {account.class === "liability" && (
            <>
              {/* A card's balance is a credit: negative is what is owed. */}
              <span data-numeric className="text-sm tabular-nums">{account.balance < 0 ? t("finance.accounts.owed", {amount: money(-account.balance)}) : money(account.balance)}</span>
              <Link href={`/console/finance/cards?card=${encodeURIComponent(account.id)}`} className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">{t("finance.accounts.open")}</Link>
            </>
          )}
          {account.archived && <span className="text-xs">{t("finance.accounts.archived")}</span>}
          {configure && account.class === "asset" && !account.archived && !opening && (
            <Button variant="ghost" size="sm" onClick={() => setOpening(true)}>{t("finance.accounts.openingBalance")}</Button>
          )}
          {configure && !account.archived && !confirming && (
            <Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>{t("finance.accounts.archive")}</Button>
          )}
        </div>
      </div>
      {confirming && (
        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm motion-safe:animate-in motion-safe:fade-in">
          <p className="text-muted-foreground">{t("finance.accounts.archiveConfirm", {name: accountName(account)})}</p>
          <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>{t("finance.accounts.cancel")}</Button>
          <Button size="sm" variant="brand" disabled={archive.isPending} onClick={() => archive.mutate(account.id)}>{t("finance.accounts.archive")}</Button>
          {archive.error && <p role="alert" className="w-full text-danger">{messageFor(archive.error)}</p>}
        </div>
      )}
      {opening && <OpeningForm account={account} onDone={() => setOpening(false)}/>}
    </li>
  )
}

/**
 * The opening balance of an account that already exists (one created before
 * 6.4, or whose opening was reversed). Once per account: a second one is
 * refused by the server, which says how to correct it.
 */
function OpeningForm({account, onDone}: {account: Account; onDone: () => void}) {
  const {t} = useTranslation()
  const [text, setText] = useState("")
  const [date, setDate] = useState(todayIso())
  const fe = useFieldErrors(["amount", "date"])
  const post = useFinanceMutation(
    (c, body: OpeningBalance, key) => postOpeningBalance(c, account.id, body, key),
    c => [financeKeys.all(c.mode, c.space)],
    onDone,
    fe.set,
  )
  const amount = parseSignedMoney(text)
  return (
    <form
      className="mt-2 grid items-start gap-3 rounded-lg bg-surface p-3 sm:grid-cols-[1fr_1fr_auto] motion-safe:animate-in motion-safe:fade-in"
      onSubmit={e => {
        e.preventDefault()
        if (amount === null) return
        fe.reset()
        post.mutate({amount, date})
      }}
    >
      <Field label={t("finance.accounts.amount")} htmlFor={`ob-${account.id}`} error={fe.of("amount")}>
        <Input id={`ob-${account.id}`} inputMode="decimal" placeholder={moneyPlaceholder()} value={text} {...fe.props("amount", `ob-${account.id}`)} onChange={e => { setText(maskMoney(e.target.value, {signed: true})); fe.clear("amount") }} autoFocus/>
      </Field>
      <Field label={t("finance.accounts.on")} htmlFor={`obd-${account.id}`} error={fe.of("date")}>
        <DateField id={`obd-${account.id}`} min={limits.minDate} max={todayIso()} value={date} invalid={!!fe.of("date")} onValueChange={v => { setDate(v); fe.clear("date") }}/>
      </Field>
      <div className="flex gap-2 sm:pt-6">
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("finance.accounts.close")}</Button>
        <Button type="submit" variant="brand" size="sm" disabled={amount === null || post.isPending}>{t("finance.accounts.save")}</Button>
      </div>
      {fe.general ? <p role="alert" className="text-sm text-danger sm:col-span-3">{fe.general}</p> : null}
    </form>
  )
}

function AccountForm({onDone}: {onDone: () => void}) {
  const {t} = useTranslation()
  const [name, setName] = useState("")
  const [cls, setCls] = useState<AccountClass>("asset")
  const groups = groupsForClass(cls)
  const [group, setGroup] = useState<DREGroup | "">("")
  const [openingText, setOpeningText] = useState("")
  const [openingDate, setOpeningDate] = useState(todayIso())
  const asset = cls === "asset"
  const openingAmount = asset ? parseSignedMoney(openingText) : null
  const openingInvalid = asset && openingText.trim() !== "" && openingAmount === null
  // The opening balance is its own fact with its own intent: when only it fails,
  // the account exists and a retry must not create a second one.
  const opening = useFinanceMutation(
    (c, v: {id: string; body: OpeningBalance}, key) => postOpeningBalance(c, v.id, v.body, key),
    c => [financeKeys.all(c.mode, c.space)],
    onDone,
  )
  const fe = useFieldErrors(["name", "class", "dre_group"])
  const create = useFinanceMutation(
    (c, body: {name: string; class: AccountClass; dre_group?: DREGroup}, key) => createAccount(c, body, key),
    c => [financeKeys.accounts(c.mode, c.space)],
    created => (openingAmount ? opening.mutate({id: created.id, body: {amount: openingAmount, date: openingDate}}) : onDone()),
    fe.set,
  )
  const chosenGroup = groups.includes(group as DREGroup) ? (group as DREGroup) : groups[0]
  return (
    <form
      className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0"
      onSubmit={e => {
        e.preventDefault()
        if (!name.trim() || openingInvalid) return
        fe.reset()
        create.mutate({name: name.trim(), class: cls, ...(chosenGroup ? {dre_group: chosenGroup} : {})})
      }}
    >
      <Field label={t("finance.accounts.name")} htmlFor="acc-name" required error={fe.of("name")}>
        <Input id="acc-name" maxLength={limits.text.accountName} value={name} {...fe.props("name", "acc-name")} onChange={e => { setName(e.target.value); fe.clear("name") }} autoFocus/>
      </Field>
      <Field label={t("finance.accounts.type")} htmlFor="acc-class" error={fe.of("class")}>
        <Select id="acc-class" value={cls} {...fe.props("class", "acc-class")} onValueChange={v => { setCls(v as AccountClass); fe.clear("class") }} options={(["asset", "income", "expense"] as AccountClass[]).map(c => ({value: c, label: classLabel(c)}))}/>
      </Field>
      {groups.length > 0 ? (
        <Field label={t("finance.accounts.dreGroup")} htmlFor="acc-group" error={fe.of("dre_group")}>
          <Select id="acc-group" value={chosenGroup} {...fe.props("dre_group", "acc-group")} onValueChange={v => { setGroup(v as DREGroup); fe.clear("dre_group") }} options={groups.map(g => ({value: g, label: dreGroupLabel(g)}))}/>
        </Field>
      ) : asset ? (
        <>
          <Field label={t("finance.accounts.openingOptional")} htmlFor="acc-opening">
            <Input id="acc-opening" inputMode="decimal" placeholder={moneyPlaceholder()} value={openingText} onChange={e => setOpeningText(maskMoney(e.target.value, {signed: true}))} aria-invalid={openingInvalid}/>
          </Field>
          {openingText.trim() !== "" && (
            <Field label={t("finance.accounts.on")} htmlFor="acc-opening-date">
              <DateField id="acc-opening-date" min={limits.minDate} max={todayIso()} value={openingDate} onValueChange={setOpeningDate}/>
            </Field>
          )}
        </>
      ) : null}
      <div className="flex gap-2 sm:col-span-2">
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("finance.accounts.cancel")}</Button>
        <Button type="submit" variant="brand" size="sm" disabled={create.isPending || opening.isPending || create.isSuccess || !name.trim() || openingInvalid}>{t("finance.accounts.create")}</Button>
      </div>
      {fe.general && <p role="alert" className="text-sm text-danger sm:col-span-2">{fe.general}</p>}
      {opening.error && opening.variables && (
        <div role="alert" className="flex flex-wrap items-center gap-2 text-sm sm:col-span-2">
          <p className="text-danger">{t("finance.accounts.openingFailed")}</p>
          <Button type="button" size="sm" variant="outline" disabled={opening.isPending} onClick={() => opening.mutate(opening.variables!)}>{t("common.tryAgain")}</Button>
          <Button type="button" size="sm" variant="ghost" onClick={onDone}>{t("finance.accounts.skipOpening")}</Button>
        </div>
      )}
    </form>
  )
}

function DefaultReceiving({accounts}: {accounts: Account[]}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const settings = useQuery({queryKey: financeKeys.settings(ctx.mode, ctx.space), queryFn: () => getSettings(ctx)})
  const save = useFinanceMutation(
    (c, id: string, key) => setDefaultReceivingAccount(c, id, key),
    c => [financeKeys.settings(c.mode, c.space)],
  )
  const options = accounts.filter(a => a.class === "asset" && !a.archived)
  return (
    <section className="space-y-2 border-t border-border pt-4">
      <Field label={t("finance.accounts.defaultReceiving")} htmlFor="default-receiving">
        <div className="max-w-xs">
          <Select
            id="default-receiving"
            value={settings.data?.default_receiving_account_id ?? ""}
            onValueChange={v => v && save.mutate(v)}
            disabled={save.isPending}
            placeholder={t("finance.accounts.none")}
            options={options.map(a => ({value: a.id, label: accountName(a)}))}
          />
        </div>
      </Field>
      {save.error && <p role="alert" className="text-sm text-danger">{messageFor(save.error)}</p>}
    </section>
  )
}
