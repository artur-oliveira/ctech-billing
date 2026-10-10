# ADR 0028 — The console's tenant comes from the selected organization's membership, not from an owner

Status: Accepted (2026-10-10) · Supersedes in part [ADR 0011](0011-console-session.md) (the tenant from the owner;
the mode from the request stands) · Extends [ADR 0025](0025-spaces-personal-and-organization.md) · Spec:
[`2026-10-10-console-self-service.md`](../specs/2026-10-10-console-self-service.md)

## Context

ADR 0011 resolved the console's tenant through a sparse key `{mode}#OWNER#{user_id}` on the organization row: one
person, one tenant per mode. ADR 0007 kept `Organization` at one owner on purpose, and ADR 0011 named the trigger for
changing it: "when `Organization` becomes multi-member, `GetByOwner` returns a list and the console grows an
organization switcher".

Both halves of that trigger have arrived:

- Organizations live in ctech-account with a membership and a four-role ladder (ADRs 0021–0023), and the console's
  finance section already resolves its space through that membership (ADR 0025), with a selector.
- The single owner failed in production on 2026-10-10: tenant zero was provisioned API-only, so its owner field was
  empty, and the owner of CTech's account organization got `403 no_organization` from `/v1.0/console/session`. The fix
  that day (the seed writing the owner once) repairs the data, not the shape: a second admin of the same organization
  still cannot reach the console, and the person who reaches it is whoever a JSON file names.
- Self-service tenants (the spec above) are created from an account organization by any of its owners or admins. A
  single stored owner would pick one of them arbitrarily.

## Decision

- **The billing (merchant) half of the console resolves its tenant from the space selector**, with the resolver the
  finance half already uses: `X-Billing-Space: org:{id}` requires a ctech-account membership for the token's `sub`,
  checked before any table read, cached 60 s positive and negative, failing closed when ctech-account does not answer.
- **The tenant is found by the account organization it is linked to**: the organization row's lookup key becomes
  `{mode}#ACCOUNT_ORG#{account_organization_id}`. At most one tenant per account organization per mode.
- **The role decides the verbs, and billing defines them** (ADR 0023): `owner` and `admin` reach the whole billing
  half of the console; `member` and `viewer` do not reach it at all (it holds the merchant's customers' personal
  data). A read-only role is a later decision with its own trigger: somebody asks for it.
- **Only organization spaces are merchants.** `X-Billing-Space: personal` has no billing half; an invoice needs a
  company behind it.
- **`owner_user_id` stops granting access.** `GetByOwner` and the `{mode}#OWNER#` key are removed once every linked
  tenant carries the new key; the field stays on old rows, unread.
- **The mode still comes from the request** (`X-Billing-Mode`), exactly as ADR 0011 decided: the membership check
  happens per request and both mode partitions of a tenant belong to the same organization, so the header cannot widen
  access.

## Consequences

- Every owner and admin of an organization reaches its tenant; nobody else does, whatever a file says.
- A person with several organizations switches between them with the selector the finance half already has; the
  switcher ADR 0011 deferred is that one.
- A removed admin may keep reading for up to 60 s (the cache), the limit ADR 0025 already accepted.
- An unlinked tenant (none exists in production; tenant zero is linked) is reachable only through M2M credentials.

## Reopen if

A tenant needs to be reached by somebody who is not an owner or admin of its organization (an accountant serving
several merchants, support staff): that is a new role or a delegation, decided in its own ADR, never a second owner
field.
