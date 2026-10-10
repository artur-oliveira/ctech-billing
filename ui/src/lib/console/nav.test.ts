import {describe, expect, it} from "vitest"

import {t} from "@/lib/i18n"

import {consoleNav} from "./nav"

// Finanças is its own area now (/finance): the console is invoicing only.
describe("consoleNav", () => {
  it("is empty until the session says there is an organization", () => {
    expect(consoleNav(false)).toEqual([])
  })
  it("lists only the invoicing sections once it does", () => {
    const labels = consoleNav(true).map(i => t(i.label))
    expect(labels[0]).toBe("Visão geral")
    expect(labels).not.toContain("Finanças")
  })
})
