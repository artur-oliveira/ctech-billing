import {describe, expect, it} from "vitest"

import {consoleNav} from "./nav"

describe("consoleNav", () => {
  it("shows only Finanças until the session says there is an organization", () => {
    expect(consoleNav(false).map(i => i.label)).toEqual(["Finanças"])
  })
  it("shows the invoicing sections before Finanças once it does", () => {
    const labels = consoleNav(true).map(i => i.label)
    expect(labels[0]).toBe("Visão geral")
    expect(labels.at(-1)).toBe("Finanças")
  })
})
