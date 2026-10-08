import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {AccountsView} from "@/components/finance/AccountsView"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import type {Account, Verb} from "@/lib/api/financeTypes"

// A name also appears as an <option> of the default-receiving select, so rows
// are found inside their list items.
async function row(name: string) {
  await screen.findByRole("heading", {name: "Contas"})
  const el = screen.getAllByText(name).find(e => e.closest("li"))
  if (!el) throw new Error(`no row ${name}`)
  return el.closest("li") as HTMLElement
}

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "sys-payables", name: "Contas a pagar", class: "liability", system: true, archived: false, balance: -5000},
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 842315},
  {id: "pp", name: "Poupança", class: "asset", system: false, archived: false, balance: 0},
  {id: "sal", name: "Salário", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "old", name: "Assinaturas antigas", class: "expense", dre_group: "operating_expenses", system: false, archived: true, balance: 0},
]

function serve(verbs: Verb[], accounts = ACCOUNTS) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{kind: "personal", label: "Pessoal", verbs}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: accounts, has_more: false})
  vi.spyOn(finance, "getSettings").mockResolvedValue({})
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F8 — accounts", () => {
  it("never lists system accounts and groups the rest", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    expect(await row("Conta corrente")).toBeInTheDocument()
    expect(screen.queryByText("Contas a pagar")).toBeNull()
    expect(screen.getByRole("heading", {name: "Contas"})).toBeInTheDocument()
    expect(screen.getByRole("heading", {name: "Receitas"})).toBeInTheDocument()
    expect(screen.getByRole("heading", {name: "Despesas"})).toBeInTheDocument()
    expect(screen.getByText("R$ 8.423,15")).toBeInTheDocument()
  })

  it("hides archived items until asked", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(screen.queryByText("Assinaturas antigas")).toBeNull()
    await userEvent.click(screen.getByRole("button", {name: "Mostrar arquivadas"}))
    expect(screen.getByText("Assinaturas antigas")).toBeInTheDocument()
  })

  it("offers each class only the DRE groups the server accepts", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta ou categoria"}))
    await userEvent.selectOptions(screen.getByLabelText("Tipo"), "income")
    const groups = [...(screen.getByLabelText("Grupo na DRE") as HTMLSelectElement).options].map(o => o.value)
    expect(groups).toEqual(["gross_revenue", "financial_result", "other"])
    await userEvent.selectOptions(screen.getByLabelText("Tipo"), "asset")
    expect(screen.queryByLabelText("Grupo na DRE")).toBeNull()
  })

  it("creates without an id and keeps one key across a retry", async () => {
    serve(ALL)
    const create = vi.spyOn(finance, "createAccount")
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockResolvedValueOnce({id: "new", name: "Cartão", class: "asset", system: false, archived: false, balance: 0})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta ou categoria"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Cartão")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await screen.findByRole("alert")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
    const [first, second] = create.mock.calls
    expect(first[1]).not.toHaveProperty("id")
    expect(first[2]).toBe(second[2]) // the same intent, the same key
  })

  it("asks before archiving and says nothing is deleted", async () => {
    serve(ALL)
    const archive = vi.spyOn(finance, "archiveAccount").mockResolvedValue(undefined)
    renderWithQuery(<AccountsView/>)
    const li = await row("Poupança")
    await userEvent.click(within(li).getByRole("button", {name: "Arquivar"}))
    expect(within(li).getByText(/nada é apagado/i)).toBeInTheDocument()
    expect(archive).not.toHaveBeenCalled()
    await userEvent.click(within(li).getByRole("button", {name: "Confirmar"}))
    await waitFor(() => expect(archive).toHaveBeenCalledWith(expect.anything(), "pp", expect.any(String)))
  })

  it("shows a member no configuration controls", async () => {
    serve(["finance.read", "finance.write", "finance.settle", "finance.import"])
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(screen.queryByRole("button", {name: "Nova conta ou categoria"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Arquivar"})).toBeNull()
    expect(screen.queryByLabelText("Conta padrão de recebimento")).toBeNull()
  })

  it("offers only active asset accounts as the default receiving account", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    const select = await screen.findByLabelText("Conta padrão de recebimento")
    const values = [...(select as HTMLSelectElement).options].map(o => o.value).filter(Boolean)
    expect(values).toEqual(["cc", "pp"])
  })

  it("teaches the first step when there is nothing yet", async () => {
    serve(ALL, [])
    renderWithQuery(<AccountsView/>)
    expect(await screen.findByText(/crie a primeira/i)).toBeInTheDocument()
  })

  it("shows an error with a retry", async () => {
    serve(ALL)
    vi.mocked(finance.listAccounts).mockRejectedValue({response: {status: 500, data: {title: "Erro"}}})
    renderWithQuery(<AccountsView/>)
    expect(await screen.findByRole("button", {name: "Tentar de novo"})).toBeInTheDocument()
  })
})
