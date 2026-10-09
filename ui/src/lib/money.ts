import {currentLocale} from "@/lib/i18n"
import type {SupportedLocale} from "@/lib/locale"
import limits from "@/lib/limits.json"

type Locale = SupportedLocale

/** Group and decimal separators of the money a person types, per language. */
function separators(locale: Locale): {group: string; dec: string} {
  return locale === "en" ? {group: ",", dec: "."} : {group: ".", dec: ","}
}

const escapeRe = (c: string) => c.replace(/[.,]/g, m => `\\${m}`)

/** What an empty money field hints at: "0,00" in pt-BR, "0.00" in English. */
export function moneyPlaceholder(locale: Locale = currentLocale()): string {
  return `0${separators(locale).dec}00`
}

/**
 * Money input to integer centavos, without ever touching a float, in the
 * notation of the language in effect (pt-BR: "1.234,56"; en: "1,234.56").
 *
 * Accepts "1.234,56", "1234,56", "10", "R$ 10", "0,5" in pt-BR. Refuses zero,
 * negatives, more than two decimals, mixed or repeated separators, and anything
 * past the API's ceiling. Ambiguous input is refused rather than guessed at:
 * "1234.5" in pt-BR (a dot groups thousands there) and "1.234,5" in English.
 */
export function parseMoney(input: string, locale: Locale = currentLocale()): number | null {
  const {group, dec} = separators(locale)
  const s = input.trim().replace(/^R\$\s*/, "")
  if (s === "") return null
  const at = s.indexOf(dec)
  let whole = s
  let frac = ""
  if (at >= 0) {
    if (s.indexOf(dec, at + 1) >= 0) return null
    whole = s.slice(0, at)
    frac = s.slice(at + 1)
  }
  const g = escapeRe(group)
  if (!new RegExp(`^(\\d{1,3}(${g}\\d{3})+|\\d+)$`).test(whole)) return null
  if (!/^\d{0,2}$/.test(frac)) return null
  const cents = BigInt(whole.split(group).join("")) * BigInt(100) + BigInt(frac.padEnd(2, "0"))
  // The ceiling is the API's (limits.json): nobody types a bigger amount than
  // R$ 9.999.999.999,99, and the server would refuse it anyway.
  if (cents <= BigInt(0) || cents > BigInt(limits.maxAmountCents)) return null
  return Number(cents)
}

const groupDigits = (digits: string, group: string) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, group)

/** Centavos to the text an input shows ("1.234,56" / "1,234.56"): the inverse of parseMoney. */
export function formatMoneyInput(cents: number, locale: Locale = currentLocale()): string {
  const {group, dec} = separators(locale)
  const whole = groupDigits(String(Math.trunc(cents / 100)), group)
  return `${whole}${dec}${String(cents % 100).padStart(2, "0")}`
}

/**
 * parseMoney for a balance, which may be negative (an overdrawn account is
 * real). A leading "-" or "−" negates; zero is still refused.
 */
export function parseSignedMoney(input: string, locale: Locale = currentLocale()): number | null {
  const s = input.trim()
  const negative = s.startsWith("-") || s.startsWith("−")
  const cents = parseMoney(negative ? s.slice(1) : s, locale)
  return cents === null ? null : negative ? -cents : cents
}

/** Integer digits a money field accepts: R$ 9.999.999.999 is ten. */
const MAX_INT_DIGITS = String(Math.floor(limits.maxAmountCents / 100)).length

/**
 * The mask applied while typing: thousands grouped, one decimal separator
 * before at most two decimals, no other characters, no leading zeros, and at
 * most ten integer digits, so the field cannot even show an amount past the
 * ceiling. `signed` keeps a leading minus (an overdrawn opening balance).
 * pt-BR groups with "." and takes "," as decimal; English the other way round.
 */
export function maskMoney(typed: string, opts: {signed?: boolean; locale?: Locale} = {}): string {
  const {group, dec} = separators(opts.locale ?? currentLocale())
  const trimmed = typed.trimStart()
  const negative = !!opts.signed && (trimmed.startsWith("-") || trimmed.startsWith("−"))
  const at = typed.indexOf(dec)
  const intRaw = (at < 0 ? typed : typed.slice(0, at)).replace(/\D/g, "")
  const decRaw = at < 0 ? null : typed.slice(at + 1).replace(/\D/g, "").slice(0, 2)
  let int = intRaw.replace(/^0+(?=\d)/, "").slice(0, MAX_INT_DIGITS)
  if (int === "" && decRaw !== null) int = "0"
  const grouped = groupDigits(int, group)
  const body = decRaw === null ? grouped : `${grouped}${dec}${decRaw}`
  return negative && body !== "" ? `-${body}` : negative ? "-" : body
}
