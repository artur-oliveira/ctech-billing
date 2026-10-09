import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {OverviewView} from "@/components/finance/OverviewView"
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import {todayIso} from "@/lib/finance/today"
import type {Account, Bill, ProjectionMonth, Verb} from "@/lib/api/financeTypes"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 842315},
  {id: "pp", name: "Poupança", class: "asset", system: false, archived: false, balance: 1250000},
  {id: "old", name: "Banco antigo", class: "asset", system: false, archived: true, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", system: false, archived: false, balance: 0},
]
const bill = (o: Partial<Bill>): Bill => ({id: "x", direction: "payable", amount: 1000, account_id: "cc", category_id: "alu",
  description: "Conta", competence_date: "2026-03-01", due_date: "2026-03-10", status: "forecast", origin: "manual", auto_settle: false, ...o})
const MONTHS: ProjectionMonth[] = [
  {month: "2026-10", receivable: 0, payable: 225490, virtual: 0},
  {month: "2026-11", receivable: 350000, payable: 40000, virtual: 0},
  {month: "2026-12", receivable: 0, payable: 0, virtual: -150000},
]

function serve() {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{kind: "personal", label: "Pessoal", verbs: ALL}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listBills").mockImplementation(async (_c, dir) => ({
    data: dir === "payable"
      ? [bill({id: "a", description: "Aluguel atrasado", bucket: "overdue"}), bill({id: "b", description: "Internet", bucket: "upcoming"})]
      : [bill({id: "c", direction: "receivable", description: "Freela", bucket: "upcoming"})],
    has_more: false,
  }))
  vi.spyOn(finance, "getCashFlow").mockImplementation(async (_c, from, to) => ({
    from, to, opening_cash: 0, closing_cash: 120000,
    months: [{month: from, in: 300000, out: 180000, openings: 0, lines: []}],
  }))
  return vi.spyOn(finance, "getProjection").mockResolvedValue({data: MONTHS, has_more: false})
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F1 — visão geral", () => {
  it("lists the balance of each active account and the total", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    const block = await screen.findByRole("region", {name: "Saldos"})
    await within(block).findByText("Conta corrente")
    expect(within(block).queryByText("Banco antigo")).toBeNull()
    expect(within(block).queryByText("Aluguel")).toBeNull()
    expect(within(block).getByText("R$ 20.923,15")).toBeInTheDocument()
  })

  it("puts overdue items first in due soon", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    const block = await screen.findByRole("region", {name: "Vencidas e próximas"})
    await within(block).findByText("Aluguel atrasado")
    const text = block.textContent ?? ""
    expect(text.indexOf("Aluguel atrasado")).toBeLessThan(text.indexOf("Internet"))
  })

  it("keeps recurrences not yet generated apart from the bills", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    const block = await screen.findByRole("region", {name: "Projeção"})
    await userEvent.click(await within(block).findByRole("button", {name: "Ver tabela"}))
    const table = within(block).getByRole("table")
    const headers = within(table).getAllByRole("columnheader").map(h => h.textContent)
    expect(headers).toEqual(["Mês", "A receber", "A pagar", "Recorrências", "Resultado do mês", "Saldo projetado"])
    const dec = within(table).getByRole("row", {name: /dez/i})
    expect(within(dec).getAllByText("−R$ 1.500,00").length).toBe(2) // the recurrences and the month's result
  })

  // Today's balance (R$ 20.923,15 across the active accounts) plus each month's
  // result, accumulated: what the person will have if everything happens.
  it("accumulates the projected balance month by month, in the table and the chart", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    const block = await screen.findByRole("region", {name: "Projeção"})
    await waitFor(() => expect(block.querySelector("svg title")?.textContent?.replace(/\s/g, " ")).toMatch(/^Out\/26: saldo R\$ 18\.668,25 /))
    // The Y axis says what the bars are in: R$, from zero up past the highest.
    const ticks = [...block.querySelectorAll("svg [data-axis=y]")].map(t => t.textContent?.replace(/\s/g, " "))
    expect(ticks[0]).toBe("R$ 0")
    expect(ticks.at(-1)).toMatch(/^R\$ \d+ mil$/)
    await userEvent.click(within(block).getByRole("button", {name: "Ver tabela"}))
    const rows = within(within(block).getByRole("table")).getAllByRole("row").slice(1)
    expect(rows.map(r => r.lastElementChild?.textContent?.replace(/\s/g, " "))).toEqual(["R$ 18.668,25", "R$ 21.768,25", "R$ 20.268,25"])
  })

  it("shows money in and out apart, recurrences included, with the net per month", async () => {
    serve()
    vi.spyOn(finance, "getProjection").mockResolvedValue({data: [
      {month: "2026-11", receivable: 350000, payable: 40000, virtual: 70000, virtual_receivable: 100000, virtual_payable: 30000},
    ], has_more: false})
    renderWithQuery(<OverviewView/>)
    const projection = await screen.findByRole("region", {name: "Projeção"})
    await userEvent.click(await within(projection).findByRole("button", {name: "Entradas e saídas"}))
    const title = await within(projection).findByText(/entradas R\$\s*4\.500,00, saídas R\$\s*700,00, resultado R\$\s*3\.800,00/)
    expect(title).toBeInTheDocument()
    expect(within(projection).getByRole("button", {name: "Entradas e saídas"})).toHaveAttribute("aria-pressed", "true")
  })

  it("shows the month's realised result, by cash, and links to the cash flow", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    const block = await screen.findByRole("region", {name: "Resultado do mês"})
    expect(await within(block).findByText("R$ 3.000,00")).toBeInTheDocument()
    expect(within(block).getByText("R$ 1.800,00")).toBeInTheDocument()
    expect(within(block).getByText("R$ 1.200,00")).toBeInTheDocument()
    expect(within(block).getByRole("link", {name: "Ver relatórios"})).toHaveAttribute("href", "/console/finance/reports?view=cash")
    const month = todayIso().slice(0, 7)
    expect(finance.getCashFlow).toHaveBeenCalledWith(expect.anything(), month, month)
  })

  it("fails one block without blanking the others", async () => {
    serve().mockRejectedValue({response: {status: 500, data: {title: "Erro"}}})
    renderWithQuery(<OverviewView/>)
    const projection = await screen.findByRole("region", {name: "Projeção"})
    expect(await within(projection).findByRole("button", {name: "Tentar novamente"})).toBeInTheDocument()
    expect(await within(screen.getByRole("region", {name: "Saldos"})).findByText("Conta corrente")).toBeInTheDocument()
  })

  it("asks for the chosen window", async () => {
    const projection = serve()
    renderWithQuery(<OverviewView/>)
    await waitFor(() => expect(projection).toHaveBeenCalledWith(expect.anything(), 6))
    await pick("Período", "12 meses")
    await waitFor(() => expect(projection).toHaveBeenCalledWith(expect.anything(), 12))
  })

  it("has no realised-result tile", async () => {
    serve()
    renderWithQuery(<OverviewView/>)
    await screen.findByRole("region", {name: "Saldos"})
    expect(screen.queryByText(/resultado realizado/i)).toBeNull()
  })
})
