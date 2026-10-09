import "@testing-library/jest-dom/vitest"

import {act, screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {RecurrencesView} from "@/components/finance/RecurrencesView"
import {optionsOf, pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import * as createRequest from "@/lib/finance/createRequest"
import type {Account, Recurrence, Verb} from "@/lib/api/financeTypes"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "sal", name: "Salário", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
]
const REC: Recurrence = {
  id: "r1", direction: "payable", amount: 180000, category_id: "alu", account_id: "cc", description: "Aluguel do apartamento",
  expression: {kind: "difference", include: {kind: "day_of_month", day: 10}, exclude: {kind: "months_of_year", months: [12]}},
  start: "2026-01-01", business_day_adjust: "roll_forward", auto_settle: false, archived: false,
}

function serve(verbs: Verb[], recs: Recurrence[] = [REC]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listRecurrences").mockResolvedValue({data: recs, has_more: false})
  return vi.spyOn(finance, "previewRecurrence").mockResolvedValue({
    data: [{nominal: "2026-10-31", due: "2026-11-03"}, {nominal: "2026-11-30", due: "2026-11-30"}], has_more: false,
  })
}

beforeEach(() => window.localStorage.clear())
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe("F4 — recorrências", () => {
  it("says where to create a category when there is none, instead of a silent disabled button", async () => {
    serve(["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"])
    vi.mocked(finance.listAccounts).mockResolvedValue({data: ACCOUNTS.filter(a => a.class === "asset"), has_more: false})
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    expect(await screen.findByText("Sem categoria de despesa. Crie em Contas.")).toBeInTheDocument()
  })

  it("lists each recurrence with its rule in words", async () => {
    serve(ALL)
    renderWithQuery(<RecurrencesView/>)
    expect(await screen.findByText("Aluguel do apartamento")).toBeInTheDocument()
    expect(screen.getByText(/Todo dia 10; exceto em dezembro/)).toBeInTheDocument()
  })

  it("previews the next occurrences while editing, showing the rolled due date", async () => {
    const preview = serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    await waitFor(() => expect(preview).toHaveBeenCalled())
    expect(await screen.findByText(/31\/10\/2026/)).toBeInTheDocument()
    expect(screen.getByText(/paga em 03\/11\/2026/)).toBeInTheDocument()
  })

  it("asks the server once for a burst of edits, and an older answer never overwrites a newer one", async () => {
    const preview = serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1))
    const day = screen.getByLabelText("Dia do mês")
    preview.mockClear()
    await userEvent.clear(day)
    await userEvent.type(day, "15")
    await waitFor(() => expect(preview).toHaveBeenCalled(), {timeout: 2000})
    expect(preview.mock.calls.length).toBeLessThanOrEqual(2)
    const lastBody = preview.mock.calls.at(-1)![1] as {expression: {day?: number; include?: {day: number}}}
    expect(lastBody.expression.day ?? lastBody.expression.include?.day).toBe(15)
  })

  it("shows field errors and does not call the server for an invalid rule", async () => {
    const preview = serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1))
    preview.mockClear()
    const day = screen.getByLabelText("Dia do mês")
    await userEvent.clear(day)
    await userEvent.type(day, "40")
    expect(await screen.findByText("Escolha um dia entre 1 e 31.")).toBeInTheDocument()
    await act(() => new Promise(r => setTimeout(r, 500)))
    expect(preview).not.toHaveBeenCalled()
  })

  it("sends Sunday as weekday 0 for a weekly rule", async () => {
    const preview = serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    await pick("Repetição", "Semanal")
    await pick("Dia da semana", "domingo")
    await waitFor(() => {
      const body = preview.mock.calls.at(-1)![1] as {expression: {kind: string; weekday: number}}
      expect(body.expression).toMatchObject({kind: "weekly", weekday: 0})
    }, {timeout: 2000})
  })

  it("offers only categories of the chosen direction", async () => {
    serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    expect(await optionsOf("Categoria")).toEqual(["Aluguel"])
  })

  it("shows the automatic switch only to a role that may settle", async () => {
    serve(["finance.read", "finance.write"], [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    expect(screen.queryByRole("switch")).toBeNull()
  })

  it("shows an existing rule read-only and says how to change it", async () => {
    serve(ALL)
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    expect(screen.getByText(/encerre e crie outra/i)).toBeInTheDocument()
    expect(screen.queryByLabelText("Dia do mês")).toBeNull()
  })

  it("asks before archiving and says the generated bills stay", async () => {
    serve(ALL)
    const archive = vi.spyOn(finance, "archiveRecurrence").mockResolvedValue(undefined)
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Encerrar"}))
    expect(within(row).getByText(/as já geradas ficam/i)).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}))
    await waitFor(() => expect(archive).toHaveBeenCalledWith(expect.anything(), "r1", expect.any(String)))
  })

  it("gives a viewer the list and no actions", async () => {
    serve(["finance.read"])
    renderWithQuery(<RecurrencesView/>)
    await screen.findByText("Aluguel do apartamento")
    expect(screen.queryByRole("button", {name: "Nova recorrência"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Editar"})).toBeNull()
  })
})

describe("the phone's central action", () => {
  it("opens Nova recorrência", async () => {
    serve(ALL)
    renderWithQuery(<RecurrencesView/>)
    act(() => createRequest.requestCreate("recurrence"))
    expect(await screen.findByRole("dialog", {name: "Nova recorrência"})).toBeInTheDocument()
  })
})
