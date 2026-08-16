# Infrastructure

Terraform, not CDK — see [ADR 0010](../docs/adr/0010-infrastructure-as-terraform.md). Conventions
are `ctech-lbalancer`'s, deliberately identical so an operator who knows one knows the other.

## Roots

| Root       | What it owns                                                                                                                                                                                                                                                                             |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `billing/` | The DynamoDB tables and their indexes, the service's IAM role and instance profile, the SSM parameters other roots read, the compute (launch template, ASG, security group), the HAProxy route, the log groups/metrics/alarm they emit, and the portal's hosting (`frontend.tf`, below). |
| `github/`  | The four GitHub OIDC roles the pipeline assumes. One workspace, not three: the roles are one set for all environments, and workspacing them would create three copies of the same principal. Applied first, and the very first apply is manual — nothing has an identity to assume yet.  |

| Asset                       | What it is                                                                                                                                                                                 |
|-----------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `assets/bootstrap.sh.tftpl` | The whole instance bootstrap, as a shell script rather than a string array. It is the port of `ctech-wallet/cdk/lib/api-stack.ts`'s UserData and the `@aoctech/cdk` fragments it composes. |

## Applying

The state backend is the shared one; it is created by
`ctech-lbalancer/scripts/bootstrap-terraform-state.sh` and is not re-created here.

```sh
cd terraform/billing
terraform init
terraform workspace select dev || terraform workspace new dev
terraform plan  -var-file=environments/dev.tfvars
terraform apply -var-file=environments/dev.tfvars
```

One workspace per environment. The S3 backend nests non-default workspaces under
`env:/<workspace>/<key>` on its own, so the `key` in `backend.tf` is the same for all three.

## The table schema lives in exactly one place

`billing/dynamodb.tf` builds every table with `for_each` over
`api/internal/repositories/schema.json`, which the Go service embeds and creates the test tables
from. Adding a table or an index is an edit to that JSON and nothing else.

It used to live twice — Go for the tests, HCL for the real thing — with a Go test that parsed this
directory with regexes to prove the two agreed. That test worked, and it was answering a question
that should not exist. `api/internal/repositories/schema_test.go` now only checks what one file can
still get wrong on its own: a key naming an undeclared attribute, an attribute no key uses, an index
name no query code names, or `schedule-index` appearing on a table no sweep reads.

## Compute

One ASG of private-IPv4-only `t4g.micro` instances, `min = max = 1` by default, behind the shared
HAProxy edge. No public IPv4 and no NAT gateway: the instances reach AWS over IPv6, which is why
every agent on them is explicitly told to use the dual-stack endpoints.

`billing/route.tf` writes the route parameter this repo's ASG is named in, rather than leaving it in
`ctech-lbalancer`'s `default_routes`. The edge reads every parameter under
`/ctech/{env}/lbalancer/routes/` and does not care who wrote them; `@aoctech/cdk`'s
`HaproxyEc2Service` already lets a service register its own. A route that lives in another
repository is a route that outlives the thing it points at.

The ASG uses **EC2** health checks, not ELB — there is no target group. HAProxy owns the application
probe and reports repeated failures back through `SetInstanceHealth`, so an instance whose app has
died is still replaced; it is just the thing actually serving traffic that notices.

### The four scheduled jobs are systemd timers, not EventBridge

| Timer | When | Why that clock |
|---|---|---|
| `cmd/sweep` | daily 07:10 UTC | 04:10 in São Paulo, the civil day it bills |
| `cmd/dunning` | daily 08:10 UTC | an hour after the sweep, so today's invoices exist before anything decides whether to chase them |
| `cmd/reconcile` | hourly | an unanswered charge is an hourly fact about an integration |
| `cmd/deliver` | every minute | somebody is waiting to be let back into a product |

All are one-shot processes that need the service's own configuration and its DynamoDB role.
Driving them from outside would mean either an HTTP route — which ADR 0002 forbids for exactly these
cross-tenant read paths — or an SSM RunCommand fan-out that runs the job once per instance
anyway. A timer on the box needs no new principal, no new network path and no new failure mode.

`Persistent=true` is the reason to use timers rather than cron: an instance replaced at 03:00 runs
the missed sweep when it boots instead of skipping a day of invoices. Dunning carries it for the
same reason. **`deliver` deliberately does not** — a missed minute is nothing, the rows are still
queued, and catching up would only fire several passes back to back at boot.

`/opt/app/job.sh` runs a job **only on the leader instance** — the lowest instance id among the
ASG's `InService` members. Every job is idempotent, so a double run is wasteful rather than wrong;
what it produces is a loser writing conditional-check failures that read exactly like a real fault.
An alarm that cries wolf every night is an alarm nobody opens. If the ASG cannot be described the
job runs anyway: skipping means an invoice that is never generated, and that is the worse side of
the ambiguity.

This is what makes `max_size > 1` a real decision rather than a number. It is safe, and it is the
change that makes the leader election load-bearing.

## The portal's hosting

`billing/frontend.tf` is an HCL port of `ctech-cdk/lib/nextjs-static-frontend.ts`, deliberately the
same shape rather than a second design: a private bucket, an Origin Access Control, a
`cloudfront-js-2.0` Function that maps `/route` to `/route.html` by looking the route up in a
**KeyValueStore manifest** the deploy writes, and a response-headers policy carrying HSTS,
frame-deny and the CSP.

The distribution has **two origins**. `/v1/*` and `/.well-known/*` are ordered cache behaviours
pointed at the same HAProxy edge the API sits behind, so the browser is same-origin with its API
and CORS never applies — [ADR 0013](../docs/adr/0013-static-portal-same-origin-api.md).

There is deliberately **no `custom_error_response`**. Those are distribution-wide, so mapping 404
to `/404.html` would also replace the API's RFC 7807 problem bodies on the `/v1/*` behaviour. The
function handles the miss instead, on the bucket's behaviour only.

The bucket, the distribution id and the route store's ARN are published to SSM, which is how
`frontend.yml` finds what to sync into without being told twice.

### Still missing

- **A first apply.** None of this has ever run against real AWS. `terraform validate` and `fmt`
  pass on both roots, which proves the configuration parses and type-checks and proves nothing
  about what it creates.
- **`github/` before everything.** It must be applied once from a workstation, because the roles it
  creates are what the pipeline authenticates as.
- **DNS.** The record pointing `billing[-env].aoctech.app` at the distribution is outside Terraform,
  the same as `billing-api` today.

### The userdata is close to EC2's limit

The rendered `bootstrap.sh` is ~28 KB, gzipped and base64'd into ~12 KB of `user_data` against a
16 KiB ceiling. Two timers were added since that figure was last checked and it is still inside the
limit, with less room than before. The next substantial addition belongs in an S3 asset the
bootstrap downloads, not in the template — and it is worth re-measuring rather than assuming, which
is `gzip -9 -c bootstrap.sh.tftpl | base64 -w0 | wc -c`.

## Two more grants, both narrow

**SES.** Dunning sends reminders, so the role may `ses:SendEmail` — and only as one address. The
policy carries a `ses:FromAddress` condition pinned to `var.email_from`, because without it every
verified identity in the account is fair game, including ctech-account's, which is the address
customers are told to trust for password resets.

**A field encryption key.** `/ctech-billing/{env}/billing/field-encryption-key` is a SecureString
under the prefix the role already reads. It is the one parameter whose loss is not recoverable by
setting a new value: rotating it makes every stored tax id unreadable
([ADR 0017](../docs/adr/0017-field-level-encryption.md)), so it belongs wherever the account's
break-glass material lives, not only in SSM.

## The IAM policy denies `dynamodb:Scan`

Not an oversight, and not merely least privilege. ADR 0002 forbids `Scan` on any tenant read path,
because a `Scan` ignores the partition key — the one thing that makes cross-tenant access
unexpressible. Putting an explicit `Deny` in the role turns that from a convention someone can
forget in review into something the platform refuses, and it survives a later policy attachment
that would otherwise grant it.

An access pattern that genuinely needs a full-table read — an export, a migration — gets its own
role, deliberately.
