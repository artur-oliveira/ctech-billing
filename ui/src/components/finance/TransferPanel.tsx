"use client";

import limits from "@/lib/limits.json";
import {Button, Field, Input, Select} from "@aoctech/ui";

import {createTransfer, type FinanceCtx, financeKeys} from "@/lib/api/finance";
import type {Account, NewTransfer} from "@/lib/api/financeTypes";
import {DateField} from "@/components/ui/DateField";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {selectCopy} from "@/lib/selectCopy";
import {todayIso} from "@/lib/finance/today";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {parseMoney} from "@/lib/money";
import {useFieldErrors} from "@/lib/useFieldErrors";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {FormError} from "@/components/finance/FormError";
import {DrawerForm, PanelActions} from "@/components/finance/PanelForm";
import {accountName} from "@/lib/finance/accountName";

/** A transfer moves balances and every report reads them: refresh the whole space. */
export const wholeSpace = (c: FinanceCtx) => [financeKeys.all(c.mode, c.space)];

/**
 * Money moved between two of the space's own accounts. It is neither income nor
 * spending, so it shows on both statements and in no report.
 */
export function TransferPanel({accounts, from: initialFrom = "", onDone}: {accounts: Account[]; from?: string; onDone: () => void}) {
  const {t} = useTranslation();
  // A bank-synced account takes only its bank's lines (BYOK § 8.1): no manual transfer.
  const active = accounts.filter(a => a.class === "asset" && !a.system && !a.archived && a.source !== "byok");
  // The statement it opens from may be a synced account, which is not offered: an unlisted value would leave the select blank.
  const [from, setFrom] = useState(active.some(a => a.id === initialFrom) ? initialFrom : "");
  const [to, setTo] = useState("");
  const [amountText, setAmountText] = useState("");
  const [date, setDate] = useState(todayIso());
  const [memo, setMemo] = useState("");
  const fe = useFieldErrors(["from_account_id", "to_account_id", "amount", "date", "memo"]);
  const create = useFinanceMutation((c, body: NewTransfer, key) => createTransfer(c, body, key), wholeSpace, onDone, fe.set);
  const amount = parseMoney(amountText);
  // The form's own checks speak after the first try (the button stays enabled), so a person is told what is missing, not left with a dead button.
  const [tried, setTried] = useState(false);
  const missing = {
    from_account_id: from === "", to_account_id: to === "" || to === from, amount: amount === null || amount <= 0, date: date === "",
  };
  const local = (k: keyof typeof missing) => tried && missing[k] ? t(k === "amount" ? "bills.common.error.amount" : "bills.common.error.required") : undefined;
  return (
    <DrawerForm
      aria-label={t("bills.transfer.title")}
      onSubmit={e => {
        e.preventDefault();
        if (create.isPending) return;
        setTried(true);
        if (Object.values(missing).some(Boolean)) return;
        fe.reset();
        create.mutate({from_account_id: from, to_account_id: to, amount: amount!, date, memo: memo.trim() || undefined});
      }}
    >
      {active.length < 2 && <p role="note" className="text-sm text-muted-foreground">{t("bills.transfer.needTwo")}</p>}
      <Field label={t("bills.transfer.from")} htmlFor="tr-from" error={fe.of("from_account_id") ?? local("from_account_id")}>
        <Select {...selectCopy()} id="tr-from" aria-label={t("bills.transfer.from")} value={from} options={active.map(a => ({value: a.id, label: accountName(a)}))} {...fe.props("from_account_id", "tr-from", local("from_account_id"))}
          onValueChange={v => { setFrom(v); fe.clear("from_account_id"); if (v === to) setTo(""); }}/>
      </Field>
      <Field label={t("bills.transfer.to")} htmlFor="tr-to" error={fe.of("to_account_id") ?? local("to_account_id")}>
        <Select {...selectCopy()} id="tr-to" aria-label={t("bills.transfer.to")} value={to} {...fe.props("to_account_id", "tr-to", local("to_account_id"))} onValueChange={v => { setTo(v); fe.clear("to_account_id"); }}
          options={active.filter(a => a.id !== from).map(a => ({value: a.id, label: accountName(a)}))}/>
      </Field>
      <Field label={t("bills.common.amount")} htmlFor="tr-amount" error={fe.of("amount") ?? local("amount")}>
        <MoneyInput id="tr-amount" value={amountText} {...fe.props("amount", "tr-amount", local("amount"))} onValueChange={v => { setAmountText(v); fe.clear("amount"); }}/>
      </Field>
      <Field label={t("bills.common.date")} htmlFor="tr-date" error={fe.of("date") ?? local("date")}>
        <DateField id="tr-date" min={limits.minDate} max={todayIso()} value={date} invalid={!!(fe.of("date") ?? local("date"))} onValueChange={v => { setDate(v); fe.clear("date"); }}/>
      </Field>
      <Field label={t("bills.common.description")} htmlFor="tr-memo" error={fe.of("memo")} className="sm:col-span-full">
        <Input id="tr-memo" maxLength={limits.text.memo} placeholder={t("bills.transfer.placeholder")} value={memo} {...fe.props("memo", "tr-memo")} onChange={e => { setMemo(e.target.value); fe.clear("memo"); }}/>
      </Field>
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={create.isPending}>{t("bills.transfer.submit")}</Button>
        <FormError error={create.error} fe={fe} className=""/>
      </PanelActions>
    </DrawerForm>
  );
}
