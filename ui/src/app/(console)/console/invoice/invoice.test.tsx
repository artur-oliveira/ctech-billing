import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleInvoicePage from "@/app/(console)/console/invoice/page";
import * as consoleApi from "@/lib/api/console";
import type {ConsoleInvoice, ConsoleInvoiceDetail} from "@/lib/api/consoleTypes";
import {ConsoleScopeProvider} from "@/lib/console/scope";

vi.mock("next/navigation", () => ({useSearchParams: () => new URLSearchParams("id=inv_1"), useRouter: () => ({push: vi.fn()})}));

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;
const ZERO = "0001-01-01T00:00:00Z";

function show(over: Partial<ConsoleInvoiceDetail>) {
  const invoice = {
    id: "inv_1", number: "INV-1", status: "PAID", total: 10000, amount_paid: 10000, amount_due: 0, currency: "BRL",
    customer_id: "cus_1", attempt_count: 1, lines: [], period: {period_start: "2026-10-01", period_end: "2026-10-31"}, due_date: "2026-10-10",
  } as unknown as ConsoleInvoice;
  vi.spyOn(consoleApi, "getConsoleInvoice").mockResolvedValue({invoice, credited: 0, fully_credited: false, timeline: [], ...over});
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}><ConsoleInvoicePage/></ConsoleScopeProvider></QueryClientProvider>);
}

afterEach(() => vi.restoreAllMocks());

describe("/console/invoice", () => {
  it("disables 'Nota de crédito' and says why when nothing is left to credit", async () => {
    show({credited: 10000, fully_credited: true});
    const button = await screen.findByRole("button", {name: "Nota de crédito"});
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription("Nada mais a creditar");
  });

  it("keeps 'Nota de crédito' enabled while something is left", async () => {
    show({credited: 4000});
    expect(await screen.findByRole("button", {name: "Nota de crédito"})).toBeEnabled();
  });

  it("renders a dash, not 1901, for a credit note and an audit entry without a date", async () => {
    show({
      credited: 1000,
      credit_notes: [{id: "cn_1", invoice_id: "inv_1", amount: 1000, currency: "BRL", reason: "x", refunded_externally: false, created_by: "user:1", created_at: ZERO}],
      timeline: [{id: "log_1", action: "invoice.paid", actor: "scheduler", created_at: ZERO}],
    });
    await screen.findByText("x");
    expect(screen.getByText(/user:1 · —/)).toBeInTheDocument();
    expect(screen.getByText("—", {selector: "time"})).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/1901|0001/);
  });
});
