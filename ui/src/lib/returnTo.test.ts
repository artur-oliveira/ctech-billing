import {describe, expect, it} from "vitest"

import {safeReturn} from "./returnTo"

const FALLBACK = "/dashboard"

describe("safeReturn", () => {
  it("keeps a same-site path, including its query", () => {
    expect(safeReturn("/invoices", FALLBACK)).toBe("/invoices")
    expect(safeReturn("/invoice?id=in_123", FALLBACK)).toBe("/invoice?id=in_123")
  })

  it("falls back when there is nothing to return to", () => {
    expect(safeReturn(null, FALLBACK)).toBe(FALLBACK)
    expect(safeReturn(undefined, FALLBACK)).toBe(FALLBACK)
    expect(safeReturn("", FALLBACK)).toBe(FALLBACK)
  })

  // The whole reason this function exists. Every one of these is a URL a
  // browser would navigate off-site for, and the first two are the ones a
  // naive `startsWith("/")` lets through.
  it("refuses anything that leaves this origin", () => {
    for (const hostile of [
      "//evil.example",
      "/\\evil.example",
      "https://evil.example",
      "http://evil.example",
      "javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "evil.example",
    ]) {
      expect(safeReturn(hostile, FALLBACK)).toBe(FALLBACK)
    }
  })
})
