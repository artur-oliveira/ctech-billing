"use client"

import {Languages} from "lucide-react"
import {useTranslation} from "react-i18next"

import {changeAppLanguage} from "@/lib/i18n"
import {normalizeLocale, persistLocalePreference} from "@/lib/locale"

/** One tap flips pt-BR / en. Shows the language you will get, like the wallet's. */
export function LanguageSwitcher({className = ""}: { className?: string }) {
  const {i18n, t} = useTranslation()
  const next = normalizeLocale(i18n.language) === "en" ? "pt-BR" : "en"
  return (
    <button
      type="button"
      onClick={() => {
        persistLocalePreference(next)
        void changeAppLanguage(next)
      }}
      aria-label={t("common.changeLanguage", {language: next === "en" ? "English" : "Português"})}
      className={`inline-flex h-9 min-w-9 touch:h-11 touch:min-w-11 items-center justify-center gap-1.5 rounded-md px-2 text-xs font-medium uppercase text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring ${className}`}
    >
      <Languages className="size-4" aria-hidden="true"/>
      {next === "en" ? "EN" : "PT"}
    </button>
  )
}
