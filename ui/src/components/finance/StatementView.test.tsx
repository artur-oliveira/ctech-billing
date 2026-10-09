import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import {StatementView} from "@/components/finance/StatementView"
import * as finance from "@/lib/api/finance"
import type {Account, Statement, StatementEntry, Verb} from "@/lib/api/financeTypes"
import {dateRange} from "@/lib/finance/periods"
import {todayIso} from "@/lib/finance/today"
import {shortDate} from "@/lib/format"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 70000},
  {id: "pp", name: "Poupança", class: "asset", system: false, archived: false, balance: 10000},
  {id: "sys-receivables", name: "A receber", class: "asset", system: true, archived: false, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
]
const entry = (over: Partial<StatementEntry>): StatementEntry => ({
  transaction_id: "t1", date: "2026-03-10", amount: -30000, balance: 70000, kind: "settlement", memo: "Aluguel",
  category_id: "alu", bill_id: "b1", reversal: false, reversed: false, ...over,
})

function serve(verbs: Verb[], statements: Record<string, () => Statement>) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  return vi.spyOn(finance, "getStatement").mockImplementation(async (_c, id) => statements[id]())
}

const statement = (entries: StatementEntry[], opening = 100000): Statement => ({
  account_id: "cc", from: "2026-03-01", to: "2026-04-01", opening,
  closing: entries.reduce((b, e) => b + e.amount, opening), entries,
})

const rowOf = async (text: string) => (await screen.findByText(text)).closest("li") as HTMLElement

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F3 — extrato", () => {
  it("shows opening, running balances and closing for the period", async () => {
    serve(ALL, {cc: () => statement([entry({})])})
    renderWithQuery(<StatementView/>)
    const {from} = dateRange("this_month", todayIso())
    expect(await screen.findByText(`Saldo em ${shortDate(from)}`)).toBeInTheDocument()
    expect(screen.getByText("R$ 1.000,00")).toBeInTheDocument()
    const row = await rowOf("Aluguel")
    expect(within(row).getByText("−R$ 300,00")).toBeInTheDocument()
    expect(within(row).getAllByText("R$ 700,00").length).toBeGreaterThan(0)
    // The first non-system asset is the default account; system rows are not offered.
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Conta corrente")
  })

  it("reverses a transfer after confirming", async () => {
    let entries = [entry({transaction_id: "tr", kind: "transfer", memo: "Transferência", category_id: undefined, bill_id: undefined, amount: -10000, balance: 90000})]
    serve(ALL, {cc: () => statement(entries)})
    const reverse = vi.spyOn(finance, "reverseTransaction").mockImplementation(async () => {
      entries = [{...entries[0], reversed: true}, entry({transaction_id: "rv", kind: "transfer", memo: "Transferência", category_id: undefined, bill_id: undefined, amount: 10000, balance: 100000, reversal: true})]
      return {transaction_id: "rv"}
    })
    renderWithQuery(<StatementView/>)
    const row = (await screen.findAllByText("Transferência"))[0].closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Estornar"}))
    expect(within(row).getByText("Estornar este lançamento? O saldo volta ao que era.")).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}))
    expect(reverse).toHaveBeenCalledWith(expect.anything(), "tr", expect.any(String))
    expect(await screen.findByText("Estornado")).toBeInTheDocument()
    expect(screen.getByText("Estorno")).toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Estornar"})).not.toBeInTheDocument()
  })

  it("undoes a payment and hides Estornar on settlements", async () => {
    let entries = [entry({})]
    serve(ALL, {cc: () => statement(entries)})
    const unsettle = vi.spyOn(finance, "unsettleBill").mockImplementation(async () => {
      entries = [{...entries[0], reversed: true}, entry({transaction_id: "rv", amount: 30000, balance: 100000, reversal: true})]
      return {} as never
    })
    renderWithQuery(<StatementView/>)
    const row = await rowOf("Aluguel")
    expect(within(row).queryByRole("button", {name: "Estornar"})).not.toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Desfazer pagamento"}))
    expect(within(row).getByText(/A conta volta para A pagar e a receber/)).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}))
    expect(unsettle).toHaveBeenCalledWith(expect.anything(), "b1", expect.any(String))
    expect(await screen.findByText("Estornado")).toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Desfazer pagamento"})).not.toBeInTheDocument()
  })

  it("hides the actions from a viewer", async () => {
    serve(["finance.read"], {cc: () => statement([entry({}), entry({transaction_id: "tr", kind: "transfer", memo: "Saque", category_id: undefined, bill_id: undefined})])})
    renderWithQuery(<StatementView/>)
    await rowOf("Saque")
    expect(screen.queryByRole("button", {name: "Estornar"})).not.toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Desfazer pagamento"})).not.toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Nova transferência"})).not.toBeInTheDocument()
  })

  it("says so when the period has no entries, and still shows the balances", async () => {
    serve(ALL, {cc: () => statement([], 50000)})
    renderWithQuery(<StatementView/>)
    expect(await screen.findByText("Nada neste período.")).toBeInTheDocument()
    expect(screen.getAllByText("R$ 500,00").length).toBe(2)
  })

  it("switching account never shows the previous rows", async () => {
    let release: (s: Statement) => void = () => {}
    serve(ALL, {
      cc: () => statement([entry({memo: "Conta de luz"})]),
      pp: () => new Promise<Statement>(r => { release = r }) as never,
    })
    renderWithQuery(<StatementView/>)
    await rowOf("Conta de luz")
    await pick("Conta", "Poupança")
    expect(screen.queryByText("Conta de luz")).not.toBeInTheDocument()
    release(statement([entry({memo: "Rendimento", amount: 500})]))
    expect(await screen.findByText("Rendimento")).toBeInTheDocument()
  })

  it("points to Contas when there is no account yet", async () => {
    vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}], organizations_unavailable: false})
    vi.spyOn(finance, "listAccounts").mockResolvedValue({data: [], has_more: false})
    renderWithQuery(<StatementView/>)
    expect(await screen.findByText("Nenhuma conta ainda.")).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText("Criar conta").closest("a")).toHaveAttribute("href", "/console/finance/accounts"))
  })
})
