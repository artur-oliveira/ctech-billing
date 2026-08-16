# ctech-billing — Technical Architecture (proposal)

## 1. Stack (matches existing CTech convention)

- **Backend**: Go, `cmd/` + `internal/` + `Dockerfile` + `Makefile` — identical layout to
  `ctech-account`, `ctech-dfe/api`, `ctech-wallet/api`. Do not introduce a second backend
  language into the company for this service.
- **Storage**: **DynamoDB**, one table per entity (a child in its parent's table), period reporting
  via a GSI whose sort key is queried by prefix. Decided and recorded in
  [`docs/adr/0002-datastore-dynamodb.md`](docs/adr/0002-datastore-dynamodb.md), which also carries
  the key layout and the limit accepted (pre-declared metrics only; ad-hoc analytics exit through
  an S3/Athena export, not through a relational migration). Every partition key begins with
  `{organization_id}#{livemode}` —
  [`ADR 0003`](docs/adr/0003-tenant-and-livemode-partition-key.md).
- **Infra**: **Terraform**, following `ctech-lbalancer`'s conventions —
  [ADR 0010](docs/adr/0010-infrastructure-as-terraform.md). This reverses the CDK direction this
  document originally carried: `ctech-lbalancer` is already a CDK-to-Terraform port with cutover
  knobs, so the question was never "CDK or introduce Terraform" but "the toolchain being migrated
  away from, or the one being migrated to". Cross-root dependencies go through SSM Parameter Store,
  never shared state — the same seam `ctech-lbalancer` uses to read `ctech-cdk`'s network outputs.
  Anything `ctech-cdk`'s shared constructs encapsulated must be ported into a **shared Terraform
  module**, not forked per repo.
- **Auth**: `ctech-account` OIDC for user-facing endpoints; `ctech-account` client-credentials
  (M2M) grant, scoped per `product_key`, for service-to-service endpoints.
- **Frontend**: **one Next.js app, two route shells** — the consumer portal and the merchant
  console, with independent layouts, navigation and density but shared design tokens and API
  client. Plus a public hosted checkout page and a public landing.
  This reverses the earlier "no UI for MVP — YAGNI" position, and the reason is a decision that
  came after it: with PIX paid directly on the invoice, **the consumer needs somewhere to pay**,
  and a hosted checkout is not something each product's SPA can reimplement. The rejected
  alternatives are recorded in the assessment § 7.1 — one app with a role switch (the consumer
  inherits operator density and permission becomes a conditional on every screen) and two separate
  repos (auth, tokens and HTTP client duplicated a third time).
  **Built:** the landing, the OAuth round trip, the portal shell (P1–P4) and the checkout (X1). The
  console shell is not. It ships as a **static export** — a directory of files, no server —
  [ADR 0013](docs/adr/0013-static-portal-same-origin-api.md) and § 10 below.

## 2. Service boundaries

```
                    ┌──────────────┐
   user token       │ ctech-account │  M2M client-credentials
  ┌────────────────▶│  (OIDC/OAuth) │◀────────────────────────┐
  │                  └──────────────┘                          │
  │                                                             │
┌─┴──────────┐   create invoice /      ┌───────────────┐   collection request*   ┌──────────────┐
│  SPA (any   │   report usage         │ ctech-billing │────────────────────────▶│ ctech-wallet │
│  product)   │────────────────────────▶│               │◀────────────────────────│              │
└─────────────┘   view/cancel sub      └───────┬───────┘   charge result*         └──────────────┘
                                                │
                                        invoice.paid webhook
                                                │
                                                ▼
                                        ┌───────────────┐
                                        │   ctech-dfe    │  (auto-emit NFS-e — suggested)
                                        └───────────────┘
```

`ctech-billing` **never** talks to a payment rail directly. It asks `ctech-wallet` to collect an
amount and later reconciles the outcome. When this was written that contract did not exist —
wallet had an internal synchronous real-balance debit and separate PIX-deposit/sandbox-purchase
flows, and no generic charge, webhook or charge lookup. It exists now, in the narrower shape § 3
describes: a caller-supplied *amount* on wallet's existing purchase machinery rather than a new
lifecycle.

One consequence of reusing that machinery rather than building a lifecycle: **it has no test
mode.** A charge opened in billing's test mode would be a real PIX charge, so collection is
live-only and billing refuses it before wallet is reached
([ADR 0004](docs/adr/0004-pix-on-invoice-via-wallet.md), second amendment).

## 3. Wallet integration contract (implemented in both repositories)

> **Superseded in scope by [`ADR 0004`](docs/adr/0004-pix-on-invoice-via-wallet.md), and shipped.**
> What was built is the smaller contract in
> [`docs/specs/2026-08-15-wallet-invoice-charge.md`](docs/specs/2026-08-15-wallet-invoice-charge.md):
> `POST /v1.0/internal/wallet/charge` taking a caller-supplied `amount_cents` under the new
> `internal:wallet:charge-amount` scope, with a per-client ceiling replacing the catalogue as the
> fraud defense. Billing's side is `api/internal/wallet` plus
> `POST /v1/internal/webhooks/wallet`; the reconciler covers the notification that never arrives.
> The generic `POST /v1/charges` lifecycle sketched below was never built. The requirements it
> states (idempotency on the invoice id, webhook replay defense, reconciliation polling) all
> survived into what was, which is why it is kept here rather than deleted.

```
POST /v1/charges
Headers: Idempotency-Key: <billing-invoice-id>
Body: { customer_ref, amount_cents, currency, method: "wallet_balance" | "pix", metadata: { invoice_id } }
→ 202 Accepted { wallet_charge_id, status: "pending" }

Webhook (ctech-wallet → ctech-billing):
POST /internal/webhooks/wallet
Body: { wallet_charge_id, status: "succeeded" | "failed", failure_reason?, occurred_at }
Signature: HMAC-SHA256 over raw body, header X-Wallet-Signature, verified before processing
```

Requirements this implies on the billing side:
- The `Idempotency-Key` on the outbound charge request is the invoice id — retrying "create
  charge for invoice X" after a timeout must be safe.
- Webhook handler must be idempotent on `wallet_charge_id` (store processed ids, reject
  replays) — this is the same defense `ctech-wallet` itself needs against the PIX provider,
  applied one layer up.
- If the webhook never arrives (delivery failure), a reconciliation job polls
  `GET /v1/charges/{id}` on `ctech-wallet` on a schedule (e.g. hourly for `OPEN` invoices past
  their expected settlement window) — never rely on webhooks as the only signal for something
  this important.

Current source baseline: wallet's closest operation is
`POST /v1.0/internal/wallet/real/debit`, authorized by the
`internal:wallet:debit-real` scope and protected by an idempotency key. It is synchronous and
not a substitute for the lifecycle above. Phase 3 therefore requires an explicit, versioned
cross-service contract plus implementation in both repositories. Boleto is excluded until a
Boleto rail actually exists.

## 4. Holiday calendar

Implemented in `api/internal/domain/brcal` as a pure function, not a maintained table:
- Fixed-date national holidays: 8 unconditional dates (month/day, not year-bound), **plus
  20 November** (Consciência Negra), which Lei 14.759/2023 made national **from 2024**. It carries
  a year condition rather than being added unconditionally, so recomputing a 2023 due date during
  a dispute or a backfill still yields what was correct then. This corrects the "8 dates" figure
  this document carried before the law.
- Moveable feasts: implement the Gauss/Meeus/Butcher Easter-date algorithm, then offset
  (Carnaval = Easter − 47/48 days, Good Friday = Easter − 2, Corpus Christi = Easter + 60).
- Unit-test against a table of *known* Easter dates for the next ~10 years as a regression
  check, but the production code path must compute, not look up.
- Scope to **national** holidays only for MVP, matching the brief exactly — do not silently
  add municipal/state holidays; that's a per-customer-location feature nobody asked for yet.

## 5. Invoice generation scheduler

- A single scheduled job. **Implemented as `services.Invoicer.RunDailySweep`, invoked by the
  `api/cmd/sweep` binary, and scheduled by a systemd timer on the leader instance** — daily at
  07:10 UTC, with `Persistent=true` so an instance replaced overnight runs the missed sweep on boot
  instead of skipping a day of invoices. This corrects the earlier "EventBridge Scheduler calling
  the billing service"
  option: the sweep reads `schedule-index`, the one index whose partition key carries no tenant
  prefix, so exposing it as a route would put the only cross-tenant read path on the public HTTP
  surface, guarded by nothing but a scope check. A one-shot binary has no route to mis-scope.
  It exits non-zero when the **live** sweep errors, so the schedule's failure alarm is the job's
  exit code; test-mode failures are logged but do not fail the run, because sandbox data is
  deliberately low-quality and an alarm that fires on it gets ignored. `-date` re-runs a missed
  day, which is safe by construction (step 3). The job:
  1. Finds every `Subscription` whose next invoice is due today (by cycle rule).
  2. For each of its items, builds a line — aggregating `UsageRecord`s for the closed period on the
     metered ones ([ADR 0018](docs/adr/0018-subscriptions-bill-several-prices.md)).
  3. Creates **one** `Invoice` under `{subscription_id}:{period_start}` — safe to re-run.
  4. Calls the Wallet charge endpoint, unless the total is zero, in which case the invoice is
     settled on issue and never chased ([ADR 0019](docs/adr/0019-zero-total-invoices.md)).
- Runs once daily; national-holiday-aware due-date computation means "today" can still
  correctly skip generating on a day nothing is actually due.

## 6. Outbound events

A consuming product learns what happened here through a signed webhook, not by
polling. The routing decision is the interesting half and it is
[ADR 0016](docs/adr/0016-webhook-routing-by-product-owner.md): events are routed
by **product owner**, because tenant zero is one organization holding every CTech
service's subscriptions, and a per-organization endpoint would send each service
the others' invoices.

Three properties, each of which is the reason for a piece of the design:

- **The event row is written inside the transaction of the change it describes.**
  Same discipline as the audit row: an invoice reaching PAID with no
  `invoice.paid` queued is a consumer who never learns their customer paid.
- **Endpoints are resolved by a separate job, not in the write path.** Fan-out
  reads configuration; keeping that out of the transaction is why an invoice can
  be paid while every endpoint in the tenant is misconfigured.
- **The payload is an id and a type.** The consumer reads the entity back through
  the API with its own credential — billing's own posture toward wallet's
  notify-back, one layer on. A misconfigured URL leaks an id, not a bill.

## 7. Dunning

**Not charge retry.** On a card rail dunning means trying the charge again; PIX
is a pull, so billing cannot debit anybody. The policy is therefore a schedule of
messages and state changes: a note at D−3, reminders at D+1/D+3/D+7, `PAST_DUE`
at D+10, `UNCOLLECTIBLE` and cancellation at D+30
(`internal/domain/billing/dunning.go`).

The invoice stays payable throughout, escalation included. Each invoice stores
the step it is on, which is what makes `cmd/dunning -date=…` replayable.

A zero-total invoice never enters the queue at all — `Finalize` writes no
schedule keys for it, so "no reminders about R$ 0,00" is a property of the sparse
index rather than a filter somebody has to remember
([ADR 0019](docs/adr/0019-zero-total-invoices.md)).

Reminders go out over SES from `internal/email`. Notification delivery is barely
billing's domain, and the reason it lives here anyway is that no notification
service exists in this family and ctech-account already sends its own mail — the
convention is that a service sends what it is responsible for saying.

## 8. Observability & audit

- Structured logs (same logger/format as the rest of the company's Go services — reuse,
  don't reinvent).
- Every state transition on `Subscription`/`Invoice` written to an append-only audit table,
  keyed by entity id, independent of application logs (logs rotate/expire; this shouldn't).
- Metrics: invoices generated/day, charge success rate, dunning-triggered cancellations,
  scheduler run duration/failures — wire alarms on the last two, they're the ones that
  silently cost real revenue if broken.

## 9. Security

- M2M client credentials scoped to `product_key` (§ OVERVIEW.md suggestion 6) — enforced in
  `ctech-account`'s token claims and checked on every mutating billing endpoint.
- **Two surfaces, two resolvers, and neither can reach the other.** `/v1/*` takes its tenant *and*
  its mode from the credential; `/v1/console/*` takes the tenant from the signed-in owner and the
  mode from a required `X-Billing-Mode` header, because a person legitimately works in both test and
  live and an integration never does. `RequireM2MScope` rejects session tokens, `RequireUserScope`
  rejects service tokens, so a credential cannot reach the route that reads the header
  ([ADR 0011](docs/adr/0011-console-session.md)). Console routes are read-only for now.
- **Personal data is encrypted per field, not only per disk**
  ([ADR 0017](docs/adr/0017-field-level-encryption.md)). The claim below said
  "encrypted at rest by the repository layer" long before anything encrypted it;
  DynamoDB's own at-rest encryption protects the disk and protects the value from
  nobody holding `dynamodb:Query`. `internal/crypto` now seals `tax_id` and the
  webhook signing secret with AES-256-GCM, the service refuses to start without a
  key, and an integration test reads the raw item to prove it.
- **Minimum PII, not zero PII.** The original "no PII beyond `customer_ref`" rule did not survive
  contact with the product: an invoice needs a name and a due-date notification needs an email.
  The rule is now "the minimum needed to invoice and to notify, and nothing else" — name, email,
  `tax_id` (encrypted at rest, masked everywhere, revealing it is an audited action), locale.
  **No phone** (no SMS/WhatsApp notification exists, so it would be PII held for nothing).
  Card data, PIX keys and bank accounts are still **never** stored — those belong to
  wallet/PSP and are referenced by opaque id. Retention per record type is fixed in
  [`ADR 0009`](docs/adr/0009-retention-and-ttl.md).
- Webhook signature verification (§ 3) is mandatory, not optional-with-a-TODO.
- **This service is an OAuth resource server and nothing else.** It verifies tokens against
  ctech-account's JWKS, publishes the scopes it accepts from its own repository and pipeline
  ([ADR 0014](docs/adr/0014-billing-publishes-its-own-scopes.md)), and serves RFC 9728 metadata at
  `/.well-known/oauth-protected-resource`. It issues no token, stores no credential, and the front
  end never sees one either: the refresh token is ctech-account's `HttpOnly` cookie and the access
  token lives in browser memory. `/login` is deliberately not a form for the same reason — a page
  on this domain that asks for a password is a page teaching customers to be phished.

## 10. Front-end delivery

The portal is a **static export** ([ADR 0013](docs/adr/0013-static-portal-same-origin-api.md)):
`output: "export"` produces a directory of files, and CloudFront serves them from a private S3
bucket through an Origin Access Control. There is no Node process in production, so there is
nothing to patch, scale, or roll back beyond objects.

Two things follow, and both are load-bearing:

- **`/v1/*` and `/.well-known/*` are ordered cache behaviours on the same distribution**, pointed
  at the HAProxy origin the API already sits behind. The browser is same-origin with its API, so
  there is no CORS configuration to maintain, no preflight, and `connect-src 'self'` plus
  ctech-account is a sufficient CSP. `NEXT_PUBLIC_API_URL` is empty in a deployed build.
- **Pretty URLs come from a CloudFront Function reading a KeyValueStore route manifest**, written
  by the deploy from the export's own output. `custom_error_response` was rejected: it is
  distribution-wide, so mapping 404 to `/404.html` would also replace the API's RFC 7807 problem
  bodies on the `/v1/*` behaviour.

`terraform/billing/frontend.tf` is an HCL port of `ctech-cdk`'s `nextjs-static-frontend`, not a new
pattern — same bucket posture, same OAC, same function, same header policy.

## 11. Delivery pipeline

Five workflows, entered from `deploy.yml` on a push to `main`, `staging` or `dev`. Path filters
decide which stages run; the order between them is a dependency chain, not a preference:

```
Terraform → OAuth scopes → API → Frontend
```

Terraform creates the bucket and the ASG the next stages deploy into and writes the SSM parameters
they read their targets from. Scopes publish before the API deploys, because the API rejects a
token carrying a scope ctech-account never learned to issue. The frontend is last because it is the
only stage a customer sees, and a portal that ships ahead of its API renders errors.

The API stage builds five arm64 binaries — `server`, `sweep`, `reconcile`, `deliver`, `dunning` —
because the systemd timers expect all of them, and a `sweep` from a different revision writes rows
the API does not read.
It uploads them and drives `/opt/app/deploy.sh` (written at boot by
`terraform/assets/bootstrap.sh.tftpl`) through SSM RunCommand.

Four IAM roles, one per stage, none trusting a pull request
([ADR 0015](docs/adr/0015-four-deploy-identities.md)). Everything that runs on a PR — the full CI
suite and `terraform validate` with `-backend=false` — is designed to need no AWS credentials at
all.
