export const DEFAULT_LOCALE = "pt-BR"
export const ENGLISH_LOCALE = "en"
export const LOCALE_STORAGE_KEY = "i18nextLng"

export type SupportedLocale = typeof DEFAULT_LOCALE | typeof ENGLISH_LOCALE

export function normalizeLocale(value: string | null | undefined): SupportedLocale {
  return value?.toLowerCase().startsWith("en") ? ENGLISH_LOCALE : DEFAULT_LOCALE
}

export function persistLocalePreference(locale: SupportedLocale): void {
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  } catch {
    // Private mode: the choice lasts for the session only.
  }
}
