import {describe, expect, it} from "vitest"

import {dateRange, monthRange, PRESETS} from "./periods"

describe("periods", () => {
  it("resolves every preset from a civil today", () => {
    expect(monthRange("this_month", "2026-03-15")).toEqual({from: "2026-03", to: "2026-03"})
    expect(monthRange("last_month", "2026-01-15")).toEqual({from: "2025-12", to: "2025-12"})
    expect(monthRange("last_3", "2026-02-01")).toEqual({from: "2025-12", to: "2026-02"})
    expect(monthRange("this_year", "2026-10-08")).toEqual({from: "2026-01", to: "2026-12"}) // the whole year: generated bills of Nov/Dec are in the DRE
    expect(monthRange("last_12", "2026-10-08")).toEqual({from: "2025-11", to: "2026-10"})
  })
  it("gives statement dates with an exclusive end", () => {
    expect(dateRange("this_month", "2026-12-31")).toEqual({from: "2026-12-01", to: "2027-01-01"})
    expect(dateRange("last_12", "2026-10-08")).toEqual({from: "2025-11-01", to: "2026-11-01"})
  })
  it("labels every preset in Portuguese", () => {
    expect(PRESETS.map(p => p.label)).toEqual(["Este mês", "Mês passado", "Últimos 3 meses", "Este ano", "Últimos 12 meses"])
  })
})
