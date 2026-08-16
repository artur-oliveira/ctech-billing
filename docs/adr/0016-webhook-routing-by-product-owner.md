# ADR 0016 — Outbound webhooks route by product owner, not by tenant or by caller

Status: Accepted (2026-08-16) · Implements OVERVIEW.md § 9.3 · Depends on
[ADR 0012](0012-portal-serves-tenant-zero.md)

## Context

Every consuming product needs to react to `invoice.paid` and
`subscription.canceled`, and the alternative to telling them is making them poll.

The obvious design is an endpoint per organization: a merchant registers a URL,
everything that happens in their tenant is delivered to it. That is correct for
an ordinary merchant, and it is **wrong for the only tenant that exists**.

CTech is tenant zero ([ADR 0012](0012-portal-serves-tenant-zero.md)): one
organization holding the subscriptions of every CTech product. `ctech-dfe` and
`ctech-poker` are both clients of it. A per-organization endpoint would send dfe
every invoice poker issued — customer ids, subscription ids and amounts of
another service's business, delivered to a URL that has no reason to see them.

Three candidates for the routing key were considered, and the third only
survived because of how the first two fail:

1. **The credential that created the subscription.** Natural, and it dies on the
   first console-created subscription: an operator acting on a merchant's behalf
   holds no M2M credential, so the routing key is empty. The console does not
   exist yet, which is exactly why this would have been discovered late.
2. **`metadata`.** Metadata is opaque to billing by decision
   ([ADR 0008](0008-opaque-metadata.md)), and reading it for routing would make a
   caller-writable map into a control plane — a merchant could redirect somebody
   else's events by writing a key.
3. **The product.** A subscription points at a price; a price points at a
   product; a product belongs to a service. It survives whoever created the
   subscription, survives an operator acting by hand, and survives the M2M client
   being rotated or revoked.

## Decision

**`WebhookEndpoint` is its own entity, and events are routed by
`Product.OwnerKey`.**

The entity, rather than fields on `APICredential`: a credential is a *reference*
to a client in ctech-account and stores nothing about it, so hanging a URL and a
signing secret on it would lose both when the OAuth client is rotated. An
endpoint also multiplies against credentials — one consumer may want two, and a
console-created subscription has none.

`Product.OwnerKey` names the service that owns a product (`"dfe"`, `"poker"`). An
endpoint declares the `OwnerKey` it wants; **empty means every product**, which
is the ordinary merchant's case and needs no configuration at all. Only tenant
zero, which is the only place the problem exists, sets the field.

The key is **copied onto the subscription at creation** and from there onto its
invoices, rather than derived per event. Deriving means subscription → items →
prices → products, three reads to answer a question whose answer cannot change,
inside a path that runs on every state transition.

**Delivery is two passes and an outbox.** The event row is written *inside the
transaction of the change it describes* — the same discipline as the audit row,
for the same reason: an invoice that reaches PAID with no `invoice.paid` queued
is a consumer who never learns their customer paid. A separate job then matches
events to endpoints and delivers them.

**The payload is an id and a type.** No amounts, no customer, no status. The
consumer reads the entity back through the API with its own credential, which is
billing's own posture toward wallet's notify-back, one layer on.

## Consequences

- **A misconfigured URL leaks an id, not a bill.** The thin payload is what makes
  the blast radius of a typo in an endpoint URL small.
- **The write path cannot be slowed or failed by webhook configuration.** No
  endpoint is read while an invoice is being paid; the fan-out pass reads them.
- **Deliveries are at-least-once.** A response that times out after the consumer
  committed is indistinguishable from one that never arrived, so this service
  retries it. The event id is a header as well as a payload field, so a consumer
  can deduplicate before parsing.
- **Signed over `timestamp + "." + body`**, HMAC-SHA256, in `X-Billing-Signature`.
  The timestamp is inside the signed material rather than beside it: signing the
  body alone produces a value that stays valid forever, so a captured delivery
  can be replayed at will.
- **Endpoints disable themselves** after a run of consecutive failures. The run
  resets on any success — counting total failures would eventually disable an
  endpoint that fails one delivery in a hundred, which is a working endpoint.
- **A third table with `schedule-index`.** `webhooks` joins `invoices` and
  `subscriptions` as a table a cross-tenant job reads, and the schema test now
  names all three. Its two queues carry a due *time* rather than a due date,
  because a backoff is measured in minutes.

## Limits accepted

- **A subscription spanning two owners routes to both endpoints**, and each then
  sees the whole subscription. This is not engineered around: it is a catalogue
  modelling decision, and the rule is not to sell one subscription across two
  services' products. Enforcing it in code would mean rejecting a shape the
  domain otherwise allows, for a case nobody has asked for.
- **`OwnerKey` is copied, so it can go stale.** A future item change — upgrade,
  downgrade — must recompute it. Those do not exist yet, and the place to
  recompute it is the code that introduces them.
- **An invoice with no priced lines has no route.** It is recorded and delivered
  nowhere rather than fanned out to everybody, because "we could not tell whose
  this was" must not resolve to "send it to all of them".
- **Delivery gives up after eight attempts over roughly two days.** Beyond that
  the consumer has a gap they must reconcile from the API, and retrying for a
  week only delays their discovering it.

## Reopen if

A merchant needs to register endpoints themselves, which turns endpoint
management into console writes and makes the signing secret tenant-managed data —
at which point it needs the field encryption of
[ADR 0017](0017-field-level-encryption.md) rather than the table's own at-rest
encryption. Or if a consumer demonstrates a real need for the entity in the
payload, which is a decision about disclosure, not about convenience.
