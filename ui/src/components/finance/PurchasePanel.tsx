"use client"

import {Button, Field, Input} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {wholeSpace} from "@/components/finance/TransferPanel"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {createPurchase} from "@/lib/api/finance"
import type {Account, NewPurchase} from "@/lib/api/financeTypes"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {money} from "@/lib/format"
import {t} from "@/lib/i18n"
import limits from "@/lib/limits.json"
import {maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"
import {accountName} from "@/lib/finance/accountName"

const countOptions = () => Array.from({length: limits.maxInstallments}, (_, i) => ({
  value: String(i + 1), label: i === 0 ? `1× (${t("bills.purchase.cash")})` : `${i + 1}×`,
}))

/**
 * The installments as the issuer prints them, with the backend's rule
 * (finance.SplitInstallments): the remainder of the division on the first.
 */
export function installmentsPreview(total: number, n: number): string {
  if (n <= 1) return t("bills.purchase.cash")
  const base = Math.floor(total / n)
  const first = base + (total - base * n)
  return first === base
    ? t("bills.purchase.each", {n, amount: money(base)})
    : t("bills.purchase.firstPlus", {first: money(first), n: n - 1, base: money(base)})
}

/** A new purchase on a card: the whole amount is the expense today; the
 *  installments only decide which statement bills each part. */
export function PurchasePanel({cardId, accounts, onDone}: {cardId: string; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation()
  const cats = accounts.filter(a => a.class === "expense" && !a.system && !a.archived)
  const [description, setDescription] = useState("")
  const [amountText, setAmountText] = useState("")
  const [date, setDate] = useState(todayIso())
  const [count, setCount] = useState("1")
  const [category, setCategory] = useState("")
  const fe = useFieldErrors(["description", "total", "installments", "date", "category_id"])
  const create = useFinanceMutation((c, body: NewPurchase, key) => createPurchase(c, cardId, body, key), wholeSpace, onDone, fe.set)
  const total = parseMoney(amountText)
  const n = Number(count)
  const ready = description.trim() !== "" && total !== null && total >= n && category !== "" && date !== "" && !create.isPending
  return (
    <form
      aria-label={t("bills.purchase.title")}
      className="grid items-start gap-x-4 gap-y-3 sm:grid-cols-2 [&>*]:min-w-0"
      onSubmit={e => {
        e.preventDefault()
        if (!ready) return
        fe.reset()
        create.mutate({date, description: description.trim(), category_id: category, total: total!, installments: n})
      }}
    >
      <Field label={t("bills.common.description")} htmlFor="pu-desc" required error={fe.of("description")} className="sm:col-span-2">
        <Input id="pu-desc" maxLength={limits.text.description} value={description} {...fe.props("description", "pu-desc")} onChange={e => { setDescription(e.target.value); fe.clear("description") }} autoFocus/>
      </Field>
      <Field label={t("bills.common.amount")} htmlFor="pu-amount" required error={fe.of("total")} hint={total ? installmentsPreview(total, n) : undefined}>
        <Input id="pu-amount" inputMode="decimal" placeholder={moneyPlaceholder()} value={amountText} {...fe.props("total", "pu-amount")} onChange={e => { setAmountText(maskMoney(e.target.value)); fe.clear("total") }}/>
      </Field>
      <Field label={t("bills.purchase.installments")} htmlFor="pu-count" error={fe.of("installments")}>
        <Select id="pu-count" aria-label={t("bills.purchase.installments")} value={count} {...fe.props("installments", "pu-count")} onValueChange={v => { setCount(v); fe.clear("installments") }} options={countOptions()}/>
      </Field>
      <Field label={t("bills.common.date")} htmlFor="pu-date" error={fe.of("date")}>
        <DateField id="pu-date" value={date} invalid={!!fe.of("date")} onValueChange={v => { setDate(v); fe.clear("date") }} min={limits.minDate} max={todayIso()}/>
      </Field>
      <Field label={t("bills.common.category")} htmlFor="pu-cat" required error={fe.of("category_id")} hint={cats.length === 0 ? t("bills.noCategory.expense") : undefined}>
        <Select id="pu-cat" aria-label={t("bills.common.category")} value={category} {...fe.props("category_id", "pu-cat")} onValueChange={v => { setCategory(v); fe.clear("category_id") }} options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{t("bills.purchase.save")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        {fe.general ? <p role="alert" className="text-sm text-danger">{fe.general}</p> : null}
      </div>
    </form>
  )
}
