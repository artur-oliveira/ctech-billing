import "@testing-library/jest-dom/vitest"

import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {ImportView} from "@/components/finance/ImportView"
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import type {Account, Bill, ImportDetail, ImportSummary, Verb} from "@/lib/api/financeTypes"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "old", name: "Conta antiga", class: "asset", system: false, archived: true, balance: 0},
  {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: 0},
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "sales", name: "Vendas", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
]
const RENT: Bill = {
  id: "rent", direction: "payable", amount: 100000, account_id: "cc", category_id: "food", description: "Aluguel",
  competence_date: "2026-03-10", due_date: "2026-03-10", status: "forecast", origin: "manual", auto_settle: false,
}
const SUMMARY: ImportSummary = {
  id: "imp1", account_id: "cc", format: "ofx", created_at: "2026-03-12T10:00:00Z", from: "2026-03-09", to: "2026-03-11",
  lines: 3, duplicates: 0, rejected_count: 0, rejected: [], pending: 2,
}
const DETAIL: ImportDetail = {
  import: SUMMARY,
  lines: [
    {n: 1, date: "2026-03-09", amount: -100000, description: "Pagamento aluguel", status: "pending", candidates: [RENT]},
    {n: 2, date: "2026-03-10", amount: -500, description: "Padaria", status: "pending", candidates: []},
    {n: 3, date: "2026-03-11", amount: -2000, description: "Transferência entre contas", status: "ignored", candidates: []},
  ],
}

function serve(verbs: Verb[], accounts: Account[] = ACCOUNTS, imports: ImportSummary[] = [SUMMARY]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{kind: "personal", label: "Pessoal", verbs}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: accounts, has_more: false})
  vi.spyOn(finance, "listImports").mockResolvedValue({data: imports, has_more: false})
  vi.spyOn(finance, "getImport").mockResolvedValue(DETAIL)
}

const ofxFile = () => new File(["<OFX></OFX>"], "extrato.ofx", {type: "application/x-ofx"})

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F6 — importar extrato", () => {
  it("imports into a bank or cash account only", async () => {
    serve(ALL)
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    await userEvent.click(screen.getByRole("combobox", {name: "Conta"}))
    const options = (await screen.findAllByRole("option")).map(o => o.textContent?.trim())
    expect(options).toEqual(["Conta corrente"])
  })

  it("teaches the first step when there is no account to import into", async () => {
    serve(ALL, ACCOUNTS.filter(a => a.class !== "asset"))
    renderWithQuery(<ImportView/>)
    expect(await screen.findByText("Nenhuma conta para importar.")).toBeInTheDocument()
  })

  it("uploads the file as base64 and says what it added", async () => {
    serve(ALL)
    const upload = vi.spyOn(finance, "uploadImport").mockResolvedValue({...SUMMARY, id: "imp2", lines: 12, duplicates: 3, rejected_count: 1})
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    await userEvent.upload(screen.getByLabelText("Arquivo do extrato"), ofxFile())
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    await waitFor(() => expect(upload).toHaveBeenCalledWith(expect.anything(),
      {account_id: "cc", format: "ofx", content: btoa("<OFX></OFX>")}, expect.any(String)))
    expect(await screen.findByRole("status")).toHaveTextContent("12 lançamentos novos, 3 já importados antes, 1 linha não lida")
  })

  it("keeps the key to retry the same file, and takes a new one for another file", async () => {
    serve(ALL)
    const upload = vi.spyOn(finance, "uploadImport")
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockResolvedValue({...SUMMARY, id: "imp2"})
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    const input = screen.getByLabelText("Arquivo do extrato")
    await userEvent.upload(input, ofxFile())
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1))
    await screen.findByRole("alert")
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(2))
    await userEvent.upload(input, new File(["<OFX>B</OFX>"], "outro.ofx"))
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(3))
    const keys = upload.mock.calls.map(c => c[2])
    expect(keys[1]).toBe(keys[0]) // a retry of the same file is the same intent
    expect(keys[2]).not.toBe(keys[0]) // another file is another intent
  })

  it("says so when the file held nothing new", async () => {
    serve(ALL)
    vi.spyOn(finance, "uploadImport").mockResolvedValue({...SUMMARY, id: undefined, lines: 0, duplicates: 3})
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    await userEvent.upload(screen.getByLabelText("Arquivo do extrato"), ofxFile())
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    expect(await screen.findByRole("status")).toHaveTextContent("Nada novo: este extrato já foi importado.")
  })

  it("settles the offered bill with the line", async () => {
    serve(ALL)
    const match = vi.spyOn(finance, "matchLine").mockResolvedValue({line: {...DETAIL.lines[0], status: "matched", bill_id: "rent"}})
    renderWithQuery(<ImportView/>)
    const row = (await screen.findByText("Pagamento aluguel")).closest("li") as HTMLElement
    expect(within(row).getByText(/Aluguel/)).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Dar baixa"}))
    await waitFor(() => expect(match).toHaveBeenCalledWith(expect.anything(), "imp1", 1, {bill_id: "rent"}, expect.any(String)))
  })

  it("turns a line nobody forecast into a paid bill under an expense category", async () => {
    serve(ALL)
    const made = vi.spyOn(finance, "newFromLine").mockResolvedValue({line: {...DETAIL.lines[1], status: "created"}})
    renderWithQuery(<ImportView/>)
    const row = (await screen.findByText("Padaria")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Nova conta"}))
    await userEvent.click(within(row).getByRole("combobox", {name: "Categoria"}))
    const options = (await screen.findAllByRole("option")).map(o => o.textContent?.trim())
    expect(options).toEqual(["Mercado"]) // money out: expense categories only
    await userEvent.keyboard("{Escape}")
    await pick("Categoria", "Mercado", row)
    await userEvent.click(within(row).getByRole("button", {name: "Criar e dar baixa"}))
    await waitFor(() => expect(made).toHaveBeenCalledWith(expect.anything(), "imp1", 2, {category_id: "food", description: "Padaria"}, expect.any(String)))
  })

  it("ignores a line, and an ignored line can be brought back", async () => {
    serve(ALL)
    const ignore = vi.spyOn(finance, "ignoreLine").mockResolvedValue({line: {...DETAIL.lines[1], status: "ignored"}})
    const reopen = vi.spyOn(finance, "reopenLine").mockResolvedValue({line: {...DETAIL.lines[2], status: "pending"}})
    renderWithQuery(<ImportView/>)
    const row = (await screen.findByText("Padaria")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: "Ignorar"}))
    await waitFor(() => expect(ignore).toHaveBeenCalledWith(expect.anything(), "imp1", 2, expect.any(String)))
    await userEvent.click(screen.getByRole("tab", {name: /Ignoradas/}))
    const ignored = (await screen.findByText("Transferência entre contas")).closest("li") as HTMLElement
    await userEvent.click(within(ignored).getByRole("button", {name: "Desfazer"}))
    await waitFor(() => expect(reopen).toHaveBeenCalledWith(expect.anything(), "imp1", 3, expect.any(String)))
  })

  it("asks for the CSV columns when the account has none saved", async () => {
    serve(ALL)
    vi.spyOn(finance, "uploadImport").mockRejectedValue({response: {status: 422, data: {type: "about:blank", title: "U", status: 422, code: "csv_mapping_required"}}})
    vi.spyOn(finance, "getCsvMapping").mockRejectedValue({response: {status: 404, data: {type: "about:blank", title: "N", status: 404, code: "resource_not_found"}}})
    const save = vi.spyOn(finance, "putCsvMapping").mockImplementation(async (_c, _a, m) => m)
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    await userEvent.upload(screen.getByLabelText("Arquivo do extrato"), new File(["a;b;c"], "extrato.csv", {type: "text/csv"}))
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    const form = await screen.findByRole("form", {name: "Colunas do CSV"})
    await userEvent.click(within(form).getByRole("button", {name: "Salvar colunas"}))
    await waitFor(() => expect(save).toHaveBeenCalledWith(expect.anything(), "cc", expect.objectContaining({delimiter: ";", date_column: 1}), expect.any(String)))
  })

  it("shows a viewer the lines and no action", async () => {
    serve(["finance.read"])
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    expect(screen.queryByLabelText("Arquivo do extrato")).not.toBeInTheDocument()
    for (const name of ["Dar baixa", "Nova conta", "Ignorar", "Importar"]) {
      expect(screen.queryByRole("button", {name})).not.toBeInTheDocument()
    }
  })
})
