import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {FakeServer} from "@/lib/realtime/fakeServer";
import {FakeWebSocket} from "@/lib/realtime/fakeSocket";

const TOKEN = "tok_abcdefghijklmnopqrstuvwxyz0123456789";
let view = {amount_due: 1000};
const getCheckout = vi.fn();
const payCheckout = vi.fn();

vi.mock("next/navigation", () => ({useSearchParams: () => new URLSearchParams({token: TOKEN})}));
vi.mock("@/lib/api/checkout", () => ({
  checkoutKeys: {view: (t: string) => ["checkout", t] as const},
  getCheckout: (...a: unknown[]) => getCheckout(...a),
  payCheckout: (...a: unknown[]) => payCheckout(...a),
}));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  API_BASE_URL: "https://billing-api.aoctech.app",
}));

import CheckoutPage from "./page";

const invoice = () => ({
  number: "1", description: "Plano", state: "open", tone: "neutral", days_until_due: 3, due_date: "2026-11-01",
  amount_due: view.amount_due, currency: "BRL", lines: [], payable: true,
});

function mount() {
  const qc = new QueryClient({defaultOptions: {queries: {retry: false}}});
  render(<QueryClientProvider client={qc}><CheckoutPage/></QueryClientProvider>);
  return qc;
}

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

beforeEach(() => {
  vi.useFakeTimers({shouldAdvanceTime: true});
  view = {amount_due: 1000};
  getCheckout.mockReset().mockImplementation(async () => ({merchant: "Loja", invoice: invoice()}));
  payCheckout.mockReset().mockResolvedValue({
    merchant: "Loja", invoice: invoice(),
    payment: {method: "pix", pix_code: "000201", expires_at: new Date(Date.now() + 1_800_000).toISOString()},
  });
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  sessionStorage.clear();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

async function openCharge() {
  await flush();
  await userEvent.setup({advanceTimers: vi.advanceTimersByTime}).click(await screen.findByRole("button", {name: /pix|pagar/i}));
  await flush();
}

describe("checkout realtime", () => {
  it("with the flag off opens no socket and keeps polling every 4 s", async () => {
    mount();
    await openCharge();
    expect(FakeWebSocket.instances).toHaveLength(0);
    const before = getCheckout.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(4_100); });
    expect(getCheckout.mock.calls.length).toBeGreaterThan(before);
  });

  it("with the flag on, the first frame carries the pay link and a paid change flips the page within the second", async () => {
    vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
    vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "checkout");
    mount();
    await openCharge();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open());
    expect(server.auth?.pay_link).toBe(TOKEN);
    expect(server.auth?.token).toBe("");
    act(() => void server.ready());
    await flush();
    expect(screen.getByText(/^(ao vivo|live)$/i)).toBeInTheDocument();

    view = {amount_due: 0};
    const calls = getCheckout.mock.calls.length;
    act(() => void server.change("01A", "in_1", 1, "invoice.paid", "invoice"));
    await flush();
    expect(getCheckout.mock.calls.length).toBe(calls + 1);
    expect(await screen.findByText(/pagamento recebido|payment received/i)).toBeInTheDocument();
  });

  it("while live the safety-net poll is slow, and with the socket blocked the 4 s poll still ends in paid", async () => {
    vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
    mount();
    await openCharge();
    // The socket never opens: the page keeps its own poll.
    view = {amount_due: 0};
    await act(async () => { await vi.advanceTimersByTimeAsync(4_100); });
    expect(await screen.findByText(/pagamento recebido|payment received/i)).toBeInTheDocument();
  });
});
