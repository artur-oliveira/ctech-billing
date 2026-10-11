# Spec — Console self-service: access by membership, applications, merchant tenants

Status: **Draft for review** · 2026-10-10 ·
Decision record: [ADR 0028](../adr/0028-console-tenant-from-membership.md) ·
Builds on: [ADR 0005](../adr/0005-payout-gate.md) (payout gate), [ADR 0011](../adr/0011-console-session.md) (console
session), [ADR 0016](../adr/0016-webhook-routing-by-product-owner.md) (owner keys), [ADRs 0021–0023](../adr/0021-platform-organizations-and-companies.md)
(organizations and membership in ctech-account), [ADR 0025](../adr/0025-spaces-personal-and-organization.md) (space
selector) · Counterpart in ctech-account: § 6.3 (one internal route)

## 1. What this is

Organizations that want to bill **their own customers** with CTech Billing can ask for it from the console, start in
test mode at once, and reach live mode when CTech approves. Four things:

1. **Access by membership.** The billing half of the console (invoices, subscriptions, customers) resolves its tenant
   from the selected organization and the person's role in ctech-account, not from a stored owner (ADR 0028).
2. **Applications.** An organization's owner or admin applies; the test-mode tenant exists immediately; CTech reviews
   the application in tenant zero's own console and approves or rejects it.
3. **Approval is an entitlement.** Approving subscribes the organization to `prod_billing` in tenant zero (R$ 0 early
   access price) and creates the live tenant; cancelling that subscription suspends it.
4. **Integration tools.** The merchant connects its own OAuth clients as API credentials and manages webhook
   endpoints from the console.

What it is **not** (each its own spec, later):
- Not catalogue, customer, subscription or one-off invoice screens. The merchant integrates through the M2M API,
  which exists; the console is for following and for the tools above.
- Not paid plans for `prod_billing`, and not money collection: tenants are created at `payout_status:
  not_configured` (ADR 0005). Wallet custody or BYOK Inter comes first.
- Not company verification (KYB), not a read-only role for members, not Finanças plans for organizations.

## 2. Decisions

| # | Decision |
|---|---|
| D1 | Self-service is for organizations CTech approves. Asking is open to any organization owner or admin; **test mode opens on asking**; live mode waits for approval. |
| D2 | Applications are reviewed in **tenant zero's console** ("Solicitações"), by the owners and admins of CTech's account organization. No new support role in ctech-account. |
| D3 | Approval = an active subscription of the organization's `ORG_` customer in tenant zero to `prod_billing` (`price_billing_early_access`, R$ 0, owner key `billing`). Its cancellation (revocation now, non-payment later) **suspends** the live tenant: reads allowed, writes refused. |
| D4 | First delivery is API-first: credentials and webhooks in the console; catalogue and invoicing screens in a later spec. |
| D5 | The merchant creates its OAuth client in ctech-account's developer page and **connects** it in the console; billing verifies the client's owner through a new ctech-account internal route. The client secret never passes through billing. |
| D6 | `owner` and `admin` reach the billing half of the console; `member` and `viewer` do not. Only organization spaces are merchants. |
| D7 | Routes and endpoints in English; Portuguese only in i18n text. |

## 3. Access by membership (ADR 0028)

- `ResolveConsoleTenant` stops resolving by owner. The billing half of the console now requires `X-Billing-Space`
  and runs behind the same `ResolveSpace` middleware the finance half uses; a new step then resolves the tenant:
  1. The space must be `org:{id}`. `personal` → `404` (`not_found`), the same body as any unknown space.
  2. The role from the membership must be `owner` or `admin`. Otherwise `403` with code `merchant_role_required`.
  3. The tenant is read by lookup key `{mode}#ACCOUNT_ORG#{id}`. None → `404` with code `no_merchant`, which the
     console renders as the invitation to apply (§ 7.1).
- Mode handling is unchanged (`X-Billing-Mode` required).
- `GET /v1.0/console/session` returns, for the person, their organization spaces where they are `owner` or `admin`,
  each with `merchant_status`: `none` | `test` (applied, pending review) | `live` (approved) | `rejected` |
  `suspended`. The console's selector and invitation read it; it is display data, never authority.
- `OrganizationRepository.GetByAccountOrganization(ctx, accountOrgID, livemode)` replaces `GetByOwner`.
  `Create` and `LinkAccountOrganization` write the new lookup key. The owner key is no longer written.

## 4. Applications and approval

### 4.1 Applying

`POST /v1.0/console/merchant/application` (space `org:{id}`, role owner/admin, mode header ignored: an application
is not a mode's). Body:

```json
{ "company_id": "…", "description": "Venda de cursos online para pessoas físicas",
  "website": "https://…", "expected_monthly_volume": "até R$ 10 mil" }
```

- `company_id` must be one of the organization's companies in ctech-account (the reach client already lists them);
  it becomes the tenant's billing company (name and document on the merchant's invoices).
- `description` required, 20–1000 characters; `website` optional `https` URL; `expected_monthly_volume` optional,
  one of a fixed list (i18n labels).
- In **one conditional transaction** (tenant zero's table and the new tenant's rows):
  1. the **test-mode tenant** (`org_{ulid}`, display name = the account organization's name, `payout_status:
     not_configured`, linked to the account organization, lookup key `test#ACCOUNT_ORG#{id}`);
  2. the **application** in tenant zero, live partition: `APPLICATION#{id}`, status `pending`, plus the pointer
     `APPLICATION_ORG#{account_org_id}` → current application, conditional on no pending one;
  3. the **`ORG_` customer** in tenant zero if absent (the `CUSTOMER_ORG#` pointer from the plans spec § 8).
- A second application while one is pending returns the pending one (`200`, no new rows). After a rejection a new
  application is allowed; earlier ones stay as history. The test tenant is created only once.
- After commit: an e-mail to the address in SSM `/ctech-billing/{env}/billing/applications-notify-email` (SES, as
  dunning does). A failed e-mail is logged, never a failed application.

`GET /v1.0/console/merchant/application` returns the selected organization's current application (status, dates,
rejection reason).

### 4.2 Reviewing (tenant zero only)

Routes under `/v1.0/console/applications`, reachable only when the resolved tenant **is tenant zero** (config
`TENANT_ZERO`) and the role is owner/admin; any other tenant → `404`.

- `GET /v1.0/console/applications?status=pending|approved|rejected` — organization name, company name and document,
  description, website, volume, who applied, when.
- `POST /v1.0/console/applications/:id/approve` — idempotent:
  1. creates (or reuses) the subscription of the `ORG_` customer to `price_billing_early_access` in tenant zero, live;
  2. creates the **live tenant** (same id as the test one, lookup key `live#ACCOUNT_ORG#{id}`), if absent;
  3. marks the application `approved` (`decided_by`, `decided_at`), audited.
  A second approval changes nothing and answers the same body.
- `POST /v1.0/console/applications/:id/reject` with `{ "reason": "…" }` (required, 10–500 characters) — marks it
  `rejected`, audited. The test tenant stays.
- Both send the applicant an e-mail (the person who applied, from ctech-account's user record).

### 4.3 Suspension

- An in-process hook on subscription state changes in tenant zero: when a subscription carrying a `prod_billing` price
  leaves an entitled state, the live tenant linked to its `ORG_` customer's organization gets `suspended_at`; when an
  entitled `prod_billing` subscription becomes active again, `suspended_at` is cleared. Both audited.
- A suspended live tenant: M2M **writes** answer `403` with code `tenant_suspended`; reads and the console keep
  working; scheduled jobs (invoicing, dunning) skip it. Test mode is never suspended.
- No entitlement read on the request path: the suspension flag is on the tenant row the request already reads.

### 4.4 Catalogue

`tenants/ctech.json` gains `prod_billing` (owner key `billing`) and `price_billing_early_access` (fixed, monthly,
R$ 0, `metadata.plan: early_access`, not shown in any public plan list).

## 5. Credentials and webhooks

### 5.1 Credentials

- `GET /v1.0/console/credentials` — the tenant's credentials in the current mode: client id, who connected it, when.
- `POST /v1.0/console/credentials` `{ "client_id": "…" }` — billing calls ctech-account's internal route (§ 6.3) and
  admits the client to the tenant in the current mode only if:
  - the client's owner is the person connecting it;
  - it is active and holds **only** scopes marked `"m2m": true` (at least one `billing:` scope). A client that also
    holds a sign-in scope (`billing:my-*`, `openid`, …) could never get a `client_credentials` token (§ 6.3), so it is
    refused here, at connect time;
  - it is not admitted to any tenant (the global `Resolve` lookup).
  Refusals: `422` codes `client_not_yours` (also for an unknown client: no oracle), `client_has_no_billing_scope`,
  `client_not_machine_only`, `client_already_connected` (never says to which tenant). Live mode requires the live tenant (approved).
- `DELETE /v1.0/console/credentials/:client_id` — removes the admission; audited.
- The console links to ctech-account's developer page with the billing scopes preselected.
- A credential belongs to the tenant: its connector leaving the organization does not remove it.

### 5.2 Webhooks

- `GET/POST /v1.0/console/webhooks`, `DELETE /v1.0/console/webhooks/:id`,
  `POST /v1.0/console/webhooks/:id/rotate-secret`, `POST /v1.0/console/webhooks/:id/test`.
- URL must be `https`; events from the existing event list; at most **5 endpoints per tenant per mode**.
- The signing secret is returned **once** (on create and on rotate), stored encrypted as today's endpoints are.
- The test call sends a `ping` event through the normal delivery path.

## 6. Interfaces

### 6.1 Console routes (new or changed)

| Route | Who |
|---|---|
| `GET /v1.0/console/session` | any signed-in person (adds organization spaces with `merchant_status`) |
| `GET/POST /v1.0/console/merchant/application` | owner/admin of the selected organization |
| `GET /v1.0/console/applications`, `POST …/:id/approve`, `POST …/:id/reject` | owner/admin, tenant zero only |
| `GET/POST /v1.0/console/credentials`, `DELETE …/:client_id` | owner/admin of the selected organization's tenant |
| `GET/POST /v1.0/console/webhooks`, `DELETE …/:id`, `POST …/:id/rotate-secret`, `POST …/:id/test` | same |

All require a user session (`RequireUserScope`). Writes need `billing:organization:write`, a new user scope in the
manifest; reads keep `billing:organization:read`.

### 6.2 Data (billing tables)

| Row | Where | Keys |
|---|---|---|
| Tenant lookup | organization row | `lookup_pk = {mode}#ACCOUNT_ORG#{account_org_id}` (replaces `#OWNER#`) |
| Suspension | organization row (live) | `suspended_at` |
| Application | tenant zero, live | `APPLICATION#{id}`; pointer `APPLICATION_ORG#{account_org_id}` |
| Credential | existing | adds `connected_by`, `connected_at` |

### 6.3 ctech-account

`GET /v1.0/internal/oauth-clients/{client_id}` → `{ "client_id", "owner_user_id", "allowed_scopes", "active" }`,
never the secret. New scope `internal:account:oauth-client-read`, granted to the client billing already uses for its
membership reads (a new `cmd/createclient -grant` mode adds a scope to an existing client). Unknown client → `404`.

**Third-party `client_credentials` (decided 2026-10-10).** Today ctech-account's `POST /v1.0/token` issues
`client_credentials` tokens to first-party clients only, so a merchant's developer-page client could not call the M2M
API at all. ctech-account changes so that a non-first-party confidential client gets a `client_credentials` token
when its owner is active and **every scope it holds is marked `"m2m": true`** by the owning resource server's manifest
(`"m2m"` is valid on public scopes only); otherwise the whole request is refused, never answered with a subset.
First-party clients are unchanged. Billing marks `"m2m": true` on `billing:subscriptions:read|write`,
`billing:invoices:read|write`, `billing:usage:write`, `billing:customers:read|write`, `billing:entitlements:read`,
`billing:products:read|write` and `billing:organization:read` — never on `billing:my-*`, `billing:finance:*` or
`billing:organization:write`. It publishes only after ctech-account's change is live in the same environment (an
older ctech-account drops the unknown field, and a later identical publish would not resend it). Plan:
`ctech-account/docs/plans/2026-10-10-oauth-client-internal-route.md`.

## 7. Console UI

Built with `/impeccable`, following `ui/DESIGN.md`; each screen has its own phone layout.

1. **Invitation** (`no_merchant` on an organization space): what billing does for them, the "Quero cobrar meus
   clientes" action → `/console/apply` (the form of § 4.1). After applying: the test mode is ready, live mode is
   under review, and the application's status.
2. **Status band** for `test` / `rejected` (with the reason and "pedir de novo") / `suspended`.
3. **`/console/applications`** (tenant zero only): pending first, approve and reject (reason dialog).
4. **`/console/credentials`** and **`/console/webhooks`** (§ 5).
5. The organization selector lists the person's organizations where they are owner/admin; the finance half's selector
   stays as it is.

## 8. Failure behaviour

- ctech-account unreachable: the billing half answers `503` (`membership_unavailable`, "tente de novo"); an
  application is never half-created (one transaction).
- Concurrent applications: one tenant, one pending application (conditions on the pointer and the tenant key).
- Concurrent approvals: one subscription (idempotency key per application), one live tenant.
- SES failures are logged and retried by nobody; the console shows the status anyway.

## 9. Deploy

1. ctech-account: the internal route and scope (§ 6.3); the scope granted to billing's client; third-party
   `client_credentials` for m2m-only clients.
2. billing part 1: membership resolution for the billing half, `ACCOUNT_ORG` lookup written on create/link, a backfill
   command (dry run by default) rewriting the lookup key of linked tenants; `GetByOwner` kept as a fallback until the
   backfill is confirmed, removed in the next deploy.
3. billing part 2: applications, review, approval, suspension; seed gains `prod_billing` and the early access price
   (`api/cmd/seed/run.sh prod`).
4. billing part 3: credentials and webhooks; the manifest marks its integration scopes `"m2m": true` (after step 1 is
   live).

Each part deploys on its own; part 1 alone only changes how people get in.

## 10. Tests and review focus

Part of done, not later hardening:

1. **Spoofing**: a member, a viewer, an admin of another organization, an invented `org:{id}` and `personal` each get
   `404`/`403` from every billing-half route, never data; a tenant-zero review route from any other tenant is `404`.
2. **Applications**: two concurrent applications make one tenant and one pending application; a re-application after
   rejection works; after approval the live tenant exists once.
3. **Suspension**: cancelling the `prod_billing` subscription suspends the live tenant (writes `403`, reads `200`,
   jobs skip it); a new entitled subscription lifts it; test mode is untouched.
4. **Credential hijack**: connecting a client owned by someone else, or already admitted elsewhere, is refused with no
   hint of the other tenant.
5. **Webhook secret** appears in exactly one response per create/rotate.
6. **Migration**: tenant zero reachable by its owner and admins after the backfill; nobody reaches it by
   `owner_user_id` alone after the fallback is removed.
