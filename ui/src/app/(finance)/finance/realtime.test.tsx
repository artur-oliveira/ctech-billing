import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";
import {financeKeys} from "@/lib/api/finance";
import {PERSONAL, type Space} from "@/lib/console/space";
import {FakeServer} from "@/lib/realtime/fakeServer";
import {FakeWebSocket} from "@/lib/realtime/fakeSocket";

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
const state = vi.hoisted(() => ({space: {kind: "personal"} as Space}));

vi.mock("next/navigation", () => ({useRouter: () => ({replace: vi.fn()})}));
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => ({authenticated: true, loading: false})}));
vi.mock("@/lib/console/useMode", () => ({useMode: () => "live"}));
vi.mock("@/lib/console/useSpace", () => ({useSpace: () => state.space}));
vi.mock("@/components/shell/BillingShell", () => ({
  BillingShell: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
  ShellContext: () => null,
  TestBanner: () => null,
}));
vi.mock("@/components/finance/SpaceSwitch", () => ({SpaceSwitch: () => null}));
vi.mock("@/components/finance/FinanceNav", () => ({SpaceFinanceNav: () => null}));
vi.mock("@/components/finance/FinanceBottomNav", () => ({FinanceBottomNav: () => null}));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  API_BASE_URL: "https://billing-api.aoctech.app",
}));

import {FinanceShell} from "@/components/finance/FinanceShell";

const jwt = `h.${btoa(JSON.stringify({sub: "u1"}))}.s`;
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

function tree(qc: QueryClient) {
  return <QueryClientProvider client={qc}><FinanceShell><p>page</p></FinanceShell></QueryClientProvider>;
}

beforeEach(() => {
  vi.useFakeTimers({shouldAdvanceTime: true});
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "finance");
  sessionStorage.clear();
  state.space = PERSONAL;
  setAccessToken(jwt);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  setAccessToken(null);
});

describe("finance realtime", () => {
  it("a posted statement line refetches the space's finance data", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    render(tree(qc));
    await flush();
    expect(screen.getByText("page")).toBeInTheDocument();
    const server = new FakeServer(FakeWebSocket.last);
    act(() => void server.open().ready().change("01A", "line_1", 1, "statement.line_posted", "statement"));
    await flush();
    expect(spy.mock.calls.map(([f]) => JSON.stringify(f?.queryKey))).toContain(JSON.stringify(financeKeys.all("live", PERSONAL)));
  });

  it("switching space closes the socket and reconnects for the new space with no cursor", async () => {
    const qc = new QueryClient();
    const view = render(tree(qc));
    await flush();
    const first = FakeWebSocket.last;
    act(() => void new FakeServer(first).open().ready().change("01A", "line_1", 1, "statement.line_posted", "statement"));
    await flush();
    state.space = {kind: "organization", organizationId: ORG} as Space;
    view.rerender(tree(qc));
    await flush();
    expect(first.readyState).toBe(FakeWebSocket.CLOSED);
    const next = new FakeServer(FakeWebSocket.last);
    act(() => void next.open());
    expect(next.auth?.space).toBe(`org:${ORG}`);
    expect(next.auth?.resume_cursor ?? "").toBe("");
  });

  it("a change for another space does not touch this one's data", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    state.space = {kind: "organization", organizationId: ORG} as Space;
    render(tree(qc));
    await flush();
    act(() => void new FakeServer(FakeWebSocket.last).open().ready().change("01A", "line_1", 1, "statement.line_posted", "statement"));
    await flush();
    const keys = spy.mock.calls.map(([f]) => JSON.stringify(f?.queryKey));
    expect(keys.some(k => k.includes("personal"))).toBe(false);
  });
});
