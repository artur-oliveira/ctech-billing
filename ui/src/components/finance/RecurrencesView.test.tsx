import "@testing-library/jest-dom/vitest"

import {act, screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {RecurrencesView} from "@/components/finance/RecurrencesView"
import {optionsOf, pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import * as createRequest from "@/lib/finance/createRequest"
import type {Account, Recurrence, RecurrenceOccurrences, Verb} from "@/lib/api/financeTypes"

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

  // Review fix (UX batch 4): re-opening a rule whose end has passed resumes it
  // from the current month; the editor says so before saving.
  it("says a re-opened rule continues from this month when a past end is cleared or moved later", async () => {
    serve(ALL, [{...REC, end: "2025-01-31"}])
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).queryByText(/continua a partir deste mês/)).toBeNull()
    await userEvent.click(within(dialog).getByRole("button", {name: "Limpar data de término"}))
    expect(within(dialog).getByText(/continua a partir deste mês/)).toBeInTheDocument()
  })

  it("says nothing about re-opening when the end is still ahead", async () => {
    serve(ALL, [{...REC, end: "2099-12-10"}])
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const dialog = await screen.findByRole("dialog")
    await userEvent.click(within(dialog).getByRole("button", {name: "Limpar data de término"}))
    expect(within(dialog).queryByText(/continua a partir deste mês/)).toBeNull()
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
  it("does not open Nova recorrência for a role that cannot create", async () => {
    serve(["finance.read"])
    createRequest.requestCreate("recurrence")
    renderWithQuery(<RecurrencesView/>)
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("opens Nova recorrência", async () => {
    serve(ALL)
    renderWithQuery(<RecurrencesView/>)
    act(() => createRequest.requestCreate("recurrence"))
    expect(await screen.findByRole("dialog", {name: "Nova recorrência"})).toBeInTheDocument()
  })
})

// ---- UX batch 3 -----------------------------------------------------------------

const OCCURRENCES: RecurrenceOccurrences = {
  history: [
    {nominal: "2026-07-10", due: "2026-07-10", bill_id: "b7", amount: 180000, state: "paid", paid_date: "2026-07-10"},
    {nominal: "2026-08-10", due: "2026-08-10", bill_id: "b8", amount: 180000, state: "skipped"},
    {nominal: "2026-09-10", due: "2026-09-10", bill_id: "b9", amount: 180000, state: "overdue"},
    {nominal: "2026-10-10", due: "2026-10-13", bill_id: "b10", amount: 180000, state: "forecast"},
  ],
  upcoming: [{nominal: "2026-11-10", due: "2026-11-10"}, {nominal: "2026-12-10", due: "2026-12-10"}],
}

describe("F4 — a recurrence's detail, in place (UX batch 3)", () => {
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]})
    vi.setSystemTime(new Date(2026, 9, 9, 9, 0)) // 9 October 2026
  })

  it("expands in place under a disclosure button that keeps the focus", async () => {
    serve(["finance.read"]) // a viewer reads the detail too
    const get = vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue(OCCURRENCES)
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    const ver = within(row).getByRole("button", {name: "Ver"})
    expect(ver).toHaveAttribute("aria-expanded", "false")
    await userEvent.click(ver)
    expect(ver).toHaveAttribute("aria-expanded", "true")
    expect(ver).toHaveFocus()
    const region = await screen.findByRole("region", {name: "Ocorrências de Aluguel do apartamento"})
    expect(ver).toHaveAttribute("aria-controls", region.id)
    await waitFor(() => expect(get).toHaveBeenCalledWith(expect.anything(), "r1"))
    await userEvent.click(ver)
    expect(ver).toHaveAttribute("aria-expanded", "false")
    expect(screen.queryByRole("region", {name: /Ocorrências de/})).toBeNull()
  })

  it("shows the next dates and the history as a timeline, each with its state in words", async () => {
    serve(ALL)
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue(OCCURRENCES)
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Ver"}))
    const region = await screen.findByRole("region", {name: /Ocorrências de/})

    const next = await within(region).findByRole("list", {name: "Próximas"})
    const nextItems = within(next).getAllByRole("listitem")
    // The bill already made for 10/10 (rolled to 13/10) leads, then what the rule will make.
    expect(nextItems.map(i => i.textContent)).toEqual([
      expect.stringMatching(/10\/10\/2026.*Prevista.*paga em 13\/10\/2026/),
      expect.stringMatching(/10\/11\/2026.*A gerar/),
      expect.stringMatching(/10\/12\/2026.*A gerar/),
    ])

    const past = within(region).getByRole("list", {name: "Histórico"})
    const pastItems = within(past).getAllByRole("listitem")
    expect(pastItems.map(i => i.textContent)).toEqual([ // most recent first
      expect.stringMatching(/10\/09\/2026.*Vencida/),
      expect.stringMatching(/10\/08\/2026.*Pulada/),
      expect.stringMatching(/10\/07\/2026.*Paga em 10\/07\/2026/),
    ])
    expect(within(pastItems[0]).getByRole("link", {name: "Ver em A pagar"}))
      .toHaveAttribute("href", "/console/finance/bills?direction=payable&bill=b9")
  })

  it("says when there is nothing to come", async () => {
    serve(ALL)
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({history: [], upcoming: []})
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Ver"}))
    expect(await screen.findByText("Nenhuma data por vir.")).toBeInTheDocument()
  })

  it("previews the next dates as a compact list with the weekday and the business-day roll", async () => {
    serve(ALL, [])
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    const list = await screen.findByRole("list", {name: "Próximas datas"})
    const items = await within(list).findAllByRole("listitem")
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent(/31\/10\/2026/)
    expect(items[0]).toHaveTextContent(/sáb/)
    expect(items[0]).toHaveTextContent(/paga em 03\/11\/2026.*próximo dia útil/)
    expect(items[1]).not.toHaveTextContent(/paga em/)
  })

  it("previews the dates left while editing, and warns when the end leaves none", async () => {
    const preview = serve(ALL)
    preview.mockResolvedValue({data: [], has_more: false})
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    expect(await screen.findByText(/Nenhuma data depois de hoje; salvar encerra a recorrência/)).toBeInTheDocument()
    await waitFor(() => expect(preview).toHaveBeenCalledWith(expect.anything(),
      expect.objectContaining({expression: REC.expression, start: REC.start}), expect.anything()))
  })

  it("asks before an end that leaves nothing to come, and archives only on confirmation", async () => {
    serve(ALL)
    const patch = vi.spyOn(finance, "patchRecurrence")
      .mockRejectedValueOnce({response: {status: 422, data: {code: "recurrence_would_end", detail: "x"}}})
      .mockResolvedValueOnce({...REC, end: "2026-10-09", archived: true})
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const dialog = screen.getByRole("dialog", {name: "Editar recorrência"})
    await userEvent.click(within(dialog).getByLabelText("Termina em"))
    await userEvent.click(await screen.findByRole("button", {name: /sexta-feira, 9 de outubro de 2026$/}))
    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    expect(await within(dialog).findByText("Isso encerra a recorrência; ela será arquivada.")).toBeInTheDocument()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch.mock.calls[0][2]).toEqual({end: "2026-10-09"})

    // Back: nothing else is sent.
    await userEvent.click(within(dialog).getByRole("button", {name: "Voltar"}))
    expect(within(dialog).queryByText(/ela será arquivada/)).toBeNull()
    expect(patch).toHaveBeenCalledTimes(1)

    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    // The confirmation appears again from the same refusal (no second request needed).
    await userEvent.click(await within(dialog).findByRole("button", {name: "Encerrar e arquivar"}))
    await waitFor(() => expect(patch).toHaveBeenLastCalledWith(expect.anything(), "r1", {end: "2026-10-09", archive: true, cancel_after_end: true}, expect.any(String)))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })
})

// UX batch 5 (the owner's decision): ending a recurrence CANCELS the unpaid
// bills it already made after the new end; a paid one stays. The confirmation
// says which, before anything is sent.
describe("F4 — ending a recurrence says which bills it cancels", () => {
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]})
    vi.setSystemTime(new Date(2026, 9, 9, 9, 0)) // 9 October 2026
  })

  async function confirmEndToday(history: RecurrenceOccurrences["history"]) {
    serve(ALL)
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({history, upcoming: []})
    vi.spyOn(finance, "patchRecurrence").mockRejectedValue({response: {status: 422, data: {code: "recurrence_would_end", detail: "x"}}})
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const dialog = screen.getByRole("dialog", {name: "Editar recorrência"})
    await userEvent.click(within(dialog).getByLabelText("Termina em"))
    await userEvent.click(await screen.findByRole("button", {name: /sexta-feira, 9 de outubro de 2026$/}))
    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    await within(dialog).findByText("Isso encerra a recorrência; ela será arquivada.")
    return dialog
  }

  const made = (nominal: string, state: "paid" | "forecast" | "overdue" | "skipped", auto = false) =>
    ({nominal, due: nominal, bill_id: `b-${nominal}`, amount: 180000, state, auto_settle: auto})

  it("names the forecast bills after the new end that will be cancelled", async () => {
    const dialog = await confirmEndToday([made("2026-09-10", "paid"), made("2026-10-10", "forecast", true), made("2026-11-10", "forecast")])
    expect(await within(dialog).findByText("Os 2 lançamentos em aberto depois do fim (10/10 e 10/11) serão cancelados.")).toBeInTheDocument()
    // Cancelled, so auto-pay no longer pays them: the old promise is gone.
    expect(within(dialog).queryByText(/pagamento automático/)).toBeNull()
  })

  it("says it in the singular for one", async () => {
    const dialog = await confirmEndToday([made("2026-10-10", "forecast")])
    expect(await within(dialog).findByText("O lançamento em aberto depois do fim (10/10) será cancelado.")).toBeInTheDocument()
  })

  it("says that a paid one after the end stays, and leaves a skipped one out", async () => {
    const dialog = await confirmEndToday([made("2026-10-10", "skipped"), made("2026-11-10", "forecast"), made("2026-12-10", "paid")])
    expect(await within(dialog).findByText("O lançamento em aberto depois do fim (10/11) será cancelado.")).toBeInTheDocument()
    expect(within(dialog).getByText("O já pago (10/12) continua: o pagamento está no extrato.")).toBeInTheDocument()
  })

  it("adds nothing when no bill was made after the new end", async () => {
    const dialog = await confirmEndToday([made("2026-09-10", "paid")])
    await act(() => new Promise(r => setTimeout(r, 50)))
    expect(within(dialog).queryByText(/cancelad|já pago/)).toBeNull()
  })
})

// UX batch 5 review (I2, I3): ANY end with open bills after it cancels them,
// asked first; a cancellation that did not finish says so and can be retried.
describe("F4 — an end that cancels bills, and one that did not finish", () => {
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]})
    vi.setSystemTime(new Date(2026, 9, 9, 9, 0)) // 9 October 2026
  })

  const made = (nominal: string, state: "paid" | "forecast" | "overdue") =>
    ({nominal, due: nominal, bill_id: `b-${nominal}`, amount: 180000, state, auto_settle: false})

  async function saveEnd(patch: ReturnType<typeof vi.spyOn>) {
    void patch
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Aluguel do apartamento")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Editar"}))
    const dialog = screen.getByRole("dialog", {name: "Editar recorrência"})
    await userEvent.click(within(dialog).getByLabelText("Termina em"))
    await userEvent.click(await screen.findByRole("button", {name: /sexta-feira, 9 de outubro de 2026$/}))
    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    return dialog
  }

  it("asks before an earlier end that cancels open bills while the rule keeps going, and sends the confirmation", async () => {
    serve(ALL) // the preview still has dates: the rule does not end
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({history: [made("2026-10-10", "forecast"), made("2026-11-10", "forecast")], upcoming: []})
    const patch = vi.spyOn(finance, "patchRecurrence")
      .mockRejectedValueOnce({response: {status: 422, data: {code: "end_cancels_bills", detail: "x"}}})
      .mockResolvedValueOnce({...REC, end: "2026-10-09"})
    const dialog = await saveEnd(patch)
    expect(await within(dialog).findByText("Os 2 lançamentos em aberto depois do fim (10/10 e 10/11) serão cancelados.")).toBeInTheDocument()
    expect(within(dialog).queryByText(/ela será arquivada/)).toBeNull()
    expect(patch).toHaveBeenCalledTimes(1)

    await userEvent.click(within(dialog).getByRole("button", {name: "Voltar"}))
    expect(patch).toHaveBeenCalledTimes(1)
    await userEvent.click(within(dialog).getByRole("button", {name: "Salvar"}))
    await userEvent.click(await within(dialog).findByRole("button", {name: "Salvar e cancelar"}))
    await waitFor(() => expect(patch).toHaveBeenLastCalledWith(expect.anything(), "r1", {end: "2026-10-09", cancel_after_end: true}, expect.any(String)))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })

  it("says the end was saved but bills stayed open, keeps the panel, and the same request finishes it", async () => {
    serve(ALL)
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({history: [made("2026-10-10", "forecast")], upcoming: []})
    const patch = vi.spyOn(finance, "patchRecurrence")
      .mockRejectedValueOnce({response: {status: 422, data: {code: "recurrence_would_end", detail: "x"}}})
      .mockRejectedValueOnce({response: {status: 409, data: {code: "recurrence_end_incomplete", detail: "x"}}})
      .mockResolvedValueOnce({...REC, end: "2026-10-09", archived: true})
    const dialog = await saveEnd(patch)
    await userEvent.click(await within(dialog).findByRole("button", {name: "Encerrar e arquivar"}))
    expect(await within(dialog).findByText("A recorrência foi encerrada, mas alguns lançamentos não foram cancelados — tente de novo.")).toBeInTheDocument()
    expect(screen.getByRole("dialog", {name: "Editar recorrência"})).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole("button", {name: "Tentar de novo"}))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(3))
    expect(patch.mock.calls[2][2]).toEqual(patch.mock.calls[1][2])
    expect(patch.mock.calls[2][2]).toEqual({end: "2026-10-09", archive: true, cancel_after_end: true})
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })
})
