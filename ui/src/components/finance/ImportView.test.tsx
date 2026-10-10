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
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
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
    // "Conta" is a bank account in Finanças; creating a bill from a line is Adicionar.
    expect(within(row).queryByRole("button", {name: "Nova conta"})).toBeNull()
    await userEvent.click(within(row).getByRole("button", {name: "Adicionar"}))
    expect(within(row).getByRole("form", {name: "Adicionar"})).toBeInTheDocument()
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

  it("says, on the pending tab only, how long pending lines are kept", async () => {
    serve(ALL)
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    const note = "Linhas pendentes ficam disponíveis por 90 dias após a importação."
    expect(screen.getByText(note)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("tab", {name: /Ignoradas/}))
    await screen.findByText("Transferência entre contas")
    expect(screen.queryByText(note)).not.toBeInTheDocument()
  })

  it("offers a bill auto-settle already paid as such, and links it instead of settling", async () => {
    const paid: Bill = {...RENT, id: "auto", description: "Internet", status: "paid", paid_date: "2026-03-10", auto_settle: true}
    serve(ALL)
    vi.spyOn(finance, "getImport").mockResolvedValue({...DETAIL, lines: [{...DETAIL.lines[0], candidates: [paid]}, ...DETAIL.lines.slice(1)]})
    const link = vi.spyOn(finance, "linkLine").mockResolvedValue({line: {...DETAIL.lines[0], status: "linked", bill_id: "auto"}})
    const match = vi.spyOn(finance, "matchLine")
    renderWithQuery(<ImportView/>)
    const row = (await screen.findByText("Pagamento aluguel")).closest("li") as HTMLElement
    expect(within(row).getByText(/Internet, já pago automaticamente em 10\/03/)).toBeInTheDocument()
    expect(within(row).queryByRole("button", {name: "Dar baixa"})).not.toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Vincular"}))
    await waitFor(() => expect(link).toHaveBeenCalledWith(expect.anything(), "imp1", 1, {bill_id: "auto"}, expect.any(String)))
    expect(match).not.toHaveBeenCalled()
  })

  it("shows a linked line as reconciled", async () => {
    serve(ALL)
    vi.spyOn(finance, "getImport").mockResolvedValue({...DETAIL, lines: [{...DETAIL.lines[0], status: "linked", bill_id: "auto", candidates: []}]})
    renderWithQuery(<ImportView/>)
    await userEvent.click(await screen.findByRole("tab", {name: /Conciliadas/}))
    const row = (await screen.findByText("Pagamento aluguel")).closest("li") as HTMLElement
    expect(within(row).getByText("Vinculada ao pagamento automático")).toBeInTheDocument()
  })

  it("shows a viewer the lines and no action", async () => {
    serve(["finance.read"])
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    expect(screen.queryByLabelText("Arquivo do extrato")).not.toBeInTheDocument()
    for (const name of ["Dar baixa", "Vincular", "Adicionar", "Ignorar", "Importar"]) {
      expect(screen.queryByRole("button", {name})).not.toBeInTheDocument()
    }
  })
})

describe("F6 — a pending line's last days", () => {
  // Only Date is faked: the query client and user events keep real timers.
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]})
    vi.setSystemTime(new Date(2026, 5, 1, 9, 0)) // 1 June 2026, 09:00 local
  })
  afterEach(() => vi.useRealTimers())

  // A line that expires at noon (local) on the given June day.
  const expiring = (n: number, description: string, june: number): ImportDetail["lines"][number] =>
    ({n, date: "2026-03-09", amount: -500, description, status: "pending", candidates: [], expires_at: new Date(2026, 5, june, 12).toISOString()})

  it("warns in the last 15 days only, and reads right on the last day", async () => {
    serve(ALL)
    vi.spyOn(finance, "getImport").mockResolvedValue({...DETAIL, lines: [
      expiring(1, "Sobra dezesseis", 17), // 16 days left: no badge
      expiring(2, "Sobra quinze", 16), // 15 days left: badge
      expiring(3, "Sobra um", 2), // tomorrow
      expiring(4, "Sobra hoje", 1), // today, later on
    ]})
    renderWithQuery(<ImportView/>)
    const row = async (text: string) => (await screen.findByText(text)).closest("li") as HTMLElement
    expect(within(await row("Sobra dezesseis")).queryByText(/expira/)).not.toBeInTheDocument()
    expect(within(await row("Sobra quinze")).getByText("expira em 15 dias")).toBeInTheDocument()
    expect(within(await row("Sobra um")).getByText("expira em 1 dia")).toBeInTheDocument()
    expect(within(await row("Sobra hoje")).getByText("expira hoje")).toBeInTheDocument()
  })

  it("does not warn on a line already reconciled", async () => {
    serve(ALL)
    vi.spyOn(finance, "getImport").mockResolvedValue({...DETAIL, lines: [{...expiring(1, "Feita", 2), status: "matched", bill_id: "rent"}]})
    renderWithQuery(<ImportView/>)
    await userEvent.click(await screen.findByRole("tab", {name: /Conciliadas/}))
    expect(within(await screen.findByText("Feita").then(e => e.closest("li") as HTMLElement)).queryByText(/expira/)).not.toBeInTheDocument()
  })
})

// UX batch 5: an OFX into an account with no opening balance and no entries
// offers the statement's balance as the opening one. Invented values.
describe("F6 — the statement's balance as the opening balance", () => {
  const PROPOSAL = {amount: 274950, date: "2026-02-01", ledger_balance: 500000, ledger_as_of: "2026-02-28", lines: 3}

  async function uploadWith(verbs: Verb[], result: Partial<ImportSummary>) {
    serve(verbs)
    vi.spyOn(finance, "uploadImport").mockResolvedValue({...SUMMARY, id: "imp2", ...result})
    renderWithQuery(<ImportView/>)
    await screen.findByText("Pagamento aluguel")
    await userEvent.upload(screen.getByLabelText("Arquivo do extrato"), ofxFile())
    await userEvent.click(screen.getByRole("button", {name: "Importar"}))
    await screen.findByRole("status")
  }

  it("asks, shows the arithmetic and says what an ignored line does", async () => {
    await uploadWith(ALL, {opening_proposal: PROPOSAL})
    const offer = await screen.findByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})
    expect(offer).toHaveTextContent("Saldo inicial de R$ 2.749,50 em 01/02/2026: o saldo do extrato (R$ 5.000,00 em 28/02/2026) menos os 3 lançamentos do arquivo.")
    expect(offer).toHaveTextContent("Uma linha que você ignorar não entra na conta: o saldo da conta fica diferente do extrato nesse valor.")
  })

  it("posts only on confirmation, to the import's own route", async () => {
    await uploadWith(ALL, {opening_proposal: PROPOSAL})
    const post = vi.spyOn(finance, "postImportOpening").mockResolvedValue({transaction_id: "tx1"})
    const offer = await screen.findByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})
    expect(post).not.toHaveBeenCalled()
    await userEvent.click(within(offer).getByRole("button", {name: "Usar como saldo inicial"}))
    await waitFor(() => expect(post).toHaveBeenCalledWith(expect.anything(), "imp2", expect.any(String)))
    expect(await screen.findByText("Saldo inicial lançado: R$ 2.749,50 em 01/02/2026.")).toBeInTheDocument()
    expect(screen.queryByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})).toBeNull()
  })

  it("can be declined, and nothing is sent", async () => {
    await uploadWith(ALL, {opening_proposal: PROPOSAL})
    const post = vi.spyOn(finance, "postImportOpening")
    const offer = await screen.findByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})
    await userEvent.click(within(offer).getByRole("button", {name: "Agora não"}))
    expect(screen.queryByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})).toBeNull()
    expect(post).not.toHaveBeenCalled()
  })

  it("says that unread lines are out of the sum too", async () => {
    await uploadWith(ALL, {opening_proposal: PROPOSAL, rejected_count: 2})
    const offer = await screen.findByRole("region", {name: "Usar o saldo do extrato como saldo inicial?"})
    expect(offer).toHaveTextContent("2 linhas não lidas ficaram fora do cálculo.")
  })

  it("is not offered without a proposal", async () => {
    await uploadWith(ALL, {})
    expect(screen.queryByRole("region", {name: /saldo inicial/})).toBeNull()
  })

  it("is not offered to someone who cannot configure accounts", async () => {
    await uploadWith(ALL.filter(v => v !== "finance.configure"), {opening_proposal: PROPOSAL})
    expect(screen.queryByRole("region", {name: /saldo inicial/})).toBeNull()
  })
})
