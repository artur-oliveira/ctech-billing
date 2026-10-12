"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {useTranslation} from "react-i18next"

import {ReportsView} from "@/components/finance/ReportsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F7 — DRE, cash flow and card spending. `?view=cash` or `?view=cards` opens on that report. */
export default function FinanceReportsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.reports"))
  return (
    <Suspense>
      <Reports/>
    </Suspense>
  )
}

function Reports() {
  const asked = useSearchParams().get("view")
  const view = asked === "cash" || asked === "cards" || asked === "review" ? asked : "dre"
  return <ReportsView view={view}/>
}
