"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {StatementView} from "@/components/finance/StatementView"

/** F3 — an account's statement. `?account=` picks the account; none, the first. */
export default function FinanceStatementPage() {
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
