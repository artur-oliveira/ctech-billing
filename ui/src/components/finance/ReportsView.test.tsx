import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import {ReportsView} from "@/components/finance/ReportsView"
import * as finance from "@/lib/api/finance"
import type {Account, CashFlow, DRE, Verb} from "@/lib/api/financeTypes"
import {monthRange} from "@/lib/finance/periods"
import {todayIso} from "@/lib/finance/today"

const ALL: Verb[] = ["finance.read"]
const ACCOUNTS: Account[] = [
  {id: "sales", name: "Vendas", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
  {id: "tax", name: "Impostos", class: "expense", dre_group: "deductions", system: false, archived: false, balance: 0},
  {id: "rent", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: true, balance: 0},
  {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: 0},
]
const DRE_DATA: DRE = {
  months: ["2026-01", "2026-02"],
  groups: [
    {group: "gross_revenue", categories: [{category_id: "sales", amounts: [500000, 300000], total: 800000}], amounts: [500000, 300000], total: 800000},
    {group: "deductions", categories: [{category_id: "tax", amounts: [-30000, -20000], total: -50000}], amounts: [-30000, -20000], total: -50000},
    {group: "operating_expenses", categories: [{category_id: "rent", amounts: [-180000, -180000], total: -360000}], amounts: [-180000, -180000], total: -360000},
  ],
  result: [290000, 100000],
  total: 390000,
}
const CASH: CashFlow = {
  from: "2026-01", to: "2026-02", opening_cash: 100000, closing_cash: 340000,
  months: [
    {month: "2026-01", in: 500000, out: 180000, openings: 0, lines: [{category_id: "sales", amount: 500000}, {category_id: "rent", amount: -180000}]},
    {month: "2026-02", in: 0, out: 80000, openings: 0, lines: [{category_id: "", amount: -50000}, {category_id: "card:visa", amount: -30000}]},
  ],
}

function serve() {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  return {
    dre: vi.spyOn(finance, "getDRE").mockResolvedValue(DRE_DATA),
    cash: vi.spyOn(finance, "getCashFlow").mockResolvedValue(CASH),
  }
}


beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F7 — relatórios", () => {
  it("lays the DRE out in the group order, with the derived subtotals", async () => {
    serve()
    renderWithQuery(<ReportsView/>)
    await screen.findByRole("rowheader", {name: "Receita bruta"})
    const heads = screen.getAllByRole("rowheader").map(h => h.textContent)
    expect(heads).toEqual([
      "Receita bruta", "Vendas", "Deduções", "Impostos", "Receita líquida",
      "Despesas operacionais", "Aluguel (arquivada)", "Resultado operacional", "Resultado",
    ])
    const net = screen.getByRole("rowheader", {name: "Receita líquida"}).closest("tr")!
    expect(within(net).getAllByRole("cell").map(c => c.textContent?.replace(/\s/g, " "))).toEqual(["R$ 4.700,00", "R$ 2.800,00", "R$ 7.500,00"])
    const rent = screen.getByRole("rowheader", {name: "Aluguel (arquivada)"}).closest("tr")!
    expect(within(rent).getAllByRole("cell")[0]).toHaveTextContent("−R$ 1.800,00")
  })

  it("keeps the period when switching to the cash flow", async () => {
    const {cash} = serve()
    renderWithQuery(<ReportsView/>)
    await screen.findByRole("rowheader", {name: "Receita bruta"})
    await pick("Período", "Este mês")
    await userEvent.click(screen.getByRole("tab", {name: "Fluxo de caixa"}))
    const {from, to} = monthRange("this_month", todayIso())
    await waitFor(() => expect(cash).toHaveBeenCalledWith(expect.anything(), from, to))
    expect(screen.getByRole("combobox", {name: "Período"})).toHaveTextContent("Este mês")
  })

  it("opens on the cash flow when the link says so, and names what it leaves out", async () => {
    serve()
    renderWithQuery(<ReportsView view="cash"/>)
    expect(screen.getByRole("tab", {name: "Fluxo de caixa"})).toHaveAttribute("aria-selected", "true")
    expect(await screen.findByRole("rowheader", {name: "Sem categoria"})).toBeInTheDocument()
    expect(screen.getByRole("rowheader", {name: "Entradas"})).toBeInTheDocument()
    expect(screen.getByRole("rowheader", {name: "Fatura Visa"})).toBeInTheDocument()
    expect(screen.getByText("Saldo final")).toBeInTheDocument()
    expect(screen.getByText("R$ 3.400,00")).toBeInTheDocument()
    expect(screen.queryByRole("rowheader", {name: "Saldos iniciais lançados"})).not.toBeInTheDocument()
  })

  it("says so when nothing was posted in the period", async () => {
    serve().dre.mockResolvedValue({months: ["2026-01"], groups: [], result: [0], total: 0})
    renderWithQuery(<ReportsView/>)
    expect(await screen.findByText("Nada neste período.")).toBeInTheDocument()
  })

  it("opens the monthly review on the last closed month and loads it only on demand", async () => {
    serve()
    const spend = vi.spyOn(finance, "getCardSpending").mockResolvedValue({basis: "statement", months: [], by_month: [], total: 0, categories: [], cards: []})
    renderWithQuery(<ReportsView/>)
    expect(spend).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole("tab", {name: "Revisão mensal"}))
    expect(await screen.findByRole("heading", {name: "Revisão mensal"})).toBeInTheDocument()
    expect(spend).toHaveBeenCalledWith(expect.anything(), expect.any(String), expect.any(String))
    expect(screen.getByLabelText("Mês revisado")).toHaveAttribute("max", expect.stringMatching(/^\d{4}-\d{2}$/))
    expect(screen.getByLabelText("Mês revisado")).toHaveAttribute("lang", "pt-BR")
  })

  it("keeps report navigation and its filter in stable rows when switching tabs", async () => {
    serve()
    renderWithQuery(<ReportsView/>)
    const tablist = screen.getByRole("tablist", {name: "Relatório"})
    const controls = tablist.parentElement!
    expect(controls.children[0]).toBe(tablist)
    expect(controls.children).toHaveLength(2)

    await userEvent.click(screen.getByRole("tab", {name: "Cartões"}))
    expect(controls.children[0]).toBe(tablist)
    expect(controls.children).toHaveLength(2)
    expect(within(controls.children[1] as HTMLElement).getByRole("combobox", {name: "Período"})).toBeInTheDocument()

    await userEvent.click(screen.getByRole("tab", {name: "Revisão mensal"}))
    expect(controls.children[0]).toBe(tablist)
    expect(controls.children).toHaveLength(2)
    expect(within(controls.children[1] as HTMLElement).getByLabelText("Mês revisado")).toHaveAttribute("lang", "pt-BR")
  })
})

describe("card spending (Gastos com cartão)", () => {
  const CARDS = [
    {id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: 0, archived: false},
    {id: "master", name: "Master", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: 0, archived: true},
  ]
  const SPEND = {
    basis: "statement" as const, months: ["2026-01", "2026-02"], by_month: [40000, 60000], total: 100000,
    categories: [{category_id: "rent", amounts: [0, 75000], total: 75000}, {category_id: "tax", amounts: [40000, -15000], total: 25000}],
    cards: [{card_id: "master", amounts: [0, 75000], total: 75000}, {card_id: "visa", amounts: [40000, -15000], total: 25000}],
  }
  function serveCards() {
    serve()
    vi.spyOn(finance, "listCards").mockResolvedValue({data: CARDS, has_more: false})
    return vi.spyOn(finance, "getCardSpending").mockResolvedValue(SPEND)
  }

  it("is a third report: per category, largest first, with each one's share and the months", async () => {
    const spend = serveCards()
    renderWithQuery(<ReportsView view="cards"/>)
    expect(screen.getByRole("tab", {name: "Cartões"})).toHaveAttribute("aria-selected", "true")
    const rent = (await screen.findByRole("rowheader", {name: "Aluguel (arquivada)"})).closest("tr")!
    expect(within(rent).getAllByRole("cell").map(c => c.textContent?.replace(/\s/g, " "))).toEqual(["R$ 0,00", "R$ 750,00", "R$ 750,00", "75%"])
    const heads = screen.getAllByRole("rowheader").map(h => h.textContent)
    expect(heads.slice(0, 3)).toEqual(["Aluguel (arquivada)", "Impostos", "Total"])
    // By statement month, said in words.
    expect(screen.getByText(/mês da fatura/)).toBeInTheDocument()
    const {from, to} = monthRange("this_year", todayIso())
    expect(spend).toHaveBeenCalledWith(expect.anything(), from, to, undefined)
    // Every card's total too, archived ones included.
    expect(screen.getByRole("rowheader", {name: "Master (arquivado)"})).toBeInTheDocument()
    // A phone reads the ranking first: total and share per category.
    const ranking = document.querySelector("[data-ranking]") as HTMLElement
    expect(within(ranking).getAllByRole("listitem").map(li => li.textContent?.replace(/\s/g, " "))).toEqual(["Aluguel (arquivada)R$ 750,0075%", "ImpostosR$ 250,0025%"])
  })

  it("narrows to one card", async () => {
    const spend = serveCards()
    renderWithQuery(<ReportsView view="cards"/>)
    await screen.findByRole("rowheader", {name: "Impostos"})
    await pick("Cartão", "Visa")
    await waitFor(() => expect(spend).toHaveBeenLastCalledWith(expect.anything(), expect.any(String), expect.any(String), "visa"))
  })

  it("says so when the cards billed nothing", async () => {
    serveCards().mockResolvedValue({...SPEND, by_month: [0, 0], total: 0, categories: [], cards: []})
    renderWithQuery(<ReportsView view="cards"/>)
    expect(await screen.findByText("Nada neste período.")).toBeInTheDocument()
  })
})
