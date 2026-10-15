import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {API_BASE_URL} from "@/lib/api/client";

import type {ChangeView} from "./codec";
import {RealtimeCore, type RealtimeStatus} from "./core";
import {createCursorStore, type KeyValueStorage} from "./cursor";
import {changesFetcher, PollUnauthorized, type PollResult, startPolling} from "./poller";

function memoryStorage(): KeyValueStorage {
  const data = new Map<string, string>();
  return {getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k)};
}

const change = (id: string, entityId = "in_1", revision = 1): ChangeView => ({
  id, type: "invoice.paid", entity: "invoice", entityId, status: "PAID", revision, occurredAtMs: 1,
});

function setup(initialCursor = "") {
  const applied: ChangeView[] = [];
  const gaps: string[] = [];
  const statuses: RealtimeStatus[] = [];
  const cursor = createCursorStore("poll:test", memoryStorage());
  if (initialCursor) cursor.set(initialCursor);
  const core = new RealtimeCore({
    scopeKind: "portal", cursor, onChange: (c) => applied.push(c), onGap: (r) => gaps.push(r),
    onStatus: (s) => statuses.push(s), onHold: () => {}, jitter: () => 0,
  });
  core.attach({send: () => true, reconnect: () => {}, close: () => {}});
  return {core, applied, gaps, statuses};
}

const page = (changes: ChangeView[], over: Partial<PollResult> = {}): PollResult => ({changes, head: changes.at(-1)?.id ?? "", more: false, ...over});

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("startPolling", () => {
  it("polls at once and then every interval, with the core's cursor each time", async () => {
    const t = setup("01A");
    const fetchChanges = vi.fn(async (cursor: string) => page(cursor === "01A" ? [change("01B")] : []));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchChanges).toHaveBeenCalledTimes(1);
    expect(fetchChanges).toHaveBeenLastCalledWith("01A");

    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchChanges).toHaveBeenCalledTimes(2);
    expect(fetchChanges).toHaveBeenLastCalledWith("01B"); // the cursor moved with what was applied
    await vi.advanceTimersByTimeAsync(9_000);
    expect(fetchChanges).toHaveBeenCalledTimes(2);
    stop();
  });

  it("does not poll a hidden tab, and resumes when it is visible", async () => {
    const t = setup("01A");
    let visible = false;
    const fetchChanges = vi.fn(async () => page([]));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => visible});
    await vi.advanceTimersByTimeAsync(35_000);
    expect(fetchChanges).not.toHaveBeenCalled();
    visible = true;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchChanges).toHaveBeenCalledTimes(1);
    stop();
  });

  it("feeds changes through the core, so the same de-duplication applies", async () => {
    const t = setup("01A");
    const fetchChanges = vi.fn(async () => page([change("01B"), change("01B"), change("01C", "in_1", 1)]));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(10_000); // the same page again
    expect(t.applied.map((c) => c.id)).toEqual(["01B", "01C"]);
    stop();
  });

  it("pages while the server says there is more", async () => {
    const t = setup("01A");
    const fetchChanges = vi.fn(async (cursor: string) => {
      if (cursor === "01A") return page([change("01B", "e1")], {more: true});
      if (cursor === "01B") return page([change("01C", "e2")], {more: true});
      return page([change("01D", "e3")]);
    });
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchChanges.mock.calls.map((c) => c[0])).toEqual(["01A", "01B", "01C"]);
    expect(t.applied).toHaveLength(3);
    stop();
  });

  it("stops the moment it is told to, and applies nothing after", async () => {
    const t = setup("01A");
    let release: (r: PollResult) => void = () => {};
    const fetchChanges = vi.fn(() => new Promise<PollResult>((resolve) => { release = resolve; }));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    stop(); // the socket is live again while the request is in flight
    release(page([change("01B")]));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(t.applied).toHaveLength(0);
    expect(fetchChanges).toHaveBeenCalledTimes(1);
  });

  it("a 401 moves the core to waiting-token", async () => {
    const t = setup("01A");
    const fetchChanges = vi.fn(async () => { throw new PollUnauthorized(); });
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    expect(t.core.status).toBe("waiting-token");
    stop();
  });

  it("a network error throws nothing and leaves the status where it was", async () => {
    const t = setup("01A");
    t.core.onUnreachable();
    const fetchChanges = vi.fn(async () => { throw new TypeError("Failed to fetch"); });
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(t.core.status).toBe("polling");
    expect(fetchChanges.mock.calls.length).toBeGreaterThanOrEqual(3); // it keeps trying
    stop();
  });

  it("reports a gap instead of changes when the cursor cannot be replayed", async () => {
    const t = setup("01A");
    const fetchChanges = vi.fn(async () => page([], {gap: "cursor_too_old"}));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    expect(t.gaps).toEqual(["cursor_too_old"]);
    stop();
  });

  it("with no cursor yet, adopts the response's head as now", async () => {
    const t = setup("");
    const fetchChanges = vi.fn(async () => page([], {head: "01H"}));
    const stop = startPolling({fetchChanges, core: t.core, intervalMs: 10_000, visible: () => true});
    await vi.advanceTimersByTimeAsync(0);
    expect(t.core.cursor).toBe("01H");
    stop();
  });
});

describe("onUnreachable", () => {
  it("turns the core to polling at the default interval, and a Ready ends it", () => {
    const t = setup();
    t.core.onUnreachable();
    expect(t.core.status).toBe("polling");
    expect(t.core.pollIntervalMs).toBe(10_000);
    t.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: []});
    expect(t.core.status).toBe("live");
    expect(t.core.pollIntervalMs).toBe(0);
  });

  it("does not override a wait for a token or a revoked session", () => {
    const t = setup();
    t.core.onFrame({type: "bye", reason: "token_expired", retryAfterMs: 0});
    t.core.onUnreachable();
    expect(t.core.status).toBe("waiting-token");
  });
});

describe("changesFetcher", () => {
  const stubFetch = (body: unknown, status = 200) => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(body), {status}));
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  };
  afterEach(() => vi.unstubAllGlobals());

  it("sends the console's credential in headers, never in the URL", async () => {
    const fetchMock = stubFetch({changes: [], head: "", more: false});
    await changesFetcher({kind: "console", scope: {org: "o1", mode: "test"}})("01CUR");
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${API_BASE_URL}/v1.0/realtime/changes?cursor=01CUR`);
    expect(url).not.toMatch(/token|access|pay_link/i);
    expect(init.headers).toMatchObject({"X-Billing-Space": "org:o1", "X-Billing-Mode": "test"});
    expect((init.headers as Record<string, string>).Authorization).toMatch(/^Bearer /);
  });

  it("sends a pay link as PayLink and nothing else", async () => {
    const fetchMock = stubFetch({changes: [], head: "", more: false});
    await changesFetcher({kind: "checkout", token: "ctech.2.p.m"})("");
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${API_BASE_URL}/v1.0/realtime/changes`);
    expect((init.headers as Record<string, string>).Authorization).toBe("PayLink ctech.2.p.m");
  });

  it("maps the wire's fields and a 401 to PollUnauthorized", async () => {
    stubFetch({changes: [{id: "01B", type: "invoice.paid", entity: "invoice", entity_id: "in_1", status: "PAID", revision: 3, occurred_at_ms: 9}], head: "01B", more: true});
    expect(await changesFetcher({kind: "portal"})("01A")).toEqual({
      changes: [{id: "01B", type: "invoice.paid", entity: "invoice", entityId: "in_1", status: "PAID", revision: 3, occurredAtMs: 9}],
      head: "01B", more: true, gap: undefined,
    });
    stubFetch({}, 401);
    await expect(changesFetcher({kind: "portal"})("01A")).rejects.toBeInstanceOf(PollUnauthorized);
    stubFetch({}, 500);
    await expect(changesFetcher({kind: "portal"})("01A")).rejects.toThrow(/500/);
  });
});
