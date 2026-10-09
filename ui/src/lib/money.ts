import limits from "@/lib/limits.json"

/**
 * Brazilian money input to integer centavos, without ever touching a float.
 *
 * Accepts "1.234,56", "1234,56", "10", "R$ 10", "0,5". Refuses zero, negatives,
 * more than two decimals, mixed or repeated separators, and anything past
 * Number.MAX_SAFE_INTEGER. "1234.5" is refused rather than guessed at: in pt-BR a
 * dot groups thousands, so it is either a typo or somebody else's notation.
 */
export function parseMoney(input: string): number | null {
  const s = input.trim().replace(/^R\$\s*/, "")
  if (s === "") return null
  const comma = s.indexOf(",")
  let whole = s
  let frac = ""
  if (comma >= 0) {
    if (s.indexOf(",", comma + 1) >= 0) return null
    whole = s.slice(0, comma)
    frac = s.slice(comma + 1)
  }
  if (!/^(\d{1,3}(\.\d{3})+|\d+)$/.test(whole)) return null
  if (!/^\d{0,2}$/.test(frac)) return null
  const cents = BigInt(whole.replaceAll(".", "")) * BigInt(100) + BigInt(frac.padEnd(2, "0"))
  // The ceiling is the API's (limits.json): nobody types a bigger amount than
  // R$ 9.999.999.999,99, and the server would refuse it anyway.
  if (cents <= BigInt(0) || cents > BigInt(limits.maxAmountCents)) return null
  return Number(cents)
}

/** Centavos to the text an input shows ("1234,56"): the inverse of parseMoney. */
export function formatMoneyInput(cents: number): string {
  const whole = String(Math.trunc(cents / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ".")
  return `${whole},${String(cents % 100).padStart(2, "0")}`
}

/**
 * parseMoney for a balance, which may be negative (an overdrawn account is
 * real). A leading "-" or "−" negates; zero is still refused.
 */
export function parseSignedMoney(input: string): number | null {
  const s = input.trim()
  const negative = s.startsWith("-") || s.startsWith("−")
  const cents = parseMoney(negative ? s.slice(1) : s)
  return cents === null ? null : negative ? -cents : cents
}

/** Integer digits a money field accepts: R$ 9.999.999.999 is ten. */
const MAX_INT_DIGITS = String(Math.floor(limits.maxAmountCents / 100)).length

/**
 * The pt-BR mask applied while typing: thousands grouped with ".", one ","
 * before at most two decimals, no other characters, no leading zeros, and at
 * most ten integer digits, so the field cannot even show an amount past the
 * ceiling. `signed` keeps a leading minus (an overdrawn opening balance).
 */
export function maskMoney(typed: string, opts: {signed?: boolean} = {}): string {
  const trimmed = typed.trimStart()
  const negative = !!opts.signed && (trimmed.startsWith("-") || trimmed.startsWith("−"))
  const comma = typed.indexOf(",")
  const intRaw = (comma < 0 ? typed : typed.slice(0, comma)).replace(/\D/g, "")
  const decRaw = comma < 0 ? null : typed.slice(comma + 1).replace(/\D/g, "").slice(0, 2)
  let int = intRaw.replace(/^0+(?=\d)/, "").slice(0, MAX_INT_DIGITS)
  if (int === "" && decRaw !== null) int = "0"
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, ".")
  const body = decRaw === null ? grouped : `${grouped},${decRaw}`
  return negative && body !== "" ? `-${body}` : negative ? "-" : body
}
