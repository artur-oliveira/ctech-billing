# ADR 0010 — Infrastructure as Terraform, not CDK

Status: Accepted (2026-08-15) · Supersedes the CDK direction in `ARCHITECTURE.md` § 1 and
`PLAN.md` Phase 0 · Decided by Artur

## Context

Every earlier document in this repository says billing's infrastructure would be AWS CDK importing
shared constructs from `ctech-cdk`. That was written from the state of `ctech-wallet`,
`ctech-dfe` and `ctech-account`, which are all CDK today.

It missed that the direction of travel has already changed. **`ctech-lbalancer` is Terraform**, and
its files say what they are: `variables.tf` opens with *"Port of `bin/ctech-lbalancer.ts` env-var
reading + validation"* and `locals.tf` with *"Port of `lib/constants.ts` and `lib/types.ts`"*. It
also carries blue-green cutover knobs (`resource_suffix`, `manage_routes`) explicitly described as
temporary, for applying the Terraform stack beside the CDK one until CDK's copies are destroyed.

So the question is not "CDK or introduce Terraform". It is "the toolchain being migrated away from,
or the one being migrated to".

## Decision

**Terraform**, following `ctech-lbalancer`'s conventions exactly:

- Root modules under `terraform/<root>/`, with `backend.tf`, `variables.tf`, `locals.tf`, the
  resource files, `outputs.tf`, and `environments/{dev,stage,prod}.tfvars`.
- The **shared** state backend created by
  `ctech-lbalancer/scripts/bootstrap-terraform-state.sh`: bucket `prod-ctech-terraform-state`,
  key `billing/terraform.tfstate`, `use_lockfile`, `profile = "ctech"`.
- One workspace per environment.
- `terraform >= 1.15`, `hashicorp/aws ~> 6.60`, region `us-east-1`, account `868899309401`.
  The version is pinned in `backend.tf` **and** locked in `.terraform.lock.hcl`, which is
  committed: a lock file left at an older major is how a root validates green locally against a
  provider nobody deploys with.
- `default_tags` with `Environment`, `Project`, `ManagedBy`.

Billing writes **no new CDK**.

## Consequences

- **The company runs two infrastructure toolchains until the migration finishes.** That is a real
  cost, and it is the cost of migrating at all — it is not created by this decision, and adding
  billing to the CDK side would have made the eventual port larger rather than avoiding it.
- **Cross-root dependencies go through SSM Parameter Store, never shared state.** That is already
  how `ctech-lbalancer` consumes `ctech-cdk`'s network outputs
  (`/ctech/{env}/network/vpc-id`, `/ctech/{env}/network/alb-sg-id`). Billing publishes
  `/ctech/{env}/billing/table-name` and `/ctech/{env}/billing/role-arn` the same way.
- **`ctech-cdk`'s shared constructs are not available**, so anything they encapsulated — userdata
  fragments, the private-IPv4 EC2 service, CloudWatch agent config — has to be ported when
  billing's compute is written. The cross-stack duplication warning still applies with the same
  force: port it into a shared Terraform module the next service can use, do not fork it per repo.
- The period index is a single concatenated, zero-padded sort key (`2026#03#05#in_...`) queried by
  prefix, rather than the four-attribute sort key copied from `ctech-dfe`
  ([ADR 0002](0002-datastore-dynamodb.md)). **This is not something Terraform forced.** The first
  version of this ADR said `aws_dynamodb_table` accepts exactly one `range_key` per index and the
  multi-attribute form was therefore unavailable. That was true of `hashicorp/aws` 5.x and is wrong
  for the 6.x pinned here: provider 6 deprecated `hash_key`/`range_key` on
  `global_secondary_index` in favour of repeatable `key_schema` blocks, an unbounded list that
  exists to express exactly the multi-attribute keys `ctech-dfe` uses. The concatenated key stays
  on its own merits, and this bullet stays as the record of a claim that was checked against the
  wrong provider version.

## Limits accepted

- ~~The schema now exists twice~~ — **resolved 2026-08-15.** It briefly did, in
  `api/internal/repositories/table.go` and in `terraform/billing/dynamodb.tf`, checked by a Go test
  that parsed the `.tf` with regexes. "Terraform cannot read Go" was true and beside the point:
  Terraform can read JSON, and Go can embed it. The schema is now
  `api/internal/repositories/schema.json`, read by both, and the drift test is deleted along with
  the drift ([ADR 0002](0002-datastore-dynamodb.md)).
- Compute is now in this root — the launch template, the ASG, the HAProxy route and the two systemd
  timers that run the sweeps. See `terraform/README.md`.

## Reopen if

The company reverses the migration and standardises on CDK. Then billing follows, and this ADR is
superseded rather than edited.
