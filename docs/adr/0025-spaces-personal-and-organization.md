# ADR 0025 — Spaces: a personal space per user, and a selector that is never authority

Status: Accepted (2026-10-07) · Extends [ADR 0003](0003-tenant-and-livemode-partition-key.md) and
[ADR 0011](0011-console-session.md) · Spec:
[`2026-10-07-finance-erp-design.md`](../specs/2026-10-07-finance-erp-design.md) § 2

## Context

The finance section serves people who have no organization, and people who belong to several.
ADR 0011 resolved the console's tenant from the signed-in owner, which knows exactly one
organization and none for everybody else. ADR 0021 already noted the console "has no switcher"
because the state did not exist; ctech-account now returns a user's organizations and leaves the
switcher to billing.

A switcher is a value the browser sends. If it were trusted, knowing another space's id would be
enough to read it.

## Decision

**A space is the first part of every finance partition key:** `{organization_id}#{mode}` or
`USER#{sub}#{mode}`. ADR 0003's rule is unchanged — a query without a space is not expressible —
and the personal space is a new kind of tenant, not an exception.

**The selector is a request, resolved on the server every time:**

1. `X-Billing-Space: personal` carries no id and resolves to the token's own `sub`.
2. `X-Billing-Space: org:{id}` requires a ctech-account membership for the token's `sub`, checked
   before any table read; the role found there decides the verbs (ADR 0023).
3. No membership is **404**, indistinguishable from a space that does not exist.
4. Repositories take a `ResolvedSpace` that only the resolver constructs, so no handler can key a
   read on the raw header.
5. Every id in a request is looked up inside the resolved partition.
6. Membership is cached 60 s, positive and negative; an unreachable ctech-account fails closed.

There is **no consolidated view** across spaces. It would be the only cross-tenant read on a request
path.

## Consequences

- The console opens for every signed-in user (personal space), not only for organization owners.
- The space joins the mode in every console query key.
- The spoofing tests in the spec are part of the definition of done, not a later hardening pass.

## Limits accepted

- A member removed in ctech-account may keep reading for up to 60 s.
- A user with many organizations sees them one at a time.

## Reopen if

Somebody needs a consolidated view across spaces. The answer is a per-user projection written on
each fact, with its own threat model and its own ADR — never a fan-out keyed on ids the browser
sent.

## Amendment, 2026-10-07 — the portal uses the same selector

An organization's CTech subscription belongs to the organization, and any of its `owner`s or
`admin`s pays and manages it. [ADR 0012](0012-portal-serves-tenant-zero.md) resolves exactly one
customer per signed-in user (`CUSTOMER_USER#{user_id}`), so an organization customer had nowhere to
be seen.

The portal therefore gets the console's selector, with the **same resolver**:

- `personal` resolves to the customer pointed at by `CUSTOMER_USER#{sub}`, as today.
- `org:{id}` passes the membership check above; the role must be `owner` or `admin`, otherwise the
  answer is the same 404 as a space that does not exist. The customer is found through a pointer
  row `CUSTOMER_ORG#{organization_id}`, written in the same conditional transaction as the
  customer, exactly like the user pointer.
- The selector lists only organizations where the user is `owner` or `admin`.

ADR 0012 stands — the portal still serves tenant zero only. What changes is that "the signed-in
customer" may be an organization the user is entitled to act for.

## Amendment, 2026-10-07 — implemented in 6.2

The resolver, the verbs and the spoofing tests exist (`internal/space`). The membership route they
rely on is a ctech-account addition, still to be built (see PLAN.md, 6.2, "Pending").

## Amendment, 2026-10-08 — the switcher's source (6.3b)

The selector is not fed by ctech-account's `GET /v1.0/organizations`: that route serves first-party
clients only, and billing's browser token is not one. Billing serves `GET /console/finance/spaces`
instead: *Pessoal* first, then the organizations from account's service route
`GET /internal/users/:user_id/organizations`, always for the token's own subject, each with the verbs
its role grants. That route has its own scope, `internal:account:user-organizations`, minted on a
token separate from the membership check, so a client allowed to ask "is this person in that
organization" is not thereby allowed to enumerate a person's organizations.

The list stays information, not authority: every request still resolves its space through the
membership check. When account is unreachable the list answers the personal space with
`organizations_unavailable: true`, and the console says so instead of failing.
