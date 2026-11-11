import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type {ReactNode} from "react";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleCatalogPage from "@/app/(console)/console/catalog/page";
import ConsoleCustomersPage from "@/app/(console)/console/customers/page";
import ConsoleInvoicePage from "@/app/(console)/console/invoice/page";
import ConsoleProductPage from "@/app/(console)/console/product/page";
import ConsoleSubscriptionsPage from "@/app/(console)/console/subscriptions/page";
import * as consoleApi from "@/lib/api/console";
import type {ConsoleInvoice, ConsoleSubscriptionRow} from "@/lib/api/consoleTypes";
import {ConsoleScopeProvider} from "@/lib/console/scope";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;

let search = "id=inv_1";
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(search),
  useRouter: () => ({push: vi.fn()}),
  usePathname: () => "/console",
}));

function phone(on: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: on, media: query, addEventListener: () => undefined, removeEventListener: () => undefined,
  }));
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  search = "id=inv_1";
});

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>);
}

const sub = (id: string): ConsoleSubscriptionRow => ({
  id, customer_id: "cus_1", status: "ACTIVE", entitled: true, recurrence: {interval: "month", count: 1},
  billing_timing: "advance", anchor: "2026-10-10", current_period: {period_start: "2026-10-10", period_end: "2026-11-10"},
  cancel_at_period_end: false, livemode: true, customer_name: "Cliente", plan_name: `Plano ${id}`,
});

describe("list cursors", () => {
  it("subscriptions: 'Mostrar mais' follows the cursor and appends", async () => {
    const list = vi.spyOn(consoleApi, "listConsoleSubscriptions")
      .mockResolvedValueOnce({data: [sub("a")], has_more: true, cursor: "c1"})
      .mockResolvedValueOnce({data: [sub("b")], has_more: false});
    wrap(<ConsoleSubscriptionsPage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Mostrar mais"}));
    expect(list).toHaveBeenLastCalledWith(SCOPE, "c1");
    expect((await screen.findAllByText("Plano b")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Plano a").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", {name: "Mostrar mais"})).toBeNull();
  });

  it("customers: 'Mostrar mais' follows the cursor and appends", async () => {
    const c = (id: string) => ({id, name: `Cli ${id}`, email: "", livemode: true});
    const list = vi.spyOn(consoleApi, "listConsoleCustomers")
      .mockResolvedValueOnce({data: [c("a")], has_more: true, cursor: "c1"} as never)
      .mockResolvedValueOnce({data: [c("b")], has_more: false} as never);
    wrap(<ConsoleCustomersPage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Mostrar mais"}));
    expect(list).toHaveBeenLastCalledWith(SCOPE, "c1");
    expect((await screen.findAllByText("Cli b")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", {name: "Mostrar mais"})).toBeNull();
  });
});

describe("invoice", () => {
  const draft = {
    id: "inv_1", number: "", status: "DRAFT", total: 5000, amount_paid: 0, amount_due: 5000, currency: "BRL",
    customer_id: "cus_1", attempt_count: 0, period: {period_start: "2026-10-01", period_end: "2026-10-31"}, due_date: "2026-10-10",
    lines: [{description: "Plano Pro", quantity: 2, unit_amount: 2500, amount: 5000, proration: true,
      period: {period_start: "2026-10-01", period_end: "2026-10-31"}}],
  } as unknown as ConsoleInvoice;

  it("asks before issuing and only then calls the API", async () => {
    vi.spyOn(consoleApi, "getConsoleInvoice").mockResolvedValue({invoice: draft, credited: 0, fully_credited: false, timeline: []});
    const issue = vi.spyOn(consoleApi, "finalizeInvoice").mockResolvedValue({invoice: {...draft, status: "OPEN"}, credited: 0, fully_credited: false, timeline: []});
    wrap(<ConsoleInvoicePage/>);
    await userEvent.click(await screen.findByRole("button", {name: "Emitir"}));
    expect(issue).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", {name: "Emitir"}));
    expect(issue).toHaveBeenCalledWith(SCOPE, "inv_1");
  });

  it("lays the lines out as a list on a phone, proration named", async () => {
    phone(true);
    vi.spyOn(consoleApi, "getConsoleInvoice").mockResolvedValue({invoice: draft, credited: 0, fully_credited: false, timeline: []});
    wrap(<ConsoleInvoicePage/>);
    const list = await screen.findByRole("list", {name: "Itens"});
    expect(within(list).getByText("Plano Pro")).toBeInTheDocument();
    expect(within(list).getByText(/proporcional/)).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });
});

describe("product prices", () => {
  const product = {
    id: "prod_1", name: "DF-e", active: true, livemode: true, dunning: {steps: [], custom: false},
    prices: [
      {id: "price_1", name: "Pro", product_id: "prod_1", type: "fixed", currency: "BRL", unit_amount: 9900,
        recurrence: {interval: "month", count: 1}, billing_timing: "advance", archived: false},
      {id: "price_0", name: "Antigo", product_id: "prod_1", type: "fixed", currency: "BRL", unit_amount: 4900,
        recurrence: {interval: "month", count: 1}, billing_timing: "advance", archived: true},
    ],
  } as never;

  it("on a phone shows versions as a list, archive asks first and names the price", async () => {
    phone(true);
    search = "id=prod_1";
    vi.spyOn(consoleApi, "getConsoleProduct").mockResolvedValue(product);
    const archive = vi.spyOn(consoleApi, "archivePrice").mockResolvedValue({} as never);
    wrap(<ConsoleProductPage/>);
    const list = await screen.findByRole("list", {name: "Preços"});
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(within(list).getByText(/arquivado/)).toBeInTheDocument();
    expect(within(list).getAllByRole("button", {name: /Arquivar/})).toHaveLength(1);
    await userEvent.click(within(list).getByRole("button", {name: "Arquivar Pro"}));
    expect(archive).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", {name: "Arquivar"}));
    expect(archive).toHaveBeenCalledWith(SCOPE, "price_1");
  });
});

describe("catalog on a phone", () => {
  it("is a list: name, cheapest price, count of active prices", async () => {
    phone(true);
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({
      data: [{id: "p", name: "DF-e Pro", active: true, owner_key: "dfe", livemode: true,
        prices: [{id: "a", unit_amount: 9900, archived: false}, {id: "b", unit_amount: 100, archived: true}]}],
      has_more: false,
    } as never);
    wrap(<ConsoleCatalogPage/>);
    const list = await screen.findByRole("list", {name: "Catálogo"});
    expect(within(list).getByRole("link", {name: "DF-e Pro"})).toHaveAttribute("href", "/console/product?id=p");
    expect(within(list).getByText(/a partir de .*99,00/)).toBeInTheDocument();
    expect(within(list).getByText(/1 preço ativo/)).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });
});
