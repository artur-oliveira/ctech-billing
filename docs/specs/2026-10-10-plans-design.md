# Spec — Plans: CTech Finanças plans, level metering, organization customers

Status: **Design approved, not implemented** · 2026-10-10 ·
Builds on: [`2026-10-09-shared-spaces-design.md`](2026-10-09-shared-spaces-design.md) § 7 (the deferred plan table),
[ADR 0008](../adr/0008-opaque-metadata.md) (price metadata is opaque), [ADR 0025](../adr/0025-spaces-personal-and-organization.md)
(the portal selector amendment), [ADR 0027](../adr/0027-personal-workspaces-in-account.md) ·
Counterpart in ctech-dfe: `ctech-dfe/docs/specs/2026-10-10-organization-subscription.md` ·
Counterpart in ctech-account: to be written from § 5 of this spec

## 1. What this is

Three things, in one spec because each needs the others:

1. **Plans for CTech Finanças**: how many additional personal spaces a person may own and how many people each
   space may hold, sold as Free, Basic, Pro and Sob demanda.
2. **Level metering**: a new, generic kind of metered price billed on the *highest level* reached in a period,
   reported per customer rather than per subscription item. Finanças is its first consumer; any per-seat charge
   later is the next.
3. **Organization customers**: `ORG_{organization_id}` as a customer reference, the `CUSTOMER_ORG#` pointer and
   the portal selector — what the DF-e needs to move its subscription from the owner's user to the organization.

What it is **not**:
- Not limits on what happens *inside* a space (bills, accounts, imports). Only spaces and people are counted.
- Not a premium feature tier. Only the people/spaces axis is sold now; the catalogue shape lets features join
  later as more metadata on the same prices.
- Not a plan for organizations' Finanças. An organization space is governed by the organization, not by a
  personal plan, and is not counted here.

## 2. Decisions

| # | Decision |
|---|---|
| D1 | Only the spaces/people axis is sold, designed to grow. |
| D2 | **The plan belongs to the person** and limits the personal workspaces (`kind: personal`) they own. The default *Pessoal* space is never counted. |
| D3 | Prices: Free R$ 0, Basic R$ 19,90, Pro R$ 49,90, Sob demanda R$ 4,90 per space and R$ 2,90 per person beyond the Free allowance. |
| D4 | Counting: the owner is not counted; pending invitations count; *Leitura* counts the same as *Acesso total*. |
| D5 | Sob demanda has a R$ 0 base that includes the Free allowance. A person is charged **once** across all of the owner's spaces. It is billed on the **highest level of the month**, not the level at close. |
| D6 | **Over the limit, nothing is removed.** A downgrade, cancellation or failed payment blocks only growth (new space, new invitation) until usage is back under the limit. |
| D7 | ctech-account enforces, reading the entitlement **live, without cache**. If billing does not answer, the operation is **refused** ("try again"); existing spaces are never affected. |
| D8 | Level metering is a new, generic billing feature (`aggregation: max`); summing stays the default. |
| D9 | **Free is implicit**: nobody gets a R$ 0 subscription. The plan screen lives in Finanças; payment uses the existing invoice checkout. |

## 3. Catalogue

A new product in `api/tenants/ctech.json`:

```json
{ "id": "prod_finance", "name": "CTech Finanças", "owner_key": "finance",
  "default_price_id": "price_finance_free" }
```

| Price | Type | Amount | Metadata |
|---|---|---|---|
| `price_finance_free` | fixed, monthly, advance | 0 | `plan: free`, `quota_spaces: 1`, `quota_people_per_space: 1` |
| `price_finance_basic_monthly` | fixed, monthly, advance | 1990 | `plan: basic`, `quota_spaces: 3`, `quota_people_per_space: 5` |
| `price_finance_pro_monthly` | fixed, monthly, advance | 4990 | `plan: pro`, `quota_spaces: 10`, `quota_people_per_space: 10` |
| `price_finance_ondemand_spaces` | metered, `aggregation: max`, `meter: finance_spaces`, `included_quantity: 1`, arrears | 490 | `plan: ondemand`, `quota_spaces: -1`, `quota_people_per_space: -1` |
| `price_finance_ondemand_people` | metered, `aggregation: max`, `meter: finance_people`, `included_quantity: 1`, arrears | 290 | `plan: ondemand` |

A Sob demanda subscription has both metered items. `-1` means unlimited, as in the DF-e catalogue.

**`default_price_id`** is the price whose metadata describes a customer with no entitling subscription for the
product. It is catalogue configuration; billing still never reads a quota (ADR 0008). It must name a price of
the same product with `unit_amount: 0`; the seed refuses anything else.

**`included_quantity`** on a metered price is the number of units not charged in each period. It is a pricing
parameter, like a tier boundary, and applies to `sum` and `max` alike. Default 0.

## 4. Entitlements

`GET /v1.0/entitlements?customer_ref=…` gains:

- **`owner_key`** (optional): only subscriptions whose items belong to that owner's products are returned. Without
  it, a `USER_` customer paying for both the DF-e and Finanças would get both mixed.
- **`default`**: when `owner_key` is given, the owner has exactly one product with a `default_price_id`, and no
  returned subscription is entitled, the answer carries `default: {price_id, plan, metadata}`.
- **A missing customer is not a 404 when `owner_key` is given**: the answer is `{entitled: false,
  subscriptions: [], default: …}`. Reading never creates a customer.

Entitled keeps its meaning (`ACTIVE`, `TRIALING`, `PAST_DUE`). Under D6 nothing is ever removed, so the grace
period only changes whether a person may grow.

**The consumer** is ctech-account, through a new M2M credential `account-billing` on tenant `ctech`, with
`entitlements:read` and `usage:write`. The credential is scoped to the `finance` owner: a report or read for
another owner's meter or product is refused (403), the way `dfe-billing` is scoped to `dfe`.

## 5. Enforcement (ctech-account's side)

Only ctech-account writes tenancy, so only ctech-account enforces. The counterpart spec in ctech-account details
it; the contract is:

| Action | Counted | Limit |
|---|---|---|
| Create a `personal` workspace | `personal` workspaces the person owns (Pessoal is not one) | `quota_spaces` |
| Invite to a `personal` workspace | members + pending invitations **in that workspace**, owner excluded | `quota_people_per_space` |
| Accept an invitation | nothing new (already counted when invited) | — |
| Transfer ownership of a `personal` workspace | the new owner's `personal` workspaces + 1 | the **new owner's** `quota_spaces` |

`-1` is unlimited. Sob demanda has no limit; it only reports.

**Order inside one request:** read the entitlement → check → write → report levels (§ 6).
- Billing unreachable or erroring at the read → **503**, *"Não foi possível verificar seu plano agora. Tente em
  instantes."* Nothing written.
- Over the limit → **402** `plan_limit`, with `limit`, `used` and the plan. The handoff shows it with a
  *"Ver planos"* link to `{BILLING}/console/finance/plano`.
- **Concurrency:** the count and the write are guarded by a per-owner conditional counter
  (`PERSONAL_SPACES#{owner}`) for spaces and a per-workspace one for people, in the same transaction as the
  write. Two tabs cannot both create the fourth space.
- Removing a member, a member leaving and revoking an invitation are never refused; they only report the new
  level.

Organizations (`kind: organization`) are not counted and not limited by these plans.

## 6. Level metering

### 6.1 Reporting

`POST /v1.0/usage/levels`

```json
{ "customer_ref": "USER_{sub}", "meter": "finance_spaces", "value": 4,
  "occurred_at": "2026-10-10T14:03:00Z", "idempotency_key": "…" }
```

- Reported on **every change, whatever the plan**: a Free person's level must already be known on the day they
  move to Sob demanda, or they would be billed 0 until their next change.
- The level belongs to the **customer reference**, not to a subscription item. No customer is created by a
  report.
- `value` is the **whole current count**, never a delta. A lost report is repaired by the next one.
- `finance_people` is the number of **distinct** people (members + pending invitations, owner excluded) across all
  of the owner's `personal` workspaces.
- Idempotency as for usage: a conditional write on the key; the same key with a different body is 409.
- ctech-account retries a failed report with the same key, after its own write has committed. Reporting failure
  never undoes the write.

### 6.2 Storage

One item per report: `pk = LEVEL#{customer_ref}#{meter}`, `sk = {occurred_at}#{idempotency_key}`, in the tenant's
livemode partition, with a 13-month TTL. The latest item before any instant is one `Query` with
`ScanIndexForward=false, Limit=1`.

### 6.3 Billing at close

For a metered item whose price has `aggregation: max`:

```
level  = max( last level before the period start  (carried in),
              every level reported inside the period )
billed = max(0, level − included_quantity) × unit_amount
```

- A month with no change bills the carried-in level.
- A peak in the middle of the month is billed.
- A subscription that starts on day 20 has its period start on day 20; the carried-in level covers what existed
  before.
- Periods are civil dates in America/Sao_Paulo (brcal), as for summed usage.

`MaxLevel(records, carriedIn, period)` sits beside `SumUsage` in `internal/domain/billing/usage.go`; the
period-close path picks one by the price's `aggregation`. `sum` (the default, and every existing price) is
unchanged.

**Corrections**: a wrong level is corrected by reporting the right one; nothing is deleted. A period already
invoiced is corrected by a credit note, as today.

## 7. The plan screen (Finanças → Plano)

`/console/finance/plano`, shown in the *Pessoal* space only (the plan is the person's, D2).

- **Usage**: "2 de 3 espaços", and for each personal workspace "4 de 5 pessoas". Read from ctech-account's
  service route that already lists the person's workspaces, extended with member and pending-invitation counts
  per workspace.
- **Plans**: a card per plan, the current one marked, with the subscribe/change button.
- **Subscribe or change**: creates or changes the subscription of customer `USER_{sub}` (created at this moment
  if it does not exist, with the same pointer as the portal) through the same change-plan service the portal
  uses, and redirects to the invoice's `checkout_url`. Upgrade, downgrade and proration follow the existing
  change rules.
- **Over the limit** (D6): a banner — *"Você tem 6 espaços e o plano Basic permite 3. Nada foi removido; para
  criar novos, volte ao limite ou mude de plano."*
- **Sob demanda above Basic**: when the month's projected on-demand amount passes R$ 19,90, a suggestion to switch.
  Never automatic.
- **Phone**: its own layout — usage first, plan cards stacked, one primary action.

## 8. Organization customers

What the DF-e needs (its spec § 1):

- **`ExternalRef: ORG_{organization_id}`** is accepted on customer creation. The customer and the pointer
  `CUSTOMER_ORG#{organization_id}` are written in one conditional transaction, like `CUSTOMER_USER#` (ADR 0025,
  2026-10-07 amendment). Creating again for the same organization returns the existing customer.
- **Name and document on the invoice**: the organization's **billing company** — its first linked company in
  ctech-account — legal name and CNPJ; with no company, the owner's name and CPF. The DF-e sends them on creation.
- **The portal selector** (ADR 0025 amendment, unbuilt until now):
  - lists *Pessoal* and the `kind: organization` workspaces where the person is `owner` or `admin`;
  - the same resolver, single 404 and zero reads before membership;
  - an organization selected → that organization's invoices and subscriptions, through `CUSTOMER_ORG#`;
  - the checkout page of an organization invoice requires the same role.
- **Finanças 6.7 (CTech invoices as expenses)**: only invoices of a `USER_` customer post to the payer's
  *Pessoal*. An organization invoice is nobody's personal expense; posting it to the organization's space is
  out of scope.

## 9. Tests

1. Entitlements with `owner_key=finance` for a customer that also has DF-e subscriptions → only Finanças ones.
2. No customer, or no entitling subscription → `default` with the Free metadata; no customer written.
3. A seed whose `default_price_id` is not R$ 0 or belongs to another product → refused.
4. Levels: a duplicate key is one record; same key with another body → 409; reporting for another owner's meter
   → 403.
5. `MaxLevel`: no reports in the period → carried-in level; peak mid-period → the peak; subscription starting
   mid-month → max from its start, carried-in included; `included_quantity` subtracted, never below 0.
6. A summed price closes exactly as before (regression over the existing close tests).
7. Plan screen: subscribe creates the `USER_` customer once; redirect to `checkout_url`; over-limit banner.
8. `ORG_` customer creation twice → one customer, one pointer; pointer and customer in one transaction.
9. Portal selector: `member` or `viewer` of an organization → 404 on its invoices; a `personal` workspace never
   listed and → 404 if forged.
10. An organization invoice paid → no expense in anyone's *Pessoal*.

## 10. Deploy order

1. **Billing**: catalogue (prices, `default_price_id`), `owner_key` filter and `default`, `usage/levels`,
   `MaxLevel`, `ORG_` customers and the portal selector, the `account-billing` credential. All additive.
   The DF-e catalogue loses `quota_users` and archives `price_dfe_ondemand_user` (DF-e spec O4) in the same
   catalogue change.
2. **ctech-account**: enforcement and level reports (its spec). Before this, nothing is limited — today's
   behaviour.
3. **Billing**: the plan screen, once ctech-account answers counts.
4. **ctech-dfe**: its own spec, after step 1.

## 11. Out of scope

- Limits inside a space (bills, accounts, imports, storage).
- Premium features; annual prices.
- Finanças plans for organizations.
- Posting organization CTech invoices into the organization's Finanças space.
- Deleting a space (shared-spaces spec § 5.4).
