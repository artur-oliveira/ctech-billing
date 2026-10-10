import type {Metadata} from "next"

import {FinanceShell} from "@/components/finance/FinanceShell"

export const metadata: Metadata = {title: "Finanças"}

/** Finanças' own area (the third, beside the portal and the console). */
export default function Layout({children}: LayoutProps<"/finance">) {
  return <FinanceShell>{children}</FinanceShell>
}
