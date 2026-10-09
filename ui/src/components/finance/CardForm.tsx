"use client"

import {Button, Field, Input} from "@aoctech/ui"
import {useState} from "react"

import {wholeSpace} from "@/components/finance/TransferPanel"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {createCard, patchCard} from "@/lib/api/finance"
import type {Account, Card} from "@/lib/api/financeTypes"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import limits from "@/lib/limits.json"

const DAYS = Array.from({length: 31}, (_, i) => ({value: String(i + 1), label: String(i + 1)}))

/** Creates a card, or edits the selected one's days and paying account. */
export function CardForm({card, accounts, onDone}: {card?: Card; accounts: Account[]; onDone: (id?: string) => void}) {
  const assets = accounts.filter(a => a.class === "asset" && !a.system && !a.archived)
  const [name, setName] = useState("")
  const [closing, setClosing] = useState(String(card?.closing_day ?? ""))
  const [due, setDue] = useState(String(card?.due_day ?? ""))
  const [payer, setPayer] = useState(card?.paying_account_id ?? "")
  const save = useFinanceMutation(
    (c, _: void, key) => card
      ? patchCard(c, card.id, {closing_day: Number(closing), due_day: Number(due), paying_account_id: payer}, key)
      : createCard(c, {name: name.trim(), closing_day: Number(closing), due_day: Number(due), paying_account_id: payer}, key),
    wholeSpace,
    saved => onDone(saved.id),
  )
  const ready = (card || name.trim() !== "") && closing !== "" && due !== "" && payer !== "" && !save.isPending
  return (
    <form
      aria-label={card ? "Editar cartão" : "Novo cartão"}
      className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0"
      onSubmit={e => {
        e.preventDefault()
        if (ready) save.mutate()
      }}
    >
      {!card && (
        <Field label="Nome" htmlFor="cd-name" required>
          <Input id="cd-name" maxLength={limits.text.accountName} placeholder="Visa, Nubank…" value={name} onChange={e => setName(e.target.value)} autoFocus/>
        </Field>
      )}
      <Field label="Fecha no dia" htmlFor="cd-closing" required>
        <Select id="cd-closing" aria-label="Fecha no dia" value={closing} onValueChange={setClosing} options={DAYS}/>
      </Field>
      <Field label="Vence no dia" htmlFor="cd-due" required>
        <Select id="cd-due" aria-label="Vence no dia" value={due} onValueChange={setDue} options={DAYS}/>
      </Field>
      <Field label="Pagar com" htmlFor="cd-payer" required hint={assets.length === 0 ? "Nenhuma conta. Crie uma em Contas." : undefined}>
        <Select id="cd-payer" aria-label="Pagar com" value={payer} onValueChange={setPayer} options={assets.map(a => ({value: a.id, label: a.name}))}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{card ? "Salvar" : "Criar cartão"}</Button>
        <Button type="button" variant="outline" size="sm" onClick={() => onDone()}>Fechar</Button>
        {save.error ? <p role="alert" className="text-sm text-danger">{messageFor(save.error)}</p> : null}
      </div>
    </form>
  )
}
