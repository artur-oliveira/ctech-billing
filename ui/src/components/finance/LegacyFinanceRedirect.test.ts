import {describe, expect, it} from "vitest"

import {legacyFinanceTarget} from "./LegacyFinanceRedirect"

describe("legacyFinanceTarget", () => {
  it("moves every old Finanças path to the same place under /finance, query kept", () => {
    expect(legacyFinanceTarget("/console/finance", "")).toBe("/finance")
    expect(legacyFinanceTarget("/console/finance/", "")).toBe("/finance")
    expect(legacyFinanceTarget("/console/finance/cards", "?card=c1")).toBe("/finance/cards?card=c1")
    expect(legacyFinanceTarget("/console/finance/spaces/created", "?organization_id=o&state=s"))
      .toBe("/finance/spaces/created?organization_id=o&state=s")
  })
})
