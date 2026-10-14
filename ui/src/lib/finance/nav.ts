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
] as const;

/** The sections this person sees in this space. */
export function visibleFinanceSections(personal: boolean, canConfigure: boolean) {
  return FINANCE_SECTIONS.filter(s => {
    if ("personalOnly" in s) return personal;
    if ("organizationConfigure" in s) return !personal && canConfigure;
    return true;
  });
}

/**
 * Pages that are not a section of their own but belong to one, so the menu
 * keeps showing where the person is. The lines to classify are a bank account's
 * (reached from Contas), not a summary: without this they fell through to the
 * first item, Resumo.
 */
const BELONGS_TO: readonly {prefix: string; section: string}[] = [
  {prefix: "/finance/unclassified", section: "/finance/accounts"},
];

/** The section a path belongs to; Resumo when none matches. */
export function currentFinanceSection(pathname: string): string {
  const owner = BELONGS_TO.find(b => pathname.startsWith(b.prefix));
  if (owner) return owner.section;
  return FINANCE_SECTIONS.find(s => ("exact" in s ? pathname === s.href : pathname.startsWith(s.href)))?.href ?? FINANCE_SECTIONS[0].href;
}
