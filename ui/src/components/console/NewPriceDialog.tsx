"use client";

import {Field, Input, Modal, Select} from "@aoctech/ui";
import {useMutation} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {messageFor} from "@/lib/api/client";
import {createPrice} from "@/lib/api/console";
import {useConsoleScope} from "@/lib/console/scope";
import limits from "@/lib/limits.json";
import {parseMetadataText} from "@/lib/metadataText";
import {maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money";
import {selectCopy} from "@/lib/selectCopy";
import {useFieldErrors} from "@/lib/useFieldErrors";

/** A price is never edited: this is the only way one comes to exist (see the product page). */
export function NewPriceDialog({
  open,
  productId,
  onClose,
  onCreated,
}: { open: boolean; productId: string; onClose: () => void; onCreated: () => void }) {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const [amount, setAmount] = useState("");
  const [type, setType] = useState<"fixed" | "metered">("fixed");
  const [name, setName] = useState("");
  const [interval, setInterval] = useState("month");
  const [count, setCount] = useState("1");
  const [timing, setTiming] = useState<"advance" | "arrears">("advance");
  const [metadataText, setMetadataText] = useState("");

  // A price may be zero (free); anything else goes through the same locale-aware parse
  // as every money field, never through a float.
  const cents = /^0+([.,]0{0,2})?$/.test(amount.trim()) ? 0 : (parseMoney(amount) ?? -1);
  const everyN = Number(count);
  const metadata = parseMetadataText(metadataText);
  const valid = cents >= 0 && amount.trim() !== "" && Number.isInteger(everyN) && everyN >= 1 && metadata.ok;
  // A metered price billed in advance would have to guess the usage it charges
  // for, and the domain refuses it: the choice only exists for a fixed price.
  const effectiveTiming = type === "metered" ? "arrears" : timing;

  const fe = useFieldErrors(["unit_amount", "name"], {keepOthers: false});
  const create = useMutation({
    mutationFn: () =>
      createPrice(scope, {
        product_id: productId,
        type,
        unit_amount: cents,
        recurrence: {interval, count: everyN},
        billing_timing: effectiveTiming,
        name: name.trim() || undefined,
        metadata: metadata.ok && Object.keys(metadata.value).length > 0 ? metadata.value : undefined,
      }),
    onSuccess: () => {
      toast.success(t("console.product.createdDone"));
      setAmount("");
      setName("");
      setMetadataText("");
      onCreated();
    },
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)); },
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("console.product.newTitle")}
      description={t("console.product.newBody")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.product.create")}
      submitDisabled={!valid}
      loading={create.isPending}
      onSubmit={() => { fe.reset(); create.mutate(); }}
    >
      <div className="space-y-4">
        <Field
          label={t("console.product.priceName")}
          htmlFor="price-name"
          hint={t("console.product.priceNameHint")}
          error={fe.of("name")}
        >
          <Input
            id="price-name"
            maxLength={limits.text.priceName}
            value={name}
            {...fe.props("name", "price-name")}
            onChange={event => { setName(event.target.value); fe.clear("name"); }}
            placeholder="DF-e Pro"
          />
        </Field>

        <fieldset className="space-y-2">
          <legend className="text-sm font-medium text-foreground">{t("console.product.type")}</legend>
          <div className="flex gap-4">
            {(["fixed", "metered"] as const).map(option => (
              <label key={option} className="flex items-center gap-2 text-sm text-foreground">
                <input
                  type="radio"
                  name="price-type"
                  checked={type === option}
                  onChange={() => setType(option)}
                  className="size-4 accent-[var(--color-brand-600)]"
                />
                {option === "fixed" ? t("console.product.typeFixed") : t("console.product.typeMetered")}
              </label>
            ))}
          </div>
        </fieldset>

        <Field
          label={type === "metered" ? t("console.product.amountMetered") : t("console.product.amountFixed")}
          htmlFor="price-amount"
          error={fe.of("unit_amount")}
          hint={
            type === "metered"
              ? t("console.product.hintMetered")
              : t("console.product.hintFixed")
          }
        >
          <Input
            id="price-amount"
            inputMode="decimal"
            value={amount}
            {...fe.props("unit_amount", "price-amount")}
            onChange={event => { setAmount(maskMoney(event.target.value)); fe.clear("unit_amount"); }}
            placeholder={moneyPlaceholder()}
          />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label={t("console.product.every")} htmlFor="price-count">
            <Input
              id="price-count"
              inputMode="numeric"
              value={count}
              onChange={event => setCount(event.target.value.replace(/\D/g, ""))}
            />
          </Field>
          <Field label={t("console.product.intervalLabel")} htmlFor="price-interval">
            <Select {...selectCopy()}
              id="price-interval"
              aria-label={t("console.product.intervalLabel")}
              className="w-full"
              value={interval}
              onValueChange={setInterval}
              options={(["day", "week", "month", "year"] as const).map(v => ({value: v, label: t(`console.intervalUnit.${v}`)}))}
            />
          </Field>
        </div>

        {type === "fixed" ? (
          <Field label={t("console.product.timingLabel")} htmlFor="price-timing" hint={t("console.product.timingHint")}>
            <Select {...selectCopy()}
              id="price-timing"
              aria-label={t("console.product.timingLabel")}
              className="w-full"
              value={timing}
              onValueChange={v => setTiming(v as "advance" | "arrears")}
              options={[
                {value: "advance", label: t("console.timing.advance")},
                {value: "arrears", label: t("console.timing.arrears")},
              ]}
            />
          </Field>
        ) : (
          <p className="text-xs text-muted-foreground">{t("console.product.timingMetered")}</p>
        )}

        <Field
          label={t("console.product.metadataLabel")}
          htmlFor="price-metadata"
          hint={t("console.product.metadataHint")}
          error={metadata.ok ? undefined : t(`console.product.metadataError.${metadata.reason}`, {line: metadata.line})}
        >
          <textarea
            id="price-metadata"
            rows={5}
            spellCheck={false}
            value={metadataText}
            onChange={event => setMetadataText(event.target.value)}
            placeholder={"plan=pro\nquota_nfe=1200"}
            className="w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-sm text-foreground"
          />
        </Field>
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      </div>
    </Modal>
  );
}
