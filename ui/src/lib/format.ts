import type {Cents, IsoDate} from "@/lib/api/types"

const BRL = new Intl.NumberFormat("pt-BR", {
  style: "currency",
  currency: "BRL",
  minimumFractionDigits: 2,
})

/** Centavos to "R$ 1.234,56". Integer arithmetic only — never a float. */
export function money(cents: Cents, currency = "BRL"): string {
  if (currency !== "BRL") {
    return new Intl.NumberFormat("pt-BR", {style: "currency", currency}).format(cents / 100)
  }
  return BRL.format(cents / 100)
}

const LONG = new Intl.DateTimeFormat("pt-BR", {day: "numeric", month: "long", year: "numeric"})
const SHORT = new Intl.DateTimeFormat("pt-BR", {day: "2-digit", month: "2-digit", year: "numeric"})

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
export const longDate = (iso: IsoDate) => LONG.format(civil(iso))

/** "03/03/2026" — for dates in a column. */
export const shortDate = (iso: IsoDate) => SHORT.format(civil(iso))

/** "1 de mar a 31 de mar" — a billing period, without repeating the year. */
export function period(start: IsoDate, end: IsoDate): string {
  const f = new Intl.DateTimeFormat("pt-BR", {day: "numeric", month: "short"})
  return `${f.format(civil(start))} a ${f.format(civil(end))}`
}

/** Seconds to "12:04", for a PIX expiry the reader is watching run out. */
export function countdown(secondsLeft: number): string {
  const s = Math.max(0, Math.floor(secondsLeft))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}
