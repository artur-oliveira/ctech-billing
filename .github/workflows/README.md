# CI/CD

Two entry points and four reusable workflows.

| File           | Trigger                                   | What it does                                                                         |
|----------------|-------------------------------------------|--------------------------------------------------------------------------------------|
| `ci.yml`       | every PR, and pushes to deploy branches   | gofmt, vet, Go unit + integration tests, UI lint/test/build. **No AWS credentials.** |
| `deploy.yml`   | push to `main`/`staging`/`dev`, or manual | Path filter and ordering. Calls the four below.                                      |
| `infra.yml`    | called; **and PRs** for validate-only     | `terraform apply` on both roots.                                                     |
| `api.yml`      | called                                    | Builds three arm64 binaries, uploads, rolling deploy via SSM.                        |
| `frontend.yml` | called                                    | Static export, S3 sync, route manifest, invalidation.                                |
| *(scopes)*     | called                                    | `ctech-account/.github/workflows/publish-resource-scopes.yml@main`.                  |

## The order is a dependency chain

```
Terraform → OAuth scopes → API → Frontend
```

- **Terraform first** because it creates the bucket the frontend syncs into and
  the ASG the API deploys onto, and writes the SSM parameters both later jobs
  read their targets from.
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
change anything, api uploads a zip and restarts a service, frontend writes
static files, scopes reads three SSM parameters. One role would give the job
that syncs HTML the rights of the job that can destroy the DynamoDB tables.

**No deploy role trusts a pull request.** The trust policies name
`repo:artur-oliveira/ctech-billing:ref:refs/heads/<branch>` for exactly three
branches. Everything that runs on a PR — `ci.yml`, and `infra.yml`'s validate
job with `-backend=false` — is designed to need no credentials at all.

## Prerequisites this pipeline does not create

These are other repositories' to provision. Each fails loudly at the first call
rather than silently degrading:

| What                                                                                                                         | Owner                                                | Used by                                      |
|------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------|----------------------------------------------|
| `/ctech/global/oidc/provider-arn`                                                                                            | ctech-cdk                                            | every role's trust policy                    |
| `/ctech/global/acm/cert-arn`                                                                                                 | ctech-cdk                                            | the CloudFront distribution                  |
| `/ctech/{env}/s3/deployments-bucket`                                                                                         | ctech-cdk                                            | `api.yml`                                    |
| `/ctech-account/{env}/scope-publishers/billing/{client-id,client-secret}`                                                    | ctech-account                                        | the scopes job                               |
| OAuth client `billing` with the four `billing:me:*` scopes and `https://billing*.aoctech.app/callback` as a redirect URI     | ctech-account                                        | the portal's login                           |
| A DNS record pointing `billing[-env].aoctech.app` at the distribution                                                        | DNS, outside Terraform — same as `billing-api` today | everything                                   |
| The four billing SecureStrings (`wallet-client-id`, `wallet-client-secret`, `wallet-webhook-secret`, `checkout-link-secret`) | set out of band                                      | payments and checkout links                  |
| `/ctech-billing/{env}/billing/field-encryption-key` — 32 bytes, base64 or hex                                                | set out of band, backed up outside SSM               | **every binary refuses to start without it** |
| `/ctech-billing/{env}/billing/email-from`, matching a verified SES identity and `var.email_from`                             | set out of band + SES                                | dunning reminders                            |
| A tenant plan applied with `cmd/seed`                                                                                        | this repo, run by an operator                        | everything — see below                       |
| `"max_charge_cents": 1000000` on billing's entry in `/ctech-wallet/{env}/m2m-clients`                                       | ctech-wallet, set out of band                        | any invoice above R$ 1.000,00                |

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
to a tenant that does not exist. `PORTAL_ORGANIZATION_ID` must name the organization the plan
creates.

It is deliberately not a pipeline stage. Creating a tenant is an admission decision, and a deploy
that quietly provisions one is a deploy that can quietly provision the wrong one. It is
create-or-skip, so re-running it is safe and adding a price to the file creates only the price.

## The charge ceiling is set in the other repository

Wallet defaults an M2M client to R$ 1.000,00 per charge. The DF-e sob-demanda plan bills per
document, so a high-volume month passes that on its own — and the refusal arrives when the customer
tries to pay an invoice that was already issued and numbered. Billing's entry in
`/ctech-wallet/{env}/m2m-clients` must carry `"max_charge_cents": 1000000`, which is the
[ADR 0004 amendment](../../docs/adr/0004-pix-on-invoice-via-wallet.md). `billing.MaxChargeCents`
mirrors it so a price can be rejected at creation; the mirror is only right while the two agree,
and nothing checks that across repositories.
