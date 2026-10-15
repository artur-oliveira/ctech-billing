# Realtime events

The catalogue of what the socket says and what each surface does about it. It is
one list that both halves are checked against:

- `api/internal/realtime/events_doc_test.go` asserts the entities the server can emit are exactly the rows below, and that every socket-only type appears in a row.
- `ui/src/lib/realtime/emission.contract.test.ts` asserts `effectsOf` agrees with each row's surface columns: `none` means it invalidates nothing, anything else means it invalidates something.

A change carries ids and state words only, never an amount or a name. A client
that does not know a type of a known entity invalidates that entity's prefix; an
entity it does not know invalidates nothing (the screens still refetch on focus).

<!-- events:begin -->
| entity | types | scopes | console | portal | finance | checkout |
|---|---|---|---|---|---|---|
| invoice | invoice.created, invoice.finalized, invoice.paid, invoice.payment_failed, invoice.uncollectible, invoice.voided, invoice.payer_recorded | tenant, invoice, user | invoices, overview | invoices | none | the view |
| payment | payment.attempted, payment.succeeded, payment.failed, payment.abandoned | tenant, invoice | invoices | invoices | none | the view |
| checkout_session | checkout.session.created, checkout.session.completed, checkout.session.expired, checkout.session.canceled | tenant, invoice | invoices | invoices | none | the view |
| credit_note | credit_note.created | tenant, invoice, user | invoices | invoices | none | the view |
| subscription | subscription.created, subscription.activated, subscription.past_due, subscription.recovered, subscription.renewed, subscription.paused, subscription.resumed, subscription.canceled, subscription.updated | tenant, user | subscriptions, overview | subscriptions | none | none |
| customer | customer.updated | tenant | customers | none | none | none |
| payout | payout.updated, payout.done, payout.failed | tenant | connections | none | accounts | none |
| connection | connection.updated | space | connections | none | connections, accounts | none |
| statement | statement.line_posted | space | none | none | the whole space | none |
| bill | bill.updated | space | none | none | the whole space | none |
<!-- events:end -->
