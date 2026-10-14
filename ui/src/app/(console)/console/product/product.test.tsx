import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleProductPage from "@/app/(console)/console/product/page";
import * as consoleApi from "@/lib/api/console";
import {ConsoleScopeProvider} from "@/lib/console/scope";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("id=prod_dfe"),
  useRouter: () => ({push: vi.fn()}),
  usePathname: () => "/console/product",
}));

afterEach(() => vi.restoreAllMocks());

function wrap() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}><ConsoleProductPage/></ConsoleScopeProvider></QueryClientProvider>);
}

function product(prices: unknown[] = []) {
  return {
    id: "prod_dfe", name: "DF-e", active: true, livemode: true,
    dunning: {steps: [], custom: false}, prices,
  } as never;
}

describe("/console/product", () => {
  it("creates a price with a name, a recurrence, a timing and metadata", async () => {
    vi.spyOn(consoleApi, "getConsoleProduct").mockResolvedValue(product());
    const create = vi.spyOn(consoleApi, "createPrice").mockResolvedValue({} as never);
    wrap();

    await userEvent.click(await screen.findByRole("button", {name: "Novo preço"}));
    const form = await screen.findByRole("dialog");
    await userEvent.type(within(form).getByLabelText("Nome"), "DF-e Pro");
    await userEvent.type(within(form).getByLabelText("Valor por período"), "350");
    const count = within(form).getByLabelText("A cada");
    await userEvent.clear(count);
    await userEvent.type(count, "3");
    await userEvent.type(within(form).getByLabelText("Metadados"), "plan=pro{enter}quota_nfe=1200");
    await userEvent.click(within(form).getByRole("button", {name: "Criar"}));

    expect(create).toHaveBeenCalledTimes(1);
    expect(create.mock.calls[0][1]).toEqual({
      product_id: "prod_dfe",
      type: "fixed",
      unit_amount: 35000,
      recurrence: {interval: "month", count: 3},
      billing_timing: "advance",
      name: "DF-e Pro",
      metadata: {plan: "pro", quota_nfe: "1200"},
    });
  });

  it("names the line of a metadata mistake and holds the submit", async () => {
    vi.spyOn(consoleApi, "getConsoleProduct").mockResolvedValue(product());
    wrap();

    await userEvent.click(await screen.findByRole("button", {name: "Novo preço"}));
    const form = await screen.findByRole("dialog");
    await userEvent.type(within(form).getByLabelText("Valor por período"), "10");
    await userEvent.type(within(form).getByLabelText("Metadados"), "plan=pro{enter}semigual");

    expect(within(form).getByText("Linha 2: use chave=valor.")).toBeInTheDocument();
    expect(within(form).getByRole("button", {name: "Criar"})).toBeDisabled();
  });

  it("lists a named price by its name, with its id under it", async () => {
    vi.spyOn(consoleApi, "getConsoleProduct").mockResolvedValue(product([{
      id: "price_01X", name: "DF-e Pro", product_id: "prod_dfe", type: "fixed", currency: "BRL", unit_amount: 35000,
      recurrence: {interval: "month", count: 3}, billing_timing: "advance", archived: false,
    }]));
    wrap();

    expect(await screen.findByText("DF-e Pro")).toBeInTheDocument();
    expect(screen.getByText("price_01X")).toBeInTheDocument();
    expect(screen.getByText(/a cada 3 meses/)).toBeInTheDocument();
  });
});
