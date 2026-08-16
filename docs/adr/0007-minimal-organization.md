# ADR 0007 — Minimal `Organization`, no company registry, no CNPJ

Status: Accepted (2026-08-15) · Records decisions **D10** and **D4**

## Context

Scope B needs organizations. But `ctech-account` has no organization model at all, and `ctech-dfe`
has a complete one (`repositories/roles.go:18-48`: OWNER/ADMIN/USER/VIEWER with `action.resource`
permissions).

Three options, each with a real cost:

| Option | Cost now | Consequence |
|---|---|---|
| (a) Tenant = `product_key`, no members | ~zero | Dies with [ADR 0001](0001-product-scope-a-and-b.md) — a merchant is an organization with people |
| (b) Organization/Membership in `ctech-account` | High (touches a production repo) | **Correct long term** — identity belongs there |
| (c) Copy dfe's model into billing | Medium | **A third lineage.** `_analysis/cross-stack-duplication.md` documents this pattern already causing a real divergence (`UpsertAttrs`) |

With no dated external merchant (D4), paying (b)'s risk now is buying a schedule risk early.

## Decision

The MVP ships a **minimal `Organization` local to billing**:

```
id, display_name, payout_status, livemode, owner (account.user_id)
```

**No CNPJ, no legal name, no address, no certificate.** No invitations, no configurable roles, no
self-service CRUD. Provisioning is manual.

Option (c) stays forbidden in every scenario.

## Consequences

- **The NFS-e CTech owes on its own revenue is not emitted by billing.** Billing fires
  `invoice.paid`; the CNPJ, the certificate and the tax rules live in `ctech-dfe`. If someone later
  asks for "just one little CNPJ field here, to make it easier" — that is the first step of the
  duplication this ADR exists to prevent. Refuse it.
- The minimal `Organization` is **not** option (c): it replicates no roles, no permissions, no
  invitations. It is a tenant record with one owner, designed to be *replaced* by a reference to
  `ctech-account`, not to grow into a second RBAC model.
- **The warning sign to watch in code review:** if this entity starts acquiring configurable roles
  before the trigger below fires, it has become option (c) without anyone deciding to.

## Limits accepted

A future migration of this record's ownership into `ctech-account`. Cheap because the partition key
already carries `organization_id` ([ADR 0003](0003-tenant-and-livemode-partition-key.md)) — it is a
migration of who owns the record, not a reindexing of data.

## Reopen if

The first external merchant signs, **or** `ctech-dfe` and `ctech-billing` need a shared
organization — whichever comes first. Then, in order: `Organization`/`Membership`/`Invitation` are
born in `ctech-account`; billing keeps only what is billing's (numbering, dunning policy, invoicing
config) and references `organization_id`; `ctech-dfe` migrates last, keeping its fiscal entity
(CNPJ, certificates, NFS-e config) as a child record keyed by the platform's `organization_id`.
CNPJ enters billing's world at that point — not before.
