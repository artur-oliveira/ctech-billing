# Realtime R2 — The server — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Billing writes a change log in the same transaction as every important mutation, publishes it after commit to the connections that are allowed to hear it (across instances), serves it from a cursor over a WebSocket and over HTTP, and refuses every connection that is not authorized for a scope, all behind `REALTIME_ENABLED`.

**Architecture:** `internal/realtime` holds the scope keys, the authorizers, the hub (connections, bounded queues, fan-out through `ctech-go-common/ws`), the gateway (`GET /v1.0/realtime`) and the changes API (`GET /v1.0/realtime/changes`). The change log is `RT#{scope}` items written by the repositories' transitions beside the existing webhook `Event` rows. The Poker gateway is the pattern for upgrade, first-frame auth, origin, caps, heartbeat and drain.

**Tech Stack:** Go (Fiber v3, `fasthttp/websocket`, DynamoDB, Valkey), `ctech-go-common/{ws,drain}`, the generated `wire` package from R1.

**Spec:** `docs/specs/2026-10-14-realtime.md` (§4.1–4.4, §4.6, §5, §6, §8). **Depends on:** R1. **Blocks:** R3 (client), R4 (surfaces).

Repos: `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing/api`. Pattern sources: `ctech-poker/api/internal/api/v1/tablews.go` (gateway), `ctech-wallet/api/internal/api/v1/ws.go` (Fiber + fasthttp/websocket), `ctech-go-common/ws` and `drain`, `internal/repositories/transition.go` (`buildEventWrites`).

## Global Constraints

- Flag `REALTIME_ENABLED` (default `false`). Off: the routes are not mounted, no `RT#` item is written, nothing publishes. Billing behaves exactly as today.
- **Scopes are derived by the server from the verified principal.** No code path takes a scope name from the client.
- The mode (`live`/`test`) is in every scope key.
- An event carries ids and state only (R1's field-set test enforces the wire; the log items carry the same).
- A publish failure never fails the mutation. The change log write is part of the mutation's transaction and fails it with it.
- Writes to a WebSocket are serialized per connection (`fasthttp/websocket` panics on concurrent writes). Every post-upgrade goroutine recovers its own panics.
- Constants for scope prefixes, close reasons, header names and limits; RFC 7807 problems on the HTTP routes; no attribution trailers; docs under `docs/specs|plans/`.

## Review Focus

- A principal with two scopes of the same kind in two modes hears only the mode of its token. Pinned in Task 2 and Task 10.
- A change whose publish was lost is still delivered by the next reconnect's replay, exactly once (client-side dedupe aside). Pinned in Task 6.
- A subscriber slower than the producer is closed with a `Gap`, and never blocks the publisher or another connection. Pinned in Task 5.
- The replay window overlaps the cursor by 5 s so two instances' clock skew cannot hide a change. Pinned in Task 3.
- A token that expires mid-connection closes the socket (`token_expired`) unless a `Reauth` arrived. Pinned in Task 6.
- A member removed from the organization stops hearing within 5 minutes. Pinned in Task 6.
- A checkout link for invoice A never receives a change for invoice B. Pinned in Task 2 and Task 10.
- With the flag off, `GET /v1.0/realtime` is a 404 and a mutation writes no `RT#` item. Pinned in Task 9.

---

## Task 1: The edge carries a WebSocket (do this first)

The one unknown that could change the design. Everything else assumes it.

**Files:**
- Create: `api/internal/realtime/probe.go`, `api/cmd/rtprobe/main.go`
- Modify: `api/internal/api/v1/router.go` (mount, flagged)

- [ ] **Step 1:** Add `GET /v1.0/realtime/probe` behind `REALTIME_PROBE=true` (a separate, throwaway flag): upgrade; read one frame under 5 s; answer an echo; send a native ping every 25 s; close after 5 minutes. No auth beyond the first frame being non-empty. It exists only to prove the path.
- [ ] **Step 2:** `cmd/rtprobe`: dials `wss://billing-api.aoctech.app/v1.0/realtime/probe` with an allowed Origin, sends one frame, then **holds the connection for 4 minutes** replying to pings, and reports: upgrade status, first-echo latency, whether it stayed open the whole time, and the close code.
- [ ] **Step 3:** Deploy with the probe flag on **in stage first** (then prod only if stage passes). Run the probe from outside the VPC (the operator's machine). Expected: upgrade 101, held 4 minutes. If the upgrade is refused or closed at ~60 s, the cause is nginx (`proxy_http_version 1.1`, `Upgrade`/`Connection` headers, `proxy_read_timeout`) or the load balancer / Cloudflare idle timeout; fix it in the shared nginx setup the bootstrap uses (`setup-nginx.sh` in ctech-cdk) for this host only, and re-run.
- [ ] **Step 4:** Record the result and the timeouts found in `docs/realtime/protocol.md` ("Edge"). **Remove the probe route and the flag** in the PR that closes this task; keep `cmd/rtprobe` (the load tool in R5 reuses it).
- [ ] **Commit** `chore(realtime): prove the edge passes a WebSocket and holds it`.

## Task 2: Scope keys and authorizers (pure code)

**Files:**
- Create: `api/internal/realtime/scope.go`, `scope_test.go`, `authorize.go`, `authorize_test.go`

**Interfaces:**
- Produces:
```go
package realtime

type Mode bool // live = true
func TenantScope(org string, live bool) string          // "tenant:{org}:{live|test}"
func SpaceScope(spaceKey string) string                  // "space:{spaceKey}"        (spaceKey already carries the mode)
func UserScope(sub string) string                        // "user:{sub}"
func InvoiceScope(org string, live bool, invoiceID string) string // "invoice:{org}:{mode}:{id}"
func Kind(scope string) string                           // "tenant" | "space" | "user" | "invoice"

type Principal struct {
	Kind     string    // "console" | "portal" | "checkout"
	Sub      string
	Scopes   []string
	ExpiresAt time.Time
}

// Authorizer turns a verified credential into a Principal. It uses the same
// resolvers as the REST routes and never reads a scope from the client.
type Authorizer interface {
	Authorize(ctx context.Context, a *wire.Auth) (*Principal, error)
	// Recheck answers whether the principal still holds its scopes (membership).
	Recheck(ctx context.Context, p *Principal) (bool, error)
}
```

- [ ] **Step 1: Write the failing tests** (`scope_test.go`): the keys are exactly the documented shapes; `TenantScope("o1", true) != TenantScope("o1", false)`; `Kind` round-trips; `InvoiceScope` embeds org and mode.
- [ ] **Step 2: Write the isolation matrix test** (`authorize_test.go`) against fake resolvers: principals {console member of org A live, console member of org A test, console member of org B, portal user U1, portal user U2, checkout link for invoice 1 (org A), checkout link for invoice 2} × change scopes {tenant A live, tenant A test, tenant B live, user U1, user U2, invoice 1, invoice 2}. For each cell assert `delivered == (scope ∈ principal.Scopes)` and that the expected diagonal holds:

```go
func TestNoPrincipalIsGrantedAScopeItDoesNotOwn(t *testing.T) {
	for _, p := range principals(t) {
		for _, s := range allScopes {
			want := owns(p.name, s)
			if got := slices.Contains(p.principal.Scopes, s); got != want {
				t.Errorf("%s / %s: granted=%v, want %v", p.name, s, got, want)
			}
		}
	}
}
```

- [ ] **Step 3: Implement** the keys, then three authorizers behind one `Authorizer`:
  - **console:** verify the JWT with the existing verifier; resolve the space from `Auth.space` through `space.Resolver` exactly as `middleware.ResolveSpace` does (require a console member with `finance.read` for `space:` and any member with `ScopeInvoicesRead`-equivalent role for `tenant:`); scopes = `tenant:{org}:{mode}` and `space:{key}`. The mode comes from the resolved space, not from a request field.
  - **portal:** verify the JWT; scopes = `user:{sub}`.
  - **checkout:** `services.PayLink.Parse(auth.PayLink)` → `(org, live, invoiceID)`; scopes = `invoice:{org}:{mode}:{id}`; `ExpiresAt` = now + 35 min (re-authorized by a new connection, as the SSE cap was).
- [ ] **Step 5:** `go test ./internal/realtime/ -run 'Scope|Authorize' -v` — PASS. **Commit** `feat(realtime): scope keys and the authorizers`.

## Task 3: The change log

**Files:**
- Create: `api/internal/repositories/realtime_log.go`, `realtime_log_test.go` (integration, DynamoDB local)
- Modify: `api/internal/repositories/transition.go` (call the writer beside `buildEventWrites`)

**Interfaces:**
- Produces:
```go
type Change struct {
	ID, Type, Entity, EntityID, Status string
	Revision uint64
	OccurredAt time.Time
	Scopes []string
}
// changeWrites renders one RT item per scope for a transaction (nil when the log is off).
func changeWrites(base Base, c Change, now time.Time) ([]types.TransactWriteItem, error)
func (r *RealtimeRepository) Since(ctx context.Context, scopes []string, cursor string, limit int) (changes []Change, more bool, err error)
func (r *RealtimeRepository) Head(ctx context.Context, scopes []string) (string, error)
```
Item shape: `pk = "RT#"+scope`, `sk = ID` (a ULID), TTL 48 h, plus the fields above.

- [ ] **Step 1: Failing integration tests** (`realtime_log_test.go`): (a) a transaction that writes a change and fails on a condition leaves **no** `RT#` item; (b) `Since(scopes, cursor)` returns the changes after the cursor in order, **minus nothing and plus those within the 5-second overlap**, deduplicated by id across scopes; (c) `limit` + `more=true`; (d) items for scope A are invisible to a query for scope B; (e) `Head` returns the newest id across the scopes.
- [ ] **Step 2: Implement.** `Since` queries each scope's partition with `sk > overlap(cursor)` where `overlap` subtracts 5 s from the cursor's embedded timestamp (the ULID's first 48 bits), merges by id, caps at `limit`.
- [ ] **Step 3: Wire it into `Transition`** (`transition.go`): `StatusChange` gains `Realtime *Change` (nil = none). `Transition` appends `changeWrites(...)` to `writes` next to `buildEventWrites`. A repository whose `RealtimeEnabled` is false skips it.
- [ ] **Step 4:** `go test -tags integration ./tests/integration/ -run RealtimeLog` — PASS. **Commit** `feat(realtime): the change log, written in the mutation's transaction`.

## Task 4: Emission sites and the publisher

**Files:**
- Create: `api/internal/realtime/emit.go`, `emit_test.go`, `publisher.go`
- Modify: the callers of `Transition` for invoices, payment attempts, subscriptions, checkout sessions, credit notes (`grep -rn "StatusChange{" internal --include=*.go`), and the explicit sites listed below

**Interfaces:**
- Produces:
```go
// Emitter builds a Change for an entity's transition, resolving its scopes.
type Emitter interface {
	ForInvoice(ctx context.Context, inv *billing.Invoice, typ billing.EventType) repositories.Change
	ForAttempt(ctx context.Context, a *billing.PaymentAttempt, typ billing.EventType) repositories.Change
	ForSubscription(ctx context.Context, sub *billing.Subscription, typ billing.EventType) repositories.Change
	ForCheckout(ctx context.Context, cs *billing.CheckoutSession, typ billing.EventType) repositories.Change
	ForCreditNote(ctx context.Context, cn *billing.CreditNote, inv *billing.Invoice) repositories.Change
	// Free-standing changes (not a transition): customer, payer recorded, connection/payout state, bills, statement lines.
	Free(entity, id, typ, status string, scopes ...string) repositories.Change
}
type Publisher interface{ Publish(ctx context.Context, changes ...repositories.Change) }
```
- Scope resolution: an invoice's change goes to `tenant:{org}:{mode}`, `invoice:{org}:{mode}:{id}`, and `user:{customer.UserID}` when the customer has a user (customer read cached 60 s in process; a customer with no user simply has no user scope). Finanças entities go to `space:{spaceKey}` only.

- [ ] **Step 1: Failing tests** (`emit_test.go`): an invoice paid for a customer with a user yields exactly the three scopes; for a customer with none, two; a test-mode invoice yields `…:test` keys only; a finance bill yields only the space scope; no `Change` field ever holds a name, an amount or a document (assert `Status` is a status word, the `Type` is in `billing.WebhookEventTypes()` or the realtime-only list).
- [ ] **Step 2: Implement** the emitter and the publisher (`Publish` encodes a `wire.ServerFrame{Change}` and calls `ws.Registry.Broadcast(ctx, scope, bytes)` once per scope; errors are logged and counted, never returned).
- [ ] **Step 3: Wire the transitions.** At each `StatusChange{…}` construction, set `Realtime: emitter.ForX(...)`; after the transaction returns nil, call `publisher.Publish(change)` (put this in the repository method that already runs after commit, e.g. the end of `InvoiceRepository.Transition`). Realtime-only types to add to `billing` (not subscribable as webhooks): `invoice.payer_recorded`, `payout.updated`, `connection.updated`, `statement.line_posted`, `bill.updated`, `customer.updated`.
- [ ] **Step 4: Explicit emitters** (each in the service that owns the mutation, written in the mutation's transaction where one exists, otherwise written then published): payer recorded (`PaymentRepository.SetPayerOnce`), payout settled (`SettlePayout`), connection state/sync (`ByokRepository.SaveConnection`), a statement line posted or linked (`LedgerRepository.PostBankLine`/`LinkBankLine`), bill create/settle/cancel, customer create/update.
- [ ] **Step 5:** `go test ./internal/realtime/... ./internal/repositories/...` and the integration suite — PASS; the existing tests need no change (the emitter is nil in them). **Commit** `feat(realtime): emit changes from every transition and the explicit mutations`.

## Task 5: The hub

**Files:**
- Create: `api/internal/realtime/hub.go`, `hub_test.go`, `conn.go`

**Interfaces:**
- Produces:
```go
type Hub struct{ /* registry ws.Registry, conns by id, limits, metrics */ }
func NewHub(reg ws.Registry, lim Limits, m *Metrics) *Hub
func (h *Hub) Add(c *Conn, scopes []string) error      // registers under each scope; ErrLimit when a cap is hit
func (h *Hub) Remove(c *Conn)
type Conn struct{ ID string; Principal *Principal; /* send queue (256), write mutex, close func */ }
func (c *Conn) Send(frame []byte) bool                 // false when the queue is full
```

- [ ] **Step 1: Failing tests** (`hub_test.go`, with `ws.NewMemoryRegistry()` and a fake socket):
  - A frame broadcast on scope S reaches exactly the connections registered under S (three connections, two scopes).
  - **Slow consumer:** a connection whose `Send` is never drained is closed with a `Gap{slow_consumer}` after 256 queued frames, **while a second connection on the same scope keeps receiving without delay** (assert via timestamps/ordering, with a 1 s deadline).
  - Writes to one connection are never concurrent (race detector: `go test -race`).
  - Caps: the 6th connection of one principal, the 31st of one IP, are refused with `ErrLimit`.
- [ ] **Step 2: Implement.** Each connection has one writer goroutine draining its queue and writing with a deadline (`wsWriteWait` 5 s); `Send` is non-blocking. `Hub.Add` registers the connection's adapter under each scope with the registry; `Remove` unregisters from every scope and cancels the writer.
- [ ] **Step 3:** `go test -race ./internal/realtime/ -run Hub -v` — PASS. **Commit** `feat(realtime): the hub (scoped fan-out, bounded queues, caps)`.

## Task 6: The gateway

**Files:**
- Create: `api/internal/realtime/gateway.go`, `gateway_test.go`
- Modify: `api/internal/api/v1/router.go`, `api/internal/app/app.go`

**Interfaces:**
- Produces: `func RegisterGateway(router fiber.Router, gw *Gateway)` mounting `GET /realtime`; `Gateway{Authorizers map[string]Authorizer, Hub *Hub, Log *RealtimeRepository, AllowedOrigins []string, Drain *drain.Tracker, Now func() time.Time}`.

- [ ] **Step 1: Failing tests** (`gateway_test.go`, a real `fasthttp` server with a `fasthttp/websocket` client; fake authorizer and an in-memory log):
  1. **First-frame auth only:** a connection that sends nothing is closed within 5 s (use a 200 ms deadline in the test config); the upgrade request carries no credential; a first frame over 32 KiB is closed.
  2. **Origin:** a listed origin connects; an unlisted one and a missing one (with an allow-list configured) are refused; no allow-list allows all (dev).
  3. **Ready then replay:** with `resume_cursor` set and three changes after it, the client receives `Ready`, then three `Change` frames in order, then live frames. A cursor older than the TTL, or more than 500 changes, yields one `Gap` and then live frames.
  4. **Auth failure** yields `Bye{reason:"unauthorized"}` and a close, never a hang.
  5. **Heartbeat:** `Ping` is answered by `Pong`; `Tick{head}` is sent on the interval; a client that never answers a native ping is closed after the pong deadline.
  6. **Expiry:** a principal with `ExpiresAt` 300 ms ahead is closed with `Bye{token_expired}`, unless a `Reauth` with a new expiry arrives first, in which case the connection lives on.
  7. **Revocation:** after `Recheck` starts answering false, the next recheck tick (shortened in test) closes with `Bye{revoked}`.
  8. **Lost publish:** a change written to the log but never published reaches a client that reconnects with its previous cursor, **once**.
  9. **Drain:** `Drain.Drain(ctx)` sends `Bye{draining, retry_after_ms}` to every connection, then closes.
  10. **Replay limit:** a 4th replay request within a minute answers `Gap{replay_limit}`.
- [ ] **Step 2: Implement** following the Poker gateway's structure: upgrade (`ReadBufferSize` 1024, `WriteBufferSize` 1024, read limit 32 KiB), `recover` in the post-upgrade goroutine, `readAuth` under `wsAuthTimeout`, authorize, `Hub.Add`, `Ready`, replay, the read loop (Ping/Reauth), a ticker for `Tick`, a timer for expiry, a ticker for `Recheck` (5 min), deferred `Hub.Remove`. Close codes: 1000 normal, 1001 draining, 1008 unauthorized, 1011 server error.
- [ ] **Step 3:** `go test -race ./internal/realtime/ -run Gateway -v` — PASS. **Commit** `feat(realtime): the gateway (auth, replay, heartbeat, reauth, drain)`.

## Task 7: The changes API (the fallback)

**Files:**
- Create: `api/internal/realtime/changes_http.go`, test
- Modify: `router.go`

- [ ] **Step 1: Failing tests:** `GET /v1.0/realtime/changes?cursor=&limit=` with `Authorization: Bearer <jwt>` (console/portal) or `Authorization: PayLink <token>` (checkout) returns `{changes:[…], head:"…", more:bool}` for the principal's scopes only; no `Authorization` → 401; a query-string credential is ignored (401); `limit` is capped at 200; the response is `Cache-Control: no-store`; the same isolation matrix as Task 2 holds over HTTP.
- [ ] **Step 2: Implement** with the same `Authorizer`s and `RealtimeRepository.Since`. JSON, not protobuf (it is the fallback and is debuggable): the field names are the proto's.
- [ ] **Step 3:** PASS. **Commit** `feat(realtime): GET /v1.0/realtime/changes (cursor fallback)`.

## Task 8: Degraded mode, metrics, drain wiring

**Files:**
- Create: `api/internal/realtime/metrics.go`, `degraded.go`
- Modify: `app.go`

- [ ] **Step 1: Failing tests:** when the registry's subscription is down (a fake registry whose `Start` failed), the gateway sends `Hint{poll_interval_ms: 10000}` after `Ready` and on every `Tick`; when it recovers the hint stops; `Metrics` counters/histograms exist and increase (connections by kind, auth failures by reason, `Bye` by reason, replays and sizes, `Gap` by reason, slow-consumer closes, publish errors, publish→write latency).
- [ ] **Step 2: Implement** metrics through the project's `observability` package; the publish→write latency is `now − Change.occurred_at_ms` observed at the socket write.
- [ ] **Step 3:** Register the hub with the existing drain tracker in `app.go` so the OnStop hook drains it with the others.
- [ ] **Step 4: Commit** `feat(realtime): degraded hint, metrics, drain`.

## Task 9: Flag, config, wiring

**Files:**
- Modify: `api/internal/config/config.go` (`REALTIME_ENABLED`, `REALTIME_ALLOWED_ORIGINS` defaults to the CORS list), `api/internal/app/app.go`, `terraform/billing/…` (env var, default off)

- [ ] **Step 1: Failing tests:** with the flag off, `GET /v1.0/realtime` and `/changes` are 404 and a transition writes no `RT#` item (assert by querying the table); with it on, both exist.
- [ ] **Step 2: Implement** and wire the emitter (nil when off) into the repositories.
- [ ] **Step 3: Commit** `feat(realtime): REALTIME_ENABLED wiring (off by default)`.

## Task 10: The isolation suite, end to end

**Files:**
- Create: `api/tests/integration/realtime_isolation_test.go`

- [ ] **Step 1:** Using the real gateway, hub, in-memory registry and DynamoDB local: create two tenants (A, B) each with a live and a test mode, two portal users, and two invoices; connect one socket per principal (console A live, console A test, console B live, portal U1, portal U2, checkout 1, checkout 2) and record every frame each receives. Mutate: pay invoice 1 (tenant A live, customer U1), pay invoice 2 (tenant B live, customer U2), pay an invoice in A test, post a Finanças bill in space A.
- [ ] **Step 2: Assert the full matrix** — each socket receives exactly the changes whose scope it owns and nothing else — and that no frame, in any socket, contains the other tenant's ids (`bytes.Contains` over all captured frames).
- [ ] **Step 3: Randomized pass:** 200 random (principal, mutation) draws from a seeded generator; the delivered set must equal the authorized set every time; the seed is logged on failure.
- [ ] **Step 4: Commit** `test(realtime): isolation across tenants, modes, users and links`.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| § 4.1 scopes, mode in every key | 2 |
| § 4.2 outbox in the mutation's transaction, 5 s overlap, 48 h | 3, 4 |
| § 4.3 publish after commit, failure never fails the mutation | 4 |
| § 4.4 gateway: first-frame auth, origin, Ready, replay, heartbeat, Reauth, recheck, backpressure, limits, drain | 5, 6 |
| § 4.6 cursor fallback | 7 |
| § 5 security controls and tests | 2, 6, 7, 10 |
| § 6 Valkey down → Hint; lost publish → replay | 6, 8 |
| § 8 metrics | 8 |
| § 9 flag | 9 |
| § 11 open item: the edge | 1 |
