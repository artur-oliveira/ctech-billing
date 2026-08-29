# ADR 0021 — Organization is a workspace, Company is a CNPJ, and they are not the same record

Status: Accepted (2026-08-29) · Reopens and supersedes the deferral in
[ADR 0007](0007-minimal-organization.md)

## Context

[ADR 0007](0007-minimal-organization.md) shipped a minimal `Organization` local to billing and named
its own trigger: *"the first external merchant signs, **or** `ctech-dfe` and `ctech-billing` need a
shared organization — whichever comes first."* The second half has arrived: the console is built,
merchants are the next thing to sell, and dfe already reads billing's entitlements to enforce a
per-plan company quota.

Three facts about the code as it stands, and they are the whole reason this decision is not simply
"do what ADR 0007 said":

- **The word `Organization` already means two different things.** In `ctech-dfe` it is a *company*:
  the row's partition key is `CNPJ_…` or `CPF_…`, it carries `owner_user_id` ("the account whose
  subscription pays for it") and a single OWNER membership. In `ctech-billing` it is a *tenant*: a
  merchant, with a display name, a payout gate and one owner, and deliberately no CNPJ. Migrating
  either one into `ctech-account` without settling which meaning wins would move the ambiguity into
  the identity service, which is the worst possible place for it to live.
- **The 1:N relationship is already in production, unnamed.** A dfe plan carries
  `quota_companies: 10` in its price metadata (opaque to billing, ADR 0008). Something already
  holds many companies and is billed once. That something has no name and no record.
- **`ctech-account` already has person-level KYC** — `kyc_level` / `kyc_status`, an enhanced tier
  with documents in S3, a manager-only review queue at `/admin/kyc`, and audit events naming
  whoever opened a document. Company verification is the missing half of a machine that exists,
  not a new machine.

## Decision

### Three concepts, named apart, each with one home

| Concept | Answers | Home | Today |
|---|---|---|---|
| **User** | who is this person | `ctech-account` | exists |
| **Organization** | who shares this workspace, and **who gets the bill** | `ctech-account` | billing's local `Organization` |
| **Company** | **in whose name** the fiscal document is issued | `ctech-dfe` | dfe's CNPJ-keyed `Organization` |

`User —membership(role)→ Organization —1:N→ Company`.

The common case is one organization with one company and the same name on both, and that is not a
modelling failure — it is the shape most customers have. The split exists because of the case that
pays for it: an accountant is one organization, one subscription and one invoice, holding forty
CNPJs that belong to forty other people. Collapsing the two concepts makes that customer
unrepresentable, and they are the customer this product is for.

### Company is scoped to its Organization, and CNPJ is not globally unique

A company row is keyed by `(organization_id, tax_id)`. Two organizations may each hold the same
CNPJ, each with its own certificate and its own configuration.

This is a change from dfe's current global `CNPJ_…` key, and it is the expensive part of the
migration. It is still right: a company handled both by its own team and by its accountant is
ordinary, and under a global key the second one cannot exist. What makes it safe rather than
duplicative is that **the fiscal truth is not in our database** — it is at the SEFAZ, which neither
knows nor cares how many systems emit under a CNPJ.

### Verification attaches to the edge, never to the node

A CNPJ is public data. Anyone can type yours. So "this company is verified" is not a fact about a
company; the fact worth storing is **this person may act for this company**, which belongs to the
`User ↔ Company` link.

The consequence is the point: two people may claim the same CNPJ, and the second does not inherit
the first one's verification. Under a boolean on the company they would, and whoever arrived second
would walk into a company somebody else proved.

A registry lookup does not shortcut this. `ctech-dfe` already queries cnpja from the browser to fill
a form in, and that is what it should remain: a lookup says the CNPJ exists and is active, never
that *this person* may act for it. A server-side one is worth adding later — it shortens the review
queue by catching a dead CNPJ before a human reads documents — and it is evidence attached to the
claim, never the field that decides it.

Verification reuses `ctech-account`'s existing KYC machinery — levels, a pending/verified/rejected
status, private documents with explicit audited access, a manager-only review queue — with a
company as the subject and the membership as what gets stamped. It is not a second document store
and not a second review UI.

### Verification gates capability, not existence

Creating an organization, inviting people and adding a company are cheap and reversible, and are not
gated. Issuing a fiscal document in somebody's name and receiving money are neither, and are.

This keeps signup free of a review queue while leaving the queue exactly where the risk is — and it
is the same queue and the same evidence [ADR 0005](0005-payout-gate.md) already requires before
custody, so there is one KYB flow rather than two.

### Billing references the platform organization and keeps only what is billing's

After the migration billing stores `organization_id` and the things that are its own: invoice
numbering, the dunning policy, the payout gate, and the issuer block that heads a PDF. Membership,
roles and invitations are read from `ctech-account` and never mirrored.

**On the issuer block.** ADR 0007 forbade "no CNPJ, no legal name, no address" and warned that
accepting "just one little CNPJ field, to make it easier" is the first step of the duplication it
existed to prevent. Billing acquired exactly those four fields when the invoice PDF shipped
(2026-08-29), without that trade being argued — the ADR's own review sign fired and nobody read it.

Settled here rather than quietly kept: the fields stay, **as print data with a named source of
truth**. Billing renders them and validates none of them; it holds no certificate, no tax regime and
no service code, and it never becomes a company registry. After the migration their source is the
organization's designated billing company, and billing's copy is a denormalized rendering input —
the same relationship an invoice line already has to the price it was generated from (ADR 0008).
Anything fiscal beyond those four fields is still refused.

### Billing sells itself through tenant zero, and two things it must never do

CTech is organization zero ([ADR 0001](0001-product-scope-a-and-b.md),
[ADR 0012](0012-portal-serves-tenant-zero.md)), so a merchant paying for the console is a customer
of tenant zero exactly as a DF-e customer is: a product in tenant zero's catalogue with
`owner_key: "billing"`, an ordinary subscription, an ordinary invoice, ordinary PIX. No new
mechanism, and [ADR 0019](0019-zero-total-invoices.md) already makes a free tier a real subscription
with a real numbered document.

Three rules close the loops that shape creates:

1. **Tenant zero does not subscribe to anything.** Without this, somebody eventually subscribes
   CTech to CTech and the sweep issues an invoice from the company to itself, every month, forever.
2. **A merchant's own unpaid bill may restrict the console. It may never restrict the portal.**
   The portal is where they pay it. Gating the service and taking away the ability to pay for it is
   the self-defeating move the dunning policy already refuses.
3. **It may never stop their sweep.** Halting a delinquent merchant's invoice generation punishes
   *their* customers, who owe money to somebody else and are not party to the dispute.

### What this does **not** decide

Selling the console to a merchant is money flowing **to** CTech, into CTech's own account, which
works today. [ADR 0005](0005-payout-gate.md)'s custody gate blocks the opposite direction — a
merchant receiving from their own customers — and is untouched here. The two are independent, and
conflating them would defer a product that is not blocked.

## Consequences

- `ctech-dfe`'s organization becomes a Company under a platform `organization_id`, and its primary
  key changes. This is the migration's real cost and the reason dfe moves last.
- An organization with one company must not make anybody learn two nouns. The interface shows one
  name until a second company exists; "empresa" is a word the product introduces when it becomes
  true, not at signup.
- A product's quota is counted by that product. Billing carries `quota_companies` as opaque text
  and dfe decides what a company is (ADR 0008). That boundary does not move.
- **Two counters, never one:** companies that exist in an organization, and companies *enabled for a
  given product*. Quota applies to the second. A company registered and not enabled costs nothing,
  which is what lets one organization hold ten CNPJs and use one.
- Downgrading a plan below the enabled count does not disable anything by itself. It refuses new
  enablements and asks a person to choose. A system that picks which company stops emitting is a
  system that picks the wrong one.

## Limits accepted

- Duplicated company rows across organizations that legitimately share a CNPJ, and with them
  duplicated certificates. Accepted deliberately over a shared record whose configuration one
  tenant could change for another.
- One organization per owner remains the shape until membership ships; billing's `GetByOwner`
  returns a single organization and the console has no switcher. That is UI for a state that cannot
  yet exist.

## Reopen if

An organization needs to be owned by another organization (a franchise, a group with sub-tenants),
or a company needs to be shared between organizations as one record rather than two — which would
mean the certificate and the fiscal configuration have found a single owner, and the reasoning above
has changed.
