import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen} from "@testing-library/react";
import type {ReactNode} from "react";
import {afterEach, describe, expect, it, vi} from "vitest";

import ConsoleInvoicesPage from "@/app/(console)/console/invoices/page";
import ConsoleSubscriptionsPage from "@/app/(console)/console/subscriptions/page";
import * as consoleApi from "@/lib/api/console";
import {ConsoleScopeProvider} from "@/lib/console/scope";

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;

afterEach(() => vi.restoreAllMocks());

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>);
}

// The console's periods are the domain's (period_start/period_end), exactly as
// production answered on 2026-10-10; the screens read `start`/`end` and threw.
describe("console periods", () => {
  it("lists an invoice with the API's period", async () => {
    vi.spyOn(consoleApi, "listConsoleInvoices").mockResolvedValue({
      data: [{
        id: "in_1", number: 8, customer_id: "cus_1", subscription_id: "sub_1", status: "PAID", overdue: false,
        period: {period_start: "2026-10-10", period_end: "2026-11-10"}, due_date: "2026-10-13", currency: "BRL",
        subtotal: 0, discount: 0, total: 0, amount_paid: 0, amount_due: 0, attempt_count: 0, livemode: true,
        customer_name: "A O CARVALHO TECH",
      }],
      has_more: false,
    } as unknown as Awaited<ReturnType<typeof consoleApi.listConsoleInvoices>>);
    wrap(<ConsoleInvoicesPage/>);
    expect(await screen.findByText("A O CARVALHO TECH")).toBeInTheDocument();
    expect(screen.getByText(/10\/10(\/26)? • 10\/11(\/26)?/)).toBeInTheDocument();
  });

  it("lists a subscription with the API's current period", async () => {
    vi.spyOn(consoleApi, "listConsoleSubscriptions").mockResolvedValue({
      data: [{
        id: "sub_1", customer_id: "cus_1", status: "ACTIVE", entitled: true, recurrence: "monthly",
        billing_timing: "in_advance", anchor: "2026-10-10",
        current_period: {period_start: "2026-10-10", period_end: "2026-11-10"},
        cancel_at_period_end: false, livemode: true,
      }],
      has_more: false,
    } as unknown as Awaited<ReturnType<typeof consoleApi.listConsoleSubscriptions>>);
    wrap(<ConsoleSubscriptionsPage/>);
    // Twice: the table, and the list a phone gets instead.
    expect(await screen.findAllByText(/10\/10(\/26)? • 10\/11(\/26)?/)).toHaveLength(2);
  });
});
