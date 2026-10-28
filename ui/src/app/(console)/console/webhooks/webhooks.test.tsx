import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type {ReactNode} from "react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import ConsoleWebhooksPage from "@/app/(console)/console/webhooks/page";
import * as consoleApi from "@/lib/api/console";
import type {ConsoleWebhookEndpoint} from "@/lib/api/consoleTypes";
import {ConsoleScopeProvider} from "@/lib/console/scope";
import {clearPendingCreate, requestCreate} from "@/lib/createRequest";
import ptBR from "@/locales/pt-BR";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;
const SECRET = `whsec_${"ab".repeat(32)}`;

const ep = (id: string, extra: Partial<ConsoleWebhookEndpoint> = {}): ConsoleWebhookEndpoint =>
  ({id, url: `https://example.com/${id}`, events: [], status: "active", failure_run: 0, provisioned: false, added_at: "2026-10-10T12:00:00Z", ...extra});

function list(data: ConsoleWebhookEndpoint[]) {
  vi.spyOn(consoleApi, "listConsoleWebhooks").mockResolvedValue({data, event_types: ["invoice.paid", "subscription.canceled"], max_endpoints: 5});
}

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return {client, ...render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>)};
}

beforeEach(() => {
  vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({
    organizations: [{organization_id: SCOPE.org, selector: `org:${SCOPE.org}`, display_name: "CTech", role: "owner", merchant_status: "live"}],
    organizations_unavailable: false,
  });
});
afterEach(() => {
  vi.restoreAllMocks();
  clearPendingCreate();
});

describe("/console/webhooks", () => {
  it("shows the new secret once, and forgets it when closed", async () => {
    list([]);
    const create = vi.spyOn(consoleApi, "createWebhook").mockResolvedValue({...ep("whe_1"), secret: SECRET});
    wrap(<ConsoleWebhooksPage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Novo endpoint"}));
    const form = await screen.findByRole("dialog");
    await userEvent.type(within(form).getByLabelText("URL"), "https://example.com/hooks");
    await userEvent.click(within(form).getByRole("button", {name: "Adicionar"}));
    expect(create).toHaveBeenCalledWith(SCOPE, {url: "https://example.com/hooks", events: []});
    expect(await screen.findByDisplayValue(SECRET)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", {name: "Já copiei"}));
    expect(screen.queryByDisplayValue(SECRET)).toBeNull();
  });

  it("sends only the chosen events when 'Todos os eventos' is off", async () => {
    list([]);
    const create = vi.spyOn(consoleApi, "createWebhook").mockResolvedValue({...ep("whe_1"), secret: SECRET});
    wrap(<ConsoleWebhooksPage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Novo endpoint"}));
    const form = await screen.findByRole("dialog");
    await userEvent.type(within(form).getByLabelText("URL"), "https://example.com/hooks");
    await userEvent.click(within(form).getByRole("switch", {name: "Todos os eventos"}));
    await userEvent.click(within(form).getByRole("checkbox", {name: /invoice\.paid/}));
    await userEvent.click(within(form).getByRole("button", {name: "Adicionar"}));
    expect(create).toHaveBeenCalledWith(SCOPE, {url: "https://example.com/hooks", events: ["invoice.paid"]});
  });

  it("disables 'Novo endpoint' at the limit and says why", async () => {
    list([ep("a"), ep("b"), ep("c"), ep("d"), ep("e")]);
    wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByRole("button", {name: "Novo endpoint"})).toBeDisabled();
    expect(screen.getByText(/Limite de 5 endpoints/)).toBeInTheDocument();
  });

  it("tests at once, and rotates only after confirming", async () => {
    list([ep("whe_1")]);
    const test = vi.spyOn(consoleApi, "testWebhook").mockResolvedValue({event_id: "evt_1"});
    const rotate = vi.spyOn(consoleApi, "rotateWebhookSecret").mockResolvedValue({...ep("whe_1"), secret: SECRET});
    wrap(<ConsoleWebhooksPage/>);
    const row = (await screen.findByText("https://example.com/whe_1")).closest("li")!;
    await userEvent.click(within(row).getByRole("button", {name: "Testar"}));
    expect(test).toHaveBeenCalledWith(SCOPE, "whe_1");
    await userEvent.click(within(row).getAllByRole("button", {name: "Trocar segredo"})[0]);
    expect(rotate).not.toHaveBeenCalled();
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: "Trocar segredo"}));
    expect(rotate).toHaveBeenCalledWith(SCOPE, "whe_1");
    expect(await screen.findByDisplayValue(SECRET)).toBeInTheDocument();
  });

  it("lists a provisioned endpoint with no action", async () => {
    list([ep("whe_dfe", {provisioned: true, added_at: undefined, owner_key: "dfe"})]);
    wrap(<ConsoleWebhooksPage/>);
    const row = (await screen.findByText("https://example.com/whe_dfe")).closest("li")!;
    expect(within(row).getByText("Gerenciado pela CTech")).toBeInTheDocument();
    expect(within(row).queryByRole("button")).toBeNull();
  });

  it("says why a URL was refused, under the URL field", async () => {
    list([]);
    vi.spyOn(consoleApi, "createWebhook").mockRejectedValue({response: {status: 422, data: {
      type: "about:blank", status: 422, title: "T", code: "validation_error",
      errors: [{field: "url", code: "webhook_host_not_public", message: "the host must be on the public internet"}],
    }}});
    wrap(<ConsoleWebhooksPage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Novo endpoint"}));
    const form = await screen.findByRole("dialog");
    await userEvent.type(within(form).getByLabelText("URL"), "https://localhost/hooks");
    await userEvent.click(within(form).getByRole("button", {name: "Adicionar"}));
    expect(await within(form).findByText(ptBR.errors.field.webhook_host_not_public)).toBeInTheDocument();
    expect(within(form).queryByText(/\{\{/)).toBeNull();
  });

  it("opens the create form when the search asks for it, once the list is known", async () => {
    list([ep("a")]);
    requestCreate("webhook");
    wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("does not open it at the limit, even when asked", async () => {
    list([ep("a"), ep("b"), ep("c"), ep("d"), ep("e")]);
    requestCreate("webhook");
    wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByRole("button", {name: "Novo endpoint"})).toBeDisabled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("a request refused at the limit is dropped: freeing a slot later does not open the form", async () => {
    list([ep("a"), ep("b"), ep("c"), ep("d"), ep("e")]);
    requestCreate("webhook");
    const {client} = wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByRole("button", {name: "Novo endpoint"})).toBeDisabled();
    list([ep("a"), ep("b"), ep("c"), ep("d")]);
    await client.invalidateQueries();
    await vi.waitFor(() => expect(screen.getByRole("button", {name: "Novo endpoint"})).toBeEnabled());
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("says whether each endpoint answers: untested, responding or failing with the reason", async () => {
    list([
      ep("whe_new"),
      ep("whe_ok", {last_delivery_at: "2026-10-12T10:00:00Z", last_delivery_ok: true}),
      ep("whe_bad", {last_delivery_at: "2026-10-12T10:00:00Z", last_delivery_ok: false, last_delivery_detail: "HTTP 404"}),
    ]);
    wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByText(/Ainda não testado/)).toBeInTheDocument();
    expect(screen.getByText("Respondendo")).toBeInTheDocument();
    expect(screen.getByText("Não está respondendo: HTTP 404")).toBeInTheDocument();
  });
});

describe("/console/webhooks, many endpoints", () => {
  const dead = (id: string, extra: Partial<ConsoleWebhookEndpoint> = {}) =>
    ep(id, {status: "disabled", last_delivery_at: "2026-10-12T10:00:00Z", last_delivery_ok: false, last_delivery_detail: "HTTP 410", ...extra});

  it("leads with the active endpoint and keeps the disabled ones behind a collapsed group, URL first", async () => {
    list([dead("d1"), dead("d2"), ep("live")]);
    wrap(<ConsoleWebhooksPage/>);
    const items = (await screen.findAllByRole("listitem")).map(li => li.textContent ?? "");
    expect(items[0]).toContain("https://example.com/live");
    expect(items[1]).toContain("https://example.com/d1");
    const group = screen.getByText("2 endpoints desativados").closest("details")!;
    expect(group).not.toHaveAttribute("open");
    expect(within(group).getAllByText(/Última falha em .*: HTTP 410/)).toHaveLength(2);
  });

  it("deletes the selected endpoints one by one after confirming, never a provisioned one", async () => {
    list([dead("d1"), dead("d2"), dead("sys", {provisioned: true, added_at: undefined})]);
    const del = vi.spyOn(consoleApi, "deleteWebhook").mockResolvedValue(undefined);
    wrap(<ConsoleWebhooksPage/>);
    await userEvent.click(await screen.findByRole("checkbox", {name: "Selecionar todos os desativados"}));
    expect(screen.queryByRole("checkbox", {name: /example.com\/sys/})).toBeNull();
    await userEvent.click(screen.getByRole("button", {name: "Excluir selecionados (2)"}));
    expect(del).not.toHaveBeenCalled();
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: "Excluir"}));
    await vi.waitFor(() => expect(del).toHaveBeenCalledTimes(2));
    expect(del.mock.calls.map(c => c[1])).toEqual(["d1", "d2"]);
  });

  it("explains a full list instead of leaving only a greyed button", async () => {
    list([ep("a"), dead("b"), dead("c"), dead("d"), dead("e"), dead("f")]);
    wrap(<ConsoleWebhooksPage/>);
    expect(await screen.findByText(/O limite conta todos os endpoints/)).toBeInTheDocument();
  });
});
