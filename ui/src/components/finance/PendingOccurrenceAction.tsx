"use client";

import {Button, Field} from "@aoctech/ui";
import {useId, useState} from "react";
import {useTranslation} from "react-i18next";

import {FormError, FormErrorText} from "@/components/finance/FormError";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {MoneyInput} from "@/components/ui/MoneyInput";
import {confirmExpectedOccurrence, financeKeys, skipExpectedOccurrence, type FinanceCtx} from "@/lib/api/finance";
import type {PendingOccurrence} from "@/lib/api/financeTypes";
import {dueLabel, todayIso} from "@/lib/finance/today";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {money, shortDate} from "@/lib/format";
import limits from "@/lib/limits.json";
import {formatMoneyInput, parseMoney} from "@/lib/money";

const touched = (c: FinanceCtx) => [financeKeys.all(c.mode, c.space)];

/** A typed amount is usable when it is a positive sum the API accepts. */
export const validAmount = (cents: number | null): cents is number => cents !== null && cents > 0 && cents <= limits.maxAmountCents;

/**
 * What a recurring occurrence waiting for its real amount needs: the amount
 * (prefilled with the expected one), one tap to confirm, and a skip behind an
 * inline confirmation. Typing shows what the difference does to the month
 * ("R$ 20,00 acima do previsto"), from the occurrence's own expected amount.
 * Used by the inbox and under a recurrence's row.
 */
export function PendingOccurrenceAction({item}: {item: PendingOccurrence}) {
  const {t} = useTranslation();
  const id = useId();
  const [amountText, setAmountText] = useState(formatMoneyInput(item.expected_amount));
  const [askingSkip, setAskingSkip] = useState(false);
  const amount = parseMoney(amountText);
  const valid = validAmount(amount);
  const delta = valid ? amount - item.expected_amount : 0;
  const confirm = useFinanceMutation((c, value: number, key) => confirmExpectedOccurrence(c, item.recurrence_id, item.nominal, value, key), touched);
  const skip = useFinanceMutation((c, _: void, key) => skipExpectedOccurrence(c, item.recurrence_id, item.nominal, key), touched, () => setAskingSkip(false));
  const rel = dueLabel(item.due, todayIso());
  return (
    <div className="space-y-2">
      <p className="text-sm text-muted-foreground">
        {t("finance.inbox.expectedOccurrence", {date: shortDate(item.due), amount: money(item.expected_amount)})}{rel ? <span> • {rel}</span> : null}
      </p>
      <form className="flex flex-wrap items-end gap-2" onSubmit={e => { e.preventDefault(); if (valid && !confirm.isPending) confirm.mutate(amount); }}>
        <Field label={t("finance.inbox.actualAmount")} htmlFor={id} className="min-w-0 flex-1 basis-32 sm:max-w-44"
          error={amountText !== "" && !valid ? t("finance.inbox.amountInvalid") : undefined}>
          <MoneyInput id={id} value={amountText} onValueChange={setAmountText} aria-invalid={amountText !== "" && !valid ? true : undefined}/>
        </Field>
        <Button type="submit" size="sm" variant="brand" disabled={!valid || confirm.isPending}>{t("finance.inbox.confirmAmount")}</Button>
        {!askingSkip && <Button type="button" size="sm" variant="ghost" onClick={() => setAskingSkip(true)}>{t("finance.inbox.skip")}</Button>}
      </form>
      <p aria-live="polite" className="min-h-0 text-xs text-muted-foreground empty:hidden">
        {delta !== 0 ? t(delta > 0 ? "finance.inbox.deltaMore" : "finance.inbox.deltaLess", {amount: money(Math.abs(delta))}) : null}
      </p>
      <FormError error={confirm.error} className=""/>
      {askingSkip && (
        <InlineConfirm className="mt-0" announce text={t("finance.inbox.skipConfirm")} cancelLabel={t("finance.inbox.skipNo")} confirmLabel={t("finance.inbox.skipYes")}
          pending={skip.isPending} error={skip.error ? <FormErrorText error={skip.error}/> : null}
          onCancel={() => { setAskingSkip(false); skip.reset(); }} onConfirm={() => skip.mutate()}/>
      )}
    </div>
  );
}
