# Wallet ledger — P5: Delete what moved — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctech-wallet` contains no Inter, Pix, charge, catalogue, `game` or gambling code, the Pix gateway Lambdas and their alarms are gone, and Billing no longer carries the Wallet collection rail.

**Architecture:** Pure deletion plus renames, in the reverse order of dependence: first prove nothing calls the old surface (metrics, then `410`), then remove callers, then remove the code, then the infrastructure and secrets. Each task leaves the build and tests green and is deployable alone. Nothing here adds behaviour.

**Tech Stack:** Go, AWS CDK (TypeScript), SSM, CloudWatch.

**Spec:** `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (§3, §8 P5, §10). **Depends on:** P1–P4 live in production for at least a week with the old paths idle (Task 1 proves it).

Repos: `P_WALLET=/home/artur-revgas/Documents/Projects/Ctech/ctech-wallet`, `P_POKER=/home/artur-revgas/Documents/Projects/Ctech/ctech-poker`, `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing`.

## Global Constraints

- Nothing is deleted while a metric shows it still being called (Task 1). A deletion that makes a build or test fail is wrong; fix the caller, not the test.
- Ledger data is never deleted: `game` wallets stay as retired rows, old `PixDeposit`, `Withdrawal`, `SandboxPurchase` and `ProductPurchase` items stay readable for audit (statements and the erasure flow still read them). Only code and infrastructure go.
- Wallet invariants 1–5, 6 (Sandbox never becomes real), 11 (re-check, never trust an event) and 14 (no money in limbo) stay enforced and tested; invariants 7–10 and 12 are retired in `CLAUDE.md` with a note and the date.
- Report the infra cost change with real numbers read from Cost Explorer for the last 30 days (Lambda, SNS, SSM) before and after; do not estimate.
- Routes in English; no attribution trailers; docs under `docs/specs|plans/`.

## Review Focus

- A client calling a retired route gets `410` with a stable problem code, never a 404 that looks like a typo, until the route is finally removed. Pinned in Task 2.
- The statement and ledger screens still render old `game`, sandbox-purchase and product-purchase entries after their code is deleted (read-only). Pinned in Task 4.
- The erasure flow still completes for a user who has old purchase rows and a retired game wallet. Pinned in Task 4.
- Billing still collects tenant zero's invoices after the Wallet charge route is gone. Pinned in Task 6.
- No tenant is left on the Wallet rail when its code is removed. Pinned in Task 6.
- The secrets and IAM grants for Inter are removed from every stack, not just the one that deployed the Lambdas. Pinned in Task 7.

---

## Task 1: Prove the old surface is idle

**Files:**
- Modify: `P_WALLET/api/internal/middleware/` (a request counter by route, if one does not exist; grep `observability` use first), `P_WALLET/docs/runbooks/p5-idle-check.md` (new)

- [ ] **Step 1:** Add (or confirm) a per-route request counter keyed by route template and client id (`azp`). Deploy it a week before the deletion tasks.
- [ ] **Step 2:** Runbook query (CloudWatch Logs Insights or the metric) listing, for the last 7 days, calls to: `/internal/wallet/charge*`, `/internal/wallet/sandbox-purchases*`, `/internal/wallet/product-purchases*`, `/internal/wallet/game/*`, `/internal/wallet/pix/confirm-*`, `/wallet/game/*`, `/wallet/gambling/*`, `/wallet/sandbox/*`, and `/wallet/deposits`/`/withdrawals` served by `LegacyRail`. Expected: zero for all but the last (which must also be zero once `BILLING_RAIL=on` has been on a week).
- [ ] **Step 3:** Paste the result into the PR. Anything non-zero names the caller to fix first. **Do not continue to Task 2 with a non-zero row.**

## Task 2: Retire the routes (410) — one deploy before removal

**Files:**
- Modify: `P_WALLET/api/internal/api/v1/router.go`, `api/internal/problem/problem.go`
- Test: `router_test.go`

- [ ] **Step 1: Failing test:** each retired route (list in Task 1) returns `410` with `type .../route-retired` and `code: route-retired` for a valid token, and for an unauthenticated call the normal 401 (retirement does not leak route existence differently).
- [ ] **Step 2: Implement** a single `retired(c)` handler registered on those paths, dropping their scope middleware except auth. **Step 3:** PASS. **Commit** `chore(wallet): retired routes answer 410`. Deploy and watch one day.

## Task 3: Remove the Pix code from Wallet

**Files (delete):**
- `api/internal/pix/` (client, fake, intertoken, lambda_client and tests), `api/internal/lambdarpc/`, `api/internal/rail/legacy*.go`, `api/internal/secrets/ssm.go` parts that read Inter secrets, `api/internal/api/v1/m2m_charge.go`, `api/internal/services/charge_amount*.go`, `m2m_webhook*.go`
- `pix-gateway/` (the whole module), `api/cmd/` uses of `pix` in `reconcile`
**Files (modify):** `api/internal/rail/rail.go`, `services/wallet.go`, `reconcile.go`, `app.go`, `config.go` (drop `PIX_GATEWAY_FUNCTION_NAME`, `BILLING_RAIL`), `go.mod`

- [ ] **Step 1:** Move the few types the rail still returns out of `pix` into `rail` (`Charge`, `Payment`, `Refund`, `TransferResult`, `ErrKeyNotFound` and the status constants — copy from `pix/client.go:11-74`), rename the interface methods nothing; `BillingRail` becomes the only implementation and the `Rail` interface can be kept (it is the test seam).
- [ ] **Step 2: Run** `go build ./... && go vet ./...` and fix every reference; delete tests that only exercised deleted code, keep every ledger/limit/lock test.
- [ ] **Step 3:** `go test ./... -race` PASS (including `TestSandboxPurchaseNeverDebitsRealWallet`'s successor: see Task 4). **Step 4: Commit** `refactor(wallet): Billing is the only rail; delete the Pix client and gateway module`.

## Task 4: Remove catalogues, purchases, `game`, gambling

**Files (delete):**
- `domain/wallet/{product_sku,sandbox_sku,responsible,daily_limits,deposit_limits}.go` as far as they only served the ring-fence (keep `rail_limits.go`: Real daily deposit/withdraw limits stay), `services/{sandbox_purchase,product_purchase,responsible}.go`, `repositories/{sandbox_purchases,product_purchases}.go` (writes only; keep a read-only `PurchaseReader` used by the statement and erasure), handlers `sandbox_purchase.go`, `m2m_*_purchase.go`, gambling and `game` handlers, scopes `ScopeWalletGame*`, `ScopeWalletGambling*`, `ScopeWalletSandboxPurchases*`, `ScopeWalletProductPurchases*`, `ScopeWalletChargeAmount`, `ScopePixConfirmDeposit` (if unused after Task 3), and their manifest entries
**Files (modify):** `services/wallet.go` (`FundGame`, `ReturnFromGame`, `PurchaseSandbox`, `ActivateGambling`, `EnsureGamblingWallets`), `services/erasure*.go`, `repositories/erasure.go`, `repositories/user.go`, `ui/` (remove pages and calls), locales

- [ ] **Step 1: Failing/keeper tests first.** Add `TestNoRouteConvertsSandboxToAnything` (iterates the registered routes and asserts none accepts `type=sandbox` as a source of a withdrawal or transfer) and `TestSandboxHasNoRealDebitPath` (the service exposes no method debiting `real` and crediting `sandbox`) — the successors of the old ring-fence regression tests, written **before** deleting the old ones so the guarantee never lapses.
- [ ] **Step 2: Failing read tests:** the statement for a wallet containing old `game_*`, `sandbox_purchase` and `product_purchase` entries renders them (the ledger entry types remain defined, read-only); `ErasureService` completes for a fixture user with old purchases and a retired game wallet.
- [ ] **Step 3: Delete** per the list; the carried-exclusion code in `erasure.go` goes too — **only after** confirming Poker's Task 7b is live and its import (P4 Task 6) carried every existing record (compare counts in the PR).
- [ ] **Step 4:** `go build ./... && go test ./... -race`; UI: `npx tsc --noEmit && npx eslint src --ext .ts,.tsx` (zero errors and zero warnings) and the UI tests PASS.
- [ ] **Step 5: Commit** `refactor(wallet): remove SKU catalogues, purchases, the game wallet and gambling rules`.

## Task 5: Poker removes its old Wallet calls

**Files (modify):** `P_POKER/api/internal/walletclient/client.go` and tests (`gamewallet_test.go`, `productpurchase_test.go`, `sandboxpurchase_test.go`, `scopes_test.go`), `internal/app/app.go`, `internal/config/config.go` (`PURCHASES_VIA`)

- [ ] **Step 1:** Delete `ListSandboxSKUs`, `PurchaseSandbox`, `GetSandboxPurchase`, `RefundSandboxPurchase`, `ListProductSKUs`, `PurchaseProduct`, `GetProductPurchase`, `RefundProductPurchase`, `HoldGame`/`ReleaseHold`/`CashoutGame` old paths (keep the generic `/holds*` ones renamed `Hold`, `Release`, `Cashout`), `IsGamblingActivated`; make `purchasegw` the only purchase implementation and the interfaces in `sandboxpurchase`/`reactionpurchase`/`cosmeticpurchase` point at it directly (drop the `PURCHASES_VIA` switch).
- [ ] **Step 2:** `ValidateRequiredScopes` lists only the scopes still used (`internal:wallet:hold`, Sandbox credit/debit, balance). Update `scopes_test.go` and the M2M client's granted scopes in config.
- [ ] **Step 3:** `go build ./... && go test ./... -race` PASS. **Commit** `refactor(poker): purchases and holds use the generic surface only`.

## Task 6: Remove the Wallet collection rail from Billing

**Files:** `P_BILL/api/internal/wallet/` (client), `internal/services/collecting.go`, `internal/domain/billing/charge_gate.go`, `internal/repositories/organizations.go`, `internal/api/v1/internal` webhook `/internal/webhooks/wallet`, config, tests

- [ ] **Step 1: Preflight (read-only):** a one-off query listing organizations whose `Rail()` is `RailWallet` and that have open invoices. Expected: only tenant zero before P1 Task 0; zero now. If any other tenant is on the Wallet rail, **stop** and ask the owner: they must be moved to a BYOK connection or suspended before this task.
- [ ] **Step 2: Failing tests:** `Collector.Pay` on a tenant with no BYOK connection returns the existing "needs a connection" gate error (the charge gate already refuses non-BYOK-ready tenants, `charge_gate.go:68`), never calls a wallet client; `MethodWalletBalance` (debit an existing Real balance first, `payment.go:39-43`) **keeps working**: it uses Wallet's `real/debit` route, which is a generic ledger route and stays.
- [ ] **Step 3: Implement:** delete `ChargeOpener`, the Wallet charge client, the `/internal/webhooks/wallet` route and `WalletChargeID` writes (keep the attribute on old rows readable); `RailWallet` stays as a stored value for historic organizations but no code path selects it for new charges.
- [ ] **Step 4:** `go test ./... && go vet ./...` PASS. **Step 5: Commit** `refactor(billing): the Wallet collection rail is gone; BYOK is the only rail`.
- [ ] **Step 6:** Amend ADR 0004: add "Superseded by `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (2026-10-14)" at the top and a line in `docs/adr/README.md`.

## Task 7: Infrastructure and secrets

**Files:** `P_WALLET/cdk/lib/{pix-gateway-stack,iam-stack,api-stack,reconcile-stack,constants}.ts`, `cdk/bin/*.ts`, `cdk/test/*`, `OPERATIONS.md`

- [ ] **Step 1:** Cost baseline: from Cost Explorer (filter service Lambda, SNS, SSM Parameter Store; tag or name `wallet`) record the last 30 days for the two Pix gateway functions, their alarms/topic subscriptions, and the Inter SSM parameters. Paste the numbers in the PR.
- [ ] **Step 2: Failing CDK tests** (`cdk/test`): the synthesized app contains no `PixGatewayStack`, no function named `pixGateway*`, no IAM statement mentioning `/wallet/<env>/inter/`, no `PIX_GATEWAY_FUNCTION_NAME` env. Run `npx cdk diff` and confirm the diff is deletions only.
- [ ] **Step 3: Implement:** remove the stack and its wiring; remove the Inter parameters from `constants.ts`; remove the `SSM_ALERTS_TOPIC_ARN` use if nothing else reads it (keep the shared topic itself, it belongs to another stack — check before deleting).
- [ ] **Step 4:** Deploy order: API first (Task 3 already live), then the CDK change; the SSM `inter/*` parameters are **retained for 30 days** (tag `delete-after`) in case of rollback, then deleted by the owner. **Commit** `chore(wallet-cdk): remove the Pix gateway stack and Inter secrets`.

## Task 8: Documentation and the retired invariants

**Files:** `P_WALLET/CLAUDE.md`, `AGENTS.md`, `api/CLAUDE.md`, `api/ENDPOINTS.md`, `README.md`, `OPERATIONS.md`; `P_BILL/CLAUDE.md`/`ARCHITECTURE.md` where they describe the Wallet rail; `P_POKER/OVERVIEW.md`, `ARCHITECTURE.md`

- [ ] **Step 1:** Rewrite the Wallet three-balance table as two balances (`real`, `sandbox`), remove invariants 7–10 and 12 with a dated "Retired 2026-10-xx — see the spec" line each (as invariant 13 already is), add the new invariants from the spec §9, and point "Before any task" at the new spec.
- [ ] **Step 2:** `ENDPOINTS.md` lists only live routes. Billing and Poker docs describe the new flow in one diagram each (copy the §3 diagram).
- [ ] **Step 3: Commit** in each repo: `docs: wallet is a ledger; Billing is the Inter rail`.

## Task 9: Final verification and report

- [ ] **Step 1:** In non-prod: deposit by simulated payment, withdraw, buy a Sandbox pack in Poker, buy-in/cashout a real-money table (flag on in non-prod only), self-exclude and confirm the buy-in is refused, delete an account with old rows. All via Billing, nothing via the deleted paths.
- [ ] **Step 2:** `grep -rIn -i "inter\b\|pix\|cobv\|pagador" ctech-wallet/api ctech-wallet/ui ctech-wallet/cdk` returns only prose in docs and historic ledger descriptions. Paste it in the PR.
- [ ] **Step 3:** Report to the owner: lines deleted per repo, the cost before and after (Task 7 numbers), the date after which the retained SSM parameters can be deleted, and the list of invariants retired.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| §3 Wallet has no Pix/charge/catalogue/gambling code | 3, 4 |
| §7 SKUs live only in Poker | 4, 5 |
| §8 P5 delete gateway, charge routes, SKUs, `game` routes | 2, 3, 4, 7 |
| §9 retired and new invariants documented | 4 (keeper tests), 8 |
| §10 cost reported with real numbers | 7, 9 |
| Billing no longer depends on Wallet for collection (ADR 0004 superseded) | 6 |
