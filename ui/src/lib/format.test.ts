import {describe, expect, it} from "vitest"

import {countdown, longDate, money, period, shortDate} from "./format"

// Intl separates the symbol with a non-breaking space; normalise so the
// assertion is about the number and not about which space Node chose.
const brl = (cents: number) => money(cents).replace(/ /g, " ")

describe("money", () => {
  it("renders centavos as reais", () => {
    expect(brl(11300)).toBe("R$ 113,00")
    expect(brl(119900)).toBe("R$ 1.199,00")
  })

  it("keeps the cents that integer arithmetic exists to protect", () => {
    expect(brl(10)).toBe("R$ 0,10")
    expect(brl(1)).toBe("R$ 0,01")
    expect(brl(0)).toBe("R$ 0,00")
  })
})

describe("dates", () => {
  // The bug this guards: `new Date("2026-03-01")` is UTC midnight, which is the
  // 29th of February in São Paulo. Every due date would render a day early.
  it("reads a civil date as its own day, not as UTC", () => {
    expect(shortDate("2026-03-01")).toBe("01/03/2026")
    expect(longDate("2026-01-01")).toContain("1 de janeiro de 2026")
  })

  it("renders a period without repeating the year", () => {
    const out = period("2026-08-04", "2026-09-03")
    expect(out).toContain("4 de ago")
    expect(out).toContain("3 de set")
    expect(out).not.toContain("2026")
  })
})

describe("countdown", () => {
  it("pads the seconds so the width does not jump every tick", () => {
    expect(countdown(64)).toBe("1:04")
    expect(countdown(600)).toBe("10:00")
  })

  it("never goes negative", () => {
    expect(countdown(-5)).toBe("0:00")
  })
})
