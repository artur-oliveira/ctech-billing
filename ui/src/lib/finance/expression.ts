/**
 * The recurrence editor's model, independent of the markup.
 *
 * A pattern ("todo dia 10", "5º dia útil") plus exceptions (whole months,
 * single dates), converted to and from the 6.1 stored form the API parses
 * (`finance.ParseSchedule`). The bounds in `validate` are the server's own, so
 * an invalid model is caught here and never sent.
 */
import type {ExpressionJSON} from "@/lib/api/financeTypes"
import {weekdayLabel} from "@/lib/finance/labels"
import {currentLocale, t} from "@/lib/i18n"

export type Pattern =
  | {kind: "day_of_month"; day: number}
  | {kind: "workday_of_month"; n: number}
  | {kind: "nth_weekday_of_month"; weekday: number; n: number}
  | {kind: "weekly"; weekday: number; every: number; anchor: string}
  | {kind: "yearly"; month: number; day: number}

export interface Exceptions {
  months: number[] // 1..12
  dates: string[] // civil YYYY-MM-DD
}

export interface EditorModel {
  pattern: Pattern
  exceptions: Exceptions
}

export interface ModelError {
  field: string
  message: string
}

/** The month's name (1..12) in the current language, e.g. "março" / "March". */
export function monthName(month: number): string {
  return new Intl.DateTimeFormat(currentLocale(), {month: "long"}).format(new Date(2026, month - 1, 1))
}

/** 1º / 1ª in Portuguese (feminine when `fem`), 1st / 2nd in English. */
export function ordinal(n: number, fem = false): string {
  if (currentLocale() === "en") {
    const rule = new Intl.PluralRules("en", {type: "ordinal"}).select(n)
    return `${n}${{one: "st", two: "nd", few: "rd", other: "th"}[rule as "one" | "two" | "few" | "other"]}`
  }
  return `${n}${fem ? "ª" : "º"}`
}

const MAX_DATES = 366
// Days in each month of a leap year: the server admits 29/02 for "yearly".
const MAX_DAY = [31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]

function weekdayOf(iso: string): number {
  const [y, mo, d] = iso.split("-").map(Number)
  return new Date(y, mo - 1, d, 12).getDay()
}

function isCivilDate(s: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(s)
}

export function toExpression(m: EditorModel): ExpressionJSON {
  const p = m.pattern
  // Sunday is weekday 0: built explicitly so a falsy 0 is never dropped.
  let expr: ExpressionJSON =
    p.kind === "weekly" ? {kind: "weekly", weekday: p.weekday, every: p.every, anchor: p.anchor}
      : p.kind === "nth_weekday_of_month" ? {kind: "nth_weekday_of_month", weekday: p.weekday, n: p.n}
        : {...p}
  if (m.exceptions.months.length > 0) {
    expr = {kind: "difference", include: expr, exclude: {kind: "months_of_year", months: [...m.exceptions.months].sort((a, b) => a - b)}}
  }
  if (m.exceptions.dates.length > 0) {
    expr = {kind: "difference", include: expr, exclude: {kind: "dates", dates: [...m.exceptions.dates].sort()}}
  }
  return expr
}

/** The inverse of toExpression, for showing a stored recurrence. Null for a shape the editor cannot represent. */
export function fromExpression(e: ExpressionJSON): EditorModel | null {
  const exceptions: Exceptions = {months: [], dates: []}
  let cur: ExpressionJSON = e
  while (cur.kind === "difference") {
    if (cur.exclude.kind === "months_of_year") exceptions.months = [...cur.exclude.months]
    else if (cur.exclude.kind === "dates") exceptions.dates = [...cur.exclude.dates]
    else return null
    cur = cur.include
  }
  switch (cur.kind) {
    case "day_of_month":
    case "workday_of_month":
    case "nth_weekday_of_month":
    case "weekly":
    case "yearly":
      return {pattern: {...cur} as Pattern, exceptions}
    default:
      return null
  }
}

export function validate(m: EditorModel): ModelError[] {
  const errs: ModelError[] = []
  const p = m.pattern
  const int = (v: number) => Number.isInteger(v)
  switch (p.kind) {
    case "day_of_month":
      if (!int(p.day) || p.day < 1 || p.day > 31) errs.push({field: "day", message: t("bills.err.day")})
      break
    case "workday_of_month":
      if (!int(p.n) || (p.n !== -1 && (p.n < 1 || p.n > 23))) errs.push({field: "n", message: t("bills.err.workday")})
      break
    case "nth_weekday_of_month":
      if (!int(p.weekday) || p.weekday < 0 || p.weekday > 6) errs.push({field: "weekday", message: t("bills.err.weekday")})
      if (!int(p.n) || (p.n !== -1 && (p.n < 1 || p.n > 4))) errs.push({field: "n", message: t("bills.err.nth")})
      break
    case "weekly":
      if (!int(p.weekday) || p.weekday < 0 || p.weekday > 6) errs.push({field: "weekday", message: t("bills.err.weekday")})
      if (!int(p.every) || p.every < 1 || p.every > 52) errs.push({field: "every", message: t("bills.err.every")})
      if (!isCivilDate(p.anchor) || weekdayOf(p.anchor) !== p.weekday) {
        errs.push({field: "anchor", message: `A primeira data precisa cair numa ${weekdayLabel(p.weekday) ?? "data válida"}.`})
      }
      break
    case "yearly":
      if (!int(p.month) || p.month < 1 || p.month > 12) errs.push({field: "month", message: t("bills.err.month")})
      else if (!int(p.day) || p.day < 1 || p.day > MAX_DAY[p.month - 1]) {
        errs.push({field: "day", message: t("bills.err.monthDay", {month: monthName(p.month), day: p.day})})
      }
      break
  }
  if (m.exceptions.months.length >= 12) errs.push({field: "months", message: t("bills.err.allMonths")})
  if (m.exceptions.dates.length > MAX_DATES) errs.push({field: "dates", message: t("bills.err.maxDates", {max: MAX_DATES})})
  return errs
}

function list(words: string[]): string {
  return new Intl.ListFormat(currentLocale(), {style: "long", type: "conjunction"}).format(words)
}

/** The rule as one sentence ("Todo dia 10; exceto em dezembro"). No em dash, by the copy rule. */
export function describeModel(m: EditorModel): string {
  const p = m.pattern
  // Sunday and Saturday are masculine in Portuguese; the weekdays are feminine.
  const masc = (d: number) => d === 0 || d === 6
  let base: string
  switch (p.kind) {
    case "day_of_month":
      base = t("bills.rule.dayOfMonth", {day: p.day})
      break
    case "workday_of_month":
      base = p.n === -1 ? t("bills.rule.lastWorkday") : t("bills.rule.workday", {ord: ordinal(p.n)})
      break
    case "nth_weekday_of_month": {
      const day = weekdayLabel(p.weekday) ?? ""
      base = p.n === -1
        ? t(masc(p.weekday) ? "bills.rule.lastWeekdayMasc" : "bills.rule.lastWeekday", {day})
        : t("bills.rule.nthWeekday", {ord: ordinal(p.n, !masc(p.weekday)), day})
      break
    }
    case "weekly": {
      const day = weekdayLabel(p.weekday) ?? ""
      const key = p.every === 1 ? "bills.rule.weekly" : "bills.rule.weeklyEvery"
      base = t(masc(p.weekday) ? `${key}Masc` : key, {day, every: p.every})
      break
    }
    case "yearly":
      base = t("bills.rule.yearly", {day: p.day, month: monthName(p.month)})
      break
  }
  const parts: string[] = []
  if (m.exceptions.months.length > 0) {
    parts.push(t("bills.rule.exceptMonths", {months: list([...m.exceptions.months].sort((a, b) => a - b).map(monthName))}))
  }
  if (m.exceptions.dates.length > 0) {
    parts.push(t("bills.rule.exceptDates", {count: m.exceptions.dates.length}))
  }
  return parts.length === 0 ? base : t("bills.rule.except", {base, parts: parts.join(", ")})
}

/** The first day on or after `start` that falls on `weekday`: a weekly rule's anchor. */
export function defaultAnchor(weekday: number, start: string): string {
  const [y, mo, d] = start.split("-").map(Number)
  const date = new Date(y, mo - 1, d, 12)
  while (date.getDay() !== weekday) date.setDate(date.getDate() + 1)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
}

export function defaultModel(start: string): EditorModel {
  const day = Number(start.split("-")[2]) || 10
  return {pattern: {kind: "day_of_month", day}, exceptions: {months: [], dates: []}}
}
