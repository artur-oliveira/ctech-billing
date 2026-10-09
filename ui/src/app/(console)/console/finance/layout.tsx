import {BottomNavSpacer} from "@aoctech/ui"
import type {Metadata} from "next"

import {FinanceBottomNav} from "@/components/finance/FinanceBottomNav"
import {FinanceNav} from "@/components/finance/FinanceNav"

export const metadata: Metadata = {title: "Finanças"}

/**
 * Finanças' sections: a column on a laptop, a picker on a tablet, a bar at the
 * bottom on a phone. The spacer keeps the end of every screen clear of that
 * bar (it collapses from `md`, where the bar is gone).
 */
export default function Layout({children}: LayoutProps<"/console/finance">) {
  return (
    <div className="grid gap-6 lg:grid-cols-[11rem_minmax(0,1fr)] lg:gap-10">
      <aside className="max-md:hidden lg:sticky lg:top-6 lg:self-start">
        <FinanceNav/>
      </aside>
      <div className="min-w-0">
        {children}
        <BottomNavSpacer/>
      </div>
      <FinanceBottomNav/>
    </div>
  )
}
