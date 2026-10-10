/** Labels are catalog keys, resolved with t() at render. */
/** Invoicing: an operator's sections, shown only with an organization. */
const INVOICING_NAV = [
  {href: "/console/overview", label: "console.nav.overview"},
  {href: "/console/invoices", label: "console.nav.invoices"},
  {href: "/console/subscriptions", label: "console.nav.subscriptions"},
  {href: "/console/customers", label: "console.nav.customers"},
  {href: "/console/catalog", label: "console.nav.catalog"},
  {href: "/console/settings", label: "console.nav.settings"},
] as const

/**
 * The console's sections: invoicing only. Finanças is its own area (/finance).
 * They appear only once the session has CONFIRMED an organization: shown while
 * it loads, they flashed for a person who has none and vanished on the 403 a
 * moment later.
 */
export function consoleNav(hasOrganization: boolean): {href: string; label: string}[] {
  return hasOrganization ? [...INVOICING_NAV] : []
}
