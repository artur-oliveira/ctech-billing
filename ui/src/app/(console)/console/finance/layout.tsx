import type {Metadata} from "next"

import {FinanceNav} from "@/components/finance/FinanceNav"

export const metadata: Metadata = {title: "Finanças"}

export default function Layout({children}: LayoutProps<"/console/finance">) {
  return (
    <div className="grid gap-6 lg:grid-cols-[11rem_minmax(0,1fr)] lg:gap-10">
      <aside className="lg:sticky lg:top-6 lg:self-start">
        <FinanceNav/>
      </aside>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
