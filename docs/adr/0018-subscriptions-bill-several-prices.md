# ADR 0018 — A subscription bills one or more prices

Status: Accepted (2026-08-16) · Replaces the one-item rule the MVP shipped with ·
Changes `POST /v1/subscriptions` and the invoice generation key

## Context

The DF-e price list is three plans, and the third one broke the model:

> **Sob demanda** — R$ 5,00 por empresa · R$ 0,05 por NF-e · R$ 0,01 por NFC-e ·
> R$ 0,50 por CT-e · R$ 0,10 por MDF-e

That is one agreement with five meters. The code could not express it.
`Subscriber.Subscribe` created a subscription with exactly one item,
`SubscriptionRepository.Create` refused more than one, and `invoiceOne` billed
`items[0]`. A five-meter plan modelled as five subscriptions produces five
invoices and five PIX charges a month for something the customer signed once;
modelled as one subscription with five items, four of the meters were **silently
never billed** — the failure `invoiceOne`'s own comment calls "revenue lost
silently, which is the failure mode nobody notices".

The one-item limit was documented as an MVP boundary deliberately placed at the
API rather than in the schema, precisely so that removing it would be an API
change and not a migration. This is that change.

## Decision

**A subscription holds one or more items, and one period of one subscription is
one invoice with one line per item.**

`SubscribeInput.PriceID`/`Quantity` become `SubscribeInput.Items`, and the
request body follows:

```json
{"customer_id": "cus_…", "items": [{"price_id": "price_…", "quantity": 1}]}
```

Three rules are enforced at the boundary, in `Subscriber.resolveItemPrices`:

- **All items share a Recurrence.** A subscription has exactly one, and it is
  what decides which period is being billed.
- **All items share a BillingTiming.** An advance item and an arrears item on one
  document cover two different windows, and nothing downstream could say which.
- **All items share an OwnerKey.** That key routes the events
  ([ADR 0016](0016-webhook-routing-by-product-owner.md)); two services on one
  subscription would each receive the other's invoices, which is the exact
  failure the key exists to prevent.

A repeated price is refused too. Two items on the same price are two lines
charging for the same thing; whoever means "twice as much" means quantity.

**The generation key moves from the item to the subscription.** It is now
`{subscription_id}:{period_start}`. Keyed on the item, each of five items would
claim the period separately and produce five single-line invoices — the exact
outcome this ADR exists to prevent, arrived at through the idempotency key
instead of through the loop.

**Usage reporting names its item.** `POST /v1/usage` gained `price_id`, optional
only when the subscription has exactly one item. A subscription metering NF-e,
NFC-e and CT-e has no defensible default, and guessing one bills NFC-e volume at
the CT-e price.

## Consequences

- **`POST /v1/subscriptions` is not backward compatible.** Nothing is in
  production, so no consumer breaks; a compatibility shim accepting the old
  `price_id` was deliberately not written, because a shim outlives the migration
  it was for.
- **The cycle rules are load-bearing, not validation for its own sake.** They are
  what lets the invoicer read `items[0].Recurrence` as the subscription's cycle
  without checking the rest.
- **A plan is one bill again.** Sob demanda produces one invoice with five lines,
  one total and one PIX.
- **`SubscriptionRepository.Create` no longer bounds the count.** What a
  subscription may hold is a question about prices agreeing on a cycle, which
  needs the prices — so it is answered where they are already read.

## Limits accepted

- **No add-item or remove-item route.** The items are fixed at subscribe time. A
  customer moving from Pro to sob demanda cancels and resubscribes. Mid-period
  item changes need proration against a subset of the lines, which is a real
  design and not a missing endpoint.
- **Mixed fixed and metered items are allowed** as long as they agree on the
  cycle — that is base-fee-plus-overage, and it works. The portal reports such a
  subscription as metered and publishes only the fixed part as the amount, which
  is honest but partial.
- **`Price.ExceedsChargeCeiling` still only answers for fixed prices.** Five
  metered lines can sum past the wallet's ceiling
  ([ADR 0004](0004-pix-on-invoice-via-wallet.md)) and the refusal arrives at the
  charge, not at the catalogue. Multi-item makes that more likely, not less.

## Reopen if

A plan needs items on different cycles — an annual base fee with monthly
overage. That is two subscriptions or a new concept, not a relaxed rule.
