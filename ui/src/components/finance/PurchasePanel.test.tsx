import "@testing-library/jest-dom/vitest"

import {screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import {installmentsPreview, PurchasePanel} from "@/components/finance/PurchasePanel"
import * as finance from "@/lib/api/finance"
import type {Account} from "@/lib/api/financeTypes"

const ACCOUNTS: Account[] = [
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
]

afterEach(() => vi.restoreAllMocks())

describe("PurchasePanel", () => {
  it("previews the installments with the backend's rule: the remainder on the first", () => {
    expect(installmentsPreview(30001, 3).replace(/\s/g, " ")).toBe("R$ 100,01 + 2× de R$ 100,00")
    expect(installmentsPreview(30000, 3).replace(/\s/g, " ")).toBe("3× de R$ 100,00")
    expect(installmentsPreview(5000, 1).replace(/\s/g, " ")).toBe("à vista")
  })

  it("sends centavos and the count", async () => {
    const create = vi.spyOn(finance, "createPurchase").mockResolvedValue({} as never)
    renderWithQuery(<PurchasePanel cardId="visa" accounts={ACCOUNTS} onDone={() => {}}/>)
    await userEvent.type(screen.getByLabelText(/^Descrição/), "TV")
    await userEvent.type(screen.getByLabelText(/^Valor/), "300,01")
    await pick("Parcelas", "3×")
    await pick("Categoria", "Mercado")
    expect(screen.getByText(/R\$\s100,01 \+ 2× de R\$\s100,00/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", {name: "Registrar compra"}))
    expect(create).toHaveBeenCalledWith(expect.anything(), "visa",
      expect.objectContaining({total: 30001, installments: 3, category_id: "food", description: "TV"}), expect.any(String))
  })
})
