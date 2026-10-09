"use client"

import limits from "@/lib/limits.json"
import {Button, buttonVariants, EmptyState, Field, Input, Skeleton, Switch} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {CircleCheck, FileUp, Landmark} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {CardBrandMark, maskedLast4} from "@/components/finance/CardBrandMark"
import {Drawer} from "@/components/ui/ConsoleOverlay"
import {LedgerRow} from "@/components/finance/LedgerRow"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {archiveAccount, createAccount, financeKeys, getSettings, listAccounts, listCards, postOpeningBalance, setDefaultReceivingAccount, setPostCTechInvoices} from "@/lib/api/finance"
import type {Account, AccountClass, Card, DREGroup, OpeningBalance} from "@/lib/api/financeTypes"
import {classLabel, dreGroupLabel, groupsForClass} from "@/lib/finance/labels"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useCreateRequest} from "@/lib/finance/createRequest"
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
  const {can, current, loading} = useFinanceSpaces()
  const configure = can("finance.configure")
  const [showArchived, setShowArchived] = useState(false)
  const [creating, setCreating] = useState(false)
  // The bar's request is a wish, not a permission: a role that may not create
  // never sees the drawer, whenever the space's verbs arrive.
  useCreateRequest("account", () => { if (loading || configure) setCreating(true) })

  const q = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const accounts = (q.data?.data ?? []).filter(a => !a.system)
  const visible = accounts.filter(a => showArchived || !a.archived)
  const hasArchived = accounts.some(a => a.archived)
  // A card's brand and last digits live on the card, not the account: read only
  // when there is a card to show them on.
  const hasCards = accounts.some(a => a.class === "liability")
  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx), enabled: hasCards})
  const cardOf = new Map((cards.data?.data ?? []).map(c => [c.id, c]))

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("finance.accounts.title")}</h1>
        {configure && (
          <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setCreating(true)}>{t("finance.accounts.new")}</Button>
        )}
      </div>

      <Drawer open={creating && configure} onClose={() => setCreating(false)} title={t("finance.accounts.new")}>
        {creating && <AccountForm onDone={() => setCreating(false)} offerImport={can("finance.import")}/>}
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
                  {rows.map(a => <AccountRow key={a.id} account={a} card={cardOf.get(a.id)} configure={configure}/>)}
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
          {/* The payer side of 6.7 posts to Pessoal only, never to a shared
              space: anywhere else this switch would do nothing. */}
          {configure && current?.kind === "personal_default" && <PostCTechInvoices/>}
        </>
      )}
    </div>
  )
}

function AccountRow({account, card, configure}: {account: Account; card?: Card; configure: boolean}) {
  const {t} = useTranslation()
  const [confirming, setConfirming] = useState(false)
  const [opening, setOpening] = useState(false)
  const archive = useFinanceMutation(
    (c, id: string, key) => archiveAccount(c, id, key),
    c => [financeKeys.accounts(c.mode, c.space)],
    () => setConfirming(false),
  )
  const isCard = account.class === "liability"
  const meta = [
    account.dre_group ? dreGroupLabel(account.dre_group) : "",
    card?.last4 ? maskedLast4(card.last4) : "",
    account.archived ? t("finance.accounts.archived") : "",
  ].filter(Boolean)
  return (
    // A ledger row (UX batch 4): on a phone the name wraps beside its balance
    // instead of being cut to "Conta co…", and Saldo inicial and Arquivar are a
    // left swipe or "⋯", each asking first.
    <LedgerRow
      title={accountName(account)}
      muted={account.archived}
      leading={isCard ? <CardBrandMark brand={card?.brand ?? "other"} className="text-muted-foreground"/> : undefined}
      meta={meta.length > 0 ? <span className="tabular-nums">{meta.join(" • ")}</span> : undefined}
      // A card's balance is a credit: negative is what is owed.
      amount={account.class === "asset" ? <span data-numeric>{money(account.balance)}</span>
        : isCard ? <span data-numeric>{account.balance < 0 ? t("finance.accounts.owed", {amount: money(-account.balance)}) : money(account.balance)}</span>
          : null}
      actions={isCard && (
        <Link href={`/console/finance/cards?card=${encodeURIComponent(account.id)}`} className="inline-flex items-center px-2.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline touch-target">{t("finance.accounts.open")}</Link>
      )}
      more={configure && !account.archived ? [
        ...(account.class === "asset" ? [{key: "opening", label: t("finance.accounts.openingBalance"), expanded: opening, onSelect: () => setOpening(v => !v)}] : []),
        {key: "archive", label: t("finance.accounts.archive"), destructive: true, expanded: confirming, onSelect: () => setConfirming(v => !v)},
      ] : []}
    >
      {confirming && (
        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm motion-safe:animate-in motion-safe:fade-in">
          <p className="text-muted-foreground">{t("finance.accounts.archiveConfirm", {name: accountName(account)})}</p>
          <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>{t("finance.accounts.cancel")}</Button>
          <Button size="sm" variant="brand" disabled={archive.isPending} onClick={() => archive.mutate(account.id)}>{t("finance.accounts.archive")}</Button>
          {archive.error && <p role="alert" className="w-full text-danger">{messageFor(archive.error)}</p>}
        </div>
      )}
      {opening && <OpeningForm account={account} onDone={() => setOpening(false)}/>}
    </LedgerRow>
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

/**
 * Creates an account (and, for a bank or cash account, its opening balance).
 * Once a bank or cash account exists, and the role may import, the drawer
 * offers the statement import for it instead of closing: the import is a step
 * of its own on Importar, never a field of this form.
 */
function AccountForm({onDone, offerImport}: {onDone: () => void; offerImport: boolean}) {
  const {t} = useTranslation()
  const [name, setName] = useState("")
  const [cls, setCls] = useState<AccountClass>("asset")
  const groups = groupsForClass(cls)
  const [group, setGroup] = useState<DREGroup | "">("")
  const [openingText, setOpeningText] = useState("")
  const [openingDate, setOpeningDate] = useState(todayIso())
  const [made, setMade] = useState<Account | null>(null)
  const asset = cls === "asset"
  const openingAmount = asset ? parseSignedMoney(openingText) : null
  const openingInvalid = asset && openingText.trim() !== "" && openingAmount === null
  const finish = (account: Account) => (offerImport && account.class === "asset" ? setMade(account) : onDone())
  // The opening balance is its own fact with its own intent: when only it fails,
  // the account exists and a retry must not create a second one. Its failure
  // still refreshes, so the account it was for shows in the list.
  const opening = useFinanceMutation(
    (c, v: {account: Account; body: OpeningBalance}, key) => postOpeningBalance(c, v.account.id, v.body, key),
    c => [financeKeys.all(c.mode, c.space)],
    (_, v) => finish(v.account),
    undefined,
    {invalidateOnError: true},
  )
  const fe = useFieldErrors(["name", "class", "dre_group"])
  // Value-aware: when an opening balance follows, the list is refreshed once,
  // after it lands. Refreshing between the two halves painted the new account at
  // R$ 0,00, a balance the person never entered.
  const create = useFinanceMutation(
    (c, body: {name: string; class: AccountClass; dre_group?: DREGroup}, key) => createAccount(c, body, key),
    (c, _created, body) => (body.class === "asset" && openingAmount ? [] : [financeKeys.accounts(c.mode, c.space)]),
    created => (openingAmount ? opening.mutate({account: created, body: {amount: openingAmount, date: openingDate}}) : finish(created)),
    fe.set,
  )
  if (made) return <ImportOffer account={made} onDone={onDone}/>
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
          <Button type="button" size="sm" variant="ghost" onClick={() => finish(opening.variables!.account)}>{t("finance.accounts.skipOpening")}</Button>
        </div>
      )}
    </form>
  )
}

/** "Conta criada. Importar um extrato agora?": Importar, with this account chosen. */
function ImportOffer({account, onDone}: {account: Account; onDone: () => void}) {
  const {t} = useTranslation()
  return (
    <div className="space-y-4 motion-safe:animate-in motion-safe:fade-in">
      <div className="flex items-start gap-3">
        <CircleCheck aria-hidden className="mt-0.5 size-5 shrink-0 text-success"/>
        <div className="min-w-0 space-y-1">
          <p role="status" className="text-sm font-medium text-foreground">{t("finance.accounts.created.title")}</p>
          <p className="text-sm text-muted-foreground">{t("finance.accounts.created.hint", {name: accountName(account)})}</p>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        {/* A link, not a button: it goes to Importar. data-slot keeps the 44px touch rule. */}
        <Link data-slot="button" className={buttonVariants({variant: "brand", size: "sm"})}
          href={`/console/finance/import?account=${encodeURIComponent(account.id)}`}>
          <FileUp aria-hidden className="size-4"/>{t("finance.accounts.created.import")}
        </Link>
        <Button variant="outline" size="sm" onClick={onDone}>{t("finance.accounts.created.later")}</Button>
      </div>
    </div>
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
            // "" is Nenhuma: the API clears the setting.
            onValueChange={v => save.mutate(v)}
            none={t("finance.accounts.none")}
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

/** "Lançar minhas faturas da CTech automaticamente neste espaço": on unless turned off. */
function PostCTechInvoices() {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const settings = useQuery({queryKey: financeKeys.settings(ctx.mode, ctx.space), queryFn: () => getSettings(ctx)})
  const save = useFinanceMutation(
    (c, on: boolean, key) => setPostCTechInvoices(c, on, key),
    c => [financeKeys.settings(c.mode, c.space)],
  )
  const on = save.isPending ? save.variables : settings.data?.post_ctech_invoices !== false
  return (
    <section className="space-y-1 border-t border-border pt-4">
      <div className="flex items-center gap-3">
        <Switch
          id="post-ctech-invoices"
          aria-labelledby="post-ctech-invoices-label"
          aria-describedby="post-ctech-invoices-help"
          checked={on}
          disabled={settings.isPending || save.isPending}
          onCheckedChange={(next: boolean) => save.mutate(next)}
        />
        <span id="post-ctech-invoices-label" className="text-sm font-medium">{t("finance.accounts.postCTechInvoices")}</span>
      </div>
      <p id="post-ctech-invoices-help" className="text-sm text-muted-foreground">{t("finance.accounts.postCTechInvoicesHelp")}</p>
      {save.error && <p role="alert" className="text-sm text-danger">{messageFor(save.error)}</p>}
    </section>
  )
}
