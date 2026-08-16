# ADR 0005 — External merchants built, but gated by `payout_status`

Status: Accepted (2026-08-15) · Records decisions **D3** and **D9**

## Context

Scope B (third-party merchants, [ADR 0001](0001-product-scope-a-and-b.md)) requires money to reach
the merchant. Sub-account custody in `ctech-wallet` sits behind `AsaasCustodyEnabled`, default
`false` (`ctech-wallet/api/internal/config/config.go:31`).

The tempting workaround — collect into CTech's pooled account and settle with the merchant outside
the system — is precisely the arrangement that raises the payment-facilitation question. It is also
the one that is easiest to do accidentally.

## Decision

**Build the path in the model and in the code; block it at the edge.**

`Organization` carries an onboarding state for receiving money:

```
payout_status: not_configured | pending_custody | enabled
```

Every route that opens a charge for an organization whose `payout_status != enabled` returns `409`
with an explicit reason. **The gate lives in a single charge-authorization function**, not spread
across handlers — one place to test, one place to audit.

**No improvised settlement.** The pooled-and-settle-outside option is removed from the design.

### Two distinct gates — the likely review mistake

| Gate | Where it lives | What it blocks |
|---|---|---|
| `AsaasCustodyEnabled` | **wallet** config (`ctech-wallet/api/internal/config/config.go:31`, default `false`) | whether custody capability exists in the ecosystem at all |
| `Organization.payout_status` | **billing**, per organization | whether one specific merchant may open charges |

Turning the first on is a **wallet deploy**, not a billing one. The second stays per-merchant even
after custody exists: custody being enabled is never blanket authorization. A global flag that
unlocks every merchant at once is exactly what `payout_status` exists to prevent.

### Who turns custody on, and when (D9)

**Artur.** Both pre-conditions are mandatory, neither waivable for schedule pressure:

1. Legal guidance on CTech intermediating a third party's receipts — specifically whether the
   operation constitutes a payment arrangement. A lawyer answers this, not this document.
2. The full KYC + Asaas sub-account creation flow **tested end to end** — not read in the
   provider's documentation.

## Consequences

- The block is **server-side authorization, not a UI feature flag.** Hiding a button is not
  blocking. The UI only reflects the server's answer.
- External merchants are **not onboardable** until custody is on. This belongs in the commercial
  contract, not discovered by the merchant at signup.
- Scope A is unaffected: CTech billing its own customers lands in CTech's own pooled account, which
  works today.

## Limits accepted

Code exists that cannot be exercised end to end until custody is enabled. That is the price of not
retrofitting tenancy later, and it is much cheaper than the alternative.

## Reopen if

Legal guidance says the arrangement is not viable in this shape. Then scope B changes shape, and
this ADR is superseded rather than edited.
