"use client"

import {Button, Field, Input} from "@aoctech/ui"

import {messageFor} from "@/lib/api/client"
import {createTransfer, type FinanceCtx, financeKeys} from "@/lib/api/finance"
import type {Account, NewTransfer} from "@/lib/api/financeTypes"
import {Select} from "@/components/ui/Select"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {limitMoneyDecimals, parseMoney} from "@/lib/money"
import {useState} from "react"

/** A transfer moves balances and every report reads them: refresh the whole space. */
export const wholeSpace = (c: FinanceCtx) => [financeKeys.all(c.mode, c.space)]

/**
 * Money moved between two of the space's own accounts. It is neither income nor
 * spending, so it shows on both statements and in no report.
 */
export function TransferPanel({accounts, from: initialFrom = "", onDone}: {accounts: Account[]; from?: string; onDone: () => void}) {
  const active = accounts.filter(a => a.class === "asset" && !a.system && !a.archived)
  const [from, setFrom] = useState(initialFrom)
  const [to, setTo] = useState("")
  const [amountText, setAmountText] = useState("")
  const [date, setDate] = useState(todayIso())
  const [memo, setMemo] = useState("")
  const create = useFinanceMutation((c, body: NewTransfer, key) => createTransfer(c, body, key), wholeSpace, onDone)
  const amount = parseMoney(amountText)
  const ready = from !== "" && to !== "" && from !== to && amount !== null && amount > 0 && date !== "" && !create.isPending
  return (
    <form
      aria-label="Nova transferência"
      className="grid items-start gap-3 rounded-lg border border-border p-4 sm:grid-cols-2 lg:grid-cols-5 motion-safe:animate-in motion-safe:fade-in"
      onSubmit={e => {
        e.preventDefault()
        if (!ready) return
        create.mutate({from_account_id: from, to_account_id: to, amount: amount!, date, memo: memo.trim() || undefined})
      }}
    >
      <Field label="De" htmlFor="tr-from">
        <Select id="tr-from" aria-label="De" value={from} options={active.map(a => ({value: a.id, label: a.name}))}
          onValueChange={v => { setFrom(v); if (v === to) setTo("") }}/>
      </Field>
      <Field label="Para" htmlFor="tr-to">
        <Select id="tr-to" aria-label="Para" value={to} onValueChange={setTo}
          options={active.filter(a => a.id !== from).map(a => ({value: a.id, label: a.name}))}/>
      </Field>
      <Field label="Valor" htmlFor="tr-amount">
        <Input id="tr-amount" inputMode="decimal" placeholder="0,00" value={amountText} onChange={e => setAmountText(limitMoneyDecimals(e.target.value))}/>
      </Field>
      <Field label="Data" htmlFor="tr-date">
        <Input id="tr-date" type="date" value={date} onChange={e => setDate(e.target.value)}/>
      </Field>
      <Field label="Descrição" htmlFor="tr-memo">
        <Input id="tr-memo" maxLength={140} placeholder="Transferência" value={memo} onChange={e => setMemo(e.target.value)}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2 lg:col-span-5">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>Transferir</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        {create.error ? <p role="alert" className="text-sm text-danger">{messageFor(create.error)}</p> : null}
      </div>
    </form>
  )
}
