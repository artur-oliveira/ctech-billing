"use client"

import {Button, Field, Input, Select} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {brandName, CARD_BRANDS, CardBrandMark} from "@/components/finance/CardBrandMark"
import {wholeSpace} from "@/components/finance/TransferPanel"
import {createCard, patchCard} from "@/lib/api/finance"
import type {Account, Card, CardBrand} from "@/lib/api/financeTypes"
import {selectCopy} from "@/lib/selectCopy"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import limits from "@/lib/limits.json"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {accountName} from "@/lib/finance/accountName"

const DAYS = Array.from({length: 31}, (_, i) => ({value: String(i + 1), label: String(i + 1)}))

// Built at render, never at import: the language can change.
const brandOptions = () => CARD_BRANDS.map(b => ({value: b, label: brandName(b), icon: <CardBrandMark brand={b}/>}))

/** Creates a card, or edits the selected one's brand, last digits, days and paying account. */
export function CardForm({card, accounts, onDone}: {card?: Card; accounts: Account[]; onDone: (id?: string) => void}) {
  const {t} = useTranslation()
  const assets = accounts.filter(a => a.class === "asset" && !a.system && !a.archived)
  const [name, setName] = useState("")
  const [closing, setClosing] = useState(String(card?.closing_day ?? ""))
  const [due, setDue] = useState(String(card?.due_day ?? ""))
  const [payer, setPayer] = useState(card?.paying_account_id ?? "")
  const [brand, setBrand] = useState<CardBrand | "">(card?.brand ?? "")
  const [last4, setLast4] = useState(card?.last4 ?? "")
  const [last4Error, setLast4Error] = useState<string>()
  const fe = useFieldErrors(["name", "brand", "last4", "closing_day", "due_day", "paying_account_id"])
  const save = useFinanceMutation(
    (c, _: void, key) => card
      ? patchCard(c, card.id, {closing_day: Number(closing), due_day: Number(due), paying_account_id: payer, brand, last4}, key)
      : createCard(c, {
        name: name.trim(), closing_day: Number(closing), due_day: Number(due), paying_account_id: payer,
        ...(brand ? {brand} : {}), ...(last4 ? {last4} : {}),
      }, key),
    wholeSpace,
    saved => onDone(saved.id),
    fe.set,
  )
  const ready = (card || name.trim() !== "") && closing !== "" && due !== "" && payer !== "" && !save.isPending
  return (
    <form
      aria-label={card ? t("finance.cards.edit") : t("finance.cards.new")}
      className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0"
      onSubmit={e => {
        e.preventDefault()
        if (!ready) return
        fe.reset()
        if (last4 !== "" && last4.length !== 4) {
          setLast4Error(t("finance.cards.last4Invalid"))
          return
        }
        save.mutate()
      }}
    >
      {!card && (
        <Field label={t("finance.cards.name")} htmlFor="cd-name" required error={fe.of("name")}>
          <Input id="cd-name" maxLength={limits.text.accountName} placeholder={t("finance.cards.namePlaceholder")} value={name} {...fe.props("name", "cd-name")} onChange={e => { setName(e.target.value); fe.clear("name") }} autoFocus/>
        </Field>
      )}
      <Field label={t("finance.cards.brand")} htmlFor="cd-brand" error={fe.of("brand")}>
        <Select {...selectCopy()} id="cd-brand" aria-label={t("finance.cards.brand")} value={brand} {...fe.props("brand", "cd-brand")} onValueChange={v => { setBrand(v as CardBrand | ""); fe.clear("brand") }} none={t("finance.cards.noBrand")} options={brandOptions()}/>
      </Field>
      <Field label={t("finance.cards.last4")} htmlFor="cd-last4" error={last4Error ?? fe.of("last4")} hint={t("finance.cards.last4Hint")}>
        <Input id="cd-last4" inputMode="numeric" autoComplete="off" maxLength={4} placeholder="1234" value={last4} {...fe.props("last4", "cd-last4")} aria-invalid={!!(last4Error ?? fe.of("last4"))}
          onChange={e => { setLast4(e.target.value.replace(/\D/g, "").slice(0, 4)); setLast4Error(undefined); fe.clear("last4") }}/>
      </Field>
      <Field label={t("finance.cards.closingDay")} htmlFor="cd-closing" required error={fe.of("closing_day")}>
        <Select {...selectCopy()} id="cd-closing" aria-label={t("finance.cards.closingDay")} value={closing} {...fe.props("closing_day", "cd-closing")} onValueChange={v => { setClosing(v); fe.clear("closing_day") }} options={DAYS}/>
      </Field>
      <Field label={t("finance.cards.dueDay")} htmlFor="cd-due" required error={fe.of("due_day")}>
        <Select {...selectCopy()} id="cd-due" aria-label={t("finance.cards.dueDay")} value={due} {...fe.props("due_day", "cd-due")} onValueChange={v => { setDue(v); fe.clear("due_day") }} options={DAYS}/>
      </Field>
      <Field label={t("finance.cards.payWith")} htmlFor="cd-payer" required error={fe.of("paying_account_id")} hint={assets.length === 0 ? t("finance.cards.noAccounts") : undefined}>
        <Select {...selectCopy()} id="cd-payer" aria-label={t("finance.cards.payWith")} value={payer} {...fe.props("paying_account_id", "cd-payer")} onValueChange={v => { setPayer(v); fe.clear("paying_account_id") }} options={assets.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{card ? t("finance.cards.save") : t("finance.cards.create")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={() => onDone()}>{t("finance.cards.close")}</Button>
        {fe.general ? <p role="alert" className="text-sm text-danger">{fe.general}</p> : null}
      </div>
    </form>
  )
}
