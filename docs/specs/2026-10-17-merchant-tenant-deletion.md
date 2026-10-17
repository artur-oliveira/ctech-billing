# Spec: Closing a merchant tenant (deleting a billing account)

- **Status:** Draft (2026-10-17)
- **Projects:** `ctech-billing` (api, ui, terraform), `ctech-account` (the `billing.merchant_tenant` blocker it lifts)
- **Parent:** ctech-account `docs/specs/2026-10-06-account-deletion-overview.md` (D4, D12), this repo's
  `docs/plans/2026-10-07-account-deletion-participant.md` (R3: a workspace with a tenant blocks erasure "until a tenant deletion flow exists")
- **Plan:** none yet; write it after the open questions in § 9 are answered.

## 1. Outcome

An owner of a workspace that sells through CTech Billing (a *merchant tenant*) can close that billing account. Everything the
tenant holds is exported on request and then erased, nothing is left half-closed, and the workspace is no longer blocked from being
deleted with the person (`billing.merchant_tenant` stops being reported).

Tenant zero (CTech's own tenant, `PORTAL_ORGANIZATION_ID`) can never be closed by this flow.

## 2. Why this is not "account deletion"

Account deletion erases a **person's** data and keeps documents that belong to someone else. A tenant is a **business that is a
controller of its own customers' data**; billing is its processor. Ending the relationship means the processor returns and deletes
what it holds on the merchant's behalf (the same rule as dfe, overview D4): the duty to keep invoices for 5 years is the
**taxpayer's**, not CTech's. So, unlike a person leaving, a tenant closing erases **invoices too**, after offering an export.

## 3. Decisions (proposed, to be confirmed)

| # | Decision | Why |
|---|---|---|
| T1 | **Self-service by an owner of the workspace** (membership role `owner` in ctech-account, checked live), from the billing console. Support can start it for a merchant who asks by e-mail, with a second admin approving (four-eyes, like a legal hold). | A tenant has money and customers attached; two people for the support path. |
| T2 | **Same safety shape as account deletion:** typed phrase (`ENCERRAR CONTA DE COBRANÇA`), recent MFA or password, e-mail confirmation, **7-day cancelable grace** during which the tenant is **read-only** (no new invoices, subscriptions, charges or credentials; webhooks keep delivering what already happened; customers can still pay open invoices). | A stolen session must not close a business. Read-only keeps customers from being hurt by the grace period. |
| T3 | **Blockers** (checked at request, and again at the end of grace; live mode only): an invoice with a payment in flight (PIX created, not settled or expired), an open refund or credit-note collection, a BYOK payout in processing, and **active customer subscriptions** (`billing.tenant_active_subscriptions`, with the count). Open unpaid invoices with no payment in flight do **not** block: they are voided at purge. | Money in flight must settle before the data that proves it disappears. Canceling third parties' subscriptions silently is not ours to decide, so the merchant does it in the console first. |
| T4 | **Erased:** credentials (and the OAuth links), webhook endpoints, events and deliveries, applications, products and prices, customers, subscriptions, invoices with items, credit notes, payment attempts, checkout sessions, usage records and level rows, audit rows of the tenant, BYOK connections, hooks and counters (`BYOKCOUNT#`, `BYOKSUBS#`, `BYOK_USAGE#`), the PDFs in S3, idempotency rows, the tenant's finance space if it has one. | Everything is the merchant's. |
| T5 | **Retained:** nothing about the merchant's customers. A minimal **tombstone** per closed tenant (`TENANT#{id}`: closed-at, request id, no personal data) so late webhooks and retries are dropped, and the request's own audit trail in account (D6-style, retained). | Prevents resurrection without keeping data. |
| T6 | **Export first, optional but offered, and required to be *offered*:** a zip of invoices (PDF + CSV of lines), customers (CSV), subscriptions and credit notes, with a presigned link valid 7 days, started from the same screen. The consequences screen states plainly that the documents will be gone and that keeping them is the taxpayer's duty. | Same rule as the dfe XML export. |
| T7 | **Re-creating a tenant later for the same workspace is allowed** and starts from nothing (new tenant id). The tombstone is keyed by the old id and never blocks it. | Account deletion's D10 spirit: no permanent lock-out. |
| T8 | **One purge, resumable and idempotent**, run by a job (like `cmd/finance-rebuild`), batching 25 keys per `BatchWriteItem`, child partitions before the rows that name them, every table classified by a test (`TestEveryTableIsClassifiedForTheTenantPurge`, same pattern as the finance `notSpaceScoped` map). A new table must choose a side. | The pattern already caught real gaps (payout hook partition) in the person purge. |

## 4. Lifecycle

```
ACTIVE ─request(owner, phrase, MFA)─▶ AWAITING_CONFIRMATION ─e-mail link─▶ CLOSING (read-only, 7 d)
   ▲                                                                           │ cancel
   └─────────────────────────────── cancel ◀───────────────────────────────────┤
                                                                               ▼ end of grace, blockers re-checked
                                                              PURGING (job) ─▶ CLOSED (tombstone)
```

A blocker found at the end of grace sends the request to `BLOCKED` (the owner cancels, resolves it, asks again), as in the account flow.

## 5. Surfaces

- Console: Settings → "Encerrar conta de cobrança": blockers with action links, the export, the consequences screen, the phrase.
- API (console routes, owner-only, `RequireOwnerOfWorkspace`): `POST /v1.0/console/closure`, `GET /v1.0/console/closure`,
  `POST /v1.0/console/closure/export`; public link routes `POST /v1.0/closure/confirm|cancel` (the token is the credential).
- Internal: none. The account-side effect is only that `Eraser.hasTenant` stops finding the tenant, so `billing.merchant_tenant` clears on
  its own at the next eligibility check.
- State: new table `{prefix}_tenant_closures` (request rows, and the `TENANT#` tombstones) declared in `schema.json`.

## 6. What changes in the account-deletion participant

`Eraser.Purge` keeps blocking a workspace that still has a tenant. Once the tenant is `CLOSED`, `GetByAccountOrganization` returns not found and the
workspace is erased like any other. Nothing else in the person purge changes. The tombstone is not read by the person purge.

## 7. Consistency with the rest of the family

- dfe's organization deletion (overview D4) is the model: export offered, then irreversible erase, the processor deletes.
- wallet is not affected: its charges are wallet's ledger, retained there under its own legal basis; billing only drops its references.
- The payment records that belong to **wallet** (PIX charges created for the tenant's invoices) are not billing's to delete.

## 8. Out of scope

- Closing tenant zero.
- Partially closing (keeping some customers or invoices). A merchant who wants to keep documents exports them.
- Transferring a tenant to another workspace.

## 9. Open questions (block the plan)

1. **Legal:** may billing, as processor, erase issued invoices at the merchant's request when the merchant holds the 5-year duty? (Same question dfe's D4 already answered with "yes, after export"; the owner and counsel should confirm it applies to billing documents and to payment attempts that prove a settlement.)
2. **Active subscriptions (T3):** blocker, or cancel them with a notice e-mail to the customers at the start of grace?
3. **Export size:** a large merchant can have many invoices; is a CSV + PDFs zip built by a job and mailed as a link acceptable, or is an API-only export enough for v1?
4. **BYOK:** disconnecting a bank connection does not unregister the webhook at the bank (R23); should closing a tenant unregister it (a call to the bank per connection)?
