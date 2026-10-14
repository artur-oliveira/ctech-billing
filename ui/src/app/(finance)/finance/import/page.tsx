"use client"

import {PageHeader} from "@aoctech/ui"
import {useSearchParams} from "next/navigation"
import {Suspense} from "react"
import {useTranslation} from "react-i18next"

import {ImportView} from "@/components/finance/ImportView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F6 — import a bank statement and reconcile it. `?account=` picks the account; none, the first. */
export default function FinanceImportPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.import"))
  return (
    <div className="space-y-6">
      <PageHeader title={t("finance.nav.import")}/>
      <Suspense>
        <Import/>
      </Suspense>
    </div>
  )
}

function Import() {
  const account = useSearchParams().get("account") ?? ""
  return <ImportView key={account} account={account}/>
}
