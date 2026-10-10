"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {useTranslation} from "react-i18next"

import {StatementView} from "@/components/finance/StatementView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F3 — an account's statement. `?account=` picks the account; none, the first. */
export default function FinanceStatementPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.statement"))
  return (
    <Suspense>
      <Statement/>
    </Suspense>
  )
}

function Statement() {
  const account = useSearchParams().get("account") ?? ""
  return <StatementView key={account} account={account}/>
}
