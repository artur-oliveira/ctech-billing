# Wallet ledger — P2: Wallet deposits and withdrawals go through Billing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ctech-wallet` opens deposit invoices and withdrawal payouts at Billing instead of calling Inter, behind a per-environment flag, with the old Pix path left intact as the fallback.

**Architecture:** A narrow `Rail` seam is cut between the deposit/withdraw services and the Pix client. `LegacyRail` wraps today's `pix.PixClient` unchanged; `BillingRail` talks to Billing (P1 routes) with an OAuth client. Billing tells Wallet a deposit was paid through a signed `invoice.paid` webhook; Wallet treats it only as a wake-up, re-reads the invoice and credits only after its own checks. Everything past the seam (locks, idempotency, daily limits, quarantine, reconcile) is unchanged.

**Tech Stack:** Go (Fiber v3, fx, DynamoDB), `api-commons/oauth2client`, Billing P1 routes, AWS SSM for secrets, CDK for env wiring.

**Spec:** `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (§3–§5, §8 P2). **Depends on:** P1 merged and deployed, tenant zero on the BYOK rail (P1 Task 0).

Repos: `P_WALLET=/home/artur-revgas/Documents/Projects/Ctech/ctech-wallet` (Go in `P_WALLET/api`, CDK in `P_WALLET/cdk`); `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing` for the webhook endpoint registration.

## Global Constraints

- All Wallet invariants hold unchanged (non-negative balance by conditional write, append-only ledger, idempotency on every mutation, one operation per wallet, ordered locks, Sandbox never becomes real, webhook never the source of truth, no money in limbo). `TestSandboxPurchaseNeverDebitsRealWallet` stays green.
- Wallet never trusts the event: it re-reads the invoice from Billing and requires the paid amount to equal the deposit's expected amount.
- The credit requires **both** Wallet's own masked-CPF match against the KYC CPF (existing `maskedCPFMatches`) **and** Billing's `payer_match = match`. Any disagreement quarantines (never credits) and refunds the payer.
- The payout destination is always the CPF on the user's KYC record; the client never supplies it.
- Flag `BILLING_RAIL` (`off` | `on`), default `off`. Off = byte-for-byte today's behaviour.
- Routes in English; constants for headers, scopes and env names; RFC 7807 problems; no attribution trailers.
- Sandbox purchases and product purchases are **not** moved here (P4/P5); they keep using `pix.PixClient`.

## Review Focus

- A `invoice.paid` event for an unknown reference, or with a bad signature or a stale timestamp, is rejected without touching the ledger (401 / 200-ignored respectively). Pinned in Task 4.
- Two events for one deposit (Billing retries) credit once. Pinned in Task 4.
- A deposit whose invoice is paid for **less** than expected is never credited and alarms. Pinned in Task 3.
- Billing down while a user deposits: the deposit call fails closed with no pending row left that blocks a retry. Pinned in Task 5.
- A withdrawal whose Billing payout call times out stays `processing`, and reconcile resolves it from Billing, never reverses on a transport error. Pinned in Task 6.
- Flag off: no Billing call is ever made, including at startup. Pinned in Task 2.

---

## Task 1: Cut the `Rail` seam (refactor, no behaviour change)

**Files:**
- Create: `api/internal/rail/rail.go`, `api/internal/rail/legacy.go`, `api/internal/rail/legacy_test.go`
- Modify: `api/internal/services/wallet.go` (the deposit, confirm, refund, withdraw call sites of `s.pix`), `api/internal/services/reconcile.go`, `api/internal/app/app.go`
- Test: existing `wallet_test.go`, `wallet_rail_test.go`, `reconcile_test.go` must pass unchanged

**Interfaces:**
- Produces:
```go
package rail

type DepositRequest struct {
	UserID, TaxID, Name, Email string // KYC identity; TaxID is the CPF
	Txid                       string // Wallet's deterministic deposit id, also Billing's reference
	AmountCents                int64
}
type Rail interface {
	OpenDeposit(ctx context.Context, in DepositRequest) (*pix.Charge, error)
	QueryDeposit(ctx context.Context, txid string) (*pix.Charge, error)
	RefundDeposit(ctx context.Context, txid, e2eID string, amountCents int64, idemKey string) (*pix.TransferResult, error)
	Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*pix.TransferResult, error)
	QueryPayout(ctx context.Context, idemKey string) (*pix.TransferResult, error)
}
func NewLegacy(p pix.PixClient) Rail
```

- [ ] **Step 1: Write the failing test** `legacy_test.go`: with `pix.NewFake()` (see `pix/fake.go`), `OpenDeposit` followed by `QueryDeposit` returns the charge opened; `Payout` then `QueryPayout` returns `TransferDone`; `RefundDeposit` forwards `e2eID`, amount and `idemKey`. (Copy the constructor name from `fake.go` before writing.)
- [ ] **Step 2: Run** `go test ./internal/rail/ -v` — FAIL (package missing).
- [ ] **Step 3: Implement** `LegacyRail` as thin delegation to `pix.PixClient` (`OpenDeposit` ignores the identity fields and calls `CreateCharge(ctx, in.Txid, in.AmountCents, "")`).
- [ ] **Step 4: Swap the call sites.** `WalletService` gets a `rail rail.Rail` field set by `SetRail`; replace `s.pix.CreateCharge/QueryCharge/Transfer/QueryTransfer/Refund` **only in** `InitiateDeposit`, `ConfirmDeposit` (and its refund helpers), `Withdraw`, `ReconcileWithdrawals`, `SweepPendingDeposits`, `SweepDepositRefunds`. `InitiateDeposit` builds `DepositRequest` from `s.kyc.Get(ctx, userID)` (the KYC client already used by `Withdraw`). Leave `s.pix` for sandbox/product purchases.
- [ ] **Step 5: Run** `go test ./...` in `api/` — PASS with no test edits except the constructor wiring. **Commit** `refactor(wallet): put deposits and withdrawals behind a Rail seam`.

## Task 2: Billing client

**Files:**
- Create: `api/internal/billingclient/client.go`, `client_test.go`
- Modify: `api/internal/config/config.go`, `config_test.go`

**Interfaces:**
- Consumes: Billing P1 routes; `api-commons/oauth2client.TokenManager` (see how `kycclient.New` builds `tokens`).
- Produces:
```go
type Client struct { /* base URL, token manager, http client with timeout 8s */ }
type Invoice struct {
	ID, Status, Reference string
	AmountCents           int64
	PixCopiaECola, QRCodeBase64 string
	ExpiresAt             time.Time
	PaidAt                *time.Time
	Payer                 *Payer     // nil until paid
	PayerMatch            string     // "match" | "mismatch" | "unknown" | ""
	PaymentID             string     // the Pix end-to-end id, once paid
}
type Payer struct { Name, Document, EndToEndID string; Masked bool }
type Payout struct { ID, Reference, Status string; PaymentID string }
func (c *Client) EnsureCustomer(ctx, userID, taxID, name, email string) (customerID string, err error)
func (c *Client) CreateInvoice(ctx, customerID, reference, description string, amountCents int64) (*Invoice, error)
func (c *Client) InvoiceByReference(ctx, reference string) (*Invoice, error)      // ErrNotFound when absent
func (c *Client) RefundInvoice(ctx, invoiceID string, amountCents int64, reason, idemKey string) error
func (c *Client) CreatePayout(ctx, cpf string, amountCents int64, reference, message string) (*Payout, error)
func (c *Client) PayoutByReference(ctx, reference string) (*Payout, error)        // ErrNotFound when absent
func VerifySignature(secret string, header, timestamp string, body []byte, now time.Time, skew time.Duration) bool
```
Config: `BILLING_URL`, `BILLING_CLIENT_ID`, `BILLING_CLIENT_SECRET` (SSM-backed like `WalletClientSecret`), `BILLING_EVENT_SECRET`, `BILLING_RAIL` (`off|on`).

- [ ] **Step 1: Failing tests** with `httptest.Server` standing in for Billing: `CreateInvoice` sends `Idempotency-Key: <reference>`, `Authorization: Bearer`, and the JSON body `{customer_id, description, amount_cents, reference}`; a 409 maps to `ErrIdempotencyConflict`; `InvoiceByReference` 404 → `ErrNotFound`; `VerifySignature` accepts a signature computed as Billing does (`HMAC-SHA256(secret, timestamp + "." + body)`, header `v1=<hex>` — read `ctech-billing/api/internal/services/delivering.go:311` `Sign` first and match it exactly), rejects a flipped byte, rejects a timestamp older than `skew`.
- [ ] **Step 2: Run** `go test ./internal/billingclient/ -v` — FAIL. **Step 3: Implement.** **Step 4:** PASS.
- [ ] **Step 5: Config test:** with `BILLING_RAIL=on` and no `BILLING_URL`, `config.Load` returns an error; with `off`, none of the Billing vars are required. **Implement**, PASS.
- [ ] **Step 6: Commit** `feat(wallet): Billing client and config (rail off by default)`.

## Task 3: `BillingRail`

**Files:**
- Create: `api/internal/rail/billing.go`, `billing_test.go`

- [ ] **Step 1: Failing tests** against a fake `billingclient` interface:
  - `OpenDeposit` calls `EnsureCustomer` then `CreateInvoice(reference = Txid)` and returns a `pix.Charge{Txid, Amount, QRCode: PixCopiaECola, QRCodeB64, Status: ChargeActive}`.
  - `QueryDeposit` of a **paid** invoice returns `Status: ChargeCompleted` and `Payments[0] = {E2EID: PaymentID, Amount: AmountCents, PayerCPF: Payer.Document}` — the shape `ConfirmDeposit` already consumes.
  - A paid invoice whose `PayerMatch == "mismatch"` or `"unknown"` returns the payment with `PayerCPF` **blanked**, so the existing "paid with no payer identity → quarantine" branch of `ConfirmDeposit` fires. (This is how Billing's verdict becomes the second half of the dual check without a new code path in the service.)
  - A paid invoice for less than expected returns `Payments[0].Amount` as paid, so `ConfirmDeposit`'s existing mismatch path refunds it.
  - `Payout` calls `CreatePayout(reference = idemKey)`; Billing `failed` + reason `key_not_found` maps to `pix.ErrKeyNotFound`; `QueryPayout` of an unknown reference returns `TransferNotFound`; a timeout is returned as an error, not as `TransferNotFound`.
  - `RefundDeposit` calls `RefundInvoice` with the invoice looked up by `txid`.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4:** `go test ./internal/rail/` PASS. **Step 5: Commit** `feat(wallet): BillingRail`.

## Task 4: Inbound `invoice.paid` event

**Files:**
- Create: `api/internal/api/v1/billing_events.go`, `billing_events_test.go`
- Modify: `api/internal/api/v1/router.go`, `api/internal/middleware/scope_manifest` test if the route needs a manifest entry (read `scope_manifest_test.go` first)

**Interfaces:**
- Produces: `POST /v1.0/internal/billing/events` — no JWT; authenticated by `X-Billing-Signature` + `X-Billing-Timestamp` (Billing's webhook headers, `delivering.go:275-278`). Body is Billing's event envelope; the handler reads `type` and the invoice `reference` from it and nothing else.

- [ ] **Step 1: Failing tests:** (a) bad signature → 401 and `ConfirmDeposit` not called; (b) timestamp older than 5 minutes → 401; (c) a valid `invoice.paid` for reference `txid` calls `svc.ConfirmDeposit(ctx, txid, "", "", false)` once; (d) the same event twice → `ConfirmDeposit` called twice but the second is a no-op on a fake ledger (assert one credit entry); (e) an unrelated event type → 200 and nothing called; (f) a reference that is not a Wallet deposit (`ConfirmDeposit` returns not-found) → 200, so Billing stops retrying.
- [ ] **Step 2: Run** `go test ./internal/api/v1/ -run BillingEvents -v` — FAIL. **Step 3: Implement** using `billingclient.VerifySignature` with `BILLING_EVENT_SECRET`; mount only when `BILLING_RAIL=on`. The existing pending-deposit sweep stays as the lost-event backstop (no change).
- [ ] **Step 4:** PASS; **Step 5: Commit** `feat(wallet): signed invoice.paid wake-up from Billing`.

## Task 5: Wiring and the flag

**Files:**
- Modify: `api/internal/app/app.go`, `api/internal/api/v1/wallet.go`, `cdk/` (env + SSM parameter references; follow how `WALLET_CLIENT_SECRET` is wired), `OPERATIONS.md`

- [ ] **Step 1: Failing test** in `app/module_test.go`: with `BILLING_RAIL=off` the fx graph builds with `LegacyRail` and constructs no Billing client (assert the Billing token manager is never created: no network, no config read); with `on` it builds `BillingRail`.
- [ ] **Step 2: Implement** `newRail(cfg, pixClient, billingClient) rail.Rail` and call `svc.SetRail`.
- [ ] **Step 3: Failing test:** with `BillingRail` and Billing returning 503 on `CreateInvoice`, `POST /wallet/deposits` returns a problem (502) and **leaves no pending deposit row that blocks the same idempotency key** (the existing `PutDepositIfAbsent` reservation must be reversible: reuse the replay branch, which already re-creates the charge with the same txid; add a test proving a retry after Billing recovers succeeds).
- [ ] **Step 4:** CDK: add `BILLING_*` env and the two SSM secrets; `cdk diff` shows only those. Document the flag in `OPERATIONS.md`.
- [ ] **Step 5: Commit** `feat(wallet): choose the rail by BILLING_RAIL`.

## Task 6: Withdrawals and reconcile through Billing

**Files:**
- Modify: `api/internal/services/wallet.go` (`Withdraw`), `api/internal/services/reconcile.go`
- Test: `wallet_rail_test.go`, `reconcile_test.go` with a fake `Rail`

- [ ] **Step 1: Failing tests:** `Withdraw` with a rail whose `Payout` times out leaves the withdrawal `processing` and the balance debited; `ReconcileWithdrawals` then calls `QueryPayout`: `TransferDone` → completed; `TransferNotFound` after the grace period → reversed (balance credited back once, idempotent); a **transport error** from `QueryPayout` → left `processing`, no reversal. (The legacy `QueryTransfer` swallows lookup failures into not-found, `inter.go:261-273`; `BillingRail.QueryPayout` must not, and this test pins the difference.)
- [ ] **Step 2: Implement** the one change: `Withdraw`/reconcile already call the seam from Task 1; only the transport-error branch in `reconcile.go` needs the explicit "error ≠ not found" handling.
- [ ] **Step 3:** PASS; **Step 4: Commit** `fix(wallet): a failed payout lookup never reverses a withdrawal`.

## Task 7: Cutover

**Files:**
- Create: `docs/runbooks/billing-rail-cutover.md` (in `P_WALLET`)

- [ ] **Step 1:** Runbook: (1) confirm P1 is live and the tenant-zero Inter connection is healthy; (2) in Billing, register the `invoice.paid` endpoint for Wallet pointing at `/v1.0/internal/billing/events`, copy the secret into SSM `BILLING_EVENT_SECRET`; (3) deploy Wallet with `BILLING_RAIL=off`; (4) in a non-prod stack set `on`, deposit R$ 1,00 by sandbox payment, confirm credit, withdraw R$ 1,00, confirm payout `done`; (5) in prod set `on` for one deploy, owner deposits R$ 1,00 and withdraws it; (6) watch the pending-deposit sweep and reconcile alarms for a week; rollback = `off` (in-flight deposits created on Billing still complete through the event or the sweep, which reads `BillingRail` only while the flag is on — so rollback is done **after** `SELECT pending deposits = 0`, checked by the query in the runbook).
- [ ] **Step 2:** Do steps 1–4 and report. **Ask the owner before step 5** (live money).
- [ ] **Step 3: Commit** the runbook; open the PR. Report: no new infra, one SSM secret and one env var; the Pix gateway still runs until P5.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| §4 deposit via Billing invoice, event as wake-up, re-read, dual check, quarantine + refund | 1, 3, 4 |
| §4.7 sweep covers lost events | 4 (unchanged sweep), 7 |
| §5 withdrawal debit-first, payout via Billing, reconcile | 6 |
| §8 flag, old path as fallback, rollback | 5, 7 |
| §9 idempotency on every call | 2, 3, 6 |
