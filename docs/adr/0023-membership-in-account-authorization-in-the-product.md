# ADR 0023 — Membership lives in `ctech-account`; authorization lives in the product

Status: Accepted (2026-08-29) · Amends [ADR 0022](0022-company-identity-in-account.md), which left
company-scoped roles out ("a role ladder inside a company is a second permission system, and there
is no case for it yet"). There is now a case: `ctech-dfe` already has one, and unifying membership
without deciding where the role goes would move it by accident.

## Context

`ctech-dfe` owns `organization_users`: one row per person per company, carrying a role
(`OWNER`/`ADMIN`/`USER`/`VIEWER`) **and** a list of explicit `action.resource` grants on top of it. It
also owns its own invitations.

[ADR 0021](0021-platform-organizations-and-companies.md) moved Organization to `ctech-account` and
[ADR 0022](0022-company-identity-in-account.md) moved Company identity there too, including a
`User ↔ Company` "may act for" edge. That leaves two systems answering "who may do what here", which
is the shape that produces a second lineage — the thing 0007 rejected option (c) to avoid.

The company re-key made this concrete rather than theoretical: `organization_users` is partitioned by
the same key Company is, so it had to be re-keyed alongside it, and the re-key's own plan first
mistook "move this table" for "unify these models". They are different work and this record separates
them.

## Decision

### The line: identity and reach in the platform, verbs in the product

**`ctech-account` answers whether a person may reach a company. The product answers what they may do
there.**

| | `ctech-account` | the product (`ctech-dfe`, and billing later) |
|---|---|---|
| Who the person is | ✓ | |
| Who is in the workspace | ✓ — membership, with the four-role ladder | |
| **Who may act for a company** | ✓ — the `User ↔ Company` edge | |
| Invitations | ✓ | |
| What role they hold **on a company** | | ✓ |
| What a role may do | | ✓ |
| Explicit grants beyond a role | | ✓ |

This is the shape every identity provider that solved this converged on — Auth0 Organizations, WorkOS,
Okta, Entra all keep a small coarse role set centrally and let each application map it to its own fine
permissions; AWS IAM owns the principal while each service defines its own actions; Kubernetes
authenticates through OIDC and authorizes in-cluster.

The reason is not fashion. **Only the system that defines the verbs can decide what they mean.**
Nothing outside `ctech-dfe` knows what `emit.nfe` is, and a permission vocabulary that lives where its
verbs do not is a vocabulary two teams edit and neither owns.

### The role stays in the product, and that is the part worth arguing

A role is a **named bundle of permissions**. It is authorization vocabulary, not identity.

Moving it to `ctech-account` looks tidy — one place for "who is what" — and is the trap. The moment
`OWNER` lives there, the next question is whether billing may use the same value, and the answer has
to be either "yes, and now two products' authorization is coupled through a word" or "no, and now the
platform holds a role only one product reads". Both are worse than leaving it where its meaning is.

So `ctech-dfe` keeps a row per `(company, user)` carrying the role and any grants. What it stops
keeping is the claim that this row is what grants **access**: the edge in `ctech-account` is.

### The company's owner is the product's `OWNER`

0022 gave Company no owner field, deliberately. This ADR does not add one.

"Only the owner may hand out explicit grants" is a rule about what somebody may do, so it is answered
where roles are: the `OWNER` on the product's own `(company, user)` row. The organization's
`owner_user_id` is **not** it — an accountant owning a workspace of forty CNPJs would own every
company in it, which is exactly the customer this model exists for and exactly the wrong answer.

### Invitations carry the companies they are for

`ctech-account`'s invitation grants organization membership and a ladder role. That is not enough:
the case that pays for this model is an accountant inviting a junior who should reach five of forty
companies, and an invitation that cannot say which five leaves the junior inside the workspace and
able to act for nothing.

So an invitation gains an optional list of companies, and accepting it writes the membership **and**
those edges in the same transaction. Optional, because inviting somebody to the workspace with no
company is a real thing — a bookkeeper who only reads invoices — and forcing a list would make the
common case carry the accountant's problem.

**An accepted invitation with no companies grants no company access, and the interface must say so.**
Silence there is a person who joined and cannot work, with nothing on screen explaining why.

## Consequences

- `ctech-dfe`'s `organization_users` stops being the access record and becomes an authorization
  overlay. Its role and permissions survive; its meaning narrows. `PermChecker` resolves reach from
  the edge and verbs from the row.
- **A row with no edge grants nothing.** That is the invariant the unification is for, and it must
  fail closed: a product that fell back to its own row when the edge was unreadable would have
  reinvented the second lineage on its first outage.
- `ctech-dfe`'s invitations are retired in favour of the platform's. Two invitation flows for one
  workspace is two e-mails, two tokens and two ways to be half-invited.
- The explicit grants the dfe migration sent to its "needs a human" bucket are still not migrated,
  and now have an order: decide the owner (done here), extend the invitation (here), then move the
  grants. Moving them first would give them to nobody empowered to change them.
- `ctech-billing` inherits the same split when its console gains multi-organization support. It gets
  the edge for free and defines its own verbs, which is the point of drawing the line here rather
  than per-product.

## Limits accepted

- **Two records per person per company** — the edge in `ctech-account`, the overlay in the product —
  and a read of both on the authorization path. The alternative is one record in one place, which is
  either the platform holding fiscal verbs or the product holding identity, and both were rejected
  above. The overlay is cached the way the membership already is.
- **They can disagree.** An edge revoked in `ctech-account` leaves an overlay row behind. That is
  survivable only because the edge is the authority and the overlay alone grants nothing — but it
  means orphaned rows accumulate, and nothing collects them yet.
- **The product's role ladder and the platform's are different vocabularies with overlapping words.**
  `OWNER` in `ctech-dfe` and `owner` in `ctech-account` mean different things, and somebody will read
  one as the other. Renaming one is the obvious fix and is not taken here: both are load-bearing in
  live data, and a rename is a migration this ADR does not need.

## Reopen if

A second product needs the same fine-grained grants `ctech-dfe` has, which would make the permission
model shared vocabulary after all and move the argument for keeping it local. Or if the edge and the
overlay disagree often enough in production that the two-record cost stops being theoretical — at
which point the answer is probably to make the edge carry the role, and this record is where to start
reading why it does not.
