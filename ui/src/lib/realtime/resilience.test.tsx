import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider, useQuery} from "@tanstack/react-query";
import {act, render} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";
import {consoleKeys} from "@/lib/api/console";

import type {ChangeView} from "./codec";
import {RealtimeCore} from "./core";
import {createCursorStore, type KeyValueStorage} from "./cursor";
import {FakeServer} from "./fakeServer";
import {FakeWebSocket} from "./fakeSocket";
import {RealtimeProvider} from "./RealtimeProvider";

const ORG = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const scope = {org: ORG, mode: "live"} as const;
const surface = {kind: "console", scope} as const;
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
const server = () => new FakeServer(FakeWebSocket.last);

function Active({queryFn}: {queryFn: () => Promise<unknown>}) {
  useQuery({queryKey: consoleKeys.invoiceMonth(scope, 2026, 10), queryFn, staleTime: Infinity});
  return null;
}

function mount(opts: {principal?: string; qc?: QueryClient; queryFn?: () => Promise<unknown>} = {}) {
  const qc = opts.qc ?? new QueryClient({defaultOptions: {queries: {retry: false}}});
  render(
    <QueryClientProvider client={qc}>
      <RealtimeProvider surface={surface} principal={opts.principal ?? "u1"}>
        {opts.queryFn && <Active queryFn={opts.queryFn}/>}
      </RealtimeProvider>
    </QueryClientProvider>,
  );
  return qc;
}

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
  vi.restoreAllMocks();
  setAccessToken(null);
});

describe("losing and regaining the connection", () => {
  it("a drop reconnects with the cursor and a replayed change is not applied again", async () => {
    const qc = mount();
    const spy = vi.spyOn(qc, "invalidateQueries");
    server().open().ready().change("01A", "in_1", 1);
    const afterFirst = spy.mock.calls.length;
    expect(afterFirst).toBeGreaterThan(0);

    act(() => void server().close(1006));
    await advance(31_000);
    const second = server();
    expect(second.socket).not.toBe(FakeWebSocket.instances[0]);
    act(() => void second.open());
    expect(second.auth?.resume_cursor).toBe("01A");

    act(() => void second.ready().change("01A", "in_1", 1)); // the replay overlaps what was applied
    expect(spy.mock.calls.length).toBe(afterFirst);
    act(() => void second.change("01B", "in_1", 2)); // and a new one is
    expect(spy.mock.calls.length).toBeGreaterThan(afterFirst);
  });

  it("a reload (a new provider over the same storage) resumes from the cursor", () => {
    mount({principal: "u1"});
    act(() => void server().open().ready().change("01C", "in_1", 1));
    document.body.innerHTML = "";

    FakeWebSocket.reset();
    mount({principal: "u1"}); // the page is loaded again; sessionStorage survives it
    act(() => void server().open());
    expect(server().auth?.resume_cursor).toBe("01C");
  });

  it("recovers at once when the network comes back, whatever the backoff", async () => {
    mount();
    act(() => void server().open().ready());
    act(() => void server().close(1006));
    const count = FakeWebSocket.instances.length;
    act(() => void window.dispatchEvent(new Event("online")));
    expect(FakeWebSocket.instances.length).toBe(count + 1);
  });
});

describe("a deploy", () => {
  it("20 clients told Bye draining together reconnect spread over time, not in a stampede", async () => {
    // A seeded generator in place of Math.random, so the spread is the same every run.
    let seed = 20_261_014;
    vi.spyOn(Math, "random").mockImplementation(() => {
      seed = (seed * 1_664_525 + 1_013_904_223) % 4_294_967_296;
      return seed / 4_294_967_296;
    });
    for (let i = 0; i < 20; i++) mount({principal: `u${i}`});
    const clients = [...FakeWebSocket.instances];
    expect(clients).toHaveLength(20);
    act(() => clients.forEach((s) => new FakeServer(s).open().ready()));

    const byeAt = Date.now();
    act(() => clients.forEach((s) => new FakeServer(s).bye("draining", 3000).close(1001)));

    const reconnectedAt: number[] = [];
    for (let elapsed = 0; elapsed < 10_000 && reconnectedAt.length < 20; elapsed += 50) {
      await advance(50);
      while (FakeWebSocket.instances.length - 20 > reconnectedAt.length) reconnectedAt.push(Date.now() - byeAt);
    }
    expect(reconnectedAt).toHaveLength(20);
    expect(Math.min(...reconnectedAt)).toBeGreaterThanOrEqual(3000); // none before the server's retry-after
    expect(Math.max(...reconnectedAt) - Math.min(...reconnectedAt)).toBeGreaterThan(1000);
    expect(Math.max(...reconnectedAt)).toBeLessThan(5100); // retry-after plus at most the jitter
  });
});

describe("a Tick that runs ahead", () => {
  it("asks for one replay, not one per Tick", async () => {
    mount();
    act(() => void server().open().ready().change("01A", "in_1", 1));
    const before = FakeWebSocket.instances.length;
    act(() => void server().tick("01B").tick("01C").tick("01D"));
    await advance(100);
    expect(FakeWebSocket.instances.length).toBe(before + 1);
    act(() => void server().open());
    expect(server().auth?.resume_cursor).toBe("01A");
  });
});

describe("a Gap", () => {
  it("after a long offline refetches the active queries once", async () => {
    const queryFn = vi.fn(async () => ({}));
    mount({queryFn});
    await advance(0);
    expect(queryFn).toHaveBeenCalledTimes(1);
    act(() => void server().open().ready());
    act(() => void server().gap("cursor_too_old"));
    await advance(0);
    expect(queryFn).toHaveBeenCalledTimes(2); // once, not a storm
    await advance(60_000);
    expect(queryFn).toHaveBeenCalledTimes(2);
  });
});

describe("duplicates and reordering", () => {
  function memoryStorage(): KeyValueStorage {
    const data = new Map<string, string>();
    return {getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k)};
  }

  it("50 duplicates and 50 reordered changes of 10 entities end on the newest of each", () => {
    const applied: ChangeView[] = [];
    const core = new RealtimeCore({
      scopeKind: "console", cursor: createCursorStore("dup", memoryStorage()),
      onChange: (c) => applied.push(c), onGap: () => {}, onStatus: () => {}, onHold: () => {}, jitter: () => 0,
    });
    core.attach({send: () => true, reconnect: () => {}, close: () => {}});

    // 10 entities, 10 revisions each: 100 distinct changes with ULID-ordered ids.
    const changes: ChangeView[] = [];
    for (let revision = 1; revision <= 10; revision++) {
      for (let entity = 0; entity < 10; entity++) {
        changes.push({
          id: `01${String(changes.length).padStart(4, "0")}`, type: "invoice.updated", entity: "invoice",
          entityId: `in_${entity}`, status: "OPEN", revision, occurredAtMs: 1,
        });
      }
    }
    let seed = 7;
    const rand = () => { seed = (seed * 1_103_515_245 + 12_345) % 2_147_483_648; return seed / 2_147_483_648; };
    const stream = [...changes];
    for (let i = 0; i < 50; i++) stream.push(changes[Math.floor(rand() * changes.length)]); // duplicates
    for (let i = stream.length - 1; i > 0 && i > stream.length - 51; i--) { // 50 swaps: reordering
      const j = Math.floor(rand() * (i + 1));
      [stream[i], stream[j]] = [stream[j], stream[i]];
    }
    for (const change of stream) core.onFrame({type: "change", change});

    const ids = applied.map((c) => c.id);
    expect(new Set(ids).size).toBe(ids.length); // never twice
    for (let entity = 0; entity < 10; entity++) {
      const revisions = applied.filter((c) => c.entityId === `in_${entity}`).map((c) => c.revision);
      expect(revisions).toEqual([...revisions].sort((a, b) => a - b)); // never an older after a newer
      expect(Math.max(...revisions)).toBe(10); // and the newest got through
    }
  });
});
