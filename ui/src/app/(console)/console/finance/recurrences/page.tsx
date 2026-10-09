"use client"

import {useTranslation} from "react-i18next"

import {RecurrencesView} from "@/components/finance/RecurrencesView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F4 — recurrences with an editor that previews before saving. */
export default function FinanceRecurrencesPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.recurrences"))
  return <RecurrencesView/>
}
