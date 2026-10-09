"use client"

import {addYearsIso, todayIso} from "@/lib/finance/today"
import limits from "@/lib/limits.json"
import {Button, Field, Input} from "@aoctech/ui"
import {X} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {
  defaultAnchor, type EditorModel, type ModelError, monthName, ordinal, type Pattern,
} from "@/lib/finance/expression"
import {weekdayLabel} from "@/lib/finance/labels"
import {shortDate} from "@/lib/format"
import {t} from "@/lib/i18n"

// Option lists are built at render, never at import: the language can change.
const kindOptions = (): {value: Pattern["kind"]; label: string}[] =>
  (["day_of_month", "workday_of_month", "nth_weekday_of_month", "weekly", "yearly"] as const)
    .map(value => ({value, label: t(`bills.editor.kind.${value}`)}))
const weekdayOptions = () => [0, 1, 2, 3, 4, 5, 6].map(i => ({value: String(i), label: weekdayLabel(i) ?? ""}))
const monthOptions = () => Array.from({length: 12}, (_, i) => ({value: String(i + 1), label: monthName(i + 1)}))
const workdayOptions = () => [
  ...Array.from({length: 23}, (_, i) => ({value: String(i + 1), label: t("bills.editor.nthWorkday", {ord: ordinal(i + 1)})})),
  {value: "-1", label: t("bills.editor.lastWorkday")},
]
const nthOptions = () => [...[1, 2, 3, 4].map(n => ({value: String(n), label: ordinal(n, true)})), {value: "-1", label: t("bills.editor.last")}]

function patternFor(kind: Pattern["kind"], start: string, prev: Pattern): Pattern {
  const day = Number(start.split("-")[2]) || 10
  switch (kind) {
    case "day_of_month": return {kind, day: "day" in prev ? prev.day : day}
    case "workday_of_month": return {kind, n: 5}
    case "nth_weekday_of_month": return {kind, weekday: 1, n: 1}
    case "weekly": return {kind, weekday: 5, every: 1, anchor: defaultAnchor(5, start)}
    case "yearly": return {kind, month: Number(start.split("-")[1]) || 1, day}
  }
}

interface EditorProps {
  model: EditorModel
  start: string
  errors: ModelError[]
  onChange: (m: EditorModel) => void
}

/**
 * The pattern fields ("Repetição" and what it needs), as bare Fields so the
 * parent lays them out in its own row. Months longer than the chosen day fall
 * back to the last day; the preview shows it, so no hint is needed here.
 */
export function PatternFields({model, start, errors, onChange}: EditorProps) {
  const {t} = useTranslation()
  const p = model.pattern
  const err = (field: string) => errors.find(e => e.field === field)?.message
  const setPattern = (next: Pattern) => onChange({...model, pattern: next})
  return (
    <>
      <Field label={t("bills.editor.repeat")} htmlFor="rx-kind">
        <Select id="rx-kind" value={p.kind} onValueChange={k => setPattern(patternFor(k as Pattern["kind"], start, p))} options={kindOptions()}/>
      </Field>

      {p.kind === "day_of_month" && (
        <Field label={t("bills.editor.dayOfMonth")} htmlFor="rx-day" error={err("day")}>
          <Input id="rx-day" maxLength={2} inputMode="numeric" value={Number.isNaN(p.day) ? "" : String(p.day)} onChange={e => setPattern({...p, day: Number.parseInt(e.target.value, 10)})} aria-invalid={!!err("day")}/>
        </Field>
      )}

      {p.kind === "workday_of_month" && (
        <Field label={t("bills.editor.workday")} htmlFor="rx-n" error={err("n")}>
          <Select id="rx-n" value={String(p.n)} onValueChange={v => setPattern({...p, n: Number(v)})} options={workdayOptions()}/>
        </Field>
      )}

      {p.kind === "nth_weekday_of_month" && (
        <>
          <Field label={t("bills.editor.which")} htmlFor="rx-nth" error={err("n")}>
            <Select id="rx-nth" value={String(p.n)} onValueChange={v => setPattern({...p, n: Number(v)})} options={nthOptions()}/>
          </Field>
          <Field label={t("bills.editor.weekday")} htmlFor="rx-wd" error={err("weekday")}>
            <Select id="rx-wd" value={String(p.weekday)} onValueChange={v => setPattern({...p, weekday: Number(v)})} options={weekdayOptions()}/>
          </Field>
        </>
      )}

      {p.kind === "weekly" && (
        <>
          <Field label={t("bills.editor.weekday")} htmlFor="rx-wd" error={err("weekday")}>
            <Select
              id="rx-wd"
              value={String(p.weekday)}
              onValueChange={v => setPattern({...p, weekday: Number(v), anchor: defaultAnchor(Number(v), start)})}
              options={weekdayOptions()}
            />
          </Field>
          <Field label={t("bills.editor.everyWeeks")} htmlFor="rx-every" error={err("every")}>
            <Input id="rx-every" maxLength={2} inputMode="numeric" value={Number.isNaN(p.every) ? "" : String(p.every)} onChange={e => setPattern({...p, every: Number.parseInt(e.target.value, 10)})} aria-invalid={!!err("every")}/>
          </Field>
          {p.every > 1 && (
            <Field label={t("bills.editor.firstDate")} htmlFor="rx-anchor" error={err("anchor")} hint={t("bills.editor.firstDateHint")}>
              <DateField id="rx-anchor" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxRecurrenceYears)} value={p.anchor} onValueChange={anchor => setPattern({...p, anchor})}/>
            </Field>
          )}
        </>
      )}

      {p.kind === "yearly" && (
        <>
          <Field label={t("bills.editor.month")} htmlFor="rx-month" error={err("month")}>
            <Select id="rx-month" value={String(p.month)} onValueChange={v => setPattern({...p, month: Number(v)})} options={monthOptions()}/>
          </Field>
          <Field label={t("bills.editor.day")} htmlFor="rx-yday" error={err("day")}>
            <Input id="rx-yday" maxLength={2} inputMode="numeric" value={Number.isNaN(p.day) ? "" : String(p.day)} onChange={e => setPattern({...p, day: Number.parseInt(e.target.value, 10)})} aria-invalid={!!err("day")}/>
          </Field>
        </>
      )}
    </>
  )
}

/** The exceptions (whole months, single dates), shown under "Mais opções". */
export function ExceptionsFields({model, errors, onChange}: Omit<EditorProps, "start">) {
  const {t} = useTranslation()
  const err = (field: string) => errors.find(e => e.field === field)?.message
  const [newDate, setNewDate] = useState("")
  return (
    <div className="space-y-3">
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium text-foreground">{t("bills.editor.exceptMonths")}</legend>
        <div className="flex flex-wrap gap-1">
          {monthOptions().map(({label}, i) => {
            const month = i + 1
            const on = model.exceptions.months.includes(month)
            return (
              <button
                key={month}
                type="button"
                aria-pressed={on}
                onClick={() => onChange({...model, exceptions: {...model.exceptions, months: on ? model.exceptions.months.filter(x => x !== month) : [...model.exceptions.months, month]}})}
                className={`rounded-md border px-2 py-1 text-xs capitalize transition-colors ${on ? "border-brand-600 bg-brand-50 text-brand-700" : "border-border text-muted-foreground hover:text-foreground"}`}
              >
                {label.slice(0, 3)}
              </button>
            )
          })}
        </div>
        {err("months") && <p role="alert" className="text-sm text-danger">{err("months")}</p>}
      </fieldset>

      <div className="space-y-2">
        <Field label={t("bills.editor.exceptDates")} htmlFor="rx-date-add">
          <div className="flex gap-2">
            <DateField id="rx-date-add" min={limits.minDate} max={addYearsIso(todayIso(), limits.maxRecurrenceYears)} value={newDate} onValueChange={setNewDate}/>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!newDate || model.exceptions.dates.includes(newDate)}
              onClick={() => {
                onChange({...model, exceptions: {...model.exceptions, dates: [...model.exceptions.dates, newDate]}})
                setNewDate("")
              }}
            >
              {t("bills.editor.add")}
            </Button>
          </div>
        </Field>
        {model.exceptions.dates.length > 0 && (
          <ul className="flex flex-wrap gap-1">
            {[...model.exceptions.dates].sort().map(d => (
              <li key={d} className="flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-xs">
                {shortDate(d)}
                <button
                  type="button"
                  aria-label={t("bills.editor.remove", {date: shortDate(d)})}
                  onClick={() => onChange({...model, exceptions: {...model.exceptions, dates: model.exceptions.dates.filter(x => x !== d)}})}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <X aria-hidden className="size-3"/>
                </button>
              </li>
            ))}
          </ul>
        )}
        {err("dates") && <p role="alert" className="text-sm text-danger">{err("dates")}</p>}
      </div>
    </div>
  )
}
