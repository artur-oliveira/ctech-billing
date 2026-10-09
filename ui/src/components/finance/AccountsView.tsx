"use client"

import limits from "@/lib/limits.json"
import {Button, EmptyState, Field, Input, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import {useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {archiveAccount, createAccount, financeKeys, getSettings, listAccounts, postOpeningBalance, setDefaultReceivingAccount} from "@/lib/api/finance"
import type {Account, AccountClass, DREGroup, OpeningBalance} from "@/lib/api/financeTypes"
import {CLASS_LABEL, DRE_GROUP_LABEL, groupsForClass} from "@/lib/finance/labels"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money} from "@/lib/format"
import {todayIso} from "@/lib/finance/today"
import {maskMoney, parseSignedMoney} from "@/lib/money"

const SECTIONS: {title: string; classes: AccountClass[]}[] = [
  {title: "Contas", classes: ["asset"]},
  {title: "Receitas", classes: ["income"]},
  {title: "Despesas", classes: ["expense"]},
]

/**
 * F8 — accounts, categories and the default receiving account.
 *
 * Three ruled groups, never cards. System accounts (contas a pagar/receber,
 * saldos iniciais) are the ledger's own and are never shown. Nothing is deleted:
 * archiving hides an account from new activity and keeps its history.
 */
export function AccountsView() {
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
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Contas e categorias</h1>
        {configure && !creating && (
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>Nova conta ou categoria</Button>
        )}
      </div>

      {creating && <AccountForm onDone={() => setCreating(false)}/>}

      {q.isLoading ? (
        <div className="space-y-2" aria-busy><Skeleton className="h-5 w-40"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-5/6"/></div>
      ) : q.error ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : accounts.length === 0 ? (
        <EmptyState
          icon={<Landmark/>}
          title="Nenhuma conta ainda"
          description="Crie a primeira conta (corrente, poupança, dinheiro) e as categorias de receita e despesa para registrar contas a pagar e a receber."
        />
      ) : (
        <>
          {SECTIONS.map(section => {
            const rows = visible.filter(a => section.classes.includes(a.class))
            if (rows.length === 0) return null
            return (
              <section key={section.title} className="space-y-2" aria-labelledby={`sec-${section.title}`}>
                <h2 id={`sec-${section.title}`} className="text-sm font-medium text-foreground">{section.title}</h2>
                <ul className="divide-y divide-border border-y border-border">
                  {rows.map(a => <AccountRow key={a.id} account={a} configure={configure}/>)}
                </ul>
              </section>
            )
          })}
          {hasArchived && (
            <Button variant="ghost" size="sm" onClick={() => setShowArchived(v => !v)}>
              {showArchived ? "Ocultar arquivadas" : "Mostrar arquivadas"}
            </Button>
          )}
          {configure && <DefaultReceiving accounts={accounts}/>}
        </>
      )}
    </div>
  )
}

function AccountRow({account, configure}: {account: Account; configure: boolean}) {
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
          <p className="truncate text-sm text-foreground">{account.name}</p>
          {account.dre_group && <p className="text-xs text-muted-foreground">{DRE_GROUP_LABEL[account.dre_group]}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {account.class === "asset" && <span data-numeric className="text-sm tabular-nums">{money(account.balance)}</span>}
          {account.archived && <span className="text-xs">Arquivada</span>}
          {configure && account.class === "asset" && !account.archived && !opening && (
            <Button variant="ghost" size="sm" onClick={() => setOpening(true)}>Saldo inicial</Button>
          )}
          {configure && !account.archived && !confirming && (
            <Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>Arquivar</Button>
          )}
        </div>
      </div>
      {confirming && (
        <div className="mt-2 flex flex-wrap items-center gap-2 text-sm motion-safe:animate-in motion-safe:fade-in">
          <p className="text-muted-foreground">Arquivar “{account.name}”? Ela sai das listas; nada é apagado e o histórico continua.</p>
          <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>Cancelar</Button>
          <Button size="sm" variant="brand" disabled={archive.isPending} onClick={() => archive.mutate(account.id)}>Confirmar</Button>
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
  const [text, setText] = useState("")
  const [date, setDate] = useState(todayIso())
  const post = useFinanceMutation(
    (c, body: OpeningBalance, key) => postOpeningBalance(c, account.id, body, key),
    c => [financeKeys.all(c.mode, c.space)],
    onDone,
  )
  const amount = parseSignedMoney(text)
  return (
    <form
      className="mt-2 grid items-start gap-3 rounded-lg bg-surface p-3 sm:grid-cols-[1fr_1fr_auto] motion-safe:animate-in motion-safe:fade-in"
      onSubmit={e => {
        e.preventDefault()
        if (amount !== null) post.mutate({amount, date})
      }}
    >
      <Field label="Valor" htmlFor={`ob-${account.id}`}>
        <Input id={`ob-${account.id}`} inputMode="decimal" placeholder="0,00" value={text} onChange={e => setText(maskMoney(e.target.value, {signed: true}))} autoFocus/>
      </Field>
      <Field label="Em" htmlFor={`obd-${account.id}`}>
        <DateField id={`obd-${account.id}`} min={limits.minDate} max={todayIso()} value={date} onValueChange={setDate}/>
      </Field>
      <div className="flex gap-2 sm:pt-6">
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        <Button type="submit" variant="brand" size="sm" disabled={amount === null || post.isPending}>Lançar</Button>
      </div>
      {post.error ? <p role="alert" className="text-sm text-danger sm:col-span-3">{messageFor(post.error)}</p> : null}
    </form>
  )
}

function AccountForm({onDone}: {onDone: () => void}) {
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
  const create = useFinanceMutation(
    (c, body: {name: string; class: AccountClass; dre_group?: DREGroup}, key) => createAccount(c, body, key),
    c => [financeKeys.accounts(c.mode, c.space)],
    created => (openingAmount ? opening.mutate({id: created.id, body: {amount: openingAmount, date: openingDate}}) : onDone()),
  )
  const chosenGroup = groups.includes(group as DREGroup) ? (group as DREGroup) : groups[0]
  return (
    <form
      className="grid items-start gap-3 border-y border-border py-4 sm:grid-cols-2 lg:grid-cols-4"
      onSubmit={e => {
        e.preventDefault()
        if (!name.trim() || openingInvalid) return
        create.mutate({name: name.trim(), class: cls, ...(chosenGroup ? {dre_group: chosenGroup} : {})})
      }}
    >
      <Field label="Nome" htmlFor="acc-name" required>
        <Input id="acc-name" maxLength={limits.text.accountName} value={name} onChange={e => setName(e.target.value)} autoFocus/>
      </Field>
      <Field label="Tipo" htmlFor="acc-class">
        <Select id="acc-class" value={cls} onValueChange={v => setCls(v as AccountClass)} options={(["asset", "income", "expense"] as AccountClass[]).map(c => ({value: c, label: CLASS_LABEL[c]}))}/>
      </Field>
      {groups.length > 0 ? (
        <Field label="Grupo na DRE" htmlFor="acc-group">
          <Select id="acc-group" value={chosenGroup} onValueChange={v => setGroup(v as DREGroup)} options={groups.map(g => ({value: g, label: DRE_GROUP_LABEL[g]}))}/>
        </Field>
      ) : asset ? (
        <>
          <Field label="Saldo inicial (opcional)" htmlFor="acc-opening">
            <Input id="acc-opening" inputMode="decimal" placeholder="0,00" value={openingText} onChange={e => setOpeningText(maskMoney(e.target.value, {signed: true}))} aria-invalid={openingInvalid}/>
          </Field>
          {openingText.trim() !== "" && (
            <Field label="Em" htmlFor="acc-opening-date">
              <DateField id="acc-opening-date" min={limits.minDate} max={todayIso()} value={openingDate} onValueChange={setOpeningDate}/>
            </Field>
          )}
        </>
      ) : null}
      <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Cancelar</Button>
        <Button type="submit" variant="brand" size="sm" disabled={create.isPending || opening.isPending || create.isSuccess || !name.trim() || openingInvalid}>Criar</Button>
      </div>
      {create.error && <p role="alert" className="text-sm text-danger sm:col-span-2 lg:col-span-4">{messageFor(create.error)}</p>}
      {opening.error && opening.variables && (
        <div role="alert" className="flex flex-wrap items-center gap-2 text-sm sm:col-span-2 lg:col-span-4">
          <p className="text-danger">A conta foi criada, mas o saldo inicial não foi lançado.</p>
          <Button type="button" size="sm" variant="outline" disabled={opening.isPending} onClick={() => opening.mutate(opening.variables!)}>Tentar de novo</Button>
          <Button type="button" size="sm" variant="ghost" onClick={onDone}>Deixar sem saldo inicial</Button>
        </div>
      )}
    </form>
  )
}

function DefaultReceiving({accounts}: {accounts: Account[]}) {
  const ctx = useFinanceCtx()
  const settings = useQuery({queryKey: financeKeys.settings(ctx.mode, ctx.space), queryFn: () => getSettings(ctx)})
  const save = useFinanceMutation(
    (c, id: string, key) => setDefaultReceivingAccount(c, id, key),
    c => [financeKeys.settings(c.mode, c.space)],
  )
  const options = accounts.filter(a => a.class === "asset" && !a.archived)
  return (
    <section className="space-y-2 border-t border-border pt-4">
      <Field label="Conta padrão de recebimento" htmlFor="default-receiving" hint="Onde as contas a receber caem quando ninguém escolhe outra.">
        <div className="max-w-xs">
          <Select
            id="default-receiving"
            value={settings.data?.default_receiving_account_id ?? ""}
            onValueChange={v => v && save.mutate(v)}
            disabled={save.isPending}
            placeholder="Nenhuma"
            options={options.map(a => ({value: a.id, label: a.name}))}
          />
        </div>
      </Field>
      {save.error && <p role="alert" className="text-sm text-danger">{messageFor(save.error)}</p>}
    </section>
  )
}
