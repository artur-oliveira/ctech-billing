import "@testing-library/jest-dom/vitest"

import {act, screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {AccountsView} from "@/components/finance/AccountsView"
import {optionsOf, pick, renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import * as createRequest from "@/lib/finance/createRequest"
import type {Account, Verb} from "@/lib/api/financeTypes"

// A name also appears as an <option> of the default-receiving select, so rows
// are found inside their list items.
async function row(name: string) {
  await screen.findAllByRole("list")
  const el = screen.getAllByText(name).find(e => e.closest("li"))
  if (!el) throw new Error(`no row ${name}`)
  return el.closest("li") as HTMLElement
}

// The list behind an open drawer is aria-hidden, so it is read by text.
async function listed(name: string) {
  return waitFor(() => {
    const el = screen.getAllByText(name).find(e => e.closest("li"))
    if (!el) throw new Error(`no row ${name}`)
    return el.closest("li") as HTMLElement
  })
}

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const ACCOUNTS: Account[] = [
  {id: "sys-payables", name: "Contas a pagar", class: "liability", system: true, archived: false, balance: -5000},
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 842315},
  {id: "pp", name: "Poupança", class: "asset", system: false, archived: false, balance: 0},
  {id: "sal", name: "Salário", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
  {id: "alu", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: -120000},
  {id: "old", name: "Assinaturas antigas", class: "expense", dre_group: "operating_expenses", system: false, archived: true, balance: 0},
]

function serve(verbs: Verb[], accounts = ACCOUNTS) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: accounts, has_more: false})
  vi.spyOn(finance, "getSettings").mockResolvedValue({})
  vi.spyOn(finance, "listCards").mockResolvedValue({data: [], has_more: false})
}

beforeEach(() => window.localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("F8 — accounts", () => {
  it("never lists system accounts and groups the rest", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    expect(await row("Conta corrente")).toBeInTheDocument()
    expect(screen.queryByText("Contas a pagar")).toBeNull()
    expect(screen.getAllByRole("heading", {name: "Contas"})).toHaveLength(2)
    expect(screen.getByRole("heading", {name: "Receitas"})).toBeInTheDocument()
    expect(screen.getByRole("heading", {name: "Despesas"})).toBeInTheDocument()
    expect(screen.getByText("R$ 8.423,15")).toBeInTheDocument()
  })

  it("hides archived items until asked", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(screen.queryByText("Assinaturas antigas")).toBeNull()
    await userEvent.click(screen.getByRole("button", {name: "Mostrar arquivadas"}))
    expect(screen.getByText("Assinaturas antigas")).toBeInTheDocument()
  })

  it("offers each class only the DRE groups the server accepts", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await pick("Tipo", "Receita")
    expect(await optionsOf("Grupo na DRE")).toEqual(["Receita bruta", "Resultado financeiro", "Outros"])
    await pick("Tipo", "Conta")
    expect(screen.queryByRole("combobox", {name: "Grupo na DRE"})).toBeNull()
  })

  it("creates without an id and keeps one key across a retry", async () => {
    serve(ALL)
    const create = vi.spyOn(finance, "createAccount")
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockResolvedValueOnce({id: "new", name: "Cartão", class: "asset", system: false, archived: false, balance: 0})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Cartão")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await screen.findByRole("alert")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
    const [first, second] = create.mock.calls
    expect(first[1]).not.toHaveProperty("id")
    expect(first[2]).toBe(second[2]) // the same intent, the same key
  })

  it("posts an opening balance right after creating an asset account", async () => {
    serve(ALL)
    vi.spyOn(finance, "createAccount").mockResolvedValue({id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0})
    const opening = vi.spyOn(finance, "postOpeningBalance").mockResolvedValue({transaction_id: "t"})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    expect(screen.queryByLabelText("Data")).not.toBeInTheDocument()
    await userEvent.type(screen.getByLabelText(/^Saldo inicial/), "-1.500,00")
    expect(screen.getByLabelText("Data")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(opening).toHaveBeenCalledWith(expect.anything(), "new", expect.objectContaining({amount: -150000}), expect.any(String)))
    await waitFor(() => expect(screen.queryByLabelText(/^Saldo inicial/)).not.toBeInTheDocument())
  })

  // UX batch 3 (the same chain as batch 2's bill flicker): refreshing the list
  // between "account created" and "its opening balance posted" painted the new
  // account at R$ 0,00, a balance the person never entered.
  it("never shows a new account at zero while its opening balance is being posted", async () => {
    let server: Account[] = [...ACCOUNTS]
    serve(ALL)
    const list = vi.mocked(finance.listAccounts).mockImplementation(async () => ({data: server, has_more: false}))
    const created: Account = {id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0}
    vi.spyOn(finance, "createAccount").mockImplementation(async () => { server = [...server, created]; return created })
    let land!: () => void
    vi.spyOn(finance, "postOpeningBalance").mockImplementation(() => new Promise(resolve => {
      land = () => { server = server.map(a => (a.id === "new" ? {...a, balance: 150000} : a)); resolve({transaction_id: "t"}) }
    }))
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    await userEvent.type(screen.getByLabelText(/^Saldo inicial/), "1.500,00")
    const before = list.mock.calls.length
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(finance.postOpeningBalance).toHaveBeenCalled())
    await act(() => new Promise(r => setTimeout(r, 50)))
    expect(list.mock.calls.length).toBe(before) // no refetch between the two halves
    expect(screen.queryAllByText("Nubank").some(e => e.closest("li"))).toBe(false)
    await act(async () => land())
    const nubank = await listed("Nubank")
    expect(within(nubank).getByText("R$ 1.500,00")).toBeInTheDocument()
  })

  it("refreshes the list when the opening balance fails, so the created account shows", async () => {
    let server: Account[] = [...ACCOUNTS]
    serve(ALL)
    vi.mocked(finance.listAccounts).mockImplementation(async () => ({data: server, has_more: false}))
    const created: Account = {id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0}
    vi.spyOn(finance, "createAccount").mockImplementation(async () => { server = [...server, created]; return created })
    vi.spyOn(finance, "postOpeningBalance").mockRejectedValue({code: "ERR_NETWORK"})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    await userEvent.type(screen.getByLabelText(/^Saldo inicial/), "1.500,00")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    expect(await screen.findByText("A conta foi criada, mas o saldo inicial não foi salvo.")).toBeInTheDocument()
    expect(await listed("Nubank")).toBeInTheDocument()
  })

  it("offers to import a statement into a new bank account, with the account chosen", async () => {
    serve(ALL)
    vi.spyOn(finance, "createAccount").mockResolvedValue({id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    const dialog = await screen.findByRole("dialog", {name: "Nova conta"})
    expect(await within(dialog).findByText("Conta criada. Importar um extrato agora?")).toBeInTheDocument()
    expect(within(dialog).getByRole("link", {name: "Importar extrato"})).toHaveAttribute("href", "/console/finance/import?account=new")
    // The import is offered, never part of the create form.
    expect(within(dialog).queryByLabelText("Arquivo do extrato")).toBeNull()
    await userEvent.click(within(dialog).getByRole("button", {name: "Agora não"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })

  it("does not offer an import for a category, nor to a role that cannot import", async () => {
    serve(ALL)
    vi.spyOn(finance, "createAccount").mockResolvedValue({id: "new", name: "Feira", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0})
    const {unmount} = renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Feira")
    await pick("Tipo", "Despesa")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(screen.queryByText(/Importar um extrato agora/)).toBeNull()
    unmount()
    vi.restoreAllMocks()

    serve(["finance.read", "finance.configure"])
    vi.spyOn(finance, "createAccount").mockResolvedValue({id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(screen.queryByText(/Importar um extrato agora/)).toBeNull()
  })

  it("keeps the account when the opening balance fails, and retries with the same key", async () => {
    serve(ALL)
    const create = vi.spyOn(finance, "createAccount").mockResolvedValue({id: "new", name: "Nubank", class: "asset", system: false, archived: false, balance: 0})
    const opening = vi.spyOn(finance, "postOpeningBalance")
      .mockRejectedValueOnce({code: "ERR_NETWORK"})
      .mockResolvedValueOnce({transaction_id: "t"})
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    await userEvent.type(screen.getByLabelText(/^Nome/), "Nubank")
    await userEvent.type(screen.getByLabelText(/^Saldo inicial/), "1.500,00")
    await userEvent.click(screen.getByRole("button", {name: "Criar"}))
    expect(await screen.findByText("A conta foi criada, mas o saldo inicial não foi salvo.")).toBeInTheDocument()
    expect(screen.getByRole("button", {name: "Deixar sem saldo inicial"})).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", {name: "Tentar novamente"}))
    await waitFor(() => expect(opening).toHaveBeenCalledTimes(2))
    expect(opening.mock.calls[0][3]).toBe(opening.mock.calls[1][3])
    expect(create).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.queryByText(/saldo inicial não foi salvo/)).not.toBeInTheDocument())
  })

  it("bounds the name to what the API accepts", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    expect(screen.getByLabelText(/^Nome/)).toHaveAttribute("maxLength", "80")
  })

  it("lists cards with what they owe and a way to open them", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    const visa = await row("Visa")
    expect(within(visa).getByText(/Deve R\$\s1\.200,00/)).toBeInTheDocument()
    expect(within(visa).getByRole("link", {name: "Abrir"})).toHaveAttribute("href", "/console/finance/cards?card=visa")
  })

  it("shows a card's mark and last digits in the Cartões list", async () => {
    serve(ALL)
    vi.mocked(finance.listCards).mockResolvedValue({data: [{id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: -120000, archived: false, brand: "visa", last4: "4242"}], has_more: false})
    renderWithQuery(<AccountsView/>)
    const visa = await row("Visa")
    expect(await within(visa).findByText("•••• 4242")).toBeInTheDocument()
    expect(visa.querySelector("svg[data-brand=visa]")).not.toBeNull()
  })

  it("never offers an opening balance on a category", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova conta"}))
    expect(screen.getByLabelText(/^Saldo inicial/)).toBeInTheDocument()
    await pick("Tipo", "Despesa")
    expect(screen.queryByLabelText(/^Saldo inicial/)).not.toBeInTheDocument()
  })

  it("posts an opening balance on an account created before there was one", async () => {
    serve(ALL)
    const opening = vi.spyOn(finance, "postOpeningBalance")
      .mockRejectedValueOnce({response: {status: 409, data: {code: "opening_balance_exists", detail: "account already has an opening balance"}}})
      .mockResolvedValueOnce({transaction_id: "t"})
    renderWithQuery(<AccountsView/>)
    const cc = await row("Conta corrente")
    expect(within(await row("Aluguel")).queryByRole("button", {name: "Saldo inicial"})).not.toBeInTheDocument()
    await userEvent.click(within(cc).getByRole("button", {name: "Saldo inicial"}))
    await userEvent.type(within(cc).getByLabelText("Valor"), "-200,00")
    await userEvent.click(within(cc).getByRole("button", {name: "Salvar"}))
    expect(await within(cc).findByText(/já tem saldo inicial/)).toBeInTheDocument()
    await userEvent.click(within(cc).getByRole("button", {name: "Salvar"}))
    await waitFor(() => expect(opening).toHaveBeenLastCalledWith(expect.anything(), "cc", expect.objectContaining({amount: -20000}), expect.any(String)))
    await waitFor(() => expect(within(cc).queryByLabelText("Valor")).not.toBeInTheDocument())
  })

  it("asks before archiving and says nothing is deleted", async () => {
    serve(ALL)
    const archive = vi.spyOn(finance, "archiveAccount").mockResolvedValue(undefined)
    renderWithQuery(<AccountsView/>)
    const li = await row("Poupança")
    await userEvent.click(within(li).getByRole("button", {name: "Arquivar"}))
    expect(within(li).getByText(/histórico fica/i)).toBeInTheDocument()
    expect(archive).not.toHaveBeenCalled()
    // The row's Arquivar stays (expanded); the confirmation has its own.
    await userEvent.click(within(within(li).getByText(/histórico fica/i).parentElement!).getByRole("button", {name: "Arquivar"}))
    await waitFor(() => expect(archive).toHaveBeenCalledWith(expect.anything(), "pp", expect.any(String)))
  })

  it("shows a member no configuration controls", async () => {
    serve(["finance.read", "finance.write", "finance.settle", "finance.import"])
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(screen.queryByRole("button", {name: "Nova conta"})).toBeNull()
    expect(screen.queryByRole("button", {name: "Arquivar"})).toBeNull()
    expect(screen.queryByRole("combobox", {name: "Conta padrão de recebimento"})).toBeNull()
  })

  it("offers only active asset accounts as the default receiving account", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(await optionsOf("Conta padrão de recebimento")).toEqual(["Nenhuma", "Conta corrente", "Poupança"])
  })

  it("posts CTech invoices by default and lets an admin turn it off", async () => {
    serve(ALL)
    const set = vi.spyOn(finance, "setPostCTechInvoices").mockResolvedValue({post_ctech_invoices: false})
    renderWithQuery(<AccountsView/>)
    const toggle = await screen.findByRole("switch", {name: "Lançar minhas faturas da CTech automaticamente neste espaço"})
    await waitFor(() => expect(toggle).toBeChecked()) // absent on the server reads as on
    expect(screen.getByText(/Hoje vale para o espaço pessoal/)).toBeInTheDocument()
    await userEvent.click(toggle)
    await waitFor(() => expect(set).toHaveBeenCalledWith(expect.anything(), false, expect.any(String)))
  })

  it("shows the CTech invoices setting off when the space turned it off", async () => {
    serve(ALL)
    vi.mocked(finance.getSettings).mockResolvedValue({post_ctech_invoices: false})
    renderWithQuery(<AccountsView/>)
    const toggle = await screen.findByRole("switch", {name: /faturas da CTech/})
    await waitFor(() => expect(toggle).not.toBeChecked())
  })

  it("hides the CTech invoices setting from someone who cannot configure", async () => {
    serve(["finance.read", "finance.write", "finance.settle", "finance.import"])
    renderWithQuery(<AccountsView/>)
    await row("Conta corrente")
    expect(screen.queryByRole("switch")).toBeNull()
  })

  // The payer side of 6.7 posts to Pessoal only, never to a shared space
  // (shared-spaces spec § 2): elsewhere the switch would do nothing.
  it("shows the CTech invoices setting only in Pessoal", async () => {
    const WS = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"
    for (const kind of ["personal", "organization"] as const) {
      window.localStorage.setItem("ctech-billing-finance-space", `org:${WS}`)
      vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({
        spaces: [
          {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false},
          {selector: `org:${WS}`, kind, display_name: "Casa", role: "owner", verbs: ALL, manage_people: kind === "personal"},
        ],
        organizations_unavailable: false,
      })
      vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
      vi.spyOn(finance, "getSettings").mockResolvedValue({})
      const {unmount} = renderWithQuery(<AccountsView/>)
      await row("Conta corrente")
      // configure is held (the default receiving account is offered) …
      expect(await screen.findByRole("combobox", {name: "Conta padrão de recebimento"})).toBeInTheDocument()
      // … and still no CTech invoices switch outside Pessoal.
      expect(screen.queryByRole("switch")).toBeNull()
      unmount()
      vi.restoreAllMocks()
    }
  })

  it("teaches the first step when there is nothing yet", async () => {
    serve(ALL, [])
    renderWithQuery(<AccountsView/>)
    expect(await screen.findByText(/nenhuma conta ainda/i)).toBeInTheDocument()
  })

  it("shows an error with a retry", async () => {
    serve(ALL)
    vi.mocked(finance.listAccounts).mockRejectedValue({response: {status: 500, data: {title: "Erro"}}})
    renderWithQuery(<AccountsView/>)
    expect(await screen.findByRole("button", {name: "Tentar novamente"})).toBeInTheDocument()
  })
})

describe("the phone's central action", () => {
  it("does not open Nova conta for a role that cannot configure", async () => {
    serve(["finance.read", "finance.write"])
    createRequest.requestCreate("account")
    renderWithQuery(<AccountsView/>)
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByRole("dialog")).toBeNull()
  })

  it("opens Nova conta", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    act(() => createRequest.requestCreate("account"))
    expect(await screen.findByRole("dialog", {name: "Nova conta"})).toBeInTheDocument()
  })
})

// UX batch 4: at 375px "Conta corrente" was cut to "Conta co…". A row of Contas
// is a ledger row: the name wraps (two lines on a phone), its balance beside
// it, and Saldo inicial and Arquivar are also in the row's "⋯".
describe("F8 — an account row on a phone", () => {
  it("wraps the name instead of cutting it, and lists its actions in ⋯", async () => {
    serve(ALL)
    renderWithQuery(<AccountsView/>)
    const li = await row("Conta corrente")
    const name = within(li).getByText("Conta corrente")
    expect(name).not.toHaveClass("truncate")
    expect(name).toHaveClass("line-clamp-2")
    expect(name).toHaveAttribute("title", "Conta corrente")
    await userEvent.click(within(li).getByRole("button", {name: "Mais ações: Conta corrente"}))
    expect((await screen.findAllByRole("menuitem")).map(i => i.textContent)).toEqual(["Saldo inicial", "Arquivar"])
  })
})
