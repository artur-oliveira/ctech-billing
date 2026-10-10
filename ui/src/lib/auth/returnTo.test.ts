import {describe, expect, it} from "vitest"

import {safeReturnTo} from "./returnTo"

describe("safeReturnTo", () => {
  it("keeps a path on this site", () => {
    expect(safeReturnTo("/finance?x=1")).toBe("/finance?x=1")
  })
  it.each(["//evil.com", "/\\evil.com", "https://evil.com", "javascript:alert(1)", "", "evil", "/\tevil"])("refuses %j", p => {
    expect(safeReturnTo(p)).toBe("/dashboard")
  })
})
