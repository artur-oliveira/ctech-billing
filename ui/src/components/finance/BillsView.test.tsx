import "@testing-library/jest-dom/vitest"

import {act, screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {BillsView} from "@/components/finance/BillsView"
import {optionsOf, pick, renderWithQuery, serveFinanceMock} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import * as createRequest from "@/lib/finance/createRequest"
import type {Account, Bill, Verb} from "@/lib/api/financeTypes"
import {todayIso} from "@/lib/finance/today"

const toast = vi.hoisted(() => ({success: vi.fn(), error: vi.fn(), info: vi.fn()}))
vi.mock("sonner", () => ({toast}))

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
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listBills").mockImplementation(async (_c, dir) => ({data: bills.filter(b => b.direction === dir), has_more: false}))
}

async function openRow(description: string) {
  const row = (await screen.findByText(description)).closest("li") as HTMLElement
  await userEvent.click(within(row).getByRole("button", {name: "Pagar"}))
  return row
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F2 — a pagar e a receber", () => {
  // UX batch 3: a recurrence's overdue occurrence links here, to its bill.
  it("opens on the linked direction and marks the linked bill", async () => {
    serve(ALL, [bill({id: "r1", direction: "receivable", description: "Projeto"}), bill({id: "r2", direction: "receivable", description: "Consultoria", bucket: "overdue"})])
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    renderWithQuery(<BillsView direction="receivable" focus="r2"/>)
    const row = (await screen.findByText("Consultoria")).closest("li") as HTMLElement
    expect(row).toHaveAttribute("aria-current", "true")
    expect((await screen.findByText("Projeto")).closest("li")).not.toHaveAttribute("aria-current")
    expect(screen.getByRole("button", {name: "A receber"})).toHaveAttribute("aria-pressed", "true")
    await waitFor(() => expect(scroll).toHaveBeenCalled())
  })

  it("shows a card statement's bill as the statement, with no cancel and no amount to edit", async () => {
    serve(ALL, [bill({id: "f", description: "Fatura Visa 2026-03", category_id: "visa", origin: "card_statement", origin_ref: "visa#2026-03"})])
    renderWithQuery(<BillsView/>)
    const row = (await screen.findByText("Fatura Visa 2026-03")).closest("li") as HTMLElement
    expect(within(row).getByText(/Fatura do cartão/)).toBeInTheDocument()
    expect(within(row).getByRole("link", {name: "Ver fatura"})).toHaveAttribute("href", "/finance/cards?card=visa")
    expect(within(row).queryByRole("button", {name: "Excluir"})).not.toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    expect(within(row).queryByLabelText(/^Valor/)).not.toBeInTheDocument()
    expect(within(row).queryByLabelText(/^Categoria/)).not.toBeInTheDocument()
  })

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
    const confirm = within(row).getByRole("button", {name: "Confirmar pagamento"})
    expect(confirm).toBeEnabled()
    const amount = within(row).getByLabelText("Valor pago")
    await userEvent.clear(amount)
    await userEvent.type(amount, "1.520,00")
    expect(within(row).getByText(/R\$ 20,00 a mais/)).toBeInTheDocument()
    expect(confirm).toBeDisabled()
    await pick("Categoria da diferença", "Juros", row)
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
    expect((await optionsOf("Categoria da diferença", row)).sort()).toEqual(["Aluguel", "Descontos obtidos", "Juros", "Salário"])
  })

  it("settles at the bill's amount and today unless changed, once on a double click", async () => {
    serve(ALL, [bill({})])
    let release: () => void = () => undefined
    const settle = vi.spyOn(finance, "settleBill").mockImplementation(() => new Promise(r => (release = () => r(bill({status: "paid"})))))
    renderWithQuery(<BillsView/>)
    const row = await openRow("Aluguel")
    const confirm = within(row).getByRole("button", {name: "Confirmar pagamento"})
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
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar pagamento"}))
    await within(row).findByRole("alert")
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar pagamento"}))
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
    await pick("Categoria da diferença", "Juros", row)
    const calls = vi.mocked(finance.listBills).mock.calls.length
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar pagamento"}))
    await within(row).findByText(/mudou/i)
    expect(within(row).getByLabelText("Valor pago")).toHaveValue("1.600,00")
    await waitFor(() => expect(vi.mocked(finance.listBills).mock.calls.length).toBeGreaterThan(calls))
  })

  it("offers a new payable only expense categories and active asset accounts", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Adicionar"}))
    expect((await optionsOf("Categoria")).sort()).toEqual(["Aluguel", "Juros"])
    expect(await optionsOf("Pagar com")).toEqual(["Conta corrente"])
  })

  it("records a bill already paid in one go: created, then settled on the date given", async () => {
    serve(ALL, [])
    const create = vi.spyOn(finance, "createBill").mockResolvedValue(bill({id: "new"}))
    const settle = vi.spyOn(finance, "settleBill").mockResolvedValue(bill({id: "new", status: "paid"}))
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Adicionar"}))
    await userEvent.type(screen.getByLabelText(/^Valor/), "50,00")
    await pick("Categoria", "Aluguel")
    await userEvent.click(screen.getAllByLabelText("Já foi pago")[0])
    await pick("Pago com", "Conta corrente")
    expect(screen.queryByLabelText("Pagar automaticamente no vencimento")).toBeNull()
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(settle).toHaveBeenCalledWith(expect.anything(), "new", {paid_date: todayIso()}, expect.any(String)))
    expect(create).toHaveBeenCalledTimes(1)
  })

  it("names the create action and its panel without the word conta, which means a bank account", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Adicionar"}))
    expect(await screen.findByRole("dialog", {name: "Novo lançamento"})).toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Nova conta"})).toBeNull()
  })

  it.each([
    ["payable", "A pagar", "Já foi pago", "Pagamento", "Data do pagamento", "Pago com"],
    ["receivable", "A receber", "Já foi recebido", "Recebimento", "Data do recebimento", "Recebido em"],
  ])("relabels the %s section once it is already settled", async (_dir, tab, toggle, section, date, account) => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: tab}))
    await userEvent.click(screen.getByRole("button", {name: "Adicionar"}))
    const dialog = await screen.findByRole("dialog", {name: "Novo lançamento"})
    expect(within(dialog).queryByRole("group", {name: section})).toBeNull()
    await userEvent.click(within(dialog).getAllByLabelText(toggle)[0])
    const group = within(dialog).getByRole("group", {name: section})
    expect(within(group).getByLabelText(date)).toBeInTheDocument()
    expect(within(group).getByRole("combobox", {name: account})).toBeInTheDocument()
  })

  // The bug: the create's own invalidation refetched the list while the
  // settle was still in flight, so the bill flashed under "A pagar" and then
  // vanished. A bill created already paid must go straight to its final state.
  it("never shows a bill created already paid in the open list, and confirms it", async () => {
    let server: Bill[] = []
    serve(ALL, [])
    vi.mocked(finance.listBills).mockImplementation(async (_c, dir) => ({data: server.filter(b => b.direction === dir && b.status !== "paid"), has_more: false}))
    vi.spyOn(finance, "createBill").mockImplementation(async () => {
      server = [bill({id: "new", description: "Conta de luz", status: "forecast"})]
      return server[0]
    })
    let release: () => void = () => undefined
    vi.spyOn(finance, "settleBill").mockImplementation(() => new Promise(r => (release = () => {
      server = [bill({id: "new", description: "Conta de luz", status: "paid"})]
      r(server[0])
    })))
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Adicionar"}))
    await userEvent.type(screen.getByLabelText(/^Descrição/), "Conta de luz")
    await userEvent.type(screen.getByLabelText(/^Valor/), "50,00")
    await pick("Categoria", "Aluguel")
    await userEvent.click(screen.getAllByLabelText("Já foi pago")[0])
    await pick("Pago com", "Conta corrente")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(finance.settleBill).toHaveBeenCalled())
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByText("Conta de luz", {selector: "li *"})).toBeNull()
    release()
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Pagamento registrado"))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(screen.queryByText("Conta de luz", {selector: "li *"})).toBeNull()
  })

  // Closing the panel while the settle is in flight takes its inline retry with
  // it; a failure after that must still reach the person, who would otherwise
  // believe the bill was paid.
  it.each([
    ["payable", "A pagar", "Já foi pago", "Pago com", "Não foi possível registrar o pagamento"],
    ["receivable", "A receber", "Já foi recebido", "Recebido em", "Não foi possível registrar o recebimento"],
  ])("tells the person when the %s settle fails after the panel was closed", async (_dir, tab, toggle, account, message) => {
    serve(ALL, [])
    vi.spyOn(finance, "createBill").mockResolvedValue(bill({id: "new"}))
    let fail: () => void = () => undefined
    vi.spyOn(finance, "settleBill").mockImplementation(() => new Promise((_, reject) => (fail = () => reject({code: "ERR_NETWORK"}))))
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: tab}))
    await userEvent.click(screen.getByRole("button", {name: "Adicionar"}))
    await userEvent.type(screen.getByLabelText(/^Valor/), "50,00")
    await pick("Categoria", tab === "A pagar" ? "Aluguel" : "Salário")
    await userEvent.click(screen.getAllByLabelText(toggle)[0])
    await pick(account, "Conta corrente")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(finance.settleBill).toHaveBeenCalled())
    await userEvent.click(screen.getAllByRole("button", {name: "Fechar"})[0])
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    const calls = vi.mocked(finance.listBills).mock.calls.length
    fail()
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(message))
    await waitFor(() => expect(vi.mocked(finance.listBills).mock.calls.length).toBeGreaterThan(calls))
  })

  it("shows the auto-settle switch only to a role that may settle", async () => {
    serve(["finance.read", "finance.write"], [])
    renderWithQuery(<BillsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Adicionar"}))
    expect(screen.queryByLabelText("Pagar automaticamente no vencimento")).toBeNull()
  })

  it("gives a viewer the list and no actions", async () => {
    serve(["finance.read"], [bill({})])
    renderWithQuery(<BillsView/>)
    await screen.findByText("Aluguel")
    expect(screen.queryByRole("button", {name: "Pagar"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Adicionar"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Editar"})).toBeNull()
  })

  it("teaches the first step when there is nothing open", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    expect(await screen.findByText(/nada a pagar/i)).toBeInTheDocument()
  })
})

describe("the phone's central action", () => {
  it("does not open Novo lançamento for a role that cannot create", async () => {
    serve(["finance.read"], [])
    createRequest.requestCreate("bill")
    renderWithQuery(<BillsView/>)
    await screen.findByText("Nada a pagar em aberto.")
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("opens Novo lançamento when it was asked for before the screen mounted", async () => {
    serve(ALL, [])
    createRequest.requestCreate("bill")
    renderWithQuery(<BillsView/>)
    expect(await screen.findByRole("dialog", {name: "Novo lançamento"})).toBeInTheDocument()
  })

  it("opens Novo lançamento when asked on the screen", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    await screen.findByText(/Nada a pagar|Nenhum/)
    expect(screen.queryByRole("dialog")).toBeNull()
    act(() => createRequest.requestCreate("bill"))
    expect(await screen.findByRole("dialog", {name: "Novo lançamento"})).toBeInTheDocument()
  })

  it("ignores a request for another screen", async () => {
    serve(ALL, [])
    renderWithQuery(<BillsView/>)
    act(() => createRequest.requestCreate("transfer"))
    await new Promise(r => setTimeout(r, 0))
    expect(screen.queryByRole("dialog")).toBeNull()
    createRequest.takePendingCreate("transfer")
  })
})

// UX batch 4: on a phone a bill keeps Pagar on its line; Editar and Excluir are
// a left swipe or the row's "⋯", and Excluir still asks first.
describe("Agenda — a bill's secondary actions", () => {
  it("lists Editar and Excluir in ⋯, and Excluir opens its confirmation without deleting", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<BillsView/>)
    const li = (await screen.findByText("Compra do mês")).closest("li")!
    expect(within(li).getByRole("button", {name: "Pagar"})).toBeInTheDocument()
    await userEvent.click(within(li).getByRole("button", {name: "Mais ações: Compra do mês"}))
    const items = await screen.findAllByRole("menuitem")
    expect(items.map(i => i.textContent)).toEqual(["Editar", "Excluir"])
    await userEvent.click(items[1])
    expect(await within(li).findByText(/Excluir “Compra do mês”\?/)).toBeInTheDocument()
    expect(sent.some(r => r.url.endsWith("/cancel"))).toBe(false)
  })
})
