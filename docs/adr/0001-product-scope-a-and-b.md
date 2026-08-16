# ADR 0001 — Product scope: CTech billing its own customers **and** third-party merchants

Status: Accepted (2026-08-15) · Records decisions **D1**, **D4**, **D6**

## Context

Two different products were on the table and the docs did not separate them:

- **A** — CTech charges its own customers for its own products (`ctech-dfe` plans, etc.).
- **B** — third parties (merchants) use CTech Billing to charge *their* customers.

A is B with exactly one tenant, so a single domain model can serve both. The cost is not in the
model — it is in what B forces into the MVP: real tenancy, PII, test/live mode, a hosted checkout,
and a regulatory posture about moving other people's money.

## Decision

**Build one codebase that serves A and B.** Tenant is generic from the first line of persisted
data. **Tenant zero is CTech's own products** — the first customer and the best test.

The *timeline* is decoupled from the *model*: there is no signed external merchant (D4) and no
committed date for the first internal consumer either (`ctech-dfe`, D6).

## Consequences

1. `Organization` is a Phase 0 dependency, not a formality. "Tenant = `product_key`" is dead.
2. `organization_id` + `livemode` are in the primary key of **every** entity from day one
   (see [ADR 0003](0003-tenant-and-livemode-partition-key.md)).
3. PII on `Customer`, test/live mode, and hosted checkout move into the MVP. With an external
   integrator they stop being refinements and become entry requirements.
4. The "money reaches the merchant" leg is built but blocked at the edge
   (see [ADR 0005](0005-payout-gate.md)).

Because there is no dated external merchant (D4), the *platform* organization work — `Membership`,
`Invitation`, role CRUD in `ctech-account` — is **not** in the critical path. The MVP ships a
minimal, locally-owned `Organization` instead (see [ADR 0007](0007-minimal-organization.md)).

Because `ctech-dfe` has no committed date (D6), the billing MVP is demonstrated against the *shape*
of dfe's two plans (fixed and metered) with CTech itself as tenant, rather than waiting on dfe.

## Limits accepted

- A future migration of the minimal `Organization` into `ctech-account`. Acceptable precisely
  because the partition key already carries `organization_id`: it becomes a migration of record
  ownership, not a reindexing of data.
- Serving B means the payment-arrangement question must be answered by a lawyer before any
  third-party money moves. ADR 0005 buys the time; it does not answer the question.

## Reopen if

A merchant signs with a date, or `ctech-dfe` and `ctech-billing` need to share one organization.
Either trigger promotes the platform-organization work (roadmap phase 6a) to real work.
