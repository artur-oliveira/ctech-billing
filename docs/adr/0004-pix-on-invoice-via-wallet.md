# ADR 0004 — Collection rail: PIX on the invoice, through wallet

Status: Accepted (2026-08-15) · Records decisions **D2** and **D11**

## Context

`ARCHITECTURE.md` § 3 proposed a generic `POST /v1/charges` lifecycle in `ctech-wallet` and marked
Phase 3 blocked on it. That contract does not exist. What exists today is exactly two things:

- `POST /v1.0/internal/wallet/real/debit` — scope `internal:wallet:debit-real`, idempotent, but it
  **debits an existing balance**. Insufficient balance fails; it does not open a PIX charge.
- The product-purchase flow — opens a real PIX charge with deterministic txid and notifies back by
  webhook, but the amount comes from a **fixed Go catalogue with no administrative write path**
  (`ctech-wallet/docs/plans/2026-08-12-product-purchase-skus.md:28`).

Requiring the consumer to pre-load a wallet balance before paying an invoice fails for every new
customer, which is most of them.

## Decision

**The consumer pays the invoice by PIX directly, without pre-loading balance.**

The unblocking contract is **not** the generic `POST /v1/charges`. It is a **generalization of the
product-purchase flow that already runs in production**: accept `amount_cents` supplied by the M2M
caller instead of a catalogue SKU, with `Idempotency-Key = {invoice_id}:{attempt_n}` and
`metadata.invoice_id`, reusing the existing notify-back webhook and deterministic-txid mechanism.
That is roughly a fifth of the work of the proposed generic contract, and extends a tested path
instead of introducing a new subsystem.

Wallet balance debit stays as a **secondary** path: tried first as an optimization when the
customer already has balance, never as the only route.

## Consequences

- § 3.3 of the assessment becomes the project's critical path, not a Phase 3 task. Its design
  starts alongside Phase 1 because it is the only cross-repo dependency.
- `CheckoutSession` moves into the MVP. PIX has a QR code, a copy-paste string and an expiry —
  that is a stateful session, not a link.
- The destination of the money differs between scope A and B: A lands in CTech's pooled account
  (works today); B needs a merchant sub-account, which is gated
  (see [ADR 0005](0005-payout-gate.md)).

### The fraud defense that is being removed, and its replacement

Accepting a caller-supplied `amount_cents` removes the "amount belongs to the catalogue" check
that is today an anti-fraud defense. Three defenses replace it, and they only work together:

1. M2M client scope — only billing may open charges of this kind.
2. **A per-charge ceiling**, validated **in the wallet**, server-side, per M2M client. Above it
   the wallet returns `422` — it does **not** truncate. Wallet's default is 100000 centavos
   (R$ 1.000,00); billing's client is configured above it, see the amendment below.
3. Mandatory `Idempotency-Key`.

## Limits accepted

- **The ceiling limits the product, not only fraud.** An annual plan above R$ 1.000,00 charged in
  one go is rejected by the ceiling itself. Either that client's ceiling is raised or the plan
  bills monthly. This is a conscious business limit, not a bug to be discovered in production.
- **The ceiling is per charge, not per period.** It does not prevent 100 charges of R$ 1.000,00.
  What it bounds is the damage of *one* forged or wrong request. An aggregate cap (per day, per
  client) is a different defense and is deliberately not built now — build it when volume makes it
  measurable.
- Boleto is out of scope until a Boleto rail actually exists in wallet. It is not designed for here.

## Reopen if

Wallet gains a real generic charge resource for other reasons, or a plan above the ceiling enters
the catalogue.

## Amendment (2026-08-16) — billing's ceiling is R$ 10.000,00

The second reopen condition fired. The DF-e sob-demanda plan bills per document, so a customer's
monthly total is a function of their volume rather than a number in a catalogue: at the Pro plan's
own volume it is around R$ 890, and two thousand CT-e alone passes R$ 1.000,00. Under the original
ceiling that customer's invoice is issued, finalized, and then cannot be paid — the refusal arrives
at the wallet on the customer's first attempt, and there is no path out of it except an operator
raising the ceiling after the fact.

**Billing's per-charge ceiling is 1000000 centavos (R$ 10.000,00).** Two things carry it and both
must move together:

- `max_charge_cents` for the billing client in `/ctech-wallet/{env}/m2m-clients`, which is the
  enforcement.
- `billing.MaxChargeCents`, which is the mirror billing uses to reject a price at creation time
  with a clear message. It is a mirror, so it is only right while it agrees.

R$ 10.000,00 rather than a larger figure because it is `DefaultMaxDeposit` in the wallet: a charge
above what a customer can fund in one PIX cannot be paid, so there is no useful ceiling past it.
What the amendment does **not** change is the reasoning — the ceiling still bounds the damage of one
forged or wrong request, and it is still per charge rather than per period.

The metered limit named above stays open: `Price.ExceedsChargeCeiling` answers only for fixed
prices, so a metered total is still discovered at the charge. [ADR 0018](0018-subscriptions-bill-several-prices.md)
makes that more likely by putting several meters on one invoice, not less.
