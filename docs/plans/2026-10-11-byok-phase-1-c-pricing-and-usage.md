# BYOK phase 1, part C: plans, quotas and usage of `billing_charges` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** BYOK collection is sold through `prod_billing` plans priced by the "cobrança emitida": the catalogue gets Grátis, Básico, Crescimento and Pro (fixed fee + metered overage on `billing_charges` via `included_quantity`), Grátis refuses past 50 charges (`byok_quota_exceeded`), every paid plan bills overage and never refuses, active subscriptions collected through BYOK are capped at the plan's charge quota (`subscription_limit_reached`), bank connections are capped by `quota_bank_connections`, and `cmd/byok` reports each live charge that survived 24 hours as one unit of `billing_charges` for the organization's `ORG_` customer in tenant zero, exactly once. A merchant chooses a plan in the console.

**Architecture:** No new invoicing feature: the existing metered price with `meter: billing_charges`, `aggregation: sum`, `included_quantity` and arrears billing computes `max(0, used − included) × unit_amount` (plans spec). The plan's limits are price metadata read through one `services.BillingPlans` resolver (the tenant's entitling `prod_billing` subscription → its prices → metadata). Counters are DynamoDB rows updated inside the same transactions that create a BYOK attempt or a subscription (`ADD` with a condition), so a limit is never raced past and nothing is counted by Query. Usage reporting is a sparse schedule partition of BYOK attempts read by `cmd/byok` step 6.

**Tech Stack:** as parts A and B.

**Spec:** [`docs/specs/2026-10-11-byok-inter-phase-1.md`](../specs/2026-10-11-byok-inter-phase-1.md) §§ 5 step 6, 7.1 (quota), 12.5, 12.6 (metadata of the internal price, part A), 17 items 3 (quota rows) and 15. Requires parts [A](2026-10-11-byok-phase-1-a-connection-and-statement.md) and [B](2026-10-11-byok-phase-1-b-collection.md) (consumes `ChargeGates.WithQuota`, `CreateByokAttempt`, `ByokJob.Register`, `PaymentAttempt.CreatedAt/CancelledAt/UsageReported`). Builds on [`docs/specs/2026-10-10-plans-design.md`](../specs/2026-10-10-plans-design.md) (metered prices, `included_quantity`, level metering).

## Scope decisions (read before executing)

1. **Sob demanda is not in the catalogue** (its automatic cap needs a new invoicing rule; deferred to the pricing plan, spec § 12.5). **Empresas is never in the catalogue** (per-customer price created by the operator).
2. **The quota counted for Grátis's refusal is "charges created this São Paulo month, minus those cancelled within 24 h"**: a counter row `BYOKCOUNT#{tenant}#{yyyy-mm}` in the `byok` table, `ADD 1` in `CreateByokAttempt`'s transaction (with `#n < :quota` as the condition when the plan refuses, so the 51st concurrent creation fails cleanly), `ADD -1` when an attempt is cancelled within 24 h of its creation. It is a gate counter only; billing (what the merchant pays) comes from usage records, which are what the invoice reads.
3. **The subscription cap counts active subscriptions of a live BYOK-rail tenant**: a counter row `BYOKSUBS#{tenant}` `ADD 1` in `SubscriptionRepository.Create`'s transaction when the tenant is on the BYOK rail (condition `#n < :quota`), `ADD -1` when a subscription reaches `CANCELED` or `ENDED`. Plan without quota (`-1`) or wallet rail: no counter write. A tenant moving onto the BYOK rail gets its counter initialised by `UseForCollection` from one paged Query of its subscriptions (once, at the switch; the only count by Query in this design).
4. **`quota_bank_connections` is enforced at connect** (live, collection or statement): connections of the organization in live across providers < quota. `quota_users` and `feature_api_access` are metadata only in phase 1: users live in ctech-account and API access is a ctech-account client grant; billing exposes them through `GET /v1.0/entitlements` for the products that enforce them, and enforcing them is a follow-up recorded in the PR.
5. **The plan screen is new**: `GET /v1.0/console/plan` lists the public `prod_billing` plans (prices whose `metadata.visibility != "internal"`, grouped by `metadata.plan`) with the tenant's current plan and this month's usage; `POST /v1.0/console/plan {plan}` changes the organization's entitling subscription with the existing `Subscriber.ChangePlan` (proration as today). Early access stays R$ 0 until the merchant chooses (spec § 12.5).
6. **Usage idempotency key = the attempt id.** A record already appended (`ErrDuplicateUsage`) is success; `usage_reported` is set on the attempt after, so a crash between the two re-appends and is deduplicated.
7. **Cost:** two counter writes per charge or subscription (inside existing transactions), one usage write per charge; no Query per create.

## Global Constraints

- Parts A and B's Global Constraints apply.
- Catalogue values, exactly (centavos): Grátis fee 0, 50 included, overage `refuse`; Básico fee 9990, 500 included, overage 25; Crescimento fee 29990, 2000 included, overage 18; Pro fee 69990, 5000 included, overage 15. Grátis: `quota_bank_connections` 1, `quota_users` 1. Paid plans' feature values (owner decision pending; defaults written here, change only the JSON): Básico `quota_bank_connections` 1, `quota_users` 3, `feature_api_access` false; Crescimento 2, 10, true; Pro 5, 25, true.
- Price ids: `price_billing_free_monthly`, `price_billing_basic_monthly`, `price_billing_basic_charges`, `price_billing_growth_monthly`, `price_billing_growth_charges`, `price_billing_pro_monthly`, `price_billing_pro_charges`. Plan keys: `free`, `basic`, `growth`, `pro` (display names "Grátis", "Básico", "Crescimento", "Pro" via i18n).
- Meter `billing_charges`, `aggregation: sum`, arrears; `quota_charges_month` = `included_quantity` (string), `-1` unlimited; `overage`: `bill` | `refuse`.
- Problem codes: `byok_quota_exceeded` (409, part B mapping, body adds `upgrade_plan`), `subscription_limit_reached` (409, `quota`), `bank_connection_limit_reached` (409, `quota`).

## Review Focus

1. **Fifty-one charges created at once on Grátis**: exactly fifty succeed; the rest answer `byok_quota_exceeded` and create no attempt. Task 3 `TestGratisNeverPassesFifty`.
2. **A charge cancelled 23 h after creation and another cancelled 25 h after**: the first is never reported, the second is reported once, whatever the number of job runs. Task 5 `TestUsageReportsSurvivorsOnce`.
3. **A paid plan past its included charges**: the 501st charge on Básico is created, and the next invoice bills `(n − 500) × R$ 0,25`. Task 3 `TestAPaidPlanNeverRefuses`, Task 5 `TestOverageIsBilledFromUsage`.
4. **Concurrent subscription creations at the cap**: never more active subscriptions than the quota. Task 4 `TestTheSubscriptionCapHoldsUnderConcurrency`.
5. **Test mode**: test charges never counted, capped or reported. Task 3 and Task 5 `…InTestModeNothingCounts`.

---

### Task 1: The catalogue (`api/tenants/ctech.json`)

**Files:** Modify `api/tenants/ctech.json`; Test `api/internal/provision/plan_test.go`.

- [ ] **Step 1: Write the failing test**

```go
func TestTheBillingPlansMatchTheSpecTable(t *testing.T) {
	plan := loadCTechPlan(t)
	type row struct{ fee, included, overage int64; refuse bool }
	want := map[string]row{"free": {0, 50, 0, true}, "basic": {9990, 500, 25, false}, "growth": {29990, 2000, 18, false}, "pro": {69990, 5000, 15, false}}
	for key, w := range want {
		fixed, metered := plan.BillingPlanPrices(key) // prices of prod_billing with metadata.plan == key
		if fixed == nil || fixed.UnitAmount != w.fee || fixed.Metadata["quota_charges_month"] != strconv.FormatInt(w.included, 10) {
			t.Fatalf("%s fee: %+v", key, fixed)
		}
		if (fixed.Metadata["overage"] == "refuse") != w.refuse {
			t.Fatalf("%s overage", key)
		}
		if w.refuse {
			if metered != nil {
				t.Fatalf("%s has no overage price", key)
			}
			continue
		}
		if metered == nil || metered.Meter != "billing_charges" || metered.IncludedQuantity != w.included || metered.UnitAmount != w.overage || metered.Timing != "arrears" {
			t.Fatalf("%s overage: %+v", key, metered)
		}
	}
	for _, p := range plan.Prices {
		if p.ProductID == "prod_billing" && (p.Metadata["plan"] == "ondemand" || p.Metadata["plan"] == "enterprise") {
			t.Fatalf("not in the catalogue: %s", p.ID)
		}
	}
}
```

- [ ] **Step 2: Run** → FAIL (`BillingPlanPrices` is a test helper added in this step's file: filter `plan.Prices` by product and `metadata.plan`, split fixed/metered).

- [ ] **Step 3: Add the prices** after `price_billing_unlimited_internal_monthly`:

```json
{"id": "price_billing_free_monthly", "product_id": "prod_billing", "type": "fixed", "unit_amount": 0, "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
 "metadata": {"plan": "free", "quota_charges_month": "50", "overage": "refuse", "quota_bank_connections": "1", "quota_users": "1", "feature_api_access": "false"}},
{"id": "price_billing_basic_monthly", "product_id": "prod_billing", "type": "fixed", "unit_amount": 9990, "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
 "metadata": {"plan": "basic", "quota_charges_month": "500", "overage": "bill", "quota_bank_connections": "1", "quota_users": "3", "feature_api_access": "false"}},
{"id": "price_billing_basic_charges", "product_id": "prod_billing", "type": "metered", "unit_amount": 25, "aggregation": "sum", "meter": "billing_charges", "included_quantity": 500,
 "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears", "metadata": {"plan": "basic"}},
{"id": "price_billing_growth_monthly", "product_id": "prod_billing", "type": "fixed", "unit_amount": 29990, "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
 "metadata": {"plan": "growth", "quota_charges_month": "2000", "overage": "bill", "quota_bank_connections": "2", "quota_users": "10", "feature_api_access": "true"}},
{"id": "price_billing_growth_charges", "product_id": "prod_billing", "type": "metered", "unit_amount": 18, "aggregation": "sum", "meter": "billing_charges", "included_quantity": 2000,
 "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears", "metadata": {"plan": "growth"}},
{"id": "price_billing_pro_monthly", "product_id": "prod_billing", "type": "fixed", "unit_amount": 69990, "recurrence": {"interval": "month", "count": 1}, "billing_timing": "advance",
 "metadata": {"plan": "pro", "quota_charges_month": "5000", "overage": "bill", "quota_bank_connections": "5", "quota_users": "25", "feature_api_access": "true"}},
{"id": "price_billing_pro_charges", "product_id": "prod_billing", "type": "metered", "unit_amount": 15, "aggregation": "sum", "meter": "billing_charges", "included_quantity": 5000,
 "recurrence": {"interval": "month", "count": 1}, "billing_timing": "arrears", "metadata": {"plan": "pro"}}
```

`price_billing_early_access` gains `"quota_charges_month": "-1", "overage": "bill", "quota_bank_connections": "-1"` (unlimited while early access lasts; R$ 0 unchanged).

- [ ] **Step 4: Run** `go test ./internal/provision/ -race -count=1` → `ok`; `api/cmd/seed/run.sh dev` against a local table succeeds (the provisioner validates metered prices: arrears, meter name).

- [ ] **Step 5: Commit** `git commit -m "feat(catalog): BYOK plans Grátis, Básico, Crescimento and Pro priced by cobrança emitida"`.

### Task 2: `BillingPlans` — the tenant's plan limits

**Files:** Create `api/internal/services/billing_plans.go`; Test `api/tests/integration/billing_plans_test.go`.

**Interfaces:**

```go
type PlanLimits struct{ Plan string; ChargesPerMonth int64 /* -1 unlimited */; RefuseOverage bool; BankConnections int64; Users int64; APIAccess bool; SubscriptionID string }
var Unlimited = PlanLimits{Plan: "unlimited", ChargesPerMonth: -1, BankConnections: -1, Users: -1, APIAccess: true}
type BillingPlans struct{ /* customers, subs, catalog, tenantZero */ }
func NewBillingPlans(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository, cat *repositories.CatalogRepository, tenantZero string) *BillingPlans
func (p *BillingPlans) For(ctx context.Context, accountOrgID string) (PlanLimits, bool /* entitled */, error)
```

Rules: tenant zero's own organization (the pilot) reads its subscription like everyone (it is on the internal unlimited price); no `ORG_` customer or no entitling `prod_billing` subscription → `(PlanLimits{}, false, nil)`; limits come from the subscription's **fixed** item price metadata; a missing key is unlimited (`-1`), so an old price never blocks. This replaces part B's `entitled` closure: `ChargeGates` is wired with `entitled = func(ctx, org) { _, ok, err := plans.For(ctx, org.AccountOrganizationID); return ok, err }`.

- [ ] **Steps:** test (subscribe the `ORG_` customer to each plan in a tenant-zero fixture and assert limits; early access unlimited; none → not entitled) FAIL → implement → `make test-integration` ok → commit `feat(billing): resolve a merchant's BYOK plan limits from its prod_billing subscription`.

### Task 3: The monthly charge counter and the Grátis refusal

**Files:**
- Modify: `api/internal/repositories/payments.go` (`CreateByokAttempt` takes `limit int64` and writes the counter), `api/internal/services/byok_collector.go` (passes the limit; cancellation within 24 h decrements), `api/internal/services/charge_gates.go` wiring (`WithQuota`)
- Create: `api/internal/repositories/byok_counters.go`
- Test: `api/tests/integration/byok_quota_test.go`

**Interfaces:**

```go
func ChargeCounterKey(tenantID string, month string) (pk, sk string) // pk "BYOKCOUNT#{tenant}", sk "{yyyy-mm}"
var ErrQuotaReached = errors.New("byok: the plan's charge quota is reached")
func (r *ByokRepository) ChargesThisMonth(ctx context.Context, tenantID string, month string) (int64, error)
// CreateByokAttempt(ctx, a, accountOrgID, now, limit int64): live only; limit < 0 means unconditional ADD;
// limit >= 0 adds the condition "attribute_not_exists(n) OR n < :limit" → ErrQuotaReached (mapped to billing.ErrByokQuotaExceeded)
func (r *PaymentRepository) MarkCancelled(ctx context.Context, a *billing.PaymentAttempt, now time.Time) error // sets cancelled_at; ADD -1 on the counter when now - created_at < 24h (live)
```

Counter update item (in the attempt's `TransactWriteItems`):

```go
func (r *PaymentRepository) chargeCounterItem(tenantID string, month string, limit int64, now time.Time) types.TransactWriteItem {
	pk, sk := ChargeCounterKey(tenantID, month)
	cond := "attribute_not_exists(pk) OR #n < :limit"
	values := map[string]types.AttributeValue{":one": numberValue(1), ":limit": numberValue(limit), ":ttl": numberValue(now.AddDate(0, 3, 0).Unix())}
	if limit < 0 {
		cond = ""
		delete(values, ":limit")
	}
	return r.byok.BuildRawUpdateTxItem(pk, &sk, "ADD #n :one SET #ttl = :ttl", cond, map[string]string{"#n": "n", "#ttl": "ttl"}, values)
}
```

`ChargeGates.WithQuota` (part B hook) answers `exceeded = limits.RefuseOverage && limits.ChargesPerMonth >= 0 && ChargesThisMonth >= ChargesPerMonth`, so the console can say "limite atingido" before a finalization; the transaction condition is what actually holds under concurrency.

- [ ] **Step 1: Write the failing tests**

```go
func TestGratisNeverPassesFifty(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.subscribeMerchantTo(t, "free")
	env.preloadCharges(t, 40)
	var wg sync.WaitGroup
	for range 11 {
		wg.Add(1)
		go func() { defer wg.Done(); env.finalizeInvoice(t, 100) }()
	}
	wg.Wait()
	if n := env.chargesThisMonth(t); n != 50 {
		t.Fatalf("counter %d, want 50", n)
	}
	if env.fake.CreatedCount() != 10 {
		t.Fatalf("%d provider charges, want 10", env.fake.CreatedCount())
	}
	r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+env.finalizeInvoice(t, 100).ID+"/charge", "{}")
	if r.code() != "byok_quota_exceeded" || !strings.Contains(string(r.body), "upgrade_plan") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestAPaidPlanNeverRefuses(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.subscribeMerchantTo(t, "basic")
	env.preloadCharges(t, 600)
	if a := env.attempt(t, env.finalizeInvoice(t, 100)); a.ChargeRef == "" {
		t.Fatal("charge 601 on Básico is created")
	}
}

func TestACancellationWithin24HoursGivesTheUnitBack(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.subscribeMerchantTo(t, "free")
	inv := env.finalizeInvoice(t, 100)
	env.voidAt(t, inv, now().Add(23*time.Hour))
	if n := env.chargesThisMonth(t); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestInTestModeNothingCounts(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	env.finalizeInvoice(t, 100)
	if n := env.chargesThisMonth(t); n != 0 {
		t.Fatalf("%d", n)
	}
}
```

(`preloadCharges` sets the counter row directly; `byoktest.Fake.CreatedCount()` returns the number of distinct refs created: add it to the fake in part A Task 2's file as a follow-up commit in ctech-go-common, tag `v1.18.1`, or count `Calls(OpCreateDueCharge)` minus idempotent repeats in the test instead. Prefer the latter: no new tag.)

- [ ] **Steps 2–5:** FAIL → implement → `make test-integration` ok → commit `feat(byok): monthly charge counter and the Grátis refusal`.

### Task 4: The active-subscription cap and the bank-connection cap

**Files:**
- Modify: `api/internal/repositories/subscriptions.go` (`Create` writes `BYOKSUBS#{tenant}` when given a cap; `Transition` to `CANCELED`/`ENDED` decrements), `api/internal/services/subscribing.go` (reads the cap for a live BYOK-rail tenant through `BillingPlans`), `api/internal/services/byok_connections.go` (`Connect` checks `BankConnections`), `api/internal/services/byok_collection_setup.go` (initialises the counter at the switch), `api/internal/problem/problem.go`
- Test: `api/tests/integration/byok_caps_test.go`

**Interfaces:**

```go
var ErrSubscriptionLimitReached = errors.New("billing: the plan's active subscriptions are at the quota")
var ErrBankConnectionLimitReached = errors.New("byok: the plan's bank connections are at the quota")
func (r *SubscriptionRepository) WithCap(cap int64) CreateOption // Create(ctx, s, items, now, opts...)
func (r *ByokRepository) InitSubscriptionCount(ctx context.Context, tenantID string, n int64) error // put if absent
```

- [ ] **Step 1: Write the failing tests**

```go
func TestTheSubscriptionCapHoldsUnderConcurrency(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.subscribeMerchantTo(t, "free") // 50
	env.createSubscriptions(t, 48)
	var wg sync.WaitGroup
	codes := make(chan string, 5)
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- env.subscribeCustomer(t).code() }()
	}
	wg.Wait()
	close(codes)
	refused := 0
	for c := range codes {
		if c == "subscription_limit_reached" {
			refused++
		}
	}
	if refused != 3 || env.activeSubscriptions(t) != 50 {
		t.Fatalf("refused %d, active %d", refused, env.activeSubscriptions(t))
	}
	env.cancelOneSubscription(t)
	if c := env.subscribeCustomer(t).code(); c != "" {
		t.Fatalf("a slot freed by a cancellation: %s", c)
	}
}

func TestBankConnectionsAreCappedByThePlan(t *testing.T) {
	env := newByokAPIWithPlan(t, "free") // 1 connection
	if r := env.connectLive(t, "s"); r.status != 201 {
		t.Fatal(r.status)
	}
	if r := env.connectLiveProvider(t, "inter", otherOrgSameAccountOrg); r.code() != "byok_already_connected" {
		t.Fatal("one per provider anyway")
	}
}
```

(Phase 1 has one provider, so the second-provider path is covered by a unit test on `Connect` with a fake `ListConnections` returning one live connection of provider "other": `ErrBankConnectionLimitReached`.)

- [ ] **Steps 2–5:** FAIL → implement → run → commit `feat(byok): cap active subscriptions and bank connections by plan`.

### Task 5: Usage reporting — `cmd/byok` step 6

**Files:**
- Modify: `api/internal/repositories/payments.go` (`CreateByokAttempt` sets the sparse `usage_pk = live#BYOK_USAGE#{creation day}`, `usage_sk = attempt id`, live only; `MarkUsageReported` removes them), `api/internal/repositories/schema.json` (`invoices` gains index `usage-index` on `usage_pk`/`usage_sk`), `api/internal/services/byok_job.go` (registers `StepUsage` once per run, not per connection: a `RegisterRun` variant), `api/cmd/byok/main.go`
- Create: `api/internal/services/byok_usage.go`
- Test: `api/tests/integration/byok_usage_test.go`

**Interfaces:**

```go
type ByokUsage struct{ /* payments, usage, subs, customers, plans, tenantZero */ }
func (u *ByokUsage) Report(ctx context.Context, now time.Time) (reported, skipped int, errs []error)
```

Algorithm: for day in {today−2, today−1} (São Paulo), page `usage-index` partition `live#BYOK_USAGE#{day}`; for each attempt with `CreatedAt` ≤ now − 24 h: if `CancelledAt` is set and `CancelledAt − CreatedAt < 24 h` → `MarkUsageReported(skipped)`; else resolve the tenant's organization (`orgs.Get(tenant, true).AccountOrganizationID`) → tenant zero's `ORG_` customer → its entitling `prod_billing` subscription → the item whose price has `meter == "billing_charges"`; none (Grátis, early access, internal) → mark reported with no record (nothing billable); else `usage.Append(UsageRecord{SubscriptionItemID, Quantity: 1, OccurredAt: a.CreatedAt, IdempotencyKey: a.ID}, periodContaining(sub, day).Start)` (duplicate = success) → `MarkUsageReported`. Attempts created more than 48 h ago are still picked up from the two partitions until reported (a missed day is caught by `-date`).

- [ ] **Step 1: Write the failing tests**

```go
func TestUsageReportsSurvivorsOnce(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.subscribeMerchantTo(t, "basic")
	early, late := env.finalizeInvoice(t, 100), env.finalizeInvoice(t, 100)
	env.voidAt(t, early, env.createdAt(early).Add(23*time.Hour))
	env.voidAt(t, late, env.createdAt(late).Add(25*time.Hour))
	keep := env.finalizeInvoice(t, 100)
	for range 3 {
		env.runUsage(t, now().Add(30*time.Hour))
	}
	recs := env.usageRecords(t, "billing_charges")
	if len(recs) != 2 || !recs.has(env.attempt(t, late).ID) || !recs.has(env.attempt(t, keep).ID) {
		t.Fatalf("%+v", recs)
	}
}

func TestOverageIsBilledFromUsage(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	sub := env.subscribeMerchantTo(t, "basic")
	env.appendUsage(t, sub, 520)
	inv := env.closePeriod(t, sub)
	if line := inv.LineFor("price_billing_basic_charges"); line.Amount != 20*25 {
		t.Fatalf("overage line %+v", line)
	}
}

func TestTestModeChargesAreNeverReported(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	env.finalizeInvoice(t, 100)
	env.runUsage(t, now().Add(30*time.Hour))
	if n := len(env.usageRecords(t, "billing_charges")); n != 0 {
		t.Fatalf("%d", n)
	}
}
```

- [ ] **Steps 2–5:** FAIL → implement → `make test-integration` → commit `feat(byok): report surviving live charges as billing_charges usage, once`.

### Task 6: The plan screen (API + UI)

**Files:**
- Create: `api/internal/api/v1/console_plan.go`, `ui/src/app/(console)/console/plan/{page.tsx,layout.tsx}`, `ui/src/components/console/BillingPlanView.tsx`, `ui/src/components/console/BillingPlanView.test.tsx`
- Modify: `api/internal/api/v1/router.go` (console: `GET /plan` `billing:organization:read`, `POST /plan` `billing:organization:write`), `ui/src/lib/console/nav.ts`, `ui/src/lib/api/console.ts`, `ui/src/locales/*/console.json`
- Test: `api/tests/integration/console_plan_test.go`

**Contract:** `GET /v1.0/console/plan` → `{current: {plan, subscription_id}, usage: {charges_this_month, quota, subscriptions_active, subscriptions_quota, bank_connections, bank_connections_quota}, plans: [{plan, fee, included, overage_unit, refuse, bank_connections, users, api_access}]}` (public plans only, from tenant zero's catalogue); `POST /v1.0/console/plan {"plan":"growth"}` → `Subscriber.ChangePlan` of the entitling subscription to the plan's prices (fixed + metered); a downgrade below current usage is allowed (the cap applies to new creations only) and the screen says so. Live mode only (test answers the same plan as live, read-only).

UI (`/impeccable`, own phone layout): four plan cards in a row at ≥ 1024 px, a stacked list on phones with the current plan first; usage meters ("47 de 50 cobranças este mês") with the `dataviz` stat-tile rules (no colour beyond status tokens); the refusal codes (`byok_quota_exceeded`, `subscription_limit_reached`) elsewhere in the console link here ("Ver planos"). Strings: "Plano", "Grátis", "Básico", "Crescimento", "Pro", "por mês", "{{count}} cobranças incluídas", "{{amount}} por cobrança adicional", "Acima do limite, novas cobranças são recusadas", "{{used}} de {{quota}} cobranças este mês", "{{used}} de {{quota}} assinaturas ativas", "Mudar para {{plan}}", "Plano atual", "Ver planos".

- [ ] **Steps:** API integration test (list excludes the internal and early-access prices; change to growth swaps both prices) and UI tests (cards, phone order, change confirmation) FAIL → implement → Go suite, then UI suite (`--maxWorkers=2`), build → commit `feat(console): the BYOK plan screen with usage and plan changes`.

## Final verification (part C)

As part A. Then deploy: `api/cmd/seed/run.sh prod` (catalogue), check `GET /v1.0/console/plan` for the pilot shows `unlimited`.

## Spec coverage (part C)

| Spec | Task |
|---|---|
| § 12.5 table, mapping onto metered prices, Grátis refusal, overage never blocks, subscription cap, feature metadata, early access, Sob demanda deferred, Empresas off-catalogue | 1, 2, 3, 4, 6 |
| § 7.1 quota in the gate | 3 (via part B's `ChargeGates.WithQuota`) |
| § 5 step 6 usage | 5 |
| § 17 item 3 quota rows, item 15 usage | 3, 5 |
