import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {BillsView} from "@/components/finance/BillsView"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import type {Account, Bill, Verb} from "@/lib/api/financeTypes"
import {todayIso} from "@/lib/finance/today"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "old-bank", name: "Banco antigo", class: "asset", system: false, archived: true, balance: 0},
  {id: "sys-payables", name: "Contas a pagar", class: "liability", system: true, archived: false, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "juros", name: "Juros", class: "expense", dre_group: "financial_result", system: false, archived: false, balance: 0},
  {id: "desc", name: "Descontos obtidos", class: "income", dre_group: "financial_result", system: false, archived: false, balance: 0},
  {id: "sal", name: "Salário", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
]
const bill = (over: Partial<Bill>): Bill => ({
  id: "b1", direction: "payable", amount: 150000, account_id: "cc", category_id: "alu", description: "Aluguel",
  competence_date: "2026-03-01", due_date: "2026-03-10", status: "forecast", origin: "manual", auto_settle: false,
  bucket: "upcoming", ...over,
})

function serve(verbs: Verb[], bills: Bill[]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{kind: "personal", label: "Pessoal", verbs}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listBills").mockImplementation(async (_c, dir) => ({data: bills.filter(b => b.direction === dir), has_more: false}))
}

async function openRow(description: string) {
  const row = (await screen.findByText(description)).closest("li") as HTMLElement
  await userEvent.click(within(row).getByRole("button", {name: "Dar baixa"}))
  return row
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F2 — a pagar e a receber", () => {
  it("lists overdue first, each bucket labelled in words", async () => {
    serve(ALL, [
      bill({id: "a", description: "Condomínio", due_date: "2026-03-01", bucket: "overdue"}),
      bill({id: "b", description: "Luz", due_date: "2026-03-10", bucket: "today"}),
      bill({id: "c", description: "Internet", due_date: "2026-03-20", bucket: "upcoming"}),
    ])
    renderWithQuery(<BillsView/>)
    await screen.findByText("Condomínio")
    const items = screen.getAllByRole("listitem").map(li => li.textContent ?? "")
    expect(items.findIndex(t => t.includes("Condomínio"))).toBeLessThan(items.findIndex(t => t.includes("Internet")))
    // The overdue row says so in words and a glyph, not colour alone.
    expect(within(screen.getByText("Condomínio").closest("li")!).getByText("Vencida")).toBeInTheDocument()
    expect(screen.getByRole("heading", {name: /Vencidas/})).toBeInTheDocument()
    expect(screen.getByRole("heading", {name: /A vencer/})).toBeInTheDocument()
  })

  it("requires a category for a different amount and says what the gap is", async () => {
    serve(ALL, [bill({})])
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    const confirm = within(row).getByRole("button", {name: "Confirmar baixa"})
    expect(confirm).toBeEnabled()
    const amount = within(row).getByLabelText("Valor pago")
    await userEvent.clear(amount)
    await userEvent.type(amount, "1.520,00")
    expect(within(row).getByText(/R\$ 20,00 a mais/)).toBeInTheDocument()
    expect(confirm).toBeDisabled()
    await userEvent.selectOptions(within(row).getByLabelText("Categoria da diferença"), "juros")
    expect(confirm).toBeEnabled()
    await userEvent.clear(amount)
    await userEvent.type(amount, "1.495,00")
    expect(within(row).getByText(/R\$ 5,00 a menos/)).toBeInTheDocument()
  })

  it("offers the gap only income and expense categories, never accounts or system rows", async () => {
    serve(ALL, [bill({})])
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    const amount = within(row).getByLabelText("Valor pago")
    await userEvent.clear(amount)
    await userEvent.type(amount, "1.600,00")
    const values = [...(within(row).getByLabelText("Categoria da diferença") as HTMLSelectElement).options].map(o => o.value).filter(Boolean)
    expect(values.sort()).toEqual(["alu", "desc", "juros", "sal"])
  })

  it("settles at the bill's amount and today unless changed, once on a double click", async () => {
    serve(ALL, [bill({})])
    let release: () => void = () => undefined
    const settle = vi.spyOn(finance, "settleBill").mockImplementation(() => new Promise(r => (release = () => r(bill({status: "paid"})))))
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    const confirm = within(row).getByRole("button", {name: "Confirmar baixa"})
    await userEvent.dblClick(confirm)
    await userEvent.click(confirm)
    expect(settle).toHaveBeenCalledTimes(1)
    const [, id, body, key] = settle.mock.calls[0]
    expect(id).toBe("b1")
    expect(body).toEqual({paid_date: todayIso()})
    expect(key).toBeTruthy()
    release()
  })

  it("reuses the key when a failed settle is retried", async () => {
    serve(ALL, [bill({})])
    const settle = vi.spyOn(finance, "settleBill")
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockResolvedValueOnce(bill({status: "paid"}))
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar baixa"}))
    await within(row).findByRole("alert")
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar baixa"}))
    await waitFor(() => expect(settle).toHaveBeenCalledTimes(2))
    expect(settle.mock.calls[0][3]).toBe(settle.mock.calls[1][3])
  })

  it("keeps the input and reloads the list on a 409", async () => {
    serve(ALL, [bill({})])
    vi.spyOn(finance, "settleBill").mockRejectedValue({response: {status: 409, data: {type: "/problems/invalid-transition", detail: "mudou"}}})
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    const amount = within(row).getByLabelText("Valor pago")
    await userEvent.clear(amount)
    await userEvent.type(amount, "1.600,00")
    await userEvent.selectOptions(within(row).getByLabelText("Categoria da diferença"), "juros")
    const calls = vi.mocked(finance.listBills).mock.calls.length
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar baixa"}))
    await within(row).findByText(/mudou enquanto/i)
    expect(within(row).getByLabelText("Valor pago")).toHaveValue("1.600,00")
    await waitFor(() => expect(vi.mocked(finance.listBills).mock.calls.length).toBeGreaterThan(calls))
  })

  it("offers a new payable only expense categories and active asset accounts", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    const cats = [...(screen.getByLabelText("Categoria") as HTMLSelectElement).options].map(o => o.value).filter(Boolean)
    expect(cats.sort()).toEqual(["alu", "juros"])
    const accts = [...(screen.getByLabelText("Pagar com") as HTMLSelectElement).options].map(o => o.value).filter(Boolean)
    expect(accts).toEqual(["cc"])
  })

  it("shows the auto-settle switch only to a role that may settle", async () => {
    serve(["finance.read", "finance.write"], [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    expect(screen.queryByLabelText("Baixa automática no vencimento")).toBeNull()
  })

  it("gives a viewer the list and no actions", async () => {
    serve(["finance.read"], [bill({})])
    renderWithQuery(<BillsView/>)
    await screen.findByText("Aluguel")
    expect(screen.queryByRole("button", {name: "Dar baixa"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Nova conta"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Editar"})).toBeNull()
  })

  it("teaches the first step when there is nothing open", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    expect(await screen.findByText(/nenhuma conta a pagar em aberto/i)).toBeInTheDocument()
  })
})
