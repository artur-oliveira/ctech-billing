import {describe, expect, it, vi} from "vitest"

import {buildSearchEntries, type SearchContext} from "@/lib/search/entries"

const t = (key: string) => key
const go = vi.fn()
const base: SearchContext = {
  portal: true,
  console: {reviewer: false, active: true},
  finance: {personal: true, canConfigure: true, canWrite: true},
}
const ids = (ctx: Partial<SearchContext>, current: "portal" | "console" | "finance" = "finance") =>
  buildSearchEntries({...base, ...ctx}, t, go, current).map(e => e.id)

describe("buildSearchEntries", () => {
  it("lists the current subproduct first, each in its nav order", () => {
    const list = ids({}, "console")
    expect(list[0]).toBe("console:/console/overview")
    expect(list.findIndex(i => i.startsWith("portal:"))).toBeGreaterThan(list.findIndex(i => i.startsWith("console:")))
  })

  it("has no Console entries without an organization, and no Portal ones without a billing account", () => {
    expect(ids({console: null}).some(i => i.includes("console:"))).toBe(false)
    expect(ids({portal: false}).some(i => i.startsWith("portal:"))).toBe(false)
  })

  it("Console without a tenant in this mode offers nothing from the console", () => {
    expect(ids({console: {reviewer: false, active: false}}).some(i => i.includes("console:"))).toBe(false)
  })

  it("Solicitações only for a reviewer", () => {
    expect(ids({}).includes("console:/console/applications")).toBe(false)
    expect(ids({console: {reviewer: true, active: true}}).includes("console:/console/applications")).toBe(true)
  })

  it("Plano only in a personal space, Conexões only where the person may configure an organization's", () => {
    expect(ids({}).includes("finance:/finance/plans")).toBe(true)
    expect(ids({}).includes("finance:/finance/settings/connections")).toBe(false)
    const org = {personal: false, canConfigure: true, canWrite: true}
    expect(ids({finance: org}).includes("finance:/finance/plans")).toBe(false)
    expect(ids({finance: org}).includes("finance:/finance/settings/connections")).toBe(true)
  })

  it("create actions follow the verbs: none for a viewer, no Nova conta without configure", () => {
    const viewer = {personal: true, canConfigure: false, canWrite: false}
    expect(ids({finance: viewer}).filter(i => i.startsWith("action:finance:"))).toEqual([])
    const writer = {personal: true, canConfigure: false, canWrite: true}
    const list = ids({finance: writer})
    expect(list).toContain("action:finance:recurrence")
    expect(list).not.toContain("action:finance:account")
  })

  it("Console create actions need an active tenant", () => {
    expect(ids({}).filter(i => i.startsWith("action:console:"))).toEqual(["action:console:product", "action:console:webhook"])
    expect(ids({console: {reviewer: false, active: false}}).filter(i => i.startsWith("action:console:"))).toEqual([])
  })

  it("an action asks to go to its screen with its kind; a page just carries its href", () => {
    const list = buildSearchEntries(base, t, go, "finance")
    list.find(e => e.id === "action:finance:recurrence")!.onSelect!()
    expect(go).toHaveBeenCalledWith("/finance/recurrences", "recurrence")
    expect(list.find(e => e.id === "finance:/finance/recurrences")).toMatchObject({kind: "page", href: "/finance/recurrences", group: "common.account.finance"})
  })

  it("keywords come from the catalog when it has them, and are empty when it does not", () => {
    const withKeywords = buildSearchEntries(base, key => key.startsWith("common.search.keywords.finance.cards") ? "cartão, fatura" : key, go, "finance")
    expect(withKeywords.find(e => e.id === "finance:/finance/cards")!.keywords).toEqual(["cartão", "fatura"])
    expect(buildSearchEntries(base, t, go, "finance").find(e => e.id === "finance:/finance/cards")!.keywords).toEqual([])
  })
})
