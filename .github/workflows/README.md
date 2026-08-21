# CI/CD

Two entry points and four reusable workflows.

| File           | Trigger                                   | What it does                                                                         |
|----------------|-------------------------------------------|--------------------------------------------------------------------------------------|
| `ci.yml`       | every PR, and pushes to deploy branches   | Static analysis → API → UI, chained. **No AWS credentials.**                        |
| `deploy.yml`   | push to `main`/`staging`/`dev`, or manual | Path filter and ordering. Calls the four below.                                      |
| `infra.yml`    | called; **and PRs** for validate-only     | `terraform apply` on both roots.                                                     |
| `api.yml`      | called                                    | Builds three arm64 binaries, uploads, rolling deploy via SSM.                        |
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
`go-version-file` resolved it to whichever patch the runner offered — and a developer on 1.26.6
saw a clean `govulncheck` while CI on 1.26.5 reported five standard-library findings, every one of
them "Fixed in: go1.26.6". Most govulncheck failures are of exactly this shape: the finding is the
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
