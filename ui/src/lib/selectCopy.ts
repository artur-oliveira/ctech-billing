import {currentLocale, t} from "@/lib/i18n"

/**
 * Billing's own copy for @aoctech/ui's `Select` (UX batch 5): the console's
 * placeholder ("Escolher…" / "Choose…", not the catalogue's "Selecione…") and
 * the language in effect, so the built-in strings follow the console's switch.
 * Spread FIRST on every Select, so a call site's own `placeholder` wins:
 * `<Select {...selectCopy()} …/>`. Read at render, like the rest of the copy.
 */
export function selectCopy(): {locale: string; placeholder: string} {
  return {locale: currentLocale(), placeholder: t("auth.select.placeholder")}
}
