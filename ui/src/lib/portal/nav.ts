/** The portal's sections; the sidebar and the search read the same list. */
export const PORTAL_NAV = [
  {href: "/dashboard", labelKey: "portal.nav.home"},
  {href: "/invoices", labelKey: "portal.nav.invoices"},
  {href: "/subscriptions", labelKey: "portal.nav.subscriptions"},
] as const
