# ADR 0026 — Finance records live as long as their space, and leave by explicit purge

Status: Accepted (2026-10-07) · Extends [ADR 0009](0009-retention-and-ttl.md)'s table with the
finance records · Spec: [`2026-10-07-finance-erp-design.md`](../specs/2026-10-07-finance-erp-design.md) § 8

## Context

ADR 0009 sets a retention per record type and writes it as TTL at creation. The finance module
([ADR 0024](0024-billing-keeps-a-management-ledger.md)) adds records whose useful life is not a
period: a person's ledger is worth keeping for as long as they use it, and worthless — and a
liability under LGPD — the day they leave. No TTL value describes that. ADR 0009 already names the
answer for this shape ("the purge must become an explicit job").

## Decision

| Record | Retention | How it leaves |
|---|---|---|
| Ledger accounts, transactions, entries, summaries | **No TTL** — life of the space | purge |
| Bills, recurrences, cards, statements, purchases | **No TTL** — life of the space | purge |
| Import batches and parsed lines | **90 days** after processing | TTL |
| Import lock rows (`FITID#`) | **No TTL** — life of the space | purge; expiring them would let an old file be imported twice |
| Raw OFX/CSV file | **Never stored** | parsed in the request and discarded |
| Finance audit rows | **5 years**, as ADR 0009 | TTL — except personal-space rows, which go with the purge |

**Purge triggers:**
- **Personal space:** the CTech account is deleted. ctech-account emits the trigger; billing deletes
  every row under `USER#{sub}#live` and `USER#{sub}#test`.
- **Organization space:** the organization is closed. A member leaving purges nothing; the data is
  the organization's.

The purge is a one-shot job over a single partition prefix per table, idempotent, and it reports
completion (ADR 0009's "explicit job", now with a caller).

## Consequences

- ADR 0009's "a missing TTL on a new entity is a review failure" now reads: a missing TTL **or a
  missing purge path**. Every finance table must be covered by the purge job, and a test lists the
  tables and asserts it.
- **Cross-repo dependency:** ctech-account's account-deletion spec (in progress) must emit the
  trigger. Until it does, a deleted account's personal finances are not removed. Owner: Artur.

## Limits accepted

- Personal-space audit rows are removed with the space, shorter than ADR 0009's five years. Those
  rows explain a person's own notes about their own money, not a commercial document anybody else
  relies on.

## Reopen if

A legal floor attaches to finance records, for example if the module ever issues documents to third
parties. Then the affected rows get ADR 0009 treatment and the purge skips them.
