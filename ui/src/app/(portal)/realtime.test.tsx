import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";
import {portalKeys} from "@/lib/api/portal";
import {FakeServer} from "@/lib/realtime/fakeServer";
import {FakeWebSocket} from "@/lib/realtime/fakeSocket";

const auth = vi.hoisted(() => ({authenticated: true, loading: false}));

vi.mock("next/navigation", () => ({usePathname: () => "/invoices", useRouter: () => ({replace: vi.fn()})}));
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => auth}));
vi.mock("@/components/shell/BillingShell", () => ({BillingShell: ({children}: {children: React.ReactNode}) => <div>{children}</div>}));
vi.mock("@/components/portal/PortalSpaceSwitch", () => ({PortalSpaceSwitch: () => null}));
vi.mock("@/components/portal/PortalBottomNav", () => ({PortalBottomNav: () => null}));
vi.mock("@/lib/api/portal", async (original) => ({
  ...(await original<typeof import("@/lib/api/portal")>()),
  getSession: async () => ({customer_id: "cus_1", name: "Ana", email: "a@b.c", terms_accepted: true}),
}));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  API_BASE_URL: "https://billing-api.aoctech.app",
}));

import PortalLayout from "./layout";

const jwt = `h.${btoa(JSON.stringify({sub: "u1"}))}.s`;
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

function mount() {
  const qc = new QueryClient({defaultOptions: {queries: {retry: false}}});
  const spy = vi.spyOn(qc, "invalidateQueries");
  render(<QueryClientProvider client={qc}><PortalLayout params={Promise.resolve({})}><p>page</p></PortalLayout></QueryClientProvider>);
  return spy;
}

beforeEach(() => {
  vi.useFakeTimers({shouldAdvanceTime: true});
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "portal");
  sessionStorage.clear();
  auth.authenticated = true;
  setAccessToken(jwt);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  setAccessToken(null);
});

describe("portal realtime", () => {
  it("opens with the bearer only, no space", async () => {
    mount();
    await flush();
    expect(await screen.findByText("page")).toBeInTheDocument();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open());
    expect(server.auth?.token).toBe(jwt);
    expect(server.auth?.space ?? "").toBe("");
  });

  it("invoice and subscription changes invalidate the portal's lists", async () => {
    const spy = mount();
    await flush();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open().ready()
      .change("01A", "in_1", 1, "invoice.paid", "invoice")
      .change("01B", "sub_1", 1, "subscription.updated", "subscription"));
    await flush();
    const keys = spy.mock.calls.map(([f]) => JSON.stringify(f?.queryKey));
    expect(keys).toContain(JSON.stringify(portalKeys.invoices));
    expect(keys).toContain(JSON.stringify(portalKeys.subscriptions));
  });

  it("opens no socket when nobody is signed in", async () => {
    auth.authenticated = false;
    setAccessToken(null);
    mount();
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(0);
  });
});
