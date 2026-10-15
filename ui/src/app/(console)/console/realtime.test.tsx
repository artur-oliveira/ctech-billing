import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";
import {consoleKeys} from "@/lib/api/console";
import {FakeServer} from "@/lib/realtime/fakeServer";
import {FakeWebSocket} from "@/lib/realtime/fakeSocket";

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
const OTHER = "0190ffff-c3d4-7e5f-8a9b-0c1d2e3f4a5c";
const mode = vi.hoisted(() => ({value: "live" as "live" | "test"}));

vi.mock("next/navigation", () => ({usePathname: () => "/console/invoices", useRouter: () => ({replace: vi.fn()})}));
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => ({authenticated: true, loading: false, name: "Ana"})}));
vi.mock("@/lib/console/useMode", () => ({useMode: () => mode.value}));
vi.mock("@/lib/console/useMerchantOrg", () => ({useMerchantOrg: () => ORG}));
vi.mock("@/components/shell/BillingShell", () => ({
  BillingShell: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
  ShellContext: () => null,
  TestBanner: () => null,
}));
vi.mock("@/lib/api/console", async (original) => ({
  ...(await original<typeof import("@/lib/api/console")>()),
  getConsoleSession: async () => ({
    organizations: [{organization_id: ORG, selector: `org:${ORG}`, display_name: "Acme", role: "owner", merchant_status: "live", can_review_applications: false}],
    organizations_unavailable: false,
  }),
}));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  API_BASE_URL: "https://billing-api.aoctech.app",
}));

import ConsoleLayout from "./layout";

const jwt = `h.${btoa(JSON.stringify({sub: "u1"}))}.s`;
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

function mount() {
  const qc = new QueryClient({defaultOptions: {queries: {retry: false}}});
  const spy = vi.spyOn(qc, "invalidateQueries");
  const view = render(<QueryClientProvider client={qc}><ConsoleLayout params={Promise.resolve({})}>{<p>page</p>}</ConsoleLayout></QueryClientProvider>);
  return {qc, spy, view};
}

beforeEach(() => {
  vi.useFakeTimers({shouldAdvanceTime: true});
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "console");
  sessionStorage.clear();
  mode.value = "live";
  setAccessToken(jwt);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  setAccessToken(null);
});

describe("console realtime", () => {
  it("connects for the selected organization and mode, with the bearer", async () => {
    mount();
    await flush();
    expect(await screen.findByText("page")).toBeInTheDocument();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open());
    expect(server.auth?.space).toBe(`org:${ORG}`);
    expect(server.auth?.mode).toBe("live");
    expect(server.auth?.token).toBe(jwt);
  });

  it("an invoice change invalidates this organization's invoices, never another one's", async () => {
    const {spy} = mount();
    await flush();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open().ready().change("01A", "in_1", 1, "invoice.paid", "invoice"));
    await flush();
    const keys = spy.mock.calls.map(([f]) => JSON.stringify(f?.queryKey));
    expect(keys).toContain(JSON.stringify(consoleKeys.invoices({org: ORG, mode: "live"})));
    expect(keys.some(k => k.includes(OTHER))).toBe(false);
    expect(keys.some(k => k.includes('"test"'))).toBe(false);
  });

  it("switching mode closes the socket and reconnects with the new mode and no cursor", async () => {
    const {view, qc} = mount();
    await flush();
    const first = FakeWebSocket.last;
    const server = new FakeServer(first);
    act(() => void server.open().ready().change("01A", "in_1", 1));
    await flush();
    mode.value = "test";
    view.rerender(<QueryClientProvider client={qc}><ConsoleLayout params={Promise.resolve({})}>{<p>page</p>}</ConsoleLayout></QueryClientProvider>);
    await flush();
    expect(first.readyState).toBe(FakeWebSocket.CLOSED);
    const next = new FakeServer(FakeWebSocket.last);
    act(() => void next.open());
    expect(next.auth?.mode).toBe("test");
    expect(next.auth?.resume_cursor ?? "").toBe("");
  });
});
