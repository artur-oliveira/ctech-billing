# ADR 0022 — Company identity lives in `ctech-account`; only issuance lives in `ctech-dfe`

Status: Accepted (2026-08-29) · Supersedes the **home** of Company in
[ADR 0021](0021-platform-organizations-and-companies.md). Everything else in 0021 — the three-concept
split, `(organization_id, tax_id)` scoping, verification on the edge, the two counters, the quota
boundary — stands unchanged, and one of its limits is upgraded from "accepted" to "enforced".

## Context

[ADR 0021](0021-platform-organizations-and-companies.md) put Company's home in `ctech-dfe`, and then argued
against itself three times.

**Existence is already separated from enablement.** 0021 requires *"two counters, never one: companies that exist in
an organization, and companies enabled for a given product."* If existence is separable from a product's enablement,
existence is not that product's fact. A registered, un-enabled company is not a DF-e object; it is an object the DF-e
has not been asked to use.

**Verification was placed on an edge the DF-e does not own.** 0021's `User ↔ Company` link is the same shape as
membership, and `ctech-account` already runs exactly that machinery for people: document upload, a manager-only review
queue at `/admin/kyc`, audited access recording whoever opened a document. Company verification in `ctech-dfe` means
building it a second time — the very duplication `_analysis/cross-stack-duplication.md` exists to prevent, and which
0007 rejected option (c) to avoid.

**Billing would depend on the fiscal emitter to bill.** 0021 has billing read *"the organization's designated billing
company"*. With Company in the DF-e, `ctech-billing` cannot invoice a customer who does not use the DF-e without
asking the DF-e who they are.

Three consumers — dfe, billing, the account UI — and an owner that is one of them. That is the shape that produces a
second lineage, which is what 0007 and 0021 were both trying to avoid.

## Decision

### Company was two things wearing one name

Not "move Company to accounts". Split it where its consumers already divide:

| | **Identity** — `ctech-account` | **Issuance** — `ctech-dfe` |
|---|---|---|
| Answers | who this company is, and who may act for it | how this company emits |
| Holds | `tax_id` + `tax_id_kind`, `legal_name`, `trade_name`, the verified `User ↔ Company` edge | inscrição estadual, CRT/regime, fiscal address, série and numbering, CSC/CSRT, the A1 certificate |
| Read by | dfe, billing, the account UI | the DF-e alone |

The line is: *identity* is a fact about the company that serves everyone; *configuration* is one product knowing how to
issue. Inscrição Estadual is state-scoped and validated against rules `ctech-account` has no business knowing.

**The A1 certificate never leaves `ctech-dfe`** — not as a file, not as a field saying one exists. It is a private key,
and the only reason to mirror its existence elsewhere would be to render a badge.

### `tax_id_kind`, because a CPF issuer already exists

`ctech-dfe` keys organizations `CNPJ_{digits}` **or** `CPF_{digits}` today
(`api/internal/repositories/organizations.go:16`): MEI and produtor rural issue under a CPF. 0021 says "Company is a
CNPJ" in its title and would have modelled a customer base that is already wider than that.

### The id is opaque; the tax id is a unique attribute, not the key

`company_id` is a UUIDv7. `(organization_id, tax_id)` uniqueness is enforced by a conditional write on a lookup row —
the mechanism `Invitation` already uses for one-invite-per-email — not by making the tax id the primary key.

The DF-e's present key *is* the CNPJ, which is the expensive half of its migration and the reason to not repeat the
shape. A CNPJ is stable in practice but the record is not: a typo caught after issuance would otherwise mean re-keying
every row referencing it.

### 0021's duplication limit is upgraded from accepted to enforced

0021 accepted *"duplicated company rows across organizations that legitimately share a CNPJ, and with them duplicated
certificates"*, listing certificates as the cost. That understates it. **An NF-e is unique by (CNPJ, modelo, série,
número, ambiente).** Two organizations issuing under the same tax id *on the same série* collide at the SEFAZ: a
duplicate rejection, or a gap in numbering somebody must justify.

The hazard is in issuance, not identity — which is what the split above makes actionable:

- **In `ctech-account`, duplicate identity is free.** A CNPJ is public data; anyone can type yours. Registering is a
  claim, not a capability.
- **In `ctech-dfe`, two enabled companies sharing a tax id must not share a série.** A fiscal-domain rule, enforced by
  the fiscal domain, at enablement — not a hazard merely accepted in prose.

This is 0021's own "verification gates capability, not existence", now with a home that can enforce it.

### Why the sharing stays, restated with the cases

0021's `(organization_id, tax_id)` scoping is unchanged, and the case that settles it is **changing accountants**: a
client leaves office A for office B. Under a globally unique tax id, B cannot register until A deletes — the former
accountant holds a departed client hostage, and deleting destroys records A must keep for five years. The others: an
accountant and their client issuing in parallel (the ordinary shape of this market), BPO fiscal, and a group where the
holding issues for a unit that also issues for itself.

Matriz and filial is **not** one of these: a filial has its own CNPJ, so it is simply a second company.

## Consequences

- `ctech-dfe` re-keys twice, not once. Its fiscal configs are singletons hanging off the organization PK
  (`organization_nfe_configs` and siblings, one record per org); they become one record per **company**. The
  organization migration that already ran mapped dfe organizations — which are really companies — onto platform
  organizations. This ADR is what unfuses them, and the second pass reuses `source_system`/`source_ref` for the same
  idempotency.
- The [organization handoff](../../../ctech-account/docs/specs/2026-08-29-organization-handoff.md) returns
  `organization_id` **and** `company_id`. The person types the CNPJ once and the DF-e asks only for what is its own,
  instead of the handoff delivering an empty organization and a second form.
- `ctech-billing` gains a designated billing company on the organization and stops needing the DF-e to know whom it
  invoices.
- **0021's one-noun rule gets stricter, not weaker.** The interface shows a single name until a second company exists.
  Moving Company to a surface people actually visit makes it easier to leak the second noun at signup, and it must not.
- Company verification reuses the KYC review queue. A second review queue is the failure this ADR exists to prevent.

## Limits accepted

- **Two records for one CNPJ shared between organizations**, unchanged from 0021 — plus, now, two identities to keep
  current. A `legal_name` corrected in one organization does not correct the other. Accepted: the alternative is a
  shared record one tenant can edit for another.
- **`ctech-account` stores a tax id it cannot verify at registration.** Anyone may type any CNPJ. This is deliberate —
  the verified edge is what gates acting for it — but it means the company list shows unverified claims, and the UI
  must never present one as established.
- **The série rule is enforced in `ctech-dfe`, where this ADR cannot see it.** Identity being permissive is only safe
  while issuance is strict; the two halves are in different repos and nothing mechanical binds them.

## Reopen if

A second product needs issuance configuration (a company issuing through something that is not `ctech-dfe`) — the
identity/issuance line would then have a third side and the split above would need a middle. Or if the verified
`User ↔ Company` edge turns out to need per-company roles rather than a single "may act for", which would mean a
permission system inside a company and a decision this ADR deliberately did not make.
