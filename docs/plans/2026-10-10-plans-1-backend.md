# Plans 1 — Catalogue, entitlements, level metering, organization customers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship deploy step 1 of the plans spec: the *CTech Finanças* catalogue (with `default_price_id`, `aggregation: max`, `meter`, `included_quantity`), entitlements filtered by `owner_key` with a `default` and no 404 for a missing customer, `POST /v1.0/usage/levels`, billing on the month's highest level at period close, credentials scoped to one owner (`account-billing` → `finance`, `dfe-billing` → `dfe`), `ORG_{organization_id}` customers with their `CUSTOMER_ORG#` pointer, the portal selector, and the 6.7 rule that only `USER_` invoices post to *Pessoal*. Everything additive; nothing existing changes behaviour except where this plan says so.

**Architecture:** Three new first-class fields on `billing.Price` (metadata stays opaque, ADR 0008) and one on `billing.Product`; a pure `billing.MaxLevel` beside `SumUsage`; a `LevelRepository` in the existing `usage` table (one item per report under `{org}#{mode}#LEVEL#{customer_ref}#{meter}`, plus an idempotency marker written in the same transaction). `Invoicer.buildLine` dispatches on `price.Aggregation`. A credential gains `owner_key`; the M2M handlers check it. Customer creation writes a `CUSTOMER_ORG#` pointer in the customer's transaction when the external ref is `ORG_{canonical uuid}`. `ResolvePortalIdentity` accepts `X-Billing-Space: org:{id}` and resolves it through the same `space.Resolver` the console uses, before any table read.

**Tech Stack:** Go 1.27.2 (`api/go.mod`), Fiber v3, DynamoDB through api-commons v1.14.0 `dynamo.Base`; Next.js 16 static export, React 19, TanStack Query, `@aoctech/ui`, vitest for the one portal UI task.

**Spec:** [`docs/specs/2026-10-10-plans-design.md`](../specs/2026-10-10-plans-design.md) — §§ 3, 4, 6, 8, 9 (tests 1–6, 8–10), 10 step 1, and its "Amendment, 2026-10-10 — planning". Also [ADR 0008](../adr/0008-opaque-metadata.md), [ADR 0025](../adr/0025-spaces-personal-and-organization.md) (2026-10-07 portal amendment, 2026-10-09 "lists `kind = organization` only"), [ADR 0016](../adr/0016-webhook-routing-by-product-owner.md) (owner keys), [`2026-10-09-shared-spaces-design.md`](../specs/2026-10-09-shared-spaces-design.md) § 7. Consumers: `ctech-account/docs/specs/2026-10-10-space-plan-limits.md` (§§ 1, 4, 5), `ctech-dfe/docs/specs/2026-10-10-organization-subscription.md` (§§ 1, 3).

## Scope decisions (read before executing)

1. **`dfe-billing` is not scoped to `dfe` today.** The spec says `account-billing` is scoped "the way `dfe-billing` is scoped to `dfe`", but no such scope exists in code: `billing.APICredential` has no owner, `provision.Credential` has no owner, `ResolveTenant` resolves only organization and mode, and `getEntitlements`/`reportUsage` read every owner's subscriptions. This plan **builds** the scope: `APICredential.OwnerKey`, `credentials[].owner_key` in the tenant plan, and the seed may set it **once** on an existing credential that has none (the same narrow exception 6.7 made for `account_organization_id`; a different stored value is refused). `ctech.json` gives `dfe-billing` `owner_key: "dfe"` and `account-billing` `owner_key: "finance"`. This is not cosmetic: the day a `USER_` customer has a Finanças subscription, an unscoped DF-e entitlement read would see it as *entitled* and grant DF-e on a Finanças plan.
2. **What a scoped credential may not do:** read entitlements for another owner (403 `owner_not_allowed`), report a level for a meter no price of its owner declares (403 `meter_not_allowed`), report usage on another owner's subscription (403 `owner_not_allowed`). Subscription and customer writes are **not** scoped in this plan (`account-billing` holds no such scope at ctech-account; `dfe-billing` creating a Finanças subscription is listed in the report as remaining).
3. **`owner_key` explicit vs implied.** A scoped credential with no `owner_key` query parameter reads its own owner's subscriptions only (never mixed). But the two new behaviours — `default` and "a missing customer is not a 404" — happen **only when the caller sends `owner_key`**. ctech-dfe today calls without it and may rely on the 404 for get-or-create; that answer must not change under it.
4. **`meter`, `aggregation` and `included_quantity` are first-class `Price` fields**, not metadata (ADR 0008: billing reads them). The DF-e prices keep their `metadata.meter` (opaque, read by ctech-dfe); no DF-e price gains the field.
5. **Prices are immutable** (`CatalogRepository` has no metadata update). Removing `quota_users` from the DF-e prices changes the plan file — fresh deployments and the test catalogue — but **existing live rows keep `quota_users`**. That is harmless (ctech-dfe stopped enforcing it, its spec O4 removes the UI), and the honest alternative (new price ids for every DF-e plan) is a DF-e migration, out of scope. Archiving `price_dfe_ondemand_user` *is* applied to existing rows: the seed gains a second narrow exception — a plan price with `"archived": true` archives a stored active one, through `CatalogRepository.ArchivePrice` (audited).
5a. **DF-e on demand: companies billed monthly by peak** (user decision, 2026-10-10). The DF-e company charge moves to level metering. Prices are immutable, so the catalogue **adds** `price_dfe_ondemand_companies_monthly` (product `prod_dfe_ondemand`, metered, `aggregation: max`, `meter: dfe_companies`, `included_quantity: 0`, 500, monthly, arrears; metadata `plan: ondemand`, `quota_companies: -1`) and **archives** `price_dfe_ondemand_company` through the same seed exception. `dfe-billing` (owner `dfe`) reports levels for `dfe_companies` — the owner check of Task 6 already allows it, since that meter's price belongs to a `dfe` product. Existing on-demand subscriptions keep billing the archived summed price until ctech-dfe moves them (`ChangePlan` onto the new set); new ones cannot choose the archived price. The `quota_companies` metadata on the fixed DF-e plans is unchanged. Cross-repo: ctech-dfe must report `dfe_companies` levels (the count of enabled companies of the organization) on every enable/disable, instead of a `companies` usage record, and its plan picker must use the new price id.
6. **`default_price_id` validation** (spec § 3): the named price is declared in the same plan, belongs to the same product, is `fixed`, `unit_amount: 0` and not archived; and at most one product per `owner_key` carries a default (so "the owner has exactly one product with a `default_price_id`" is a seed invariant, and the read still checks it).
7. **Level storage keys.** `pk = {org}#{mode}#LEVEL#{customer_ref}#{meter}`, `sk = {occurred_at}#{idempotency_key}` with `occurred_at` in a **fixed-width** UTC layout (`2006-01-02T15:04:05.000000000Z`): RFC 3339 Nano trims trailing zeros and would sort `…:00.5Z` after `…:00.123Z`. Rows live in the `usage` table (no schema change).
8. **Level idempotency needs a marker.** The key is inside `sk` next to `occurred_at`, so the same key with another `occurred_at` would be a *different* item and a conditional put on it cannot see the reuse. Each report also writes `pk = {org}#{mode}#LEVEL_KEY#{idempotency_key}`, `sk = KEY` holding a hash of `(customer_ref, meter, value, occurred_at)`, **in the same transaction**, both conditional on absence. A failed condition re-reads the marker: same hash → 200 `duplicate: true`; other hash → 409 `idempotency_key_reused`. A `TransactionConflict` (api-commons v1.11+ no longer reads it as a failed condition) → `ErrTransactionConflict` → 409 `concurrent_update`. The route is **not** behind the `Idempotency-Key` header middleware: the body key is the contract ctech-account's spec § 4 sends, and the marker is a stronger guarantee than the header store (which does not serialize).
9. **The 13-month TTL must not drop a level that never changes** (amended 2026-10-10, planning). "A month with no change bills the carried-in level", but a level reported once and never again would expire after 13 months and the next close would bill 0 — for a subscriber and, worse, for a Free person who moves to Sob demanda after a quiet year. So each `(customer_ref, meter)` also has **one latest item with no TTL**, `pk = {org}#{mode}#LEVEL_LATEST#{customer_ref}#{meter}`, `sk = LATEST`, holding the newest level and its instant, written **in the same transaction** as the report that makes it the newest (conditional on the latest still being the one read; a race re-reads and retries, at most 3 times). Each report item also stores `previous`: the level held just before it, as the store knew it. The close's carried-in level is, in order: the newest in-TTL report before the period start; else the `previous` of the first report at or after the start; else the latest item (which is then necessarily before the start); else 0. No checkpoint is written by the close.
10. **Included units apply to `sum` too** (spec § 3), via `billing.BillableUnits` inside `MeteredLine`. Every existing price has `included_quantity` 0, so their lines are unchanged (spec test 6 is the regression over the existing close tests). The line's `Quantity` is the **billed** units.
11. **`ORG_` customers.** The pointer is written only when the external ref is `ORG_` followed by a canonical lower-case UUID (`space.IsOrganizationID`); any other `ORG_…` stays an ordinary external ref, so a third-party merchant already using that prefix is not broken. `user_id` together with an `ORG_{uuid}` ref is 422 (`user_id_not_allowed`): an organization is nobody's portal identity, and the owner's `CUSTOMER_USER#` pointer is already taken by their `USER_` customer. Creating again returns the existing customer with **200** (201 for a new one). Name and document are whatever ctech-dfe sends (its spec: the billing company's legal name and CNPJ, else the owner's); billing does not derive them.
12. **The portal selector** reuses the console's header (`X-Billing-Space`), parser and resolver. Absent or `personal` = today's behaviour. `org:{id}`: resolver first (zero table reads before membership), then the space must be `kind = organization` **and** hold `space.Configure` — which `VerbsFor` grants exactly to `owner` and `admin` — or the answer is the same `space-not-found` 404 byte for byte; ctech-account unreachable → 503. Then the customer is read through `CUSTOMER_ORG#`. A selected organization with no customer is the existing typed 403 `no-billing-account` (the empty state), as for a person. `GET /v1.0/portal/spaces` lists *Pessoal* and the organizations where `VerbsFor(kind, role).Has(Configure)`; it is mounted **before** the portal group so an organization admin who is not themselves a customer can still pick. Every portal handler that used `"user:"+customer.UserID` as the audit actor uses `actorOfUser(c)` (an organization customer has no `UserID`).
13. **"The checkout page of an organization invoice requires the same role"** is read as the portal's pay route (`POST /portal/invoices/:id/pay`) and the portal invoice page, which are now behind the selector. The public signed link `/checkout/:token` stays a bearer capability (ADR: whoever holds the link may pay), unchanged.
14. **6.7:** the payer side skips a customer whose external ref is `ORG_{uuid}` with reason `payer_is_organization`, checked **before** `user_id`.
15. **Out of this plan:** the plan screen and the counts on ctech-account's route (plan 2 — deploy step 3), ctech-account's enforcement (its repo), ctech-dfe's migration (its repo), subscription writes scoped by owner (decision 2), the plan screen's scheduled change to Sob demanda (plan 2).

## Global Constraints

- Go commands from `api/`. Go 1.27.2. `gofmt -l ./internal ./cmd` (no output), `go vet ./...`, `go test ./...`; `make test-integration` with DynamoDB Local (`docker compose -f api/docker-compose.test.yml up -d`).
- UI (Task 12 only): from `ui/`, `npm ci` first (the installed `@aoctech/ui` is 0.3.0, `package.json` asks `^0.4.1`). `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"` — **judge vitest by its exit code**, not by scanning output. `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`. Never run the Go and UI suites at the same time. The UI task is executed with the `/impeccable` skill and uses `@aoctech/ui` components where they exist.
- Catalogue values, verbatim from spec § 3: product `prod_finance`, name `CTech Finanças`, `owner_key: finance`, `default_price_id: price_finance_free`. Prices: `price_finance_free` fixed monthly advance 0 (`plan: free`, `quota_spaces: 1`, `quota_people_per_space: 1`); `price_finance_basic_monthly` fixed monthly advance 1990 (`plan: basic`, `quota_spaces: 3`, `quota_people_per_space: 5`); `price_finance_pro_monthly` fixed monthly advance 4990 (`plan: pro`, `quota_spaces: 10`, `quota_people_per_space: 10`); `price_finance_ondemand_spaces` metered, `aggregation: max`, `meter: finance_spaces`, `included_quantity: 1`, arrears, 490 (`plan: ondemand`, `quota_spaces: -1`, `quota_people_per_space: -1`); `price_finance_ondemand_people` metered, `aggregation: max`, `meter: finance_people`, `included_quantity: 1`, arrears, 290 (`plan: ondemand`, `quota_spaces: -1`, `quota_people_per_space: -1` — amended 2026-10-10 so either on-demand item carries the quotas). `price_dfe_ondemand_companies_monthly`: metered, `aggregation: max`, `meter: dfe_companies`, `included_quantity: 0`, arrears, 500 (`plan: ondemand`, `quota_companies: -1`); `price_dfe_ondemand_company` archived. `-1` means unlimited.
- `billed = max(0, level − included_quantity) × unit_amount`; `level = max(last level before the period start, every level reported inside the period)`; periods are São Paulo civil dates (`brcal`).
- Level TTL: 13 months on each report; the latest level per `(customer_ref, meter)` has no TTL. Usage TTL unchanged (24 months).
- Entitled = `ACTIVE`, `TRIALING`, `PAST_DUE` (unchanged `Subscription.IsEntitled`).
- Billing never reads a quota (ADR 0008): no Go code in this plan reads `metadata["quota_*"]`.
- Errors: `TransactionConflict` reaching a route is 409 `concurrent_update` (`problem.FromError` already maps it); `IsConditionFailed` means ConditionalCheckFailed only.
- Commit messages: Conventional Commits, no attribution trailer of any kind.

## Review Focus

1. **The same `idempotency_key` retried with a different `occurred_at` or `value`** — 409 `idempotency_key_reused`, never a silent second level; the identical retry is 200 `duplicate: true` and one item — Task 5 `TestALevelKeyReusedWithAnotherInstantIsRefused`, Task 6 `TestLevelReportsAreIdempotentByBodyKey`.
2. **ctech-dfe reading entitlements as it does today (no `owner_key`) for a `USER_` customer who also pays Finanças** — only DF-e subscriptions, and a missing customer is still 404 — Task 7 `TestAScopedCredentialWithoutOwnerKeyReadsOnlyItsOwner`, `TestWithoutOwnerKeyAMissingCustomerIsStill404`.
3. **A level that never changes for more than the TTL** — the no-TTL latest item still carries it in, for a subscriber and for a person who subscribes after a quiet year — Task 5 `TestTheLatestLevelOutlivesItsReports`, Task 8 `TestALevelOlderThanItsTTLIsStillCarriedIn`.
4. **`ORG_` refs that are not organization ids, and `user_id` sent with an organization ref** — an ordinary ref (no pointer) and a 422, respectively — Task 9 `TestAnOrgPrefixThatIsNotAnIDIsAnOrdinaryRef`, `TestAnOrganizationCustomerCannotCarryAUser`.
5. **An organization admin who is not personally a customer opens the portal** — the spaces list still answers, and picking the organization shows its invoices; a `member` gets the same 404 as a stranger — Task 11 `TestAnAdminWhoIsNotACustomerCanListAndPick`, `TestAMemberOrViewerGetsTheSame404`.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/domain/billing/catalog.go` (modify) | `Aggregation`, `Price.Meter/Aggregation/IncludedQuantity`, `Product.DefaultPriceID`, `ValidMeter`, validation |
| `api/internal/domain/billing/invoice_lines.go` (modify) | `BillableUnits`; `MeteredLine` subtracts included units |
| `api/internal/domain/billing/levels.go` (new), `levels_test.go` (new), `price_metering_test.go` (new) | `LevelRecord`, `MaxLevel` |
| `api/internal/domain/billing/credential.go` (modify) | `APICredential.OwnerKey`, `Allows` |
| `api/internal/repositories/credentials.go` (modify) | `SetOwnerKey`, `ErrCredentialOwnerSet` |
| `api/internal/provision/plan.go`, `apply.go`, `plan_test.go` (modify) | credential owner, product default, price metering fields, archive |
| `api/tenants/ctech.json` (modify) | `prod_finance`, its prices, `account-billing`, owners on credentials, DF-e `quota_users` removed, `price_dfe_ondemand_user` archived |
| `api/internal/repositories/retention.go`, `retention_test.go` (modify) | `RetentionThirteenMonths`, `RetentionLevel` |
| `api/internal/repositories/keys.go`, `keys_test.go` (modify) | `LevelPK`, `LevelSK`, `LevelKeyPK`, `levelBound`, `CustomerOrgSK`, `OrganizationOfRef` |
| `api/internal/repositories/levels.go` (new) | `LevelRepository`: `Append` (report + key marker + latest), `Latest`, `LatestBefore`, `FirstFrom`, `InPeriod` |
| `api/internal/api/v1/levels.go` (new), `dto.go`, `handlers.go`, `router.go` (modify) | `POST /v1.0/usage/levels`, owner checks, entitlements `owner_key`/`default` |
| `api/internal/services/invoicing.go` (modify) | `WithLevels`, `buildLine` by aggregation, carried-in level with the no-TTL fallback; `endAtBoundary` (Task 8b) |
| `api/internal/app/app.go` (modify) | wiring (server, sweep) |
| `api/internal/repositories/customers.go`, `rows.go` (modify) | `CUSTOMER_ORG#` pointer, `GetByOrganization`, `ErrOrganizationAlreadyCustomer` |
| `api/internal/services/finance_invoices.go`, `finance_invoices_test.go` (modify) | `payer_is_organization` |
| `api/internal/middleware/portal.go`, `portal_test.go` (new) | portal selector |
| `api/internal/api/v1/portal.go`, `portal_spaces.go` (new) | `GET /portal/spaces`, actor fixes |
| `api/tests/integration/levels_test.go`, `entitlements_owner_test.go`, `max_close_test.go`, `org_customers_test.go`, `portal_spaces_test.go` (new) | DynamoDB Local tests |
| `ui/src/lib/portal/space.ts` (new), `ui/src/lib/api/portal.ts`, `ui/src/components/portal/PortalSpaceSwitch.tsx` (new), `ui/src/app/(portal)/layout.tsx`, locales (modify) | the portal selector in the UI |
| `PLAN.md`, `docs/specs/2026-10-10-plans-design.md`, `docs/adr/0025-spaces-personal-and-organization.md` (modify) | records |

---

### Task 1: Metering fields on the price, default on the product, included units on the line

**Files:**
- Modify: `api/internal/domain/billing/catalog.go`, `api/internal/domain/billing/invoice_lines.go`
- Test: `api/internal/domain/billing/price_metering_test.go` (new)

**Interfaces:**
- Produces: `type billing.Aggregation string`; `billing.AggregationSum = "sum"`, `billing.AggregationMax = "max"`; `Price.Aggregation Aggregation`, `Price.Meter string`, `Price.IncludedQuantity int64` (dynamo/json `aggregation`, `meter`, `included_quantity`, all omitempty); `Product.DefaultPriceID string` (`default_price_id`, omitempty); `billing.ValidMeter(string) bool`; `billing.BillableUnits(units, included int64) int64`; `MeteredLine` now bills `BillableUnits(units, p.IncludedQuantity)`.

- [ ] **Step 1: Write the failing tests** — `price_metering_test.go`:

```go
package billing

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func meteredPrice() *Price {
	return &Price{
		ID: "p", Type: PriceMetered, Currency: CurrencyBRL, UnitAmount: 490,
		Recurrence: Recurrence{Interval: IntervalMonth, Count: 1}, Timing: BillArrears,
	}
}

func TestAMaxPriceNamesItsMeter(t *testing.T) {
	p := meteredPrice()
	p.Aggregation = AggregationMax
	if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("max without a meter: %v", err)
	}
	p.Meter = "finance_spaces"
	p.IncludedQuantity = 1
	if err := p.Validate(); err != nil {
		t.Fatalf("a valid max price: %v", err)
	}
}

func TestMeteringFieldsBelongToMeteredPrices(t *testing.T) {
	for name, mut := range map[string]func(*Price){
		"aggregation": func(p *Price) { p.Aggregation = AggregationSum },
		"meter":       func(p *Price) { p.Meter = "x" },
		"included":    func(p *Price) { p.IncludedQuantity = 1 },
	} {
		p := &Price{ID: "p", Type: PriceFixed, Currency: CurrencyBRL, UnitAmount: 1990,
			Recurrence: Recurrence{Interval: IntervalMonth, Count: 1}, Timing: BillAdvance}
		mut(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
			t.Errorf("%s on a fixed price: %v", name, err)
		}
	}
}

func TestMeteringFieldsAreChecked(t *testing.T) {
	for name, mut := range map[string]func(*Price){
		"unknown aggregation": func(p *Price) { p.Aggregation = "avg" },
		"negative included":   func(p *Price) { p.IncludedQuantity = -1 },
		"meter with a hash":   func(p *Price) { p.Meter = "a#b" },
		"meter upper case":    func(p *Price) { p.Meter = "Spaces" },
	} {
		p := meteredPrice()
		mut(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIncludedUnitsAreNotCharged(t *testing.T) {
	p := meteredPrice()
	p.IncludedQuantity = 1
	period := Period{Start: brcal.New(2026, time.October, 1), End: brcal.New(2026, time.November, 1)}
	for units, want := range map[int64]Cents{0: 0, 1: 0, 4: 3 * 490} {
		line := MeteredLine(p, "Espaços", period, units)
		if line.Amount != want || line.Quantity != int64(want/490) {
			t.Errorf("units %d: line = %+v, want amount %d", units, line, want)
		}
	}
}

// Spec test 6: every existing price has no included units, so its line is the
// one it always was.
func TestAPriceWithoutIncludedUnitsBillsEveryUnit(t *testing.T) {
	p := meteredPrice()
	line := MeteredLine(p, "NF-e", Period{}, 7)
	if line.Quantity != 7 || line.Amount != 7*490 {
		t.Fatalf("line = %+v", line)
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `cd api && go test ./internal/domain/billing/ -run 'Max|Metering|Included' -v` → FAIL (`p.Aggregation undefined`).

- [ ] **Step 3: Implement.** In `catalog.go`, after the `PriceMetered` const block:

```go
// Aggregation is how a metered price turns a period's reports into units.
type Aggregation string

const (
	// AggregationSum adds every usage record of the period. The default: an
	// empty Aggregation is sum, which is what every price before plans was.
	AggregationSum Aggregation = "sum"
	// AggregationMax bills the highest level reached in the period, the level
	// carried in from before it included (spec § 6.3). Levels belong to the
	// customer reference and the meter, not to a subscription item.
	AggregationMax Aggregation = "max"
)

// ValidMeter accepts a meter name: lower-case letters, digits and "_", starting
// with a letter, at most 40 characters. It becomes part of a partition key, so
// it must not be able to carry "#".
func ValidMeter(s string) bool {
	if len(s) == 0 || len(s) > 40 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
```

On `Product`, after `DunningPolicy`:

```go
	// DefaultPriceID names the price whose metadata describes a customer with no
	// entitling subscription for this product (spec § 3). Catalogue
	// configuration: billing carries that metadata, it never reads it (ADR 0008).
	// The tenant plan refuses one that is not a free fixed price of this product.
	DefaultPriceID string `dynamodbav:"default_price_id,omitempty" json:"default_price_id,omitempty"`
```

On `Price`, after `Timing`:

```go
	// Aggregation, Meter and IncludedQuantity are pricing parameters of a
	// metered price, first-class because billing reads them (unlike metadata,
	// ADR 0008). Empty Aggregation is sum. Meter is required for max: it is the
	// name levels are reported under. IncludedQuantity units are not charged in
	// each period, for sum and max alike.
	Aggregation      Aggregation `dynamodbav:"aggregation,omitempty"       json:"aggregation,omitempty"`
	Meter            string      `dynamodbav:"meter,omitempty"             json:"meter,omitempty"`
	IncludedQuantity int64       `dynamodbav:"included_quantity,omitempty" json:"included_quantity,omitempty"`
```

In `Price.Validate`, before the metadata check:

```go
	switch p.Aggregation {
	case "", AggregationSum, AggregationMax:
	default:
		return fmt.Errorf("%w: unknown aggregation %q", ErrInvalidPrice, p.Aggregation)
	}
	if p.Type != PriceMetered && (p.Aggregation != "" || p.Meter != "" || p.IncludedQuantity != 0) {
		return fmt.Errorf("%w: only a metered price aggregates, names a meter or includes units", ErrInvalidPrice)
	}
	if p.Aggregation == AggregationMax && p.Meter == "" {
		return fmt.Errorf("%w: a max price names the meter its levels are reported under", ErrInvalidPrice)
	}
	if p.Meter != "" && !ValidMeter(p.Meter) {
		return fmt.Errorf("%w: meter %q is not a meter name", ErrInvalidPrice, p.Meter)
	}
	if p.IncludedQuantity < 0 {
		return fmt.Errorf("%w: included quantity %d is negative", ErrInvalidPrice, p.IncludedQuantity)
	}
```

In `invoice_lines.go`, replace `MeteredLine`:

```go
// BillableUnits is what is charged of a period's units: those beyond the
// price's included quantity, never below zero.
func BillableUnits(units, included int64) int64 {
	if units <= included {
		return 0
	}
	return units - included
}

// MeteredLine builds the line for a closed metered period. Quantity is the
// billed units: the included ones are not on the bill.
func MeteredLine(p *Price, productName string, period Period, units int64) InvoiceItem {
	billed := BillableUnits(units, p.IncludedQuantity)
	return InvoiceItem{
		Description: productName,
		PriceID:     p.ID,
		Period:      period,
		Quantity:    billed,
		UnitAmount:  p.UnitAmount,
		Amount:      p.UnitAmount * Cents(billed),
	}
}
```

- [ ] **Step 4: Run** — `go test ./internal/domain/billing/ -v` → PASS (the existing `SumUsage` and line tests included).
- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/billing/catalog.go api/internal/domain/billing/invoice_lines.go api/internal/domain/billing/price_metering_test.go
git commit -m "feat(billing): metered prices aggregate by sum or max and include units"
```

---

### Task 2: `MaxLevel`

**Files:**
- Create: `api/internal/domain/billing/levels.go`, `api/internal/domain/billing/levels_test.go`

**Interfaces:**
- Consumes: `billing.Period`, `brcal`.
- Produces:

```go
type LevelRecord struct {
	OrganizationID string
	Livemode       bool
	CustomerRef    string
	Meter          string
	Value          int64
	OccurredAt     time.Time
	IdempotencyKey string
	Previous       int64 // the level held just before this report, as the store knew it (Task 5)
}
func (l *LevelRecord) Validate() error          // ErrInvalidUsage on a bad record
func (l *LevelRecord) Date() brcal.Date
func MaxLevel(records []LevelRecord, carriedIn int64, period Period) int64
```

- [ ] **Step 1: Write the failing tests** — `levels_test.go` (spec § 9 test 5):

```go
package billing

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func lvl(v int64, y int, m time.Month, d, h int) LevelRecord {
	return LevelRecord{Value: v, OccurredAt: time.Date(y, m, d, h, 0, 0, 0, time.UTC)}
}

var october = Period{Start: brcal.New(2026, time.October, 1), End: brcal.New(2026, time.November, 1)}

func TestNoReportInThePeriodBillsTheCarriedInLevel(t *testing.T) {
	if got := MaxLevel(nil, 4, october); got != 4 {
		t.Fatalf("MaxLevel = %d, want 4", got)
	}
}

func TestAPeakInTheMiddleIsBilled(t *testing.T) {
	recs := []LevelRecord{lvl(5, 2026, time.October, 10, 15), lvl(2, 2026, time.October, 20, 15)}
	if got := MaxLevel(recs, 3, october); got != 5 {
		t.Fatalf("MaxLevel = %d, want 5", got)
	}
}

func TestADropBelowTheCarriedInLevelStillBillsTheCarriedIn(t *testing.T) {
	if got := MaxLevel([]LevelRecord{lvl(1, 2026, time.October, 2, 15)}, 3, october); got != 3 {
		t.Fatalf("MaxLevel = %d, want 3 (the level held on day 1)", got)
	}
}

// A subscription anchored on the 20th: its period starts on the 20th and what
// existed before is the carried-in level; a report on the 19th is outside.
func TestASubscriptionStartingMidMonthCountsFromItsStart(t *testing.T) {
	p := Period{Start: brcal.New(2026, time.October, 20), End: brcal.New(2026, time.November, 20)}
	recs := []LevelRecord{lvl(9, 2026, time.October, 19, 15), lvl(2, 2026, time.October, 25, 15)}
	if got := MaxLevel(recs, 1, p); got != 2 {
		t.Fatalf("MaxLevel = %d, want 2", got)
	}
}

// 02:30 UTC on 1 November is still 31 October in São Paulo.
func TestTheBoundaryIsTheSaoPauloDay(t *testing.T) {
	late := LevelRecord{Value: 8, OccurredAt: time.Date(2026, time.November, 1, 2, 30, 0, 0, time.UTC)}
	if got := MaxLevel([]LevelRecord{late}, 0, october); got != 8 {
		t.Fatalf("MaxLevel = %d, want 8", got)
	}
}

func TestIncludedQuantityIsSubtractedNeverBelowZero(t *testing.T) {
	if BillableUnits(MaxLevel(nil, 0, october), 1) != 0 || BillableUnits(MaxLevel(nil, 3, october), 1) != 2 {
		t.Fatal("included units")
	}
}

func TestALevelRecordIsValidated(t *testing.T) {
	ok := LevelRecord{CustomerRef: "USER_x", Meter: "finance_spaces", Value: 0, OccurredAt: time.Now(), IdempotencyKey: "k"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*LevelRecord){
		"no ref":    func(l *LevelRecord) { l.CustomerRef = "" },
		"bad meter": func(l *LevelRecord) { l.Meter = "a#b" },
		"negative":  func(l *LevelRecord) { l.Value = -1 },
		"no time":   func(l *LevelRecord) { l.OccurredAt = time.Time{} },
		"no key":    func(l *LevelRecord) { l.IdempotencyKey = "" },
	} {
		l := ok
		mut(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/domain/billing/ -run 'Level|Carried|Peak|MidMonth|Boundary|Included' -v` → FAIL (`undefined: MaxLevel`).

- [ ] **Step 3: Implement** — `levels.go`:

```go
package billing

import (
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// LevelRecord is one report of a level: the whole current count of something a
// customer has (spaces, people), never a delta (spec § 6.1). It belongs to the
// customer reference and the meter, not to a subscription item, so it is known
// before the customer subscribes to a plan that bills it.
type LevelRecord struct {
	OrganizationID string    `dynamodbav:"organization_id" json:"-"`
	Livemode       bool      `dynamodbav:"livemode"        json:"-"`
	CustomerRef    string    `dynamodbav:"customer_ref"    json:"customer_ref"`
	Meter          string    `dynamodbav:"meter"           json:"meter"`
	Value          int64     `dynamodbav:"value"           json:"value"`
	OccurredAt     time.Time `dynamodbav:"occurred_at"     json:"occurred_at"`
	IdempotencyKey string    `dynamodbav:"idempotency_key" json:"idempotency_key"`
	// Previous is the level held just before this one, as the store knew it
	// when the report was written. Filled by the repository, never by a caller:
	// it is how the close recovers a carried-in level whose own report expired.
	Previous int64 `dynamodbav:"previous" json:"-"`
}

// Validate checks the record can be stored. A reference with "#" would reach
// into another key, so it is refused like a bad meter.
func (l *LevelRecord) Validate() error {
	switch {
	case l.CustomerRef == "" || strings.Contains(l.CustomerRef, "#"):
		return fmt.Errorf("%w: customer_ref is required and may not contain '#'", ErrInvalidUsage)
	case !ValidMeter(l.Meter):
		return fmt.Errorf("%w: meter %q is not a meter name", ErrInvalidUsage, l.Meter)
	case l.Value < 0:
		return fmt.Errorf("%w: level %d is negative", ErrInvalidUsage, l.Value)
	case l.OccurredAt.IsZero():
		return fmt.Errorf("%w: missing occurred_at", ErrInvalidUsage)
	case l.IdempotencyKey == "":
		return fmt.Errorf("%w: missing idempotency key", ErrInvalidUsage)
	}
	return nil
}

// Date is the São Paulo civil date the level was reached on.
func (l *LevelRecord) Date() brcal.Date { return brcal.FromTime(l.OccurredAt) }

// MaxLevel is the highest level held during period: the level carried in from
// before its start, or any level reported inside it (spec § 6.3). A month with
// no change bills the carried-in level; a peak mid-month is billed even if the
// level dropped again. Records outside the period are ignored.
func MaxLevel(records []LevelRecord, carriedIn int64, period Period) int64 {
	level := carriedIn
	for i := range records {
		if period.Contains(records[i].Date()) && records[i].Value > level {
			level = records[i].Value
		}
	}
	return level
}
```

- [ ] **Step 4: Run** — `go test ./internal/domain/billing/ -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/billing/levels.go api/internal/domain/billing/levels_test.go
git commit -m "feat(billing): MaxLevel bills the highest level of a period"
```

---

### Task 3: Credentials scoped to an owner; the tenant plan learns owners, defaults, metering and archiving

**Files:**
- Modify: `api/internal/domain/billing/credential.go`, `api/internal/repositories/credentials.go`, `api/internal/provision/plan.go`, `api/internal/provision/apply.go`, `api/internal/provision/plan_test.go`

**Interfaces:**
- Consumes: Task 1 fields.
- Produces: `APICredential.OwnerKey string` (`owner_key`, omitempty); `func (c *APICredential) Allows(ownerKey string) bool`; `func (r *CredentialRepository) SetOwnerKey(ctx, cred *billing.APICredential, ownerKey string, now time.Time) error`; `repositories.ErrCredentialOwnerSet`; plan JSON: `credentials[].owner_key`, `products[].default_price_id`, `prices[].aggregation`, `prices[].meter`, `prices[].included_quantity`, `prices[].archived`.

- [ ] **Step 1: Write the failing tests** — append to `plan_test.go` (spec § 9 test 3):

```go
func TestParseRejectsABadDefaultPrice(t *testing.T) {
	base := func(products, prices string) string {
		return `{"organization":{"id":"o","display_name":"O"},"products":[` + products + `],"prices":[` + prices + `]}`
	}
	free := `{"id":"free","product_id":"fin","type":"fixed","unit_amount":0,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	paid := `{"id":"paid","product_id":"fin","type":"fixed","unit_amount":1990,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	other := `{"id":"oth","product_id":"dfe","type":"fixed","unit_amount":0,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"}`
	cases := map[string]string{
		"not free":           base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"paid"}`, paid),
		"another product":    base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"oth"},{"id":"dfe","name":"D","owner_key":"dfe"}`, other),
		"not declared":       base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"ghost"}`, free),
		"two for one owner":  base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"},{"id":"fin2","name":"G","owner_key":"finance","default_price_id":"free2"}`, free+`,`+strings.Replace(free, `"free","product_id":"fin"`, `"free2","product_id":"fin2"`, 1)),
		"archived default":   base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"}`, strings.Replace(free, `"billing_timing"`, `"archived":true,"billing_timing"`, 1)),
	}
	for name, doc := range cases {
		if _, err := Parse(strings.NewReader(doc)); err == nil || !strings.Contains(err.Error(), "default_price_id") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	ok := base(`{"id":"fin","name":"F","owner_key":"finance","default_price_id":"free"}`, free+","+paid)
	if _, err := Parse(strings.NewReader(ok)); err != nil {
		t.Fatalf("a valid default: %v", err)
	}
}

func TestACredentialOwnerMustBeAnOwnerOfThePlan(t *testing.T) {
	doc := `{"organization":{"id":"o","display_name":"O"},"credentials":[{"client_id":"c","owner_key":"finnace"}],
	         "products":[{"id":"fin","name":"F","owner_key":"finance"}]}`
	if _, err := Parse(strings.NewReader(doc)); err == nil || !strings.Contains(err.Error(), "owner_key") {
		t.Fatalf("a typo in a credential's owner must be refused, err = %v", err)
	}
}

func TestPriceMeteringFieldsReachTheDomain(t *testing.T) {
	doc := `{"organization":{"id":"o","display_name":"O"},"products":[{"id":"fin","name":"F"}],
	  "prices":[{"id":"sp","product_id":"fin","type":"metered","unit_amount":490,"aggregation":"max","meter":"finance_spaces",
	    "included_quantity":1,"recurrence":{"interval":"month","count":1},"billing_timing":"arrears"}]}`
	p, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	e := p.Prices[0].entity("o", true)
	if e.Aggregation != billing.AggregationMax || e.Meter != "finance_spaces" || e.IncludedQuantity != 1 {
		t.Fatalf("entity = %+v", e)
	}
}
```

Add `"gopkg.aoctech.app/billing/api/internal/domain/billing"` to the test imports. Replace `TestTenantZeroPlanLinksCTechsAccountOrganization`'s body after the existing check with the catalogue assertions in Task 4 (that task changes the file; this one only adds the tests above).

- [ ] **Step 2: Run** — `go test ./internal/provision/ -v` → FAIL (`unknown field "default_price_id"`, `entity.Aggregation undefined` once compiled).

- [ ] **Step 3: Implement.**

`credential.go` — on `APICredential`, after `Description`:

```go
	// OwnerKey scopes the credential to one service's products (ADR 0016's
	// owner): entitlements, levels and usage of other owners are refused.
	// Empty is an ordinary merchant's credential, which acts for the whole
	// tenant. Set by the tenant plan, never by a route.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"owner_key,omitempty"`
```

and:

```go
// Allows reports whether this credential may act for ownerKey's products.
func (c *APICredential) Allows(ownerKey string) bool {
	return c.OwnerKey == "" || c.OwnerKey == ownerKey
}
```

`credentials.go`:

```go
// ErrCredentialOwnerSet reports a credential that already has an owner.
var ErrCredentialOwnerSet = errors.New("credential already has an owner")

// SetOwnerKey scopes an unscoped credential to one owner, once. Changing an
// owner is not a seed operation: it changes what an integration can read.
func (r *CredentialRepository) SetOwnerKey(ctx context.Context, cred *billing.APICredential, ownerKey string, now time.Time) error {
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(cred.OrganizationID, cred.Livemode), new(CredentialSK(cred.ClientID)),
		"SET owner_key = :o, updated_at = :u",
		"attribute_exists(pk) AND attribute_not_exists(owner_key)",
		nil,
		map[string]types.AttributeValue{
			":o": &types.AttributeValueMemberS{Value: ownerKey},
			":u": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		})
	err := r.base.TransactWrite(ctx, txItems(update))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrCredentialOwnerSet, cred.ClientID)
	}
	if err != nil {
		return conflictErr(err)
	}
	cred.OwnerKey = ownerKey
	return nil
}
```

(imports: `errors`, `github.com/aws/aws-sdk-go-v2/service/dynamodb/types`.)

`plan.go` — fields:

```go
type Credential struct {
	ClientID    string `json:"client_id"`
	Description string `json:"description,omitempty"`
	// OwnerKey scopes the credential to one owner's products (owner_key of a
	// product in this plan). An existing credential with none gets it once.
	OwnerKey string `json:"owner_key,omitempty"`
}
```

On `Product`: `DefaultPriceID string \`json:"default_price_id,omitempty"\``. On `Price`, after `Timing`:

```go
	Aggregation      billing.Aggregation `json:"aggregation,omitempty"`
	Meter            string              `json:"meter,omitempty"`
	IncludedQuantity int64               `json:"included_quantity,omitempty"`
	// Archived archives the price: on creation, or — the one update Apply makes
	// to a price — on an existing active one. Never the reverse.
	Archived bool `json:"archived,omitempty"`
```

In `Validate`, collect owners while reading products (`owners := map[string]bool{}`; `if prod.OwnerKey != "" { owners[prod.OwnerKey] = true }`), move the credentials loop **after** the products loop, and add:

```go
	for _, c := range p.Credentials {
		if c.OwnerKey != "" && !owners[c.OwnerKey] {
			return fmt.Errorf("credential %q: owner_key %q is not the owner_key of any product in this plan", c.ClientID, c.OwnerKey)
		}
	}
```

After the prices loop:

```go
	byID := map[string]Price{}
	for _, pr := range p.Prices {
		byID[pr.ID] = pr
	}
	defaults := map[string]string{} // owner -> product
	for _, prod := range p.Products {
		if prod.DefaultPriceID == "" {
			continue
		}
		pr, ok := byID[prod.DefaultPriceID]
		switch {
		case !ok:
			return fmt.Errorf("product %q: default_price_id %q is not declared in this plan", prod.ID, prod.DefaultPriceID)
		case pr.ProductID != prod.ID:
			return fmt.Errorf("product %q: default_price_id %q belongs to product %q", prod.ID, pr.ID, pr.ProductID)
		case pr.Type != billing.PriceFixed || pr.UnitAmount != 0 || pr.Archived:
			return fmt.Errorf("product %q: default_price_id %q must be an active fixed price of 0", prod.ID, pr.ID)
		}
		if other, dup := defaults[prod.OwnerKey]; dup {
			return fmt.Errorf("product %q: owner %q already has a default_price_id on product %q", prod.ID, prod.OwnerKey, other)
		}
		defaults[prod.OwnerKey] = prod.ID
	}
```

In `Price.entity`, add `Aggregation: p.Aggregation, Meter: p.Meter, IncludedQuantity: p.IncludedQuantity, Archived: p.Archived`; in `Product.entity`, `DefaultPriceID: p.DefaultPriceID`.

`apply.go` — credential loop, `case err == nil:` becomes:

```go
		case err == nil:
			if existing.OrganizationID != orgID {
				return nil, fmt.Errorf("credential %s is already admitted to organization %s, not %s",
					c.ClientID, existing.OrganizationID, orgID)
			}
			switch {
			case c.OwnerKey == "" || existing.OwnerKey == c.OwnerKey:
				res.skipped("credential", c.ClientID)
			case existing.OwnerKey == "":
				// The one change a seed makes to a credential: scoping an
				// unscoped one, once (scope decision 1).
				if err := repos.Credentials.SetOwnerKey(ctx, existing, c.OwnerKey, now); err != nil {
					return res, fmt.Errorf("scoping credential %s: %w", c.ClientID, err)
				}
				res.created("credential owner", c.ClientID)
			default:
				return nil, fmt.Errorf("credential %s is scoped to %q, plan says %q — re-scope deliberately, not through a seed",
					c.ClientID, existing.OwnerKey, c.OwnerKey)
			}
			continue
```

and the new credential gets `OwnerKey: c.OwnerKey`. Price loop, `case err == nil:` becomes:

```go
		case err == nil:
			if p.Archived && !existing.Archived {
				if err := repos.Catalog.ArchivePrice(ctx, existing, provisionActor, "", now); err != nil {
					return res, fmt.Errorf("archiving price %s: %w", p.ID, err)
				}
				res.created("price archive", p.ID)
				continue
			}
			res.skipped("price", p.ID)
			continue
```

(rename the discarded `_` to `existing` in that `switch`.)

- [ ] **Step 4: Run** — `go test ./internal/provision/ ./internal/domain/... -v` → PASS.
- [ ] **Step 5: Integration test for Apply** — append to `api/tests/integration/repositories_test.go` (the `provision.Repos` literal is what `app.BuildProvisioner` builds; imports `provision`, `strings`):

```go
func TestApplyScopesAnUnscopedCredentialOnceAndArchivesAPrice(t *testing.T) {
	ctx := ctxT(t)
	repos := provision.Repos{
		Organizations: repositories.NewOrganizationRepository(testDB, testCfg),
		Credentials:   repositories.NewCredentialRepository(testDB, testCfg),
		Catalog:       repositories.NewCatalogRepository(testDB, testCfg),
		Webhooks:      repositories.NewWebhookRepository(testDB, testCfg),
	}
	orgID, client := "org_"+id.New(), "cli_"+id.New()
	doc := func(owner string, archived bool) *provision.Plan {
		ownerField, archivedField := "", ""
		if owner != "" {
			ownerField = `,"owner_key":"` + owner + `"`
		}
		if archived {
			archivedField = `,"archived":true`
		}
		raw := `{"organization":{"id":"` + orgID + `","display_name":"T"},
		  "credentials":[{"client_id":"` + client + `"` + ownerField + `}],
		  "products":[{"id":"p","name":"P","owner_key":"dfe"},{"id":"q","name":"Q","owner_key":"finance"}],
		  "prices":[{"id":"x","product_id":"p","type":"fixed","unit_amount":10,"recurrence":{"interval":"month","count":1},"billing_timing":"advance"` +
			archivedField + `}]}`
		p, err := provision.Parse(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := provision.Apply(ctx, repos, doc("", false), true, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := provision.Apply(ctx, repos, doc("dfe", true), true, now()); err != nil {
		t.Fatal(err)
	}
	cred, err := repos.Credentials.Resolve(ctx, client)
	if err != nil || cred.OwnerKey != "dfe" {
		t.Fatalf("credential = %+v, %v", cred, err)
	}
	price, _ := repos.Catalog.GetPrice(ctx, orgID, true, "x")
	if !price.Archived {
		t.Fatal("the plan archived the price")
	}
	if _, err := provision.Apply(ctx, repos, doc("finance", true), true, now()); err == nil {
		t.Fatal("re-scoping through a seed must be refused")
	}
}
```

Run `make test-integration` (only this test: `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run TestApplyScopes -v`) → PASS.

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/billing/credential.go api/internal/repositories/credentials.go api/internal/provision api/tests/integration/repositories_test.go
git commit -m "feat(provision): credentials scoped to an owner, product defaults, metering fields and archiving"
```

---

### Task 4: The catalogue

**Files:**
- Modify: `api/tenants/ctech.json`, `api/internal/provision/plan_test.go`

**Interfaces:** Consumes Task 3's plan fields. Produces the catalogue every later task (and ctech-account) relies on.

- [ ] **Step 1: Write the failing test** — extend `TestTenantZeroPlanLinksCTechsAccountOrganization` (after the organization check):

```go
	creds := map[string]string{}
	for _, c := range p.Credentials {
		creds[c.ClientID] = c.OwnerKey
	}
	if creds["dfe-billing"] != "dfe" || creds["account-billing"] != "finance" {
		t.Fatalf("credentials = %v", creds)
	}
	var fin *Product
	for i := range p.Products {
		if p.Products[i].ID == "prod_finance" {
			fin = &p.Products[i]
		}
	}
	if fin == nil || fin.Name != "CTech Finanças" || fin.OwnerKey != "finance" || fin.DefaultPriceID != "price_finance_free" {
		t.Fatalf("prod_finance = %+v", fin)
	}
	prices := map[string]Price{}
	for _, pr := range p.Prices {
		prices[pr.ID] = pr
		if _, has := pr.Metadata["quota_users"]; has {
			t.Errorf("price %s still carries quota_users (DF-e spec O4)", pr.ID)
		}
	}
	for _, archived := range []string{"price_dfe_ondemand_user", "price_dfe_ondemand_company"} {
		if !prices[archived].Archived {
			t.Errorf("%s must be archived", archived)
		}
	}
	comp := prices["price_dfe_ondemand_companies_monthly"]
	if comp.ProductID != "prod_dfe_ondemand" || comp.Type != billing.PriceMetered || comp.Aggregation != billing.AggregationMax ||
		comp.Meter != "dfe_companies" || comp.IncludedQuantity != 0 || comp.UnitAmount != 500 || comp.Timing != billing.BillArrears ||
		comp.Metadata["quota_companies"] != "-1" || comp.Metadata["plan"] != "ondemand" {
		t.Errorf("price_dfe_ondemand_companies_monthly = %+v", comp)
	}
	// The fixed DF-e plans keep their company quotas.
	if prices["price_dfe_pro_monthly"].Metadata["quota_companies"] != "10" || prices["price_dfe_free_monthly"].Metadata["quota_companies"] != "1" {
		t.Error("fixed DF-e company quotas changed")
	}
	want := map[string]struct {
		amount   billing.Cents
		typ      billing.PriceType
		meter    string
		included int64
		plan     string
	}{
		"price_finance_free":            {0, billing.PriceFixed, "", 0, "free"},
		"price_finance_basic_monthly":   {1990, billing.PriceFixed, "", 0, "basic"},
		"price_finance_pro_monthly":     {4990, billing.PriceFixed, "", 0, "pro"},
		"price_finance_ondemand_spaces": {490, billing.PriceMetered, "finance_spaces", 1, "ondemand"},
		"price_finance_ondemand_people": {290, billing.PriceMetered, "finance_people", 1, "ondemand"},
	}
	for id, w := range want {
		g, ok := prices[id]
		if !ok || g.ProductID != "prod_finance" || g.UnitAmount != w.amount || g.Type != w.typ || g.Meter != w.meter ||
			g.IncludedQuantity != w.included || g.Metadata["plan"] != w.plan {
			t.Errorf("%s = %+v", id, g)
		}
		if w.typ == billing.PriceMetered && (g.Aggregation != billing.AggregationMax || g.Timing != billing.BillArrears) {
			t.Errorf("%s: aggregation %q timing %q", id, g.Aggregation, g.Timing)
		}
	}
	quotas := map[string][2]string{
		"price_finance_free": {"1", "1"}, "price_finance_basic_monthly": {"3", "5"},
		"price_finance_pro_monthly": {"10", "10"}, "price_finance_ondemand_spaces": {"-1", "-1"},
		"price_finance_ondemand_people": {"-1", "-1"},
	}
	for id, q := range quotas {
		md := prices[id].Metadata
		if md["quota_spaces"] != q[0] || md["quota_people_per_space"] != q[1] {
			t.Errorf("%s quotas = %v", id, md)
		}
	}
```

- [ ] **Step 2: Run** — `go test ./internal/provision/ -run TestTenantZero -v` → FAIL.

- [ ] **Step 3: Edit `ctech.json`.** `credentials`:

```json
  "credentials": [
    {
      "client_id": "dfe-billing",
      "description": "CTech DF-e — creates subscriptions and reports usage for DF-e plans",
      "owner_key": "dfe"
    },
    {
      "client_id": "account-billing",
      "description": "ctech-account — reads Finanças entitlements and reports space and people levels",
      "owner_key": "finance"
    }
  ],
```

Append to `products`:

```json
    {
      "id": "prod_finance",
      "name": "CTech Finanças",
      "owner_key": "finance",
      "default_price_id": "price_finance_free"
    }
```

Append to `prices`:

```json
    {
      "id": "price_finance_free", "product_id": "prod_finance", "type": "fixed", "unit_amount": 0,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
      "metadata": {"plan": "free", "quota_spaces": "1", "quota_people_per_space": "1"}
    },
    {
      "id": "price_finance_basic_monthly", "product_id": "prod_finance", "type": "fixed", "unit_amount": 1990,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
      "metadata": {"plan": "basic", "quota_spaces": "3", "quota_people_per_space": "5"}
    },
    {
      "id": "price_finance_pro_monthly", "product_id": "prod_finance", "type": "fixed", "unit_amount": 4990,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
      "metadata": {"plan": "pro", "quota_spaces": "10", "quota_people_per_space": "10"}
    },
    {
      "id": "price_finance_ondemand_spaces", "product_id": "prod_finance", "type": "metered", "unit_amount": 490,
      "aggregation": "max", "meter": "finance_spaces", "included_quantity": 1,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears",
      "metadata": {"plan": "ondemand", "quota_spaces": "-1", "quota_people_per_space": "-1"}
    },
    {
      "id": "price_finance_ondemand_people", "product_id": "prod_finance", "type": "metered", "unit_amount": 290,
      "aggregation": "max", "meter": "finance_people", "included_quantity": 1,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears",
      "metadata": {"plan": "ondemand", "quota_spaces": "-1", "quota_people_per_space": "-1"}
    },
    {
      "id": "price_dfe_ondemand_companies_monthly", "product_id": "prod_dfe_ondemand", "type": "metered", "unit_amount": 500,
      "aggregation": "max", "meter": "dfe_companies", "included_quantity": 0,
      "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears",
      "metadata": {"plan": "ondemand", "quota_companies": "-1"}
    }
```

Remove every `"quota_users": …` line from the DF-e prices — `price_dfe_ondemand_user` included — and add `"archived": true,` to `price_dfe_ondemand_user` and to `price_dfe_ondemand_company`. Live rows keep their metadata (scope decision 5); only the archive reaches them. Keep the file's existing formatting style (one key per line, 2-space indent) — the compact form above is for the plan only.

- [ ] **Step 4: Run** — `go test ./internal/provision/ -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/tenants/ctech.json api/internal/provision/plan_test.go
git commit -m "feat(catalog): CTech Finanças plans, DF-e companies by monthly peak, account-billing credential"
```

---

### Task 5: The level store

**Files:**
- Modify: `api/internal/repositories/retention.go`, `retention_test.go`, `keys.go`, `keys_test.go`
- Create: `api/internal/repositories/levels.go`, `api/tests/integration/levels_test.go`

**Interfaces:**
- Consumes: `billing.LevelRecord` (Task 2).
- Produces:

```go
const RetentionLevel = RetentionThirteenMonths
func LevelPK(organizationID string, livemode bool, customerRef, meter string) string
func LevelSK(at time.Time, key string) string
func LevelKeyPK(organizationID string, livemode bool, key string) string
func LevelLatestPK(organizationID string, livemode bool, customerRef, meter string) string // no TTL
var ErrDuplicateLevel, ErrLevelKeyReused error
type LevelRepository struct{ ... }
func NewLevelRepository(db *dynamodb.Client, cfg *config.Config) *LevelRepository
func (r *LevelRepository) Append(ctx context.Context, l *billing.LevelRecord, now time.Time) error
func (r *LevelRepository) LatestBefore(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, before time.Time) (*billing.LevelRecord, error) // nil, nil when none
func (r *LevelRepository) InPeriod(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from, to time.Time) ([]billing.LevelRecord, error) // [from, to)
func (r *LevelRepository) Latest(ctx context.Context, organizationID string, livemode bool, customerRef, meter string) (*billing.LevelRecord, error)    // nil, nil when none
func (r *LevelRepository) FirstFrom(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from time.Time) (*billing.LevelRecord, error) // nil, nil when none
```

- [ ] **Step 1: Write the failing unit tests.** `retention_test.go`:

```go
func TestALevelIsKeptThirteenMonths(t *testing.T) {
	now := time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC)
	got := RetentionLevel.ExpiresAt(now)
	if got == nil || *got != now.AddDate(0, 13, 0).Unix() {
		t.Fatalf("ExpiresAt = %v", got)
	}
}
```

`keys_test.go`:

```go
// Fixed width, so the sort key orders by time: RFC 3339 Nano would trim
// ".500000000" to ".5" and sort it after ".123".
func TestLevelSortKeysOrderByTime(t *testing.T) {
	a := LevelSK(time.Date(2026, 10, 1, 0, 0, 0, 123_000_000, time.UTC), "k")
	b := LevelSK(time.Date(2026, 10, 1, 0, 0, 0, 500_000_000, time.UTC), "k")
	if !(a < b) {
		t.Fatalf("%q !< %q", a, b)
	}
	if LevelPK("ctech", true, "USER_x", "finance_spaces") != "ctech#live#LEVEL#USER_x#finance_spaces" {
		t.Fatal(LevelPK("ctech", true, "USER_x", "finance_spaces"))
	}
	// A report at exactly the bound sorts after it: "before t" excludes t.
	if !(levelBound(time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)) < LevelSK(time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC), "k")) {
		t.Fatal("bound")
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/repositories/ -run 'Level' -v` → FAIL.

- [ ] **Step 3: Implement.** `retention.go`: append `RetentionThirteenMonths` after `RetentionNinetyDays` in the iota block (comment: "a level report (spec § 6.2): long enough to carry a level across a year of no change"), `RetentionLevel = RetentionThirteenMonths` in the table, and in `ExpiresAt`:

```go
	case RetentionThirteenMonths:
		return unixPtr(now.AddDate(0, 13, 0))
```

`keys.go`, after `UsagePK`:

```go
// levelTimeLayout is fixed-width UTC, so a level's sort key orders by time.
const levelTimeLayout = "2006-01-02T15:04:05.000000000Z"

// LevelPK is the partition of one customer reference's levels on one meter
// (spec § 6.2), inside the tenant's mode partition like UsagePK.
func LevelPK(organizationID string, livemode bool, customerRef, meter string) string {
	return TenantPK(organizationID, livemode) + "#LEVEL#" + customerRef + "#" + meter
}

// LevelSK orders reports by when the level was reached; the key makes two
// reports at the same instant two items.
func LevelSK(at time.Time, key string) string { return levelBound(at) + "#" + key }

// levelBound is the sort-key prefix of an instant. Every report at that
// instant sorts after it ("…Z#key" > "…Z"), so `sk < levelBound(t)` is
// "strictly before t".
func levelBound(t time.Time) string { return t.UTC().Format(levelTimeLayout) }

// LevelKeyPK is the idempotency marker of a level report: keyed by the
// caller's key alone, so a reuse with another instant is seen (scope decision 8).
func LevelKeyPK(organizationID string, livemode bool, key string) string {
	return TenantPK(organizationID, livemode) + "#LEVEL_KEY#" + key
}

// LevelLatestPK holds the newest level of (customer_ref, meter) with no TTL, so
// a level that never changes is never lost (scope decision 9).
func LevelLatestPK(organizationID string, livemode bool, customerRef, meter string) string {
	return TenantPK(organizationID, livemode) + "#LEVEL_LATEST#" + customerRef + "#" + meter
}
```

`levels.go`:

```go
package repositories

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

var (
	// ErrDuplicateLevel is a retried report: the same key with the same body.
	// Callers answer success.
	ErrDuplicateLevel = errors.New("level already reported")
	// ErrLevelKeyReused is the same key with another body: a caller bug that
	// must not become a silent second level.
	ErrLevelKeyReused = errors.New("idempotency key already used for another level")
)

const levelKeySK = "KEY"

const levelLatestSK = "LATEST"

// LevelRepository stores level reports (spec § 6.2) in the usage table.
type LevelRepository struct {
	base  Base
	table string // physical name, for the one conditional Put Base has no builder for
}

func NewLevelRepository(db *dynamodb.Client, cfg *config.Config) *LevelRepository {
	return &LevelRepository{base: NewBase(db, cfg, TableUsage), table: TableName(cfg, TableUsage)}
}

// levelLatestRow is the newest level of one (customer_ref, meter). No TTL.
type levelLatestRow struct {
	keys
	billing.LevelRecord
	At string `dynamodbav:"at"` // levelBound(OccurredAt), compared in the condition
}

type levelRow struct {
	keys
	billing.LevelRecord
}

type levelKeyRow struct {
	keys
	Hash string `dynamodbav:"hash"`
}

func levelHash(l *billing.LevelRecord) string {
	h := sha256.Sum256([]byte(l.CustomerRef + "\x00" + l.Meter + "\x00" + strconv.FormatInt(l.Value, 10) + "\x00" + levelBound(l.OccurredAt)))
	return hex.EncodeToString(h[:])
}

// Append stores a report once per idempotency key: the level, its key marker
// and — when it is the newest level of its meter — the no-TTL latest item, in
// one transaction (scope decisions 8 and 9). The latest is conditional on still
// being the one read; when only that condition fails, another report moved it
// first, and the whole report is re-planned (at most 3 times).
func (r *LevelRepository) Append(ctx context.Context, l *billing.LevelRecord, now time.Time) error {
	if err := l.Validate(); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		latest, err := r.latestRow(ctx, l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter)
		if err != nil {
			return err
		}
		rec := *l
		newest := latest == nil || !l.OccurredAt.Before(latest.OccurredAt)
		switch {
		case newest && latest != nil:
			rec.Previous = latest.Value
		case !newest:
			// A late report: what came before it is whatever is stored before it.
			prior, err := r.LatestBefore(ctx, l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter, l.OccurredAt)
			if err != nil {
				return err
			}
			if prior != nil {
				rec.Previous = prior.Value
			}
		}
		items, err := r.reportItems(&rec, now)
		if err != nil {
			return err
		}
		if newest {
			put, err := r.latestPut(&rec, latest, now)
			if err != nil {
				return err
			}
			items = append(items, put)
		}
		err = r.base.TransactWrite(ctx, items)
		codes := cancellationCodes(err)
		if newest && len(codes) == 3 && codes[0] == codeNone && codes[1] == codeNone && codes[2] == codeConditionFailed {
			continue // the latest moved under us: re-read and re-plan
		}
		if !IsConditionFailed(err) {
			return conflictErr(err)
		}
		return r.duplicateOrReused(ctx, l)
	}
	return fmt.Errorf("%w: the latest level of %s kept moving", ErrTransactionConflict, l.Meter)
}

// reportItems are the marker and the report, both put-if-absent.
func (r *LevelRepository) reportItems(l *billing.LevelRecord, now time.Time) ([]types.TransactWriteItem, error) {
	item, err := Encode(levelRow{
		keys:        newKeys(LevelPK(l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter), LevelSK(l.OccurredAt, l.IdempotencyKey), RetentionLevel, now),
		LevelRecord: *l,
	})
	if err != nil {
		return nil, err
	}
	marker, err := Encode(levelKeyRow{
		keys: newKeys(LevelKeyPK(l.OrganizationID, l.Livemode, l.IdempotencyKey), levelKeySK, RetentionLevel, now),
		Hash: levelHash(l),
	})
	if err != nil {
		return nil, err
	}
	return txItems(r.base.BuildPutTxItemIfAbsent(marker), r.base.BuildPutTxItemIfAbsent(item)), nil
}

// latestPut replaces the latest item only if it is still the one read.
func (r *LevelRepository) latestPut(l *billing.LevelRecord, read *levelLatestRow, now time.Time) (types.TransactWriteItem, error) {
	item, err := Encode(levelLatestRow{
		keys:        newKeys(LevelLatestPK(l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter), levelLatestSK, RetentionPermanent, now),
		LevelRecord: *l,
		At:          levelBound(l.OccurredAt),
	})
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	put := &types.Put{TableName: aws.String(r.table), Item: item}
	if read == nil {
		put.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		put.ConditionExpression = aws.String("#at = :read")
		put.ExpressionAttributeNames = map[string]string{"#at": "at"}
		put.ExpressionAttributeValues = map[string]types.AttributeValue{":read": &types.AttributeValueMemberS{Value: read.At}}
	}
	return types.TransactWriteItem{Put: put}, nil
}

// duplicateOrReused reads the marker after a refused report.
func (r *LevelRepository) duplicateOrReused(ctx context.Context, l *billing.LevelRecord) error {
	stored, err := r.base.GetItem(ctx, LevelKeyPK(l.OrganizationID, l.Livemode, l.IdempotencyKey), levelKeySK)
	if err != nil {
		return err
	}
	if stored == nil {
		// The level item itself existed with no marker: the same instant and key,
		// so the same report.
		return fmt.Errorf("%w: %s", ErrDuplicateLevel, l.IdempotencyKey)
	}
	row, err := Decode[levelKeyRow](stored)
	if err != nil {
		return err
	}
	if row.Hash != levelHash(l) {
		return fmt.Errorf("%w: %s", ErrLevelKeyReused, l.IdempotencyKey)
	}
	return fmt.Errorf("%w: %s", ErrDuplicateLevel, l.IdempotencyKey)
}

func (r *LevelRepository) latestRow(ctx context.Context, organizationID string, livemode bool, customerRef, meter string) (*levelLatestRow, error) {
	item, err := r.base.GetItem(ctx, LevelLatestPK(organizationID, livemode, customerRef, meter), levelLatestSK)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[levelLatestRow](item)
}

// Latest is the newest level of (customer_ref, meter), kept with no TTL.
func (r *LevelRepository) Latest(ctx context.Context, organizationID string, livemode bool, customerRef, meter string) (*billing.LevelRecord, error) {
	row, err := r.latestRow(ctx, organizationID, livemode, customerRef, meter)
	if err != nil || row == nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// FirstFrom is the oldest in-TTL report at or after from.
func (r *LevelRepository) FirstFrom(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from time.Time) (*billing.LevelRecord, error) {
	res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk >= :from"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":   &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
			":from": &types.AttributeValueMemberS{Value: levelBound(from)},
		},
		ScanIndexForward: aws.Bool(true),
		Limit:            aws.Int32(1),
		ConsistentRead:   aws.Bool(true),
	})
	if err != nil || len(res.Items) == 0 {
		return nil, err
	}
	row, err := Decode[levelRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// LatestBefore is the last level reached strictly before `before`: one Query,
// newest first, limit 1. Nil when there is none.
func (r *LevelRepository) LatestBefore(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, before time.Time) (*billing.LevelRecord, error) {
	res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk < :before"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
			":before": &types.AttributeValueMemberS{Value: levelBound(before)},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
		ConsistentRead:   aws.Bool(true),
	})
	if err != nil || len(res.Items) == 0 {
		return nil, err
	}
	row, err := Decode[levelRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// InPeriod returns the levels reached in [from, to), oldest first, following
// continuation keys: the close needs all of them or none.
func (r *LevelRepository) InPeriod(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from, to time.Time) ([]billing.LevelRecord, error) {
	var out []billing.LevelRecord
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
			// BETWEEN is inclusive, and a report at exactly `to` sorts after
			// levelBound(to) ("…Z#key" > "…Z"), so this is [from, to).
			KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :lo AND :hi"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
				":lo": &types.AttributeValueMemberS{Value: levelBound(from)},
				":hi": &types.AttributeValueMemberS{Value: levelBound(to)},
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[levelRow](res.Items)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.LevelRecord)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

```

(`cancellationCodes`, `codeNone` and `codeConditionFailed` already exist in `ledger.go`.)

- [ ] **Step 4: Run** — `go test ./internal/repositories/ -v` → PASS.

- [ ] **Step 5: Integration tests** — `api/tests/integration/levels_test.go`:

```go
//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func level(org string, v int64, at time.Time, key string) *billing.LevelRecord {
	return &billing.LevelRecord{OrganizationID: org, Livemode: true, CustomerRef: "USER_" + org, Meter: "finance_spaces",
		Value: v, OccurredAt: at, IdempotencyKey: key}
}

func TestALevelReportedTwiceIsOneRecord(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 3, 0, 0, time.UTC)
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); !errors.Is(err, repositories.ErrDuplicateLevel) {
		t.Fatalf("retry: %v", err)
	}
	got, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	if len(got) != 1 {
		t.Fatalf("%d records", len(got))
	}
}

// Review Focus 1.
func TestALevelKeyReusedWithAnotherInstantIsRefused(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 3, 0, 0, time.UTC)
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); err != nil {
		t.Fatal(err)
	}
	for _, other := range []*billing.LevelRecord{level(org, 4, at.Add(time.Minute), "k1"), level(org, 5, at, "k1")} {
		if err := r.Append(ctx, other, now()); !errors.Is(err, repositories.ErrLevelKeyReused) {
			t.Fatalf("reuse: %v", err)
		}
	}
	got, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	if len(got) != 1 || got[0].Value != 4 {
		t.Fatalf("records = %+v", got)
	}
}

func TestLatestBeforeIsStrictlyBefore(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	start := time.Date(2026, time.March, 1, 3, 0, 0, 0, time.UTC) // midnight in São Paulo
	_ = r.Append(ctx, level(org, 2, start.Add(-48*time.Hour), "a"), now())
	_ = r.Append(ctx, level(org, 3, start.Add(-time.Hour), "b"), now())
	_ = r.Append(ctx, level(org, 9, start, "c"), now())
	got, err := r.LatestBefore(ctx, org, true, "USER_"+org, "finance_spaces", start)
	if err != nil || got == nil || got.Value != 3 {
		t.Fatalf("latest = %+v, %v", got, err)
	}
	none, err := r.LatestBefore(ctx, org, true, "USER_"+org, "finance_people", start)
	if err != nil || none != nil {
		t.Fatalf("another meter = %+v, %v", none, err)
	}
	in, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", start, start.AddDate(0, 1, 0))
	if len(in) != 1 || in[0].Value != 9 {
		t.Fatalf("in period = %+v", in)
	}
}
```

Append (Review Focus 3):

```go
// The latest item has no TTL: with every report gone (simulating expiry), the
// level is still known, and each report knows the level before it.
func TestTheLatestLevelOutlivesItsReports(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2025, time.January, 5, 14, 0, 0, 0, time.UTC)
	_ = r.Append(ctx, level(org, 2, at, "a"), now())
	_ = r.Append(ctx, level(org, 4, at.Add(time.Hour), "b"), now())
	_ = r.Append(ctx, level(org, 3, at.Add(-time.Hour), "late"), now()) // late: does not move the latest
	b, _ := r.FirstFrom(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(time.Minute))
	if b == nil || b.Value != 4 || b.Previous != 2 {
		t.Fatalf("b = %+v", b)
	}
	base := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	for _, k := range []struct {
		at  time.Time
		key string
	}{{at, "a"}, {at.Add(time.Hour), "b"}, {at.Add(-time.Hour), "late"}} {
		if _, err := base.DeleteItem(ctx, repositories.LevelPK(org, true, "USER_"+org, "finance_spaces"), repositories.LevelSK(k.at, k.key)); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := r.Latest(ctx, org, true, "USER_"+org, "finance_spaces")
	if err != nil || latest == nil || latest.Value != 4 {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
}

func TestConcurrentReportsLeaveTheNewestAsLatest(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.Append(ctx, level(org, int64(i), at.Add(time.Duration(i)*time.Minute), "k"+strconv.Itoa(i)), now())
		}(i)
	}
	wg.Wait()
	if latest, _ := r.Latest(ctx, org, true, "USER_"+org, "finance_spaces"); latest == nil || latest.Value != 5 {
		t.Fatalf("latest = %+v", latest)
	}
}
```

(imports: `strconv`, `sync`. A report that loses all three retries returns `ErrTransactionConflict` → 409 `concurrent_update`, and ctech-account retries it with the same key.)

Run: `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Level' -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add api/internal/repositories/retention.go api/internal/repositories/retention_test.go api/internal/repositories/keys.go api/internal/repositories/keys_test.go api/internal/repositories/levels.go api/tests/integration/levels_test.go
git commit -m "feat(levels): store level reports once per key, with a latest level that never expires"
```

---

### Task 6: `POST /v1.0/usage/levels`, and the owner scope on usage

**Files:**
- Create: `api/internal/api/v1/levels.go`
- Modify: `api/internal/api/v1/handlers.go` (struct field, `reportUsage` owner check), `api/internal/api/v1/router.go`, `api/internal/problem/problem.go`, `api/internal/app/app.go`
- Test: `api/tests/integration/entitlements_owner_test.go` (new; Task 7 appends to it)

**Interfaces:**
- Consumes: `LevelRepository` (Task 5), `APICredential.Allows`/`OwnerKey` (Task 3).
- Produces: `Deps.Levels *repositories.LevelRepository`; `handlers.levels`; `func (h *handlers) meterOwners(ctx context.Context, t middleware.Tenant, meter string) ([]string, error)`; `func ownerNotAllowed(c fiber.Ctx) error`; the route; problem mapping `ErrLevelKeyReused` → 409 `idempotency_key_reused`.

- [ ] **Step 1: Write the failing integration tests** — `entitlements_owner_test.go`:

```go
//go:build integration

package integration

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ownersEnv is newAPI plus a Finanças product (with its default), a DF-e
// product, and one credential scoped to each owner.
type ownersEnv struct {
	*apiEnv
	finClient, dfeClient          string
	free, basic, spaces, dfePrice string
}

func newOwnersEnv(t *testing.T) ownersEnv {
	t.Helper()
	e, ctx := newAPI(t), ctxT(t)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	mk := func(p *billing.Product) {
		p.OrganizationID, p.Livemode, p.Active = e.org.ID, true, true
		if err := cat.CreateProduct(ctx, p, "test", "r", now()); err != nil {
			t.Fatal(err)
		}
	}
	price := func(product string, typ billing.PriceType, amount billing.Cents, md billing.Metadata, set func(*billing.Price)) string {
		p := &billing.Price{ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: e.org.ID, Livemode: true, ProductID: product,
			Type: typ, Currency: billing.CurrencyBRL, UnitAmount: amount,
			Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1}, Timing: billing.BillAdvance, Metadata: md}
		if set != nil {
			set(p)
		}
		if err := cat.CreatePrice(ctx, p, "test", "r", now()); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	o := ownersEnv{apiEnv: e, free: id.NewWithPrefix(id.PrefixPrice)}
	fin := "prod_fin_" + id.New()
	mk(&billing.Product{ID: fin, Name: "CTech Finanças", OwnerKey: "finance", DefaultPriceID: o.free})
	price(fin, billing.PriceFixed, 0, billing.Metadata{"plan": "free", "quota_spaces": "1", "quota_people_per_space": "1"},
		func(p *billing.Price) { p.ID = o.free })
	o.basic = price(fin, billing.PriceFixed, 1990, billing.Metadata{"plan": "basic", "quota_spaces": "3"}, nil)
	o.spaces = price(fin, billing.PriceMetered, 490, billing.Metadata{"plan": "ondemand"}, func(p *billing.Price) {
		p.Timing, p.Aggregation, p.Meter, p.IncludedQuantity = billing.BillArrears, billing.AggregationMax, "finance_spaces", 1
	})
	dfe := "prod_dfe_" + id.New()
	mk(&billing.Product{ID: dfe, Name: "DF-e Pro", OwnerKey: "dfe"})
	o.dfePrice = price(dfe, billing.PriceFixed, 0, billing.Metadata{"plan": "pro"}, nil)
	price(dfe, billing.PriceMetered, 500, billing.Metadata{"plan": "ondemand"}, func(p *billing.Price) {
		p.Timing, p.Aggregation, p.Meter = billing.BillArrears, billing.AggregationMax, "dfe_companies"
	})

	creds := repositories.NewCredentialRepository(testDB, testCfg)
	for owner, client := range map[string]*string{"finance": &o.finClient, "dfe": &o.dfeClient} {
		*client = "cli_" + owner + "_" + id.New()
		if err := creds.Create(ctx, &billing.APICredential{ClientID: *client, OrganizationID: e.org.ID, Livemode: true, Active: true, OwnerKey: owner}, now()); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func levelBody(ref, meter, key, at string, v int) string {
	return `{"customer_ref":"` + ref + `","meter":"` + meter + `","value":` + itoa(v) + `,"occurred_at":"` + at + `","idempotency_key":"` + key + `"}`
}

func itoa(n int) string { return strconv.Itoa(n) }

// Spec test 4.
func TestLevelReportsAreIdempotentByBodyKey(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	body := levelBody("USER_a", "finance_spaces", "lvl:a:1", "2026-03-10T11:00:00Z", 4)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", body); r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.body)
	}
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", body); r.status != http.StatusOK || !strings.Contains(string(r.body), `"duplicate":true`) {
		t.Fatalf("retry: %d %s", r.status, r.body)
	}
	other := levelBody("USER_a", "finance_spaces", "lvl:a:1", "2026-03-10T11:00:00Z", 5)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", other); r.status != http.StatusConflict || !strings.Contains(string(r.body), "idempotency_key_reused") {
		t.Fatalf("reuse: %d %s", r.status, r.body)
	}
}

func TestALevelForAnotherOwnersMeterIsRefused(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.dfeClient, "", middleware.ScopeUsageWrite)
	r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("USER_a", "finance_spaces", "k", "2026-03-10T11:00:00Z", 1))
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "meter_not_allowed") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fin := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", fin, "", levelBody("USER_a", "nobody_meters", "k2", "2026-03-10T11:00:00Z", 1)); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown meter: %d %s", r.status, r.body)
	}
}

// User decision 2026-10-10: dfe-billing reports its own meter's levels.
func TestDfeReportsItsCompaniesLevel(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.dfeClient, "", middleware.ScopeUsageWrite)
	r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("ORG_x", "dfe_companies", "c1", "2026-03-10T11:00:00Z", 3))
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fin := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", fin, "", levelBody("ORG_x", "dfe_companies", "c2", "2026-03-10T11:00:00Z", 3)); r.status != http.StatusForbidden {
		t.Fatalf("finance reporting dfe_companies: %d", r.status)
	}
}

func TestALevelNeedsNoCustomerAndCreatesNone(t *testing.T) {
	o := newOwnersEnv(t)
	tok := o.token(t, o.finClient, "", middleware.ScopeUsageWrite)
	if r := o.do(t, http.MethodPost, "/v1.0/usage/levels", tok, "", levelBody("USER_nobody", "finance_spaces", "k", "2026-03-10T11:00:00Z", 2)); r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByExternalRef(ctxT(t), o.org.ID, true, "USER_nobody"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a report created a customer: %v", err)
	}
}
```

(imports: add `errors`, `strconv`, `strings`. The `price` helper's `ID` override is applied before `CreatePrice`, so the default price is created under the id the product names.)

- [ ] **Step 2: Run** — `go test -tags integration ./tests/integration/ -run 'Level.*Key|AnotherOwnersMeter|NeedsNoCustomer' -v` → FAIL (404 on the route).

- [ ] **Step 3: Implement.** `handlers.go`: add `levels *repositories.LevelRepository` to `handlers`; in `reportUsage`, after `sub` is read:

```go
	if cred := middleware.GetCredential(c); cred != nil && !cred.Allows(sub.OwnerKey) {
		return ownerNotAllowed(c)
	}
```

`levels.go`:

```go
package v1

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type reportLevelRequest struct {
	CustomerRef    string `json:"customer_ref"`
	Meter          string `json:"meter"`
	Value          *int64 `json:"value"`
	OccurredAt     string `json:"occurred_at"`
	IdempotencyKey string `json:"idempotency_key"`
}

func ownerNotAllowed(c fiber.Ctx) error {
	return problem.Forbidden("this credential may not act for that owner").WithCode("owner_not_allowed").Send(c)
}

// reportLevel stores a level report (spec § 6.1): the whole current count of a
// meter for a customer reference. It needs no customer and creates none. Not
// behind the Idempotency-Key middleware: the body's key is the contract, and
// the repository enforces it with a marker (scope decision 8).
func (h *handlers) reportLevel(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req reportLevelRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.text("customer_ref", req.CustomerRef, true, limits.ExternalRef)
	ch.text("idempotency_key", req.IdempotencyKey, true, 255)
	if !billing.ValidMeter(req.Meter) {
		ch.fail("meter", "invalid_format", "lower-case letters, digits and _")
	}
	switch {
	case req.Value == nil:
		ch.fail("value", "required", "required")
	case *req.Value < 0 || *req.Value > limits.MaxUsageQuantity:
		ch.fail("value", "out_of_range", fmt.Sprintf("between 0 and %d", limits.MaxUsageQuantity), "min", 0, "max", limits.MaxUsageQuantity)
	}
	at, err := time.Parse(time.RFC3339, req.OccurredAt)
	switch {
	case req.OccurredAt == "":
		ch.fail("occurred_at", "required", "required")
	case err != nil:
		ch.fail("occurred_at", "invalid_format", "use RFC 3339", "format", "RFC3339")
	case at.Before(limits.MinDate.Time()) || at.After(h.now().Add(24*time.Hour)):
		ch.fail("occurred_at", "out_of_range", "outside the accepted range")
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}

	owners, err := h.meterOwners(c.Context(), t, req.Meter)
	if err != nil {
		return fail(c, err)
	}
	if len(owners) == 0 {
		return problem.Validation([]problem.FieldError{fieldErr("meter", "not_found", "no price is metered by this meter")}).Send(c)
	}
	if cred := middleware.GetCredential(c); cred != nil && cred.OwnerKey != "" && !slices.Contains(owners, cred.OwnerKey) {
		return problem.Forbidden("this credential may not report this meter").WithCode("meter_not_allowed").Send(c)
	}

	rec := &billing.LevelRecord{
		OrganizationID: t.OrganizationID, Livemode: t.Livemode, CustomerRef: req.CustomerRef, Meter: req.Meter,
		Value: *req.Value, OccurredAt: at, IdempotencyKey: req.IdempotencyKey,
	}
	switch err := h.levels.Append(c.Context(), rec, h.now()); {
	case err == nil:
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"recorded": true, "duplicate": false})
	case errors.Is(err, repositories.ErrDuplicateLevel):
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"recorded": true, "duplicate": true})
	default:
		return fail(c, err)
	}
}

// meterOwners lists the owner keys of the products whose prices are metered by
// meter (archived prices included: a subscription may still bill one).
func (h *handlers) meterOwners(ctx context.Context, t middleware.Tenant, meter string) ([]string, error) {
	prices, err := h.cat.ListPrices(ctx, t.OrganizationID, t.Livemode, pageLimit)
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, p := range prices {
		if p.Meter != meter {
			continue
		}
		product, err := h.cat.GetProduct(ctx, t.OrganizationID, t.Livemode, p.ProductID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(owners, product.OwnerKey) {
			owners = append(owners, product.OwnerKey)
		}
	}
	return owners, nil
}
```

`problem.go`, next to the `ErrDuplicateUsage` case:

```go
	case errors.Is(err, repositories.ErrLevelKeyReused):
		return New(409, TypeIdempotencyConflict, "Idempotency Conflict",
			"this idempotency_key was already used for another level").WithCode("idempotency_key_reused")
```

`router.go`: `Deps.Levels *repositories.LevelRepository`; `levels: d.Levels` in `h`; replace the usage registration with

```go
	usage := m2m("/usage")
	usage.Post("", middleware.RequireM2MScope(middleware.ScopeUsageWrite), idem, h.reportUsage)
	// Levels are idempotent by the body's key (spec § 6.1), enforced in the
	// repository; the header middleware is deliberately not in front of it.
	usage.Post("/levels", middleware.RequireM2MScope(middleware.ScopeUsageWrite), h.reportLevel)
```

`app.go` `Build`: `levels := repositories.NewLevelRepository(db, cfg)` and `Levels: levels` in `v1.Deps`.

- [ ] **Step 4: Run** — `go build ./... && go test ./... && go test -tags integration ./tests/integration/ -run 'Level' -count=1 -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1/levels.go api/internal/api/v1/handlers.go api/internal/api/v1/router.go api/internal/problem/problem.go api/internal/app/app.go api/tests/integration/entitlements_owner_test.go
git commit -m "feat(api): POST /usage/levels, idempotent by key, refused for another owner's meter"
```

---

### Task 7: Entitlements by owner, with a default and no 404

**Files:**
- Modify: `api/internal/api/v1/handlers.go` (`getEntitlements`), `api/internal/api/v1/dto.go`
- Test: append to `api/tests/integration/entitlements_owner_test.go`

**Interfaces:**
- Consumes: `ownersEnv` (Task 6), `Product.DefaultPriceID`, `Subscription.OwnerKey`.
- Produces: `entitlementResponse.Default *entitlementDefault` (`default`, omitempty), `CustomerID` becomes `omitempty`, `Subscriptions` always an array; `type entitlementDefault struct{ PriceID string \`json:"price_id"\`; Plan string \`json:"plan"\`; Metadata billing.Metadata \`json:"metadata"\` }`; `func (h *handlers) entitlementDefaultFor(ctx context.Context, t middleware.Tenant, owner string) (*entitlementDefault, error)`.

- [ ] **Step 1: Write the failing tests** (spec § 9 tests 1, 2):

```go
// subscribeIn creates a customer and an ACTIVE subscription on prices via the
// service, the way newCatalog's subscriber does.
func (o ownersEnv) customerWith(t *testing.T, ref string, priceIDs ...string) string {
	t.Helper()
	ctx := ctxT(t)
	c := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: o.org.ID, Livemode: true, Name: "P", ExternalRef: ref}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, c, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	inv := repositories.NewInvoiceRepository(testDB, testCfg)
	s := services.NewSubscriber(subs, cat, services.NewInvoicer(subs, inv, cat, repositories.NewUsageRepository(testDB, testCfg)))
	for _, p := range priceIDs {
		if _, _, err := s.Subscribe(ctx, services.SubscribeInput{OrganizationID: o.org.ID, Livemode: true, CustomerID: c.ID,
			Items: []services.SubscribeItem{{PriceID: p}}, Actor: "test"}, now()); err != nil {
			t.Fatal(err)
		}
	}
	return c.ID
}

type entitlementsBody struct {
	CustomerID    string `json:"customer_id"`
	Entitled      bool   `json:"entitled"`
	Subscriptions []struct {
		PriceID string `json:"price_id"`
	} `json:"subscriptions"`
	Default *struct {
		PriceID  string            `json:"price_id"`
		Plan     string            `json:"plan"`
		Metadata map[string]string `json:"metadata"`
	} `json:"default"`
}

func (o ownersEnv) entitlements(t *testing.T, client, query string) (int, entitlementsBody) {
	t.Helper()
	r := o.do(t, http.MethodGet, "/v1.0/entitlements?"+query, o.token(t, client, "", middleware.ScopeEntitlementsRead), "", "")
	var b entitlementsBody
	if r.status == http.StatusOK {
		r.decode(t, &b)
	}
	return r.status, b
}

func TestOwnerKeyReturnsOnlyThatOwnersSubscriptions(t *testing.T) {
	o := newOwnersEnv(t)
	o.customerWith(t, "USER_both", o.basic, o.dfePrice)
	code, b := o.entitlements(t, o.finClient, "customer_ref=USER_both&owner_key=finance")
	if code != 200 || len(b.Subscriptions) != 1 || b.Subscriptions[0].PriceID != o.basic {
		t.Fatalf("%d %+v", code, b)
	}
}

// Review Focus 2: ctech-dfe sends no owner_key; its scoped credential still
// sees only DF-e, and a missing customer is still the 404 it relies on.
func TestAScopedCredentialWithoutOwnerKeyReadsOnlyItsOwner(t *testing.T) {
	o := newOwnersEnv(t)
	o.customerWith(t, "USER_both2", o.basic, o.dfePrice)
	code, b := o.entitlements(t, o.dfeClient, "customer_ref=USER_both2")
	if code != 200 || len(b.Subscriptions) != 1 || b.Subscriptions[0].PriceID != o.dfePrice || b.Default != nil {
		t.Fatalf("%d %+v", code, b)
	}
}

func TestWithoutOwnerKeyAMissingCustomerIsStill404(t *testing.T) {
	o := newOwnersEnv(t)
	if code, _ := o.entitlements(t, o.dfeClient, "customer_ref=USER_ghost"); code != http.StatusNotFound {
		t.Fatalf("status %d", code)
	}
}

func TestAMissingCustomerWithOwnerKeyGetsTheDefaultAndWritesNothing(t *testing.T) {
	o := newOwnersEnv(t)
	code, b := o.entitlements(t, o.finClient, "customer_ref=USER_ghost&owner_key=finance")
	if code != 200 || b.Entitled || b.Subscriptions == nil || len(b.Subscriptions) != 0 ||
		b.Default == nil || b.Default.PriceID != o.free || b.Default.Plan != "free" || b.Default.Metadata["quota_spaces"] != "1" {
		t.Fatalf("%d %+v", code, b)
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByExternalRef(ctxT(t), o.org.ID, true, "USER_ghost"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatal("reading created a customer")
	}
}

func TestACustomerWithNoEntitlingSubscriptionGetsTheDefault(t *testing.T) {
	o := newOwnersEnv(t)
	o.customerWith(t, "USER_dfeonly", o.dfePrice)
	code, b := o.entitlements(t, o.finClient, "customer_ref=USER_dfeonly&owner_key=finance")
	if code != 200 || b.Entitled || len(b.Subscriptions) != 0 || b.Default == nil {
		t.Fatalf("%d %+v", code, b)
	}
	// An entitled one carries no default.
	o.customerWith(t, "USER_basic", o.basic) // INCOMPLETE until paid: not entitled
	_, b = o.entitlements(t, o.finClient, "customer_ref=USER_basic&owner_key=finance")
	if b.Entitled || b.Default == nil {
		t.Fatalf("an unpaid Basic is not entitled and gets the default: %+v", b)
	}
}

func TestAScopedCredentialCannotAskForAnotherOwner(t *testing.T) {
	o := newOwnersEnv(t)
	if code, _ := o.entitlements(t, o.finClient, "customer_ref=USER_x&owner_key=dfe"); code != http.StatusForbidden {
		t.Fatalf("status %d", code)
	}
}
```

(imports: `services`.)

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `dto.go`: `CustomerID string \`json:"customer_id,omitempty"\``, add `Default *entitlementDefault \`json:"default,omitempty"\`` and the type. `handlers.go`, replace `getEntitlements`:

```go
func (h *handlers) getEntitlements(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	// asked is what the caller sent; owner is what is read. A scoped credential
	// reads only its own owner, whether or not it says so; the default and the
	// missing-customer answer are for callers that ask (scope decision 3).
	asked := c.Query("owner_key")
	owner := asked
	if cred := middleware.GetCredential(c); cred != nil && cred.OwnerKey != "" {
		switch {
		case asked == "":
			owner = cred.OwnerKey
		case asked != cred.OwnerKey:
			return ownerNotAllowed(c)
		}
	}
	resp := entitlementResponse{Subscriptions: []entitlementSubscription{}}

	customerID := c.Query("customer_id")
	if ref := c.Query("customer_ref"); ref != "" {
		customer, err := h.customers.GetByExternalRef(c.Context(), t.OrganizationID, t.Livemode, ref)
		switch {
		case err == nil:
			customerID = customer.ID
		case errors.Is(err, repositories.ErrNotFound) && asked != "":
			// Reading never creates a customer (spec § 4).
			return h.answerEntitlements(c, t, asked, resp)
		default:
			return fail(c, err)
		}
	}
	if customerID == "" {
		return problem.BadRequest("informe customer_id ou customer_ref").Send(c)
	}
	resp.CustomerID = customerID

	subs, err := h.subs.ListByCustomer(c.Context(), t.OrganizationID, t.Livemode, customerID, 100)
	if err != nil {
		return fail(c, err)
	}
	for i := range subs {
		sub := &subs[i]
		if owner != "" && sub.OwnerKey != owner {
			continue
		}
		entitled := sub.IsEntitled()
		resp.Entitled = resp.Entitled || entitled
		out := entitlementSubscription{
			ID: sub.ID, Status: sub.Status, Entitled: entitled,
			CancelAtPeriodEnd: sub.CancelAtPeriodEnd, Period: sub.CurrentPeriod(),
		}
		if err := h.describeEntitlement(c, sub, &out); err != nil {
			return fail(c, err)
		}
		resp.Subscriptions = append(resp.Subscriptions, out)
	}
	return h.answerEntitlements(c, t, asked, resp)
}

// answerEntitlements adds the owner's default when the caller asked for an
// owner and nothing returned is entitled.
func (h *handlers) answerEntitlements(c fiber.Ctx, t middleware.Tenant, asked string, resp entitlementResponse) error {
	if asked != "" && !resp.Entitled {
		d, err := h.entitlementDefaultFor(c.Context(), t, asked)
		if err != nil {
			return fail(c, err)
		}
		resp.Default = d
	}
	return c.JSON(resp)
}

// entitlementDefaultFor is the default of the owner's one product that names
// one; none, or more than one, is no default.
func (h *handlers) entitlementDefaultFor(ctx context.Context, t middleware.Tenant, owner string) (*entitlementDefault, error) {
	products, err := h.cat.ListProducts(ctx, t.OrganizationID, t.Livemode, pageLimit)
	if err != nil {
		return nil, err
	}
	var priceID string
	for _, p := range products {
		if p.OwnerKey == owner && p.DefaultPriceID != "" {
			if priceID != "" {
				return nil, nil
			}
			priceID = p.DefaultPriceID
		}
	}
	if priceID == "" {
		return nil, nil
	}
	price, err := h.cat.GetPrice(ctx, t.OrganizationID, t.Livemode, priceID)
	if err != nil {
		return nil, err
	}
	return &entitlementDefault{PriceID: price.ID, Plan: price.Metadata[metadataKeyPlan], Metadata: price.Metadata}, nil
}
```

(add `context` to imports.)

- [ ] **Step 4: Run** — `go test ./... && go test -tags integration ./tests/integration/ -run 'Owner|Entitle|Default|Subscribe' -count=1 -v` → PASS (including the existing `TestSubscribeThroughTheAPI` entitlements step, with an unscoped credential).
- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1/handlers.go api/internal/api/v1/dto.go api/tests/integration/entitlements_owner_test.go
git commit -m "feat(api): entitlements filtered by owner_key, with the owner's default"
```

---

### Task 8: The close bills `max` prices by level

**Files:**
- Modify: `api/internal/services/invoicing.go`, `api/internal/app/app.go` (`Build`, `BuildInvoicer`)
- Create: `api/tests/integration/max_close_test.go`

**Interfaces:**
- Consumes: `LevelRepository` (Task 5), `MaxLevel`, `MeteredLine` (Tasks 1–2), `CustomerRepository.Get`.
- Produces: `func (s *Invoicer) WithLevels(levels *repositories.LevelRepository, customers *repositories.CustomerRepository) *Invoicer`; `ErrLevelsNotWired`. The close writes nothing to the level store.

- [ ] **Step 1: Write the failing integration tests** — `max_close_test.go` (reuse `newCatalog` from `multi_item_test.go`):

```go
//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type maxFixture struct {
	*catalogFixture
	levels   *repositories.LevelRepository
	invoicer *services.Invoicer
	ref      string
	lastKey  string
	sub      *billing.Subscription
	items    []billing.SubscriptionItem
}

func newMaxFixture(t *testing.T, anchor brcal.Date) *maxFixture {
	t.Helper()
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	ref := "USER_" + id.New()
	cust := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: f.org.ID, Livemode: true, Name: "P", ExternalRef: ref}
	if err := customers.Create(ctx, cust, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	cat := f.catalog
	price := &billing.Price{ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: f.org.ID, Livemode: true, ProductID: f.product.ID,
		Type: billing.PriceMetered, Currency: billing.CurrencyBRL, UnitAmount: 490,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1}, Timing: billing.BillArrears,
		Aggregation: billing.AggregationMax, Meter: "finance_spaces", IncludedQuantity: 1}
	if err := cat.CreatePrice(ctx, price, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true, CustomerID: cust.ID,
		Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	items, _ := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	inv := services.NewInvoicer(f.subs, f.invoices, cat, f.usage).WithLevels(levels, customers)
	return &maxFixture{catalogFixture: f, levels: levels, invoicer: inv, ref: ref, sub: sub, items: items}
}

func (m *maxFixture) report(t *testing.T, v int64, at time.Time) {
	t.Helper()
	m.lastKey = id.New()
	if err := m.levels.Append(ctxT(t), &billing.LevelRecord{OrganizationID: m.org.ID, Livemode: true, CustomerRef: m.ref,
		Meter: "finance_spaces", Value: v, OccurredAt: at, IdempotencyKey: m.lastKey}, now()); err != nil {
		t.Fatal(err)
	}
}

func (m *maxFixture) close(t *testing.T, p billing.Period) *billing.Invoice {
	t.Helper()
	inv, err := m.invoicer.GenerateForPeriod(ctxT(t), m.sub, m.items, p, "scheduler", now())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestAPeakMidMonthIsBilledBeyondTheIncludedUnit(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.February, 1))
	m.report(t, 2, time.Date(2026, time.January, 20, 15, 0, 0, 0, time.UTC)) // carried in
	m.report(t, 5, time.Date(2026, time.February, 10, 15, 0, 0, 0, time.UTC))
	m.report(t, 1, time.Date(2026, time.February, 20, 15, 0, 0, 0, time.UTC))
	inv := m.close(t, billing.Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)})
	if inv.Total != 4*490 {
		t.Fatalf("total = %d, want %d", inv.Total, 4*490)
	}
}

// Review Focus 3: the level was reported long ago and never changed; its
// report is gone (TTL), and the period is still billed on it.
func TestALevelOlderThanItsTTLIsStillCarriedIn(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.January, 1))
	at := time.Date(2024, time.November, 15, 15, 0, 0, 0, time.UTC)
	m.report(t, 3, at)
	// Simulate the TTL: delete every report of the meter, keep the latest item.
	base := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	recs, _ := m.levels.InPeriod(ctxT(t), m.org.ID, true, m.ref, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	for _, r := range recs {
		_, _ = base.DeleteItem(ctxT(t), repositories.LevelPK(m.org.ID, true, m.ref, "finance_spaces"), repositories.LevelSK(r.OccurredAt, r.IdempotencyKey))
	}
	jan := billing.Period{Start: brcal.New(2026, time.January, 1), End: brcal.New(2026, time.February, 1)}
	if inv := m.close(t, jan); inv.Total != 2*490 {
		t.Fatalf("january = %d, want %d", inv.Total, 2*490)
	}
}

// The old report expired, and the level changed during the period: the
// report's own `previous` is the carried-in level.
func TestAnExpiredCarriedInLevelIsReadFromTheFirstReportsPrevious(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.January, 1))
	old := time.Date(2024, time.November, 15, 15, 0, 0, 0, time.UTC)
	m.report(t, 6, old)
	_, _ = repositories.NewBase(testDB, testCfg, repositories.TableUsage).DeleteItem(ctxT(t),
		repositories.LevelPK(m.org.ID, true, m.ref, "finance_spaces"), repositories.LevelSK(old, m.lastKey))
	m.report(t, 2, time.Date(2026, time.January, 20, 15, 0, 0, 0, time.UTC))
	jan := billing.Period{Start: brcal.New(2026, time.January, 1), End: brcal.New(2026, time.February, 1)}
	if inv := m.close(t, jan); inv.Total != 5*490 {
		t.Fatalf("january = %d, want %d (6 held until the 20th)", inv.Total, 5*490)
	}
}

func TestAMaxPriceOnACustomerWithoutRefFailsLoudly(t *testing.T) {
	m := newMaxFixture(t, brcal.New(2026, time.February, 1))
	m.sub.CustomerID = "cus_missing"
	if _, err := m.invoicer.GenerateForPeriod(ctxT(t), m.sub, m.items,
		billing.Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)}, "scheduler", now()); err == nil {
		t.Fatal("a max price with no customer reference must not bill 0")
	}
}
```

The existing `multi_item_test.go` / `invoicing_test.go` sum tests are the spec test 6 regression — they must pass unchanged.

- [ ] **Step 2: Run** → FAIL (`WithLevels` undefined).

- [ ] **Step 3: Implement** in `invoicing.go`: fields `levels *repositories.LevelRepository; customers *repositories.CustomerRepository`;

```go
// ErrLevelsNotWired is a max price closed by an Invoicer built without levels.
var ErrLevelsNotWired = errors.New("level metering is not wired into this invoicer")

// WithLevels lets the invoicer bill prices aggregated by max (spec § 6.3). A
// nil one refuses them rather than billing 0.
func (s *Invoicer) WithLevels(levels *repositories.LevelRepository, customers *repositories.CustomerRepository) *Invoicer {
	s.levels, s.customers = levels, customers
	return s
}
```

In `buildLine`, before the usage read:

```go
	if price.Aggregation == billing.AggregationMax {
		units, err := s.maxLevel(ctx, sub, price, period)
		if err != nil {
			return billing.InvoiceItem{}, fmt.Errorf("levels for item %s: %w", item.ID, err)
		}
		return billing.MeteredLine(price, product.Name, period, units), nil
	}
```

and

```go
// maxLevel is the highest level of the price's meter for the subscription's
// customer reference during period (spec § 6.3). A customer with no external
// reference cannot have levels, and that is an error, not a free month.
func (s *Invoicer) maxLevel(ctx context.Context, sub *billing.Subscription, price *billing.Price, period billing.Period) (int64, error) {
	if s.levels == nil || s.customers == nil {
		return 0, ErrLevelsNotWired
	}
	customer, err := s.customers.Get(ctx, sub.OrganizationID, sub.Livemode, sub.CustomerID)
	if err != nil {
		return 0, err
	}
	if customer.ExternalRef == "" {
		return 0, fmt.Errorf("customer %s has no external reference to read %s levels under", customer.ID, price.Meter)
	}
	carried, err := s.carriedIn(ctx, sub, customer.ExternalRef, price.Meter, period.Start.Time())
	if err != nil {
		return 0, err
	}
	records, err := s.levels.InPeriod(ctx, sub.OrganizationID, sub.Livemode, customer.ExternalRef, price.Meter, period.Start.Time(), period.End.Time())
	if err != nil {
		return 0, err
	}
	return billing.MaxLevel(records, carried, period), nil
}

// carriedIn is the level held at start (scope decision 9): the newest in-TTL
// report before it; else the `previous` of the first report from it; else the
// no-TTL latest level, which is then before start; else 0.
func (s *Invoicer) carriedIn(ctx context.Context, sub *billing.Subscription, ref, meter string, start time.Time) (int64, error) {
	if before, err := s.levels.LatestBefore(ctx, sub.OrganizationID, sub.Livemode, ref, meter, start); err != nil || before != nil {
		if before == nil {
			return 0, err
		}
		return before.Value, nil
	}
	if first, err := s.levels.FirstFrom(ctx, sub.OrganizationID, sub.Livemode, ref, meter, start); err != nil || first != nil {
		if first == nil {
			return 0, err
		}
		return first.Previous, nil
	}
	latest, err := s.levels.Latest(ctx, sub.OrganizationID, sub.Livemode, ref, meter)
	if err != nil || latest == nil {
		return 0, err
	}
	return latest.Value, nil
}
```

`app.go`: `invoicer := services.NewInvoicer(...).WithOrganizations(orgs).WithLevels(levels, customers)` (move `levels :=` above it); in `BuildInvoicer` build `customers := repositories.NewCustomerRepository(db, cfg)` and chain `.WithLevels(repositories.NewLevelRepository(db, cfg), customers)`.

- [ ] **Step 4: Run** — `go test ./... && make test-integration` → PASS, the existing sum close tests included.
- [ ] **Step 5: Commit**

```bash
git add api/internal/services/invoicing.go api/internal/app/app.go api/tests/integration/max_close_test.go
git commit -m "feat(invoicing): a max price closes on the period's highest level"
```

---

### Task 8b: The sweep honours `cancel_at_period_end`

Found while planning the scheduled change (plan 2): `ScheduleCancellation` sets `cancel_at_period_end`, but **nothing executes it** — `Invoicer.invoiceOne` bills the next advance period and renews regardless, so a person who cancels at period end in the portal today is billed again. Plan 2's "Voltar ao Free" depends on this, and DF-e customers are exposed now, so it ships in deploy step 1.

**Files:**
- Modify: `api/internal/services/invoicing.go` (`invoiceOne`)
- Create: `api/tests/integration/sweep_cancel_test.go`

**Interfaces:**
- Consumes: `SubscriptionRepository.Transition`, `ScheduleCancellation`, `billing.PeriodToInvoice`.
- Produces: `func (s *Invoicer) endAtBoundary(ctx context.Context, sub *billing.Subscription, items []billing.SubscriptionItem, actor string, now time.Time) error` — called first in `invoiceOne` when `sub.CancelAtPeriodEnd`. Plan 2 extends the same boundary step with scheduled changes.

- [ ] **Step 1: Write the failing integration tests** (anchors in 2033 so the cross-tenant sweep date belongs to these tests alone):

```go
//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestAnAdvanceSubscriptionCancelledAtPeriodEndIsNotBilledAgain(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	anchor := brcal.New(2033, time.July, 19)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "user:x", "r", now()); err != nil {
		t.Fatal(err)
	}
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	res := inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now())
	if res.Failed != 0 {
		t.Fatalf("sweep: %+v", res.Errors)
	}
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	if got.Status != billing.SubscriptionCanceled {
		t.Fatalf("status = %s", got.Status)
	}
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if len(invoices) != 1 { // the first period's, from Subscribe
		t.Fatalf("%d invoices, want 1", len(invoices))
	}
}

func TestAnArrearsSubscriptionCancelledAtPeriodEndBillsTheEndedPeriod(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 5, billing.IntervalMonth)
	anchor := brcal.New(2033, time.September, 23)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	_ = f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "user:x", "r", now())
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	if res := inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now()); res.Failed != 0 {
		t.Fatalf("sweep: %+v", res.Errors)
	}
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if got.Status != billing.SubscriptionCanceled || len(invoices) != 1 {
		t.Fatalf("status %s, %d invoices (want CANCELED and the ended period's)", got.Status, len(invoices))
	}
}
```

- [ ] **Step 2: Run** — `go test -tags integration ./tests/integration/ -run CancelledAtPeriodEnd -count=1 -v` → FAIL (status ACTIVE, two invoices).
- [ ] **Step 3: Implement** — at the top of `invoiceOne`, after the items are read:

```go
	if sub.CancelAtPeriodEnd {
		return s.endAtBoundary(ctx, sub, items, actor, now)
	}
```

and

```go
// endAtBoundary executes a cancellation scheduled for the period end. In
// arrears the period that just ended was served and is billed now; in advance
// the period that would start is not billed. Re-runnable: an already generated
// period and an already canceled subscription are not failures.
func (s *Invoicer) endAtBoundary(ctx context.Context, sub *billing.Subscription, items []billing.SubscriptionItem, actor string, now time.Time) error {
	if sub.Timing == billing.BillArrears {
		if _, err := s.GenerateForPeriod(ctx, sub, items, billing.PeriodToInvoice(sub), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	if _, err := s.subs.Transition(ctx, sub, billing.SubscriptionCanceled, billing.CauseScheduler, actor, "", now); err != nil &&
		!errors.Is(err, repositories.ErrConcurrentModification) && !errors.Is(err, billing.ErrInvalidTransition) {
		return err
	}
	return nil
}
```

`ACTIVE → CANCELED` already accepts `CauseScheduler`; a `PAST_DUE` subscription with the flag is left to dunning (its edge has no scheduler cause), which `ErrInvalidTransition` above lets through. `Transition` to `CANCELED` already removes `schedule_pk`/`schedule_sk` (`sweepable` is false), so the sweep stops finding it.

- [ ] **Step 4: Run** — `go test ./... && make test-integration` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/services/invoicing.go api/tests/integration/sweep_cancel_test.go
git commit -m "fix(invoicing): a subscription cancelled at period end ends at the boundary"
```

---

### Task 9: `ORG_` customers and the `CUSTOMER_ORG#` pointer

**Files:**
- Modify: `api/internal/repositories/keys.go`, `keys_test.go`, `rows.go`, `customers.go`, `api/internal/api/v1/handlers.go` (`createCustomerAs`), `api/internal/problem/problem.go` (`ErrUserAlreadyCustomer` → 409)
- Create: `api/tests/integration/org_customers_test.go`

**Interfaces:**
- Produces: `const repositories.OrgRefPrefix = "ORG_"`; `func repositories.OrganizationOfRef(ref string) (string, bool)`; `func repositories.CustomerOrgSK(organizationID string) string`; `repositories.ErrOrganizationAlreadyCustomer`; `func (r *CustomerRepository) GetByOrganization(ctx context.Context, organizationID string, livemode bool, accountOrganizationID string) (*billing.Customer, error)`.

- [ ] **Step 1: Write the failing tests.** `keys_test.go`:

```go
func TestOrganizationOfRef(t *testing.T) {
	id, ok := OrganizationOfRef("ORG_0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	if !ok || id != "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" {
		t.Fatal(id, ok)
	}
	for _, ref := range []string{"ORG_acme", "ORG_0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B", "USER_x", "org_0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} {
		if _, ok := OrganizationOfRef(ref); ok {
			t.Errorf("%q read as an organization", ref)
		}
	}
}
```

`org_customers_test.go` (spec § 9 test 8):

```go
//go:build integration

package integration

import (
	"net/http"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newOrgRef() (string, string) {
	org := newSpaceOrgID()
	return org, "ORG_" + org
}

func TestCreatingAnOrganizationCustomerTwiceIsOneCustomer(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	org, ref := newOrgRef()
	body := `{"name":"Acme LTDA","tax_id":"11222333000181","external_ref":"` + ref + `"}`
	first := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), body)
	second := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), body)
	if first.status != http.StatusCreated || second.status != http.StatusOK {
		t.Fatalf("first %d %s, second %d %s", first.status, first.body, second.status, second.body)
	}
	var a, b struct{ ID string `json:"id"` }
	first.decode(t, &a)
	second.decode(t, &b)
	if a.ID != b.ID {
		t.Fatalf("two customers: %s, %s", a.ID, b.ID)
	}
	got, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctxT(t), e.org.ID, true, org)
	if err != nil || got.ID != a.ID {
		t.Fatalf("pointer = %+v, %v", got, err)
	}
}

// The pointer and the customer are one transaction: a taken pointer leaves no
// orphan customer behind.
func TestATakenPointerWritesNoCustomer(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	repo := repositories.NewCustomerRepository(testDB, testCfg)
	_, ref := newOrgRef()
	first := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true, Name: "A", ExternalRef: ref}
	if err := repo.Create(ctx, first, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	second := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true, Name: "B", ExternalRef: ref}
	if err := repo.Create(ctx, second, "test", "r", now()); !errors.Is(err, repositories.ErrOrganizationAlreadyCustomer) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := repo.Get(ctx, org.ID, true, second.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("an orphan customer was written: %v", err)
	}
}

// Review Focus 4.
func TestAnOrganizationCustomerCannotCarryAUser(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	_, ref := newOrgRef()
	r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Acme","external_ref":"`+ref+`","user_id":"usr_1"}`)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// A second customer for an account that already is one was a 500.
func TestASecondCustomerForOneUserIsA409(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	user := "usr_" + id.New()
	if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana","user_id":"`+user+`"}`); r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.body)
	}
	r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana","user_id":"`+user+`"}`)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "user_already_customer") {
		t.Fatalf("second: %d %s", r.status, r.body)
	}
}

func TestAnOrgPrefixThatIsNotAnIDIsAnOrdinaryRef(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	for i := 0; i < 2; i++ {
		if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Acme","external_ref":"ORG_acme"}`); r.status != http.StatusCreated {
			t.Fatalf("%d: %d %s", i, r.status, r.body)
		}
	}
}
```

(imports: `errors`, `strings`.) `newSpaceOrgID` already exists in the finance integration tests (used by `finance_ledger_test.go`); reuse it.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `keys.go`: add `skCustomerOrg = "CUSTOMER_ORG#"` to the sort-key prefixes and

```go
// OrgRefPrefix marks an external reference that names a ctech-account
// organization (spec § 8).
const OrgRefPrefix = "ORG_"

// OrganizationOfRef reads ORG_{organization_id}. Only a canonical organization
// id counts: any other ORG_… is an ordinary external reference, so a merchant
// already using the prefix is unaffected.
func OrganizationOfRef(ref string) (string, bool) {
	id, ok := strings.CutPrefix(ref, OrgRefPrefix)
	if !ok || !space.IsOrganizationID(id) {
		return "", false
	}
	return id, true
}

// CustomerOrgSK is the pointer from a ctech-account organization to its
// customer, the organization counterpart of CustomerUserSK (ADR 0025,
// 2026-10-07 amendment).
func CustomerOrgSK(organizationID string) string { return skCustomerOrg + organizationID }
```

(import `space` in `keys.go` — `repositories` already imports it elsewhere, so no cycle.) `rows.go`:

```go
// customerOrgRow points a ctech-account organization at its customer, written
// in the customer's transaction and conditionally, like customerUserRow.
type customerOrgRow struct {
	keys
	AccountOrganizationID string `dynamodbav:"account_organization_id"`
	CustomerID            string `dynamodbav:"customer_id"`
}
```

`customers.go`: `var ErrOrganizationAlreadyCustomer = errors.New("organization is already a customer of this organization")`. In `Create`, after the user pointer block:

```go
	orgID, isOrg := OrganizationOfRef(c.ExternalRef)
	if isOrg {
		pointer, err := Encode(customerOrgRow{
			keys:                  newKeys(TenantPK(c.OrganizationID, c.Livemode), CustomerOrgSK(orgID), RetentionCustomer, now),
			AccountOrganizationID: orgID,
			CustomerID:            c.ID,
		})
		if err != nil {
			return err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(pointer))
	}
```

and the condition branch:

```go
	if IsConditionFailed(err) {
		if isOrg {
			return fmt.Errorf("%w: %s", ErrOrganizationAlreadyCustomer, orgID)
		}
		if c.UserID != "" {
			return fmt.Errorf("%w: %s", ErrUserAlreadyCustomer, c.UserID)
		}
		return fmt.Errorf("customer %s already exists", c.ID)
	}
	return conflictErr(err)
```

`GetByOrganization` mirrors `GetByUser` with `CustomerOrgSK` and `Decode[customerOrgRow]`. `problem.go`, beside the other customer errors:

```go
	case errors.Is(err, repositories.ErrUserAlreadyCustomer):
		return New(409, TypeInvalidTransition, "Already a Customer",
			"this account is already a customer of this organization").WithCode("user_already_customer")
``` `handlers.go` `createCustomerAs`, after the existing checks:

```go
	orgRef, isOrg := repositories.OrganizationOfRef(req.ExternalRef)
	if isOrg && req.UserID != "" {
		ch.fail("user_id", "not_allowed", "an organization customer has no user")
	}
```

and after `Create`:

```go
	if err := h.customers.Create(c.Context(), customer, actor, middleware.GetRequestID(c), h.now()); err != nil {
		if isOrg && errors.Is(err, repositories.ErrOrganizationAlreadyCustomer) {
			// Creating again for the same organization returns it (spec § 8).
			existing, gerr := h.customers.GetByOrganization(c.Context(), t.OrganizationID, t.Livemode, orgRef)
			if gerr != nil {
				return fail(c, gerr)
			}
			return c.Status(fiber.StatusOK).JSON(newCustomerResponse(existing))
		}
		return fail(c, err)
	}
```

- [ ] **Step 4: Run** — `go test ./... && go test -tags integration ./tests/integration/ -run 'Organization|OrgPrefix|TakenPointer|OneAccountCannotBeTwo|SecondCustomer' -count=1 -v` → PASS. (Check `TestOneAccountCannotBeTwoCustomers` in `portal_test.go`: it asserts the repository error, not a status, so it is unaffected.)
- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories api/internal/api/v1/handlers.go api/tests/integration/org_customers_test.go
git commit -m "feat(customers): ORG_ customers with a CUSTOMER_ORG# pointer in the same transaction"
```

---

### Task 10: Only `USER_` invoices post to *Pessoal* (6.7)

**Files:**
- Modify: `api/internal/services/finance_invoices.go`, `api/internal/services/finance_invoices_test.go`

**Interfaces:** Consumes `repositories.OrganizationOfRef` (Task 9). Produces skip reason `payer_is_organization`.

- [ ] **Step 1: Write the failing test** (spec § 9 test 10):

```go
// An organization's invoice is nobody's personal expense, even if a user id
// was stored on it somehow.
func TestAnOrganizationInvoiceIsNobodysPersonalExpense(t *testing.T) {
	f := newInvoiceFixture()
	f.customers["cus_1"].ExternalRef = "ORG_" + linkedOrg
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	wantResult(t, out, SidePayer, PostingSkipped, "payer_is_organization")
	wantResult(t, out, SideIssuer, PostingPosted, "")
	if _, ok := f.books.recorded[payerPK]; ok {
		t.Fatal("an expense was posted to a personal space")
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/services/ -run OrganizationInvoice -v` → FAIL.
- [ ] **Step 3: Implement** — in `payer`, the `switch` becomes:

```go
	switch {
	case c.Anonymized:
		return skipped(SidePayer, "payer_anonymized")
	case isOrganizationCustomer(c):
		// Spec § 8: only a USER_ customer's invoice is a person's expense.
		return skipped(SidePayer, "payer_is_organization")
	case c.UserID == "":
		return skipped(SidePayer, "payer_has_no_account")
	}
```

with `func isOrganizationCustomer(c *billing.Customer) bool { _, ok := repositories.OrganizationOfRef(c.ExternalRef); return ok }`. Remove the stale "Organization customers (spec § 1) are not modelled yet" comment.

- [ ] **Step 4: Run** — `go test ./internal/services/ -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/services/finance_invoices.go api/internal/services/finance_invoices_test.go
git commit -m "fix(finance): an organization's CTech invoice is not posted to anyone's Pessoal"
```

---

### Task 11: The portal selector (API)

**Files:**
- Modify: `api/internal/middleware/portal.go`, `api/internal/api/v1/router.go`, `api/internal/api/v1/portal.go`
- Create: `api/internal/middleware/portal_test.go`, `api/internal/api/v1/portal_spaces.go`, `api/tests/integration/portal_spaces_test.go`

**Interfaces:**
- Consumes: `space.Resolver`, `space.ParseSelector`, `space.VerbsFor`, `GetByOrganization` (Task 9), `spaceLister` (existing).
- Produces: `type middleware.PortalCustomers interface { GetByUser(ctx, organizationID string, livemode bool, userID string) (*billing.Customer, error); GetByOrganization(ctx, organizationID string, livemode bool, accountOrganizationID string) (*billing.Customer, error) }`; `ResolvePortalIdentity(customers PortalCustomers, organizationID string, spaces *space.Resolver) fiber.Handler`; `GET /v1.0/portal/spaces` → `{spaces: [{selector, display_name, role?}], organizations_unavailable}`.

- [ ] **Step 1: Write the failing middleware tests** — `portal_test.go` (spec § 9 test 9; zero reads before membership):

```go
package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type kinded struct {
	calls int
	m     map[string][2]string // "org|user" -> {kind, role}
}

func (k *kinded) Membership(_ context.Context, org, user string) (string, string, bool, error) {
	k.calls++
	a, ok := k.m[org+"|"+user]
	return a[0], a[1], ok, nil
}

type portalCustomers struct{ reads int }

func (p *portalCustomers) GetByUser(_ context.Context, _ string, _ bool, user string) (*billing.Customer, error) {
	p.reads++
	return &billing.Customer{ID: "cus_" + user}, nil
}
func (p *portalCustomers) GetByOrganization(_ context.Context, _ string, _ bool, org string) (*billing.Customer, error) {
	p.reads++
	if org == orgA {
		return &billing.Customer{ID: "cus_org"}, nil
	}
	return nil, repositories.ErrNotFound
}

func portalApp(src *kinded, cust *portalCustomers) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.Locals(ClaimsKey, &Claims{Sub: "alice", SID: "s"}); return c.Next() })
	app.Use(ResolvePortalIdentity(cust, "ctech", space.NewResolver(src, nil)))
	app.Get("/p", func(c fiber.Ctx) error { return c.SendString(GetCustomer(c).ID) })
	return app
}

func portalGet(t *testing.T, app *fiber.App, sel string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/p", nil)
	if sel != "" {
		req.Header.Set(SpaceHeader, sel)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 512)
	n, _ := resp.Body.Read(b)
	return resp.StatusCode, string(b[:n])
}

func TestThePortalWithoutASelectorIsThePerson(t *testing.T) {
	code, body := portalGet(t, portalApp(&kinded{}, &portalCustomers{}), "")
	if code != 200 || body != "cus_alice" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAnOwnerOrAdminOfAnOrganizationSeesItsCustomer(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		src := &kinded{m: map[string][2]string{orgA + "|alice": {"organization", role}}}
		if code, body := portalGet(t, portalApp(src, &portalCustomers{}), "org:"+orgA); code != 200 || body != "cus_org" {
			t.Fatalf("%s: %d %s", role, code, body)
		}
	}
}

// Review Focus 5 (and spec test 9): member, viewer, a personal workspace and a
// stranger all get the same 404, and nothing was read before the membership.
func TestAMemberOrViewerGetsTheSame404(t *testing.T) {
	var first string
	for name, m := range map[string]map[string][2]string{
		"member":   {orgA + "|alice": {"organization", "member"}},
		"viewer":   {orgA + "|alice": {"organization", "viewer"}},
		"personal": {orgA + "|alice": {"personal", "owner"}},
		"stranger": {},
	} {
		cust := &portalCustomers{}
		code, body := portalGet(t, portalApp(&kinded{m: m}, cust), "org:"+orgA)
		if code != 404 || cust.reads != 0 {
			t.Fatalf("%s: %d %s, %d reads", name, code, body, cust.reads)
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Fatalf("%s: body differs: %s vs %s", name, body, first)
		}
	}
}

func TestAMalformedOrganizationSelectorIsTheSame404(t *testing.T) {
	cust := &portalCustomers{}
	if code, _ := portalGet(t, portalApp(&kinded{}, cust), "org:USER#bob"); code != 404 || cust.reads != 0 {
		t.Fatalf("%d, %d reads", code, cust.reads)
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/middleware/ -run Portal -v` → FAIL (signature).

- [ ] **Step 3: Implement** — `portal.go`:

```go
// PortalCustomers is what the portal reads to find "the signed-in customer".
type PortalCustomers interface {
	GetByUser(ctx context.Context, organizationID string, livemode bool, userID string) (*billing.Customer, error)
	GetByOrganization(ctx context.Context, organizationID string, livemode bool, accountOrganizationID string) (*billing.Customer, error)
}

// ResolvePortalIdentity turns a signed-in person — and, with X-Billing-Space:
// org:{id}, an organization they own or administer — into the customer the
// portal shows (ADR 0012, ADR 0025 2026-10-07 amendment). The selector goes
// through the console's resolver first: no table is read before membership, and
// every refusal (not a member, member/viewer, a personal workspace, no such
// organization, a malformed id) is the same 404.
func ResolvePortalIdentity(customers PortalCustomers, organizationID string, spaces *space.Resolver) fiber.Handler {
	return func(c fiber.Ctx) error {
		if organizationID == "" {
			return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
		}
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
		}

		lookup := func() (*billing.Customer, error) {
			return customers.GetByUser(c.Context(), organizationID, true, cl.Sub)
		}
		if raw := c.Get(SpaceHeader); raw != "" && raw != "personal" {
			sel, err := space.ParseSelector(raw)
			if err != nil {
				if errors.Is(err, space.ErrSpaceNotFound) {
					return spaceNotFound().Send(c)
				}
				return problem.BadRequest("informe " + SpaceHeader + ": personal ou org:{id}").Send(c)
			}
			if spaces == nil {
				return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable, "Space unavailable", "could not verify access right now").Send(c)
			}
			sp, err := spaces.Resolve(c.Context(), cl.Sub, sel, true)
			switch {
			case errors.Is(err, space.ErrSpaceNotFound):
				return spaceNotFound().Send(c)
			case errors.Is(err, space.ErrInvalidSubject):
				return problem.Unauthorized("invalid credentials").WithCode("invalid_credentials").Send(c)
			case err != nil:
				return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable, "Space unavailable", "could not verify access right now").Send(c)
			}
			// owner and admin are the organization roles that hold Configure
			// (space.VerbsFor); a personal workspace has no CTech subscription.
			if sp.Kind() != space.KindOrganization || !sp.Can(space.Configure) {
				return spaceNotFound().Send(c)
			}
			lookup = func() (*billing.Customer, error) {
				return customers.GetByOrganization(c.Context(), organizationID, true, sel.OrganizationID)
			}
		}

		customer, err := lookup()
		// …the existing ErrNotFound → TypeNoBillingAccount 403, other → 500,
		// Anonymized → 403 handling, unchanged…
		c.Locals(CustomerKey, customer)
		c.Locals(TenantKey, Tenant{OrganizationID: organizationID, Livemode: true})
		return c.Next()
	}
}
```

(keep the existing error handling body verbatim in place of the comment; add imports `context`, `space`.) `router.go` `registerPortal`:

```go
	ph := &portalHandlers{handlers: h, collector: d.Collector, bus: d.SettlementBus, spaces: d.SpaceLister, portalOrg: d.PortalOrganizationID}
	// Before the group: the list must answer for a person who is not themselves
	// a customer but administers an organization that is (scope decision 12).
	v1.Get("/portal/spaces", auth, middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.listSpaces)
	identity := middleware.ResolvePortalIdentity(d.Customers, d.PortalOrganizationID, d.Spaces)
```

`portal_spaces.go`:

```go
package v1

import (
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type portalSpaceDTO struct {
	Selector    string `json:"selector"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role,omitempty"`
}

// listSpaces is the portal's selector: Pessoal, then the organizations (kind
// organization only) where the token's subject is owner or admin. Information
// only — ResolvePortalIdentity re-authorizes every request.
func (h *portalHandlers) listSpaces(c fiber.Ctx) error {
	if h.portalOrg == "" {
		return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
	}
	out := struct {
		Spaces                   []portalSpaceDTO `json:"spaces"`
		OrganizationsUnavailable bool             `json:"organizations_unavailable"`
	}{Spaces: []portalSpaceDTO{{Selector: "personal", DisplayName: "Pessoal"}}}
	if h.spaces == nil {
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	ws, err := h.spaces.Organizations(c.Context(), middleware.GetClaims(c).Sub)
	if err != nil {
		slog.Warn("portal: listing organizations failed", "error", err)
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	var orgs []spaceDTO
	for _, w := range ws {
		kind := space.WorkspaceKind(w.Kind)
		if kind != space.KindOrganization || !space.IsOrganizationID(w.ID) || !space.VerbsFor(kind, w.Role).Has(space.Configure) {
			continue
		}
		orgs = append(orgs, spaceDTO{Selector: "org:" + w.ID, DisplayName: w.DisplayName, Role: w.Role})
	}
	sortByName(orgs)
	for _, o := range orgs {
		out.Spaces = append(out.Spaces, portalSpaceDTO{Selector: o.Selector, DisplayName: o.DisplayName, Role: o.Role})
	}
	return c.JSON(out)
}
```

(`sortByName` takes `[]spaceDTO`, which is why the organizations are collected and sorted as `spaceDTO` first.) `portal.go`: add `spaces spaceLister` and `portalOrg string` to `portalHandlers`; replace `"user:"+customer.UserID` in `payInvoice` and `cancelSubscription` with `actorOfUser(c)`.

- [ ] **Step 4: Integration tests** — `portal_spaces_test.go`, reusing `fakeAccount` (finance_shared_spaces_test.go) and `newPortal`:

```go
//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type portalSpacesEnv struct {
	*portalEnv
	account *fakeAccount
}

func newPortalSpaces(t *testing.T) portalSpacesEnv {
	t.Helper()
	p := newPortal(t)
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}}
	srv := acct.serve(t)
	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL, cfg.CtechJWKSURL, cfg.ServiceAudience = testIssuer, p.jwksURL, testAudience
	cfg.PortalOrganizationID = p.org.ID
	cfg.AccountBaseURL, cfg.AccountTokenURL = srv.URL, srv.URL+"/token"
	cfg.AccountClientID, cfg.AccountClientSecret = "billing-test", "secret"
	server, err := app.Build(ctxT(t), &cfg, func() time.Time { return now() })
	if err != nil {
		t.Fatal(err)
	}
	p.app = server
	return portalSpacesEnv{portalEnv: p, account: acct}
}

func (e portalSpacesEnv) get(t *testing.T, path, token, selector string) apiResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if selector != "" {
		req.Header.Set(middleware.SpaceHeader, selector)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return apiResponse{status: resp.StatusCode, body: []byte(b.String())}
}

// Review Focus 5.
func TestAnAdminWhoIsNotACustomerCanListAndPick(t *testing.T) {
	e := newPortalSpaces(t)
	ctx := ctxT(t)
	accountOrg := newSpaceOrgID()
	admin := "usr_" + id.New()
	e.account.set(accountOrg, admin, "organization", "admin")
	e.account.names[accountOrg] = "Acme"
	orgCustomer := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true, Name: "Acme LTDA", ExternalRef: "ORG_" + accountOrg}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, orgCustomer, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	inv := newInvoiceFor(t, e.org, orgCustomer.ID)
	due := brcal.New(2026, time.March, 10)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Finalize(ctx, inv, due, due, billing.CauseScheduler, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	tok := e.token(t, admin, "sess_"+id.New(), middleware.ScopeMySubscriptionsRead, middleware.ScopeMyInvoicesRead)
	list := e.get(t, "/v1.0/portal/spaces", tok, "")
	if list.status != 200 || !strings.Contains(string(list.body), "org:"+accountOrg) {
		t.Fatalf("spaces: %d %s", list.status, list.body)
	}
	if r := e.get(t, "/v1.0/portal/invoices", tok, "org:"+accountOrg); r.status != 200 || !strings.Contains(string(r.body), inv.ID) {
		t.Fatalf("invoices: %d %s", r.status, r.body)
	}
	if r := e.get(t, "/v1.0/portal/invoices", tok, ""); r.status != http.StatusForbidden {
		t.Fatalf("personal, no customer: %d", r.status)
	}
}

func TestPortalSpacesListOnlyOrganizationsTheyManage(t *testing.T) {
	e := newPortalSpaces(t)
	user := "usr_" + id.New()
	managed, member, personal := newSpaceOrgID(), newSpaceOrgID(), newSpaceOrgID()
	e.account.set(managed, user, "organization", "owner")
	e.account.set(member, user, "organization", "member")
	e.account.set(personal, user, "personal", "owner")
	r := e.get(t, "/v1.0/portal/spaces", e.token(t, user, "s", middleware.ScopeMySubscriptionsRead), "")
	body := string(r.body)
	if r.status != 200 || !strings.Contains(body, managed) || strings.Contains(body, member) || strings.Contains(body, personal) {
		t.Fatalf("%d %s", r.status, body)
	}
}

func TestAForgedPersonalWorkspaceIsA404(t *testing.T) {
	e := newPortalSpaces(t)
	user := "usr_" + id.New()
	ws := newSpaceOrgID()
	e.account.set(ws, user, "personal", "owner")
	if r := e.get(t, "/v1.0/portal/invoices", e.token(t, user, "s", middleware.ScopeMyInvoicesRead), "org:"+ws); r.status != 404 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
```

(imports: add `io` and `gopkg.aoctech.app/billing/api/internal/domain/brcal`; the finalize call is the one `TestPortalShowsOnlyTheSignedInCustomersInvoices` uses.)

- [ ] **Step 5: Run** — `go test ./... && make test-integration` → PASS (the existing portal tests unchanged).
- [ ] **Step 6: Commit**

```bash
git add api/internal/middleware/portal.go api/internal/middleware/portal_test.go api/internal/api/v1/router.go api/internal/api/v1/portal.go api/internal/api/v1/portal_spaces.go api/tests/integration/portal_spaces_test.go
git commit -m "feat(portal): an organization's owners and admins select it in the portal"
```

---

### Task 12: The portal selector (UI) — with `/impeccable`

**Files:**
- Create: `ui/src/lib/portal/space.ts`, `ui/src/lib/portal/space.test.ts`, `ui/src/components/portal/PortalSpaceSwitch.tsx`, `ui/src/components/portal/PortalSpaceSwitch.test.tsx`
- Modify: `ui/src/lib/api/portal.ts`, `ui/src/lib/api/types.ts`, `ui/src/app/(portal)/layout.tsx`, `ui/src/components/portal/NoBillingAccount.tsx`, `ui/src/dev/mockData.ts` (the portal mock), `ui/src/locales/{pt-BR,en}/*.json`

**Interfaces:**
- Consumes: `GET /v1.0/portal/spaces` (Task 11); `parseSpace`, `spaceHeader`, `Space` from `@/lib/console/space` (same grammar, **separate storage**).
- Produces: `getPortalSpace(): Space`, `setPortalSpace(s: Space)`, `onPortalSpaceChange(fn)`, `usePortalSpace()` (stored under `ctech-billing-portal-space`, never shared with the finance selection); `listPortalSpaces(): Promise<PortalSpaces>`; every portal call sends `X-Billing-Space: spaceHeader(getPortalSpace())`.

- [ ] **Step 1: Run `/impeccable`** in `shape` mode for the portal header selector, with `ui/PRODUCT.md`, `ui/DESIGN.md` (portal is `comfortable` density, no organization was ever shown here — this is the first time), the contract below, and the existing `SpaceSwitch` (console) as the cross-surface reference. Check `@aoctech/ui` (`Select`, `Badge`) and ctech-ui for an existing switcher before drawing one. Write the outcome under "Portal" in `ui/DESIGN.md`.

Contract: shown only when the list has more than *Pessoal*; *Pessoal* first, organizations by name; the current choice visible in the header at 320px without truncating to nothing; switching cancels and resets every `["portal"]` query (`queryClient.cancelQueries({queryKey: ["portal"]})` then `resetQueries`), then routes to `/dashboard`; a stored organization missing from a loaded list (and `organizations_unavailable` false) falls back to *Pessoal* with a toast (`portal.space.lost`); a 404 `space-not-found` from any portal call does the same; on `NoBillingAccount` the switch stays visible, with the line "Escolha uma organização acima para ver as faturas dela." when the list has organizations.

- [ ] **Step 2: Write the failing tests.** `space.test.ts`:

```ts
import {beforeEach, describe, expect, it} from "vitest"

import {getPortalSpace, setPortalSpace} from "./space"
import {getSpace, PERSONAL} from "@/lib/console/space"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

describe("portal space", () => {
  beforeEach(() => window.localStorage.clear())

  it("defaults to personal and keeps a canonical organization", () => {
    expect(getPortalSpace()).toEqual(PERSONAL)
    setPortalSpace({kind: "organization", organizationId: ORG})
    expect(getPortalSpace()).toEqual({kind: "organization", organizationId: ORG})
  })

  it("never moves the finance selection", () => {
    setPortalSpace({kind: "organization", organizationId: ORG})
    expect(getSpace()).toEqual(PERSONAL)
  })
})
```

`PortalSpaceSwitch.test.tsx`: render with a mocked `apiClient.request`/`get` answering `/v1.0/portal/spaces` with Pessoal + one organization; assert the switch is present, choosing the organization stores `org:{id}`, and the next `listInvoices()` call carries `X-Billing-Space: org:{id}`; with only Pessoal, nothing renders. Use `renderWithQuery` from `src/components/finance/finance.test-utils.tsx` and `pick` for the `Select`.

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/portal src/components/portal/PortalSpaceSwitch.test.tsx; echo "vitest exit=$?"` → non-zero.

- [ ] **Step 3: Implement.** `space.ts` mirrors `lib/console/space.ts` (same `parseSpace`/`spaceHeader`, own `KEY = "ctech-billing-portal-space"` and `EVENT = "ctech-portal-space"`, try/catch around storage). In `portal.ts`:

```ts
import {spaceHeader} from "@/lib/console/space"
import {getPortalSpace} from "@/lib/portal/space"

/** The portal's selector (ADR 0025): read at call time, re-authorized by the server. */
const sel = () => ({headers: {"X-Billing-Space": spaceHeader(getPortalSpace())}})

export interface PortalSpaces {
  spaces: {selector: string; display_name: string; role?: string}[]
  organizations_unavailable: boolean
}

export async function listPortalSpaces(): Promise<PortalSpaces> {
  const {data} = await apiClient.get<PortalSpaces>("/v1.0/portal/spaces")
  return data
}
```

and pass `sel()` (merged with any existing `params`) to every `/v1.0/portal/*` call except `/portal/spaces` and `getHealth`. Add `portalKeys.spaces = ["portal-spaces"] as const` (outside the `["portal"]` prefix, so a switch does not reset the list itself). Build `PortalSpaceSwitch` from `@aoctech/ui` `Select` (spread `selectCopy()`), place it in the portal header per the design brief, and show it on `NoBillingAccount`. Strings in both locales: `portal.space.label` ("Conta" / "Account"), `portal.space.personal` ("Pessoal" / "Personal"), `portal.space.lost`, `portal.space.pickOrganization`. Add `/v1.0/portal/spaces` to the portal dev mock.

- [ ] **Step 4: Run** — `npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `exit=0`; `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build` → clean. Browser check on the mock (`npm run dev:mock`) at 320, 375 and 1280 px: switch visible, choice readable, invoices change on switch, back to Pessoal works.
- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/portal ui/src/lib/api/portal.ts ui/src/lib/api/types.ts ui/src/components/portal ui/src/app/\(portal\)/layout.tsx ui/src/dev ui/src/locales ui/DESIGN.md
git commit -m "feat(portal): pick Pessoal or an organization you manage"
```

---

### Task 13: Records

**Files:** Modify `PLAN.md`, `docs/specs/2026-10-10-plans-design.md`, `docs/adr/0025-spaces-personal-and-organization.md`.

- [ ] **Step 1:** `PLAN.md`: a new item under Phase 6 "Plans — deploy step 1 (billing)" with links to the spec and this plan, naming what shipped (catalogue, owner-scoped credentials incl. `dfe-billing`, entitlements `owner_key`/`default`, `usage/levels`, `MaxLevel` + the no-TTL latest level, `ORG_` customers, portal selector, 6.7 rule) and the **Found on the way** list: (1) `dfe-billing` was never owner-scoped — now it is; (2) live DF-e prices keep `quota_users` (immutable prices); (3) `cancel_at_period_end` was never executed by the sweep — now it is (Task 8b); (4) `ErrUserAlreadyCustomer` was a 500 — now 409; (5) scoped credentials may still write subscriptions for other owners. Replace "organization customers" in "Still open in Phase 6" with "deploy: run `seed` in both modes (credential owners, `prod_finance`, archive)".
- [ ] **Step 2:** Spec status line: "Deploy step 1 implemented (2026-10-10)"; § 4 gains one sentence on scope decision 3 (default and no-404 only when `owner_key` is sent); § 6.2 gains the fixed-width timestamp and the key marker; § 6.3 the carried-in order (scope decision 9).
- [ ] **Step 3:** ADR 0025: "Amendment, 2026-10-10 — the portal selector is built": header, resolver, owner/admin via `Configure`, `kind = organization` only, `/portal/spaces` outside the identity gate, the public checkout link unchanged.
- [ ] **Step 4: Verify** (sequentially): `gofmt -l ./internal ./cmd`, `go vet ./...`, `go test ./...`, `make test-integration`; then UI `npx vitest run --maxWorkers=2; echo "vitest exit=$?"`, `npx tsc --noEmit`, `npm run lint`, `npm run build`.
- [ ] **Step 5: Commit**

```bash
git add PLAN.md docs/specs/2026-10-10-plans-design.md docs/adr/0025-spaces-personal-and-organization.md
git commit -m "docs: plans deploy step 1 recorded"
```

---

## Spec coverage (self-review)

| Spec | Task |
|---|---|
| § 3 product, prices, `default_price_id` + seed refusal, `included_quantity` | 1, 3, 4 |
| § 4 `owner_key`, `default`, no 404, `account-billing` scoped to `finance` | 3, 4, 6, 7 |
| § 6.1 reporting, idempotency, 409, no customer created | 5, 6 |
| § 6.2 storage, TTL, one-Query latest | 5 |
| § 6.3 `MaxLevel`, close by aggregation, sum unchanged | 2, 8 |
| § 8 `ORG_` + pointer in one transaction, portal selector, checkout role, 6.7 | 9, 10, 11, 12 |
| § 9 tests 1–6, 8–10 | 7, 7, 3, 6/5, 2, 1/8, 9, 11, 10 |
| § 10 step 1, DF-e `quota_users`/archive | 4 |
| Amendment 2026-10-10: latest level without TTL | 2, 5, 8 |
| Amendment: people price quotas; DF-e companies monthly by peak | 4, 6 |
| Amendment: `ErrUserAlreadyCustomer` → 409 | 9 |
| Amendment: `cancel_at_period_end` executed at the boundary | 8b |

Spec § 7 and § 9 test 7 are plan 2.
