import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {CardsView} from "@/components/finance/CardsView"
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import * as createRequest from "@/lib/finance/createRequest"
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
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listCards").mockResolvedValue({data: cards, has_more: false})
  vi.spyOn(finance, "listPurchases").mockResolvedValue({data: [PURCHASE], has_more: false})
  return vi.spyOn(finance, "getCardStatement").mockImplementation(async (_c, _id, month) =>
    statement(month, month === "2026-03" ? "open" : "future", month === "2026-03" ? 1 : 2))
}

const pickIn = (root: HTMLElement, label: string, option: string) => pick(label, option, root)

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
    expect(await screen.findByText("Nenhum cartão ainda.")).toBeInTheDocument()
    expect(screen.getByRole("button", {name: "Novo cartão"})).toBeInTheDocument()
  })
})

describe("F5 — a card's brand and last four digits (UX batch 3)", () => {
  const BRANDED: Card = {...CARD, brand: "mastercard", last4: "4242"}

  it("shows the card's mark, name and last digits in its header and in the picker", async () => {
    serve(ALL, [BRANDED, {...CARD, id: "elo", name: "Vivo Elo", brand: "elo"}])
    renderWithQuery(<CardsView/>)
    const header = await screen.findByRole("heading", {name: /Visa/})
    expect(within(header).getByText("•••• 4242")).toBeInTheDocument()
    expect(header.closest("[data-card-header]")?.querySelector("svg[data-brand=mastercard]")).not.toBeNull()
    await userEvent.click(screen.getByRole("combobox", {name: "Cartão"}))
    const option = await screen.findByRole("option", {name: /Vivo Elo/})
    expect(option.querySelector("svg[data-brand=elo]")).not.toBeNull()
  })

  it("creates a card with its brand, picked by mark and name, and its last four digits", async () => {
    serve(ALL, [])
    const create = vi.spyOn(finance, "createCard").mockResolvedValue({...BRANDED, id: "n"})
    renderWithQuery(<CardsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Novo cartão"}))
    const form = screen.getByRole("form", {name: "Novo cartão"})
    await userEvent.type(within(form).getByLabelText(/^Nome/), "Nubank")
    await userEvent.click(within(form).getByRole("combobox", {name: "Bandeira"}))
    const options = await screen.findAllByRole("option")
    // By accessible name: the mark is aria-hidden decoration beside the name.
    const names = ["Visa", "Mastercard", "Elo", "American Express", "Hipercard", "Diners Club", "Outra"]
    expect(options).toHaveLength(names.length)
    names.forEach((name, i) => expect(options[i]).toBe(screen.getByRole("option", {name})))
    for (const o of options) expect(o.querySelector("svg[data-brand]")).not.toBeNull()
    await userEvent.click(screen.getByRole("option", {name: "Mastercard"}))
    // Only digits, at most four.
    await userEvent.type(within(form).getByLabelText(/Últimos 4 dígitos/), "42a425")
    expect(within(form).getByLabelText(/Últimos 4 dígitos/)).toHaveValue("4242")
    await pickIn(form, "Fecha no dia", "3")
    await pickIn(form, "Vence no dia", "10")
    await pickIn(form, "Pagar com", "Conta corrente")
    await userEvent.click(within(form).getByRole("button", {name: "Criar cartão"}))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.anything(),
      expect.objectContaining({name: "Nubank", brand: "mastercard", last4: "4242"}), expect.any(String)))
  })

  it("refuses fewer than four digits in words, before calling the server", async () => {
    serve(ALL, [])
    const create = vi.spyOn(finance, "createCard")
    renderWithQuery(<CardsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Novo cartão"}))
    const form = screen.getByRole("form", {name: "Novo cartão"})
    await userEvent.type(within(form).getByLabelText(/^Nome/), "Nubank")
    await userEvent.type(within(form).getByLabelText(/Últimos 4 dígitos/), "42")
    await pickIn(form, "Fecha no dia", "3")
    await pickIn(form, "Vence no dia", "10")
    await pickIn(form, "Pagar com", "Conta corrente")
    await userEvent.click(within(form).getByRole("button", {name: "Criar cartão"}))
    expect(await within(form).findByText("Digite os 4 últimos dígitos, ou deixe em branco.")).toBeInTheDocument()
    expect(create).not.toHaveBeenCalled()
  })

  it("edits the brand and clears the digits", async () => {
    serve(ALL, [BRANDED])
    const patch = vi.spyOn(finance, "patchCard").mockResolvedValue({...BRANDED, brand: "visa", last4: undefined})
    renderWithQuery(<CardsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}))
    const form = screen.getByRole("form", {name: "Editar cartão"})
    expect(within(form).getByRole("combobox", {name: "Bandeira"})).toHaveTextContent("Mastercard")
    expect(within(form).getByLabelText(/Últimos 4 dígitos/)).toHaveValue("4242")
    await pickIn(form, "Bandeira", "Visa")
    await userEvent.clear(within(form).getByLabelText(/Últimos 4 dígitos/))
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}))
    await waitFor(() => expect(patch).toHaveBeenCalledWith(expect.anything(), "visa",
      expect.objectContaining({brand: "visa", last4: ""}), expect.any(String)))
  })
})

describe("F5 — statement status colours (UX batch 3)", () => {
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]})
    vi.setSystemTime(new Date(2026, 2, 20, 9, 0)) // 20 March 2026
  })
  afterEach(() => vi.useRealTimers())

  const badge = async (text: string) => (await screen.findByText(text)).closest("[data-slot=badge], span") as HTMLElement

  it("an open statement is calm green, never a warning", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    const b = await badge("Aberta")
    expect(b).toHaveClass("text-success")
    expect(b).not.toHaveClass("text-warning")
    expect(b.querySelector("svg")).not.toBeNull() // a glyph, never colour alone
  })

  it("a closed statement not yet due is neutral; red only once it is past due unpaid", async () => {
    const get = serve(ALL)
    get.mockImplementation(async (_c, _id, month) => ({
      ...statement(month, "closed", 1), bill_id: "b1",
      due_date: month === "2026-03" ? "2026-03-25" : "2026-03-10",
    }))
    renderWithQuery(<CardsView/>)
    const closed = await badge("Fechada")
    expect(closed).not.toHaveClass("text-danger")
    expect(closed).not.toHaveClass("text-warning")
    await userEvent.click(screen.getByRole("button", {name: "Fatura anterior"}))
    const late = await badge("Vencida")
    expect(late).toHaveClass("text-danger")
  })
})

describe("the phone's central action", () => {
  it("does not open Nova compra for a role that cannot create", async () => {
    serve(["finance.read"])
    createRequest.requestCreate("purchase")
    renderWithQuery(<CardsView/>)
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("opens Nova compra on the card on screen, once the cards are known", async () => {
    serve(ALL)
    createRequest.requestCreate("purchase")
    renderWithQuery(<CardsView/>)
    expect(await screen.findByRole("dialog", {name: "Nova compra"})).toBeInTheDocument()
  })

  it("opens Novo cartão instead when there is no card to buy with", async () => {
    serve(ALL, [])
    createRequest.requestCreate("purchase")
    renderWithQuery(<CardsView/>)
    expect(await screen.findByRole("dialog", {name: "Novo cartão"})).toBeInTheDocument()
  })
})
