"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {useTranslation} from "react-i18next"

import {ReportsView} from "@/components/finance/ReportsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F7 — DRE and cash flow. `?view=cash` opens on the cash flow. */
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
  const view = useSearchParams().get("view") === "cash" ? "cash" : "dre"
  return <ReportsView view={view}/>
}
