"use client"

import {Button, EmptyState, Field, Input, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Landmark} from "lucide-react"
import {useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {archiveAccount, createAccount, financeKeys, getSettings, listAccounts, setDefaultReceivingAccount} from "@/lib/api/finance"
import type {Account, AccountClass, DREGroup} from "@/lib/api/financeTypes"
import {CLASS_LABEL, DRE_GROUP_LABEL, groupsForClass} from "@/lib/finance/labels"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money} from "@/lib/format"

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
    </li>
  )
}

function AccountForm({onDone}: {onDone: () => void}) {
  const [name, setName] = useState("")
  const [cls, setCls] = useState<AccountClass>("asset")
  const groups = groupsForClass(cls)
  const [group, setGroup] = useState<DREGroup | "">("")
  const create = useFinanceMutation(
    (c, body: {name: string; class: AccountClass; dre_group?: DREGroup}, key) => createAccount(c, body, key),
    c => [financeKeys.accounts(c.mode, c.space)],
    onDone,
  )
  const chosenGroup = groups.includes(group as DREGroup) ? (group as DREGroup) : groups[0]
  return (
    <form
      className="grid gap-3 border-y border-border py-4 sm:grid-cols-[2fr_1fr_1fr_auto] sm:items-end"
      onSubmit={e => {
        e.preventDefault()
        if (!name.trim()) return
        create.mutate({name: name.trim(), class: cls, ...(chosenGroup ? {dre_group: chosenGroup} : {})})
      }}
    >
      <Field label="Nome" htmlFor="acc-name" required>
        <Input id="acc-name" value={name} onChange={e => setName(e.target.value)} autoFocus/>
      </Field>
      <Field label="Tipo" htmlFor="acc-class">
        <Select id="acc-class" value={cls} onValueChange={v => setCls(v as AccountClass)} options={(["asset", "income", "expense"] as AccountClass[]).map(c => ({value: c, label: CLASS_LABEL[c]}))}/>
      </Field>
      {groups.length > 0 ? (
        <Field label="Grupo na DRE" htmlFor="acc-group">
          <Select id="acc-group" value={chosenGroup} onValueChange={v => setGroup(v as DREGroup)} options={groups.map(g => ({value: g, label: DRE_GROUP_LABEL[g]}))}/>
        </Field>
      ) : <div/>}
      <div className="flex gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Cancelar</Button>
        <Button type="submit" variant="brand" size="sm" disabled={create.isPending || !name.trim()}>Criar</Button>
      </div>
      {create.error && <p role="alert" className="text-sm text-danger sm:col-span-4">{messageFor(create.error)}</p>}
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
