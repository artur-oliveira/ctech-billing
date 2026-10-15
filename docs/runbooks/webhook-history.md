# Webhook history: what happened, where it was sent, what they answered

Three things, in this order. An **event** is the record that something happened (`invoice.finalized`, `invoice.paid`, `subscription.past_due`). A **delivery** is that event on its way to one webhook endpoint (status, number of attempts, last answer). An **attempt** is one try, kept as it happened.

## Routes

Service clients (scope in brackets; a credential scoped to an owner key sees only that owner's events):

- `GET /v1.0/invoices/{id}/events` [`billing:invoices:read`]
- `GET /v1.0/subscriptions/{id}/events` [`billing:subscriptions:read`]: the subscription's own events and those of the invoices it produced.

Console (a signed-in operator, organization read access):

- `GET /v1.0/console/invoices/{id}/events`, `GET /v1.0/console/subscriptions/{id}/events`: the same, for the selected organization and mode.
- `GET /v1.0/console/events/{id}`: one event in full, with its payload, its deliveries and every attempt.
- `GET /v1.0/console/webhooks/{id}/deliveries`: everything one endpoint was sent, newest first.

Parameters: `limit` (1 to 50, default 20), `cursor` (the `cursor` of the previous page; `has_more` says whether another page exists), `include=attempts` (adds each delivery's `attempt_log`; the event route always includes it).

An event looks like:

```json
{
  "id": "evt_...", "type": "invoice.paid", "object": "invoice", "object_id": "in_...",
  "subscription_id": "sub_...", "owner_key": "wallet", "occurred_at": "2026-10-15T15:20:01Z", "livemode": true,
  "deliveries": [{
    "endpoint_id": "whe_wallet", "endpoint_url": "https://wallet.internal.aoctech.app/v1.0/internal/billing/events",
    "status": "delivered", "attempts": 2, "last_status_code": 200, "delivered_at": "...",
    "attempt_log": [
      {"number": 1, "at": "...", "status_code": 401, "duration_ms": 3, "error": "endpoint answered 401", "response": "..."},
      {"number": 2, "at": "...", "status_code": 200, "duration_ms": 412}
    ]
  }]
}
```

`endpoint_url` has no query string or credentials. `response` is the first 512 bytes of what the endpoint answered, to tell its own 401 from a gateway's page. The request body is never kept (it is the event's payload, `GET /console/events/{id}`).

## How it is stored

No new index. Pointer rows beside the event and the delivery, in the same transactions: `ENTITY#<object>#<id>#<event>` (an event is reachable from its object, and from its subscription), `ENDPOINT#<endpoint>#<event>` (written when the event is fanned out) and `ATTEMPT#<event>#<endpoint>#NN` (one per try, idempotent on the number). They expire with the event (90 days).

Events and deliveries from before this existed get their pointers from `api/cmd/backfill-event-lookups/run.sh <env>` (dry run) and `... -apply` (it Scans the webhook table, which the service role may not do, so it runs with the operator's AWS profile). Idempotent.
