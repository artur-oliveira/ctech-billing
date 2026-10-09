/**
 * Today as a civil `YYYY-MM-DD` in the reader's clock, built from the local
 * date parts — never `toISOString()`, which is UTC and is yesterday every
 * evening in São Paulo.
 */
export function todayIso(now: Date = new Date()): string {
  const y = now.getFullYear()
  const m = String(now.getMonth() + 1).padStart(2, "0")
  const d = String(now.getDate()).padStart(2, "0")
  return `${y}-${m}-${d}`
}

/** "março de 2026" for a civil date's month. */
export function monthLabel(iso: string): string {
  const [y, m] = iso.split("-").map(Number)
  return new Intl.DateTimeFormat("pt-BR", {month: "long", year: "numeric"}).format(new Date(y, m - 1, 1))
}

const MONTHS = ["Jan", "Fev", "Mar", "Abr", "Mai", "Jun", "Jul", "Ago", "Set", "Out", "Nov", "Dez"]

/** "Mar/26" — a month in a table header or an axis, from `YYYY-MM`. */
export function monthShort(ym: string): string {
  const [y, m] = ym.split("-")
  return `${MONTHS[Number(m) - 1]}/${y.slice(2)}`
}
