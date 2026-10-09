import "@testing-library/jest-dom/vitest"

import {QueryClientProvider} from "@tanstack/react-query"
import {screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

const nav = vi.hoisted(() => ({pathname: "/console/finance", push: vi.fn()}))
vi.mock("next/navigation", () => ({usePathname: () => nav.pathname, useRouter: () => ({push: nav.push})}))

import {FinanceBottomNav} from "@/components/finance/FinanceBottomNav"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import type {Card, Verb} from "@/lib/api/financeTypes"
import * as create from "@/lib/finance/createRequest"

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]

const VISA: Card = {id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: 0, archived: false}

function serve(verbs: Verb[] = ALL, cards: Card[] = [VISA]) {
  vi.spyOn(finance, "listCards").mockResolvedValue({data: cards, has_more: false})
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({
    spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}],
    organizations_unavailable: false,
  })
}

function at(pathname: string, verbs: Verb[] = ALL, cards?: Card[]) {
  nav.pathname = pathname
  serve(verbs, cards)
  return renderWithQuery(<FinanceBottomNav/>)
}

const bar = () => screen.getByRole("navigation", {name: "Navegação principal"})

beforeEach(() => {
  window.localStorage.clear()
  nav.push.mockReset()
  create.clearPendingCreate()
})
afterEach(() => vi.restoreAllMocks())

describe("FinanceBottomNav", () => {
  it("has three tabs and Mais, the current one marked", async () => {
    at("/console/finance/statement")
    const tabs = within(bar()).getAllByRole("link").map(l => l.getAttribute("aria-label"))
    expect(tabs).toEqual(["Resumo", "Agenda", "Extrato"])
    expect(within(bar()).getByRole("link", {name: "Extrato"})).toHaveAttribute("aria-current", "page")
    expect(within(bar()).getByRole("link", {name: "Resumo"})).not.toHaveAttribute("aria-current")
    expect(within(bar()).getByRole("button", {name: "Mais"})).toBeInTheDocument()
  })

  it("puts the secondary sections in the Mais sheet, grouped", async () => {
    at("/console/finance/cards")
    // The verbs arrive first: the bar gains its action slot then, and the
    // sheet is opened after that.
    await within(bar()).findByRole("button", {name: "Nova compra"})
    // Mais reads as current while one of its sections is on screen.
    const more = within(bar()).getByRole("button", {name: "Mais"})
    expect(more).toHaveAttribute("data-active")
    await userEvent.click(more)
    const sheet = await screen.findByRole("dialog", {name: "Mais seções"})
    const launch = within(sheet).getByRole("region", {name: "Lançamentos"})
    expect(within(launch).getAllByRole("link").map(l => l.textContent)).toEqual(["Recorrências", "Cartões", "Importar"])
    const manage = within(sheet).getByRole("region", {name: "Análise e cadastro"})
    expect(within(manage).getAllByRole("link").map(l => l.textContent)).toEqual(["Relatórios", "Contas"])
    expect(within(sheet).getByRole("link", {name: "Cartões"})).toHaveAttribute("aria-current", "page")
    expect(within(sheet).getByRole("link", {name: "Relatórios"})).toHaveAttribute("href", "/console/finance/reports")
  })

  describe("the central action creates what the screen lists", () => {
    it.each([
      ["/console/finance/bills", "Adicionar", "bill"],
      ["/console/finance/cards", "Nova compra", "purchase"],
      ["/console/finance/recurrences", "Recorrência", "recurrence"],
      ["/console/finance/statement", "Transferir", "transfer"],
      ["/console/finance/accounts", "Nova conta", "account"],
    ] as const)("on %s it is %s, on the same screen", async (path, label, kind) => {
      const request = vi.spyOn(create, "requestCreate")
      at(path)
      await userEvent.click(await within(bar()).findByRole("button", {name: label}))
      expect(request).toHaveBeenCalledWith(kind)
      expect(nav.push).not.toHaveBeenCalled()
    })

    it.each(["/console/finance", "/console/finance/import", "/console/finance/reports"])(
      "on %s, with nothing of its own to create, it adds a lançamento in Agenda",
      async path => {
        const request = vi.spyOn(create, "requestCreate")
        at(path)
        await userEvent.click(await within(bar()).findByRole("button", {name: "Adicionar"}))
        expect(request).toHaveBeenCalledWith("bill", "/console/finance/bills")
        expect(nav.push).toHaveBeenCalledWith("/console/finance/bills")
      },
    )

    it("is absent for a role that cannot create there", async () => {
      at("/console/finance/bills", ["finance.read"])
      // Wait for the space's verbs to arrive, then the slot must still be empty.
      await screen.findByRole("navigation", {name: "Navegação principal"})
      await new Promise(r => setTimeout(r, 0))
      expect(within(bar()).queryByRole("button", {name: "Adicionar"})).toBeNull()
    })

    it("on Cartões with no card yet, it is Novo cartão for a role that may configure", async () => {
      const request = vi.spyOn(create, "requestCreate")
      at("/console/finance/cards", ALL, [])
      await userEvent.click(await within(bar()).findByRole("button", {name: "Novo cartão"}))
      expect(request).toHaveBeenCalledWith("purchase")
    })

    it("on Cartões with no card and no configure, there is no action: a button that does nothing is worse", async () => {
      at("/console/finance/cards", ["finance.read", "finance.write"], [])
      await new Promise(r => setTimeout(r, 50))
      expect(within(bar()).queryByRole("button", {name: "Nova compra"})).toBeNull()
      expect(within(bar()).queryByRole("button", {name: "Novo cartão"})).toBeNull()
    })

    it("asks for configure, not write, for Nova conta", async () => {
      at("/console/finance/accounts", ["finance.read", "finance.write"])
      await new Promise(r => setTimeout(r, 0))
      expect(within(bar()).queryByRole("button", {name: "Nova conta"})).toBeNull()
    })
  })
})

describe("createRequest", () => {
  it("delivers a request made before the screen mounted, once", () => {
    create.requestCreate("bill")
    expect(create.takePendingCreate("bill")).toBe(true)
    expect(create.takePendingCreate("bill")).toBe(false)
  })

  it("does not hand one screen's request to another", () => {
    create.requestCreate("transfer")
    expect(create.takePendingCreate("bill")).toBe(false)
    expect(create.takePendingCreate("transfer")).toBe(true)
  })

  it("drops a request nobody took within a few seconds", () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(1_000_000)
    create.requestCreate("bill")
    now.mockReturnValue(1_000_000 + 4_000)
    expect(create.takePendingCreate("bill")).toBe(false)
  })

  it("drops Resumo's Adicionar when the person goes somewhere else before Agenda opens", async () => {
    const {rerender, client} = at("/console/finance")
    await userEvent.click(await within(bar()).findByRole("button", {name: "Adicionar"}))
    expect(nav.push).toHaveBeenCalledWith("/console/finance/bills")
    // They tap Extrato before the bills screen mounted.
    nav.pathname = "/console/finance/statement"
    rerender(<QueryClientProvider client={client}><FinanceBottomNav/></QueryClientProvider>)
    // Minutes later, opening Agenda must not open Novo lançamento.
    expect(create.takePendingCreate("bill")).toBe(false)
  })

  it("keeps Resumo's Adicionar when the navigation lands on Agenda", async () => {
    const {rerender, client} = at("/console/finance")
    await userEvent.click(await within(bar()).findByRole("button", {name: "Adicionar"}))
    nav.pathname = "/console/finance/bills"
    rerender(<QueryClientProvider client={client}><FinanceBottomNav/></QueryClientProvider>)
    expect(create.takePendingCreate("bill")).toBe(true)
  })
})
