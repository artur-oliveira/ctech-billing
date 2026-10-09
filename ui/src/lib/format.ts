import type {Cents, IsoDate} from "@/lib/api/types"
import {currentLocale, t} from "@/lib/i18n"

/** Intl formatters per language, built once: the language is read at call time so a switch needs no reload. */
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat>()
function intl<T extends Intl.NumberFormat | Intl.DateTimeFormat>(kind: string, make: (locale: string) => T): T {
  const locale = currentLocale()
  const key = `${locale}:${kind}`
  let f = cache.get(key)
  if (!f) cache.set(key, (f = make(locale)))
  return f as T
}

/** Centavos to "R$ 1.234,56" (pt-BR) or "R$1,234.56" (en). Integer arithmetic only, never a float. */
export function money(cents: Cents, currency = "BRL"): string {
  return intl(`money:${currency}`, l => new Intl.NumberFormat(l, {style: "currency", currency, minimumFractionDigits: 2})).format(cents / 100)
}

/**
 * `YYYY-MM-DD` is a civil date in São Paulo, so it is parsed as local noon
 * rather than through `new Date(iso)`, which reads a bare date string as UTC
 * midnight and renders the day before for every reader west of Greenwich.
 */
function civil(iso: IsoDate): Date {
  const [y, m, d] = iso.split("-").map(Number)
  return new Date(y, m - 1, d, 12)
}

/** "3 de março de 2026" — for a single date the reader is meant to remember. */
export const longDate = (iso: IsoDate) =>
  intl("long", l => new Intl.DateTimeFormat(l, {day: "numeric", month: "long", year: "numeric"})).format(civil(iso))

/** "03/03/2026" — for dates in a column. */
export const shortDate = (iso: IsoDate) =>
  intl("short", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit", year: "numeric"})).format(civil(iso))

/** "1 de mar a 31 de mar" — a billing period, without repeating the year. */
export function period(start: IsoDate, end: IsoDate): string {
  const f = intl("day-month", l => new Intl.DateTimeFormat(l, {day: "numeric", month: "short"}))
  return t("common.periodRange", {start: f.format(civil(start)), end: f.format(civil(end))})
}

/** Seconds to "12:04", for a PIX expiry the reader is watching run out. */
export function countdown(secondsLeft: number): string {
  const s = Math.max(0, Math.floor(secondsLeft))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}

/** "−R$ 300,00" (a real minus sign) for a negative amount, "R$ 300,00" otherwise. */
export const signedMoney = (cents: Cents) => (cents < 0 ? `−${money(-cents)}` : money(cents))
