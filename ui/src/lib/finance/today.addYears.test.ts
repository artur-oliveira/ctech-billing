import {describe, expect, it} from "vitest"

import {addYearsIso} from "./today"

describe("addYearsIso", () => {
  it("moves civil years, Feb 29 landing on Feb 28", () => {
    expect(addYearsIso("2026-10-09", 10)).toBe("2036-10-09")
    expect(addYearsIso("2028-02-29", 1)).toBe("2029-02-28")
    expect(addYearsIso("2028-02-29", 4)).toBe("2032-02-29")
  })
})
