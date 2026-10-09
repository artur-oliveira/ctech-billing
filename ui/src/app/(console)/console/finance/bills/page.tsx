"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"
import {useTranslation} from "react-i18next"

import {BillsView} from "@/components/finance/BillsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F2 — payables and receivables. `?direction=receivable&bill={id}` opens on that side with that bill marked. */
export default function FinanceBillsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.bills"))
  return (
    <Suspense>
      <Bills/>
    </Suspense>
  )
}

function Bills() {
  const params = useSearchParams()
  const direction = params.get("direction") === "receivable" ? "receivable" : "payable"
  const bill = params.get("bill") ?? undefined
  return <BillsView key={`${direction}-${bill ?? ""}`} direction={direction} focus={bill}/>
}
