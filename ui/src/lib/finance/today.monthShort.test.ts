import {describe, expect, it} from "vitest"

import {monthShort} from "./today"

describe("monthShort", () => {
  it("reads like a column header, capital first letter only", () => {
    expect(monthShort("2026-01")).toBe("Jan/26")
    expect(monthShort("2025-12")).toBe("Dez/25")
  })
})
