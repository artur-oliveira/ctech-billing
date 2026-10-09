"use client"

import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"

import {Select} from "@/components/ui/Select"

// "Resumo", not "Visão geral": the console's own nav already has a "Visão geral"
// one row up, and two items with one name read as one menu printed twice.
const SECTIONS = [
  {href: "/console/finance", label: "Resumo", exact: true},
  {href: "/console/finance/bills", label: "A pagar e a receber"},
  {href: "/console/finance/statement", label: "Extrato"},
  {href: "/console/finance/recurrences", label: "Recorrências"},
  {href: "/console/finance/reports", label: "Relatórios"},
  {href: "/console/finance/accounts", label: "Contas"},
] as const

/**
 * Finanças' own sections, one level under the console's nav: a column at the
 * left on a wide screen (a second row of tabs under the first looked like the
 * same menu twice), a picker on a phone.
 */
export function FinanceNav() {
  const pathname = usePathname()
  const router = useRouter()
  const current = SECTIONS.find(s => ("exact" in s ? pathname === s.href : pathname.startsWith(s.href)))?.href ?? SECTIONS[0].href
  return (
    <>
      <div className="lg:hidden">
        <Select aria-label="Seção" value={current} onValueChange={href => router.push(href)} options={SECTIONS.map(s => ({value: s.href, label: s.label}))}/>
      </div>
      <nav aria-label="Finanças" className="hidden lg:block">
        <ul className="space-y-0.5 border-l border-border">
          {SECTIONS.map(s => {
            const active = s.href === current
            return (
              <li key={s.href}>
                <Link
                  href={s.href}
                  aria-current={active ? "page" : undefined}
                  className={`-ml-px flex h-8 items-center border-l-2 pl-3 text-sm transition-colors ${
                    active ? "border-brand-600 font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {s.label}
                </Link>
              </li>
            )
          })}
        </ul>
      </nav>
    </>
  )
}
