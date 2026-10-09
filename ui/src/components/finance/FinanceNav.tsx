"use client"

import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import {Select} from "@/components/ui/Select"

// "Resumo" (not "Visão geral") "Visão geral": the console's own nav already has a "Visão geral"
// one row up, and two items with one name read as one menu printed twice.
const SECTIONS = [
  {href: "/console/finance", key: "overview", exact: true},
  {href: "/console/finance/bills", key: "bills"},
  {href: "/console/finance/statement", key: "statement"},
  {href: "/console/finance/cards", key: "cards"},
  {href: "/console/finance/recurrences", key: "recurrences"},
  {href: "/console/finance/reports", key: "reports"},
  {href: "/console/finance/accounts", key: "accounts"},
] as const

/**
 * Finanças' own sections, one level under the console's nav: a column at the
 * left on a wide screen (a second row of tabs under the first looked like the
 * same menu twice), a picker on a phone.
 */
export function FinanceNav() {
  const {t} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const current = SECTIONS.find(s => ("exact" in s ? pathname === s.href : pathname.startsWith(s.href)))?.href ?? SECTIONS[0].href
  return (
    <>
      <div className="lg:hidden">
        <Select aria-label={t("finance.nav.section")} value={current} onValueChange={href => router.push(href)} options={SECTIONS.map(s => ({value: s.href, label: t(`finance.nav.${s.key}`)}))}/>
      </div>
      <nav aria-label={t("finance.nav.label")} className="hidden lg:block">
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
                  {t(`finance.nav.${s.key}`)}
                </Link>
              </li>
            )
          })}
        </ul>
      </nav>
    </>
  )
}
