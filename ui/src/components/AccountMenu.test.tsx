import "@testing-library/jest-dom/vitest"

import {screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

const auth = vi.hoisted(() => ({
  name: "Ana" as string | null,
  authenticated: true,
  loading: false,
  login: vi.fn(),
  logout: vi.fn(),
  onCallback: vi.fn(),
}))
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => auth}))

import {AccountMenu} from "@/components/AccountMenu"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as consoleApi from "@/lib/api/console"
import * as portal from "@/lib/api/portal"

const FULL = "Ana Maria Ribeiro da Silva Albuquerque"
const EMAIL = "ana.maria.ribeiro.albuquerque@exemplo.com.br"
const forbidden = {response: {status: 403, data: {title: "Sem organização"}}}

function person({operator}: {operator: boolean}) {
  vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: FULL, email: EMAIL, terms_accepted: true})
  const probe = vi.spyOn(consoleApi, "getConsoleSession")
  if (operator) probe.mockResolvedValue({organization_id: "org", display_name: "Acme", livemode: true, payout_status: "ok", can_charge: true})
  else probe.mockRejectedValue(forbidden)
}

async function open() {
  await userEvent.click(await screen.findByRole("button", {name: `Menu da conta: ${FULL}`}))
  return screen.findByRole("menu")
}

beforeEach(() => {
  auth.name = "Ana"
  auth.authenticated = true
  auth.logout.mockReset()
})
afterEach(() => vi.restoreAllMocks())

describe("AccountMenu", () => {
  it("shows the whole name and the e-mail, never cut", async () => {
    person({operator: false})
    renderWithQuery(<AccountMenu view="portal"/>)
    const menu = await open()
    expect(within(menu).getByText(FULL)).toBeInTheDocument()
    expect(within(menu).getByText(EMAIL)).toBeInTheDocument()
  })

  it("marks the portal as the current view, and offers the console as Finanças to someone with no organization", async () => {
    person({operator: false})
    renderWithQuery(<AccountMenu view="portal"/>)
    const menu = await open()
    const portalItem = within(menu).getByRole("menuitem", {name: "Portal"})
    const consoleItem = await within(menu).findByRole("menuitem", {name: "Console"})
    expect(portalItem).toHaveAttribute("aria-current", "true")
    expect(portalItem).toHaveAttribute("href", "/dashboard")
    expect(consoleItem).not.toHaveAttribute("aria-current")
    expect(consoleItem).toHaveAttribute("href", "/console/finance")
  })

  it("marks the console as the current view, and opens an operator's console on invoicing", async () => {
    person({operator: true})
    renderWithQuery(<AccountMenu view="console"/>)
    await screen.findByRole("button", {name: `Menu da conta: ${FULL}`})
    // The operator probe answers before the menu is read.
    await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalled())
    const menu = await open()
    const consoleItem = within(menu).getByRole("menuitem", {name: "Console"})
    expect(consoleItem).toHaveAttribute("aria-current", "true")
    await vi.waitFor(() => expect(consoleItem).toHaveAttribute("href", "/console/overview"))
    expect(within(menu).getByRole("menuitem", {name: "Portal"})).not.toHaveAttribute("aria-current")
  })

  it("falls back to the signed-in name when there is no billing account to read one from", async () => {
    vi.spyOn(portal, "getSession").mockRejectedValue({response: {status: 403, data: {code: "no_billing_account"}}})
    vi.spyOn(consoleApi, "getConsoleSession").mockRejectedValue(forbidden)
    auth.name = "Ana Ribeiro"
    renderWithQuery(<AccountMenu view="portal"/>)
    await userEvent.click(await screen.findByRole("button", {name: "Menu da conta: Ana Ribeiro"}))
    expect(within(await screen.findByRole("menu")).getByRole("menuitem", {name: "Console"})).toBeInTheDocument()
  })

  it("signs out from Sair", async () => {
    person({operator: false})
    renderWithQuery(<AccountMenu view="console"/>)
    const menu = await open()
    await userEvent.click(within(menu).getByRole("menuitem", {name: "Sair"}))
    expect(auth.logout).toHaveBeenCalledTimes(1)
  })

  it("is not there for a signed-out reader", () => {
    person({operator: false})
    auth.authenticated = false
    renderWithQuery(<AccountMenu view="portal"/>)
    expect(screen.queryByRole("button", {name: /Menu da conta/})).toBeNull()
  })
})

// Production: an organization with no billing account. The shell shows the
// empty state because the session answered 403, and mounts this menu; if the
// menu re-asked for the session on mount, the refetch would clear the error,
// the shell would drop the empty state and the menu, the 403 would come back,
// and the two would trade places forever.
describe("AccountMenu with no billing account", () => {
  it("does not ask for the session again when it mounts on a 403", async () => {
    const getSession = vi.spyOn(portal, "getSession").mockRejectedValue({response: {status: 403, data: {type: "/problems/no-billing-account"}}})
    vi.spyOn(consoleApi, "getConsoleSession").mockRejectedValue(forbidden)
    const {client} = renderWithQuery(<p>shell</p>)
    await client.prefetchQuery({queryKey: portal.portalKeys.session, queryFn: portal.getSession, retry: false})
    expect(getSession).toHaveBeenCalledTimes(1)
    const {QueryClientProvider} = await import("@tanstack/react-query")
    const {render} = await import("@testing-library/react")
    render(<QueryClientProvider client={client}><AccountMenu view="portal"/></QueryClientProvider>)
    await screen.findByRole("button", {name: /Menu da conta/})
    expect(getSession).toHaveBeenCalledTimes(1)
  })
})
