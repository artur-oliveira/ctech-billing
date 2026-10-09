"use client"

import {useTranslation} from "react-i18next"

import {BillsView} from "@/components/finance/BillsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F2 — payables and receivables. */
export default function FinanceBillsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.bills"))
  return <BillsView/>
}
