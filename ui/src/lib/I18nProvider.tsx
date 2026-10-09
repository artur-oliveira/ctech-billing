"use client"

import LanguageDetector from "i18next-browser-languagedetector"
import {Fragment, type ReactNode, useEffect, useRef} from "react"
import {I18nextProvider, useTranslation} from "react-i18next"

import i18n, {changeAppLanguage} from "@/lib/i18n"
import {LOCALE_STORAGE_KEY, normalizeLocale} from "@/lib/locale"

function LanguageSync() {
  const {i18n: instance} = useTranslation()
  const detectedOnce = useRef(false)

  useEffect(() => {
    if (detectedOnce.current) return
    detectedOnce.current = true
    let resolved: string | null = null
    try {
      resolved = window.localStorage.getItem(LOCALE_STORAGE_KEY)
    } catch {}
    if (!resolved) {
      const detector = new LanguageDetector()
      detector.init({languageUtils: instance.services.languageUtils})
      const detected = detector.detect()
      resolved = Array.isArray(detected) ? detected[0] : (detected ?? null)
    }
    const supported = normalizeLocale(resolved)
    if (supported !== instance.language) void changeAppLanguage(supported)
  }, [instance])

  useEffect(() => {
    document.documentElement.lang = normalizeLocale(instance.language)
  }, [instance.language])

  return null
}

/**
 * The tree is keyed by language: formatters in lib/format read the language at
 * call time, so a switch remounts the screen and every date, amount and label
 * is re-read in one go.
 */
function Keyed({children}: { children: ReactNode }) {
  const {i18n: instance} = useTranslation()
  return <Fragment key={instance.language}>{children}</Fragment>
}

export function I18nProvider({children}: { children: ReactNode }) {
  return (
    <I18nextProvider i18n={i18n}>
      <LanguageSync/>
      <Keyed>{children}</Keyed>
    </I18nextProvider>
  )
}
