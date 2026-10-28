import type {Cents, IsoDate} from "@/lib/api/types";
import {currentLocale, t} from "@/lib/i18n";

/** Intl formatters per language, built once: the language is read at call time so a switch needs no reload. */
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat>();
function intl<T extends Intl.NumberFormat | Intl.DateTimeFormat>(kind: string, make: (locale: string) => T): T {
  const locale = currentLocale();
  const key = `${locale}:${kind}`;
  let f = cache.get(key);
  if (!f) cache.set(key, (f = make(locale)));
  return f as T;
}

/** Centavos to "R$ 1.234,56" (pt-BR) or "R$1,234.56" (en). Integer arithmetic only, never a float. */
export function money(cents: Cents, currency = "BRL"): string {
  return intl(`money:${currency}`, l => new Intl.NumberFormat(l, {style: "currency", currency, minimumFractionDigits: 2})).format(cents / 100);
}

/**
 * `YYYY-MM-DD` is a civil date in São Paulo, so it is parsed as local noon
 * rather than through `new Date(iso)`, which reads a bare date string as UTC
 * midnight and renders the day before for every reader west of Greenwich.
 */
function civil(iso: IsoDate): Date {
  const [y, m, d] = iso.split("-").map(Number);
  return new Date(y, m - 1, d, 12);
}

/** "3 de março de 2026" — for a single date the reader is meant to remember. */
export const longDate = (iso: IsoDate) =>
  intl("long", l => new Intl.DateTimeFormat(l, {day: "numeric", month: "long", year: "numeric"})).format(civil(iso));

/** "10 de outubro de 2026" for an RFC 3339 instant, in the reader's time zone. */
export const instantDate = (instant: string) =>
  intl("instant-long", l => new Intl.DateTimeFormat(l, {day: "numeric", month: "long", year: "numeric"})).format(new Date(instant));

/** "10/10/2026" for an RFC 3339 instant, on São Paulo's calendar (a connect at
 *  22:00 local is that day, not the next UTC one). For dates in a list. */
export const instantShortDate = (instant: string) =>
  intl("instant-short", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit", year: "numeric", timeZone: "America/Sao_Paulo"}))
    .format(new Date(instant));

/** The dash shown for a date the API does not have: absent, empty, or Go's zero
 *  time ("0001-01-01T00:00:00Z"), which an older row serialises as. */
export const NO_DATE = "—";
export const hasInstant = (instant?: string | null): instant is string =>
  !!instant && !instant.startsWith("0001-");

/** "03/03/2026" — for dates in a column. */
export const shortDate = (iso: IsoDate) =>
  intl("short", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit", year: "numeric"})).format(civil(iso));

/** "sáb." / "Sat" — beside a date in a list of dates, where the day of the week decides. */
export const weekdayShort = (iso: IsoDate) =>
  intl("weekday-short", l => new Intl.DateTimeFormat(l, {weekday: "short"})).format(civil(iso));

/** "10/03" (pt-BR) — a recent date, inside a sentence. */
export const dayMonth = (iso: IsoDate) =>
  intl("day-month-numeric", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit"})).format(civil(iso));

/** Today's civil date in São Paulo, `YYYY-MM-DD`. */
export const todayIso = (): IsoDate =>
  new Intl.DateTimeFormat("en-CA", {timeZone: "America/Sao_Paulo"}).format(new Date()) as IsoDate;

/**
 * "08/09" in this year, "31/12/25" in another: a row's date in a list whose
 * filter or heading already names the period. `today` is for tests.
 */
export function compactDate(iso: IsoDate, today: IsoDate = todayIso()): string {
  if (iso.slice(0, 4) === today.slice(0, 4)) return dayMonth(iso);
  return intl("short-yy", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit", year: "2-digit"})).format(civil(iso));
}

/** "08/09 • 07/10": a billing period in a list row. */
export const compactPeriod = (start: IsoDate, end: IsoDate, today: IsoDate = todayIso()) =>
  `${compactDate(start, today)} • ${compactDate(end, today)}`;

/**
 * Whole calendar days, in the reader's time zone, from now until the instant
 * (an RFC 3339 timestamp): 0 when it falls later today, 1 tomorrow.
 */
export function calendarDaysUntil(instant: string, now: Date = new Date()): number {
  const at = new Date(instant);
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate(), 12).getTime();
  return Math.round((day(at) - day(now)) / 86_400_000);
}

/** "1 de mar a 31 de mar" — a billing period, without repeating the year. */
export function period(start: IsoDate, end: IsoDate): string {
  const f = intl("day-month", l => new Intl.DateTimeFormat(l, {day: "numeric", month: "short"}));
  return t("common.periodRange", {start: f.format(civil(start)), end: f.format(civil(end))});
}

/** Seconds to "12:04", for a PIX expiry the reader is watching run out. */
export function countdown(secondsLeft: number): string {
  const s = Math.max(0, Math.floor(secondsLeft));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** "−R$ 300,00" (a real minus sign) for a negative amount, "R$ 300,00" otherwise; `plus` writes "+R$ 300,00" for a credit. */
export const signedMoney = (cents: Cents, plus = false) => (cents < 0 ? `−${money(-cents)}` : plus && cents > 0 ? `+${money(cents)}` : money(cents));

/**
 * A CPF ("000.000.000-00") or CNPJ ("00.000.000/0000-00") by the length of what
 * is given. Digits and the mask bullet "•" both count as a position, so the
 * masked value the API returns (bullets then the last four digits) comes out in
 * the document's own shape. Anything of another length is returned unchanged.
 */
export function formatTaxID(value: string): string {
  const chars = [...value].filter(c => c === "•" || (c >= "0" && c <= "9")).join("");
  const shape = chars.length === 11 ? "###.###.###-##" : chars.length === 14 ? "##.###.###/####-##" : null;
  if (shape === null) return value;
  let i = 0;
  return [...shape].map(c => (c === "#" ? chars[i++] : c)).join("");
}
