import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {CardsView} from "@/components/finance/CardsView"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import type {Account, Card, CardStatement, Purchase, Verb} from "@/lib/api/financeTypes"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: -30000},
]
const CARD: Card = {id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: -30000, archived: false}
const PURCHASE: Purchase = {
  id: "p1", description: "Geladeira", category_id: "food", date: "2026-03-01", total: 30000, refunded: false,
  installments: [{number: 1, amount: 10000, month: "2026-03"}, {number: 2, amount: 10000, month: "2026-04"}, {number: 3, amount: 10000, month: "2026-05"}],
}
const statement = (month: string, status: CardStatement["status"], number: number): CardStatement => ({
  card_id: "visa", month, status, closing_date: `${month}-03`, due_date: `${month}-10`, total: 10000,
  items: [{purchase_id: "p1", description: "Geladeira", category_id: "food", date: "2026-03-01", number, of: 3, kind: "installment", amount: 10000}],
})

function serve(verbs: Verb[], cards: Card[] = [CARD]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{kind: "personal", label: "Pessoal", verbs}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listCards").mockResolvedValue({data: cards, has_more: false})
  vi.spyOn(finance, "listPurchases").mockResolvedValue({data: [PURCHASE], has_more: false})
  return vi.spyOn(finance, "getCardStatement").mockImplementation(async (_c, _id, month) =>
    statement(month, month === "2026-03" ? "open" : "future", month === "2026-03" ? 1 : 2))
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F5 — cartões", () => {
  it("opens on the card's open statement, in words, with its total", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    expect(await screen.findByText("Aberta")).toBeInTheDocument()
    expect(screen.getByText("Mar/26")).toBeInTheDocument()
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement
    expect(within(row).getByText(/1\/3/)).toBeInTheDocument()
  })

  it("pages to the next statement, which is still to come", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    await screen.findByText("Aberta")
    await userEvent.click(screen.getByRole("button", {name: "Próxima fatura"}))
    expect(await screen.findByText("Futura")).toBeInTheDocument()
    expect(await screen.findByText(/2\/3/)).toBeInTheDocument()
  })

  it("refunds a purchase after confirming", async () => {
    serve(ALL)
    const refund = vi.spyOn(finance, "refundPurchase").mockResolvedValue({...PURCHASE, refunded: true})
    renderWithQuery(<CardsView/>)
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Estornar compra"}))
    expect(within(row).getByText(/As parcelas já cobradas voltam como crédito/)).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}))
    await waitFor(() => expect(refund).toHaveBeenCalledWith(expect.anything(), "visa", "p1", expect.any(String)))
  })

  it("hides every action from a viewer", async () => {
    serve(["finance.read"])
    renderWithQuery(<CardsView/>)
    await screen.findByText("Geladeira")
    for (const name of ["Nova compra", "Estornar compra", "Antecipar parcelas", "Fechar fatura agora", "Novo cartão"]) {
      expect(screen.queryByRole("button", {name})).not.toBeInTheDocument()
    }
  })

  it("teaches the first step when there is no card", async () => {
    serve(ALL, [])
    renderWithQuery(<CardsView/>)
    expect(await screen.findByText("Nenhum cartão ainda")).toBeInTheDocument()
    expect(screen.getByRole("button", {name: "Novo cartão"})).toBeInTheDocument()
  })
})
