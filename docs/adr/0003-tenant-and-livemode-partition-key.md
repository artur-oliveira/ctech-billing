# ADR 0003 — Partition key `{organization_id}#{livemode}`

Status: Accepted (2026-08-15) · Records decision **D7** and § 12.4 of the assessment

## Context

Two things are ruinously expensive to add after data exists: the tenant identifier and the
test/live separation. Both are cheap today — one composite key — and both are a data migration with
financial risk tomorrow.

A tenant id kept as a *filterable attribute* rather than as the partition key fails the same way
every time: one query somewhere forgets the filter, and it returns another tenant's invoices.

## Decision

**Every partition key begins with `{organization_id}#{livemode}`.** Not an attribute — the key.

`livemode` is a boolean rendered as `live` / `test`. Test and live are separate partitions of the
same table, not separate tables and not a flag on rows.

The one exception is `schedule-index`, whose partition is `{livemode}#{job}#{date}` because a daily
sweep is inherently cross-tenant. It is reachable only from scheduler code, never from a
request-scoped path (see [ADR 0002](0002-datastore-dynamodb.md)).

## Consequences

- A query without a tenant is not expressible. Forgetting the filter stops being a class of bug.
- Test mode is real isolation, not a display flag: **no data crosses modes, ever**; test webhooks
  go only to test endpoints.
- **No real wallet call in test mode.** Test mode uses a deterministic fake that can simulate
  success, failure and timeout. This is cheap and is the difference between an integrator
  discovering a failure path in staging and discovering it in production.
- `ctech-account` issues separate credentials per mode; the token's mode decides the partition, so
  a test key physically cannot read live data.

## Limits accepted

- One partition per tenant per mode concentrates a tenant's control-plane items in one partition.
  At the volumes in sight this is fine, and the one entity with unbounded volume (`UsageRecord`)
  already has its own sub-partition. If a tenant ever gets hot, the fix is more sub-partitions with
  the same prefix rule — not abandoning the rule.
- Cross-tenant admin reporting (CTech looking at all merchants at once) is not expressible without
  a dedicated index or an export. That is intentional: it should require deliberate work.

## Reopen if

Never, realistically. This is the most expensive decision in the project to retrofit; that is
exactly why it is made before the first item is written.
