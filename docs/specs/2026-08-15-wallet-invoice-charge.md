# Spec — `ctech-wallet`: charge an amount the caller supplies

Status: **Implemented** · 2026-08-15 · Implements [ADR 0004](../adr/0004-pix-on-invoice-via-wallet.md)
· Repository that changes: **`ctech-wallet`** · Consumer: `ctech-billing`

This was the one cross-repo dependency in the billing plan. Both sides are now built:

| Side | Where |
|---|---|
| Scope | `ctech-wallet/api/internal/middleware/scope.go` (`ScopeWalletChargeAmount`), manifest + its test |
| Ceiling | `M2MClient.MaxChargeCents` / `MaxCharge()`, `ctech-wallet/api/internal/services/wallet.go` |
| Route | `ctech-wallet/api/internal/services/charge_amount.go`, `api/v1/m2m_charge.go`, `api/v1/router.go` |
| Acceptance (§5) | `ctech-wallet/api/internal/services/charge_amount_test.go` |
| Consumer | `ctech-billing/api/internal/wallet/client.go`, `internal/services/collecting.go` |

Two things remain, and neither is code: the SSM `m2m-clients` blob needs a `billing` entry (webhook
URL, HMAC secret, and optionally `max_charge_cents`), and `ctech-account` has to issue billing a
client holding `internal:wallet:charge-amount`.

**Deliberately reused, not rebuilt.** The charge lands in `wallet_product_purchases` with the same
`prdp` prefix, so `/pix/confirm-product-purchase`, the pending sweep, the notify-back and the refund
all already work on it. The rows are told apart by `ProductPurchase.Kind` (`"product"` vs
`"charge"`), never by inspecting the SKU field — for a charge that field holds a label billing chose,
and routing wallet's own behaviour off a consumer's string is the coupling the constant exists to
avoid.

**One pre-existing gap closed on the way.** `RetryFailedM2MWebhooks` only swept the sandbox table;
`ListWebhookFailedOlderThan` existed on the product repository with no caller, so a failed
notify-back there was recorded and never retried. It now sweeps both.

## 1. What exists today (verified in source, not in docs)

| Fact                                                                         | Where                                                                  |
|------------------------------------------------------------------------------|------------------------------------------------------------------------|
| M2M PIX sale, opened on a user's behalf, no ledger effect                    | `ctech-wallet/api/internal/api/v1/router.go:141-145`                   |
| The amount comes from a **fixed Go catalogue**, never from the caller        | `internal/services/product_purchase.go:62-64`, `wallet.ProductSKUByID` |
| Deterministic txid `prdp + sha256(client#user#idemkey)[:n]`                  | `internal/services/product_purchase.go:32-36`                          |
| Idempotent reservation before the charge, with a request-hash conflict check | `internal/services/product_purchase.go:80-100`                         |
| Confirmation **re-queries the provider**, never trusts the webhook body      | `internal/services/product_purchase.go:127-137`                        |
| Amount paid ≠ amount expected raises an alarm and refuses                    | `internal/services/product_purchase.go:134-137`                        |
| Notify-back to the requesting client, HMAC-signed, retried by the sweep      | `internal/services/m2m_webhook.go:125-174`, `:185`                     |
| Per-client config (`webhook_url`, `hmac_secret`) loaded from SSM JSON        | `internal/services/wallet.go:189-192`, `internal/secrets/ssm.go:33`    |
| A charge can carry a payer CPF hint                                          | `internal/pix/lambda_client.go:66`                                     |

So the machinery billing needs is **already in production**. Exactly one thing is missing: the
amount is the catalogue's, and an invoice total is not — proration and metered usage make it
arbitrary by construction.

## 2. The change

**Let a specifically-scoped M2M client supply `amount_cents` instead of a SKU.** Everything else on
the path — reservation, txid, confirm-by-re-query, webhook, sweep, refund — is reused unchanged.

### 2.1 A separate scope, not a wider one

`internal:wallet:charge-amount`, new, granted to billing only.

`internal:wallet:product-purchase` (`internal/middleware/scope.go:63`) stays exactly as strict as it
is. If the amount field were accepted under the existing scope, every client that today can only
sell a R$ 4,90 catalogue item — ctech-poker among them — could name its own price the moment the
field lands. The catalogue is a fraud defense; removing it for one caller must not remove it for
all of them.

### 2.2 The route

Same handler shape as `m2mPurchaseProduct`, mounted alongside it:

```
POST /v1.0/internal/wallet/charge
Scope: internal:wallet:charge-amount
Body: { user_id, amount_cents, reference, idempotency_key }
→ 201 { purchase_id, amount, status, pix_copia_e_cola, qr_code_base64, expires_at }
→ 422 when amount_cents exceeds the client's ceiling
→ 409 on the same idempotency_key with a different amount or user
```

`reference` is a caller-owned opaque label (billing sends the invoice id). It is stored where the
SKU is stored today, so the row shape, the read routes and the webhook payload stay identical. The
webhook's `kind` is `"charge"`.

### 2.3 The ceiling — the defense that replaces the catalogue

- `max_charge_cents` becomes a third field of `M2MClient` (`internal/services/wallet.go:189`),
  sourced from the same SSM JSON. Default when absent: **100000** (R$ 1.000,00).
- Validated **server-side, in wallet**, before the reservation. Over the ceiling is `422`. It is
  never truncated to the ceiling — a silently reduced charge is a paid invoice that is still short.
- The three defenses only work together: the dedicated scope, the ceiling, the mandatory
  idempotency key. Any one alone is not enough (ADR 0004 § "The fraud defense that is being
  removed").

### 2.4 Request hash

`reqHash` currently binds `client#user#sku` to the catalogue price
(`internal/services/product_purchase.go:73`). For a caller-supplied amount it must bind
`client#user#reference` to **the amount from the request**. Without that, replaying one idempotency
key with a bigger amount returns the original charge and looks like success — the exact hole the
catalogue used to close.

### 2.5 Payer hint

Billing may send the customer's CPF; wallet forwards it as `payerHintCPF`
(`internal/pix/lambda_client.go:66`). Optional, and only worth sending because the rail actually
uses it to match the payer — not stored by billing for this purpose and not required for the charge
to open.

## 3. What billing builds against it

Not part of this spec's wallet work, listed so the contract is read against its real consumer:

- `PaymentAttempt` and `CheckoutSession` **repositories** — the domain types and state machines
  exist (`api/internal/domain/billing/payment.go`), the persistence does not.
- A wallet client, keyed by `PaymentAttempt.IdempotencyKey()` = `{invoice_id}:{attempt_n}`
  (`payment.go:93`), which is already shaped for exactly this call.
- `POST /internal/webhooks/wallet` — verifies `X-Wallet-Signature`, then **re-reads**
  `GET /v1.0/internal/wallet/charge/:id` before touching the invoice. The webhook is a wake-up
  signal, never payment authority; that is wallet's own posture and it must not be weakened one
  layer up.
- A reconciliation job for charges whose webhook never arrived — `AttemptAbandoned` already exists
  and is documented as an integration-bug alarm, not a non-paying customer.
  Built: `api/internal/services/reconciling.go`, `api/cmd/reconcile`.

## 4. What this spec does **not** unblock

**The third-party merchant's checkout.** Two independent blockers, and this contract removes
neither:

1. **Destination of the money.** Every charge here settles into CTech's own account. A merchant
   collecting from their own customers needs a sub-account and a split, which is gated by
   [ADR 0005](../adr/0005-payout-gate.md) behind a legal opinion and an end-to-end KYC test.
2. **The payer has no CTech account.** `PurchaseProductDirect` is keyed on `user_id` throughout —
   reservation, ownership check, history, refund
   (`internal/services/product_purchase.go:61,163,196`). A merchant's customer arriving from a
   payment link has no such id, and inventing one would put a stranger's purchase in someone's
   wallet history.

CTech's own checkout has neither problem: the money is already going where it goes today, and the
payer is a CTech account holder (that is what the portal is —
[ADR 0012](../adr/0012-portal-serves-tenant-zero.md)).

## 5. Acceptance

Wallet-side, as tests in `ctech-wallet`:

1. A client with `internal:wallet:charge-amount` opens a charge for an arbitrary amount and gets a
   QR code.
2. The same client, same idempotency key, same amount → the same `purchase_id`, no second charge.
3. Same key, **different amount** → `409`, and the original charge is untouched.
4. `amount_cents` one centavo over the ceiling → `422`, and no reservation row is written.
5. A client holding only `internal:wallet:product-purchase` calling this route → `403`.
6. Confirm re-queries the provider; a webhook claiming success for a charge the provider reports as
   pending changes nothing.
7. Paid amount ≠ expected → alarm, no confirmation.

## 6. Open

- Whether billing's ceiling stays at the R$ 1.000,00 default. An annual plan above it is rejected by
  the ceiling, which is a product limit, not a bug (ADR 0004 § Limits accepted). Decide before the
  first annual plan, not after.
- Aggregate caps (per day, per client) are deliberately not in this spec. Build when volume makes
  the threshold measurable rather than guessed.
