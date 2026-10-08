import type {Metadata} from "next"

import {FinanceTabs} from "@/components/finance/FinanceTabs"

export const metadata: Metadata = {title: "Finanças · Console"}

export default function Layout({children}: LayoutProps<"/console/finance">) {
  return (
    <div className="space-y-6">
      <FinanceTabs/>
      {children}
    </div>
  )
}
