"use client"

import {Button, Field, Input} from "@aoctech/ui"
import {useState} from "react"

import {wholeSpace} from "@/components/finance/TransferPanel"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {createPurchase} from "@/lib/api/finance"
import type {Account, NewPurchase} from "@/lib/api/financeTypes"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {money} from "@/lib/format"
import limits from "@/lib/limits.json"
import {maskMoney, parseMoney} from "@/lib/money"

const COUNTS = Array.from({length: limits.maxInstallments}, (_, i) => ({
  value: String(i + 1), label: i === 0 ? "1× (à vista)" : `${i + 1}×`,
}))

/**
 * The installments as the issuer prints them, with the backend's rule
 * (finance.SplitInstallments): the remainder of the division on the first.
 */
export function installmentsPreview(total: number, n: number): string {
  if (n <= 1) return "à vista"
  const base = Math.floor(total / n)
  const first = base + (total - base * n)
  return first === base ? `${n}× de ${money(base)}` : `${money(first)} + ${n - 1}× de ${money(base)}`
}

/** A new purchase on a card: the whole amount is the expense today; the
 *  installments only decide which statement bills each part. */
export function PurchasePanel({cardId, accounts, onDone}: {cardId: string; accounts: Account[]; onDone: () => void}) {
  const cats = accounts.filter(a => a.class === "expense" && !a.system && !a.archived)
  const [description, setDescription] = useState("")
  const [amountText, setAmountText] = useState("")
  const [date, setDate] = useState(todayIso())
  const [count, setCount] = useState("1")
  const [category, setCategory] = useState("")
  const create = useFinanceMutation((c, body: NewPurchase, key) => createPurchase(c, cardId, body, key), wholeSpace, onDone)
  const total = parseMoney(amountText)
  const n = Number(count)
  const ready = description.trim() !== "" && total !== null && total >= n && category !== "" && date !== "" && !create.isPending
  return (
    <form
      aria-label="Nova compra"
      className="grid items-start gap-3 rounded-lg border border-border p-4 sm:grid-cols-2 lg:grid-cols-5 motion-safe:animate-in motion-safe:fade-in"
      onSubmit={e => {
        e.preventDefault()
        if (ready) create.mutate({date, description: description.trim(), category_id: category, total: total!, installments: n})
      }}
    >
      <Field label="Descrição" htmlFor="pu-desc" required>
        <Input id="pu-desc" maxLength={limits.text.description} value={description} onChange={e => setDescription(e.target.value)} autoFocus/>
      </Field>
      <Field label="Valor" htmlFor="pu-amount" required hint={total ? installmentsPreview(total, n) : undefined}>
        <Input id="pu-amount" inputMode="decimal" placeholder="0,00" value={amountText} onChange={e => setAmountText(maskMoney(e.target.value))}/>
      </Field>
      <Field label="Parcelas" htmlFor="pu-count">
        <Select id="pu-count" aria-label="Parcelas" value={count} onValueChange={setCount} options={COUNTS}/>
      </Field>
      <Field label="Data" htmlFor="pu-date">
        <DateField id="pu-date" value={date} onValueChange={setDate} min={limits.minDate} max={todayIso()}/>
      </Field>
      <Field label="Categoria" htmlFor="pu-cat" required hint={cats.length === 0 ? "Nenhuma categoria de despesa. Crie uma em Contas." : undefined}>
        <Select id="pu-cat" aria-label="Categoria" value={category} onValueChange={setCategory} options={cats.map(a => ({value: a.id, label: a.name}))}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2 lg:col-span-5">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>Registrar compra</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        {create.error ? <p role="alert" className="text-sm text-danger">{messageFor(create.error)}</p> : null}
      </div>
    </form>
  )
}
