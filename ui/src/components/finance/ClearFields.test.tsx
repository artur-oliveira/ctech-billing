import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {AccountsView} from "@/components/finance/AccountsView"
import {BillsView} from "@/components/finance/BillsView"
import {CardsView} from "@/components/finance/CardsView"
import {RecurrencesView} from "@/components/finance/RecurrencesView"
import {lastWrite, pick, renderWithQuery, selectByLabel, serveFinanceMock} from "@/components/finance/finance.test-utils"

// UX batch 4: every optional field of an edit form can be emptied, and the
// emptied field reaches the API as null (the API's clear). Each case runs the
// whole stack against the dev mock: fill it, empty it, read what was sent,
// then what the screen shows once the data is read again.

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

const monthName = new Intl.DateTimeFormat("pt-BR", {month: "long"}).format(new Date())

describe("clearing an optional field after saving it", () => {
  it("removes a recurrence's end date: Limpar, then Salvar sends end null, and the rule has no end again", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<RecurrencesView/>)
    // Create it with an end.
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    const dialog = await screen.findByRole("dialog")
    await userEvent.type(within(dialog).getByLabelText(/^Descrição/), "Academia")
    await userEvent.type(within(dialog).getByLabelText(/^Valor/), "9990")
    await pick(/^Categoria/, "Aluguel", dialog)
    await pick(/^Pagar com/, "Conta corrente", dialog)
    await userEvent.click(within(dialog).getByRole("button", {name: "Mais opções"}))
    await userEvent.click(within(dialog).getByLabelText("Termina em"))
    await userEvent.click(await screen.findByRole("button", {name: new RegExp(`, 28 de ${monthName} de `)}))
    await userEvent.click(within(dialog).getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(lastWrite(sent, "post", "/recurrences").end).toMatch(/-28$/)

    // Edit it: empty the end.
    const row = (await screen.findByText("Academia")).closest("li")!
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const edit = await screen.findByRole("dialog")
    await userEvent.click(within(edit).getByRole("button", {name: "Limpar data de término"}))
    expect(within(edit).getByLabelText("Termina em")).toHaveTextContent("Não termina")
    await userEvent.click(within(edit).getByRole("button", {name: "Salvar"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(lastWrite(sent, "patch", "")).toEqual({end: null})

    // Read again: no end, and nothing left to clear.
    await userEvent.click(within((await screen.findByText("Academia")).closest("li")!).getByRole("button", {name: "Editar"}))
    const again = await screen.findByRole("dialog")
    expect(within(again).getByLabelText("Termina em")).toHaveTextContent("Não termina")
    expect(within(again).queryByRole("button", {name: "Limpar data de término"})).not.toBeInTheDocument()
  })

  it("empties a bill's description: Salvar sends description null, and the row says it has none", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<BillsView/>)
    const row = (await screen.findByText("Compra do mês")).closest("li")!
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const input = within(row).getByLabelText("Descrição")
    await userEvent.clear(input)
    await userEvent.click(within(row).getByRole("button", {name: "Salvar"}))
    await waitFor(() => expect(lastWrite(sent, "patch", "/bills/b-mercado")).toEqual({description: null}))
    expect(await screen.findByText("Sem descrição")).toBeInTheDocument()
    expect(screen.queryByText("Compra do mês")).not.toBeInTheDocument()
  })

  it("removes a card's brand with Nenhuma: Salvar sends brand null, and the card has none", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<CardsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}))
    const dialog = await screen.findByRole("dialog")
    expect(selectByLabel("Bandeira", dialog)).toHaveTextContent("Mastercard")
    await pick("Bandeira", "Nenhuma", dialog)
    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(lastWrite(sent, "patch", "/cards/card-nubank").brand).toBeNull()
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}))
    expect(selectByLabel("Bandeira", await screen.findByRole("dialog"))).toHaveTextContent("Nenhuma")
  })

  it("removes the default receiving account with Nenhuma: the PUT sends null, and the setting reads Nenhuma", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<AccountsView/>)
    await pick(await screen.findByRole("combobox", {name: "Conta padrão de recebimento"}).then(() => "Conta padrão de recebimento"), "Poupança")
    await waitFor(() => expect(selectByLabel("Conta padrão de recebimento")).toHaveTextContent("Poupança"))
    await pick("Conta padrão de recebimento", "Nenhuma")
    await waitFor(() => expect(lastWrite(sent, "put", "/settings/default-receiving-account")).toEqual({default_receiving_account_id: null}))
    await waitFor(() => expect(selectByLabel("Conta padrão de recebimento")).toHaveTextContent("Nenhuma"))
  })
})
