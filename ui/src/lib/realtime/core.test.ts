import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import type {ChangeView, Inbound, Outbound} from "./codec";
import {RealtimeCore, type RealtimeStatus, type Transport} from "./core";
import {createCursorStore, type KeyValueStorage} from "./cursor";

function memoryStorage(): KeyValueStorage {
  const data = new Map<string, string>();
  return {getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k)};
}

class FakeTransport implements Transport {
  sent: Outbound[] = [];
  reconnects = 0;
  closed = false;
  send(o: Outbound) { this.sent.push(o); return true; }
  reconnect() { this.reconnects++; }
  close() { this.closed = true; }
}

const change = (id: string, over: Partial<ChangeView> = {}): ChangeView => ({
  id, type: "invoice.paid", entity: "invoice", entityId: "in_1", status: "PAID", revision: 1, occurredAtMs: 1, ...over,
});

interface Harness {
  core: RealtimeCore
  transport: FakeTransport
  applied: ChangeView[]
  gaps: string[]
  statuses: RealtimeStatus[]
  holds: boolean[]
  storage: KeyValueStorage
}

function harness(opts: {storage?: KeyValueStorage; jitter?: number; cursorKey?: string} = {}): Harness {
  const storage = opts.storage ?? memoryStorage();
  const h: Harness = {
    core: undefined as never, transport: new FakeTransport(), applied: [], gaps: [], statuses: [], holds: [], storage,
  };
  h.core = new RealtimeCore({
    scopeKind: "portal",
    cursor: createCursorStore(opts.cursorKey ?? "portal:u1", storage),
    onChange: (c) => h.applied.push(c),
    onGap: (r) => h.gaps.push(r),
    onStatus: (s) => h.statuses.push(s),
    onHold: (held) => h.holds.push(held),
    jitter: () => opts.jitter ?? 0,
  });
  h.core.attach(h.transport);
  return h;
}

const frame = (c: ChangeView): Inbound => ({type: "change", change: c});

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("Auth on open", () => {
  it("sends Auth with no cursor on the first run, and the stored one afterwards", () => {
    const storage = memoryStorage();
    const first = harness({storage});
    first.core.onOpen({token: "tok", space: "org:1"});
    expect(first.transport.sent[0]).toEqual({type: "auth", token: "tok", space: "org:1"});

    first.core.onFrame(frame(change("01A")));
    const again = harness({storage});
    again.core.onOpen({token: "tok2"});
    expect(again.transport.sent[0]).toMatchObject({type: "auth", token: "tok2", resumeCursor: "01A"});
  });
});

describe("changes", () => {
  it("applies a change once, advances the cursor, and ignores the same id again", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A")));
    h.core.onFrame(frame(change("01A")));
    expect(h.applied).toHaveLength(1);
    expect(h.core.cursor).toBe("01A");
  });

  it("drops an older revision of an entity and applies an equal or newer one", () => {
    const h = harness();
    h.core.onFrame(frame(change("01B", {revision: 5})));
    h.core.onFrame(frame(change("01C", {revision: 3}))); // older, arrived later
    h.core.onFrame(frame(change("01D", {revision: 5}))); // equal
    h.core.onFrame(frame(change("01E", {revision: 6})));
    expect(h.applied.map((c) => c.revision)).toEqual([5, 5, 6]);
  });

  it("does not order revisions across different entities", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A", {entityId: "in_1", revision: 9})));
    h.core.onFrame(frame(change("01B", {entityId: "in_2", revision: 1})));
    expect(h.applied).toHaveLength(2);
  });

  it("applies an id older than the cursor that it has not seen (the replay overlap), once", () => {
    const h = harness();
    h.core.onFrame(frame(change("01Z")));
    h.core.onFrame(frame(change("01M", {entityId: "in_2"}))); // before the cursor, never seen
    h.core.onFrame(frame(change("01M", {entityId: "in_2"})));
    expect(h.applied.map((c) => c.id)).toEqual(["01Z", "01M"]);
    expect(h.core.cursor).toBe("01Z"); // an older id never moves the cursor back
  });

  it("applies a change with no revision (it cannot be ordered) without comparing", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A", {revision: 7})));
    h.core.onFrame(frame(change("01B", {revision: 0})));
    expect(h.applied).toHaveLength(2);
  });

  it("bounds what it remembers: the seen set holds 500 ids and the oldest is forgotten", () => {
    const h = harness();
    const id = (n: number) => `01${String(n).padStart(6, "0")}`;
    for (let n = 1; n <= 501; n++) h.core.onFrame(frame(change(id(n), {entityId: `e${n}`})));
    expect(h.applied).toHaveLength(501);
    h.core.onFrame(frame(change(id(1), {entityId: "e1"}))); // forgotten: it applies again
    h.core.onFrame(frame(change(id(501), {entityId: "e501"}))); // remembered: it does not
    expect(h.applied).toHaveLength(502);
  });

  it("bounds the revision guard too (2000 entities)", () => {
    const h = harness();
    for (let n = 1; n <= 2100; n++) h.core.onFrame(frame(change(`01${String(n).padStart(7, "0")}`, {entityId: `e${n}`, revision: 10})));
    expect(h.applied).toHaveLength(2100);
  });
});

describe("Gap", () => {
  it("reports the gap, drops a cursor that cannot be replayed from, and adopts the next tick's head", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A")));
    h.core.onFrame({type: "gap", reason: "cursor_too_old"});
    expect(h.gaps).toEqual(["cursor_too_old"]);
    expect(h.core.cursor).toBe("");
    h.core.onFrame({type: "tick", head: "01H"});
    expect(h.core.cursor).toBe("01H");
    expect(h.transport.reconnects).toBe(0); // nothing to replay: it just reloaded
  });

  it("keeps the cursor after a slow-consumer gap: the reconnect replays what was dropped", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A")));
    h.core.onFrame({type: "gap", reason: "slow_consumer"});
    expect(h.gaps).toEqual(["slow_consumer"]);
    expect(h.core.cursor).toBe("01A");
  });
});

describe("Tick", () => {
  it("asks for a replay once when the server is ahead, and not again while one is pending", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A")));
    h.core.onFrame({type: "tick", head: "01B"});
    h.core.onFrame({type: "tick", head: "01C"});
    expect(h.transport.reconnects).toBe(1);
    h.core.onOpen({token: "t"}); // the replay request was served: a later tick may ask again
    h.core.onFrame({type: "tick", head: "01D"});
    expect(h.transport.reconnects).toBe(2);
  });

  it("does nothing when the head is the cursor or older", () => {
    const h = harness();
    h.core.onFrame(frame(change("01M")));
    h.core.onFrame({type: "tick", head: "01M"});
    h.core.onFrame({type: "tick", head: "01A"});
    expect(h.transport.reconnects).toBe(0);
  });

  it("adopts the head on a first run, with nothing to replay", () => {
    const h = harness();
    h.core.onFrame({type: "tick", head: "01X"});
    expect(h.core.cursor).toBe("01X");
    expect(h.transport.reconnects).toBe(0);
  });
});

describe("status", () => {
  it("is connecting until Ready, then live", () => {
    const h = harness();
    expect(h.core.status).toBe("connecting");
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: ["user"]});
    expect(h.core.status).toBe("live");
  });

  it("stays connected and does not disconnect on a protocol version it does not know", () => {
    const h = harness();
    h.core.onFrame({type: "ready", protocolVersion: 0, serverTimeMs: 1, scopeKinds: []});
    expect(h.core.status).toBe("live");
    expect(h.transport.closed).toBe(false);
    expect(h.core.protocolVersion).toBe(0);
  });

  it("a Hint with an interval turns to polling while the socket stays open; Hint 0 goes back", () => {
    const h = harness();
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: []});
    h.core.onFrame({type: "hint", pollIntervalMs: 10_000});
    expect(h.core.status).toBe("polling");
    expect(h.core.pollIntervalMs).toBe(10_000);
    expect(h.transport.closed).toBe(false);
    h.core.onFrame({type: "hint", pollIntervalMs: 0});
    expect(h.core.status).toBe("live");
  });

  it("a close goes back to connecting", () => {
    const h = harness();
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: []});
    h.core.onClose();
    expect(h.core.status).toBe("connecting");
  });

  it("emits a status only when it changes", () => {
    const h = harness();
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: []});
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 2, scopeKinds: []});
    expect(h.statuses.filter((s) => s === "live")).toHaveLength(1);
  });
});

describe("Bye", () => {
  it("draining: holds the reconnect for retry-after plus jitter, then lets it go", () => {
    const h = harness({jitter: 1500});
    h.core.onFrame({type: "bye", reason: "draining", retryAfterMs: 3000});
    expect(h.core.status).toBe("connecting");
    expect(h.holds).toEqual([true]);
    vi.advanceTimersByTime(4499);
    expect(h.holds).toEqual([true]);
    vi.advanceTimersByTime(2);
    expect(h.holds).toEqual([true, false]);
  });

  it("token_expired: waits for a token, with no reconnect before one arrives", () => {
    const h = harness();
    h.core.onFrame({type: "bye", reason: "token_expired", retryAfterMs: 0});
    expect(h.core.status).toBe("waiting-token");
    vi.advanceTimersByTime(60_000);
    expect(h.holds).toEqual([true]); // still held, however long it waits
    h.core.onToken("fresh");
    expect(h.holds).toEqual([true, false]);
    expect(h.core.status).toBe("connecting");
  });

  it("unauthorized waits for a token too", () => {
    const h = harness();
    h.core.onFrame({type: "bye", reason: "unauthorized", retryAfterMs: 0});
    expect(h.core.status).toBe("waiting-token");
  });

  it("revoked ends the session's stream and clears the cursor, for good", () => {
    const h = harness();
    h.core.onFrame(frame(change("01A")));
    h.core.onFrame({type: "bye", reason: "revoked", retryAfterMs: 0});
    expect(h.core.status).toBe("revoked");
    expect(h.core.cursor).toBe("");
    h.core.onToken("fresh");
    expect(h.core.status).toBe("revoked");
  });

  it("limit leaves the backoff to the shared hook", () => {
    const h = harness();
    h.core.onFrame({type: "bye", reason: "limit", retryAfterMs: 0});
    expect(h.holds).toEqual([]);
    expect(h.core.status).toBe("connecting");
  });
});

describe("tokens", () => {
  it("sends Reauth in band and does not reconnect", () => {
    const h = harness();
    h.core.onFrame({type: "ready", protocolVersion: 1, serverTimeMs: 1, scopeKinds: []});
    h.core.onToken("fresh");
    expect(h.transport.sent.at(-1)).toEqual({type: "reauth", token: "fresh"});
    expect(h.transport.reconnects).toBe(0);
    expect(h.holds).toEqual([]);
  });
});
