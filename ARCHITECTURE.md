# ctech-billing — Technical Architecture (proposal)

## 1. Stack (matches existing CTech convention)

- **Backend**: Go, `cmd/` + `internal/` + `Dockerfile` + `Makefile` — identical layout to
  `ctech-account`, `ctech-dfe/api`, `ctech-wallet/api`. Do not introduce a second backend
  language into the company for this service.
- **Storage**: DynamoDB (matches `ctech-dfe`'s documented `DynamoDB-Tables.md` pattern) is the
  default candidate, **but** billing math (pro-rata, tiered usage aggregation, plan
  versioning joins) is relational-shaped. Recommendation: confirm what `ctech-wallet/api`
  actually uses for its ledger (pending audit) and follow suit — a ledger-adjacent service and
  a billing service have very similar consistency needs (append-only records, strong
  idempotency, occasional range queries for period aggregation). If `ctech-wallet` uses
  Postgres/Aurora, `ctech-billing` should too, for the same reasons, and to keep the company's
  operational surface (backup, migration, on-call knowledge) to one relational engine instead
  of two.
- **Infra**: CDK, importing shared constructs from `ctech-cdk` rather than redefining VPC/IAM
  boundary policies per service (see the cross-stack report for what's actually shared today).
- **Auth**: `ctech-account` OIDC for user-facing endpoints; `ctech-account` client-credentials
  (M2M) grant, scoped per `product_key`, for service-to-service endpoints.
- **Frontend**: none owned by this service for MVP — subscription view/cancel is a thin
  panel embedded in each product's own SPA (e.g. `ctech-dfe/ui`), using `ctech-oauth-client`
  for auth exactly like the other SPAs. A dedicated `ctech-billing/ui` is unnecessary for MVP —
  YAGNI. Revisit only if a standalone billing/admin portal becomes a real need.

## 2. Service boundaries

```
                    ┌──────────────┐
   user token       │ ctech-account │  M2M client-credentials
  ┌────────────────▶│  (OIDC/OAuth) │◀────────────────────────┐
  │                  └──────────────┘                          │
  │                                                             │
┌─┴──────────┐   create invoice /      ┌───────────────┐   debit / PIX / Boleto  ┌──────────────┐
│  SPA (any   │   report usage         │ ctech-billing │────────────────────────▶│ ctech-wallet │
│  product)   │────────────────────────▶│               │◀────────────────────────│              │
└─────────────┘   view/cancel sub      └───────┬───────┘   charge result webhook  └──────────────┘
                                                │
                                        invoice.paid webhook
                                                │
                                                ▼
                                        ┌───────────────┐
                                        │   ctech-dfe    │  (auto-emit NFS-e — suggested)
                                        └───────────────┘
```

`ctech-billing` **never** talks to a payment rail directly. It asks `ctech-wallet` to collect
an amount against a `customer_ref`; `ctech-wallet` decides whether that's a balance debit or a
new PIX/Boleto charge, and reports back success/failure asynchronously via webhook plus a
synchronous "accepted" response. This mirrors how `ctech-wallet`'s own `pix-gateway` should be
treating the actual bank rail — same pattern, one layer up.

## 3. Wallet integration contract (proposal — confirm against actual `ctech-wallet` API once its audit lands)

```
POST /v1/charges
Headers: Idempotency-Key: <billing-invoice-id>
Body: { customer_ref, amount_cents, currency, method: "wallet_balance" | "pix" | "boleto", metadata: { invoice_id } }
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

## 4. Holiday calendar

Implement as a pure function, not a maintained table:
- Fixed-date national holidays: hardcode the 8 dates (month/day, not year-bound).
- Moveable feasts: implement the Gauss/Meeus/Butcher Easter-date algorithm, then offset
  (Carnaval = Easter − 47/48 days, Good Friday = Easter − 2, Corpus Christi = Easter + 60).
- Unit-test against a table of *known* Easter dates for the next ~10 years as a regression
  check, but the production code path must compute, not look up.
- Scope to **national** holidays only for MVP, matching the brief exactly — do not silently
  add municipal/state holidays; that's a per-customer-location feature nobody asked for yet.

## 5. Invoice generation scheduler

- A single scheduled job (EventBridge Scheduler → the billing service, or a CDK-scheduled
  Lambda if this ends up being cheaper than an always-on cron inside the Go service — the
  workload is a daily sweep, not a latency-sensitive path, so Lambda-on-a-schedule is a
  legitimate cost-conscious choice here, unlike the IPv4/Inter-API Lambda whose cost profile
  the CDK audit is separately checking) that:
  1. Finds every `Subscription` whose next invoice is due today (by cycle rule).
  2. For `METERED` plans, aggregates `UsageRecord`s for the closed period.
  3. Creates the `Invoice` with the idempotency key from § 2 — safe to re-run.
  4. Calls the Wallet charge endpoint.
- Runs once daily; national-holiday-aware due-date computation means "today" can still
  correctly skip generating on a day nothing is actually due.

## 6. Observability & audit

- Structured logs (same logger/format as the rest of the company's Go services — reuse,
  don't reinvent).
- Every state transition on `Subscription`/`Invoice` written to an append-only audit table,
  keyed by entity id, independent of application logs (logs rotate/expire; this shouldn't).
- Metrics: invoices generated/day, charge success rate, dunning-triggered cancellations,
  scheduler run duration/failures — wire alarms on the last two, they're the ones that
  silently cost real revenue if broken.

## 7. Security

- M2M client credentials scoped to `product_key` (§ OVERVIEW.md suggestion 6) — enforced in
  `ctech-account`'s token claims and checked on every mutating billing endpoint.
- No PII stored beyond `customer_ref` — keep `ctech-billing` boring from a data-breach-impact
  perspective; identity and payment-instrument data belong to `ctech-account`/`ctech-wallet`.
- Webhook signature verification (§ 3) is mandatory, not optional-with-a-TODO.
