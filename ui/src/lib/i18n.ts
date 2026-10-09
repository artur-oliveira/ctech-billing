import i18n from "i18next"
import {initReactI18next} from "react-i18next"

import {DEFAULT_LOCALE, ENGLISH_LOCALE, type SupportedLocale} from "@/lib/locale"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"

/**
 * One catalog per language, one top-level key per area (common, portal,
 * console, finance...). Both bundles ship: the app is a static export with
 * a few hundred strings, and a lazy second bundle would only add a flash.
 *
 * It always boots in pt-BR so server HTML and the first client render match;
 * `LanguageSync` switches to the saved or browser language right after mount.
 */
i18n.use(initReactI18next).init({
  resources: {
    "pt-BR": {translation: ptBR},
    en: {translation: en},
  },
  lng: DEFAULT_LOCALE,
  fallbackLng: DEFAULT_LOCALE,
  supportedLngs: [DEFAULT_LOCALE, ENGLISH_LOCALE],
  interpolation: {escapeValue: false},
})

export const t = i18n.t.bind(i18n)

/** Whether the catalog has this key in the current language chain. */
export const i18nKeyExists = (key: string): boolean => i18n.exists(key)

/** The language in effect, for Intl formatters that run outside React. */
export const currentLocale = (): SupportedLocale =>
  i18n.language === ENGLISH_LOCALE ? ENGLISH_LOCALE : DEFAULT_LOCALE

export function changeAppLanguage(locale: SupportedLocale): Promise<unknown> {
  return i18n.changeLanguage(locale)
}

export default i18n
