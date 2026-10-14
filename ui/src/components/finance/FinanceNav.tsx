"use client"

import {SidebarNav, type SidebarNavGroup} from "@aoctech/ui"
import {usePathname} from "next/navigation"
import {useTranslation} from "react-i18next"

import {useSpace} from "@/lib/console/useSpace"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {renderLink} from "@/components/shell/BillingShell"

// "Resumo" (not "Visão geral") "Visão geral": the console's own nav already has a "Visão geral"
// one row up, and two items with one name read as one menu printed twice.
export const FINANCE_SECTIONS = [
  {href: "/finance", key: "overview", exact: true},
  {href: "/finance/inbox", key: "inbox"},
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
 * Finanças' own sections, the shell's column, grouped like the phone's Mais
 * sheet. On a phone the column is not shown: FinanceBottomNav is the
 * navigation there.
 */
export function FinanceNav({canConfigure = false}: {canConfigure?: boolean}) {
  const {t} = useTranslation()
  const pathname = usePathname()
  const personal = useSpace().kind === "personal"
  const current = currentFinanceSection(pathname)
  const visible = visibleFinanceSections(personal, canConfigure)
  const group = (keys: string[], label?: string): SidebarNavGroup => ({
    label,
    items: visible.filter(s => keys.includes(s.key)).map(s => ({
      href: s.href, label: t(`finance.nav.${s.key}`), active: s.href === current,
    })),
  })
  const groups = [
    group(["overview", "inbox", "bills", "statement"]),
    group(["recurrences", "cards", "import"], t("finance.nav.groupLaunch")),
    group(["reports", "accounts", "plan", "connections"], t("finance.nav.groupManage")),
  ].filter(g => g.items.length > 0)
  return <SidebarNav label={t("finance.nav.label")} groups={groups} renderLink={renderLink}/>
}

/** FinanceNav with the space's own verbs (the shell's: the spaces query is cached). */
export function SpaceFinanceNav() {
  const {can} = useFinanceSpaces()
  return <FinanceNav canConfigure={can("finance.configure")}/>
}
