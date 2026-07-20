# ctech-billing — Product / Functional Spec

## 1. Purpose

Give every CTech product (starting with `ctech-dfe`) a shared way to sell recurring or
usage-based subscriptions, without each product reinventing invoicing, pro-rata, or dunning.
`ctech-billing` is the system of record for **what is owed and why**. `ctech-wallet` is the
system of record for **what actually moved**. Never conflate the two — `ctech-billing` must be
rebuildable from zero by replaying `ctech-wallet`'s transaction history plus its own event log.

## 2. Core entities

### Plan
A sellable offer. Deliberately generic — a `Plan` is either **FIXED** or **METERED**, never both.

| Field | Notes |
|---|---|
| `id` | ULID |
| `product_key` | e.g. `dfe`, `poker` — which CTech product owns this plan |
| `name` | e.g. "DF-e Basic", "DF-e Sob Demanda" |
| `type` | `FIXED` \| `METERED` |
| `currency` | `BRL` only for MVP |
| `fixed_price_cents` | required if `type=FIXED` |
| `usage_unit` | e.g. `emissions` — required if `type=METERED` |
| `usage_tiers` | ordered list of `{ up_to, unit_price_cents }`, last tier `up_to=null` — required if `type=METERED`. A flat per-unit price is just a single tier. |
| `billing_timing` | `ADVANCE` \| `ARREARS` — see § 4. Default (on the `VARIABLE_ANCHOR` cycle): `ADVANCE` for FIXED, `ARREARS` for METERED. On `FIXED_MONTHLY` this field is **overridden to `ARREARS`** by the cycle rule (see § 4), so the default does not apply there. |
| `trial_days` | optional |
| `active` | plans are never deleted, only deactivated (existing subscriptions keep working) |
| `version` | incremented on every price/tier change (see § 6 — Plan Versioning) |

### Subscription
| Field | Notes |
|---|---|
| `id` | ULID |
| `customer_ref` | opaque tenant/customer id, owned by the consuming product (not by `ctech-billing`) — we never store PII about the customer beyond this reference |
| `plan_id` + `plan_version` | pinned at subscribe time (and at every renewal, re-pinned to current plan version unless the customer is grandfathered — configurable per plan) |
| `status` | `TRIALING` \| `ACTIVE` \| `PAST_DUE` \| `PAUSED` \| `CANCELED` |
| `cycle_type` | `FIXED_MONTHLY` \| `VARIABLE_ANCHOR` — see § 4 |
| `anchor_day` | 1–28, or `null` for `FIXED_MONTHLY` (which is always "1st business day of month, referencing the prior month") |
| `current_period_start` / `current_period_end` | |
| `cancel_at_period_end` | bool — "cancel but let the paid period run out" |
| `canceled_at` | |
| `created_at` | |

### Invoice ("Fatura")
| Field | Notes |
|---|---|
| `id` | ULID |
| `subscription_id` | |
| `period_start` / `period_end` | the period this invoice bills for |
| `due_date` | computed per § 5 (holiday/weekend rollforward) |
| `line_items` | `[{ description, quantity, unit_price_cents, amount_cents, usage_record_ids? }]` |
| `subtotal_cents` / `discount_cents` / `total_cents` | |
| `status` | `DRAFT` \| `OPEN` \| `PAID` \| `VOID` \| `UNCOLLECTIBLE` |
| `wallet_charge_id` | set once `ctech-wallet` accepts the charge request (see ARCHITECTURE.md § Wallet Integration) |
| `paid_at` | |
| `idempotency_key` | `{subscription_id}:{period_start}:{plan_version}` — invoice generation is idempotent; re-running the scheduler never double-bills |

### UsageRecord
Reported by the owning product for `METERED` plans.

| Field | Notes |
|---|---|
| `id` | |
| `subscription_id` | |
| `quantity` | |
| `occurred_at` | |
| `idempotency_key` | caller-supplied — required. `ctech-dfe` reporting "1 emission" for the same fiscal document twice (e.g. on retry) must not double-count. |

### CreditNote
Adjustments/refunds against an already-issued invoice. Never mutate a `PAID` invoice's
line items directly — every correction is a new `CreditNote` referencing the original invoice.
This is non-negotiable for auditability (and because `ctech-dfe` will need it to cancel/adjust
an NFS-e it emitted against a bad charge).

## 3. Genericity requirement

A `Plan` must express both examples from the brief without special-casing either:

- **DF-e Basic** → `type=FIXED`, `fixed_price_cents=9900`, `billing_timing=ADVANCE`.
- **DF-e Sob Demanda** → `type=METERED`, `usage_unit=emissions`,
  `usage_tiers=[{up_to:1000, unit_price_cents:50}, {up_to:null, unit_price_cents:35}]`,
  `billing_timing=ARREARS`.

Any third product (poker table rake, a future flat "Pro" tier, a hybrid base+overage plan)
must be expressible by combining these primitives, not by adding a new `Plan.type`. A
base-fee-plus-overage plan is modeled as a `FIXED` plan with amount 0 plus a linked `METERED`
component — **explicitly deferred to post-MVP**; call it out rather than bolt it on ad hoc.

## 4. Billing cycles

- **`FIXED_MONTHLY`** ("Ciclagem mensal fixa"): invoice is generated on the **1st business day
  of the month**, referencing the **entire previous calendar month**. This is always
  arrears-style regardless of plan `billing_timing`, because the reference period must be
  fully closed before the invoice reflects it. (If a FIXED plan is on this cycle, it's billed
  for the month that already elapsed — arrears — not for the month ahead.)
  - **Clarification (resolves the § 2 default conflict):** `billing_timing` only takes effect on
    the `VARIABLE_ANCHOR` cycle. On `FIXED_MONTHLY` the stored `billing_timing` value is
    ignored and the cycle is always `ARREARS`. So a FIXED plan's § 2 default of `ADVANCE` does
    **not** apply here — `FIXED_MONTHLY` is the one cycle where the plan's `billing_timing`
    field is overridden by the cycle rule.
- **`VARIABLE_ANCHOR`** ("Ciclagem mensal variável"): invoice due date is the customer-chosen
  `anchor_day` every month. `billing_timing` decides direction:
  - `ADVANCE` (default for FIXED plans on this cycle): invoice for period `[anchor, anchor+1)`
    is generated and due **at the start** of that period — i.e., generated `grace_days` before
    `anchor_day` (default 0 — due exactly on `anchor_day`).
  - `ARREARS` (default for METERED plans on this cycle): invoice for period
    `[anchor-1, anchor)` is generated **on** `anchor_day`, once usage for that period is closed.
- `anchor_day` > days-in-month clamps to the last day of that month (e.g. anchor=31 in
  February → Feb 28/29).

## 5. Date rules (mandatory)

1. Compute the nominal due date per § 4.
2. If it falls on a **national holiday** (fixed: Jan 1, Apr 21, May 1, Sep 7, Oct 12, Nov 2,
   Nov 15, Dec 25; moveable, Easter-based: Carnaval (Mon+Tue), Good Friday, Corpus Christi) —
   roll forward to the next day.
3. If it falls on **Saturday or Sunday** — roll forward to the next day.
4. Repeat 2–3 until the date is a weekday and not a holiday.
5. Rolling forward changes the **due date only**, never the `period_start`/`period_end` used
   for pro-rata math — those are always calendar-exact.

This requires a real Brazilian-holiday calculator (fixed dates + a Gauss/Meeus Easter
algorithm for the moveable ones), not a hardcoded per-year table that silently goes stale in
January. See ARCHITECTURE.md § Holiday Calendar.

**Open question for the business owner, not assumed:** roll-forward vs roll-backward is a
policy choice (rolling backward avoids ever charging a customer "late" relative to their
anchor, rolling forward is simpler and standard for most billing systems). This spec assumes
**forward**, matching Stripe/most SaaS billing convention — confirm before building.

## 6. Pro-rata

Applies on: subscribe mid-cycle, upgrade/downgrade mid-cycle, cancel mid-cycle with immediate
effect (as opposed to `cancel_at_period_end`).

```
days_in_period   = period_end - period_start   (calendar-exact, inclusive of DST-free BRT)
days_applicable  = the sub-range of days_in_period the plan/price actually applied
prorated_cents   = round(fixed_price_cents * days_applicable / days_in_period)
```

- Never assume 30-day months. Use actual calendar days for the specific period.
- Downgrade mid-cycle: credit the unused portion of the old plan as a `CreditNote`-style
  balance credit, charge the new plan pro-rata for the remainder — do **not** issue a net
  invoice that mixes both in one ambiguous line item; two clearly separate line items.
- METERED plans are never prorated — usage is usage, it's already period-exact.

## 7. Plan versioning

Editing a live `Plan`'s price/tiers creates a **new immutable version**; existing
`Subscription`s keep referencing their pinned `plan_version` until their next renewal, at
which point they re-pin to the current version **unless** the plan is marked
`grandfather_existing=true`. This is what makes "we changed DF-e Basic's price" not
retroactively corrupt every past invoice's math.

## 8. MVP scope (as specified by the business)

- Plan CRUD (internal/admin only — no public plan marketplace).
- Subscription lifecycle: create, view, cancel (customer-facing, via `ctech-account`-issued
  user token).
- M2M invoice/usage-record creation for authorized external services.
- Wallet debit integration for collection.
- Invoice generation via Wallet (PIX/Boleto — Wallet-side, not duplicated here).
- Pro-rata, national holidays, weekend due-date rollforward.
- Fixed and variable billing cycles as described above.

## 9. Suggested features (not in the original brief — flagged as suggestions)

1. **Auto-emit NFS-e via `ctech-dfe` for every paid invoice.** CTech is a company that
   charges for services (subscriptions) and separately builds Brazil's fiscal-document
   tooling. Every paid `ctech-billing` invoice is *itself* a taxable service CTech must
   document. Wiring `invoice.paid` → an internal `ctech-dfe` NFS-e emission call closes a real
   compliance gap the business will otherwise hit the first time an accountant asks for it,
   and it's close to free given `ctech-dfe` already exists.
2. **Dunning policy per plan**: configurable retry schedule (e.g. D+0, D+3, D+7) on failed
   wallet debit, auto-transition `Subscription` to `PAST_DUE` then `CANCELED` after N failures,
   and emit a webhook/event so the owning product (e.g. `ctech-dfe`) can gate feature access.
   Without this, a failed charge just silently produces an unpaid invoice forever.
3. **Webhook delivery system** (HMAC-signed, retried with backoff, delivery log) for
   `invoice.created` / `invoice.paid` / `invoice.past_due` / `subscription.canceled` — every
   consuming product needs to react to these; don't make them poll.
4. **Idempotency-key requirement on every mutating M2M endpoint**, not just usage records —
   this is the single most important defense against double-billing from a retrying caller
   and should be enforced in the HTTP layer, not left to each handler.
5. **Audit log** of every `Subscription`/`Invoice` state transition (who/what/when), separate
   from application logs — support and compliance will need this and it's much cheaper to
   build in from day one than to reconstruct later.
6. **Per-client API scoping**: an M2M client credential for `ctech-dfe` must not be able to
   create invoices against a `ctech-poker` subscription. Scope client credentials to
   `product_key` at the `ctech-account` level.
7. **Invoice PDF + durable storage (S3)** for customer download and audit — deferred to
   post-MVP but worth a placeholder field (`pdf_object_key`) now so the migration isn't painful.

## 10. Explicitly out of scope for MVP

- Multi-currency (schema allows it, nothing else does).
- Tax computation beyond the NFS-e integration above (ISS calculation rules are a project of
  their own).
- Public self-serve plan changes/upgrades UI (admin-managed plans only, customer can view/cancel).
- Base-fee-plus-overage hybrid plans (see § 3).

## 11. Known spec inconsistencies & open decisions (backlog B37)

This project is design-only (no implementation exists). The following tensions are **known** and
must be resolved before the PLAN.md phases that depend on them are executed. They are not bugs in
the prose — they are genuinely undecided. Do not treat any single doc as final.

1. **Datastore: "default DynamoDB" vs relational recommendation.** ARCHITECTURE.md § 1 lists
   DynamoDB as the *initial candidate* (matching `ctech-dfe`'s pattern) but recommends following
   whatever ledger engine `ctech-wallet/api` actually uses (likely Postgres/Aurora). The two are
   not reconciled — `ctech-billing`'s datastore is **undecided** until the `ctech-wallet` audit
   lands. See PLAN.md Phase 0 ("Confirm datastore choice … before writing a single migration").
2. **`FIXED_MONTHLY` vs `billing_timing=ADVANCE`.** A FIXED plan defaults to `ADVANCE` (§ 2),
   but the `FIXED_MONTHLY` cycle is always `ARREARS` "regardless of plan billing_timing" (§ 4).
   Resolved in-place above: `billing_timing` only applies to `VARIABLE_ANCHOR`; `FIXED_MONTHLY`
   overrides it to `ARREARS`. The stored field is effectively meaningless on that cycle.
3. **MVP depends on an unconfirmed `ctech-wallet` contract.** The entire wallet-debit path
   (ARCHITECTURE.md § 3 charge/webhook API, PLAN.md Phase 3) is a *proposal* pending
   confirmation of `ctech-wallet`'s real charge/webhook shape. Until that contract is settled,
   Phase 3 and the MVP's "Wallet debit integration for collection" (§ 8) are **blocked**.

### Other open decisions (from PLAN.md)
- Roll-forward vs roll-backward for holiday/weekend due dates (§ 5). Spec assumes forward;
  confirm with the business owner.
- `ctech-wallet` datastore choice (drives `ctech-billing`'s own — see item 1 above).

