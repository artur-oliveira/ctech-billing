import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type {ReactNode} from "react";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleCredentialsPage from "@/app/(console)/console/credentials/page";
import * as consoleApi from "@/lib/api/console";
import type {MerchantStatus} from "@/lib/api/consoleTypes";
import {ConsoleScopeProvider} from "@/lib/console/scope";
import ptBR from "@/locales/pt-BR";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;

function session(status: MerchantStatus) {
  vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({
    organizations: [{organization_id: SCOPE.org, selector: `org:${SCOPE.org}`, display_name: "CTech", role: "owner", merchant_status: status}],
    organizations_unavailable: false,
  });
}

function list() {
  vi.spyOn(consoleApi, "listConsoleCredentials").mockResolvedValue({
    data: [
      {client_id: "dfe-billing", livemode: true, active: true, provisioned: true, connected_by_me: false},
      {client_id: "cli_mine", livemode: true, active: true, provisioned: false, connected_by: "usr_1", connected_by_me: true, connected_at: "2026-10-10T12:00:00Z"},
    ],
    machine_scopes: ["billing:invoices:read", "billing:usage:write"],
  });
}

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>);
}

afterEach(() => vi.restoreAllMocks());

describe("/console/credentials", () => {
  it("offers disconnect only on a connected client and says who connected it", async () => {
    session("live");
    list();
    wrap(<ConsoleCredentialsPage/>);
    const mine = (await screen.findByText("cli_mine")).closest("li")!;
    expect(within(mine).getByText(/Conectado por você em 10\/10\/2026/)).toBeInTheDocument();
    expect(within(mine).getByRole("button", {name: "Desconectar"})).toBeInTheDocument();
    const seeded = screen.getByText("dfe-billing").closest("li")!;
    expect(within(seeded).getByText("Gerenciado pela CTech")).toBeInTheDocument();
    expect(within(seeded).queryByRole("button")).toBeNull();
  });

  it("links to the developer page with the API's machine scopes", async () => {
    session("live");
    list();
    wrap(<ConsoleCredentialsPage/>);
    const link = await screen.findByRole("link", {name: "Criar cliente na CTech Account"});
    const href = new URL(link.getAttribute("href")!);
    expect(href.searchParams.get("scopes")).toBe("billing:invoices:read,billing:usage:write");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("connects the trimmed id and shows a refusal under the field", async () => {
    session("live");
    list();
    const connect = vi.spyOn(consoleApi, "connectCredential").mockRejectedValue(
      {response: {status: 422, data: {type: "about:blank", status: 422, title: "T", code: "client_not_yours"}}});
    wrap(<ConsoleCredentialsPage/>);
    await userEvent.type(await screen.findByLabelText("ID do cliente"), "  cli_x  ");
    await userEvent.click(screen.getByRole("button", {name: "Conectar"}));
    expect(connect).toHaveBeenCalledWith(SCOPE, "cli_x");
    expect(await screen.findByText(ptBR.errors.code.client_not_yours)).toBeInTheDocument();
  });

  it("asks before disconnecting and names the client", async () => {
    session("live");
    list();
    const disconnect = vi.spyOn(consoleApi, "disconnectCredential").mockResolvedValue();
    wrap(<ConsoleCredentialsPage/>);
    const mine = (await screen.findByText("cli_mine")).closest("li")!;
    await userEvent.click(within(mine).getByRole("button", {name: "Desconectar"}));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/cli_mine/)).toBeInTheDocument();
    expect(disconnect).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", {name: "Desconectar"}));
    expect(disconnect).toHaveBeenCalledWith(SCOPE, "cli_mine");
  });

  it("offers no connect while live is suspended", async () => {
    session("suspended");
    list();
    wrap(<ConsoleCredentialsPage/>);
    await screen.findByText("cli_mine");
    expect(screen.queryByRole("button", {name: "Conectar"})).toBeNull();
    expect(screen.getByText(ptBR.errors.code.tenant_suspended)).toBeInTheDocument();
  });
});
