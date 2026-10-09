"use client"

import Link from "next/link"
import {usePathname} from "next/navigation"

const TABS = [
  {href: "/console/finance", label: "Visão geral", exact: true},
  {href: "/console/finance/bills", label: "A pagar e a receber"},
  {href: "/console/finance/statement", label: "Extrato"},
  {href: "/console/finance/recurrences", label: "Recorrências"},
  {href: "/console/finance/reports", label: "Relatórios"},
  {href: "/console/finance/accounts", label: "Contas"},
] as const

/** The second row of navigation inside Finanças, under the console's own nav. */
export function FinanceTabs() {
  const pathname = usePathname()
  return (
    <nav aria-label="Finanças" className="-mt-2 border-b border-border">
      <ul className="-mb-px flex gap-1 overflow-x-auto">
        {TABS.map(tab => {
          const active = "exact" in tab ? pathname === tab.href : pathname.startsWith(tab.href)
          return (
            <li key={tab.href}>
              <Link
                href={tab.href}
                aria-current={active ? "page" : undefined}
                className={`inline-flex h-9 items-center whitespace-nowrap border-b-2 px-3 text-sm transition-colors ${
                  active ? "border-brand-600 font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"
                }`}
              >
                {tab.label}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
