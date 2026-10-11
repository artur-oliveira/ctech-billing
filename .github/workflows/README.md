# CI/CD

Two entry points and four reusable workflows.

| File           | Trigger                                   | What it does                                                                         |
|----------------|-------------------------------------------|--------------------------------------------------------------------------------------|
| `ci.yml`       | every PR, and pushes to deploy branches   | Static analysis → API → UI, chained. **No AWS credentials.**                        |
| `deploy.yml`   | push to `main`/`staging`/`dev`, or manual | Path filter and ordering. Calls the four below.                                      |
| `infra.yml`    | called; **and PRs** for validate-only     | `terraform apply` on both roots.                                                     |
| `api.yml`      | called                                    | Builds six arm64 binaries (`server`, `sweep`, `reconcile`, `deliver`, `dunning`, `finance`), uploads, rolling deploy via SSM. |
| `frontend.yml` | called                                    | Thin caller of `ctech-cdk`'s `frontend-cloudflare.yml@main`: static export, generated `_headers`, deploy to Cloudflare Workers Static Assets. |
| *(scopes)*     | called                                    | `ctech-account/.github/workflows/publish-resource-scopes.yml@main`.                  |

Dependency bumps arrive weekly through [`dependabot.yml`](../dependabot.yml): the Go module, the
UI's npm tree, the actions these workflows pin, and both Terraform roots. Every one of them is
gated by `ci.yml` or `infra.yml`, which is what makes the pull requests quick to read — a bump that
breaks something says so before anybody looks at it.

## CI is a chain too

```
Static analysis → API → UI
```

Same `needs:` shape as the deploy pipeline below, for the same reason: the cheapest check that can
fail should fail first. `gofmt` disagreeing costs seconds; finding out after a DynamoDB container
has started and `npm ci` has resolved the front end costs minutes, on every push of the branch.

The static stage is the one with no services and no build step — gofmt, `go vet`, **staticcheck**
and **govulncheck**, each run twice where it matters: once plain and once under `-tags integration`,
because `go vet ./...` never compiles a build-tagged file and the integration suite is the only
place the payment path runs end to end. Left out, the least-checked code in the repository would be
the code that moves money.

`staticcheck` and `govulncheck` are invoked as `go run …@latest`, matching `ctech-poker`'s
`api.yml`. The trade is real: a new release can turn CI red on a day nobody changed code. That is a
morning's annoyance against running a vulnerability scanner a year out of date.

**`api/go.mod` pins the Go patch version**, not just `go 1.26`. The bare minor is a floor, so
`go-version-file` resolved it to whichever patch the runner offered — and a developer on 1.27
saw a clean `govulncheck` while CI on 1.27 reported five standard-library findings, every one of
them "Fixed in: go1.27". Most govulncheck failures are of exactly this shape: the finding is the
toolchain, and the fix is that one line. `ctech-account` and `ctech-poker`, the siblings that also
run the scanner, pin the same way.

## The deploy order is a dependency chain

```
Terraform → OAuth scopes → API → Frontend
```

- **Terraform first** because it creates the ASG the API deploys onto and writes
  the SSM parameters the later jobs read their targets from. It no longer creates
  anything the frontend needs — the portal is deployed to Cloudflare, not to a
  bucket ([ADR 0020](../../docs/adr/0020-portal-on-cloudflare-workers.md)) — but
  the ordering stands for the API's sake.
- **Scopes before the API** because the API rejects a token carrying a scope
  ctech-account never learned to issue. Publishing afterwards leaves a window in
  which a new route is live and nothing can call it.
- **Frontend last** because it is the only one a customer sees. A portal that
  ships before the API answering it is a portal that renders errors.

A stage whose paths did not change is skipped, and a skipped stage does not
block the ones after it. A stage that *fails* does.

## Branch → environment

`main` → `prod`, `staging` → `stage`, anything else → `dev`. Resolved
identically in every workflow, and by the same `case` the Terraform variables
use.

## Why four roles and not one

`terraform/github` creates `ctech-billing-gha-{infra,api,frontend,scopes}`.
They are four different blast radii: infra runs Terraform and must be able to
change anything, api uploads a zip and restarts a service, frontend wrote
static files, scopes reads three SSM parameters. One role would give the job
that syncs HTML the rights of the job that can destroy the DynamoDB tables.
The `frontend` role is now unused — the portal deploys with a Cloudflare API
token, not an AWS identity — and is removed with the rest of the teardown.

**No deploy role trusts a pull request.** The trust policies name
`repo:artur-oliveira/ctech-billing:ref:refs/heads/<branch>` for exactly three
branches. Everything that runs on a PR — `ci.yml`, and `infra.yml`'s validate
job with `-backend=false` — is designed to need no credentials at all.

### Each branch is named twice, and that is not redundancy

GitHub emits the `sub` claim as `repo:owner/name:...` for older repositories and
as `repo:owner@<ownerId>/name@<repoId>:...` for repositories with immutable IDs
— which is what a repository created, or deleted and recreated, recently gets.
`terraform/github` matches both spellings, with `StringLike` and `@*` covering
the numeric ids only; the owner and repository names stay literal, and a GitHub
account name cannot contain `@`.

Matching only the first spelling is what produces

```
Could not assume role with OIDC: Not authorized to perform sts:AssumeRoleWithWebIdentity
```

twelve times, with nothing in the message naming the claim that failed. Every
sibling repository's `oidc-stack` and `ctech-cdk`'s `githubTrustPrincipal` carry
the same pair.

**Fixing it means applying `terraform/github` from a workstation.** The broken
trust policy is on the role the pipeline would need to assume in order to repair
it, so the pipeline cannot fix itself — which is the same reason the first apply
is a manual step.

## Prerequisites this pipeline does not create

These are other repositories' to provision. Each fails loudly at the first call
rather than silently degrading:

| What                                                                                                                         | Owner                                                | Used by                                      |
|------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------|----------------------------------------------|
| `/ctech/global/oidc/provider-arn`                                                                                            | ctech-cdk                                            | every role's trust policy                    |
| `/ctech/global/acm/cert-arn`                                                                                                 | ctech-cdk                                            | the retired CloudFront distribution          |
| `/ctech/{env}/s3/deployments-bucket`                                                                                         | ctech-cdk                                            | `api.yml`                                    |
| `/ctech-account/{env}/scope-publishers/billing/{client-id,client-secret}`                                                    | ctech-account                                        | the scopes job                               |
| OAuth client `billing` with the four `billing:me:*` scopes and `https://billing*.aoctech.app/callback` as a redirect URI     | ctech-account                                        | the portal's login                           |
| A DNS record pointing `billing[-env].aoctech.app` at the Cloudflare Worker                                                   | Cloudflare, outside Terraform                        | everything                                   |
| The four billing SecureStrings (`wallet-client-id`, `wallet-client-secret`, `wallet-webhook-secret`, `checkout-link-secret`) | set out of band                                      | payments and checkout links                  |
| `/ctech-billing/{env}/billing/field-encryption-key` — 32 bytes, base64 or hex                                                | set out of band, backed up outside SSM               | **every binary refuses to start without it** |
| `/ctech-billing/{env}/billing/email-from`, matching a verified SES identity and `var.email_from`                             | set out of band + SES                                | dunning reminders                            |
| A tenant plan applied with `cmd/seed`                                                                                        | this repo, run by an operator                        | everything — see below                       |
| An entry in `/ctech-wallet/{env}/m2m-clients` **keyed by the OAuth client id** (`ctech-charge`), carrying `webhook_url` and `"max_charge_cents": 1000000` — set 2026-08-16 | ctech-wallet, set out of band | every settlement; any invoice above R$ 1.000,00 |
| The OAuth client `ctech-charge` holding `internal:wallet:charge-amount`, and `wallet-client-id` in SSM set to the same string | ctech-account (exists) + this repo's SSM             | opening any charge at all                    |
| `/ctech-billing/{env}/billing/applications-notify-email` (String) | set out of band | the notice of a new merchant application |
| billing's account client granted `internal:account:company-actor`; ctech-account route `GET /v1.0/internal/organizations/{id}/companies` | ctech-account (granted in prod to `billing-account-membership`; route shipped in ctech-account #56) | the application form's company picker and check |
| OAuth client `billing` granted `billing:organization:write` | ctech-account (appended by the scopes job's publish) | **sign-in itself** once the UI asks for it |

## The pipeline does not seed

There is one manual step after the first deploy, and nothing works before it:

```sh
cd api
FIELD_ENCRYPTION_KEY=… TABLE_PREFIX={env}_billing AWS_REGION=us-east-1 \
WEBHOOK_SECRET_DFE=… \
  go run ./cmd/seed -file tenants/ctech.json -mode test
# then again with -mode live
```

Until it runs there is no organization, no credential and no catalogue, so every M2M call resolves
to a tenant that does not exist.

`api/cmd/seed/run.sh {env}` does both runs in that order, reading `FIELD_ENCRYPTION_KEY` and
`WEBHOOK_SECRET_DFE` from SSM without printing them (operator's AWS profile, `ctech` by default).

**Re-seed after the applications deploy (console self-service part 2).** The plan
gained `prod_billing` and `price_billing_early_access`; approving an application
fails with `resource_not_found` on the price until it exists:

    api/cmd/seed/run.sh prod   # test then live; idempotent

The plan's `owner_user_id` is who reaches the tenant's console. A tenant first seeded without one
(API-only, as `ctech` was in production until 2026-10-10) gets it on the next run, once.

A credential lives in one mode (its client id resolves to one row). Only the **live** run creates a new
one; the test run, which goes first, leaves it — unless the plan marks it `"test": true`.

**Run both modes in every environment, dev included.** The portal resolves its customer with
livemode hardcoded to `true` (`middleware.ResolvePortalIdentity`) — test mode exists so an
integration cannot touch real data, and a consumer does not integrate — so a dev environment seeded
only with `-mode test` has a working M2M API and a portal that 403s every user.

`portal_organization_id` in each `environments/*.tfvars` must equal the plan's `organization.id`,
which is `ctech`. Nothing checks that the two agree: when they disagree the portal answers 403
"nenhuma conta de cobrança para este usuário" to everybody, which reads like missing customer data
rather than a mismatched string. Left empty — the variable's default — every portal route 404s
instead, which is the safer failure and the reason the default is what it is.

It is deliberately not a pipeline stage. Creating a tenant is an admission decision, and a deploy
that quietly provisions one is a deploy that can quietly provision the wrong one. It is
create-or-skip, so re-running it is safe and adding a price to the file creates only the price.

## Adding an index to a live table: subscriptions by customer

`SubscriptionRepository.ListByCustomer` used to read the tenant's whole `SUB#` range and filter it
down to one `customer_id`, following the continuation key to the end of the partition — a read that
grows with the tenant rather than with the customer. It now has a partition of its own:
`lookup-index` on `subscriptions` (hash `lookup_pk`, range `sk`), with
`lookup_pk = {org}#{mode}#CUSTOMER_SUBS#{customer_id}` (`repositories.LookupCustomerSubscriptionsPK`).
Sparse: only the subscription row carries it, never its `#ITEM#` rows.

A new row gets the attribute at `Create`. A row written before the index existed does not, and an
index query cannot see a row that lacks its key — so switching the read in the same deploy that
creates the index would make every existing subscriber disappear (Free on screen and in
entitlements) until somebody backfilled. The change therefore ships as **two pull requests**, in
this order:

1. **Index + write + backfill (read unchanged).** `schema.json` gains the index; Terraform creates
   it and waits for it to be `ACTIVE` before the API stage runs; the API then writes `lookup_pk` on
   every new subscription, while `ListByCustomer` still reads the old way.
2. **Backfill production**, from a workstation (the backfill Scans, and the service role denies
   `dynamodb:Scan`):

   ```sh
   api/cmd/backfill-subscription-lookup/run.sh prod           # dry run: lists what is missing
   api/cmd/backfill-subscription-lookup/run.sh prod -apply    # writes it
   api/cmd/backfill-subscription-lookup/run.sh prod           # must report missing=0, exit 0
   ```

   Both modes and every tenant in one pass. It reads `FIELD_ENCRYPTION_KEY` from SSM without
   printing it, writes only `lookup_pk`, and only where it is absent — so it is safe to re-run, and
   safe to run while the API is creating subscriptions. Exit 1 means something is left (or a row is
   mismatched or has no customer); read the log lines before re-running.
3. **Switch the read** to a key-condition query on `lookup-index` — the second pull request, merged
   only after step 2's final dry run reports `missing=0`. Done: `ListByCustomer` reads only the
   index now, so a subscription row without `lookup_pk` is invisible to it. If one ever appears
   (a restore from a pre-index backup, say), re-run the backfill.

The same sequence applies to any future index whose read must see rows that already exist.

### Invoices by customer

`InvoiceRepository.ListByCustomer` (the portal's invoice list) made **one** Query over the
tenant's whole `INVOICE#` range — lines, attempts, checkouts and credit notes included — with a
`customer_id` filter and the page limit. DynamoDB applies `Limit` before the filter, so a
customer whose invoices were not among the first rows evaluated got an empty or short list. Same
bug class as the subscriptions read above; same fix, same two pull requests.

One difference: an invoice row's `lookup_pk` is already taken — it is the invoice's
subscription partition (`LookupSubscriptionInvoicesPK`, read by `ListBySubscription`), and one
attribute can name only one partition. So the customer partition is a **new attribute on a new
index**: `customer-index` on `invoices` (hash `customer_pk`, range `sk`), with
`customer_pk = {org}#{mode}#CUSTOMER_INVOICES#{customer_id}` (`repositories.LookupCustomerInvoicesPK`).
Sparse: only the invoice row carries it, never its `#LINE#`, `#ATTEMPT#`, checkout or credit-note
rows. Written once at `Create` (an invoice's customer never changes).

```sh
api/cmd/backfill-invoice-lookup/run.sh prod           # dry run: lists what is missing
api/cmd/backfill-invoice-lookup/run.sh prod -apply    # writes customer_pk where absent
api/cmd/backfill-invoice-lookup/run.sh prod           # must report missing=0, exit 0
```

The read moves onto `customer-index` in the second pull request, merged only after the final
production dry run reports `missing=0`. Done: `ListByCustomer` reads only the index now, newest
first, following the continuation key, so an invoice row without `customer_pk` is invisible to it.
If one ever appears (a restore from a pre-index backup, say), re-run the backfill.

## Console access by membership (ADR 0028)

The billing half of the console finds its tenant by `{mode}#ACCOUNT_ORG#{account_organization_id}`
instead of `{mode}#OWNER#{user_id}`. Production rows (tenant zero `ctech`, linked to account
organization `01a04ed6-1af9-745e-bcf7-d3b66fe52321`) still carry the owner key, so:

1. **Deploy part 1 (API and UI together).** New and linked tenants get the new key; the console
   falls back to the owner key **only** for a tenant linked to the selected organization, so tenant
   zero stays reachable by its stored owner. Its other admins see "cobrança não ativada" until step 2.
2. **Backfill production**, from a workstation:

   ```sh
   api/cmd/backfill-organization-lookup/run.sh prod           # dry run: lists what is missing
   api/cmd/backfill-organization-lookup/run.sh prod -apply    # writes it
   api/cmd/backfill-organization-lookup/run.sh prod           # must report missing=0, exit 0
   ```

   Both modes, every tenant, one pass. It reads `FIELD_ENCRYPTION_KEY` from SSM without printing it,
   writes only `lookup_pk`, conditionally on the row being unchanged. Exit 1 means something is
   missing, mismatched or in conflict (two tenants on one organization): read the log lines first.
   `unlinked` lines are tenants that lose console access (reachable only by M2M credentials); none
   is expected in production.
3. **Confirm** tenant zero opens in the console for an admin of the CTech organization who is not
   its `owner_user_id`.
4. **Remove the fallback** (`GetByOwner`, the owner key): the follow-up pull request, merged only
   after step 2's final dry run reports `missing=0`.

## Deploy sequence: console self-service part 2 (applications)

1. ctech-account: the companies list route, and `internal:account:company-actor`
   granted to billing's account client (`createclient -grant`), in this env.
2. Terraform creates the `applications-notify-email` parameter (value
   `SET-OUT-OF-BAND`) and the bootstrap variable; the launch template change
   rolls the ASG (instance refresh). Set the parameter as soon as it exists:
   `load-ssm-env.sh` holds the `aws ssm` calls, not their results, so the API
   reads it at its next start (the API deploy below, or any restart). Until
   then a new application's notice goes nowhere (logged, never a failed
   application).
3. Scopes job: publishes `billing:organization:write`. Publishing appends every
   new public active scope to the first-party public client with the resource
   server's id (ctech-account `EnsureFirstPartyPublicClientScopes`), so the
   OAuth client `billing` holds it before the UI deploys (deploy.yml orders
   scopes → API → frontend). Confirm in ctech-account's
   `{env}_account_oauth_clients` (`CLIENT_billing`, `allowed_scopes`): the UI
   asks for it, and ctech-account fails the whole sign-in on an ungranted scope.
4. API.
5. `api/cmd/seed/run.sh prod` (adds `prod_billing` and the early access price).
6. UI.
7. Smoke: apply from an organization you administer, see test mode, approve it
   in tenant zero (Solicitações), see live; reject a second one and re-apply.

## Collection is configured in the other repository

The charge route itself ships (`ctech-wallet/api/internal/services/charge_amount.go`). What does
not ship with it is billing's entry in `/ctech-wallet/{env}/m2m-clients`, and it carries two
settings that fail in opposite ways:

**The blob is keyed by the OAuth client id, not by the service name.** Wallet reads
`s.m2mClients[claims.AZP]` — the `azp` of the token that opened the charge. Billing's client is
`ctech-charge`, so the key is `ctech-charge`; an entry filed under `billing` is an entry wallet
never finds. It does not error, it returns the zero value, and the zero value is both failures
below at once. `WALLET_CLIENT_ID` in this repo's SSM must be that same string, because it is what
mints the token the `azp` comes from.

- **`webhook_url`.** Without it `dispatchM2MWebhookProduct` logs `no registered webhook for client`
  and marks delivery failed. The customer pays, wallet records it, and billing hears nothing until
  `cmd/reconcile` runs — so the failure looks like an hour of latency rather than a
  misconfiguration, which is exactly why it is worth naming here. The keys are `webhook_url`,
  `hmac_secret` and `max_charge_cents`, exactly as `services.M2MClient` tags them; wallet's
  `2026-07-30` spec shows `WebhookURL` / `HMACSecret`, which do **not** unmarshal — the
  case-insensitive fallback in `encoding/json` does not bridge the underscores, so those keys parse
  to empty and produce precisely the silent failure above.
- **`"max_charge_cents": 1000000`.** Wallet defaults an M2M client to R$ 1.000,00 per charge. The
  DF-e sob-demanda plan bills per document, so a high-volume month passes that on its own — and the
  refusal arrives when the customer tries to pay an invoice that was already issued and numbered.
  This is the [ADR 0004 amendment](../../docs/adr/0004-pix-on-invoice-via-wallet.md).
  `billing.MaxChargeCents` mirrors it so a price can be rejected at creation; the mirror is only
  right while the two agree, and nothing checks that across repositories.

**There is no test-mode rail.** Wallet opens a real PIX charge whatever mode billing is in, so
`Collector.Pay` refuses a test-mode invoice outright (`services.ErrTestModeNotPayable`) and no
`checkout_url` is published for one. Integrators exercise the catalogue, subscriptions, usage and
invoicing in test mode; collection is live-only until wallet has a sandbox charge kind.

## Lifting a merchant suspension

A live tenant whose `prod_billing` subscription in tenant zero is cancelled is
suspended: its M2M writes answer `403 tenant_suspended`, and `sweep` and
`dunning` skip it (spec § 4.3). Both jobs read one day's queue, so the days
skipped stay where they were. When a new entitled subscription lifts the
suspension, re-run each suspended day, oldest first; both are idempotent:

    # on an instance, as the job user, once per day D of the suspension
    /opt/app/job.sh sweep -date D
    /opt/app/job.sh dunning -date D

`reconcile` and `deliver` are never skipped: they record money that arrived and
tell integrations about it.
