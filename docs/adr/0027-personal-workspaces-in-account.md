# ADR 0027 — Shared personal spaces are ctech-account workspaces of kind `personal`

Status: Accepted (2026-10-09) · Extends [ADR 0025](0025-spaces-personal-and-organization.md) ·
Applies [ADR 0023](0023-membership-in-account-authorization-in-the-product.md) · Spec:
[`2026-10-09-shared-spaces-design.md`](../specs/2026-10-09-shared-spaces-design.md)

## Context

People want more than one personal space and want to share one — a couple keeping their finances
together, at read or full access. An organization would serve, but it is shaped for a company: a CNPJ
on the way in, a four-rung role ladder, and the word "organização" on every screen.

Three homes were considered for the people of a shared space:

| Option | Cost |
|---|---|
| **ctech-account workspace, `kind: personal`** | Changes in ctech-account: a kind field, a three-role ladder on that kind, two handoffs, `kind` on two internal routes, and other products filtering the kind out. |
| Billing-local spaces, grants and invitations | A second invitation system — e-mail verification, hashed tokens, acceptance — which is what ADR 0023 retired in ctech-dfe. Account deletion would also have to learn about billing's members. |
| Billing spaces and grants, ctech-account invitations | Two sources for "is this person in this space", which is the shape that produces a second lineage. |

ADR 0021 already says an organization is a **workspace**, and ctech-account's handoff spec says that
outside a handoff "the company is optional". The CNPJ friction is in how organizations are entered,
not in the model.

## Decision

**A shared or additional personal space is a ctech-account workspace with `kind: personal`.**

- ctech-account owns it end to end: creation, invitations, roles, leaving, transfer and deletion of
  the person. Billing never writes membership. It **redirects** for creation and for managing people
  (handoff), as every product does for organizations.
- Roles on `personal` are `owner`, `member` (*Acesso total*) and `viewer` (*Leitura*). Billing maps
  them by **kind and role**; a role that does not exist on that kind, or an unknown kind, grants
  nothing, and the answer is the ordinary 404.
- **The resolver gains no path.** A personal workspace is selected with `org:{id}` and resolved by the
  same membership check, cache, single 404 and zero reads before membership as an organization. The
  protection ADR 0025 built is reused as is, not extended.
- **The default personal space (`USER#{sub}`) is never shareable.** Its key is a person's identity.
- A new space starts empty. No code path reads one space and writes another.

## Consequences

- `MembershipSource` returns the kind; the membership cache stores it. An entry without a kind reads
  as `organization`, which never grants more than `personal` would.
- The console shows only Finanças in a personal workspace; the portal never lists one.
- ctech-account's existing deletion rules cover these workspaces with no change: sole-member ones are
  erased and listed on `user.erase`, shared ones block deletion until ownership is transferred.
- Plan limits (spaces per owner, people per space) are enforced by ctech-account at creation and
  invitation, asking billing for the entitlement — the plans spec decides the numbers.

## Limits accepted

- A removed person keeps read access for up to 60 s (ADR 0025's cache).
- Deleting a space is not possible until a per-workspace purge protocol exists across products.
- `member` means different verbs on the two kinds. The interface never shows the raw role; it shows
  *Acesso total* or *Leitura*.

## Reopen if

A shared space needs permissions finer than the two levels, or needs to be shared with an
organization rather than with people. Either one means a permission model inside a space, and that is a
different decision.
