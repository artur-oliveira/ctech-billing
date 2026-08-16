# ADR 0008 — `metadata` is an opaque key/value map

Status: Accepted (2026-08-15) · Records decision **D8**

## Context

Every billing system eventually faces the same request: an integrator needs to carry a value that
only makes sense to them (a purchase order number, an internal customer id, a receipt reference).
Answering it by adding a column each time is the worst pattern in this domain.

The concrete case raised: attaching an NF-e / receipt reference to a charge.

## Decision

A free key/value list attachable to `Customer`, `Subscription`, `Invoice`, `Product`, `Price`,
`CheckoutSession` and `CreditNote`.

| Aspect | Rule |
|---|---|
| Type | `map[string]string` — always string, never nested, never typed |
| Limits | ≤ 50 keys; key ≤ 40 chars; value ≤ 500 chars |
| Semantics | **Opaque to billing.** No business rule reads `metadata` |
| Writes | Merchant/integrator via API and console. Replace by key, not blind merge |
| Reads | Returned on every entity response; **propagated in every webhook** |
| Inheritance | `Subscription.metadata` is **copied** into the generated `Invoice`, at generation time |
| Audit | Changing `metadata` produces an audit entry like any other field |
| Visibility | **Never rendered in the consumer portal or on the public invoice** |

Named `metadata` — not `tags` (which means value-less labels in this market) and not
`custom_fields` (which implies schema definition and validation).

## Consequences

**Why "opaque" is a rule, not a preference:** the day a billing decision reads
`metadata["skip_dunning"]`, the free field has become an informal schema with no validation, no
migration and no tests. If a value needs to change behavior, it deserves a first-class field. That
single line is what keeps `metadata` useful instead of turning it into debt.

**Copied, not referenced:** an invoice is a historical record. If it pointed at the subscription's
live `metadata`, editing the subscription would rewrite the past of closed invoices.

**On the NF-e example specifically:** `metadata` is right for *attaching* a number somebody typed.
But once `ctech-dfe` emits NFS-e from `invoice.paid`, that link becomes a system relation needing an
index, reconciliation and state (issued / failed / canceled). It **starts in `metadata` now** and is
promoted to a first-class `Invoice.fiscal_document_ref` with an index when automatic emission
lands. Not the other way round — a dedicated field before the flow exists is speculation.

## Limits accepted

- **Searching by `metadata` is not in the MVP.** In DynamoDB, filtering by map value is a `Scan`,
  forbidden by [ADR 0002](0002-datastore-dynamodb.md). When it becomes a real need, the answer is a
  sparse index over keys **declared** by the merchant (`indexed_metadata_keys` on the organization),
  not generic search.
- **`metadata` is an LGPD surface.** It is free-form and propagated in every webhook, so one
  integrator writing `metadata["cpf_titular"]` exports undeclared PII through a channel no policy
  covers. Mitigations, all cheap on day one and expensive later: a warning in the UI and the docs;
  `Customer` deletion anonymizes the `metadata` of linked entities too; the same retention/TTL as
  the record that carries it ([ADR 0009](0009-retention-and-ttl.md)). Automatic CPF detection in
  values is tempting and **not** recommended — it false-positives, breaks legitimate integrations,
  and creates the illusion of compliance.

## Reopen if

A merchant needs to query by a metadata key. The answer is the declared-key sparse index above.
