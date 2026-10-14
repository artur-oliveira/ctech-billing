import "@testing-library/jest-dom/vitest"

import {fireEvent, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

const auth = vi.hoisted(() => ({name: "Ana" as string | null, authenticated: true, loading: false, login: vi.fn(), logout: vi.fn(), onCallback: vi.fn()}))
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => auth}))
const nav = vi.hoisted(() => ({push: vi.fn()}))
vi.mock("next/navigation", () => ({usePathname: () => "/finance", useRouter: () => ({push: nav.push})}))

import {renderWithQuery} from "@/components/finance/finance.test-utils"
import {GlobalSearch} from "@/components/shell/GlobalSearch"
import * as consoleApi from "@/lib/api/console"
import * as finance from "@/lib/api/finance"
import type {Verb} from "@/lib/api/financeTypes"
import * as portal from "@/lib/api/portal"
import {clearPendingCreate, takePendingCreate} from "@/lib/createRequest"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]

function person({operator, billing = true}: {operator: boolean; billing?: boolean}) {
  if (billing) vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: "Ana", email: "ana@exemplo.com.br", terms_accepted: true})
  else vi.spyOn(portal, "getSession").mockRejectedValue({response: {status: 403, data: {title: "Sem conta"}}})
  vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({
    organizations: operator ? [{organization_id: ORG, selector: `org:${ORG}`, display_name: "Acme", role: "owner", merchant_status: "live"}] : [],
    organizations_unavailable: false,
  })
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({
    spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}],
    organizations_unavailable: false,
  })
}

async function search(text: string) {
  renderWithQuery(<GlobalSearch view="finance"/>)
  fireEvent.keyDown(window, {key: "k", ctrlKey: true})
  const input = await screen.findByRole("combobox")
  // Wait for the sessions that decide what is searchable.
  await vi.waitFor(() => expect(portal.getSession).toHaveBeenCalled())
  await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalled())
  await userEvent.type(input, text)
}
const names = () => screen.queryAllByRole("option").map(o => o.textContent ?? "")

beforeEach(() => {
  window.localStorage.clear()
  nav.push.mockReset()
  clearPendingCreate()
})
afterEach(() => vi.restoreAllMocks())

describe("GlobalSearch", () => {
  it("opens on Ctrl+K and finds Recorrências, then Nova recorrência, for 'recor'", async () => {
    person({operator: false})
    await search("recor")
    await vi.waitFor(() => expect(names()).toHaveLength(2))
    expect(names()[0]).toContain("Recorrências")
    expect(names()[1]).toContain("Nova recorrência")
  })

  it("choosing the action asks the screen to open its form, and navigates there", async () => {
    person({operator: false})
    await search("nova recor")
    await userEvent.click(await screen.findByRole("option", {name: /Nova recorrência/}))
    expect(nav.push).toHaveBeenCalledWith("/finance/recurrences")
    expect(takePendingCreate("recurrence")).toBe(true)
  })

  it("an operator finds the console's screens, as links to their routes, marked Console", async () => {
    person({operator: true})
    await search("catalogo")
    const option = await screen.findByRole("option", {name: /Catálogo/})
    expect(option).toHaveAttribute("href", "/console/catalog")
    expect(option).toHaveTextContent("Console")
  })

  it("someone with no organization has nothing from the console", async () => {
    person({operator: false})
    await search("catalogo")
    expect(await screen.findByText(/Nada encontrado para/)).toBeInTheDocument()
  })

  it("the portal's screens are searchable with a billing account", async () => {
    person({operator: false})
    await search("fatura")
    await vi.waitFor(() => expect(names().some(n => n.includes("Portal"))).toBe(true))
  })

  it("and are not without one", async () => {
    person({operator: false, billing: false})
    await search("fatura")
    await screen.findAllByRole("option") // Cartões, by its keyword
    expect(names().some(n => n.includes("Portal"))).toBe(false)
  })

  it("asks for what makes it searchable once it is opened", async () => {
    person({operator: false})
    renderWithQuery(<GlobalSearch view="portal"/>)
    await userEvent.click(screen.getByRole("button", {name: "Buscar"}))
    await screen.findByRole("combobox")
    expect(finance.getFinanceSpaces).toHaveBeenCalled()
  })

  it("does not call the spaces list or the sessions at first paint", async () => {
    person({operator: false})
    renderWithQuery(<GlobalSearch view="portal"/>)
    await new Promise(r => setTimeout(r, 50))
    expect(finance.getFinanceSpaces).not.toHaveBeenCalled()
    expect(consoleApi.getConsoleSession).not.toHaveBeenCalled()
  })
})
