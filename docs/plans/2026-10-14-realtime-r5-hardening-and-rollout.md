# Realtime R5 — Hardening, observability and rollout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove, with numbers and with induced failures, that the realtime system holds under load, loses nothing when instances, Valkey or the network fail, leaks nothing across principals, can be operated by reading one runbook, and then retire the Server-Sent Events route it replaces.

**Architecture:** A load tool built on R2's `cmd/rtprobe`, a chaos script that breaks one component at a time against staging while checking an end-to-end invariant ("every change committed is observed exactly once by every authorized client, or the client was told to refetch"), fuzz and abuse tests in CI, metrics and alarms on the series R2 emits, a runbook, a canary per surface, and the removal of the old path after a soak.

**Tech Stack:** Go (`cmd/rtload`), shell + `aws` for fault injection, Go native fuzzing, vitest, CloudWatch alarms through the existing alerts topic, Terraform for the alarms.

**Spec:** `docs/specs/2026-10-14-realtime.md` (§5, §6, §8, §9, §10). **Depends on:** R2, R3, R4 merged. **Blocks:** nothing; it closes the work.

Repos: `P_BILL` (api, ui, terraform, docs).

## Global Constraints

- Chaos and load run against **staging** only; a production run is a read-only canary.
- The invariant is checked by the tool, not eyeballed: a run that cannot state its result as pass/fail is not a result.
- No test weakens a production control (caps, deadlines, origin checks) to get a number; a test config may shorten a timer, never remove a check.
- Every alarm names its runbook section.
- No attribution trailers; docs under `docs/specs|plans/` and `docs/runbooks/`.

## Review Focus

- A change committed while the publishing instance is killed is delivered after the clients reconnect, exactly once to each authorized client, or they are told `Gap`. Pinned in Task 2.
- With Valkey down the clients keep updating (polling) and recover without a reload when it returns. Pinned in Task 2.
- A deploy with 2,000 connected clients does not make the survivors fail: the reconnect spread keeps the new instance's CPU and DynamoDB reads under the limits. Pinned in Task 2.
- Garbage, oversized, replayed and flooded input closes the offender and never the server. Pinned in Task 3.
- No log line, metric label or error body contains a token, a pay link, or a scope key with a user id in clear. Pinned in Task 3.
- Turning the flag off while 1,000 clients are connected closes them cleanly and the screens fall back to polling. Pinned in Task 5.
- After the SSE route is removed, nothing in the UI or the API references it. Pinned in Task 7.

---

## Task 1: Load

**Files:**
- Create: `api/cmd/rtload/main.go`, `docs/realtime/capacity.md`

- [ ] **Step 1:** Extend the R2 probe into `rtload`: `-conns N -scopes-per-conn K -principals P -rate R/s`. It opens N authenticated connections (test-mode tokens minted for synthetic tenants seeded by the tool), registers P principals, and a publisher thread writes R changes/s through the real API path (a synthetic mutation endpoint is **not** added; the tool drives real invoice transitions on the seeded tenants). Each connection records the publish→receive latency using `occurred_at_ms`.
- [ ] **Step 2: Run the ladder on one staging instance:** 500, 2,000, 5,000, 10,000 connections at 5, 50 and 200 changes/s. For each point record: connections held, memory per connection (instance RSS / connections), CPU, publish→receive p50/p95/p99, DynamoDB consumed units, Valkey messages/s, send-queue overflows.
- [ ] **Step 3: Acceptance** (written in `capacity.md` before the run, edited only with a reason): 5,000 connections per instance at 50 changes/s with p95 ≤ 500 ms, zero slow-consumer closes under normal load, memory ≤ 100 KiB per connection, CPU ≤ 60 %. Record the cap (`REALTIME_MAX_CONNECTIONS_PER_INSTANCE`) the numbers support, with 30 % headroom, and set the alarm threshold to 80 % of it.
- [ ] **Step 4:** If the numbers are missed, the finding is a task (queue sizes, a mutex, an allocation in the write path) fixed and re-run before continuing; if 10,000 per instance is out of reach, the answer is another instance of the same ASG (spec § 10), not a new component.
- [ ] **Commit** `test(realtime): rtload and the capacity numbers`.

## Task 2: Chaos, with an invariant

**Files:**
- Create: `api/cmd/rtchaos/main.go`, `scripts/realtime-chaos.sh`, `docs/realtime/chaos.md`

The checker: `rtchaos` connects K clients per scope kind, drives M sequential numbered mutations (each carries a sequence number in a field the checker can read back from the REST API, e.g. an invoice's `metadata.seq`), injects one fault, then waits and asserts **for every client: the set of sequence numbers it observed through changes (or, after a `Gap`, through the refetch) equals the set committed** — no loss, no duplicate application, no foreign scope.

- [ ] **Step 1: Scenarios, each a script step + a checker run (pass/fail printed):**
  1. **Kill the app process** on the instance the clients are on (`systemctl kill`), mid-stream.
  2. **Stop the instance** (ASG replaces it) with clients on both instances.
  3. **Valkey unreachable** for 3 minutes (security group deny from the app instances, restored after): clients must show `polling`, keep updating within 10 s, and return to `live`; publish-error alarm must fire.
  4. **Valkey flush** (loses subscriptions state): clients recover on the next `Tick`.
  5. **Network partition** between clients and the edge for 90 s (client-side `iptables`/`tc`): reconnect, resume from the cursor, no loss.
  6. **DynamoDB throttling** on the change-log reads (a test hook that makes `Since` fail 50 % of the time): replay answers `Gap`, clients refetch, final state correct.
  7. **Deploy during traffic** (a real `deploy.yml` run on staging with 2,000 clients): `Bye{draining}`, jittered reconnect, no `Gap` expected, instance CPU under limit while they return.
  8. **Slow consumer:** one client reads at 1 frame/s; it is closed with `Gap`; the others unaffected (latency unchanged within 10 %).
  9. **Clock skew:** one instance's clock 3 s behind; no change lost (the 5 s overlap).
  10. **Token expiry at scale:** 1,000 clients with 2-minute tokens and silent refresh; none reconnect needlessly (count `Reauth` vs new sockets).
- [ ] **Step 2:** Run all ten, twice. Failures become fixes; the script's final output is the table of results pasted into `chaos.md` with the date and commit.
- [ ] **Commit** `test(realtime): chaos scenarios with a no-loss invariant`.

## Task 3: Abuse, fuzz and secrecy (in CI)

**Files:**
- Create: `api/internal/realtime/gateway_abuse_test.go`, `api/internal/realtime/wire/fuzz_test.go`, `ui/src/lib/realtime/codec.fuzz.test.ts`, `api/internal/realtime/logscrub_test.go`

- [ ] **Step 1: Failing tests, then fixes:**
  - **Fuzz** (`go test -fuzz` for 60 s in a nightly job; a seed corpus runs in every CI run): `ClientFrame` decoding and the gateway's frame handler never panic and never allocate more than 2× the frame size; the TypeScript `decode` never throws anything but its documented error on random bytes (property test with `fast-check` only if already a dependency, else 10,000 seeded random buffers).
  - **Abuse:** a 1 MiB first frame is closed immediately; 10,000 pings in a second closes the connection (20 frames/s cap); a valid `Auth` replayed on a second socket after the first closed works only as a new authentication (no session token to steal); 40 connections from one IP, 6 from one principal are refused with `Bye{limit}`; a client that never reads is closed (Task 5 of R2); a half-open TCP connection is reaped by the pong deadline.
  - **Origin and CSWSH:** the Poker gateway's origin cases, adapted.
  - **Secrecy:** with a logging sink capturing every line during the full gateway test run, no captured line contains the test token, the pay link, or a user id outside a hashed form; metrics labels contain scope *kinds* only (never keys).
- [ ] **Step 2:** The nightly fuzz job is added to the existing CI as its own workflow (`realtime-fuzz.yml`, scheduled), not in the PR path.
- [ ] **Commit** `test(realtime): abuse, fuzz and log-secrecy tests`.

## Task 4: Observability and the runbook

**Files:**
- Create: `terraform/billing/realtime_alarms.tf`, `docs/runbooks/realtime.md`
- Modify: `api/internal/realtime/metrics.go` if a series is missing

- [ ] **Step 1: Alarms** (through the existing alerts SNS topic, each with the runbook anchor in its description): publish errors > 0 for 5 min; publish→write p95 > 1 s for 10 min; `Gap` rate > 1 % of connections/hour; connections ≥ 80 % of the cap; auth failures spike (> 5× the hourly median); fallback-poll requests > 20 % of active clients (the edge or Valkey is unwell); zero connections for 15 min during business hours while the flag is on (the gateway is down).
- [ ] **Step 2: Runbook sections:** how the system works in one page; reading the metrics; "Gap rate is up", "publish errors", "clients stuck polling", "a customer says the screen did not update" (check order: flag, edge, cursor, scope, change log row); how to turn it off (`REALTIME_ENABLED=false` and redeploy, or per surface via `NEXT_PUBLIC_REALTIME_SURFACES`); how to drain; how to read a change log partition (`aws dynamodb query` on `RT#…`); how to replay for one user.
- [ ] **Step 3: Failing test for the alarms:** a Terraform test (`tests/realtime.tftest.hcl`) that every alarm has a description containing `docs/runbooks/realtime.md#`.
- [ ] **Commit** `chore(realtime): alarms and the runbook`.

## Task 5: Kill switches that actually kill

**Files:**
- Modify: provider gating in `RealtimeProvider`, API config read

- [ ] **Step 1: Failing tests:** turning `REALTIME_ENABLED` off at the API (restart) closes every connection with `Bye{draining}`-class behaviour and the clients fall to polling, which then answers 404 → the clients go to the normal screens' behaviour without error banners; turning one surface off in `NEXT_PUBLIC_REALTIME_SURFACES` mounts nothing for it on the next load; a stale UI build with the flag on talking to an API with it off degrades the same way.
- [ ] **Step 2:** Rehearse the rollback in staging with 1,000 clients and record the time to quiet.
- [ ] **Commit** `test(realtime): the flags turn the system off cleanly`.

## Task 6: Production canary

- [ ] **Step 1:** Enable per surface in the order of R4's table with the acceptance column checked from the dashboards, not from impressions. Each step waits the stated soak. A rollback trigger is written before each step (for example: `Gap` rate > 3 %, p95 > 1.5 s, any cross-scope frame found in the abuse logs).
- [ ] **Step 2:** Measure the real cost for one week (DynamoDB units for `RT#` writes and replays, Valkey messages, instance memory) and put the numbers in `docs/realtime/capacity.md`. The spec's § 10 estimate is replaced by them.
- [ ] **Step 3:** The owner is told, with numbers, before each production step and after.

## Task 7: Retire the old path

**Files:**
- Delete: `api/internal/api/v1/portal_events.go` (the SSE route) and its tests, `ui/src/lib/hooks/usePaymentStream.ts` and its tests; `api/internal/settlement/` if nothing else uses the bus
- Modify: `PixPanel.tsx` (drop the flag branch), `router.go`, docs

- [ ] **Step 1: Precondition (checked, not assumed):** the checkout has run on the socket in production for 14 days; SSE route traffic is ~0 over the last 7 days (access log count pasted in the PR).
- [ ] **Step 2: Failing test first:** a repository-wide test (`grep`-style Go test and an ESLint `no-restricted-imports` rule) that fails while any reference to `usePaymentStream`, `invoiceEvents` or the settlement bus remains; then delete until it passes.
- [ ] **Step 3:** If `settlement.Bus` still serves another caller, keep it and say which; otherwise remove it with its Valkey channel naming.
- [ ] **Step 4:** `docs/adr/0031-realtime.md`: the decision, the alternatives (SSE only, polling only, DynamoDB Streams to a push service), and why the log-plus-cursor design won. Update `ARCHITECTURE.md` and `CLAUDE.md`'s pointer list.
- [ ] **Commit** `chore(realtime): remove the SSE payment stream; ADR 0031`.

## Done means

The ten chaos scenarios pass twice; the capacity numbers are recorded and the cap set; the abuse, fuzz and secrecy tests run in CI; alarms exist with runbook links; the four surfaces run in production for their soaks without hitting a rollback trigger; the SSE path is gone; the cost is measured and reported to the owner.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| § 5 security controls proved | 3 (+ R2 Task 10) |
| § 6 every failure mode exercised | 2 |
| § 8 metrics and alarms | 4 |
| § 9 flag, canary, SSE removal | 5, 6, 7 |
| § 10 cost measured | 1, 6 |
| Multi-instance resilience | 2 (scenarios 1, 2, 4, 7) |
| Reconnect resilience | 2 (scenarios 5, 7, 10) |
