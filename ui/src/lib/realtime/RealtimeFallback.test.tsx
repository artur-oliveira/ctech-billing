import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";

import {FakeWebSocket} from "./fakeSocket";
import {RealtimeProvider} from "./RealtimeProvider";
import {useRealtimeStatus} from "./useRealtimeStatus";
import {ServerFrame} from "./wire/realtime";

const ORG = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const surface = {kind: "console", scope: {org: ORG, mode: "live"}} as const;

const server = (body: NonNullable<ServerFrame["body"]>) => ServerFrame.encode({body}).finish();
const ready = server({$case: "ready", value: {protocol_version: 1, server_time_ms: 1, scope_kinds: ["tenant"]}});

function Status() {
  return <p data-testid="status">{useRealtimeStatus()}</p>;
}

function mount() {
  const qc = new QueryClient();
  render(
    <QueryClientProvider client={qc}>
      <RealtimeProvider surface={surface} principal="u1"><Status/></RealtimeProvider>
    </QueryClientProvider>,
  );
  return qc;
}

const status = () => screen.getByTestId("status").textContent;
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });

function stubChanges(handler: () => Response | Promise<Response>) {
  const fetchMock = vi.fn(async () => handler());
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const ok = (body: object) => new Response(JSON.stringify({changes: [], head: "", more: false, ...body}), {status: 200});

beforeEach(() => {
  vi.useFakeTimers();
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "");
  sessionStorage.clear();
  setAccessToken("tok-1");
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  setAccessToken(null);
});

describe("the fallback ladder", () => {
  it("polls the cursor route once the socket has been down for 15 s, and not before", async () => {
    const fetchMock = stubChanges(() => ok({}));
    mount(); // the socket never opens
    await advance(14_000);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(status()).not.toBe("polling");

    await advance(2_000);
    expect(status()).toBe("polling");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain("/v1.0/realtime/changes");
    expect(url).not.toMatch(/token|access/i);
    expect(init.headers).toMatchObject({Authorization: "Bearer tok-1", "X-Billing-Space": `org:${ORG}`, "X-Billing-Mode": "live"});

    await advance(10_000);
    expect(fetchMock).toHaveBeenCalledTimes(2); // every 10 s
  });

  it("stops polling the moment the socket is live again", async () => {
    const fetchMock = stubChanges(() => ok({}));
    mount();
    await advance(16_000);
    expect(status()).toBe("polling");
    const before = fetchMock.mock.calls.length;

    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.receive(ready));
    expect(status()).toBe("live");
    await advance(60_000);
    expect(fetchMock.mock.calls.length).toBe(before);
  });

  it("polls beside an open socket when the server asks (a Hint), at its own interval", async () => {
    const fetchMock = stubChanges(() => ok({}));
    mount();
    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.receive(ready));
    expect(fetchMock).not.toHaveBeenCalled();
    act(() => FakeWebSocket.last.receive(server({$case: "hint", value: {poll_interval_ms: 4_000}})));
    expect(status()).toBe("polling");
    await advance(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await advance(4_000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(FakeWebSocket.last.readyState).toBe(FakeWebSocket.OPEN); // the socket stays
  });

  it("a 401 on the cursor route waits for a new token instead of polling on", async () => {
    const fetchMock = stubChanges(() => new Response("{}", {status: 401}));
    mount();
    await advance(16_000);
    expect(status()).toBe("waiting-token");
    const calls = fetchMock.mock.calls.length;
    await advance(60_000);
    expect(fetchMock.mock.calls.length).toBe(calls);
  });

  it("with the network gone too, nothing throws and it keeps trying", async () => {
    const fetchMock = stubChanges(() => { throw new TypeError("Failed to fetch"); });
    mount();
    await advance(16_000);
    await advance(30_000);
    expect(status()).toBe("polling");
    expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(3);
  });

  it("applies what the cursor route returns, through the same binding", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    stubChanges(() => ok({
      changes: [{id: "01B", type: "invoice.paid", entity: "invoice", entity_id: "in_1", status: "PAID", revision: 1, occurred_at_ms: 1}],
      head: "01B",
    }));
    render(
      <QueryClientProvider client={qc}>
        <RealtimeProvider surface={surface} principal="u1"><Status/></RealtimeProvider>
      </QueryClientProvider>,
    );
    await advance(16_000);
    expect(spy).toHaveBeenCalled();
  });
});
