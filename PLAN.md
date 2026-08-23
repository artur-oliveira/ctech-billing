# ctech-billing — Development Plan

> A phased roadmap, not a bite-sized task list. Each phase ends with something demoable before
> the next begins. Checked items are implemented and covered by tests; unchecked ones are not.

## Phase 0 — Foundations (infra + skeleton)
- [x] Datastore decision recorded in an ADR before any persistence code —
      [`docs/adr/0002`](docs/adr/0002-datastore-dynamodb.md), with the key layout and the accepted
      limit. All 9 decisions are in [`docs/adr/`](docs/adr/).
- [x] Go module skeleton (`api/`, `internal/`) and CI running gofmt, `go vet`, the race-enabled
      test suite and `terraform fmt`/`validate` on every PR.
- [x] Infrastructure as **Terraform**, not CDK ([ADR 0010](docs/adr/0010-infrastructure-as-terraform.md)) —
      `terraform/billing/` owns the tables, their indexes, TTL, the service role and the SSM
      parameters. `terraform validate` passes. The schema is not written in the `.tf` at all: it is
      `for_each` over `api/internal/repositories/schema.json`, the same file the Go service embeds,
      so there is nothing to keep in step and no drift test to keep honest.
- [x] Compute: security group, launch template, ASG, the HAProxy route, and the four schedules —
      `cmd/sweep` daily, `cmd/dunning` daily an hour later, `cmd/reconcile` hourly, `cmd/deliver`
      every minute. The `ctech-cdk` userdata conventions are ported into
      `terraform/assets/bootstrap.sh.tftpl` as a shell script rather than a string array, which is
      the whole point of doing it in Terraform. The route is registered from this root and not from
      `ctech-lbalancer`: the edge reads every parameter under its routes prefix regardless of who
      wrote it, and a route living in another repository outlives the thing it points at.
      The jobs are **systemd timers on the leader instance**, not EventBridge — each is a one-shot
      process needing the service's own config and role, so the alternatives were an HTTP route
      (which ADR 0002 forbids for exactly these cross-tenant paths) or an SSM fan-out that runs
      the job once per instance anyway. See [`terraform/README.md`](terraform/README.md).
- [x] **The deploy path.** Four GitHub OIDC roles in `terraform/github/` and five workflows in
      `.github/workflows/`. `deploy.sh` was never missing — `terraform/assets/bootstrap.sh.tftpl`
      writes it to `/opt/app/deploy.sh` at boot; what was missing was a principal allowed to call
      it, and `api.yml` now does through SSM RunCommand. The roles are split by stage and none of
      them trusts a pull request ([ADR 0015](docs/adr/0015-four-deploy-identities.md)); every check
      that runs on a PR needs no AWS credentials at all.
- [x] `api/cmd/server` and `Dockerfile` — arrived with the API in Phase 2, not before, so the
      binary served something from its first commit.
- [x] **`billing:*` scopes — published by this repository, not registered by hand in
      `ctech-account`.** The premise of the original item was wrong: resource servers self-publish
      through CI/CD, as `ctech-dfe` already does. `api/internal/oauthresource/scope-manifest.json`
      holds the 14 entries, `deploy.yml` calls ctech-account's reusable
      `publish-resource-scopes.yml@main` before the API stage, and a test asserts the manifest and
      `middleware.AllScopes` are the same set ([ADR 0014](docs/adr/0014-billing-publishes-its-own-scopes.md)).
      The service also serves RFC 9728 metadata at `/.well-known/oauth-protected-resource`.
- [x] **Tenant provisioning** (`internal/provision`, `cmd/seed`, `api/tenants/ctech.json`). Nothing
      else could create the first row: every write path resolves its tenant from a credential
      (ADR 0003), and a credential is itself a row, so a fresh deployment had no way in. A plan is
      a reviewable JSON file rather than a sequence of flags — a tenant has a shape, and the shape
      belongs in a pull request rather than in shell history. Create-or-skip on every row, so
      re-running is a no-op and adding a price creates only the price.
- [ ] **Configuration in other repositories** — the only thing between this service and a real
      payment, and none of it is code here:
    - The M2M client `ctech-billing` itself needs, holding `internal:wallet:charge-amount` — the
      scope exists and is enforced in wallet (`ctech-wallet/api/internal/middleware/scope.go`).
    - The entry in wallet's SSM `m2m-clients` blob (`webhook_url`, `hmac_secret`, optionally
      `max_charge_cents`), without which billing's notify-back is never delivered and
      reconciliation does all the work.
    - The `billing` OAuth client at ctech-account, with the four `billing:me:*` scopes and
      `https://billing*.aoctech.app/callback` as a redirect URI, plus the scope-publisher
      credentials the pipeline reads.
    - The rest of the table in [`.github/workflows/README.md`](.github/workflows/README.md), each
      row with an owner.

## Phase 1 — Domain core (no external integrations)
- [x] Brazilian holiday calculator as a pure, unit-tested function — `internal/domain/brcal`.
      Easter is computed (Meeus/Jones/Butcher) and pinned against a known-date table; 20 November
      carries a year condition because it only became national in 2024.
- [x] Civil `Date` type with end-of-month-clamping arithmetic. The clamp is what stops a
      subscription anchored on the 31st from skipping February and billing twice in March.
- [x] Money in centavos with **one** rounding policy (`MulDiv`, half away from zero).
- [x] Pro-rata calculator, pure and table-tested — including the property that the used and
      remaining halves always sum back to the full price, which independent rounding breaks.
- [x] Billing cycles and due-date computation with roll-forward, combining both of the above.
- [x] `Product`/`Price` (immutable), `Subscription`, `Invoice` + lines, `PaymentAttempt`,
      `CheckoutSession`, `CreditNote`, `UsageRecord`, `Customer`, `Organization` (payout gate),
      `AuditLog`, and `metadata` — as pure types with their validation.
- [x] State machines as one `Transition` function per entity, with causes: an operator cannot mark
      a payment succeeded, and an UNCOLLECTIBLE invoice only reaches PAID through a reconciled
      payment.
- [x] Storage layer over DynamoDB, on `ctech-go-common`'s shared primitives rather than a second
      implementation of them. One table per entity, with each child in its parent's table and
      partition; three index shapes across the tables that need them; TTL written on every create.
- [x] **Audit written inside the transaction of the change it records** — a status cannot change
      without leaving a record of who changed it, and a rejected transition writes nothing.
- [x] Gapless per-organization, per-year invoice numbering under concurrency.
- [x] Integration tests against a real DynamoDB (`make test-integration`), covering tenant/mode
      isolation, numbering, transactional audit, optimistic concurrency, sweep-index sparseness,
      usage idempotency and TTL.
- [x] No HTTP API yet — the domain logic is proven in isolation first.

## Phase 2 — M2M + user-facing API
- [x] M2M endpoints: create customer, create/cancel subscription, report usage, read invoices,
      query entitlements — authenticated via `ctech-account` client-credentials and scoped per
      resource **and direction** (`billing:invoices:read` is not `billing:invoices:write`).
- [x] The tenant comes from the credential, never from the request: there is no
      `organization_id` parameter on any route, and the credential also decides test vs live.
- [x] Idempotency-key enforcement middleware (OVERVIEW.md § 9.4) — applied once, at the HTTP
      layer, to every mutating route, keyed on the body hash so a reused key with a different
      body is a 409 rather than a wrong replay.
- [x] Request-id correlation on every response.
- [x] Contract tests against a real signed-token issuer (an httptest JWKS server), not a
      bypassed verifier.
- [x] Console read API (`/v1.0/console/*`) — the backend the console screens C1–C9 and C17 need:
      session, invoice/subscription/customer/product listings and details, each detail carrying its
      audit timeline. The tenant comes from the signed-in **owner** and the mode from a required
      header, which is a deliberate, argued departure from ADR 0003's second half
      ([ADR 0011](docs/adr/0011-console-session.md)). A service token is rejected on every console
      route and a session token on every M2M route — both directions are tested.
- [ ] Console **writes** (finalize, void, credit note, new price). Held back on purpose: a second
      write path to the same entities is a second place for the audit cause to be wrong, so each
      arrives with the screen that needs it. Cancellation is the first one and has landed, below.
- [x] Portal read API (`/v1.0/portal/*`) plus `GET /v1.0/me` — the consumer surface for **CTech's own
      customers**, tenant zero ([ADR 0012](docs/adr/0012-portal-serves-tenant-zero.md)). Every read
      is filtered to the signed-in customer, not merely to the tenant, because in the portal every
      user shares one. Payloads carry no internal status, metadata or audit trail, and a test
      asserts that on the wire. A third-party merchant's customers are out of scope by decision:
      they reach one invoice through the public checkout.
- [x] **Subscription cancellation, on both surfaces.** Immediate and at-period-end are two
      different operations, not a checkbox: one stops entitlement now, the other keeps it until
      the period the customer already paid for runs out.
    - [x] Console: `POST /v1.0/console/subscriptions/:id/cancel`, both modes, actor is the signed-in
          owner (`user:<sub>`, never `client:<id>` — `actorOfUser`). A test asserts the operator's
          own id lands in the audit row, because that is the whole reason this route exists rather
          than pointing the console at the M2M one.
    - [x] Portal: `POST /v1.0/portal/subscriptions/:id/cancel`, scope
          `billing:my-subscriptions:write`, filtered to the caller's own subscription and
          **at-period-end only** — enforced in the handler, not left to a request field.
    - [x] Both: repeating a cancellation is not an error and writes no second audit row.
    - [x] **Defect found and fixed on the way:** `ScheduleCancellation` hard-coded
          `CauseScheduleCancel` and silently discarded the cause its caller passed, so an
          operator's scheduled cancellation and a customer's own were indistinguishable in the
          audit trail. The cause is now threaded through, and the domain's edge accepts all three
          (`subscription.go`) — because "why is this ending" genuinely has three answers.
- [x] UI foundation + portal P1–P3. Next 16 app in `ui/`, two route shells in one app, on
      `@aoctech/ui` — a new sibling repo (`ctech-ui`) holding the seven primitives these screens
      need, themed entirely by the consuming app's CSS variables so dfe's green and billing's
      sienna are the same components. [`ui/DESIGN.md`](ui/DESIGN.md) records the token system and
      what was rejected. Consumed by `file:` link until the package is published.
    - [x] P1 Início, P2 Faturas, P3 Fatura, plus P4 Assinaturas with at-period-end cancellation.
    - [x] Mock mode (`npm run dev:mock`), following `ctech-poker`'s pattern: an axios adapter, so
          the mock replaces the transport and every screen, hook and query key above it is the same
          code that talks to the real API. Ten named scenarios, most of which cannot be produced
          against a real backend — an overdue invoice needs a past due date, an expired PIX needs
          thirty minutes. Aliased out of the production bundle by `next.config.ts`, and verified
          absent from `.next/` rather than assumed.
    - [x] **Colour reversed on the evidence.** The palette shipped as terracotta
          `oklch(0.470 0.145 36)` and failed the first real screenshot of an overdue invoice: at
          that chroma it is a red, and beside the `urgent` badge the page read as an error rather
          than a bill. Now `oklch(0.440 0.095 45)`, deep sienna. Dropping the chroma — not the hue
          — is what makes `danger` the only saturated colour on any screen.
    - [x] **Sign-in, on `@aoctech/auth-client`.** `/login`, `/callback`, the provider, and the four
          `billing:me:*` scopes. `/login` is not a form on purpose: ctech-account owns every
          credential in the family and this app never sees one. No token is stored — the refresh
          token is ctech-account's `HttpOnly` cookie and the access token lives in memory. A 401
          buys one silent refresh and one retry before the reader is sent to sign in again.
    - [x] **The public landing** (`/`), matching what Wallet and Account already show a stranger,
          and the only indexed route in the app. Everything behind it is `noindex`, twice — the
          root layout and `robots.txt` fail differently, and what is being protected is one
          customer's invoices in a search result.
    - [x] **X1, the payment link page** (`/checkout?token=`), against the checkout API that already
          existed. No session and no navigation; the merchant's name is the first thing on the page.
          Two bugs here were findable only in a browser: the 4-second poll was overwriting the
          opened charge in the query cache and wiping the QR code from under somebody scanning it,
          and the maintenance probe resolved `undefined`, which TanStack Query treats as an error,
          so the page never let anybody off it.
    - [x] **Static export, and the rule that follows from it.** A production build is
          `output: "export"` ([ADR 0013](docs/adr/0013-static-portal-same-origin-api.md)), so there
          are no dynamic route segments: an invoice is `/invoice?id=…`. The export is served by
          Cloudflare Workers Static Assets ([ADR 0020](docs/adr/0020-portal-on-cloudflare-workers.md))
          and `NEXT_PUBLIC_API_URL` names the API host, so the browser calls it cross-origin and CORS
          applies.
    - [x] **Metadata and titles.** A `layout.tsx` per route for the static ones; `useDocumentTitle`
          for the invoice number, which is not knowable at build time because one file serves every
          invoice. Open Graph everywhere despite `noindex` — preview scrapers ignore robots, and
          that is what makes a payment link unfurl in WhatsApp, the channel it actually travels
          through.
- [x] **Recent invoices on the subscription detail.** One attribute on the invoice row, reusing the
      existing sparse `lookup-index`, whose sort key is already `INVOICE#<ULID>` — so "the N most
      recent invoices of this subscription" is the ordering the table already has. A fourth GSI
      would have meant a backfill on a live table and a second copy of every invoice row to answer
      what one screen asks. Returned only from `GET /v1.0/portal/subscriptions/:id`.
      **Caveat:** invoices written before this change carry no `lookup_pk` and will not appear.
      Harmless today because no environment holds invoices; a backfill before one does.
- [ ] UI: the console (C1–C9, C17). Same foundation, `data-density="compact"` on its shell.
      Deferred by decision, twice: the portal is what stands between a customer and paying, and an
      operator can already get everything the console reads from the API. It is also not worth
      opening before the console **writes** above exist, because a read-only console answers
      "what happened" and never "fix it".

## Phase 3 — Invoice generation + payment
- [x] Invoice-generation sweep (ARCHITECTURE.md § 5), idempotent by construction and proven by a
      test that runs it three times and asserts one invoice. Built early because it needs nothing
      from wallet: it finds due subscriptions, aggregates metered usage for the closed period,
      builds the lines, and finalizes with a business-day-adjusted due date. One broken
      subscription is counted and reported, never allowed to stop the run.
- [x] `api/cmd/sweep` — the sweep's entry point, as a one-shot binary rather than a route, so the
      one cross-tenant read path never gets an HTTP surface. Sweeps live and test on the same run,
      fails the job only on live errors, and takes `-date` so a missed day is re-runnable.
      Scheduled by a systemd timer on the leader instance at 07:10 UTC — 04:10 in São Paulo, the
      civil day it bills. `Persistent=true`, so an instance replaced overnight runs the missed sweep
      on boot instead of skipping a day of invoices.

The order here was deliberate: **CTech's own customers pay first, third-party merchants after the
gate.** Both sides of the payment path are now built — billing's, and wallet's.

- [x] **The wallet contract, implemented in `ctech-wallet`.**
  [`docs/specs/2026-08-15-wallet-invoice-charge.md`](docs/specs/2026-08-15-wallet-invoice-charge.md)
  was written against wallet's real source, and the implementation reuses it rather than copying it:
  `POST /v1.0/internal/wallet/charge` takes a caller-supplied `amount_cents` under the **new,
  billing-only** `internal:wallet:charge-amount`, and everything downstream — the deterministic
  txid, the idempotent reservation, `/pix/confirm-product-purchase`, the pending sweep, the HMAC
  notify-back, the refund — is the machinery that was already in production.

  The catalogue was a fraud defense, so removing it needed a replacement rather than a deletion:
  a **server-side per-client ceiling** (`M2MClient.MaxChargeCents`, wallet's default R$ 1.000,00,
  billing's client raised to R$ 10.000,00 by the ADR 0004 amendment), which refuses rather than
  truncates — a silently reduced charge is a paid invoice that is still short.
  It is a new scope and not a wider `internal:wallet:product-purchase` because accepting an amount
  field under the existing one would let every catalogue client, ctech-poker included, name its own
  price the day the field landed.

  The request hash binds the idempotency key to **the amount from the request**, which is the hole
  the catalogue used to close: without it, replaying one key with a bigger number returns the
  original charge and reads as success.

  The configuration that was left is done (2026-08-16): the `ctech-charge` client holds the new
  scope, and wallet's `m2m-clients` blob has an entry **keyed by that client id** carrying
  `"max_charge_cents": 1000000`. Absent, wallet applies its R$ 1.000,00 default and a sob-demanda
  invoice above it is issued and then cannot be paid — and a misfiled key produces exactly that
  default, silently.
- [x] `PaymentAttempt` and `CheckoutSession` **repositories** (`repositories/payments.go`). Both
  nest under the invoice partition, so neither needs an index; the attempt is written create-only,
  which is the concurrency control and is why no counter is needed. The session stores the PIX
  string because wallet returns it once — without that, a customer who reloads loses the only
  thing on the page. Session expiry is **derived on read**, never written by a sweep: a job that
  writes EXPIRED races the confirmation, and losing that race kills a paid invoice's session.
- [x] Wallet charge client (`internal/wallet`) + `POST /v1.0/internal/webhooks/wallet`. The webhook
  verifies its signature over the **raw bytes** and then re-reads the charge before touching an
  invoice. It is a wake-up signal, never payment authority. A settled amount that is not the
  amount billing opened the charge for raises an alarm and refuses — a short payment marked PAID
  is worse than one left open, because the second is visible on a screen somebody reads.
- [x] **X1 — the hosted checkout**, in both halves: `POST /v1.0/portal/invoices/:id/pay` for a
  signed-in customer, and `GET|POST /v1.0/checkout/:token` for a payment link with no session at
  all. Both go through one `Collector`, so they share the charge, the idempotency key and the
  audit trail. Paying twice returns the same QR code and opens one charge, which is the property
  that actually matters and the one the tests pin.
- [x] **Payment links** (`services/paylink.go`). Derived and HMAC-signed, not stored: no token
  row, no index, no expiry job, and rotating the secret revokes every outstanding link. Valid as
  long as the invoice is payable — deliberately a different lifetime from the 30-minute checkout
  session, because an invoice due in 30 days needs a link that lives 30 days. The public payload
  carries no name, e-mail, tax id or other invoice, asserted on the wire.
- [x] End-to-end test: open invoice → charge opened → QR code → webhook → invoice `PAID`, with the
  audit naming `service:ctech-wallet`. Plus the refusals: a lying webhook, a forged signature, a
  short payment, a gated organization, and a payer with no CTech account. The fake wallet
  implements the spec, so these tests fail the day the real route disagrees with what billing was
  built against.
- [x] **A paid plan starts INCOMPLETE** (`Subscriber.Subscribe`). The interim behaviour Phase 1
  shipped — ACTIVE on create, service granted before the first invoice is paid — was deferred on
  the argument that a state with no way out is worse. Both halves of the way out now exist, so
  it is gone. Free plans (`unit_amount` 0) and arrears plans still start ACTIVE, and for
  different reasons: the first period costs nothing, or has not been served yet.

  The status is decided **before** the row is written, from the prices, rather than corrected
  after the invoice exists — a subscription that is ACTIVE for even an instant is one an
  entitlement check can see. That means the same arithmetic as `billing.FixedLine` in a second
  place, and what keeps the two honest is a test rather than a comment:
  `TestSubscribingToAPaidPlanStartsIncomplete` asserts the status Subscribe chose against the
  invoice Subscribe produced.

  Two consequences fell out, both of them real questions this change forced rather than scope:
    - **Dunning no longer escalates a subscription that never activated.** The reminders still
      go out — the bill is owed and they are what gets it paid — but D+10 does nothing, because
      there is no service to restrict, and the end of the policy cancels under
      `CauseActivationExpired`. Before this it hard-failed on `ErrCauseNotAllowed` and left the
      invoice OPEN forever. **This removes the need for a separate activation-expiry job**: one
      policy already owns the whole life of an unpaid first invoice.
    - **Cancelling an INCOMPLETE subscription at period end ends it now.** There is no paid
      period to protect, and the domain has no self-edge to schedule against.
- [x] **A payment restores the service it pays for** (`Collector.activateSubscription`).
  Found while planning ctech-dfe's integration, and it was live: `CauseInvoicePaid` appeared
  nowhere outside the domain package, so `invoice.paid` moved the invoice and nothing else.
  Dunning could take a subscription to `PAST_DUE` on D+10 and there was **no edge back** — a
  customer who paid on D+12 kept their service restricted by a bill they had settled. The
  transitions existed (`PastDue → Active` as `subscription.recovered`, `Incomplete → Active` as
  `subscription.activated`); only the caller was missing.

  Three properties, each of which is why it sits in `settleInvoice` rather than beside it:
  it runs on the **repeat** path as well as the fresh one, because a retry after a
  half-succeeded write has to finish the steps that did not happen; it is a **no-op on an
  already-ACTIVE** subscription, checked here rather than left to the domain to refuse,
  because `ACTIVE → ACTIVE` is legal under other causes and an `ErrInvalidTransition` logged on
  every renewal is a log nobody reads; and it **never fails the settlement**, because the money
  arrived and there is no "unpay" to retry into.

  The cause is `CauseInvoicePaid` and not the cause that settled the charge: the webhook and the
  reconciler are two ways of learning one fact, and what moved the subscription is the payment.
  The actor still names the messenger, which is where that distinction belongs.

  `subs` is a required argument to `NewCollector` rather than an optional setter like the
  settlement bus — a nil bus degrades a screen, a nil subscription repository silently
  reintroduces exactly this bug, so it should be a compile error.
- [x] **Reconciliation** (`services/reconciling.go`, `api/cmd/reconcile`) for charges whose webhook
  never arrived. It never asks "did the webhook arrive?" — it asks wallet what happened, which is
  the only question with an authoritative answer, and settles through the same `Confirm` the webhook
  uses so a late notification finds the work done rather than done twice.

  Four outcomes, and keeping them apart is the point: **settled** (the customer paid and the
  notification was lost — the reason the job exists), **waiting** (still inside its window; failing
  it here would kill a live QR code), **FAILED** (expired unpaid — an ordinary customer decision,
  invoice still owed), and **ABANDONED** (wallet does not know the charge — billing's own bug, and
  the only one worth an alarm). Reporting the third as the fourth is how a real alarm gets ignored.

  A PENDING attempt carries schedule keys and loses them on any terminal transition, so the sweep
  partition is the work outstanding rather than the day's history. `abandonAfter` is a deliberate
  24h: a charge nobody paid costs nothing by staying PENDING for a day, and a charge somebody paid
  costs a customer their money.

  On the wallet side, the same pass closed a pre-existing gap — `RetryFailedM2MWebhooks` only swept
  the sandbox table, so a failed notify-back for a product sale (and now a charge) was recorded and
  never retried.
- **Gated, not blocked-by-unknowns: the third-party merchant's checkout.** Two separate reasons,
  and the wallet contract above removes neither. The money would have to land in a merchant
  sub-account, which is [ADR 0005](docs/adr/0005-payout-gate.md)'s legal-opinion-and-KYC gate; and
  the payer has no CTech account, while wallet's purchase path is keyed on `user_id` end to end
  (reservation, ownership, history, refund). Building it needs a decision on both, not more code.

  Both refusals are **implemented and tested**, not merely documented: an organization that is not
  `payout_status=enabled` gets a 409 with no wallet call at all, and a customer with no
  ctech-account subject gets `ErrNoPayerAccount` before the call rather than an invented user id
  that would file a stranger's purchase in somebody's wallet history.

## Phase 4 — Dunning, audit, observability
- [x] Shared error observability via `ctech-go-common`: correlated Request ID, one structured boundary log for every
      RFC 7807 response, and non-serialized internal causes.
- [x] **Dunning** (`internal/domain/billing/dunning.go`, `services/dunning.go`, `cmd/dunning`).
      The premise in OVERVIEW.md § 9.2 needed correcting first: "retry policy" is a card-rail
      idea, and PIX is a pull — billing cannot debit anybody, so there is no charge to retry.
      What the policy actually is: a reminder at D−3 while the bill can still be paid on time,
      reminders at D+1/D+3/D+7, `PAST_DUE` at D+10 (the signal a consuming product acts on),
      and `UNCOLLECTIBLE` plus cancellation at D+30.
    - [x] The invoice stays **payable throughout**, escalation included. Gating a service and
          taking away the ability to pay for it is self-defeating.
    - [x] Each invoice stores the step it is on, so `-date` re-runs a missed day and performs
          each step exactly once. The write is conditional on that step, so two instances
          cannot both send the same reminder.
    - [x] Reminders go out over SES (`internal/email`). Flagged: this is the **third** SES
          client in the company and belongs in `ctech-go-common` — noted in the package rather
          than fixed there, because moving it changes two other repositories.
    - [x] `cmd/dunning` refuses to start without `EMAIL_FROM` or `CHECKOUT_LINK_SECRET`.
          Running the escalation half of the policy without the reminders that precede it is
          how a customer who was never contacted loses access.
    - [ ] **Per-plan dunning.** One policy today, deliberately: a configurable schedule needs a
          place to configure it, a migration, and a console screen, and none of that changes
          the outcome for the only tenant that exists.
- ~~Append-only audit log for state transitions~~ — **moved to Phase 1**: audit cannot be applied
  retroactively, and history that was not recorded cannot be reconstructed. The record type and
  the transition causes that feed it already exist.
- [x] **Outbound webhooks** ([ADR 0016](docs/adr/0016-webhook-routing-by-product-owner.md)).
      The routing question was the whole problem: tenant zero is one organization holding every
      CTech service's subscriptions, so a per-organization endpoint sends dfe every invoice
      poker issued. Routed by **product owner** instead, which survives an operator creating a
      subscription in the console — the case that kills routing by the calling credential.
    - [x] `WebhookEndpoint` as its own entity, not fields on `APICredential`. A credential is a
          reference to a client in ctech-account and stores nothing about it; rotating the
          client would have taken the endpoint with it.
    - [x] The event row is written **inside the transaction of the change it describes**, in
          `CommitStatusChange` — so no call site can forget one, exactly as with the audit row.
    - [x] `cmd/deliver` on a one-minute timer: fan-out, then one signed attempt per
          (event, endpoint), exponential backoff, eight attempts, endpoints that fail twelve
          times in a row disable themselves.
    - [x] The payload is an id and a type. A consumer reads the entity back with its own
          credential, so a misconfigured URL leaks an id rather than a customer's bill.
    - [x] Proven by two CTech services in one tenant receiving only their own events.
- [x] **Field-level encryption** ([ADR 0017](docs/adr/0017-field-level-encryption.md)). Not on
      the original plan, and it should have been: `Customer.TaxID` carried a comment saying it
      was "encrypted at rest by the repository layer" and nothing encrypted it. Any role with
      `dynamodb:Query` read every CPF in plaintext. Now AES-256-GCM through `internal/crypto`,
      with no development fallback key — a constant key in the repository is a published key —
      and an integration test that reads the raw DynamoDB item rather than trusting a
      round trip.
- Metrics + alarms on scheduler health and charge success rate.

## Phase 5 — First real integration (ctech-dfe)
- Wire `ctech-dfe`'s two example plans (DF-e Basic fixed, DF-e Sob Demanda metered) end to end
  in a staging environment.
- Wire the suggested `invoice.paid → NFS-e emission` call to `ctech-dfe` (OVERVIEW.md § 9.1) —
  this is the feature most likely to surface real business-requirements gaps (tax rules,
  service codes), so do it against a real consumer, not synthetically.

## What is actually left (as of 2026-08-16)

The MVP is built and unreleased. In the order it blocks things:

1. **The first deploy.** Never run. `terraform/github` has to be applied once from a workstation
   (`export AWS_PROFILE=ctech` — neither root names a profile in source, see
   [`terraform/README.md`](terraform/README.md)) before any workflow has an identity to assume, and
   the whole pipeline is untested against real AWS — it is verified by `terraform validate`, `fmt`,
   and reading, which is not the same thing. Re-applying that root is also what picks up a trust
   policy fix, since the pipeline cannot repair the role it needs in order to run.
2. **The `email-from` SSM parameter**, set to the same address as `var.email_from`
   (`billing@aoctech.app`). Verifying the *domain* in SES is necessary and not sufficient: the
   role's policy pins `ses:FromAddress` to the Terraform variable on purpose, so that dunning
   cannot send as ctech-account's address. The two are copies of one fact, nothing checks they
   agree, and a mismatch is discovered when the first reminder is refused at send time rather than
   at deploy time.
3. **Console writes, then the console** (Phase 2). In that order, for the reason each item gives.

Cleared on 2026-08-16, recorded so the cost of re-deriving them is not paid twice:

- ~~**Billing's entry in `/ctech-wallet/{env}/m2m-clients`**~~ — set. Two naming traps, both of
  which fail by returning a zero value rather than an error. The **blob key is the OAuth client
  id** (`ctech-charge`), because wallet looks up `s.m2mClients[claims.AZP]`; an entry under
  `billing` is never found. And the **field names are `webhook_url` / `hmac_secret` /
  `max_charge_cents`**, matching `services.M2MClient`'s struct tags — wallet's own older spec
  document shows `WebhookURL` / `HMACSecret`, which do not unmarshal, because Go's
  case-insensitive fallback does not bridge underscores.
- ~~**The OAuth client holding `internal:wallet:charge-amount`**~~ — exists as `ctech-charge`.
  `wallet-client-id` in this repo's SSM has to be that same string.
- ~~**`@aoctech/ui` unpublished**~~ — published at `0.1.1`; `ui/` builds from the registry and the
  Turbopack root is narrowed back to `ui/`.
- ~~**`infra.yml` requested `pull-requests: write`**~~ — removed. No step used it, and a called
  workflow asking for more than its caller grants is rejected outright, so the unused permission
  broke every deploy.

Not blockers, worth naming so they are not rediscovered as surprises:

- **The seed is the first thing to run after a deploy**, not an afterthought. Until
  `cmd/seed` has applied a tenant plan there is no organization, no credential and no catalogue,
  so every M2M call resolves to nothing. `PORTAL_ORGANIZATION_ID` must name the organization the
  plan creates.
- **The rendered userdata is at ~12 KB of a 16 KiB ceiling** after four timers. It still fits and
  the margin is smaller than it was; the next substantial addition belongs in an S3 asset the
  bootstrap downloads.
- **The field encryption key cannot be rotated yet.** Values carry a `v1.` marker so a second key
  can be added, but nothing reads one. Losing the key makes every stored tax id unreadable, so it
  belongs wherever the account's break-glass material lives.
- **No environment holds invoices**, which is what makes the `lookup_pk` caveat above harmless and
  is the last moment it will be.

## Explicitly deferred (post-MVP, do not build now)
- Invoice PDF generation/storage.
- Base-fee-plus-overage hybrid plan type.
- Multi-currency.
- Self-serve plan upgrade/downgrade UI.

## Open decisions — resolved
1. ~~Wallet charge lifecycle~~ — resolved as scope
   ([ADR 0004](docs/adr/0004-pix-on-invoice-via-wallet.md)), written as a contract
   ([spec](docs/specs/2026-08-15-wallet-invoice-charge.md)), and **implemented on both sides as of
   2026-08-16**: `ctech-wallet/api/internal/services/charge_amount.go` behind
   `internal:wallet:charge-amount`, consumed by `api/internal/wallet/client.go`. Field names and
   the `confirmed` status were checked against wallet's shipped handler, not against its docs.
   What is left is configuration, not code — see item 1 below.
2. ~~Roll-forward vs roll-backward~~ — roll-forward
   ([ADR 0006](docs/adr/0006-due-date-roll-forward.md)), implemented and tested.
3. ~~Datastore~~ — DynamoDB ([ADR 0002](docs/adr/0002-datastore-dynamodb.md)).

Scheduled work that has an owner rather than a blocker: the legal opinion and the end-to-end KYC /
Asaas sub-account test that together gate custody
([ADR 0005](docs/adr/0005-payout-gate.md)) — needed before phase "merchant actually receives
money", not before anything else.
