# Realtime R4 — The surfaces — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The checkout, the console, the portal and Finanças update by themselves when something they show changes, and the checkout no longer uses Server-Sent Events. Each surface goes live behind the flag on its own, in that order.

**Architecture:** Each surface mounts the `RealtimeProvider` from R3 in its layout with the right `Surface` and auth parameters, and lets the existing screens keep their queries: the binding invalidates, the screens refetch through the authorized REST API. Where a screen polled to learn what the socket now tells it, the poll slows to a safety net. The server-side emission sites (R2 Task 4) are verified end to end here, surface by surface.

**Tech Stack:** Next.js 16 static export, React 19, TanStack Query 5, vitest + Testing Library, the R3 fake server; the R2 API in a staging stack for the end-to-end checks.

**Spec:** `docs/specs/2026-10-14-realtime.md` (§1, §4.1, §9). **Depends on:** R2 and R3 merged, `REALTIME_ENABLED` available in staging. **Blocks:** R5.

Repos: `P_BILL/ui` (mount points: `src/app/checkout/layout.tsx`, `src/app/(console)/console/layout.tsx`, `src/app/(portal)/layout.tsx`, `src/app/(finance)/finance/layout.tsx`), `P_BILL/api` (only fixes found by the end-to-end checks).

## Global Constraints

- One surface per PR, in this order: **checkout → console → portal → Finanças**. Each PR is shippable and reversible by the flag alone.
- A screen must still work if the socket never connects (R3's ladder); the end-to-end test of each surface runs it once with the WebSocket blocked.
- The provider takes its principal from what the surface already knows (the console scope, the selected Finanças space, the pay link token); no screen passes a scope.
- No screen shows a blocking state for the socket. The indicator appears only on the checkout.
- ESLint zero warnings; routes and copy rules as in the rest of the UI; no attribution trailers.

## Review Focus

- On the checkout, "pago" appears within about a second of the Pix settling, and the page does not flash "pago" for another invoice when the token changes. Pinned in Task 1.
- The console's open invoice list changes status in place when a payment arrives, without losing the scroll position, the open drawer or a half-typed filter. Pinned in Task 2.
- A merchant with the invoice detail open sees the payer appear (the `payer_recorded` change) without reloading. Pinned in Task 2.
- A customer in the portal never sees another customer's invoice change (the user scope). Pinned in Task 3.
- Finanças "A classificar" loses a line classified in another tab, and the account's count and balance follow. Pinned in Task 4.
- Switching organization or mode in the console, or space in Finanças, never shows a change from the previous one. Pinned in Tasks 2 and 4.
- With the flag off, all four surfaces are byte-for-byte today's behaviour (no socket, SSE still on the checkout). Pinned in Task 5.

---

## Task 1: Checkout (replaces SSE)

**Files:**
- Modify: `ui/src/app/checkout/layout.tsx` (mount), `ui/src/components/portal/PixPanel.tsx` (replace `usePaymentStream`), `ui/src/lib/hooks/usePaymentStream.ts` (kept, used when the flag is off), `PixPanel.test.tsx`
- Create: `ui/src/lib/realtime/useInvoiceSettled.ts`, test

**Interfaces:**
- Produces: `useInvoiceSettled(invoiceId: string, enabled: boolean): StreamStatus` — the same return type as `usePaymentStream` (`"connecting" | "waiting" | "paid" | "lost"`), so `PixPanel` changes one line.

- [ ] **Step 1: Failing tests:** with the flag on, `PixPanel` shows the waiting state, then **"pago" when a `Change{entity:"invoice", type:"invoice.paid", entity_id: <this invoice>}` arrives**; a change for another invoice does nothing; a `payment.failed` for this invoice keeps waiting (the person may try again); when the socket is blocked the polling fallback still ends in "pago" within one interval; with the flag off the SSE hook is used and no socket opens. The `Surface` is `{kind:"checkout", token}` and the first frame carries the pay link, not a bearer.
- [ ] **Step 2: Implement.** `useInvoiceSettled` reads the checkout query (`checkoutKeys.view(token)`): the binding patches/invalidates it on `invoice.paid`; the hook derives `paid` from the refetched invoice status. The Pix code is never in an event; the page already holds it.
- [ ] **Step 3: End-to-end (staging):** open a checkout for a test-mode invoice, pay it with `SimulatePayment`, time the page flip. Record the time in the PR (target ≤ 1.5 s p95 over 20 runs).
- [ ] **Step 4:** Leave `portal_events.go` (the SSE route) and `usePaymentStream` in place; they are removed in R5 after the soak. **Commit** `feat(realtime): the checkout learns of payment over the socket`.

## Task 2: Console

**Files:**
- Modify: `ui/src/app/(console)/console/layout.tsx`, `ui/src/components/console/*` where a screen sets `refetchInterval`
- Create: `ui/src/app/(console)/console/realtime.test.tsx`

- [ ] **Step 1: Failing tests** (R3 fake server, the real screens, a seeded `QueryClient`): (a) the invoices list for the month shows an `OPEN` invoice; a `Change{invoice.paid}` for it flips the row to paid in place, and a row below it keeps its position and the filter text typed in the search box is untouched; (b) with the invoice detail open, `invoice.payer_recorded` makes the payer block appear; (c) a `Change` for tenant B's invoice (delivered by a buggy server in the test) changes **no** cache entry of tenant A (the keys carry org and mode); (d) switching mode `live → test` closes the socket and reopens with no cursor; (e) the overview month totals refetch on an `invoice.*` change; (f) customers, subscriptions and connections screens follow their entities (the Task 3 catalogue rows).
- [ ] **Step 2: Implement.** Mount `<RealtimeProvider surface={{kind:"console", scope}} auth={() => ({token: getAccessToken(), space: selectorOf(scope)})}>` in the console layout. Where a console screen polls only to see payments (`refetchInterval` on the invoice list/detail), keep it but lengthen it to 60 s when `useRealtimeStatus() === "live"`.
- [ ] **Step 3: End-to-end (staging):** two browser sessions of the same org (one watching the list) and one paying through the checkout; the list flips without a reload. A second org's session in a third browser sees nothing (assert on the captured frames). **Commit** `feat(realtime): the console follows its invoices, customers, subscriptions and connections`.

## Task 3: Portal

**Files:**
- Modify: `ui/src/app/(portal)/layout.tsx`
- Create: `ui/src/app/(portal)/realtime.test.tsx`

- [ ] **Step 1: Failing tests:** the portal invoice list and detail update on `invoice.*` for the signed-in user's invoices; the subscription list/detail on `subscription.*`; a change that does not belong to this user (the buggy-server case) changes nothing; the `Surface` is `{kind:"portal"}` and the first frame carries the bearer only (no space).
- [ ] **Step 2: Implement** the mount; the catalogue rows exist from R3. The portal's existing `usePaymentStream` use inside `PixPanel` is already handled by Task 1.
- [ ] **Step 3: End-to-end (staging):** a customer with a user account open in the portal while the invoice is paid from the console: the portal row flips. A second customer's portal stays still. **Commit** `feat(realtime): the portal follows the customer's invoices and subscriptions`.

## Task 4: Finanças

**Files:**
- Modify: `ui/src/app/(finance)/finance/layout.tsx`, `ui/src/components/finance/{AccountsView,UnclassifiedView,BillsView,StatementView}.tsx` only where they pin a poll
- Create: `ui/src/app/(finance)/finance/realtime.test.tsx`

- [ ] **Step 1: Failing tests:** `statement.line_posted` makes the statement list and the account's balance refetch; a line classified from another tab (`bill.updated`/`statement.line_posted`) leaves the "A classificar" list in this tab and updates the unclassified count shown in Contas; `connection.updated` refreshes the connection health card and `payout.updated` the payout state; switching the space (personal ↔ organization) closes the socket and clears the cursor; **the bulk classification running in this tab does not trigger a refetch storm** (the binding coalesces invalidations of the same key within 250 ms).
- [ ] **Step 2: Implement** the mount with `{kind:"finance", mode, space}` and `auth={() => ({token, space: spaceHeader(space)})}`; add the 250 ms coalescing to `applyChange` (a `Map<queryKeyHash, timer>`; test: 100 changes in 50 ms produce one invalidation per distinct key).
- [ ] **Step 3: End-to-end (staging):** run a bulk classification of 20 lines in one tab while a second tab shows the same list; the second tab empties without a reload and without more than a handful of requests. **Commit** `feat(realtime): Finanças follows its statement, bills and connection`.

## Task 5: Contract between the two halves

**Files:**
- Create: `api/tests/integration/realtime_ui_contract_test.go`, `ui/src/lib/realtime/emission.contract.test.ts`, `docs/realtime/events.md`

- [ ] **Step 1:** `docs/realtime/events.md` is the catalogue as a table: every `type` the server emits (`invoice.*`, `payment.*`, `checkout.session.*`, `subscription.*`, `credit_note.created`, `invoice.payer_recorded`, `payout.updated`, `connection.updated`, `statement.line_posted`, `bill.updated`, `customer.updated`), its `entity`, its scopes, and the UI keys it touches.
- [ ] **Step 2: Failing tests, one on each side of the same list:** the Go test parses `events.md` and asserts the set of types the emitter can produce equals the table (a new emission site without a table row fails); the TypeScript test parses the same file and asserts `effectsOf` has an entry or the documented fallback for every row (a row with no UI mapping fails).
- [ ] **Step 3: Flag-off check:** with the flag off the API test suite writes no `RT#` item and the UI renders no provider (already pinned in R2/R3; assert once more here across all four layouts).
- [ ] **Step 4: Commit** `test(realtime): the event catalogue is one list both halves are checked against`.

## Rollout order and acceptance

| Surface | Staging soak | Production | Acceptance |
|---|---|---|---|
| Checkout | 2 days | flag on for the checkout only (`NEXT_PUBLIC_REALTIME_SURFACES=checkout`) | payment shown ≤ 1.5 s p95; no `Gap` storm; SSE route traffic falling to ~0 |
| Console | 3 days | + console | list flips in place; no cross-tenant frame in logs |
| Portal | 3 days | + portal | same |
| Finanças | 3 days | + finance | bulk classification does not storm |

(`NEXT_PUBLIC_REALTIME_SURFACES` is a comma list read by the provider; a surface not in the list mounts nothing.)

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| § 1 checkout, console, portal, Finanças update by themselves | 1, 2, 3, 4 |
| D4 the checkout leaves SSE in the first delivery | 1 (SSE removed after the soak, R5) |
| § 4.1 scopes per surface | 1–4 (surface → scope kind) |
| § 5 one customer never sees another's change | 2, 3 (+ R2 Task 10) |
| § 9 rollout order, flag | rollout table, 5 |
