# Wallet ledger — P4: Poker owns catalogues, Sandbox sales and gambling rules — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctech-poker` sells Sandbox packs and premium goods by Pix through Billing invoices with its own price catalogue, credits Sandbox in Wallet when an invoice is paid, and enforces consent, personal limits and self-exclusion itself. Wallet keeps only generic holds on `real` and a Sandbox credit route.

**Architecture:** Poker adds a `billingclient` (its own OAuth client), a `catalog` package that owns the SKU tables copied from Wallet, and a `purchasegw` adapter that implements the **same** small `wallet` interfaces the existing `sandboxpurchase`, `reactionpurchase` and `cosmeticpurchase` services already depend on, so those services and the purchase lifecycle stay as they are. A `responsible` package takes over the gambling state (consent, limits, self-exclusion) from Wallet, imported once from a Wallet export route, and Poker's real-money buy-in checks and meters it. Wallet only gains generic hold routes.

**Tech Stack:** Go (Fiber v3), DynamoDB, `api-commons/oauth2client`, Billing P1 routes, Wallet P3 (merged `real`), React/Next UI in `ctech-poker/ui`.

**Spec:** `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (§7, §8 P4). **Depends on:** P1 (one-off invoices, refund route, `invoice.paid` webhook), P3 (holds on `real`). P2 is independent.

Repos: `P_POKER=/home/artur-revgas/Documents/Projects/Ctech/ctech-poker` (Go in `P_POKER/api`, UI in `P_POKER/ui`); `P_WALLET=/home/artur-revgas/Documents/Projects/Ctech/ctech-wallet`.

## Global Constraints

- Prices live **only** in Poker's `catalog`; Billing and Wallet never receive a SKU. Billing gets `description` + `amount_cents`; Wallet gets a Sandbox amount.
- Sandbox is credited only after Poker re-reads the invoice from Billing and sees it paid for exactly the catalogue price (the webhook is a wake-up, spec §9). Credit idempotency key = the Billing invoice id.
- No route converts Sandbox into anything, and there is no Real → Sandbox purchase path in Poker either (spec §7). A test asserts the purchase code never calls Wallet's real-debit route.
- Exclusion and limits block **real-money buy-ins only** (matches today's `HoldGame` guard). Sandbox has no monetary value: a self-excluded user can still buy and play Sandbox, and Sandbox purchases are not metered.
- Self-exclusion and limit increases keep today's rules exactly: limit **decreases** apply at once, **increases** after the cooling-off the Wallet code implements (`domain/wallet/responsible.go`); an exclusion cannot be lifted before its end (check `RevokeSelfExclusion` for the exact rule and port it).
- Existing consent versions are carried over, never fabricated (invariant 10 of the old Wallet): a user who had not accepted the gambling addendum has not accepted it in Poker.
- Routes in English, Portuguese only in locale files; constants for headers/scopes; no attribution trailers; Poker UI gate is its own lint config (read `ui/CLAUDE.md`).

## Review Focus

- A `invoice.paid` for a Poker invoice delivered twice credits Sandbox once. Pinned in Task 4.
- A paid invoice whose amount differs from the catalogue price is never credited and alarms. Pinned in Task 4.
- A user whose imported exclusion is still active cannot place a real-money buy-in even if Wallet's guard is already gone. Pinned in Task 7.
- The imported limits equal Wallet's byte for byte, including a pending increase mid-cooling-off. Pinned in Task 6.
- Limits are **gross inflow**: cashing out never frees headroom (old invariant 8, now Poker's). Pinned in Task 7.
- Billing down: buying a pack fails closed with no half-created local record that blocks a retry. Pinned in Task 3.
- A Wallet export call without the new scope is refused. Pinned in Task 5.

---

## Part A — Wallet gets generic routes (`P_WALLET`)

### Task 1: Hold routes without "game" in the name

**Files:**
- Modify: `api/internal/api/v1/router.go`, `api/internal/middleware/scope.go`, `api/internal/oauthresource/scope-manifest.json` (+ its test), `api/internal/api/v1/internal.go`
- Test: `router_test.go`, `scope_manifest_test.go`

- [ ] **Step 1: Failing tests:** `POST /v1.0/internal/wallet/holds`, `POST /holds/:hold_id/release`, `POST /holds/cashout` and `GET /holds/status/:user_id` exist, require `internal:wallet:hold` (new scope, same semantics as `ScopeWalletGameHold`), and call the same handlers as the `/game/*` routes (assert both paths reach `holdGame`/`releaseHold`/`cashoutGame` and produce identical responses for the same body). The manifest test passes with the new scope listed.
- [ ] **Step 2: Implement** by registering the second path on the same handlers. **The old routes and scopes stay** (removed in P5).
- [ ] **Step 3:** SSM/M2M client entry: grant Poker's M2M client the new scope (CDK/`ctech-account` client config; follow how `ScopeWalletGameHold` was granted).
- [ ] **Step 4: Commit** `feat(wallet): generic hold routes alongside the game-named ones`.

### Task 2: Export route for the gambling state

**Files:**
- Create: `api/internal/api/v1/responsible_export.go`, test beside it
- Modify: `router.go`, `scope.go`, manifest

**Interfaces:**
- Produces: `GET /v1.0/internal/wallet/responsible/export?cursor=` scope `internal:wallet:responsible-export` →
```json
{ "users": [ { "user_id": "...", "terms_addendum_version": "...", "gambling_addendum_version": "...", "gambling_activated_at": "...",
  "self_exclusion": {...}, "game_limits": {...}, "pending_limits": {...}, "cpf_hmac": "..." } ], "next_cursor": "" }
```
(use the JSON of `wallet.SelfExclusion` / `wallet.GameLimits` as they are today; read `domain/wallet/responsible.go:22-46` and keep field names).

- [ ] **Step 1: Failing tests:** a scoped client pages through three users with `limit` and gets all three once; a client with `internal:wallet:hold` only → 403; a user with no gambling state is omitted.
- [ ] **Step 2: Implement** over `users` repo paging (`repositories/user.go`). Read-only.
- [ ] **Step 3:** PASS; **Commit** `feat(wallet): export route for responsible-gambling state (one-off migration)`.

---

## Part B — Poker (`P_POKER/api`)

Branch: `feat/poker-owns-games`.

### Task 3: Billing client and the `catalog`

**Files:**
- Create: `internal/billingclient/client.go`, `client_test.go`, `internal/catalog/catalog.go`, `catalog_test.go`
- Modify: `internal/config/config.go`, `internal/app/app.go`

**Interfaces:**
- Produces:
```go
package catalog
type Sandbox struct { ID string; PriceCents, Credits int64 }   // copy the table from ctech-wallet/api/internal/domain/wallet/sandbox_sku.go
type Product struct { ID string; PriceCents int64 }            // copy the table from product_sku.go
func Sandboxes() []Sandbox; func SandboxByID(id string) (Sandbox, bool)
func Products() []Product;  func ProductByID(id string) (Product, bool)
```
`billingclient` has the same methods and error types as Wallet's `billingclient` (P2 Task 2: `EnsureCustomer`, `CreateInvoice`, `InvoiceByReference`, `RefundInvoice`, `VerifySignature`); copy the package rather than importing across services, then propose extracting it to `ctech-go-common` as a follow-up (CLAUDE.md: reusable code goes to a shared package) — the extraction is **not** part of this plan, but the PR notes the duplication.

- [ ] **Step 1: Failing tests:** `catalog` returns exactly the SKUs and prices the Wallet tables hold today (a test that lists ids and prices literally, so a later edit is deliberate); `billingclient` tests mirror P2 Task 2's against an `httptest.Server`.
- [ ] **Step 2: Implement.** Config: `BILLING_URL`, `BILLING_CLIENT_ID`, `BILLING_CLIENT_SECRET`, `BILLING_EVENT_SECRET`, `PURCHASES_VIA` (`wallet` | `billing`, default `wallet`).
- [ ] **Step 3:** PASS. **Commit** `feat(poker): billing client and price catalogue`.

### Task 4: `purchasegw` — the adapter behind the existing interfaces

**Files:**
- Create: `internal/purchasegw/gateway.go`, `gateway_test.go`
- Modify: `internal/app/app.go` (choose `walletclient.Client` or `purchasegw.Gateway` for the three purchase services by `PURCHASES_VIA`)

**Interfaces:**
- Consumes: the interfaces already declared in `sandboxpurchase/service.go` (`ListSandboxSKUs`, `PurchaseSandbox`, `GetSandboxPurchase`, `RefundSandboxPurchase`), `reactionpurchase` and `cosmeticpurchase` (`ListProductSKUs`, `PurchaseProduct`, `GetProductPurchase`, `RefundProductPurchase`); the types `walletclient.SandboxPurchase` / `ProductPurchase` as the return shapes (so the services and the webhook handler are untouched).
- Produces: `purchasegw.Gateway` implementing all of them using `catalog`, `billingclient` and a `walletCredit` interface (`Credit`, `Debit` of `walletclient.Client`).

- [ ] **Step 1: Failing tests** (fake Billing + fake wallet credit):
  - `PurchaseSandbox("u","pack_x",key,desc)` creates an invoice with `amount_cents = catalog price`, `reference = "snd_"+key-derived id`, and returns a purchase in `pending` with the Pix payload; replay with the same key returns the same purchase; a replay with another SKU → idempotency conflict.
  - `GetSandboxPurchase` of a **paid** invoice calls `Credit(user, credits, idemKey = invoice id, "sandbox_pack", description)` exactly once even if called three times, and returns `completed`.
  - Paid for a different amount than the catalogue → no credit, returns an error that `purchaselifecycle.DefinitiveWalletRejection` treats as definitive, and an alarm log.
  - `RefundSandboxPurchase` → read `ctech-wallet/api/internal/services/sandbox_purchase.go:RefundSandboxPurchase` first and reproduce its semantics (what is debited, what window); money back through `RefundInvoice`.
  - Billing returns 503 on `CreateInvoice` → the call returns a retryable error and persists nothing.
- [ ] **Step 2: Run** `go test ./internal/purchasegw/ -v` — FAIL. **Step 3: Implement.** **Step 4:** PASS.
- [ ] **Step 5: Billing event route:** `POST /v1.0/webhooks/billing` verifies `X-Billing-Signature`/`X-Billing-Timestamp` (as P2 Task 4) and routes by `reference` prefix to the same `ConfirmFromWebhook` entry points `walletwebhook.go` uses (`snd_` → sandbox service, `prd_` → the product services picked by SKU). Failing test first: bad signature → 401; duplicate event → one credit.
- [ ] **Step 6: Commit** `feat(poker): sell Sandbox and goods through Billing invoices`.

---

## Part C — Responsible gambling moves to Poker

### Task 5: `responsible` package (domain port)

**Files:**
- Create: `internal/responsible/domain.go`, `domain_test.go`, `store.go`, `service.go`, `service_test.go`

- [ ] **Step 1:** Copy `ctech-wallet/api/internal/domain/wallet/responsible.go` and its test `responsible_test.go` into `internal/responsible/` (package rename only). The ported tests must pass unchanged — that is the proof the rules did not drift.
- [ ] **Step 2: Failing service tests** for `Service` over an in-memory store: `SelfExclude`, `RevokeSelfExclusion` (rules as ported), `SetLimits`, `CancelPendingLimits`, `Status`, `Eligibility(userID, now) (ok bool, reason string)`, `RecordInflow(userID, cents, now) (*Breach, error)` using `CheckDeposit` windows (gross: no decrement API exists, and a test asserts none is reachable).
- [ ] **Step 3: Implement** the store on Poker's DynamoDB single-table helpers (look at `internal/promocode/store.go` for the conditional-write style); the audit trail is an append-only item type written with `attribute_not_exists`; IAM: explicit DENY on `UpdateItem`/`DeleteItem` for the audit item prefix, as Wallet did (`cdk` of Poker; follow Wallet's `iam-stack.ts` rule).
- [ ] **Step 4:** PASS. **Commit** `feat(poker): responsible-gambling domain, service and store`.

### Task 6: Import from Wallet

**Files:**
- Create: `cmd/import-responsible/main.go`, `internal/responsible/import.go`, `import_test.go`

- [ ] **Step 1: Failing tests:** the importer upserts each exported user once (re-run changes nothing); consent versions and timestamps are copied verbatim; a user with an active exclusion keeps the same `until`; a pending limit increase keeps its effective time; a user in the export with **no** consent stays unconsented.
- [ ] **Step 2: Implement** paging `GET /internal/wallet/responsible/export` with Poker's M2M token. `--dry-run` prints counts; the real run writes with `attribute_not_exists(pk)` per user so a second run is harmless.
- [ ] **Step 3:** PASS. **Commit** `feat(poker): one-off import of responsible-gambling state from Wallet`.

### Task 7: Buy-in enforces them

**Files:**
- Modify: `internal/buyin/service.go` (`activationChecker` → `eligibility`), `internal/config/config.go`, `internal/app/app.go`
- Test: `internal/buyin/*_test.go`

- [ ] **Step 1: Failing tests:** a real-money buy-in by a user without consent → refused (`consent_required`); by a self-excluded user → refused; over the daily/weekly/monthly limit → refused with the window reset time, **before** any Wallet hold; accepted buy-in calls `RecordInflow` once; a Wallet hold failure after `RecordInflow`… must not leave inflow counted (record after the hold succeeds, with an idempotency key = the buy-in key so a retry is not double counted); a cashout never reduces the counters. The sandbox path is untouched.
- [ ] **Step 2: Implement:** replace `IsGamblingActivated` (Wallet) with `responsible.Service.Eligibility`; switch Poker's Wallet calls from `/internal/wallet/game/*` to `/internal/wallet/holds*` (Task 1) in `walletclient`.
- [ ] **Step 3:** PASS. **Commit** `feat(poker): real-money buy-in enforces consent, limits and exclusion locally`.

### Task 8: Poker UI

**Files:**
- Modify/Create: `ui/src` pages for gambling consent, limits, self-exclusion (move from `ctech-wallet/ui` — find with `rg -l "self-exclude|gambling" ctech-wallet/ui/src`), Poker API client, locales `pt-BR`/`en`; remove the "Dinheiro de Jogos"/game-wallet copy in `ui/src/components/lobby/ProfileMenu.tsx` and friends

- [ ] **Step 1: Failing component tests** for the limits form (decrease applies at once, increase shows the cooling-off notice) and self-exclusion confirm dialog, using the repo's existing test setup.
- [ ] **Step 2: Implement; Step 3:** `npx eslint` zero warnings per `ui/CLAUDE.md`; **Step 4: Commit** `feat(poker-ui): responsible-gambling screens live in Poker`.
- [ ] **Wallet side (P5 deletes the old pages):** here only add a banner to the Wallet gambling pages pointing to Poker.

### Task 7b: A self-exclusion survives account deletion

Wallet today carries an active exclusion past erasure, keyed by the CPF's keyed hash, so deleting an account is not a way out of an exclusion (`ctech-wallet/api/internal/services/erasure.go:251-350`, `wallet.CarriedSelfExclusion`, `PutCarriedSelfExclusion`, `applyCarriedExclusion` in `responsible.go:269`). That duty moves with the rest.

**Files:**
- Create: `internal/responsible/carried.go`, `carried_test.go`
- Modify: `internal/userpurge/*` (Poker's erasure participant; read it first and follow its contract with `ctech-account`), `internal/responsible/service.go`, Wallet `responsible_export.go` (Task 2: add `carried_exclusions: [{cpf_hmac, exclusion, expires_at}]` to the export), `internal/responsible/import.go` (Task 6: import them)

- [ ] **Step 1: Failing tests:** purging a user with an active exclusion stores a carried record keyed by `cpf_hmac` with the same `until`, and **before** the profile is erased; purging a user whose exclusion has ended stores nothing; a new account whose KYC CPF hash matches a carried record becomes self-excluded until the same date on first `Eligibility` call; an expired carried record is ignored and removable; the import (Task 6) loads Wallet's existing carried records so nobody's exclusion is dropped by the move.
- [ ] **Step 2: Run** `go test ./internal/responsible ./internal/userpurge -v` — FAIL. **Step 3: Implement** by porting Wallet's `CarriedSelfExclusion` rule and `carrySelfExclusion` ordering (carry first, erase second). The `cpf_hmac` comes from the same ctech-account internal KYC call Wallet used (`kycclient.KYC.CPFHMAC`); give Poker a minimal client or reuse the one it already has for KYC if present (grep first).
- [ ] **Step 4:** PASS; **Commit** `feat(poker): carry an active self-exclusion across account deletion`.

## Task 9: Cutover

**Files:**
- Create: `docs/runbooks/poker-owns-games.md` (in `P_POKER`)

- [ ] **Step 1:** Runbook order, each step safe on its own: (1) deploy Wallet routes (Task 1–2); (2) deploy Poker with `PURCHASES_VIA=wallet`; (3) register Poker's `invoice.paid` endpoint in Billing, store the secret; (4) run `import-responsible --dry-run`, then for real in non-prod and prod; compare counts with the Wallet export; (5) switch Poker buy-in to local eligibility (Task 7) — Wallet still also guards, so both agree; (6) non-prod: `PURCHASES_VIA=billing`, buy a pack with the sandbox payment, see credit; prod: owner buys the smallest pack; (7) leave both for a week, watch alarms. Rollback: `PURCHASES_VIA=wallet`.
- [ ] **Step 2:** Run non-prod steps and report; **ask the owner before the live purchase and before step 5 in prod.**
- [ ] **Step 3: Commit.** Report: no new infra; one SSM secret pair and env vars for Poker.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| §7 Poker sells Sandbox by Pix via Billing; credits on `invoice.paid`, idempotent on invoice | 3, 4 |
| §7 SKU catalogues move to Poker | 3, 4 (Wallet's tables deleted in P5) |
| §7 limits, consent, self-exclusion, metering in Poker | 5, 6, 7, 8 |
| Exclusion survives account deletion (carried record) | 7b |
| §7 no Real→Sandbox; Sandbox never converts out | Global Constraints, 4 |
| §8 P4 shippable alone, rollback | 9 |
