# ADR 0015 — Four deploy identities, none of which trusts a pull request

Status: Accepted (2026-08-16) · Completes the deploy path PLAN.md Phase 0 left open ·
Extends [ADR 0010](0010-infrastructure-as-terraform.md)

## Context

The pipeline does four different things to AWS: it runs Terraform, it uploads an API artifact and
restarts a service, it writes static files into a bucket and invalidates a distribution, and it
reads three SSM parameters in order to publish a scope manifest.

The default is one `ctech-billing-gha` role that can do all of it. It is one trust policy, one ARN
to paste into every workflow, and one thing to reason about — and it means the job that syncs HTML
holds `dynamodb:DeleteTable`. A compromised dependency in the front-end build, a mistyped `aws s3
rm`, or a malicious pull request that manages to run with credentials all reach the whole account
through the smallest of the four jobs.

The trust policy is the second half of the same question. `repo:owner/name:*` is what most examples
show, and it trusts **every ref in the repository** — every pull request branch, every tag anybody
can push. A fork's PR does not get the token, but a branch pushed to the repository does, and the
subject condition is the only thing standing between "somebody can open a PR" and "somebody can
assume a deploy role".

## Decision

**Four roles, one per thing a deploy does**, created by `terraform/github/`:

| Role | What it may do |
|---|---|
| `ctech-billing-gha-infra` | Run Terraform — by definition, change anything |
| `ctech-billing-gha-api` | Upload an artifact and drive the rolling deploy through SSM |
| `ctech-billing-gha-frontend` | Write the static bucket, read three SSM parameters, invalidate |
| `ctech-billing-gha-scopes` | Read the three parameters the publish workflow needs |

**Every trust policy names `repo:artur-oliveira/ctech-billing:ref:refs/heads/<branch>` for exactly
the three deploy branches** (`main`, `staging`, `dev`), never a wildcard and never a pull-request
subject.

**Everything that runs on a pull request is designed to need no credentials at all.** `ci.yml` runs
gofmt, vet, the Go suites and the UI build with placeholder environment. `infra.yml`'s validate job
runs `terraform init -backend=false` and `validate`, which type-check the configuration without
touching state.

`terraform/github/` is a separate root with a single workspace, because the roles are one set for
all three environments and workspacing them would create three copies of the same principal.

## Consequences

- **A blast radius is a role, and the role is named after it.** "Can the frontend job drop a table"
  is answerable by reading one policy, not by reasoning about what a shared policy accumulated.
- **The infra role is the powerful one, and it is used by exactly one job**, on exactly three
  branches, in a workflow whose PR path never assumes it.
- **A PR cannot reach AWS**, so a PR check cannot be the hole. It also means PR checks cannot run
  `terraform plan` against real state — see the limit below.
- **The ARNs are hardcoded in the workflows**, not looked up. A workflow that has to authenticate
  before it can discover how to authenticate has a bootstrapping problem.
- **The roles must be applied before the first deploy of anything else**, which is why `infra.yml`
  applies `terraform/github` ahead of `terraform/billing` — and why the very first apply is a manual
  one from a workstation.

## Limits accepted

- **No `terraform plan` on pull requests.** Reviewing infrastructure changes therefore means reading
  HCL, not reading a plan comment. The alternative is a read-only fifth role trusted by PR subjects,
  and a read-only role in an account with SSM SecureStrings is not read-only in the sense that
  matters. If plan-on-PR is wanted later, it needs an explicitly parameter-blind policy, decided on
  its own.
- **Four roles is four policies to keep correct.** They will drift toward each other under pressure —
  the fix for a permissions failure is always to add the action to the role in front of you. The
  guard is that they are declared together in one small file.
- **The subject list is branch names in Terraform.** Renaming the default branch breaks deploys until
  the list is updated, which is a good failure and still a manual step.

## Reopen if

The repository gains environments with genuinely different accounts, or a deploy stage needs to run
from a tag. Both change the subject list rather than the split, and both are worth being explicit
about instead of widening to `:*`.
