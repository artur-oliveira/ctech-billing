"use client";

import {Button, Field, Input, Select, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {statusOf} from "@/lib/api/client";
import {financeKeys, getCsvMapping, putCsvMapping} from "@/lib/api/finance";
import type {CsvDateFormat, CsvDelimiter, CsvMapping} from "@/lib/api/financeTypes";
import {selectCopy} from "@/lib/selectCopy";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {FormError} from "@/components/finance/FormError";
import {PanelActions, PanelForm} from "@/components/finance/PanelForm";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {useFieldErrors} from "@/lib/useFieldErrors";

/** The usual Brazilian bank export: "Data;Histórico;Valor", one header row. */
export const DEFAULT_MAPPING: CsvMapping = {
  delimiter: ";", decimal: ",", date_format: "dd/mm/yyyy", skip_rows: 1, date_column: 1, description_column: 2, amount_column: 3, debit_column: 0,
};

const COLUMNS = ["date_column", "description_column", "amount_column", "debit_column"] as const;
type NumField = (typeof COLUMNS)[number] | "skip_rows"

const RANGE: Record<NumField, [number, number]> = {
  skip_rows: [0, 20], date_column: [1, 50], description_column: [1, 50], amount_column: [1, 50], debit_column: [0, 50],
};

/**
 * What is wrong with the typed numbers before anything is sent: a whole number
 * inside the field's range, and no two columns pointing at the same position
 * (the debit column is optional: 0 is "none"). Maps a field to its problem.
 */
export function mappingProblems(text: Record<string, string>): Partial<Record<NumField, "range" | "duplicate">> {
  const out: Partial<Record<NumField, "range" | "duplicate">> = {};
  const value = (k: NumField) => (text[k] === "" || text[k] === undefined ? (k === "debit_column" || k === "skip_rows" ? 0 : NaN) : Number(text[k]));
  for (const k of Object.keys(RANGE) as NumField[]) {
    const v = value(k);
    if (!Number.isInteger(v) || v < RANGE[k][0] || v > RANGE[k][1]) out[k] = "range";
  }
  for (const k of COLUMNS) {
    const v = value(k);
    if (out[k] || v === 0) continue;
    if (COLUMNS.some(o => o !== k && value(o) === v)) out[k] = "duplicate";
  }
  return out;
}

/**
 * How this account's CSV is read: saved once per account, then every CSV
 * upload to it uses it. Columns are counted from 1, as a spreadsheet shows them.
 */
export function CsvMappingForm({accountId, onDone}: {accountId: string; onDone: () => void}) {
  const ctx = useFinanceCtx();
  // A 404 is "never saved": the form starts from the usual layout.
  const saved = useQuery({
    queryKey: financeKeys.csvMapping(ctx.mode, ctx.space, accountId),
    queryFn: () => getCsvMapping(ctx, accountId).catch(e => { if (statusOf(e) === 404) return DEFAULT_MAPPING; throw e; }),
  });
  if (saved.error) return <ErrorBlock error={saved.error} onRetry={() => void saved.refetch()}/>;
  if (saved.isLoading || !saved.data) return <Skeleton className="h-24 w-full"/>;
  return <MappingFields accountId={accountId} initial={saved.data} onDone={onDone}/>;
}

function MappingFields({accountId, initial, onDone}: {accountId: string; initial: CsvMapping; onDone: () => void}) {
  const {t} = useTranslation();
  const [m, setM] = useState<CsvMapping>(initial);
  const [text, setText] = useState<Record<string, string>>(() =>
    Object.fromEntries(["skip_rows", ...COLUMNS].map(k => [k, String(initial[k as keyof CsvMapping] || (k === "debit_column" ? "" : 0))])));
  const fe = useFieldErrors(["delimiter", "decimal", "date_format", "skip_rows", ...COLUMNS]);
  const save = useFinanceMutation(
    (c, body: CsvMapping, key) => putCsvMapping(c, accountId, body, key),
    c => [financeKeys.csvMapping(c.mode, c.space, accountId)],
    onDone, fe.set,
  );
  const num = (k: string) => Number(text[k] || 0);
  // The form's own checks speak after the first try, so typing is not scolded half-way.
  const [tried, setTried] = useState(false);
  const problems = tried ? mappingProblems(text) : {};
  const local = (k: NumField) => problems[k] === "range" ? t("errors.field.out_of_range", {min: RANGE[k][0], max: RANGE[k][1]})
    : problems[k] === "duplicate" ? t("finance.import.mapping.duplicateColumn") : undefined;
  const number = (k: NumField, label: string, hint?: string) => (
    <Field key={k} label={label} htmlFor={`csv-${k}`} error={fe.of(k) ?? local(k)} hint={hint}>
      <Input id={`csv-${k}`} type="number" inputMode="numeric" min={RANGE[k][0]} max={RANGE[k][1]}
        value={text[k]} {...fe.props(k, `csv-${k}`, local(k))} onChange={e => { setText(s => ({...s, [k]: e.target.value})); fe.clear(k); }}/>
    </Field>
  );
  return (
    <PanelForm
      aria-label={t("finance.import.columns")}
      columns="sm:grid-cols-3"
      onSubmit={e => {
        e.preventDefault();
        fe.reset();
        setTried(true);
        if (Object.keys(mappingProblems(text)).length > 0) return;
        save.mutate({...m, skip_rows: num("skip_rows"), date_column: num("date_column"), description_column: num("description_column"),
          amount_column: num("amount_column"), debit_column: num("debit_column")});
      }}
    >
      <Field label={t("finance.import.mapping.delimiter")} htmlFor="csv-delimiter" error={fe.of("delimiter")}>
        <Select {...selectCopy()} id="csv-delimiter" aria-label={t("finance.import.mapping.delimiter")} value={m.delimiter} onValueChange={v => setM(x => ({...x, delimiter: v as CsvDelimiter}))}
          options={[{value: ";", label: t("finance.import.mapping.semicolon")}, {value: ",", label: t("finance.import.mapping.comma")}, {value: "\t", label: t("finance.import.mapping.tab")}]}/>
      </Field>
      <Field label={t("finance.import.mapping.decimal")} htmlFor="csv-decimal" error={fe.of("decimal")}>
        <Select {...selectCopy()} id="csv-decimal" aria-label={t("finance.import.mapping.decimal")} value={m.decimal} onValueChange={v => setM(x => ({...x, decimal: v as "," | "."}))}
          options={[{value: ",", label: t("finance.import.mapping.decimalComma")}, {value: ".", label: t("finance.import.mapping.decimalDot")}]}/>
      </Field>
      <Field label={t("finance.import.mapping.dateFormat")} htmlFor="csv-date-format" error={fe.of("date_format")}>
        <Select {...selectCopy()} id="csv-date-format" aria-label={t("finance.import.mapping.dateFormat")} value={m.date_format} onValueChange={v => setM(x => ({...x, date_format: v as CsvDateFormat}))}
          options={(["dd/mm/yyyy", "yyyy-mm-dd", "mm/dd/yyyy"] as const).map(f => ({value: f, label: t(`finance.import.mapping.formats.${f.replaceAll("/", "").replaceAll("-", "")}`)}))}/>
      </Field>
      {number("skip_rows", t("finance.import.mapping.skipRows"))}
      {number("date_column", t("finance.import.mapping.dateColumn"))}
      {number("description_column", t("finance.import.mapping.descriptionColumn"))}
      {number("amount_column", t("finance.import.mapping.amountColumn"))}
      {number("debit_column", t("finance.import.mapping.debitColumn"), t("finance.import.mapping.debitHint"))}
      <PanelActions>
        <Button type="submit" variant="brand" size="sm" disabled={save.isPending}>{t("finance.import.mapping.save")}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>{t("finance.import.back")}</Button>
        <FormError error={save.error} fe={fe} className=""/>
      </PanelActions>
    </PanelForm>
  );
}
