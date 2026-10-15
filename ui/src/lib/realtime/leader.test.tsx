import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";

import {FakeServer} from "./fakeServer";
import {FakeWebSocket} from "./fakeSocket";
import {channelName} from "./leader";
import {RealtimeProvider} from "./RealtimeProvider";

const ORG = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const surface = {kind: "console", scope: {org: ORG, mode: "live"}} as const;

/** navigator.locks: one holder per name, the rest queue in order and are granted on release. */
class FakeLocks {
  private held = new Set<string>();
  private queues = new Map<string, Array<() => void>>();

  request(name: string, callback: () => Promise<unknown>): Promise<unknown> {
    return new Promise((resolve) => {
      const run = () => {
        this.held.add(name);
        void Promise.resolve(callback()).finally(() => {
          this.held.delete(name);
          this.queues.get(name)?.shift()?.();
          resolve(undefined);
        });
      };
      if (!this.held.has(name)) run();
      else this.queues.set(name, [...(this.queues.get(name) ?? []), run]);
    });
  }
}

/** BroadcastChannel: delivers to every other channel of the same name, asynchronously. */
class FakeBroadcastChannel {
  static channels = new Map<string, Set<FakeBroadcastChannel>>();
  static names(): string[] {
    return [...FakeBroadcastChannel.channels.keys()];
  }

  onmessage: ((event: {data: unknown}) => void) | null = null;

  constructor(readonly name: string) {
    FakeBroadcastChannel.channels.set(name, (FakeBroadcastChannel.channels.get(name) ?? new Set()).add(this));
  }

  postMessage(data: unknown): void {
    const copy = structuredClone(data);
    for (const peer of FakeBroadcastChannel.channels.get(this.name) ?? []) {
      if (peer !== this) queueMicrotask(() => peer.onmessage?.({data: copy}));
    }
  }

  close(): void {
    FakeBroadcastChannel.channels.get(this.name)?.delete(this);
  }
}

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

function mount(principal: string, qc = new QueryClient()) {
  const view = render(
    <QueryClientProvider client={qc}>
      <RealtimeProvider surface={surface} principal={principal}>{null}</RealtimeProvider>
    </QueryClientProvider>,
  );
  return {qc, unmount: view.unmount, spy: vi.spyOn(qc, "invalidateQueries")};
}

function installBrowserApis() {
  Object.defineProperty(navigator, "locks", {value: new FakeLocks(), configurable: true});
  vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
}

beforeEach(() => {
  vi.useFakeTimers();
  FakeWebSocket.reset();
  FakeBroadcastChannel.channels = new Map();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubEnv("NEXT_PUBLIC_REALTIME", "true");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "");
  vi.stubEnv("NEXT_PUBLIC_REALTIME_SHARED", "true");
  sessionStorage.clear();
  setAccessToken("tok-1");
  installBrowserApis();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  Reflect.deleteProperty(navigator, "locks");
  setAccessToken(null);
});

describe("one socket per browser", () => {
  it("of three instances for one principal exactly one opens a socket, and all three hear its changes", async () => {
    const a = mount("u1");
    const b = mount("u1");
    const c = mount("u1");
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(1);

    act(() => void new FakeServer(FakeWebSocket.last).open().ready().change("01A", "in_1", 1));
    await flush();
    for (const t of [a, b, c]) expect(t.spy).toHaveBeenCalled();
  });

  it("when the leader goes, one follower takes over from the cursor with nothing lost or applied twice", async () => {
    const a = mount("u1");
    const b = mount("u1");
    await flush();
    act(() => void new FakeServer(FakeWebSocket.last).open().ready().change("01A", "in_1", 1));
    await flush();
    const applied = b.spy.mock.calls.length;
    expect(applied).toBeGreaterThan(0);

    a.unmount(); // the leader's tab closes
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(2); // the follower connected
    const successor = new FakeServer(FakeWebSocket.last);
    act(() => void successor.open());
    await flush();
    expect(successor.auth?.resume_cursor).toBe("01A"); // from what it heard as a follower

    act(() => void successor.ready().change("01A", "in_1", 1).change("01B", "in_1", 2));
    await flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(300); }); // the coalescing window closes
    const added = b.spy.mock.calls.length - applied;
    expect(added).toBeGreaterThan(0); // 01B applied
    expect(added).toBeLessThanOrEqual(2); // and 01A was not applied a second time
  });

  it("without the browser APIs every instance connects on its own", async () => {
    Reflect.deleteProperty(navigator, "locks");
    vi.stubGlobal("BroadcastChannel", undefined);
    mount("u1");
    mount("u1");
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(2);
  });

  it("with the flag off every instance connects on its own, APIs or not", async () => {
    vi.stubEnv("NEXT_PUBLIC_REALTIME_SHARED", "");
    mount("u1");
    mount("u1");
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(2);
  });

  it("another principal never shares a socket or a channel", async () => {
    const a = mount("u1");
    const b = mount("u2");
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(2); // each is its own leader
    expect(new Set(FakeBroadcastChannel.names()).size).toBe(2);

    act(() => void new FakeServer(FakeWebSocket.instances[0]).open().ready().change("01A", "in_1", 1));
    await flush();
    expect(a.spy).toHaveBeenCalled();
    expect(b.spy).not.toHaveBeenCalled();
  });
});

describe("channelName", () => {
  it("names the principal, the mode and the space without containing them", () => {
    const name = channelName(`console:u1:${ORG}:live`);
    expect(name).toMatch(/^billing-rt:[0-9a-f]{8}$/);
    expect(name).not.toContain("u1");
    expect(name).not.toContain(ORG);
    expect(channelName(`console:u1:${ORG}:live`)).toBe(name);
    expect(channelName(`console:u1:${ORG}:test`)).not.toBe(name);
    expect(channelName(`console:u2:${ORG}:live`)).not.toBe(name);
  });
});
