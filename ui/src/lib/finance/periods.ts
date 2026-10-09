/**
 * Report periods. Months are `YYYY-MM` strings and the arithmetic is on numbers,
 * never on `new Date("YYYY-MM-DD")` (which reads a civil date as UTC midnight).
 */
export type PresetId = "this_month" | "last_month" | "last_3" | "this_year" | "last_12"

export const PRESETS: {value: PresetId; label: string}[] = [
  {value: "this_month", label: "Este mês"},
  {value: "last_month", label: "Mês passado"},
  {value: "last_3", label: "Últimos 3 meses"},
  {value: "this_year", label: "Este ano"},
  {value: "last_12", label: "Últimos 12 meses"},
]

function shift(month: string, n: number): string {
  const [y, m] = month.split("-").map(Number)
  const i = y * 12 + (m - 1) + n
  return `${Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`
}

/** The preset's months, `from` and `to` both inclusive. */
export function monthRange(id: PresetId, today: string): {from: string; to: string} {
  const now = today.slice(0, 7)
  switch (id) {
    case "this_month": return {from: now, to: now}
    case "last_month": return {from: shift(now, -1), to: shift(now, -1)}
    case "last_3": return {from: shift(now, -2), to: now}
    case "this_year": return {from: `${now.slice(0, 4)}-01`, to: now}
    case "last_12": return {from: shift(now, -11), to: now}
  }
}

/** The preset as statement dates: `to` is exclusive, the day after the last month. */
export function dateRange(id: PresetId, today: string): {from: string; to: string} {
  const {from, to} = monthRange(id, today)
  return {from: `${from}-01`, to: `${shift(to, 1)}-01`}
}
