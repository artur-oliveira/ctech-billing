"use client";

import {Button, Field, Select} from "@aoctech/ui";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {FormError} from "@/components/finance/FormError";
import {DrawerForm, PanelActions} from "@/components/finance/PanelForm";
import {wholeSpace} from "@/components/finance/TransferPanel";
import {DateField} from "@/components/ui/DateField";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {addCardCredit} from "@/lib/api/finance";
import type {Account, NewCredit} from "@/lib/api/financeTypes";
import {accountName} from "@/lib/finance/accountName";
import {todayIso} from "@/lib/finance/today";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import limits from "@/lib/limits.json";
import {parseMoney} from "@/lib/money";
import {selectCopy} from "@/lib/selectCopy";
import {useFieldErrors} from "@/lib/useFieldErrors";

/** A credit on the card that is not the refund of a purchase on file: it lowers the open statement. */
export function CreditPanel({cardId, accounts, onDone}: {cardId: string; accounts: Account[]; onDone: () => void}) {
  const {t} = useTranslation();
  const cats = accounts.filter(a => a.class === "expense" && !a.system && !a.archived);
  const [amountText, setAmountText] = useState("");
  const [date, setDate] = useState(todayIso());
  const [category, setCategory] = useState("");
  const fe = useFieldErrors(["amount", "date", "category_id"]);
  const create = useFinanceMutation((c, body: NewCredit, key) => addCardCredit(c, cardId, body, key), wholeSpace, onDone, fe.set);
  const amount = parseMoney(amountText);
  const ready = amount !== null && amount > 0 && category !== "" && date !== "" && !create.isPending;
  return (
    <DrawerForm
      aria-label={t("finance.cards.creditTitle")}
      onSubmit={e => {
        e.preventDefault();
        if (!ready) return;
        fe.reset();
        create.mutate({date, category_id: category, amount: amount!});
      }}
    >
      <p className="text-sm text-muted-foreground sm:col-span-2">{t("finance.cards.creditHint")}</p>
      <Field label={t("bills.common.amount")} htmlFor="cr-amount" required error={fe.of("amount")}>
        <MoneyInput id="cr-amount" value={amountText} {...fe.props("amount", "cr-amount")} onValueChange={v => { setAmountText(v); fe.clear("amount"); }}/>
      </Field>
      <Field label={t("bills.common.date")} htmlFor="cr-date" error={fe.of("date")}>
        <DateField id="cr-date" value={date} invalid={!!fe.of("date")} onValueChange={v => { setDate(v); fe.clear("date"); }} min={limits.minDate} max={todayIso()}/>
      </Field>
      <Field label={t("bills.common.category")} htmlFor="cr-cat" required error={fe.of("category_id")} hint={cats.length === 0 ? t("bills.noCategory.expense") : undefined}>
        <Select {...selectCopy()} id="cr-cat" aria-label={t("bills.common.category")} value={category} {...fe.props("category_id", "cr-cat")} onValueChange={v => { setCategory(v); fe.clear("category_id"); }}
          options={cats.map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{t("finance.cards.creditSave")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("bills.common.close")}</Button>
        <FormError error={create.error} fe={fe} className=""/>
      </PanelActions>
    </DrawerForm>
  );
}
