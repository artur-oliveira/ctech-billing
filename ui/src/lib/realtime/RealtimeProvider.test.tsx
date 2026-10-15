import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {setAccessToken} from "@/lib/api/client";
import {consoleKeys} from "@/lib/api/console";

import {encode as encodeClient, type Outbound} from "./codec";
import {createCursorStore} from "./cursor";
import {FakeWebSocket} from "./fakeSocket";
import {RealtimeProvider} from "./RealtimeProvider";
import {useRealtimeStatus} from "./useRealtimeStatus";
import {ClientFrame, ServerFrame} from "./wire/realtime";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  API_BASE_URL: "https://billing-api.aoctech.app",
}));

const ORG_A = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const ORG_B = "01b04ed6-1af9-745e-bcf7-d3b66fe52322";
const consoleA = {kind: "console", scope: {org: ORG_A, mode: "live"}} as const;
const consoleB = {kind: "console", scope: {org: ORG_B, mode: "live"}} as const;
const KEY_A = `console:u1:${ORG_A}:live`;

const server = (body: NonNullable<ServerFrame["body"]>) => ServerFrame.encode({body}).finish();
const ready = server({$case: "ready", value: {protocol_version: 1, server_time_ms: 1, scope_kinds: ["tenant"]}});
const changeFrame = (id: string, entityId = "in_1", revision = 1) =>
  server({$case: "change", value: {id, type: "invoice.paid", entity: "invoice", entity_id: entityId, status: "PAID", revision, occurred_at_ms: 1}});

function firstFrame(socket: FakeWebSocket, index = 0) {
  return ClientFrame.decode(socket.sent[index]).body;
}

function Status() {
  return <p data-testid="status">{useRealtimeStatus()}</p>;
}

function mount(surface: Parameters<typeof RealtimeProvider>[0]["surface"], principal: string | null = "u1", qc = new QueryClient()) {
  const tree = (s: typeof surface, p: string | null) => (
    <QueryClientProvider client={qc}>
      <RealtimeProvider surface={s} principal={p}>
        <Status/>
      </RealtimeProvider>
    </QueryClientProvider>
  );
  const view = render(tree(surface, principal));
  return {qc, view, rerender: (s: typeof surface, p: string | null = principal) => view.rerender(tree(s, p))};
}

const status = () => screen.getByTestId("status").textContent;

beforeEach(() => {
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

describe("the connection", () => {
  it("opens wss://…/v1.0/ws with no query string and a binary socket", () => {
    mount(consoleA);
    const socket = FakeWebSocket.last;
    expect(socket.url).toBe("wss://billing-api.aoctech.app/v1.0/ws");
    expect(socket.url).not.toContain("?");
    expect(socket.binaryType).toBe("arraybuffer");
  });

  it("sends Auth first, with the token, the space and mode, and the stored cursor", () => {
    createCursorStore(KEY_A).set("01STORED");
    mount(consoleA);
    act(() => FakeWebSocket.last.open());
    expect(firstFrame(FakeWebSocket.last)).toMatchObject({
      $case: "auth",
      value: {token: "tok-1", space: `org:${ORG_A}`, mode: "live", resume_cursor: "01STORED", pay_link: ""},
    });
  });

  it("sends a pay link and no token from a checkout", () => {
    mount({kind: "checkout", token: "ctech.2.payload.mac"}, null);
    expect(FakeWebSocket.instances).toHaveLength(0); // a checkout has no signed-in principal: it is not enabled
    mount({kind: "checkout", token: "ctech.2.payload.mac"}, "anonymous");
    act(() => FakeWebSocket.last.open());
    expect(firstFrame(FakeWebSocket.last)).toMatchObject({$case: "auth", value: {pay_link: "ctech.2.payload.mac", token: ""}});
  });

  it("turns each change into an invalidation through the binding, once", () => {
    const {qc} = mount(consoleA);
    const spy = vi.spyOn(qc, "invalidateQueries");
    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.receive(ready));
    expect(status()).toBe("live");
    act(() => FakeWebSocket.last.receive(changeFrame("01A", "in_1", 1)));
    act(() => FakeWebSocket.last.receive(changeFrame("01A", "in_1", 1))); // the same id again
    expect(spy).toHaveBeenCalledWith({queryKey: consoleKeys.invoices(consoleA.scope)});
    const calls = spy.mock.calls.length;
    expect(calls).toBe(2); // the invoices and the overview, from one change
  });

  it("is off without the flag: nothing opens and the status is off", () => {
    vi.stubEnv("NEXT_PUBLIC_REALTIME", "");
    mount(consoleA);
    expect(FakeWebSocket.instances).toHaveLength(0);
    expect(status()).toBe("off");
  });

  it("mounts nothing for a surface that is not in the list", () => {
    vi.stubEnv("NEXT_PUBLIC_REALTIME_SURFACES", "checkout,portal");
    mount(consoleA);
    expect(FakeWebSocket.instances).toHaveLength(0);
    expect(status()).toBe("off");
  });
});

describe("a change of principal", () => {
  it("closes the socket, forgets the cursor and opens another with no resume cursor", () => {
    createCursorStore(KEY_A).set("01OLD");
    const {rerender} = mount(consoleA);
    const first = FakeWebSocket.last;
    act(() => first.open());
    expect(firstFrame(first)).toMatchObject({value: {resume_cursor: "01OLD"}});

    rerender(consoleB); // another organization
    expect(first.readyState).toBe(FakeWebSocket.CLOSED);
    expect(createCursorStore(KEY_A).get()).toBe("");
    const second = FakeWebSocket.last;
    expect(second).not.toBe(first);
    act(() => second.open());
    expect(firstFrame(second)).toMatchObject({$case: "auth", value: {space: `org:${ORG_B}`, resume_cursor: ""}});
  });

  it("does the same for the mode and for the space of Finanças", () => {
    const {rerender} = mount({kind: "console", scope: {org: ORG_A, mode: "live"}});
    act(() => FakeWebSocket.last.open());
    const first = FakeWebSocket.last;
    rerender({kind: "console", scope: {org: ORG_A, mode: "test"}});
    expect(first.readyState).toBe(FakeWebSocket.CLOSED);
    expect(FakeWebSocket.last).not.toBe(first);

    const finance = mount({kind: "finance", mode: "live", space: {kind: "personal"}});
    act(() => FakeWebSocket.last.open());
    const personal = FakeWebSocket.last;
    finance.rerender({kind: "finance", mode: "live", space: {kind: "organization", organizationId: ORG_A}});
    expect(personal.readyState).toBe(FakeWebSocket.CLOSED);
  });

  it("signing out closes the socket and forgets the cursor", () => {
    createCursorStore(KEY_A).set("01OLD");
    const {rerender} = mount(consoleA);
    const socket = FakeWebSocket.last;
    act(() => socket.open());
    rerender(consoleA, null);
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED);
    expect(createCursorStore(KEY_A).get()).toBe("");
    expect(status()).toBe("off");
  });

  it("does not remount the screens when it changes", () => {
    let mounts = 0;
    function Probe() {
      mounts += 1;
      return null;
    }
    const qc = new QueryClient();
    const tree = (s: typeof consoleA | typeof consoleB) => (
      <QueryClientProvider client={qc}>
        <RealtimeProvider surface={s} principal="u1"><Probe/></RealtimeProvider>
      </QueryClientProvider>
    );
    const view = render(tree(consoleA));
    const before = mounts;
    view.rerender(tree(consoleB));
    // Rendered again (a prop changed) but never unmounted: its state would survive.
    expect(mounts - before).toBeLessThanOrEqual(2);
    expect(FakeWebSocket.instances.length).toBe(2);
  });
});

describe("tokens", () => {
  it("sends a refreshed token in band and does not reconnect", () => {
    mount(consoleA);
    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.receive(ready));
    const socket = FakeWebSocket.last;
    const count = FakeWebSocket.instances.length;

    act(() => setAccessToken("tok-2"));
    expect(FakeWebSocket.instances).toHaveLength(count);
    expect(socket.readyState).toBe(FakeWebSocket.OPEN);
    expect(ClientFrame.decode(socket.sent.at(-1) as Uint8Array).body).toEqual({$case: "reauth", value: {token: "tok-2"}});
  });

  it("waits quietly after token_expired, however long, and reconnects when a token arrives", () => {
    vi.useFakeTimers();
    mount(consoleA);
    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.receive(ready));
    const first = FakeWebSocket.last;
    act(() => FakeWebSocket.last.receive(server({$case: "bye", value: {reason: "token_expired", retry_after_ms: 0}})));
    expect(status()).toBe("waiting-token");
    act(() => first.drop(1008)); // the server closes after the Bye
    const count = FakeWebSocket.instances.length;
    act(() => void vi.advanceTimersByTime(10 * 60_000));
    expect(FakeWebSocket.instances).toHaveLength(count); // no reconnect loop with the dead token

    act(() => setAccessToken("tok-fresh"));
    expect(FakeWebSocket.instances).toHaveLength(count + 1);
    act(() => FakeWebSocket.last.open());
    expect(firstFrame(FakeWebSocket.last)).toMatchObject({$case: "auth", value: {token: "tok-fresh"}});
  });
});

describe("the shared hook's recovery options", () => {
  it("keeps reconnecting past ten attempts", () => {
    vi.useFakeTimers();
    mount(consoleA);
    for (let i = 0; i < 14; i++) {
      act(() => FakeWebSocket.last.drop());
      act(() => void vi.advanceTimersByTime(31_000));
    }
    expect(FakeWebSocket.instances.length).toBeGreaterThan(12);
  });

  it("gives up on a socket stuck connecting after ten seconds and tries again", () => {
    vi.useFakeTimers();
    mount(consoleA);
    const stuck = FakeWebSocket.last; // never opens
    act(() => void vi.advanceTimersByTime(10_001));
    expect(stuck.readyState).toBe(FakeWebSocket.CLOSED);
    act(() => void vi.advanceTimersByTime(31_000));
    expect(FakeWebSocket.instances.length).toBeGreaterThan(1);
  });

  it("recovers at once when the network comes back, without waiting out the backoff", () => {
    vi.useFakeTimers();
    mount(consoleA);
    act(() => FakeWebSocket.last.open());
    act(() => FakeWebSocket.last.drop());
    const count = FakeWebSocket.instances.length;
    act(() => void window.dispatchEvent(new Event("online")));
    expect(FakeWebSocket.instances.length).toBe(count + 1);
  });
});

describe("encode", () => {
  it("is what the shared hook uses for its own heartbeat", () => {
    const frame: Outbound = {type: "ping"};
    expect(ClientFrame.decode(encodeClient(frame)).body?.$case).toBe("ping");
  });
});
