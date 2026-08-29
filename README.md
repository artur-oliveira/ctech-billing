# ctech-billing

Recurring subscription and metered billing service for the CTech ecosystem.

**Documentação jurídica vigente:** os Termos do CTech Billing publicados pela
Central Jurídica estão na versão **1.1**, em
`https://accounts.aoctech.app/products/billing`. Billing ainda não possui um
registro próprio de aceite/versionamento no serviço; até que esse fluxo exista,
a versão é uma referência documental e não um gate de acesso.

`ctech-billing` owns the **subscription and invoice domain**: plans, subscriptions, billing
cycles, pro-rata, invoice generation, and dunning. It does not move money itself — every
charge is collected by delegating to [`ctech-wallet`](../ctech-wallet), which owns the
current DynamoDB ledger and the PIX integrations. That contract
([spec](docs/specs/2026-08-15-wallet-invoice-charge.md)) is now implemented on both sides.

Status: **the MVP is built and unreleased.** Domain, persistence, the four API surfaces, the
collection path, reconciliation, dunning, outbound webhooks, the consumer portal, the public
checkout, the infrastructure they run on and the pipeline that deploys them are implemented and
green locally. The prerequisites in
[`.github/workflows/README.md`](.github/workflows/README.md) that gate payments are now in place:
wallet's `m2m-clients` entry, keyed by the OAuth client id `ctech-charge`, and that client holding
`internal:wallet:charge-amount`. `@aoctech/ui` is on npm at `0.1.1` and the front end builds from
the registry. What is left is **the first deploy, which has never run**, and the `email-from` SSM
parameter. Nothing is deployed, nothing is committed, and nothing has moved real money.
[PLAN.md § What is actually left](PLAN.md) is the ordered list.

## Error observability

The API uses `api-commons/observability` and `api-commons/observability/fiber`. Every RFC 7807 response is logged once
at the HTTP boundary (`WARN` for 4xx, `ERROR` for 5xx) with `request_id`, method, path, problem type and the internal
cause when available. `X-Request-ID` is preserved or generated before panic recovery, returned to callers and exposed
through CORS. Internal causes are never serialized. Logs exclude tokens, HMAC values, tax IDs, invoice/customer
payloads and other unnecessary PII. This adds no OpenTelemetry exporter or custom metric.

| Path | What is there |
|---|---|
| `api/internal/domain/brcal` | Civil dates, Brazilian national holiday calendar, business-day roll-forward. Tested. |
| `api/internal/domain/billing` | Money arithmetic and rounding policy, `metadata` rules, billing cycles, proration, and the entity state machines. Tested. |
| `api/internal/repositories` | DynamoDB persistence on `ctech-go-common`'s shared primitives: one table per entity with each child in its parent's table, tenant-scoped keys, retention/TTL, gapless invoice numbering, and audit written inside the transaction of the change it records — across two tables, which changes nothing, because a transaction item carries its own table name. The schema is `schema.json`, the one file Terraform also reads. |
| `api/internal/services` | The invoice-generation sweep, the subscribe/cancel flow, the collection path (`Collector`: open a charge, reuse a live one, settle from a re-read), reconciliation for charges whose webhook never arrived, the signed payment link, **dunning** (the schedule that chases an unpaid invoice), and **outbound webhook delivery**. |
| `api/internal/crypto` | Field-level encryption for the stored values that are personal data on their own — today the customer's CPF/CNPJ ([ADR 0017](docs/adr/0017-field-level-encryption.md)). The service refuses to start without a key: writing a tax id in the clear is a failure no screen shows. |
| `api/internal/email` | What billing says to a customer, and when. The SESv2 transport itself moved to `gopkg.aoctech.app/api-commons/email` — it was the same code here and in ctech-account — and the templates stayed, because a shared package that knew about invoices would be a notification service. |
| `api/internal/invoicepdf` | The invoice as a PDF, rendered in-process with [folio](https://github.com/carlos7ags/folio) from an HTML template, and stored write-once in its own S3 bucket. The document carries frozen facts and no status — an invoice does not stop being that document by being paid — which is what makes rendering it on first download safe rather than requiring a job at finalization. |
| `api/internal/jobs` | How the four scheduled binaries report a failure to a person: one alert to the account's SNS topic, and the existing exit codes. Not CloudWatch alarms — those are billed per alarm per month to say the one thing every job already knows when it happens. The failure a metric would miss entirely is the one that alerts loudest: a binary that dies on configuration emits no counters and looks like a quiet day. |
| `api/internal/settlement` | "This invoice was paid", published over the Valkey this service already uses, so the browser holding a PIX code hears it from whichever instance the webhook reached. The payment stream still re-reads the invoice every thirty seconds — pub/sub has no replay, so the publish makes the common case instant and the re-read makes every case correct. |
| `api/internal/provision` | A tenant as a reviewable JSON file: the organization, the integrations admitted to act for it, the catalogue and the webhook endpoints. It exists because nothing else can create the first row — every write path resolves its tenant from a credential, and a credential is itself a row. |
| `api/internal/wallet` | The client for `ctech-wallet`'s charge contract, plus the HMAC verification for its notify-back. |
| `api/internal/api/v1`, `internal/middleware` | Four HTTP surfaces: the M2M API (tenant from the credential, idempotency on every mutating route), the console API (tenant from the signed-in owner, mode from a required header — [ADR 0011](docs/adr/0011-console-session.md)), the portal API (tenant zero, filtered to the signed-in customer — [ADR 0012](docs/adr/0012-portal-serves-tenant-zero.md)), and the public checkout (no session at all; a signed link is the whole credential). The first three verify JWTs against `ctech-account` and gate every route on a scope. |
| `api/internal/oauthresource` | The 15-scope manifest this service publishes about itself, plus the RFC 9728 `/.well-known/oauth-protected-resource` document derived from it. Billing is a resource server and registers its own vocabulary from its own pipeline — [ADR 0014](docs/adr/0014-billing-publishes-its-own-scopes.md). A test asserts the manifest and `middleware.AllScopes` are the same set, because a scope enforced here and unknown to ctech-account fails silently, at runtime, in one direction. |
| `api/cmd/server` | The service entry point. |
| `api/cmd/sweep` | The daily invoice sweep, as a one-shot binary. Deliberately not a route: the sweep is the one cross-tenant read path, so it has no HTTP surface to mis-scope. `sweep -date=YYYY-MM-DD` re-runs a missed day, which is safe because a period already billed is skipped, not billed twice. |
| `api/cmd/seed` | Applies a tenant plan (`api/tenants/*.json`) to one mode. Create-or-skip on every row, so it is safe to re-run and safe to extend. A binary and not a route, for the same reason the sweep is one. |
| `api/cmd/dunning` | The daily pass over invoices nobody has paid, on **the schedule each invoice was issued under** — copied onto it at finalization from the product's policy, the organization's, or the built-in default, so editing a policy never rewrites what happens to a bill already being chased. By default: a reminder before the due date and three after it, then PAST_DUE, then UNCOLLECTIBLE. It is **not** charge retry — PIX is a pull, so there is nothing to retry — which is why the policy is a schedule of messages and state changes rather than a backoff. `-date` re-runs a missed day, safely: each invoice stores the step it is on. |
| `api/cmd/deliver` | Outbound webhooks, on a one-minute timer. Two passes: match queued events to endpoints, then make one signed HTTP attempt each, with exponential backoff. |
| `api/cmd/reconcile` | Asks wallet about every charge billing is still waiting on, hourly. A webhook is a delivery and deliveries fail; without this, a lost notification is a customer who paid looking at an invoice that says they did not. Separate from `sweep` because it runs on a different clock — invoicing is a daily fact about a calendar, an unanswered charge is an hourly fact about an integration. |
| `api/tests/integration` | Repository, API and payment tests against a real DynamoDB (`make test-integration`): subscribe → invoice on the right date with a holiday-adjusted due date, invoice → QR code → webhook → `PAID` against a fake wallet that implements the spec, the four reconciliation outcomes, a tax id proven encrypted by reading the raw item, the dunning policy walked one step per day, and two CTech services in one tenant proven to receive only their own webhooks. |
| `terraform/billing/` | The DynamoDB tables and their indexes — built with `for_each` over `api/internal/repositories/schema.json`, the same file the Go service embeds, so the schema exists once — TTL, the service IAM role (which explicitly **denies** `dynamodb:Scan`), the SSM parameters, the compute (one ASG of private-IPv4-only instances behind the shared HAProxy edge), and the route it registers. Terraform, not CDK — [ADR 0010](docs/adr/0010-infrastructure-as-terraform.md). |
| `terraform/assets/` | The instance bootstrap, as a shell script rather than a string array: nginx, the realip refresh, the CloudWatch agent, the systemd unit for the API, and the four timers that run `sweep`, `reconcile`, `deliver` and `dunning` on the leader instance. |
| `terraform/billing/frontend.tf` | The portal's **former** hosting: a private bucket behind CloudFront with an Origin Access Control, a KeyValueStore route manifest read by a CloudFront Function for pretty URLs, a response-headers policy, and a `/v1.0/*` behaviour forwarding to the HAProxy origin. Nothing routes through it any more — `billing.aoctech.app` is served by Cloudflare Workers Static Assets ([ADR 0020](docs/adr/0020-portal-on-cloudflare-workers.md)) and the pages call `billing-api[-env].aoctech.app` directly ([ADR 0013](docs/adr/0013-static-portal-same-origin-api.md)'s amendment). It is still applied only because the teardown has not run; deleting it is Phase 4 of `ctech-cdk/docs/plans/2026-08-20-frontend-cloudflare-migration.md`. |
| `terraform/github/` | The four GitHub OIDC roles the pipeline assumes — `infra`, `api`, `frontend`, `scopes` — each scoped to what one stage does, none trusting a pull request ([ADR 0015](docs/adr/0015-four-deploy-identities.md)). Its own root, one workspace: the roles are one set for all three environments. |
| `.github/` | The pipeline, plus weekly Dependabot over the Go module, the UI, the pinned actions and both Terraform roots. `ci.yml` runs on every PR with no AWS credentials at all and is itself a chain — static analysis (gofmt, vet, staticcheck, govulncheck, each also under `-tags integration`) → API → UI. `deploy.yml` owns the path filters and calls the three workflows for infra, API and frontend plus the scopes stage that calls ctech-account's reusable publisher; that order — Terraform → scopes → API → frontend — is a dependency chain and [its README](.github/workflows/README.md) says why each link is where it is. |
| `ui/` | The Next 16 app, built and passing — **two shells, one account**. The portal: the public landing, the OAuth round trip, the five signed-in screens (P1–P5) and the public checkout (X1). The console: C2–C9 at `data-density="compact"`, with the test/live switch that is a header on every request and part of every query key. One sign-in serves both, and the console's routes 403 for a customer who was never provisioned an organization — which the shell explains rather than erroring. A production build is `output: "export"` — a directory of files, no server ([ADR 0013](docs/adr/0013-static-portal-same-origin-api.md)), deployed to Cloudflare Workers Static Assets ([ADR 0020](docs/adr/0020-portal-on-cloudflare-workers.md)) by `ctech-cdk`'s reusable workflow. Primitives come from `@aoctech/ui` on npm, built in the sibling repo `ctech-ui`; `DESIGN.md` records the token system and what was rejected, `PRODUCT.md` the users and the screen list. |
| `docs/adr/` | The 20 architecture decisions that shape all of the above. |
| `docs/analysis/` | The product/architecture assessment those decisions came out of. |
| `docs/specs/` | Cross-repository contracts — currently the [`ctech-wallet` charge contract](docs/specs/2026-08-15-wallet-invoice-charge.md), now implemented on both sides. |
| `api/internal/repositories/creditnotes.go` | The corrections issued against an invoice, nested in its partition. Written with their audit row and their event in one transaction, conditional on the invoice's status — two operators crediting the same invoice at once cannot both pass a total check that was true for each separately. A credit note never moves money: wallet refunds, billing records that it did. |
| — | **Not built:** C1 (visão geral) and C17 (configurações). C1 needs aggregates no endpoint computes; C17 has nothing to configure yet. Revealing a tax id in full is also absent — `RecordTaxIDAccess` exists and no route calls it, and a button that showed the CPF without writing that audit row would be worse than none. Named in PLAN.md. |

### API surface (v1, M2M)

All routes take the tenant from the credential — there is no `organization_id`
parameter and no `livemode` flag anywhere, deliberately (ADR 0003).

| Route | Scope |
|---|---|
| `GET /v1.0/health` | — |
| `POST /v1.0/customers` · `GET /v1.0/customers/:id` | `billing:customers:write` / `:read` |
| `POST /v1.0/subscriptions` · `GET /v1.0/subscriptions/:id` · `POST /v1.0/subscriptions/:id/cancel` · `POST /v1.0/subscriptions/:id/change` | `billing:subscriptions:write` / `:read` |
| `POST /v1.0/usage` | `billing:usage:write` |
| `GET /v1.0/invoices` · `GET /v1.0/invoices/:id` | `billing:invoices:read` |
| `GET /v1.0/products` · `GET /v1.0/products/:id` | `billing:products:read` |
| `GET /v1.0/entitlements?customer_ref=` | `billing:entitlements:read` |

### Console writes (browser session, never a service token)

| Route | Scope |
|---|---|
| `POST /v1.0/console/subscriptions/:id/cancel` · `/change` | `billing:subscriptions:write` |
| `POST /v1.0/console/invoices/:id/finalize` · `/void` · `/credit-notes` | `billing:invoices:write` |
| `POST /v1.0/console/customers` | `billing:customers:write` |
| `POST /v1.0/console/products` · `POST /v1.0/console/prices` · `POST /v1.0/console/prices/:id/archive` | `billing:products:write` |
| `POST /v1.0/console/customers/:id/tax-id` | `billing:customers:write` |
| `GET /v1.0/console/invoices/:id/pdf` | `billing:invoices:read` |
| `PUT /v1.0/console/settings/issuer` | `billing:invoices:write` |
| `PUT /v1.0/console/settings/dunning` · `PUT /v1.0/console/products/:id/dunning` | `billing:invoices:write` |

Revealing a tax id is a `POST` because it has an effect — it writes the audit row
naming who looked — and the row is written **before** the value is returned: the
other order loses the record exactly when the response fails to arrive.

The dunning schedule is behind the invoice write scope rather than the catalogue
one on both routes: what it changes is how unpaid bills are chased, and somebody
who may set a price is not thereby somebody who may decide when a customer loses
access.

Each is a no-op when the invoice is already in the state it asks for — a second
click must not spend a second invoice number or write a second audit row — and
the credit note is the exception that is refused rather than replayed, because
crediting past the invoice total is the one thing this document may never do.

Every `POST` requires an `Idempotency-Key`. A repeat returns the first response
verbatim with `Idempotent-Replay: true`; the same key with a different body is a
`409`.

### A subscription bills a list of prices

```json
POST /v1.0/subscriptions
{"customer_id": "cus_…", "items": [{"price_id": "price_…", "quantity": 1}]}
```

Several items is the normal case, not an advanced one: a usage-based plan meters
NF-e, NFC-e, CT-e and MDF-e separately and sends **one** bill with four lines
([ADR 0018](docs/adr/0018-subscriptions-bill-several-prices.md)). The items must
agree on their recurrence, their timing and their product owner, because a
subscription has exactly one of each.

`POST /v1.0/usage` therefore carries a `price_id` saying which item the consumption
is for. It may be omitted only when the subscription has one item — with several
meters there is no defensible default, and guessing bills NFC-e volume at the
CT-e price.

**An invoice whose total is zero is issued and settled on the spot**
([ADR 0019](docs/adr/0019-zero-total-invoices.md)). That is the Free plan: a real
subscription, a real numbered document, `invoice.paid` emitted the same as any
other, no charge opened and no reminder scheduled.

### Changing plan mid-period

```json
POST /v1.0/subscriptions/sub_…/change
{"items": [{"price_id": "price_…", "quantity": 1}], "effective": "now"}
```

`items` is the **complete** new set, not a delta — otherwise "remove the CT-e
meter" and "forgot to send the CT-e meter" are the same request. The anchor and
the period index do not move: changing plan does not change the day the customer
is billed on. The recurrence may not change; a monthly plan becoming annual is a
new subscription, not a swap.

The difference for the remainder of the current period is billed on **one invoice
with two separate lines** — a credit for the unused part of the old price and a
charge for the new one — never a single net figure a customer cannot reconstruct.
Metered prices are never prorated: usage is billed for what was used, and the
closed period's consumption still arrives on the normal sweep.

**A downgrade issues no invoice.** A change that nets zero or negative is money
owed back, which is a `CreditNote` — a different document, and one this service
does not yet issue. So the plan changes, nothing is billed, and the unused
remainder of the period already paid for is forfeited. That is the branch to
revisit when credit notes exist.

### Where to send the customer to pay

Every invoice payload — `POST /v1.0/subscriptions`, `GET /v1.0/invoices/:id`, the
console's, the list — carries `checkout_url` when there is something to collect:

```json
{"invoice": {"id": "in_…", "amount_due": 35000,
             "checkout_url": "https://billing.aoctech.app/checkout?token=…"}}
```

It is the signed public link, not a portal URL. A consumer who has just signed in
to the DF-e should not have to consent to a second OAuth client to pay their first
bill, and the link needs no session at all.

**Branch on the field being present, never build the URL.** It is absent exactly
when the invoice cannot be paid, and the three cases are ordinary rather than
exceptional:

| Situation | `checkout_url` | Why |
|---|---|---|
| Pro, R$ 350,00 due | present | there is a bill |
| Free plan | absent | the invoice closed on issue owing nothing |
| Sob demanda, first period | absent | metered arrears — the period has not been billed yet |
| Draft, paid or void | absent | a link to a draft 404s by design |
| Test mode | absent | collection is live-only, [ADR 0004](docs/adr/0004-pix-on-invoice-via-wallet.md) |

So the flow for contracting a plan is: create the customer **with `user_id`**
(without it there is nobody to charge), create the subscription, and redirect to
`invoice.checkout_url` **if it is there**. Entitlement comes from
`GET /v1.0/entitlements`, and settlement from the `invoice.paid` webhook — not from
the browser coming back, which for PIX it may never do.

### One call renders the whole billing screen

`GET /v1.0/entitlements` answers more than "can this customer use the product":

```json
{"customer_id": "cus_…", "entitled": false, "subscriptions": [{
  "id": "sub_…", "status": "INCOMPLETE", "entitled": false, "plan": "pro",
  "cancel_at_period_end": false,
  "current_period": {"start": "2026-03-10", "end": "2026-04-10"},
  "items": [{"price_id": "price_dfe_pro_monthly", "product_id": "prod_dfe_pro",
             "type": "fixed", "unit_amount": 35000, "quantity": 1,
             "metadata": {"plan": "pro", "quota_nfe": "1200"}}],
  "open_invoice": {"id": "in_…", "total_cents": 35000, "due_date": "2026-03-10",
                   "checkout_url": "https://billing.aoctech.app/checkout?token=…"}
}]}
```

Three of those fields exist so a consuming product does **not** keep its own copy
of something billing already knows. `plan` and the per-item `metadata` are where
the quotas live, so a limit a product enforces and the limit the invoice says was
sold are the same number. `cancel_at_period_end` is the notice that entitlement
ends at the boundary — `entitled` is still true today, and a screen showing only
that tells the customer nothing is happening right up until it does.
`open_invoice` is what turns "pagamento pendente" into a button.

Billing does not read the quota keys. Metadata is opaque to this service by
decision ([ADR 0008](docs/adr/0008-opaque-metadata.md)); it carries them, and
what a quota *means* is the consuming product's rule.

### API surface (v1, console)

The browser surface for the merchant console (screens C1–C9 and C17). It is
**read-only** and takes a user session token, never a service token: the
organization comes from the token's subject, and the mode comes from a required
`X-Billing-Mode: live | test` header — the one place in the service where a
request states its own mode, and why that is safe is
[ADR 0011](docs/adr/0011-console-session.md).

| Route | Scope |
|---|---|
| `GET /v1.0/console/session` | `billing:organization:read` |
| `GET /v1.0/console/invoices?year=&month=&cursor=` · `GET /v1.0/console/invoices/:id` | `billing:invoices:read` |
| `GET /v1.0/console/subscriptions?cursor=` · `GET /v1.0/console/subscriptions/:id` | `billing:subscriptions:read` |
| `GET /v1.0/console/customers?cursor=` · `GET /v1.0/console/customers/:id` | `billing:customers:read` |
| `GET /v1.0/console/products` · `GET /v1.0/console/products/:id` | `billing:products:read` |
| `POST /v1.0/console/subscriptions/:id/cancel` · `POST /v1.0/console/subscriptions/:id/change` | `billing:subscriptions:write` |

Detail routes return the entity plus its audit timeline, which is what makes
"who changed this, and why" answerable on the screen where it is asked. The
payment link an operator sends when a customer asks "can you send it again?" is
`invoice.checkout_url`, the same field the M2M surface publishes and under the
same rule — a link one surface hands out that the other refuses is a support
call.

Cancellation is the one write here, and it takes both modes: an operator ending
a subscription immediately is a deliberate decision, and refusing it would push
them into the M2M API, where the audit trail records a client id instead of a
person.

### API surface (v1, portal)

The consumer surface, for **CTech's own customers** — tenant zero
([ADR 0012](docs/adr/0012-portal-serves-tenant-zero.md)). A third-party merchant's
customers never sign in here; they reach one invoice through the public checkout.
The organization is configuration (`PORTAL_ORGANIZATION_ID`), the mode is always
live, and every read is filtered to the signed-in customer — tenant scoping alone
would show each of them all of the others.

| Route | Scope |
|---|---|
| `GET /v1.0/me` | — (identity only; says which shells this person can open) |
| `GET /v1.0/portal/session` · `/portal/subscriptions` · `/portal/subscriptions/:id` | `billing:my-subscriptions:read` |
| `GET /v1.0/portal/invoices` · `/portal/invoices/:id` | `billing:my-invoices:read` |
| `POST /v1.0/portal/invoices/:id/pay` | `billing:my-invoices:write` |
| `POST /v1.0/portal/subscriptions/:id/cancel` | `billing:my-subscriptions:write` |

The `me` scopes are deliberately not the console's: `billing:my-invoices:read`
reads *my* invoices, `billing:invoices:read` reads the organization's, and a
consumer token is never one scope away from a merchant's customer list. Portal
payloads carry no internal status, metadata or audit trail — "Vence em 3 dias",
never `OPEN` — and a test asserts that on the wire.

Portal cancellation is **at period end only**. A consumer cancelling mid-period
is asking for money back, which is a credit note and a separate decision;
converting one into the other silently is how a billing system starts refunding
by accident. Somebody who wants the immediate one talks to a human, and the human
uses the console.

### API surface (v1, checkout)

The public payment page (screen X1) and wallet's notify-back. **No session on any
of them**, which is the point: the person opening a payment link is paying a
bill, and a bill is not the moment to ask somebody to create an account.

| Route | Authenticated by |
|---|---|
| `GET /v1.0/checkout/:token` · `POST /v1.0/checkout/:token/pay` | the signed link token itself |
| `POST /v1.0/internal/webhooks/wallet` | wallet's `X-Wallet-Signature` HMAC |
| `GET /.well-known/oauth-protected-resource` | nothing — it is metadata about what this API accepts (RFC 9728, [ADR 0014](docs/adr/0014-billing-publishes-its-own-scopes.md)) |

The token travels in the **query string** — `{CHECKOUT_BASE_URL}?token=…`. The page is one file in a
static export ([ADR 0013](docs/adr/0013-static-portal-same-origin-api.md)), so there is no route
below `/checkout` for the edge to resolve; a link shaped `/checkout/{token}` is a 404, which is what
every dunning email pointed at until 2026-08-16.

The link token is derived, never stored: `{organization}\|{mode}\|{invoice}` plus
an HMAC over it, so there is no token row, no lookup index and no expiry job, and
rotating `CHECKOUT_LINK_SECRET` invalidates every outstanding link at once. It
stays valid as long as the invoice is payable — deliberately longer than a
*checkout session*, which expires in half an hour, because an invoice due in 30
days needs a link that lives 30 days.

The public payload carries no name, no e-mail, no tax id and no other invoice: a
forwarded link must not become a disclosure, and the way to guarantee that is for
the data never to be in the response.

The webhook is a **wake-up signal, never payment authority**. Its signature says
the call came from wallet; the charge is then re-read from wallet before any
invoice moves. That is wallet's own posture toward its provider, and it does not
get weaker one layer up.

All of these routes are unmounted entirely unless the deployment is fully
configured. A checkout that 404s is a deployment somebody notices; one that fails
after showing a QR code is a customer who thinks they paid. The same predicate
decides whether `checkout_url` appears on an invoice, so the field never points
at a route this deployment does not serve.

**Collection is live-mode only.** ctech-wallet has no sandbox rail for this charge
kind — `OpenCharge` reaches a real PIX provider whatever mode billing is in — and
billing holds one set of wallet credentials, so there is no second client to route
a test call to. `Collector.Pay` refuses a test-mode invoice outright, and no
`payable` flag or `checkout_url` is published for one. Everything before
collection works in test mode; rehearsing a payment would rehearse it with real
money ([ADR 0004](docs/adr/0004-pix-on-invoice-via-wallet.md), second amendment).

### Terms and privacy

The billing terms addendum lives in **ctech-account's legal centre**
(`accounts.aoctech.app/products/billing`), not here. One company, one place a person reads what
they agreed to; a service hosting its own copy is a service whose copy is eventually the stale one.
`ui/src/lib/legal.ts` is the same module `ctech-dfe` and `ctech-wallet` carry.

`billing.CurrentTermsVersion` is what the portal gates on and `Customer.TermsVersion` is what it
compares against — a **version, never a boolean**, because a boolean cannot be re-asked: the day
the terms change, every stored `true` would claim consent to a document nobody read. Bumping the
constant re-gates everybody on their next visit, which is the intended cost of changing the terms.

`GET /v1.0/portal/session` publishes `terms_accepted` — the comparison, not the version — and
`POST /v1.0/portal/terms/accept` records agreement with an audit row. It takes no body: the version
is the server's, and a client that could name one could accept a document it chose.

**The gate stops at the portal.** The public checkout carries the same links in its footer and no
wall: the person there has no session to record an answer against, and a consent screen in front of
a payment refuses money over a document nobody needs to have read in order to owe the bill.

### Telling other services what happened

Every consuming product needs to react to `invoice.paid` and
`subscription.canceled`, and the alternative to telling them is making them poll.

The hard part is not delivery, it is **which endpoint an event belongs to**. CTech
is tenant zero ([ADR 0012](docs/adr/0012-portal-serves-tenant-zero.md)) — one
organization holding every CTech product's subscriptions — so an endpoint
registered per organization would send `ctech-dfe` every invoice `ctech-poker`
issued. Routing is therefore by **product owner**
([ADR 0016](docs/adr/0016-webhook-routing-by-product-owner.md)): a subscription
points at a price, a price at a product, and a product belongs to a service. That
survives the two things which break every other candidate — an operator creating
a subscription by hand in the console, and the M2M client being rotated.

An endpoint with no owner key receives everything, which is what an ordinary
merchant wants and needs no configuration at all.

The event row is written **inside the transaction of the change it describes**,
the same discipline as the audit row and for the same reason. The payload is an
id and a type: the consumer reads the entity back through the API with its own
credential, so a misconfigured URL leaks an id rather than somebody's bill.
Deliveries are signed over `timestamp + "." + body`, at-least-once, and retried
with backoff for about two days before the endpoint is left to reconcile from the
API.

### Chasing an invoice nobody pays

**Dunning here is not charge retry.** On a card rail dunning means trying the
charge again; PIX is a pull, so billing cannot debit anybody and there is nothing
to retry. What it does instead is remind a person, gate the service they are not
paying for, and eventually stop carrying a receivable that is not going to arrive.

One schedule, relative to the due date: a note at **D−3** while the invoice can
still be paid on time, reminders at **D+1, D+3, D+7**, `PAST_DUE` at **D+10** —
which is the signal a consuming product acts on to restrict access — and
`UNCOLLECTIBLE` plus cancellation at **D+30**.

The invoice stays payable throughout, including after escalation: gating a
service and removing the ability to pay for it is self-defeating. Each invoice
stores the step it is on, so re-running a missed day performs each step exactly
once.

### The front end

One Next 16 app in `ui/`, exported to static files. Routes are English, labels are Portuguese.

| Route | Who reaches it |
|---|---|
| `/` | Anybody. The public landing, and the **only** indexed route — every other page is `noindex`, because the thing being protected is one customer's invoices appearing in a search result. |
| `/login` · `/callback` | The OAuth round trip against ctech-account. `/login` is deliberately **not a form**: ctech-account owns every credential in the family and this app never sees one, so a page here that looked like a login is a page that teaches customers to type their password on the wrong domain. |
| `/dashboard` · `/invoices` · `/invoice?id=` · `/subscriptions` | A signed-in CTech customer (P1–P4). The route group is the shell and the auth gate. |
| `/checkout?token=` | Anybody holding a payment link (X1). No session, no navigation — the signed token is the whole credential, and the merchant's name is the first thing on the page, because a payment screen that does not say who is being paid is indistinguishable from phishing. |
| `/maintenance` · `/404` | Whole-page states. A 503 from any request sends the reader here with the path they came from, and the page probes `/v1.0/health` and carries them back. |

Tokens are never stored by this app: the refresh token is ctech-account's `HttpOnly` cookie, which
JS cannot read, and the access token lives in memory and dies with the tab.

There are **no dynamic route segments and there cannot be** — a static export prerenders one file
per route, so a subject arrives in the query string. `ui/README.md` carries this as a rule rather
than a detail, because it is the constraint that quietly reappears with every new screen.

### Deploying

Five workflows in `.github/workflows/`, entered from `deploy.yml` on a push to `main`, `staging` or
`dev`. Stages run in dependency order and a stage whose paths did not change is skipped:

```
Terraform → OAuth scopes → API → Frontend
```

Terraform first because it creates the bucket and the ASG the next two deploy into; scopes before
the API because the API rejects a token carrying a scope ctech-account never learned to issue;
frontend last because it is the only stage a customer sees.

Four IAM roles, one per stage, none trusting a pull request
([ADR 0015](docs/adr/0015-four-deploy-identities.md)). Every check that runs on a PR — the full CI
suite and `terraform validate` — is designed to need no AWS credentials at all.

**The pipeline does not create its own prerequisites.** The OIDC provider, the ACM certificate, the
deployments bucket, ctech-account's publisher credentials, the `billing` OAuth client, the DNS
record and the four SecureStrings belong to other repositories or to out-of-band configuration.
They are listed, with owners, in [`.github/workflows/README.md`](.github/workflows/README.md), and
each fails loudly at the first call rather than degrading quietly.

See [OVERVIEW.md](OVERVIEW.md) for the product spec, [ARCHITECTURE.md](ARCHITECTURE.md) for the
technical design, [`docs/adr/`](docs/adr/) for the decisions and their accepted limits, and
[PLAN.md](PLAN.md) for the phased build plan.

> **Backlog B37 status:** the datastore choice is decided
> ([ADR 0002](docs/adr/0002-datastore-dynamodb.md)); `FIXED_MONTHLY` vs `billing_timing=ADVANCE`
> was never one enum and is resolved as two independent axes (recurrence and timing — see
> `api/internal/domain/billing/cycle.go`). The `ctech-wallet` capability the MVP depended on —
> one field and one scope, not a new subsystem — is specified in
> [`docs/specs/2026-08-15-wallet-invoice-charge.md`](docs/specs/2026-08-15-wallet-invoice-charge.md),
> reasoned in [ADR 0004](docs/adr/0004-pix-on-invoice-via-wallet.md), and now **implemented in both
> repositories**. **Still open:** the credentials and the SSM entry that let the two actually talk.

## Relationship to other CTech services

- **ctech-account** — issues the M2M (client-credentials) tokens that authorize external
  services (e.g. `ctech-dfe`) to create invoices, and the user tokens that authorize a
  customer to view/cancel their own subscriptions.
- **ctech-wallet** — is the source of truth for money movement. Its M2M PIX sale had everything
  billing needed except one thing: the amount came from a fixed Go catalogue, and an invoice total
  is arbitrary because of proration and metered usage. `POST /v1.0/internal/wallet/charge` now takes
  a caller-supplied amount under `internal:wallet:charge-amount`, with a per-client ceiling
  replacing the catalogue as the fraud defense — [spec](docs/specs/2026-08-15-wallet-invoice-charge.md),
  implemented in both repos. Boleto is not in scope.
- **ctech-dfe** — first consumer: will create subscriptions/invoices for DF-e plans, and is
  the natural place to auto-emit the NFS-e (service tax invoice) CTech itself owes on every
  paid `ctech-billing` invoice (see OVERVIEW.md § Suggested Features).
- **ctech-poker** — future consumer, likely only for real-money-mode entry fees / rake
  reporting, if that model is adopted.
