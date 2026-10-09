/** Invoicing: an operator's sections, shown only with an organization. */
const INVOICING_NAV = [
  {href: "/console/overview", label: "Visão geral"},
  {href: "/console/invoices", label: "Faturas"},
  {href: "/console/subscriptions", label: "Assinaturas"},
  {href: "/console/customers", label: "Clientes"},
  {href: "/console/catalog", label: "Catálogo"},
  {href: "/console/settings", label: "Configurações"},
] as const

/** Finance: everyone's, in a personal space or an organization's (ADR 0025). */
export const FINANCE_HREF = "/console/finance"

/**
 * The console's sections. The invoicing ones appear only once the session has
 * CONFIRMED an organization: shown while it loads, they flashed for a person
 * who has none and vanished on the 403 a moment later.
 */
export function consoleNav(hasOrganization: boolean): {href: string; label: string}[] {
  return [...(hasOrganization ? INVOICING_NAV : []), {href: FINANCE_HREF, label: "Finanças"}]
}
