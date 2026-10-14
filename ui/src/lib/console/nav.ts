/** Labels are catalog keys, resolved with t() at render. */
/** Invoicing: an operator's sections, shown only with an organization. */
const INVOICING_NAV = [
  {href: "/console/overview", label: "console.nav.overview"},
  {href: "/console/invoices", label: "console.nav.invoices"},
  {href: "/console/subscriptions", label: "console.nav.subscriptions"},
  {href: "/console/customers", label: "console.nav.customers"},
  {href: "/console/catalog", label: "console.nav.catalog"},
  {href: "/console/connections", label: "console.nav.connections"},
  {href: "/console/plan", label: "console.nav.plan"},
  {href: "/console/credentials", label: "console.nav.credentials"},
  {href: "/console/webhooks", label: "console.nav.webhooks"},
  {href: "/console/settings", label: "console.nav.settings"},
] as const;

/**
 * The console's sections: invoicing only. Finanças is its own area (/finance).
 * They appear only once the session has confirmed a tenant for the selected
 * organization in this mode: shown while it loads, they flashed for a person
 * who has none and vanished a moment later. Solicitações is added for the
 * organization whose tenant is tenant zero (the server decides; this is display).
 */
/** Tenant zero's review of merchant applications (spec § 4.2). */
const REVIEW_ITEM = {href: "/console/applications", label: "console.nav.applications"} as const;

export function consoleNav(hasOrganization: boolean, reviewer = false): {href: string; label: string}[] {
  if (!hasOrganization) return [];
  return reviewer ? [...INVOICING_NAV, REVIEW_ITEM] : [...INVOICING_NAV];
}
