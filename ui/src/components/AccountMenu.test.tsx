import "@testing-library/jest-dom/vitest";

import {screen, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

const auth = vi.hoisted(() => ({
  name: "Ana" as string | null,
  authenticated: true,
  loading: false,
  login: vi.fn(),
  logout: vi.fn(),
  onCallback: vi.fn(),
}));
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => auth}));

import {AccountMenu} from "@/components/AccountMenu";
import {renderWithQuery} from "@/components/finance/finance.test-utils";
import * as consoleApi from "@/lib/api/console";
import * as portal from "@/lib/api/portal";
import {getMode, setMode} from "@/lib/console/mode";

const FULL = "Ana Maria Ribeiro da Silva Albuquerque";
const EMAIL = "ana.maria.ribeiro.albuquerque@exemplo.com.br";
const forbidden = {response: {status: 403, data: {title: "Sem organização"}}};

function person({operator}: {operator: boolean}) {
  vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: FULL, email: EMAIL, terms_accepted: true});
  const probe = vi.spyOn(consoleApi, "getConsoleSession");
  if (operator) probe.mockResolvedValue({organizations: [{organization_id: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", selector: "org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", display_name: "Acme", role: "owner", merchant_status: "live"}], organizations_unavailable: false});
  else probe.mockResolvedValue({organizations: [], organizations_unavailable: false});
}

async function open() {
  await userEvent.click(await screen.findByRole("button", {name: `Menu da conta: ${FULL}`}));
  return screen.findByRole("menu");
}

beforeEach(() => {
  auth.name = "Ana";
  auth.authenticated = true;
  auth.logout.mockReset();
});
afterEach(() => vi.restoreAllMocks());

describe("AccountMenu", () => {
  it("offers the language, which the bar's switch hides on a phone", async () => {
    person({operator: false});
    renderWithQuery(<AccountMenu view="portal"/>);
    const menu = await open();
    expect(within(menu).getByRole("menuitem", {name: "Português"})).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", {name: "English"})).toBeInTheDocument();
  });

  it("shows the whole name and the e-mail, never cut", async () => {
    person({operator: false});
    renderWithQuery(<AccountMenu view="portal"/>);
    const menu = await open();
    expect(within(menu).getByText(FULL)).toBeInTheDocument();
    expect(within(menu).getByText(EMAIL)).toBeInTheDocument();
  });

  it("marks the portal as the current view, offers Finanças to everyone and no console to a non-operator", async () => {
    person({operator: false});
    renderWithQuery(<AccountMenu view="portal"/>);
    await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalled());
    const menu = await open();
    const portalItem = within(menu).getByRole("menuitem", {name: "Portal"});
    const financeItem = within(menu).getByRole("menuitem", {name: "Finanças"});
    expect(portalItem).toHaveAttribute("aria-current", "true");
    expect(portalItem).toHaveAttribute("href", "/dashboard");
    expect(financeItem).toHaveAttribute("href", "/finance");
    expect(financeItem).not.toHaveAttribute("aria-current");
    expect(within(menu).queryByRole("menuitem", {name: "Console"})).toBeNull();
  });

  it("marks Finanças as the current view in its own area", async () => {
    person({operator: true});
    renderWithQuery(<AccountMenu view="finance"/>);
    const menu = await open();
    expect(within(menu).getByRole("menuitem", {name: "Finanças"})).toHaveAttribute("aria-current", "true");
    expect(await within(menu).findByRole("menuitem", {name: "Console"})).not.toHaveAttribute("aria-current");
  });

  it("marks the console as the current view, and opens an operator's console on invoicing", async () => {
    person({operator: true});
    renderWithQuery(<AccountMenu view="console"/>);
    await screen.findByRole("button", {name: `Menu da conta: ${FULL}`});
    // The operator probe answers before the menu is read.
    await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalled());
    const menu = await open();
    const consoleItem = within(menu).getByRole("menuitem", {name: "Console"});
    expect(consoleItem).toHaveAttribute("aria-current", "true");
    await vi.waitFor(() => expect(consoleItem).toHaveAttribute("href", "/console/overview"));
    expect(within(menu).getByRole("menuitem", {name: "Portal"})).not.toHaveAttribute("aria-current");
  });

  it("falls back to the signed-in name when there is no billing account to read one from", async () => {
    vi.spyOn(portal, "getSession").mockRejectedValue({response: {status: 403, data: {code: "no_billing_account"}}});
    vi.spyOn(consoleApi, "getConsoleSession").mockRejectedValue(forbidden);
    auth.name = "Ana Ribeiro";
    renderWithQuery(<AccountMenu view="portal"/>);
    await userEvent.click(await screen.findByRole("button", {name: "Menu da conta: Ana Ribeiro"}));
    expect(within(await screen.findByRole("menu")).getByRole("menuitem", {name: "Finanças"})).toBeInTheDocument();
  });

  it("signs out from Sair", async () => {
    person({operator: false});
    renderWithQuery(<AccountMenu view="console"/>);
    const menu = await open();
    await userEvent.click(within(menu).getByRole("menuitem", {name: "Sair"}));
    expect(auth.logout).toHaveBeenCalledTimes(1);
  });

  it("is not there for a signed-out reader", () => {
    person({operator: false});
    auth.authenticated = false;
    renderWithQuery(<AccountMenu view="portal"/>);
    expect(screen.queryByRole("button", {name: /Menu da conta/})).toBeNull();
  });
});

// Production: an organization with no billing account. The shell shows the
// empty state because the session answered 403, and mounts this menu; if the
// menu re-asked for the session on mount, the refetch would clear the error,
// the shell would drop the empty state and the menu, the 403 would come back,
// and the two would trade places forever.
describe("AccountMenu with no billing account", () => {
  it("does not ask for the session again when it mounts on a 403", async () => {
    const getSession = vi.spyOn(portal, "getSession").mockRejectedValue({response: {status: 403, data: {type: "/problems/no-billing-account"}}});
    vi.spyOn(consoleApi, "getConsoleSession").mockRejectedValue(forbidden);
    const {client} = renderWithQuery(<p>shell</p>);
    await client.prefetchQuery({queryKey: portal.portalKeys.session, queryFn: portal.getSession, retry: false});
    expect(getSession).toHaveBeenCalledTimes(1);
    const {QueryClientProvider} = await import("@tanstack/react-query");
    const {render} = await import("@testing-library/react");
    render(<QueryClientProvider client={client}><AccountMenu view="portal"/></QueryClientProvider>);
    await screen.findByRole("button", {name: /Menu da conta/});
    expect(getSession).toHaveBeenCalledTimes(1);
  });

  it("offers the console to an admin whose organization does not bill yet, to reach the invitation", async () => {
    vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: FULL, email: EMAIL, terms_accepted: true});
    vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({organizations: [{organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", selector: "org:0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", display_name: "Padaria", role: "admin", merchant_status: "none"}], organizations_unavailable: false});
    renderWithQuery(<AccountMenu view="portal"/>);
    const menu = await open();
    expect(await within(menu).findByRole("menuitem", {name: "Console"})).toBeInTheDocument();
  });

  // The menu's probe is display only: it does not ask ctech-account again every
  // time the window regains focus (part 1 deferred minor). An application or a
  // decision invalidates the session explicitly.
  it("does not ask for the console session again when the window regains focus", async () => {
    person({operator: true});
    const {client} = renderWithQuery(<AccountMenu view="portal"/>);
    await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalledTimes(1));
    const query = client.getQueryCache().find({queryKey: consoleApi.consoleKeys.session()});
    const options = query?.observers.map(o => o.options) ?? [];
    expect(options).toHaveLength(1);
    expect(options[0].refetchOnWindowFocus).toBe(false);
    expect(options[0].staleTime).toBeGreaterThanOrEqual(5 * 60_000);
  });
});

// The mode moved from a header segment into the menu (2026-10-14): it is
// chosen here in the console and Finanças, and never offered in the portal.
describe("AccountMenu mode", () => {
  afterEach(() => setMode("live"));

  it("offers Produção and Teste in the console, the current one marked", async () => {
    person({operator: true});
    renderWithQuery(<AccountMenu view="console"/>);
    const menu = await open();
    expect(within(menu).getByRole("menuitem", {name: "Produção"})).toHaveAttribute("aria-current", "true");
    await userEvent.click(within(menu).getByRole("menuitem", {name: "Teste"}));
    expect(getMode()).toBe("test");
  });

  it("has no mode in the portal", async () => {
    person({operator: false});
    renderWithQuery(<AccountMenu view="portal"/>);
    const menu = await open();
    expect(within(menu).queryByRole("menuitem", {name: "Teste"})).toBeNull();
  });
});
