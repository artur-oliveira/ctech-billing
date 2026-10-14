import type {CommandEntry} from "@aoctech/ui"

import {visibleFinanceSections} from "@/lib/finance/nav"
import {consoleNav} from "@/lib/console/nav"
import type {CreateKind} from "@/lib/createRequest"
import {PORTAL_NAV} from "@/lib/portal/nav"

export type View = "portal" | "console" | "finance"

export interface SearchContext {
  /** Has a billing account (the portal session did not answer 403). */
  portal: boolean
  /** null: the person runs no organization. `active`: it has a tenant in this mode. */
  console: {reviewer: boolean; active: boolean; addsRefused: boolean} | null
  finance: {personal: boolean; canConfigure: boolean; canWrite: boolean}
}

export type SearchText = (key: string) => string
/** Navigate to `href`; with `kind`, ask the screen there to open its create form. */
export type SearchGo = (href: string, kind?: CreateKind) => void

/** Create actions: kind, the screen that owns the form, the verb that may use it. */
const FINANCE_ACTIONS: {kind: CreateKind; href: string; verb: "write" | "configure"}[] = [
  {kind: "bill", href: "/finance/bills", verb: "write"},
  {kind: "purchase", href: "/finance/cards", verb: "write"},
  {kind: "recurrence", href: "/finance/recurrences", verb: "write"},
  {kind: "transfer", href: "/finance/statement", verb: "write"},
  {kind: "account", href: "/finance/accounts", verb: "configure"},
]
const CONSOLE_ACTIONS: {kind: CreateKind; href: string}[] = [
  {kind: "product", href: "/console/catalog"},
  {kind: "webhook", href: "/console/webhooks"},
]

/** Keywords for a screen, from `common.search.keywords.<key>` ("a, b, c"); none when the catalog has no entry. */
function keywordsOf(t: SearchText, key: string): string[] {
  const path = `common.search.keywords.${key}`
  const raw = t(path)
  return raw && raw !== path ? raw.split(",").map(s => s.trim()).filter(Boolean) : []
}

/**
 * What a person can search for: the screens of the three navs and the create
 * actions of the screens they own, filtered by what this person may reach. The
 * subproduct on screen comes first, so an empty query lists what is nearest.
 */
export function buildSearchEntries(ctx: SearchContext, t: SearchText, go: SearchGo, current: View): CommandEntry[] {
  const section = (view: View): CommandEntry[] => {
    const group = t(`common.account.${view}`)
    const page = (label: string, href: string, keywordKey: string): CommandEntry => ({
      id: `${view}:${href}`, label, group, kind: "page", href, keywords: keywordsOf(t, keywordKey),
    })
    const action = (kind: CreateKind, href: string): CommandEntry => ({
      id: `action:${view}:${kind}`, label: t(`common.search.actions.${kind}`), group, kind: "action",
      keywords: keywordsOf(t, `action.${kind}`), onSelect: () => go(href, kind),
    })
    if (view === "portal") {
      if (!ctx.portal) return []
      return PORTAL_NAV.map(n => page(t(n.labelKey), n.href, `portal.${n.href.slice(1)}`))
    }
    if (view === "console") {
      if (!ctx.console?.active) return []
      const pages = consoleNav(true, ctx.console.reviewer).map(n => page(t(n.label), n.href, `console.${n.href.split("/")[2]}`))
      // A suspended live tenant may not add integration surface: the screen would refuse.
      const actions = CONSOLE_ACTIONS.filter(a => !(a.kind === "webhook" && ctx.console?.addsRefused))
      return [...pages, ...actions.map(a => action(a.kind, a.href))]
    }
    const pages = visibleFinanceSections(ctx.finance.personal, ctx.finance.canConfigure)
      .map(s => page(t(`finance.nav.${s.key}`), s.href, `finance.${s.key}`))
    const actions = FINANCE_ACTIONS
      .filter(a => (a.verb === "write" ? ctx.finance.canWrite : ctx.finance.canConfigure))
      .map(a => action(a.kind, a.href))
    return [...pages, ...actions]
  }
  const order: View[] = [current, ...(["portal", "console", "finance"] as View[]).filter(v => v !== current)]
  return order.flatMap(section)
}
