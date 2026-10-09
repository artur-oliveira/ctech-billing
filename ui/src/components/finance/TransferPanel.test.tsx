import "@testing-library/jest-dom/vitest"

import {screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {optionsOf, pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import {TransferPanel} from "@/components/finance/TransferPanel"
import * as finance from "@/lib/api/finance"
import type {Account} from "@/lib/api/financeTypes"

const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "pp", name: "Poupança", class: "asset", system: false, archived: false, balance: 0},
  {id: "old", name: "Banco antigo", class: "asset", system: false, archived: true, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", system: false, archived: false, balance: 0},
]

afterEach(() => vi.restoreAllMocks())

describe("TransferPanel", () => {
  it("offers only active accounts and never the origin as the destination", async () => {
    renderWithQuery(<TransferPanel accounts={ACCOUNTS} onDone={() => {}}/>)
    expect(await optionsOf("De")).toEqual(["Conta corrente", "Poupança"])
    await pick("De", "Conta corrente")
    expect(await optionsOf("Para")).toEqual(["Poupança"])
  })

  it("refuses a transfer to the same account", async () => {
    renderWithQuery(<TransferPanel accounts={ACCOUNTS} onDone={() => {}}/>)
    await pick("De", "Conta corrente")
    await pick("Para", "Poupança")
    await userEvent.type(screen.getByLabelText("Valor"), "100,00")
    const submit = screen.getByRole("button", {name: "Transferir"})
    expect(submit).toBeEnabled()
    await pick("De", "Poupança")
    expect(submit).toBeDisabled()
  })

  it("sends centavos and the memo", async () => {
    const create = vi.spyOn(finance, "createTransfer").mockResolvedValue({transaction_id: "t"})
    const done = vi.fn()
    renderWithQuery(<TransferPanel accounts={ACCOUNTS} onDone={done}/>)
    await pick("De", "Conta corrente")
    await pick("Para", "Poupança")
    await userEvent.type(screen.getByLabelText("Valor"), "1.250,50")
    await userEvent.type(screen.getByLabelText("Descrição"), "Reserva")
    await userEvent.click(screen.getByRole("button", {name: "Transferir"}))
    expect(create).toHaveBeenCalledWith(expect.anything(),
      expect.objectContaining({from_account_id: "cc", to_account_id: "pp", amount: 125050, memo: "Reserva"}), expect.any(String))
    await vi.waitFor(() => expect(done).toHaveBeenCalled())
  })
})
