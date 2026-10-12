"use client"

import {Select} from "@aoctech/ui"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import {useSpace} from "@/lib/console/useSpace"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {selectCopy} from "@/lib/selectCopy"

// "Resumo" (not "Visão geral") "Visão geral": the console's own nav already has a "Visão geral"
// one row up, and two items with one name read as one menu printed twice.
export const FINANCE_SECTIONS = [
  {href: "/finance", key: "overview", exact: true},
  {href: "/finance/bills", key: "bills"},
  {href: "/finance/statement", key: "statement"},
  {href: "/finance/import", key: "import"},
  {href: "/finance/cards", key: "cards"},
  {href: "/finance/recurrences", key: "recurrences"},
  {href: "/finance/reports", key: "reports"},
  {href: "/finance/accounts", key: "accounts"},
  // The plan is the person's (plans spec D2): listed in Pessoal only.
  {href: "/finance/plans", key: "plan", personalOnly: true},
  // Bank connections (BYOK spec 2026-10-11 § 11): an organization's, for
  // whoever may configure its space (owner, admin).
  {href: "/finance/settings/connections", key: "connections", organizationConfigure: true},
] as const

/** The sections this person sees in this space. */
export function visibleFinanceSections(personal: boolean, canConfigure: boolean) {
  return FINANCE_SECTIONS.filter(s => {
    if ("personalOnly" in s) return personal
    if ("organizationConfigure" in s) return !personal && canConfigure
    return true
  })
}

/** The section a path belongs to; Resumo when none matches. */
export function currentFinanceSection(pathname: string): string {
  return FINANCE_SECTIONS.find(s => ("exact" in s ? pathname === s.href : pathname.startsWith(s.href)))?.href ?? FINANCE_SECTIONS[0].href
}

/**
 * Finanças' own sections, one level under the console's nav: a column at the
 * left on a wide screen (a second row of tabs under the first looked like the
 * same menu twice), a picker on a tablet. On a phone neither: FinanceBottomNav
 * is the navigation there, and this renders nothing visible under `md`.
 */
export function FinanceNav({canConfigure = false}: {canConfigure?: boolean}) {
  const {t} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const current = currentFinanceSection(pathname)
  const personal = useSpace().kind === "personal"
  const sections = visibleFinanceSections(personal, canConfigure)
  return (
    <>
      <div className="hidden md:block lg:hidden">
        <Select {...selectCopy()} aria-label={t("finance.nav.section")} value={current} onValueChange={href => router.push(href)} options={sections.map(s => ({value: s.href, label: t(`finance.nav.${s.key}`)}))}/>
      </div>
      <nav aria-label={t("finance.nav.label")} className="hidden lg:block">
        <ul className="space-y-0.5 border-l border-border">
          {sections.map(s => {
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

/** FinanceNav with the space's own verbs (the shell's: the spaces query is cached). */
export function SpaceFinanceNav() {
  const {can} = useFinanceSpaces()
  return <FinanceNav canConfigure={can("finance.configure")}/>
}
