# Realtime R3 — The client — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `RealtimeProvider` and `useRealtime()` in the UI keep one authorized connection per browser, turn every `Change` into the right TanStack Query invalidation or patch, survive every loss path in the spec (reconnect, gap, drain, expiry, blocked WebSocket), and fall back to cursor polling and then to today's behaviour, all behind `NEXT_PUBLIC_REALTIME`.

**Architecture:** A React-free **core** (`RealtimeCore`) handles frames, the cursor, de-duplication, revision guards, and decides when to poll; it talks to a **transport** interface. The production transport wraps `@aoctech/ws-client`'s `useWebSocket` (heartbeat, jittered unlimited backoff, recovery on `online`/`visible`, token-change handling, binary frames), with `encode`/`decode` mapping protobuf frames to plain objects. A **catalogue** maps each change to query keys; a **binding** applies it to the `QueryClient`. Nothing renders a state the user must act on; an indicator appears only where it matters.

**Tech Stack:** React 19, TanStack Query 5, `@aoctech/ws-client`, `ts-proto` output from R1, vitest + Testing Library, a scripted fake `WebSocket`.

**Spec:** `docs/specs/2026-10-14-realtime.md` (§4.5–4.7, §6). **Depends on:** R1 (generated types, golden bytes), R2 (server; the client is tested against fakes so it can start after R1 and meet the server in R4). **Blocks:** R4.

Repos: `P_BILL/ui`. Pattern sources: `ui/src/lib/hooks/usePaymentStream.ts` (what is replaced), `lib/api/{console,finance,portal,checkout}.ts` (the key factories), `@aoctech/ws-client` `src/useWebSocket.ts`.

## Global Constraints

- The flag `NEXT_PUBLIC_REALTIME` (default off) and the comma list `NEXT_PUBLIC_REALTIME_SURFACES` (`checkout,console,portal,finance`; a surface not listed mounts nothing). Off: the provider renders its children and does nothing; every screen is as it is today.
- **No credential in the URL.** The token goes in the first frame (the `Auth` message), a polling request uses the `Authorization` header.
- An event is a hint (spec D3). The binding invalidates; it patches only a status word, guarded by `revision`, and never invents data.
- The client never chooses a scope. It sends its token, the selected space, the pay link, and its cursor; the server decides.
- Cursor storage and every browser API (`sessionStorage`, `BroadcastChannel`, `navigator.locks`) is wrapped in try/catch and optional.
- Copy in `locales/{pt-BR,en}`; no new dependency beyond the generated code's `@bufbuild/protobuf` (already added in R1). ESLint zero warnings; `consistent-return` satisfied by real returns, never by deleting a guard `return`.

## Review Focus

- An event delivered twice, or an older revision after a newer one, changes nothing the second time. Pinned in Task 2.
- A `Gap` refetches **every active query of the scope kind** and nothing else. Pinned in Task 3.
- After a reload the connection resumes from the stored cursor and replays nothing it already applied (dedupe by id). Pinned in Task 2 and Task 6.
- A token refresh sends `Reauth` in-band and does **not** reconnect; a missing refresh ends in `Bye{token_expired}` and a quiet wait, never a reconnect loop with the dead token. Pinned in Task 4.
- `Bye{draining}` reconnects after `retry_after_ms` plus jitter, so a deploy does not become a stampede. Pinned in Task 6.
- Two tabs hold one socket between them, and closing the leader hands the socket to the other without losing a change. Pinned in Task 7.
- With the WebSocket blocked, the screens still update within 10 s by polling. Pinned in Task 5.
- A principal switch (another space, another organization, sign-out) closes the connection and clears the cursor before anything of the new principal is applied. Pinned in Task 4.

---

## Task 1: Codec and frame mapping

**Files:**
- Create: `ui/src/lib/realtime/codec.ts`, `codec.test.ts`

**Interfaces:**
- Produces:
```ts
export type Inbound =
  | {type: "ready"; protocolVersion: number; serverTimeMs: number; scopeKinds: string[]}
  | {type: "change"; change: ChangeView}
  | {type: "gap"; reason: string}
  | {type: "pong"}                      // the shape @aoctech/ws-client's heartbeat looks for
  | {type: "tick"; head: string}
  | {type: "bye"; reason: string; retryAfterMs: number}
  | {type: "hint"; pollIntervalMs: number}
export interface ChangeView {id: string; type: string; entity: string; entityId: string; status: string; revision: number; occurredAtMs: number}
export type Outbound =
  | {type: "auth"; token?: string; payLink?: string; space?: string; resumeCursor?: string}
  | {type: "reauth"; token: string}
  | {type: "ping"}                      // what the shared hook sends every 20 s
export const encode: (o: Outbound) => Uint8Array
export const decode: (data: ArrayBuffer) => Inbound   // throws on a malformed frame; the hook ignores it
```

- [ ] **Step 1: Failing tests** (`codec.test.ts`): (a) every golden file in `proto/testdata/` decodes to the expected `Inbound`/round-trips for `Outbound`; (b) a `ServerFrame` with an unknown `oneof` case decodes to `{type: "unknown"}`-free no-op (decode returns a `{type:"pong"}`-incompatible sentinel the core ignores: assert it does not throw); (c) a frame with an unknown extra field decodes; (d) `encode({type:"ping"})` is a `ClientFrame` with `ping` set; (e) a 33 KiB buffer is rejected before decoding (the server's own cap).
- [ ] **Step 2:** Implement with the generated `ClientFrame`/`ServerFrame` (`oneof=unions-value` gives `{$case, value}`), mapping to the plain shapes above.
- [ ] **Step 3: Commit** `feat(realtime-ui): codec between the wire and plain frames`.

## Task 2: The core

**Files:**
- Create: `ui/src/lib/realtime/core.ts`, `core.test.ts`, `cursor.ts`, `cursor.test.ts`, `seen.ts`

**Interfaces:**
- Produces:
```ts
export interface Transport { send(o: Outbound): boolean; reconnect(): void; close(): void }
export interface CoreOptions {
  scopeKind: "console" | "portal" | "checkout"
  cursorKey: string                      // storage key, includes the principal and mode
  onChange: (c: ChangeView) => void      // the binding
  onGap: (reason: string) => void
  onStatus: (s: RealtimeStatus) => void
  now?: () => number
}
export type RealtimeStatus = "off" | "connecting" | "live" | "polling" | "waiting-token" | "revoked"
export class RealtimeCore {
  attach(t: Transport): void
  onOpen(auth: {token?: string; payLink?: string; space?: string}): void   // sends Auth with the stored cursor
  onFrame(f: Inbound): void
  onClose(): void
  onToken(token: string): void           // sends Reauth
  readonly cursor: string
}
```

- [ ] **Step 1: Failing tests** (`core.test.ts`, fake transport, fake clock):
  1. `onOpen` sends `Auth` with `resumeCursor` equal to the stored cursor (none on first run).
  2. A `Change` calls `onChange` once and advances the cursor; the same id again does nothing; **an event with `revision` lower than the last applied for that entity is dropped, an equal or higher one is applied**; ids older than the cursor but unseen (the 5 s overlap) **are** applied once.
  3. The seen-set is bounded (500 ids): the 501st evicts the oldest, no unbounded growth.
  4. `Gap` calls `onGap(reason)` and keeps the cursor; `Tick{head}` with `head > cursor` triggers a **replay request** by calling `transport.reconnect()` only if no reconnect is already pending (it re-sends `Auth` with the cursor); equal or lower does nothing.
  5. `Ready` sets status `live`; an unsupported `protocolVersion` lower than the client's minimum feature set leaves features gated off but does not disconnect.
  6. `Hint{pollIntervalMs>0}` sets status `polling` while the socket stays open; `Hint{0}` returns to `live`.
  7. `Bye`: `draining` → status `connecting` and a reconnect after `retryAfterMs + jitter(0..2000)` (jitter injectable); `token_expired` → `waiting-token` (no reconnect until `onToken`); `revoked` → `revoked` and the cursor is cleared; `unauthorized` → `waiting-token`; `limit` → backoff left to the hook.
  8. `onToken(t)` sends `Reauth` and does not reconnect.
  9. **Cursor persistence** (`cursor.test.ts`): stored per `cursorKey`; survives a new core instance; storage that throws (private mode) degrades to memory without throwing; a cursor for another principal is not read.
- [ ] **Step 2:** Implement. `seen.ts` is an insertion-ordered `Set` capped at 500. The revision guard keeps `Map<entity#id, revision>` capped at 2000 entries (oldest out).
- [ ] **Step 3: Commit** `feat(realtime-ui): the core (cursor, dedupe, revisions, gap, bye, tick, hint)`.

## Task 3: Catalogue and binding

**Files:**
- Create: `ui/src/lib/realtime/catalogue.ts`, `catalogue.test.ts`, `binding.ts`, `binding.test.ts`

**Interfaces:**
- Produces:
```ts
export type Surface =
  | {kind: "console"; scope: ConsoleScope}
  | {kind: "portal"}
  | {kind: "finance"; mode: Mode; space: Space}
  | {kind: "checkout"; token: string}
// What a change touches: query keys to invalidate and an optional status patch.
export function effectsOf(surface: Surface, c: ChangeView): {invalidate: QueryKey[]; patch?: (qc: QueryClient) => void}
export function applyChange(qc: QueryClient, surface: Surface, c: ChangeView): void
export function applyGap(qc: QueryClient, surface: Surface): void
```
- The catalogue (the table is the spec of what updates what):

| change `entity` / `type` | console | portal | finance | checkout |
|---|---|---|---|---|
| `invoice.*` | `consoleKeys.invoices(scope)` (prefix: list, month, detail), `consoleKeys.overview` | `portalKeys.invoices`, `portalKeys.invoice(id)` | — | `checkoutKeys.view(token)` + patch status |
| `invoice.payer_recorded` | `consoleKeys.invoice(scope,id)` | `portalKeys.invoice(id)` | — | — |
| `payment.*`, `checkout.session.*` | the invoice's detail | the invoice's detail | — | `checkoutKeys.view(token)` |
| `subscription.*` | `consoleKeys.subscriptions`, `.subscription(id)`, `.overview` | `portalKeys.subscriptions`, `.subscription(id)` | — | — |
| `customer.updated` | `consoleKeys.customers`, `.customer(id)` | — | — | — |
| `payout.updated`, `connection.updated` | `consoleKeys.*` of the connections screen | — | `connectionKeys.list(ctx)`, `.one(ctx,"inter")`, `financeKeys.accounts` | — |
| `statement.line_posted`, `bill.updated` | — | — | `financeKeys.all(mode, space)` prefix for bills, statement, accounts, `connectionKeys.unclassified` | — |

- [ ] **Step 1: Failing tests:** for each row, build a `QueryClient` with seeded queries under the real key factories, apply a change, and assert (a) the keys listed are invalidated (`isInvalidated`) and **unrelated keys are not** (another org's, another mode's, another invoice's detail); (b) the status patch rewrites only the `status` field of the cached invoice and only when `c.revision` is not older than the cache's `updated_at`-derived revision; (c) `applyGap` invalidates every query under the surface's root key and nothing under other roots; (d) an unknown `type` invalidates the entity's whole list prefix (never nothing, never everything).
- [ ] **Step 2:** Implement `effectsOf` as a lookup over `(surface.kind, entity)` with `type` refinements; `applyChange` calls `qc.invalidateQueries({queryKey})` for each key and runs the patch.
- [ ] **Step 3: Commit** `feat(realtime-ui): the catalogue of what a change updates, and its binding`.

## Task 4: Transport, provider and hook

**Files:**
- Create: `ui/src/lib/realtime/RealtimeProvider.tsx`, `RealtimeProvider.test.tsx`, `useRealtimeStatus.ts`, `fakeSocket.ts` (test support)
- Modify: `ui/src/lib/api/client.ts` only if `subscribeAccessToken` does not exist (add a minimal pub/sub; check first)

- [ ] **Step 1: Failing tests** (with `fakeSocket`, a scripted `WebSocket` replacement installed on `globalThis`, fake timers):
  1. The URL is `${API_BASE_URL}/v1.0/realtime` mapped to `wss://`; **no query string**; `binaryType` is `arraybuffer`.
  2. On open the first frame is an `Auth` carrying the token, the selected space (console/finance), or the pay link (checkout), and the stored cursor.
  3. Changes applied to the `QueryClient` through the binding (spy on `invalidateQueries`).
  4. **Principal switch:** changing the space, the organization, the mode or signing out closes the socket, clears the cursor key, and the next `Auth` has no `resumeCursor`.
  5. **Token refresh:** `subscribeAccessToken` fires → a `Reauth` frame is sent, **no new socket is created**; if no refresh comes before `Bye{token_expired}`, no reconnect happens until the token changes (status `waiting-token`).
  6. The shared hook options are set: `reconnectForever`, `reconnectJitter`, `recoverOnNetworkRestore`, `connectTimeoutMs: 10_000`.
  7. Flag off: nothing opens and `useRealtimeStatus()` is `off`.
- [ ] **Step 2:** Implement: `RealtimeProvider({surface, auth, children})` builds the core, calls `useWebSocket({url, enabled, binaryType, encode: codec.encode, decode: codec.decode, onMessage: core.onFrame, onOpen: () => core.onOpen(auth()), reconnectForever: true, reconnectJitter: true, recoverOnNetworkRestore: true, connectTimeoutMs: 10_000})` without `authToken`, and passes the hook's `send`/`reconnect` as the transport. `decode` maps the server's `Pong` to `{type: "pong"}` so the hook's heartbeat recognises it. `useRealtimeStatus()` exposes the status.
- [ ] **Step 3: Commit** `feat(realtime-ui): RealtimeProvider over the shared WebSocket hook`.

## Task 5: Fallback ladder

**Files:**
- Create: `ui/src/lib/realtime/poller.ts`, `poller.test.ts`

**Interfaces:**
- Produces: `startPolling(opts: {fetchChanges: (cursor: string) => Promise<{changes: ChangeView[]; head: string; more: boolean}>; core: RealtimeCore; intervalMs: number; visible: () => boolean}): () => void`

- [ ] **Step 1: Failing tests:** while status is `polling` (socket down for more than 15 s, or `Hint`), the poller requests `GET /v1.0/realtime/changes?cursor=…` every 10 s **only while the tab is visible**, with `Authorization` (never a query credential), feeds the changes through `core.onFrame` as `change` frames (so dedupe and revisions apply), pages while `more`, stops the moment the socket is `live` again, and a 401 moves the status to `waiting-token`. With polling impossible too (network error), the screens keep their normal `refetchOnWindowFocus`/`refetchInterval` (assert nothing throws and the status stays `polling`).
- [ ] **Step 2:** Implement and hook it to the provider: enter `polling` when the hook reports `disconnected`/`error` for 15 s, or on `Hint`; leave on `Ready`.
- [ ] **Step 3: Commit** `feat(realtime-ui): cursor polling when the socket is down or the server asks`.

## Task 6: Resilience scenarios (scripted fake server)

**Files:**
- Create: `ui/src/lib/realtime/resilience.test.tsx`, `fakeServer.ts`

- [ ] **Step 1:** `fakeServer.ts` is a scripted server speaking real protobuf over the fake socket: it can drop the connection, send `Bye{draining, retryAfterMs}`, send frames out of order and duplicated, send `Gap`, send `Tick` ahead of the client, and delay `Ready`.
- [ ] **Step 2: Scenarios, each a test:** (1) drop and reconnect → `Auth` carries the cursor and a replayed change already applied is not applied again; (2) 20 clients get `Bye{draining}` together → their reconnect times are spread (assert the spread exceeds 1 s over 20 instances with a seeded RNG); (3) `Tick` ahead of the cursor → one replay, not a storm; (4) reload (new provider, same storage) resumes from the cursor; (5) `online` event while backing off → immediate attempt (shared hook option exercised); (6) 50 duplicates and 50 reordered changes of 10 entities leave the cache equal to applying the newest of each; (7) a `Gap` after a long offline period refetches active queries once.
- [ ] **Step 3: Commit** `test(realtime-ui): reconnect, drain, gap, tick and reload scenarios`.

## Task 7: One socket per browser

**Files:**
- Create: `ui/src/lib/realtime/leader.ts`, `leader.test.ts`

- [ ] **Step 1: Failing tests** (fake `navigator.locks` and `BroadcastChannel`): of three provider instances for the same principal, exactly one opens a socket; the others receive its frames through the channel and apply them to their own `QueryClient`; when the leader unmounts, one follower takes the lock, connects with the stored cursor and no change is lost or applied twice; with neither API available each instance connects on its own (graceful degradation); a different principal never shares a channel (the channel name includes a hash of the cursor key).
- [ ] **Step 2:** Implement behind `NEXT_PUBLIC_REALTIME_SHARED` (default off until R5's soak says otherwise).
- [ ] **Step 3: Commit** `feat(realtime-ui): one socket per browser via Web Locks and BroadcastChannel (flagged)`.

## Task 8: The indicator and the dev mock

**Files:**
- Create: `ui/src/components/realtime/LiveIndicator.tsx` + test; locale keys; `ui/src/dev/realtimeMock.ts`
- Modify: `ui/src/dev/mockRuntime.ts`

- [ ] **Step 1: Failing tests:** the indicator renders "Ao vivo" for `live`, "Atualizando a cada 10 s" for `polling`, nothing for `off`/`connecting` for less than 3 s, and it is a `role="status"` (polite), never an alert, never blocking.
- [ ] **Step 2:** In mock mode (`USE_MOCK`), `realtimeMock` emits scripted changes on a timer so a developer sees screens update without a backend.
- [ ] **Step 3:** `npx tsc --noEmit && npx eslint src && npx vitest run` — all green. **Commit** `feat(realtime-ui): the live indicator and a dev mock`.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| § 4.7 one connection per tab/browser, codec, cursor, query binding | 1, 2, 3, 4, 7 |
| § 4.7 dedupe, revisions, Gap, Bye variants | 2, 3 |
| § 4.7 reconnect, jitter, online/visible, fallback ladder | 4, 5, 6 |
| § 4.4.6 Reauth in-band | 4 |
| § 4.6 cursor polling fallback | 5 |
| § 5 token never in the URL | 4, 5 |
| § 6 failure modes (drain, gap, offline, blocked WS) | 6 |
| § 9 flag off is today | 4 |
