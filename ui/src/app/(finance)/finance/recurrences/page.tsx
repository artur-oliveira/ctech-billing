"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"
import {useTranslation} from "react-i18next"

import {RecurrencesView} from "@/components/finance/RecurrencesView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F4 — recurrences with an editor that previews before saving. `?recurrence=` opens one (a statement item links to it). */
export default function FinanceRecurrencesPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.recurrences"))
  return (
    <Suspense>
      <Recurrences/>
    </Suspense>
  )
}

function Recurrences() {
  const focus = useSearchParams().get("recurrence") ?? undefined
  return <RecurrencesView focus={focus}/>
}
