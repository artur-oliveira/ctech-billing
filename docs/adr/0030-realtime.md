# ADR 0030: Realtime, a change log and a cursor

Status: accepted (2026-10-15). Spec: `docs/specs/2026-10-14-realtime.md`.

## Context

Screens learned of a payment or a classification only by refetching on focus, and the portal held one Server-Sent Events stream per open Pix. We wanted the console, portal, checkout and Finanças to update by themselves, on one instance today and several later, cheaply, and without any screen losing a change when a network, an instance or Valkey fails.

## Decision

Every change that matters is written, in the transaction that commits the state, to a per-scope **change log** (`RT#{scope}` in the webhook table, 48 h TTL), then published on the ws registry. A WebSocket (`GET /v1.0/ws`) authenticates on its first frame, replays from the client's **cursor** (a ULID), and streams live; a plain HTTP route serves the same cursor when the socket cannot be had. A change carries ids and state words only: the client invalidates query keys and refetches through the authorized REST API, so authorization stays where it was and nothing is patched into caches.

## Alternatives

- **SSE only** (what the portal had): one-way is enough, but one connection per invoice, no resume, and the same fan-out problem across instances.
- **Polling only**: the cost is requests times readers times seconds for facts that change a few times a day; kept as the fallback.
- **DynamoDB Streams into a push service**: another component to run and pay for, and per-tenant authorization re-implemented there.

## Consequences

- At-least-once delivery with de-duplication on change id and revision at the client; a `Gap` (cursor too old, replay limit, slow consumer) means refetch everything the surface holds.
- Cost is the log writes (one extra item per mutation), the replay reads and instance memory. Metrics are in-process counters in one log line a minute; **no CloudWatch alarms or custom metrics** (owner's rule, cost). Health is read with `scripts/realtime-check.sh`.
- Off by default behind `REALTIME_ENABLED` (API) and `NEXT_PUBLIC_REALTIME[_SURFACES]` (UI); rollout one surface at a time (`docs/runbooks/realtime.md`).

## Limits accepted

- The portal for an organization's customers has no user scope, so bills of an organization the person administers keep refetching on focus.
- Nothing is delivered after 48 h offline: the client refetches.

## Reopen if

Fan-out outgrows one Valkey, or a surface needs server-initiated data larger than ids (then a typed payload, not a patch).
