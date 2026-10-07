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

1. `Billing-Space: personal` carries no id and resolves to the token's own `sub`.
2. `Billing-Space: org:{id}` requires a ctech-account membership for the token's `sub`, checked
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
