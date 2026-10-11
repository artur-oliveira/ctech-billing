import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import {describe, expect, it} from "vitest"

import {NoMerchant} from "@/components/console/NoMerchant"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"

describe("NoMerchant", () => {
  it("names the organization and says billing is not on, with no action yet", () => {
    render(<NoMerchant name="Padaria Bom Dia"/>)
    expect(screen.getByText("Cobrança não ativada")).toBeInTheDocument()
    expect(screen.getByText(/Padaria Bom Dia ainda não cobra/)).toBeInTheDocument()
    expect(screen.queryByRole("button")).toBeNull()
  })

  it("has no em dash in the console copy, in either language", () => {
    for (const catalog of [ptBR, en]) {
      expect(JSON.stringify(catalog.console)).not.toContain("—")
    }
  })
})
