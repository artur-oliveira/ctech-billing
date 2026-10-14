"use client";

import {Button, Field, Input} from "@aoctech/ui";
import {useState, type FormEvent} from "react";
import {useTranslation} from "react-i18next";

import type {CollectionSettings as Settings} from "@/lib/api/consoleTypes";

/** "2,5" (or "2.5") → 250 basis points; "" → 0; anything else → NaN. */
export function percentToBasisPoints(raw: string): number {
  const s = raw.trim().replace(",", ".");
  if (s === "") return 0;
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return Number.NaN;
  return Math.round(Number(s) * 100);
}

function bpToText(bp: number): string {
  return (bp / 100).toLocaleString("pt-BR", {maximumFractionDigits: 2});
}

/**
 * "Usar o Inter para cobrar" and its late fees (BYOK spec § 7.2): the fine
 * and the monthly interest every Pix issued from now on carries. Percentages
 * with two decimals here; basis points on the wire's other side.
 */
export function CollectionSettings({initial, active, onSubmit, disabled}: {
  initial: Settings
  active: boolean
  onSubmit: (v: {late_fee_percent: number; monthly_interest_percent: number}) => Promise<unknown>
  disabled?: boolean
}) {
  const {t} = useTranslation();
  const [fine, setFine] = useState(bpToText(initial.fine_bp));
  const [interest, setInterest] = useState(bpToText(initial.monthly_interest_bp));
  const [errors, setErrors] = useState<{fine?: string; interest?: string}>({});
  const [saving, setSaving] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const fineBp = percentToBasisPoints(fine), interestBp = percentToBasisPoints(interest);
    const next: typeof errors = {};
    if (!(fineBp >= 0 && fineBp <= 2000)) next.fine = t("connections.collection.outOfRange", {min: 0, max: 20});
    if (!(interestBp >= 0 && interestBp <= 1000)) next.interest = t("connections.collection.outOfRange", {min: 0, max: 10});
    setErrors(next);
    if (next.fine || next.interest) return;
    setSaving(true);
    try {
      await onSubmit({late_fee_percent: fineBp / 100, monthly_interest_percent: interestBp / 100});
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={submit} noValidate className="max-w-xl space-y-4">
      <p className="text-pretty text-sm text-muted-foreground">
        {active ? t("connections.collection.active") : t("connections.collection.intro")}
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("connections.collection.fine")} htmlFor="collection-fine" hint={t("connections.collection.fineHint")} error={errors.fine}>
          <Input id="collection-fine" inputMode="decimal" value={fine} onChange={e => setFine(e.target.value)}
            aria-invalid={errors.fine ? true : undefined}/>
        </Field>
        <Field label={t("connections.collection.interest")} htmlFor="collection-interest" hint={t("connections.collection.interestHint")} error={errors.interest}>
          <Input id="collection-interest" inputMode="decimal" value={interest} onChange={e => setInterest(e.target.value)}
            aria-invalid={errors.interest ? true : undefined}/>
        </Field>
      </div>
      <Button type="submit" variant={active ? "outline" : "brand"} disabled={saving || disabled} className="w-full sm:w-auto">
        {active ? t("connections.collection.save") : t("connections.collection.use")}
      </Button>
    </form>
  );
}
