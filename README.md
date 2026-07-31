# ctech-billing

Recurring subscription and metered billing service for the CTech ecosystem.

`ctech-billing` owns the **subscription and invoice domain**: plans, subscriptions, billing
cycles, pro-rata, invoice generation, and dunning. It does not move money itself — every
charge will be collected by delegating to [`ctech-wallet`](../ctech-wallet), which owns the
current DynamoDB ledger and PIX deposit/sandbox-purchase integrations. The generic recurring
charge contract required by billing does not exist yet and must be designed and implemented
before billing can collect money.

Status: **design-only — not built yet.** This repository contains **specification and design
docs only** (`README.md`, `OVERVIEW.md`, `ARCHITECTURE.md`, `PLAN.md`). There is no source
code: no `api/`, `ui/`, `cdk/`, `cmd/`, or `internal/`. Nothing here moves money or stores
data. See [OVERVIEW.md](OVERVIEW.md) for the product spec, [ARCHITECTURE.md](ARCHITECTURE.md)
for the technical design, and [PLAN.md](PLAN.md) for the phased build plan.

> **Known spec inconsistencies & open decisions (backlog B37):** the spec docs below carry a
> few unresolved tensions (datastore choice, `FIXED_MONTHLY` vs `billing_timing=ADVANCE`, and
> an MVP that depends on an unconfirmed `ctech-wallet` contract). They are catalogued in
> [OVERVIEW.md § 11](OVERVIEW.md) — read that before treating any single doc as final.

## Relationship to other CTech services

- **ctech-account** — issues the M2M (client-credentials) tokens that authorize external
  services (e.g. `ctech-dfe`) to create invoices, and the user tokens that authorize a
  customer to view/cancel their own subscriptions.
- **ctech-wallet** — is the source of truth for money movement. Its implemented internal API
  includes an idempotent real-balance debit, but it has no generic charge resource, Boleto
  rail, billing webhook, or charge-status lookup. Those are new integration work, not current
  capabilities.
- **ctech-dfe** — first consumer: will create subscriptions/invoices for DF-e plans, and is
  the natural place to auto-emit the NFS-e (service tax invoice) CTech itself owes on every
  paid `ctech-billing` invoice (see OVERVIEW.md § Suggested Features).
- **ctech-poker** — future consumer, likely only for real-money-mode entry fees / rake
  reporting, if that model is adopted.
