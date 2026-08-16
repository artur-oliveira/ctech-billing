import {describe, expect, it} from "vitest"

import {portalKeys} from "./portal"

// The bug this guards against threw at runtime, on one navigation, with a
// message about `length` — nothing about it was visible from reading either
// screen. Two queries shared ["portal","invoices"], one storing a ListResponse
// and one storing TanStack's {pages, pageParams}, and whichever ran second read
// the other's shape.

const key = (k: readonly unknown[]) => k.join("/")

describe("portalKeys", () => {
  it("gives every invoice query its own key", () => {
    const keys = [
      portalKeys.invoiceList,
      portalKeys.invoicePages,
      portalKeys.invoice("inv_1"),
      portalKeys.invoice("inv_2"),
    ].map(key)

    expect(new Set(keys).size).toBe(keys.length)
  })

  it("keeps `invoices` a prefix of all of them, so one invalidation catches all", () => {
    const prefix = key(portalKeys.invoices)

    for (const k of [portalKeys.invoiceList, portalKeys.invoicePages, portalKeys.invoice("x")]) {
      expect(key(k).startsWith(`${prefix}/`)).toBe(true)
    }
  })

  it("never lets an invoice id collide with a list key", () => {
    // An id is a segment deeper than "list"/"pages", so even `getInvoice("list")`
    // lands somewhere else.
    expect(key(portalKeys.invoice("list"))).not.toBe(key(portalKeys.invoiceList))
    expect(key(portalKeys.invoice("pages"))).not.toBe(key(portalKeys.invoicePages))
  })
})
