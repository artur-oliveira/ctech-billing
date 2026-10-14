import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleCatalogPage from "@/app/(console)/console/catalog/page";
import * as consoleApi from "@/lib/api/console";
import {ConsoleScopeProvider} from "@/lib/console/scope";
import {clearPendingCreate, requestCreate} from "@/lib/createRequest";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;

function wrap() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}><ConsoleCatalogPage/></ConsoleScopeProvider></QueryClientProvider>);
}

afterEach(() => {
  vi.restoreAllMocks();
  clearPendingCreate();
});

describe("/console/catalog", () => {
  it("opens the new-product form when the search asks for it", async () => {
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({data: [], has_more: false} as never);
    requestCreate("product");
    wrap();
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("stays closed without a request", async () => {
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({data: [], has_more: false} as never);
    wrap();
    await screen.findAllByRole("button", {name: "Novo plano"});
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("lists the prices of each plan, the cheapest one, the product and its link", async () => {
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({
      data: [{
        id: "prod_dfe_pro", name: "DF-e Pro", active: true, owner_key: "dfe", livemode: true,
        description: "Emissão de notas", url: "https://dfe.aoctech.app",
        prices: [
          {id: "p1", unit_amount: 19900, archived: false},
          {id: "p2", unit_amount: 9900, archived: false},
          {id: "p3", unit_amount: 100, archived: true},
        ],
      }],
      has_more: false,
    } as never);
    wrap();
    expect(await screen.findByText("DF-e")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText(/99,00/)).toBeInTheDocument();
    expect(screen.getByText("Emissão de notas")).toBeInTheDocument();
    expect(screen.getByRole("link", {name: "dfe.aoctech.app"})).toHaveAttribute("href", "https://dfe.aoctech.app");
  });
});
