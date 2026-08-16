# ADR 0009 — Retention periods and DynamoDB TTL

Status: Accepted (2026-08-15) · Records decision **D12**

## Context

LGPD requires data minimization, but commercial documents have a legal retention floor. Both have
to be answered per record type, and in DynamoDB ([ADR 0002](0002-datastore-dynamodb.md)) TTL is an
**attribute written when the item is created**. Changing the policy later only affects new items —
the past keeps whatever it was written with.

So the attribute goes in from the very first item written, even with a generous period. What is not
allowed is creating items without it and "deciding later".

## Decision

| Record | Retention | Why |
|---|---|---|
| `Invoice`, `InvoiceItem`, `CreditNote` | **No TTL** | Commercial document with a 5-year legal floor; purging is a reviewed process, not a TTL |
| `AuditLog` | **5 years** | Tracks the floor of the document it explains |
| `PaymentAttempt` | **5 years** | It is the proof of why an invoice is or is not paid; it disappears with the invoice, never before |
| Canceled `Subscription` | **No TTL** | It explains invoices that continue to exist |
| `Event` + `WebhookDelivery` | **90 days** | Redelivery/debugging window. The invoice is the truth; the event is a notification |
| `CheckoutSession` `EXPIRED`/`CANCELED` | **90 days** | Serves support ("I paid and it didn't land"), not accounting |
| Raw `UsageRecord` | **24 months** | The aggregate already lives on the invoice; the raw record is for auditing a consumption dispute |
| Anonymized `Customer` (§ 8) | **No TTL** | The record stays; the identifying content is what goes |

Alongside: soft-delete of `Customer` that preserves invoices and anonymizes the profile; data-subject
export; and an `AuditLog` that records **access** to `tax_id`, not only writes.

## Consequences

- Every repository write path sets the TTL attribute. A missing TTL on a new entity is a review
  failure, not a detail.
- Per-organization configurable retention is **out of the MVP**. One period per record type solves
  the problem; a configurable period is more surface to misconfigure.

## Limits accepted

Two technical caveats that are not cosmetic:

- **DynamoDB TTL is not an exact deadline.** AWS typically deletes within ~48 h *after* expiry. It
  is data hygiene; it is **not** a guarantee of "deleted on day X" for a hard legal deadline. If
  such a requirement appears, the purge must become an explicit job.
- **`metadata` inherits the TTL of the item carrying it.** There is no separate retention for
  `metadata`, deliberately: one more deadline is one more thing to forget.

## Reopen if

A hard legal deadline appears. The answer is an explicit purge job, not tighter TTLs.
