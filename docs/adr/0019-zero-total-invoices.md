# ADR 0019 — A zero-total invoice is issued, then settled on issue

Status: Accepted (2026-08-16) · Makes the Free plan billable ·
Adds `CauseNothingDue`

## Context

The DF-e price list opens with a Free plan: R$ 0/mês, one company, three NF-e.
Nothing in the code had ever seen a total of zero, and every path handled it
badly:

- `Finalize` opened the invoice and armed the dunning queue, so a Free customer
  would be emailed "sua fatura vence em 3 dias — R$ 0,00" three days before a due
  date that means nothing, and chased for thirty days after it.
- `Collector.Pay` would open a PIX charge for zero centavos, which the wallet
  rejects (`gt=0`) — leaving an OPEN invoice nobody could ever pay or close.

The alternative considered was **not creating a subscription at all** for Free,
leaving the price in the catalogue only so the portal can render the plan ladder.
It was rejected: the entitlement, the renewal, the upgrade path and the event
stream all hang off a subscription, and a plan that has none is a second code
path in every consumer.

## Decision

**A zero-total invoice is a real invoice, finalized and then closed immediately
with a new cause, and never entered into the dunning queue.**

Three parts:

- **`Invoice.NothingDue()`** — total is zero. The document is still issued: the
  period was served and the record says so, with a number from the same gapless
  sequence every other invoice draws from. Skipping it would make the Free plan
  invisible in the invoice list, in the period index and in the audit trail.
- **`CauseNothingDue`** — its own cause, deliberately **not** a member of
  `paymentCauses`. No money moved, and an accountant reading the trail has to be
  able to tell the two apart. `Invoice.Transition` refuses it on an invoice that
  owes anything, which is what stops it from being a way to mark any invoice PAID
  with no money behind it and no operator named — the same thing `CauseManual` is
  kept out of `paymentCauses` to prevent.
- **A zero settlement date arms nothing.** `InvoiceRepository.Finalize` writes the
  schedule keys only when given a date, so a Free invoice never appears in the
  dunning partition at all. Filtering it out on read would still cost every
  morning's query and would still be one bad transition away from a reminder
  about R$ 0,00.

The event emitted is `invoice.paid`, the same one a real payment emits. A
consumer that grants service on that event must grant it to a Free plan too, and
the alternative is every consumer learning a second event that means the same
thing. What separates them is the cause, which is recorded.

## Consequences

- **Free is a subscription like any other.** It renews through the sweep, emits
  the same events, appears in the portal, and upgrades by changing price.
- **The rule is about the total, not about the plan.** A metered period with zero
  usage settles the same way, which is correct and was previously a R$ 0,00
  invoice sitting OPEN forever.
- **`cmd/dunning` never sees these invoices**, so "no reminders for Free" is a
  property of the queue rather than a check somebody has to remember to write.

## Limits accepted

- **A discount that happens to reach the full total takes this path too.** That is
  intended — nothing is owed — but it means an invoice can be PAID without a
  payment for a reason other than being on a free plan. The cause and the audit
  entry are what distinguish them, and there is no separate state for it.
- **No entitlement difference is expressed here.** Billing does not know that Free
  allows three NF-e; the quotas live in the price's metadata for the DF-e to read
  ([ADR 0008](0008-opaque-metadata.md) — billing never reads a metadata key). Who
  enforces the quota is the DF-e, and this ADR does not change that.

## Reopen if

A merchant needs a zero invoice to stay OPEN — a manual-payment plan where the
amount is agreed later, for instance. That is a different document, not a
relaxation of this rule.
