"use client";

import {SidebarNav, type SidebarNavGroup} from "@aoctech/ui";
import {usePathname} from "next/navigation";
import {useTranslation} from "react-i18next";

import {useSpace} from "@/lib/console/useSpace";
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces";
import {renderNavLink} from "@/components/shell/renderLink";
import {currentFinanceSection, visibleFinanceSections} from "@/lib/finance/nav";

/**
 * Finanças' own sections, the shell's column, grouped like the phone's Mais
 * sheet. On a phone the column is not shown: FinanceBottomNav is the
 * navigation there.
 */
export function FinanceNav({canConfigure = false}: {canConfigure?: boolean}) {
  const {t} = useTranslation();
  const pathname = usePathname();
  const personal = useSpace().kind === "personal";
  const current = currentFinanceSection(pathname);
  const visible = visibleFinanceSections(personal, canConfigure);
  const group = (keys: string[], label?: string): SidebarNavGroup => ({
    label,
    items: visible.filter(s => keys.includes(s.key)).map(s => ({
      href: s.href, label: t(`finance.nav.${s.key}`), active: s.href === current,
    })),
  });
  const groups = [
    group(["overview", "inbox", "bills", "statement"]),
    group(["recurrences", "cards", "import"], t("finance.nav.groupLaunch")),
    group(["reports", "accounts", "plan", "connections"], t("finance.nav.groupManage")),
  ].filter(g => g.items.length > 0);
  return <SidebarNav label={t("finance.nav.label")} groups={groups} renderLink={renderNavLink}/>;
}

/** FinanceNav with the space's own verbs (the shell's: the spaces query is cached). */
export function SpaceFinanceNav() {
  const {can} = useFinanceSpaces();
  return <FinanceNav canConfigure={can("finance.configure")}/>;
}
