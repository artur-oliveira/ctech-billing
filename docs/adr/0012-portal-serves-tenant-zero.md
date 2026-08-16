# ADR 0012 — The portal serves tenant zero; other merchants' customers get the checkout

Status: Accepted (2026-08-15) · Scopes the consumer surface referenced by
[ADR 0011](0011-console-session.md) · Screens P1–P3 and X1 (assessment § 15)

## Context

"Consumer portal" is ambiguous in a multi-tenant billing product, and the two readings lead to
completely different systems.

Read one way, it is **a portal for every customer of every merchant**: a person who buys from three
merchants signs in and sees all of it. That requires a read whose answer spans tenants, and every
partition key in this service begins with `{organization_id}#{livemode}` precisely so that such a
read is not expressible ([ADR 0003](0003-tenant-and-livemode-partition-key.md)). It would also make
billing responsible for a relationship it is not party to — the merchant's relationship with their
own customer, including who may see what and under whose privacy policy.

Read the other way, it is **CTech's own customer view**. CTech is tenant zero
([ADR 0001](0001-product-scope-a-and-b.md)): it sells `ctech-dfe` and the rest, and it invoices the
people who buy them. Those people already have a ctech-account, already sign in to the products they
bought, and have nowhere today to see what they are paying for.

## Decision

**The portal is tenant zero's customer view.** One organization, named by
`PORTAL_ORGANIZATION_ID`. A signed-in user is resolved to their `Customer` record **in that
organization** and sees their own subscriptions and invoices, nothing else.

**A third-party merchant's customers do not sign in to billing at all.** They reach one invoice
through the public hosted checkout, by a link, and that page is the whole of their experience. A
merchant who wants their customers to see history, manage a subscription, or hold an account builds
that themselves against the M2M API — with their own identity system, on their own domain, under
their own privacy policy.

**Everyone who signs in is a customer; some are also operators.** Holding an organization is what
makes someone an operator, and it is additive: the same account sees the portal and the console
without switching accounts or picking a role. `GET /v1.0/me` reports both, so the app never has to
probe a route and read a 403 as information.

## Consequences

- **The portal needs no index and no cross-tenant read.** The organization is known from
  configuration, so "which customer is this user" is a sort key inside that tenant's own partition
  (`CUSTOMER_USER#{user_id}`) and the lookup is a `GetItem`. ADR 0002 and ADR 0003 stand untouched;
  there is no exception to argue.
- **`Customer` gains a `user_id`**, written by the merchant on create. It is a reference to
  ctech-account, not new personal data. It is **not** matched by email: an address changes, an
  address is mistyped, and matching on one would hand a stranger somebody's invoices.
- **The portal is live-only.** Test mode exists so an integration cannot touch real data; a consumer
  does not integrate. If a merchant ever needs to preview it, that is a header, not a redesign.
- **A pointer row per (organization, user).** Written in the same transaction as the customer and
  conditional, so two customers in one organization cannot claim the same account.
- **The consumer surface publishes no internal vocabulary.** Status, cause and error codes are
  translated at the edge — "Vence em 3 dias", never `OPEN`. That is a rule about the DTO layer, not
  a suggestion for the UI: the internal name must not be in the payload to begin with.

## Limits accepted

- CTech's customers get a portal and a merchant's customers do not. That asymmetry is deliberate and
  it is a product boundary, not a technical one — but it will be read as favouritism the first time
  a merchant asks. The answer is the M2M API and the checkout page, both of which already exist for
  exactly this.
- One organization per portal deployment. Serving a second merchant's portal would mean resolving
  the organization from the request (a subdomain, a path), and that is a different decision with a
  different threat model. It is not a config change, and it should not be made to look like one.

## Amendment, 2026-08-16 — the no-account 403 is typed

A person who signs in with a valid CTech account and has never bought anything gets 403 from
`ResolvePortalIdentity`, alongside a closed account and a service token on a person's route. That
much stands: there is no customer behind the session, nothing below can serve them, and the three
cases must stay indistinguishable to anybody probing from outside.

What did not stand was the portal rendering all three the same way. The first is somebody at the
beginning, and they were met with a red error block quoting a sentence written for a log —
"nenhuma conta de cobrança para este usuário" — with no way to tell whether the portal was broken
or they were.

So that one refusal now carries `type: "/problems/no-billing-account"` and the portal renders it as
an empty state, in the same shape P1 already shows a customer who owes nothing. The type rather
than the status or the message, because the other 403s must keep looking like refusals and `detail`
is prose that gets rewritten.

This discloses nothing new. The body already said it, and it says it about the reader themselves —
that *this* account has no billing record is a fact they hold already. Nothing here reveals whether
some *other* account exists, which is what the paragraph above is protecting.

## Reopen if

A merchant is admitted whose customers genuinely have no other place to see their invoices, and the
checkout page is demonstrably not enough. Then the question is a per-merchant portal with the
organization resolved from the host — reopened as its own ADR, not as a wider default here.
