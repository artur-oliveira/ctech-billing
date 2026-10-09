"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {ReportsView} from "@/components/finance/ReportsView"

/** F7 — DRE and cash flow. `?view=cash` opens on the cash flow. */
export default function FinanceReportsPage() {
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
