# Plans 2 — Finanças → Plano Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship deploy step 3 of the plans spec: `/finance/plans`, shown in *Pessoal* only, where a person sees how many personal spaces they own and how many people each holds (read from ctech-account), the Finanças plans as cards with the current one marked, subscribes or changes plan through the existing `Subscriber` and is sent to the invoice's `checkout_url`, is told plainly when they are over a limit (nothing removed), and is offered Basic when Sob demanda is projected to cost more than it — with its own phone layout.

**Architecture:** A service, `services.FinancePlan`, owns the three use cases (read the state, choose a plan, go back to Free) against tenant zero **live**; it reuses `CustomerRepository` (get-or-create `USER_{sub}` with its `CUSTOMER_USER#` pointer), `Subscriber.Subscribe`/`ChangePlan`/`Cancel`, `LevelRepository` and `billing.MaxLevel` (plan 1) for the on-demand projection. Three routes under `/v1.0/console/finance/plan`, mounted beside `/spaces` (outside the finance route table: they write no finance data and must not create a finance space). The usage counts come from ctech-account's existing `GET /internal/users/:user_id/organizations`, which gains `people` and `pending_invitations` on owned personal workspaces (ctech-account spec § 6). The UI interprets the price metadata (quotas) — the Go side passes it through opaque (ADR 0008).

**Tech Stack:** Go 1.27.2, Fiber v3, DynamoDB (api-commons v1.14.0); Next.js 16 static export (read `ui/node_modules/next/dist/docs/` before writing the route — `ui/AGENTS.md`), React 19, TanStack Query, axios, `@aoctech/ui` ^0.4.1, Tailwind 4, vitest + Testing Library.

**Spec:** [`docs/specs/2026-10-10-plans-design.md`](../specs/2026-10-10-plans-design.md) — § 7 (this plan), § 2 D2/D3/D5/D6/D9, § 3 (catalogue), § 6.3 (projection arithmetic), § 9 test 7, § 10 step 3, and the "Amendment, 2026-10-10 — planning" (item 4). Builds on [plans 1](2026-10-10-plans-1-backend.md) (catalogue, levels, `ORG_`/owner scope). Counterpart: `ctech-account/docs/specs/2026-10-10-space-plan-limits.md` § 6 (counts), § 7 (the *Ver planos* link lands here).

## Scope decisions (read before executing)

1. **Prerequisites.** Plan 1 is deployed and seeded; ctech-account answers `people`/`pending_invitations` (its deploy step 2). Without the counts the screen still works and says the usage is unavailable (decision 6).
2. **Tenant zero, live, whatever the console's mode.** The plan is the person's real plan (D2), and ctech-account reads it with a live credential. The routes still run behind `ResolveSpace` (which requires `X-Billing-Mode`), but read and write `PortalOrganizationID` in live mode regardless of the header. In test mode the screen shows a one-line note that plans are always live. No `PortalOrganizationID` → the routes answer 404 and the nav entry is hidden.
3. **Pessoal only.** The routes answer `404 plan_personal_only` unless the resolved space is `space.KindPersonalDefault`; the nav entry exists only there. The plan screen does not show organizations' plans (spec § 11).
4. **Where the quota is read.** The API returns the finance prices **raw** (id, type, amount, aggregation, meter, included quantity, metadata) plus the product's `default_price_id`; `ui/src/lib/finance/plan.ts` groups them into plan cards by `metadata.plan` and reads `quota_spaces` / `quota_people_per_space` (`-1` unlimited; missing or not an integer → `0`, matching ctech-account spec § 1, so a catalogue mistake shows as "no room" rather than "unlimited"). No Go code reads quota metadata.
5. **Choosing.** `POST /plan {price_ids, name, email?}`: every id must be an active price of an `owner_key: finance` product and not the default (Free is not subscribed to, D9) → else 422 `not_a_plan_price`. The customer `USER_{sub}` is found through `CUSTOMER_USER#{sub}`; absent → created with `external_ref: USER_{sub}`, `user_id: sub`, the name and e-mail the console sends (the token carries neither; the UI has the id-token name). A pointer to a customer whose external ref is **not** `USER_{sub}` (e.g. made by hand in the console) → 409 `customer_ref_mismatch`: entitlements are read by `customer_ref`, so subscribing that customer would sell a plan ctech-account never sees. Two concurrent first choices: the loser's `ErrUserAlreadyCustomer` re-reads the pointer and continues. Then: no Finanças subscription → `Subscribe`; an `INCOMPLETE` one (first invoice unpaid) → 409 `plan_payment_pending` with that invoice's `checkout_url`, so the person pays or waits for expiry instead of stacking a second unpaid plan; the same price set → no change, answer the current state; from an advance-billed plan (Basic, Pro) to Sob demanda (arrears) → **scheduled for the end of the current paid period** (decision 5a); choosing the current plan again while a change is scheduled clears it; otherwise `ChangePlan` with `CauseCustomer` (upgrade, downgrade and proration by the existing rules), which also clears any scheduled change. The response carries the invoice when there is one; the UI redirects to its `checkout_url`.
5a. **Advance → Sob demanda is scheduled, not immediate** (user decision, 2026-10-10). The period the person already paid for in advance is never also billed in arrears: the subscription records `pending_change {price_ids, at}` with `at` = the current period's end, its items (and so its entitlement and limits) stay those of the current plan until then, and the daily sweep applies the change **at that boundary** in one transaction with the renewal — new items, timing `arrears`, period index +1 — and bills nothing for the period that starts (it will be billed in arrears when it ends). `Subscriber` had no scheduled-change mechanism; Task 1b adds the minimal one. **Sob demanda → Basic/Pro stays immediate** under the existing `ChangePlan` rules: the items and timing switch now, and the remainder of the period is invoiced at the new plan's prorated price (the old arrears side contributes no credit). The open arrears period then **closes unbilled for its on-demand part**: the metered items are replaced, so the boundary sweep bills the next period in advance and no line ever bills the on-demand peak between the period start and the switch. That is how the existing rules behave; billing that partial peak at the switch is a separate decision (recorded in the report).
6. **Usage.** From `SpaceLister.Organizations(sub)`: the `kind: personal` workspaces whose role is `owner`, with `people` (members, owner excluded) and `pending_invitations`. A space's people for the limit = `people + pending_invitations` (spec D4). If ctech-account fails, or a workspace comes without counts, `usage.unavailable: true` and the cards still render; the banner is not shown on unknown numbers.
7. **Projection** (*Sob demanda above Basic*): for each `aggregation: max` finance price, `peak = MaxLevel(InPeriod(period), LatestBefore(period.start), period)` on the person's `USER_{sub}` levels; `projected = Σ BillableUnits(peak, included) × unit_amount`. The period is the Finanças subscription's current period, or the current São Paulo calendar month when there is none. This reads billing's own levels — the exact numbers the close bills, including `finance_people` as **distinct** people, which per-space counts cannot give (spec § 6.1). The UI suggests Basic when the current plan is Sob demanda and `projected > Basic's unit_amount`; never automatic.
8. **Going back to Free** = `POST /plan/cancel`: cancel the Finanças subscription at period end (`CauseCustomer`), as the portal does. Nothing is removed (D6).
9. **Not here:** a `CancelAtPeriodEnd` subscription chosen again is changed by `ChangePlan` but stays scheduled to cancel — the screen shows "termina em {date}" and the Free card's action is hidden; undoing a scheduled cancellation is a separate feature (no route exists). A scheduled change and a cancellation at period end together: the cancellation wins at the boundary (Task 1b checks it first).

## Global Constraints

- Prices, verbatim from spec § 3: Free R$ 0, Basic R$ 19,90 (1990), Pro R$ 49,90 (4990), Sob demanda R$ 4,90 per space (490) and R$ 2,90 per person (290) beyond the Free allowance (`included_quantity: 1` each). Shown from the API, never hard-coded in the UI.
- Counting (D4): the owner is not counted; pending invitations count; *Leitura* counts the same as *Acesso total*; Pessoal is never counted.
- Over-limit copy, verbatim (spec § 7): *"Você tem {n} espaços e o plano {plano} permite {limite}. Nada foi removido; para criar novos, volte ao limite ou mude de plano."* — and the people variant in the same voice: *"{espaço} tem {n} pessoas e o plano {plano} permite {limite}. Ninguém foi removido; para convidar mais, volte ao limite ou mude de plano."*
- Phone (spec § 7): usage first, plan cards stacked, one primary action.
- Plan changes (spec amendment 2026-10-10, planning): Basic/Pro → Sob demanda is **scheduled** for the end of the paid period, the current plan's limits hold until then; Sob demanda → Basic/Pro is immediate under the existing change rules.
- Route: `/finance/plans`. ctech-account links `{BILLING}/finance/plans` — never rename.
- Go: commands from `api/`; `gofmt -l ./internal ./cmd` (no output), `go vet ./...`, `go test ./...`, `make test-integration` (DynamoDB Local).
- UI: from `ui/`, `npm ci` first (installed `@aoctech/ui` is 0.3.0; `package.json` asks `^0.4.1`). `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"` — **judge by the exit code**. `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`. UI tasks are executed with the `/impeccable` skill, on `@aoctech/ui` components (`Button`, `Badge`, `Alert`, `Skeleton`, `EmptyState`, `ErrorState`, `PageHeader`, `Modal`) — check ctech-ui for a plan/pricing card before drawing one; no new dependency. Money only through `money()` (`src/lib/format.ts`). pt-BR and en.
- Never run the Go and UI suites at the same time (the machine is resource-constrained).
- Commit messages: Conventional Commits, no attribution trailer of any kind.

## Review Focus

1. **A person whose first click is "Assinar" on two tabs at once** — one `USER_` customer, one pointer, one subscription — Task 2 `TestTwoFirstChoicesMakeOneCustomer`.
2. **ctech-account down, or a workspace without counts** — the plans still render and can be chosen, the usage says it is unavailable, no banner on unknown numbers — Task 3 `TestUsageUnavailableStillAnswers`, Task 6 `it("shows plans and no banner when usage is unavailable")`.
3. **Malformed quota metadata** (`"três"`, missing) — read as 0, so the banner errs towards "over", never "unlimited" — Task 5 `it("reads a malformed quota as 0")`.
4. **A first invoice still unpaid when the person picks another plan** — 409 with the pending invoice's link, no second subscription — Task 2 `TestAnUnpaidFirstInvoiceIsPaidBeforeChanging`, Task 6 `it("sends to the pending invoice on plan_payment_pending")`.
5. **The screen opened in an organization or a personal workspace (or a forged selector)** — 404 from the API, no nav entry — Task 3 `TestThePlanIsPessoalOnly`, Task 6 `it("has no Plano entry outside Pessoal")`.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/accountclient/membership.go`, `membership_test.go` (modify) | `Organization.People`, `PendingInvitations` |
| `api/internal/domain/billing/subscription.go`, `statemachine_test.go` (modify) | `PendingChange`; `CauseScheduler` on the ACTIVE update edge |
| `api/internal/repositories/subscriptions.go` (modify) | `SchedulePlanChange`, `ApplyScheduledChange`; `ChangeItems` clears a pending change |
| `api/internal/services/subscribing.go`, `invoicing.go` (modify), `api/tests/integration/scheduled_change_test.go` (new) | `Subscriber.SchedulePlanChange`; the boundary applies it |
| `api/internal/services/finance_plan.go` (new), `finance_plan_test.go` (new) | `FinancePlan`: `State`, `Choose`, `Cancel`, projection |
| `api/internal/api/v1/finance_plan.go` (new), `router.go`, `finance_http.go` (modify) | the three routes, Pessoal-only gate, DTOs |
| `api/internal/problem/problem.go` (modify) | the plan errors' codes |
| `api/internal/app/app.go` (modify) | wiring |
| `api/tests/integration/finance_plan_test.go` (new), `finance_shared_spaces_test.go` (modify: counts in `fakeAccount`) | end to end |
| `ui/src/lib/api/financeTypes.ts`, `ui/src/lib/api/finance.ts` (modify) | `PlanState`, `getPlan`, `choosePlan`, `cancelPlan`, `financeKeys.plan` |
| `ui/src/dev/financeMockData.ts` (modify) | `/plan` in the mock |
| `ui/src/lib/finance/plan.ts`, `plan.test.ts` (new) | cards from prices, quotas, over-limit, suggestion |
| `ui/src/components/finance/PlanView.tsx`, `PlanView.test.tsx` (new) | the screen |
| `ui/src/app/(finance)/finance/plans/{layout,page}.tsx` (new) | the route |
| `ui/src/components/finance/FinanceNav.tsx`, `FinanceBottomNav.tsx` (modify) | the Pessoal-only entry |
| `ui/src/locales/{pt-BR,en}/finance.json`, `ui/DESIGN.md` (modify) | copy, design record |
| `PLAN.md`, `docs/specs/2026-10-10-plans-design.md` (modify) | records |

---

### Task 1: ctech-account's counts in the client

**Files:** Modify `api/internal/accountclient/membership.go`, `api/internal/accountclient/membership_test.go`.

**Interfaces:** Produces `accountclient.Organization.People *int` (`json:"people,omitempty"`) and `.PendingInvitations *int` (`json:"pending_invitations,omitempty"`). Pointers: absent ≠ 0 (decision 6).

- [ ] **Step 1: Write the failing test** — in `membership_test.go`, next to the existing organizations tests (they call `organizationsWithToken` against an `httptest.Server`; follow the same setup):

```go
func TestOrganizationsCarryCountsOnOwnedPersonalWorkspaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"organizations":[
		  {"id":"a","display_name":"Casa","role":"owner","kind":"personal","people":3,"pending_invitations":1},
		  {"id":"b","display_name":"Viagem","role":"viewer","kind":"personal"}]}`))
	}))
	defer srv.Close()
	c := &Client{http: srv.Client(), baseURL: srv.URL}
	got, err := c.organizationsWithToken(context.Background(), "tok", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].People == nil || *got[0].People != 3 || got[0].PendingInvitations == nil || *got[0].PendingInvitations != 1 {
		t.Fatalf("owned = %+v", got[0])
	}
	if got[1].People != nil || got[1].PendingInvitations != nil {
		t.Fatalf("a space not owned has no counts: %+v", got[1])
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/accountclient/ -run Counts -v` → FAIL (`got[0].People undefined`).
- [ ] **Step 3: Implement** — on `Organization`:

```go
	// People (members, owner excluded) and PendingInvitations are sent only on
	// personal workspaces the user owns (ctech-account spec § 6). Absent is
	// "not known", never zero.
	People             *int `json:"people,omitempty"`
	PendingInvitations *int `json:"pending_invitations,omitempty"`
```

Raise `maxBody` only if the existing limit (8 KiB) is below `quota_spaces` max (10 owned workspaces + organizations) × ~200 bytes — it is not; leave it.
- [ ] **Step 4: Run** — `go test ./internal/accountclient/ -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/accountclient
git commit -m "feat(account): read people and pending invitations on owned personal spaces"
```

---

### Task 1b: A plan change scheduled for the period end

**Files:**
- Modify: `api/internal/domain/billing/subscription.go`, `api/internal/domain/billing/statemachine_test.go`, `api/internal/repositories/subscriptions.go`, `api/internal/services/subscribing.go`, `api/internal/services/invoicing.go`
- Create: `api/tests/integration/scheduled_change_test.go`

**Interfaces:**
- Consumes: plan 1 Task 8b's `endAtBoundary` (the boundary step this extends); `SubscriptionRepository.ChangeItems`'s body.
- Produces:

```go
// billing
type PendingChange struct {
	PriceIDs []string   `dynamodbav:"price_ids" json:"price_ids"`
	At       brcal.Date `dynamodbav:"at"        json:"at"`
}
// Subscription.PendingChange *PendingChange `dynamodbav:"pending_change,omitempty" json:"pending_change,omitempty"`

// repositories
func (r *SubscriptionRepository) SchedulePlanChange(ctx context.Context, s *billing.Subscription, pc *billing.PendingChange, cause billing.Cause, actor, requestID string, now time.Time) error // nil pc clears
func (r *SubscriptionRepository) ApplyScheduledChange(ctx context.Context, s *billing.Subscription, oldItems, newItems []billing.SubscriptionItem, timing billing.BillingTiming, ownerKey, actor string, now time.Time) error

// services
func (s *Subscriber) SchedulePlanChange(ctx context.Context, sub *billing.Subscription, in ChangeInput, now time.Time) error
func (s *Subscriber) ClearPlanChange(ctx context.Context, sub *billing.Subscription, in ChangeInput, now time.Time) error
```

- [ ] **Step 1: Write the failing tests.** `statemachine_test.go`:

```go
// The boundary applies a scheduled change as the scheduler.
func TestTheSchedulerMayUpdateAnActiveSubscription(t *testing.T) {
	s := &Subscription{Status: SubscriptionActive}
	if _, err := s.Transition(SubscriptionActive, CauseScheduler); err != nil {
		t.Fatal(err)
	}
}
```

`scheduled_change_test.go` (anchors in 2034, so the cross-tenant sweep date is these tests' alone; `newCatalog`/`f.price` from `multi_item_test.go`):

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

func TestAdvanceToArrearsHappensAtTheBoundaryAndBillsNothingTwice(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth) // free, so it starts ACTIVE
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	anchor := brcal.New(2034, time.March, 7)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}},
		Cause: billing.CauseCustomer, Actor: "user:x"}, now()); err != nil {
		t.Fatal(err)
	}
	// Until the boundary nothing changes: same items, same timing.
	items, _ := f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID)
	if len(items) != 1 || items[0].PriceID != basic.ID || got.Timing != billing.BillAdvance ||
		got.PendingChange == nil || got.PendingChange.At != sub.CurrentPeriod().End {
		t.Fatalf("before the boundary: %+v %+v", items, got)
	}

	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	boundary := sub.CurrentPeriod().End
	for run := 0; run < 2; run++ { // re-runnable
		if res := inv.RunDailySweep(ctx, true, boundary, "scheduler", now()); res.Failed != 0 {
			t.Fatalf("sweep %d: %+v", run, res.Errors)
		}
	}
	got, _ = f.subs.Get(ctx, f.org.ID, true, sub.ID)
	items, _ = f.subs.ListItems(ctx, f.org.ID, true, sub.ID)
	if got.Timing != billing.BillArrears || got.PendingChange != nil || got.PeriodIndex != 1 || len(items) != 1 || items[0].PriceID != spaces.ID {
		t.Fatalf("after the boundary: %+v %+v", got, items)
	}
	invoices, _ := f.invoices.ListBySubscription(ctx, f.org.ID, true, sub.ID, 10)
	if len(invoices) != 1 { // only the first, prepaid period's; the new period is billed when it ends
		t.Fatalf("%d invoices, want 1", len(invoices))
	}
}

func TestAnImmediateChangeClearsAScheduledOne(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	pro := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	sub, _, _ := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: brcal.New(2034, time.May, 11), Actor: "t"}, now())
	_ = f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now())
	if _, err := f.subber.ChangePlan(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: pro.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PendingChange != nil {
		t.Fatalf("pending = %+v", got.PendingChange)
	}
}

func TestACancellationAtPeriodEndWinsOverAScheduledChange(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	basic := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	spaces := f.price(t, f.product.ID, billing.PriceMetered, billing.BillArrears, 490, billing.IntervalMonth)
	sub, _, _ := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: basic.ID}}, Anchor: brcal.New(2034, time.June, 13), Actor: "t"}, now())
	_ = f.subber.SchedulePlanChange(ctx, sub, services.ChangeInput{Items: []services.SubscribeItem{{PriceID: spaces.ID}}, Cause: billing.CauseCustomer, Actor: "u"}, now())
	_ = f.subs.ScheduleCancellation(ctx, sub, billing.CauseCustomer, "u", "r", now())
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage)
	_ = inv.RunDailySweep(ctx, true, sub.CurrentPeriod().End, "scheduler", now())
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.Status != billing.SubscriptionCanceled {
		t.Fatalf("status = %s", got.Status)
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/domain/billing/ -run Scheduler -v` and `go test -tags integration ./tests/integration/ -run 'Boundary|ClearsAScheduled|WinsOverAScheduled' -count=1 -v` → FAIL.

- [ ] **Step 3: Implement.**

`subscription.go`: add `CauseScheduler` to the `{SubscriptionActive, SubscriptionActive}` `EventSubscriptionUpdated` causes, and

```go
// PendingChange is a plan change the customer asked for, applied by the sweep
// at the boundary `At` (the end of the period already paid for). Until then
// the subscription's items — and so its entitlement — are unchanged.
type PendingChange struct {
	PriceIDs []string   `dynamodbav:"price_ids" json:"price_ids"`
	At       brcal.Date `dynamodbav:"at"        json:"at"`
}
```

with `PendingChange *PendingChange \`dynamodbav:"pending_change,omitempty" json:"pending_change,omitempty"\`` on `Subscription` (after `CancelAtPeriodEnd`).

`subscriptions.go`: in `ChangeItems`' `Set`, add `"pending_change": &types.AttributeValueMemberNULL{Value: true}` and set `updated.PendingChange = nil` — any immediate change supersedes a scheduled one. Then:

```go
// SchedulePlanChange records (or, with nil, clears) a change for the period end.
// A self-edge like ScheduleCancellation: the status does not move.
func (r *SubscriptionRepository) SchedulePlanChange(ctx context.Context, s *billing.Subscription, pc *billing.PendingChange,
	cause billing.Cause, actor, requestID string, now time.Time) error {
	if _, err := (&billing.Subscription{Status: s.Status}).Transition(s.Status, cause); err != nil {
		return err
	}
	value := types.AttributeValue(&types.AttributeValueMemberNULL{Value: true})
	after := "pending_change=none"
	if pc != nil {
		av, err := attributevalue.Marshal(pc)
		if err != nil {
			return err
		}
		value, after = av, "pending_change="+strings.Join(pc.PriceIDs, ",")+"@"+pc.At.String()
	}
	change := StatusChange{
		OrganizationID: s.OrganizationID, Livemode: s.Livemode,
		PK: TenantPK(s.OrganizationID, s.Livemode), SK: SubscriptionSK(s.ID),
		From: string(s.Status), To: string(s.Status),
		Set: map[string]types.AttributeValue{"pending_change": value},
		Audit: AuditEntry{Entity: EntitySubscription, EntityID: s.ID, Action: string(billing.EventSubscriptionUpdated),
			Cause: cause, Actor: actor, RequestID: requestID, Before: "pending_change", After: after},
		Emit:    []billing.EventType{billing.EventSubscriptionUpdated},
		Subject: subscriptionSubject(s),
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return err
	}
	s.PendingChange = pc
	return nil
}
```

(`attributevalue` is `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue`; if the repository encodes through `Encode` only, `Encode(pc)` returns a map — wrap it in `&types.AttributeValueMemberM{Value: m}`.) `ApplyScheduledChange` is `ChangeItems`' body — extract it into `changeItems(ctx, s, old, new, timing, ownerKey, cause, actor, requestID, now, renew bool)` and call it from both — where `renew` also does `updated.PeriodIndex++` **before** the schedule keys are computed, adds `"period_index": N(updated.PeriodIndex)` to `Set`, and appends `billing.EventSubscriptionRenewed` to `Emit`. One transaction: the change and the renewal cannot be separated by a crash, so a re-run finds the subscription already moved (not due on this date) and does nothing.

`subscribing.go`:

```go
// SchedulePlanChange records a change for the end of the current period (plans
// spec, amendment: advance → arrears never bills a paid period twice).
func (s *Subscriber) SchedulePlanChange(ctx context.Context, sub *billing.Subscription, in ChangeInput, now time.Time) error {
	prices, _, err := s.resolveItemPrices(ctx, SubscribeInput{OrganizationID: sub.OrganizationID, Livemode: sub.Livemode, Items: in.Items})
	if err != nil {
		return err
	}
	if prices[0].Recurrence != sub.Recurrence {
		return fmt.Errorf("%w: changing the cycle means a new subscription", billing.ErrInvalidSubscriptionItem)
	}
	ids := make([]string, len(in.Items))
	for i, it := range in.Items {
		ids[i] = it.PriceID
	}
	return s.subs.SchedulePlanChange(ctx, sub, &billing.PendingChange{PriceIDs: ids, At: sub.CurrentPeriod().End}, in.Cause, in.Actor, in.RequestID, now)
}

// ClearPlanChange drops a scheduled change (the person chose their current plan again).
func (s *Subscriber) ClearPlanChange(ctx context.Context, sub *billing.Subscription, in ChangeInput, now time.Time) error {
	if sub.PendingChange == nil {
		return nil
	}
	return s.subs.SchedulePlanChange(ctx, sub, nil, in.Cause, in.Actor, in.RequestID, now)
}
```

`invoicing.go`, in `invoiceOne` right after the `CancelAtPeriodEnd` branch (plan 1 Task 8b — the cancellation is checked first):

```go
	if pc := sub.PendingChange; pc != nil && pc.At == sub.CurrentPeriod().End {
		return s.changeAtBoundary(ctx, sub, items, pc, actor, now)
	}
```

```go
// changeAtBoundary applies a scheduled plan change as the period ends. The
// period that ends is billed under the old items only if they bill in arrears
// (an advance one was paid already); the change and the renewal are one write;
// the period that starts is billed now only if the new items bill in advance.
func (s *Invoicer) changeAtBoundary(ctx context.Context, sub *billing.Subscription, items []billing.SubscriptionItem,
	pc *billing.PendingChange, actor string, now time.Time) error {
	if sub.Timing == billing.BillArrears {
		if _, err := s.GenerateForPeriod(ctx, sub, items, billing.PeriodToInvoice(sub), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	newItems := make([]billing.SubscriptionItem, len(pc.PriceIDs))
	timing, owner := sub.Timing, sub.OwnerKey
	for i, priceID := range pc.PriceIDs {
		price, err := s.catalog.GetPrice(ctx, sub.OrganizationID, sub.Livemode, priceID)
		if err != nil {
			return err
		}
		product, err := s.catalog.GetProduct(ctx, sub.OrganizationID, sub.Livemode, price.ProductID)
		if err != nil {
			return err
		}
		timing, owner = price.Timing, product.OwnerKey
		newItems[i] = billing.SubscriptionItem{ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: sub.OrganizationID,
			Livemode: sub.Livemode, SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1}
	}
	if err := s.subs.ApplyScheduledChange(ctx, sub, items, newItems, timing, owner, actor, now); err != nil {
		return err
	}
	if timing == billing.BillAdvance {
		if _, err := s.GenerateForPeriod(ctx, sub, newItems, sub.CurrentPeriod(), actor, now); err != nil &&
			!errors.Is(err, repositories.ErrAlreadyGenerated) {
			return err
		}
	}
	return nil
}
```

(An archived price at the boundary: `GetPrice` still returns it and the change applies — the person chose it while it was offered.)

- [ ] **Step 4: Run** — `go test ./... && make test-integration` → PASS (the existing `plan_change_test.go` included).
- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/billing api/internal/repositories/subscriptions.go api/internal/services/subscribing.go api/internal/services/invoicing.go api/tests/integration/scheduled_change_test.go
git commit -m "feat(subscriptions): a plan change scheduled for the end of the paid period"
```

---

### Task 2: `services.FinancePlan`

**Files:**
- Create: `api/internal/services/finance_plan.go`, `api/tests/integration/finance_plan_test.go` (service-level tests here; HTTP tests in Task 3)

**Interfaces:**
- Consumes: `CustomerRepository.GetByUser/Create`, `ErrUserAlreadyCustomer`; `SubscriptionRepository.ListByCustomer/ListItems`; `InvoiceRepository.ListBySubscription`; `CatalogRepository.ListProducts/ListPrices/GetPrice/GetProduct`; `LevelRepository.LatestBefore/InPeriod` and `billing.MaxLevel`, `billing.BillableUnits` (plan 1); `Subscriber.Subscribe/ChangePlan/Cancel/SchedulePlanChange/ClearPlanChange` (Task 1b).
- Produces:

```go
const FinanceOwnerKey = "finance"
var (
	ErrNotAPlanPrice       = errors.New("not a Finanças plan price")
	ErrCustomerRefMismatch = errors.New("the person's customer is not USER_{sub}")
	ErrPlanPaymentPending  = errors.New("the plan's first invoice is not paid yet")
	ErrNoFinancePlan       = errors.New("no Finanças subscription")
)
type PlanPaymentPending struct{ Invoice *billing.Invoice } // returned wrapped: errors.As
func (e *PlanPaymentPending) Error() string
func (e *PlanPaymentPending) Unwrap() error // ErrPlanPaymentPending

type LevelView struct{ Current, Peak int64 }
type PlanState struct {
	Subscription   *billing.Subscription // nil: Free (implicit)
	PriceIDs       []string              // the subscription's items
	PendingChange  *billing.PendingChange // a change scheduled for the period end
	OpenInvoice    *billing.Invoice
	Prices         []billing.Price       // active prices of finance products
	DefaultPriceID string
	Period         billing.Period
	Levels         map[string]LevelView  // by meter
	Projected      billing.Cents         // Σ over max prices
}
type ChooseInput struct {
	UserID, Name, Email string
	PriceIDs            []string
	RequestID           string
}
func NewFinancePlan(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository, catalog *repositories.CatalogRepository,
	levels *repositories.LevelRepository, subscriber *Subscriber, tenantZero string) *FinancePlan
func (s *FinancePlan) Enabled() bool
func (s *FinancePlan) State(ctx context.Context, userID string, now time.Time) (*PlanState, error)
func (s *FinancePlan) Choose(ctx context.Context, in ChooseInput, now time.Time) (*billing.Subscription, *billing.Invoice, error)
func (s *FinancePlan) Cancel(ctx context.Context, userID, requestID string, now time.Time) (*billing.Subscription, error)
```

- [ ] **Step 1: Write the failing integration tests** — `finance_plan_test.go` (seed the plan-1 catalogue into a fresh tenant with `provision.Apply` and the real `tenants/ctech.json` re-targeted to a new organization id, so the test exercises the real prices):

```go
//go:build integration

package integration

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// seedCTechCatalogue applies tenants/ctech.json under a fresh organization id.
func seedCTechCatalogue(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	orgID := "org_" + id.New()
	doc := strings.Replace(string(raw), `"id": "ctech"`, `"id": "`+orgID+`"`, 1)
	// Credentials are global by client id: drop them, a test needs none.
	plan, err := provision.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	plan.Credentials = nil
	plan.Endpoints = nil
	repos := provision.Repos{
		Organizations: repositories.NewOrganizationRepository(testDB, testCfg),
		Credentials:   repositories.NewCredentialRepository(testDB, testCfg),
		Catalog:       repositories.NewCatalogRepository(testDB, testCfg),
		Webhooks:      repositories.NewWebhookRepository(testDB, testCfg),
	}
	if _, err := provision.Apply(ctxT(t), repos, plan, true, now()); err != nil {
		t.Fatal(err)
	}
	return orgID
}

func newFinancePlan(t *testing.T, tenant string) (*services.FinancePlan, *repositories.LevelRepository) {
	t.Helper()
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	inv := repositories.NewInvoiceRepository(testDB, testCfg)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	invoicer := services.NewInvoicer(subs, inv, cat, repositories.NewUsageRepository(testDB, testCfg)).WithLevels(levels, customers)
	return services.NewFinancePlan(customers, subs, inv, cat, levels, services.NewSubscriber(subs, cat, invoicer), tenant), levels
}

func TestChoosingBasicCreatesTheUserCustomerAndAnInvoice(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	sub, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionIncomplete || inv == nil || inv.Total != 1990 {
		t.Fatalf("sub %+v inv %+v", sub, inv)
	}
	c, err := repositories.NewCustomerRepository(testDB, testCfg).GetByUser(ctxT(t), tenant, true, user)
	if err != nil || c.ExternalRef != "USER_"+user {
		t.Fatalf("customer = %+v, %v", c, err)
	}
}

// Review Focus 1.
func TestTwoFirstChoicesMakeOneCustomer(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_pro_monthly"}}, now())
		}()
	}
	wg.Wait()
	page, err := repositories.NewCustomerRepository(testDB, testCfg).List(ctxT(t), tenant, true, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range page.Items {
		if c.UserID == user {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d customers for one person", n)
	}
}

// Review Focus 4.
func TestAnUnpaidFirstInvoiceIsPaidBeforeChanging(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	_, first, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_pro_monthly"}}, now())
	var pending *services.PlanPaymentPending
	if !errors.As(err, &pending) || pending.Invoice.ID != first.ID {
		t.Fatalf("err = %v", err)
	}
}

func TestOnDemandStartsActiveAndProjectsFromLevels(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, levels := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana",
		PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now()); err != nil {
		t.Fatal(err)
	}
	for meter, v := range map[string]int64{"finance_spaces": 5, "finance_people": 4} {
		if err := levels.Append(ctxT(t), &billing.LevelRecord{OrganizationID: tenant, Livemode: true, CustomerRef: "USER_" + user,
			Meter: meter, Value: v, OccurredAt: now().Add(-time.Hour), IdempotencyKey: id.New()}, now()); err != nil {
			t.Fatal(err)
		}
	}
	st, err := plan.State(ctxT(t), user, now())
	if err != nil {
		t.Fatal(err)
	}
	// (5-1)×490 + (4-1)×290 = 1960 + 870
	if st.Subscription == nil || st.Subscription.Status != billing.SubscriptionActive || st.Projected != 2830 {
		t.Fatalf("state = %+v", st)
	}
	if st.Levels["finance_spaces"].Peak != 5 {
		t.Fatalf("levels = %+v", st.Levels)
	}
}

// Decision 5a, through the service: a paid Basic switching to Sob demanda
// keeps Basic until the period ends.
func TestBasicToOnDemandIsScheduledForThePeriodEnd(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	sub, first, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	payInvoice(t, tenant, first) // mark PAID → ACTIVE, the way checkout_test.go settles one
	_, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana",
		PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	if err != nil || inv != nil {
		t.Fatalf("inv %+v err %v", inv, err)
	}
	st, _ := plan.State(ctxT(t), user, now())
	if len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_finance_basic_monthly" || st.PendingChange == nil || st.PendingChange.At != sub.CurrentPeriod().End {
		t.Fatalf("state = %+v", st)
	}
	// Choosing Basic again drops the scheduled change.
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := plan.State(ctxT(t), user, now()); st.PendingChange != nil {
		t.Fatal("the scheduled change survived choosing the current plan")
	}
}

func TestOnDemandToBasicIsImmediate(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	_, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil || inv == nil || inv.Total <= 0 {
		t.Fatalf("an immediate prorated Basic invoice, got %+v %v", inv, err)
	}
	st, _ := plan.State(ctxT(t), user, now())
	if len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_finance_basic_monthly" || st.Subscription.Timing != billing.BillAdvance {
		t.Fatalf("state = %+v", st)
	}
}

func TestOnlyFinancePlanPricesAreAccepted(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	for _, ids := range [][]string{{"price_dfe_pro_monthly"}, {"price_finance_free"}, {}, {"price_nope"}} {
		if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: "usr_" + id.New(), Name: "A", PriceIDs: ids}, now()); !errors.Is(err, services.ErrNotAPlanPrice) {
			t.Errorf("%v: %v", ids, err)
		}
	}
}

func TestACustomerUnderAnotherRefIsRefused(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: tenant, Livemode: true, Name: "Ana", UserID: user, ExternalRef: "crm_42",
	}, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now()); !errors.Is(err, services.ErrCustomerRefMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelGoesBackToFreeAtPeriodEnd(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if _, err := plan.Cancel(ctxT(t), user, "r", now()); !errors.Is(err, services.ErrNoFinancePlan) {
		t.Fatalf("nothing to cancel: %v", err)
	}
	_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	sub, err := plan.Cancel(ctxT(t), user, "r", now())
	if err != nil || !sub.CancelAtPeriodEnd {
		t.Fatalf("sub = %+v, %v", sub, err)
	}
}
```

(`payInvoice(t, tenant, inv)` is a small helper in this file: `InvoiceRepository.Transition(ctx, inv, billing.InvoicePaid, billing.CauseInvoicePaid, …)` then `Collector`'s activation — copy what `checkout_test.go` does after a settled charge, or call `repositories.NewSubscriptionRepository(...).Transition(ctx, sub, billing.SubscriptionActive, billing.CauseInvoicePaid, …)` directly; the test needs ACTIVE, not the payment path. `seedCTechCatalogue`'s `strings.Replace` targets the organization id line as the file formats it, `"id": "ctech"`; if Task 4 of plan 1 kept a different spacing, match it. `account_organization_id` stays — harmless in a test tenant.)

- [ ] **Step 2: Run** — `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Choos|FirstChoices|Unpaid|OnDemand|PlanPrices|AnotherRef|BackToFree' -v` → FAIL (`undefined: services.FinancePlan`).

- [ ] **Step 3: Implement** — `finance_plan.go`:

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// FinanceOwnerKey is CTech Finanças' owner in tenant zero's catalogue.
const FinanceOwnerKey = "finance"

var (
	ErrNotAPlanPrice       = errors.New("not a Finanças plan price")
	ErrCustomerRefMismatch = errors.New("the person's customer is not USER_{sub}")
	ErrPlanPaymentPending  = errors.New("the plan's first invoice is not paid yet")
	ErrNoFinancePlan       = errors.New("no Finanças subscription")
)

// PlanPaymentPending carries the unpaid first invoice, so the screen can send
// the person to pay it.
type PlanPaymentPending struct{ Invoice *billing.Invoice }

func (e *PlanPaymentPending) Error() string { return ErrPlanPaymentPending.Error() }
func (e *PlanPaymentPending) Unwrap() error { return ErrPlanPaymentPending }

// FinancePlan is the person's Finanças plan (spec § 7), always in tenant zero,
// live (scope decision 2).
type FinancePlan struct {
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	invoices   *repositories.InvoiceRepository
	catalog    *repositories.CatalogRepository
	levels     *repositories.LevelRepository
	subscriber *Subscriber
	tenant     string
}

func NewFinancePlan(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository, catalog *repositories.CatalogRepository,
	levels *repositories.LevelRepository, subscriber *Subscriber, tenantZero string) *FinancePlan {
	return &FinancePlan{customers: customers, subs: subs, invoices: invoices, catalog: catalog, levels: levels, subscriber: subscriber, tenant: tenantZero}
}

// Enabled is false where no tenant zero is configured.
func (s *FinancePlan) Enabled() bool { return s != nil && s.tenant != "" }

type LevelView struct{ Current, Peak int64 }

type PlanState struct {
	Subscription   *billing.Subscription
	PriceIDs       []string
	OpenInvoice    *billing.Invoice
	Prices         []billing.Price
	DefaultPriceID string
	Period         billing.Period
	Levels         map[string]LevelView
	Projected      billing.Cents
}

type ChooseInput struct {
	UserID, Name, Email string
	PriceIDs            []string
	RequestID           string
}

func userRef(userID string) string { return "USER_" + userID }

// catalogue returns the active prices of finance products and the default.
func (s *FinancePlan) catalogue(ctx context.Context) ([]billing.Price, string, error) {
	products, err := s.catalog.ListProducts(ctx, s.tenant, true, 100)
	if err != nil {
		return nil, "", err
	}
	finance := map[string]bool{}
	def := ""
	for _, p := range products {
		if p.OwnerKey == FinanceOwnerKey && p.Active {
			finance[p.ID] = true
			if p.DefaultPriceID != "" {
				def = p.DefaultPriceID
			}
		}
	}
	all, err := s.catalog.ListPrices(ctx, s.tenant, true, 100)
	if err != nil {
		return nil, "", err
	}
	var out []billing.Price
	for _, p := range all {
		if finance[p.ProductID] && !p.Archived {
			out = append(out, p)
		}
	}
	return out, def, nil
}

// current is the person's Finanças subscription that is not canceled.
func (s *FinancePlan) current(ctx context.Context, customerID string) (*billing.Subscription, error) {
	subs, err := s.subs.ListByCustomer(ctx, s.tenant, true, customerID, 100)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].OwnerKey == FinanceOwnerKey && subs[i].Status != billing.SubscriptionCanceled {
			return &subs[i], nil
		}
	}
	return nil, nil
}

func (s *FinancePlan) openInvoice(ctx context.Context, sub *billing.Subscription) (*billing.Invoice, error) {
	invs, err := s.invoices.ListBySubscription(ctx, s.tenant, true, sub.ID, 10)
	if err != nil {
		return nil, err
	}
	for i := range invs {
		if invs[i].Status == billing.InvoiceOpen {
			return &invs[i], nil
		}
	}
	return nil, nil
}

func (s *FinancePlan) State(ctx context.Context, userID string, now time.Time) (*PlanState, error) {
	prices, def, err := s.catalogue(ctx)
	if err != nil {
		return nil, err
	}
	st := &PlanState{Prices: prices, DefaultPriceID: def, Levels: map[string]LevelView{}}
	today := brcal.FromTime(now)
	st.Period = billing.Period{Start: brcal.New(today.Year, today.Month, 1), End: brcal.New(today.Year, today.Month+1, 1)}

	customer, err := s.customers.GetByUser(ctx, s.tenant, true, userID)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		sub, err := s.current(ctx, customer.ID)
		if err != nil {
			return nil, err
		}
		if sub != nil {
			st.Subscription, st.Period, st.PendingChange = sub, sub.CurrentPeriod(), sub.PendingChange
			items, err := s.subs.ListItems(ctx, s.tenant, true, sub.ID)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				st.PriceIDs = append(st.PriceIDs, it.PriceID)
			}
			if st.OpenInvoice, err = s.openInvoice(ctx, sub); err != nil {
				return nil, err
			}
		}
	}
	return st, s.project(ctx, userID, st)
}

// project fills Levels and Projected from billing's own level reports — the
// numbers the close bills (scope decision 7).
func (s *FinancePlan) project(ctx context.Context, userID string, st *PlanState) error {
	for _, p := range st.Prices {
		if p.Aggregation != billing.AggregationMax {
			continue
		}
		carried := int64(0)
		before, err := s.levels.LatestBefore(ctx, s.tenant, true, userRef(userID), p.Meter, st.Period.Start.Time())
		if err != nil {
			return err
		}
		if before != nil {
			carried = before.Value
		}
		records, err := s.levels.InPeriod(ctx, s.tenant, true, userRef(userID), p.Meter, st.Period.Start.Time(), st.Period.End.Time())
		if err != nil {
			return err
		}
		current := carried
		if len(records) > 0 {
			current = records[len(records)-1].Value
		}
		peak := billing.MaxLevel(records, carried, st.Period)
		st.Levels[p.Meter] = LevelView{Current: current, Peak: peak}
		st.Projected += p.UnitAmount * billing.Cents(billing.BillableUnits(peak, p.IncludedQuantity))
	}
	return nil
}

// customerFor finds or creates USER_{sub} with its portal pointer.
func (s *FinancePlan) customerFor(ctx context.Context, in ChooseInput, now time.Time) (*billing.Customer, error) {
	c, err := s.customers.GetByUser(ctx, s.tenant, true, in.UserID)
	if err == nil {
		if c.ExternalRef != userRef(in.UserID) {
			return nil, fmt.Errorf("%w: customer %s has external ref %q", ErrCustomerRefMismatch, c.ID, c.ExternalRef)
		}
		return c, nil
	}
	if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	c = &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: s.tenant, Livemode: true,
		ExternalRef: userRef(in.UserID), UserID: in.UserID, Name: in.Name, Email: in.Email,
	}
	err = s.customers.Create(ctx, c, "user:"+in.UserID, in.RequestID, now)
	if errors.Is(err, repositories.ErrUserAlreadyCustomer) {
		// The other tab won (Review Focus 1): use its customer.
		return s.customerFor(ctx, ChooseInput{UserID: in.UserID}, now)
	}
	return c, err
}

func (s *FinancePlan) validPrices(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: choose at least one price", ErrNotAPlanPrice)
	}
	prices, def, err := s.catalogue(ctx)
	if err != nil {
		return err
	}
	for _, want := range ids {
		ok := want != def && slices.ContainsFunc(prices, func(p billing.Price) bool { return p.ID == want })
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotAPlanPrice, want)
		}
	}
	return nil
}

func (s *FinancePlan) Choose(ctx context.Context, in ChooseInput, now time.Time) (*billing.Subscription, *billing.Invoice, error) {
	if err := s.validPrices(ctx, in.PriceIDs); err != nil {
		return nil, nil, err
	}
	customer, err := s.customerFor(ctx, in, now)
	if err != nil {
		return nil, nil, err
	}
	items := make([]SubscribeItem, len(in.PriceIDs))
	for i, p := range in.PriceIDs {
		items[i] = SubscribeItem{PriceID: p}
	}
	actor := "user:" + in.UserID

	sub, err := s.current(ctx, customer.ID)
	if err != nil {
		return nil, nil, err
	}
	if sub == nil {
		return s.subscriber.Subscribe(ctx, SubscribeInput{
			OrganizationID: s.tenant, Livemode: true, CustomerID: customer.ID, Items: items, Actor: actor,
		}, now)
	}
	open, err := s.openInvoice(ctx, sub)
	if err != nil {
		return nil, nil, err
	}
	if sub.Status == billing.SubscriptionIncomplete {
		return nil, nil, &PlanPaymentPending{Invoice: open}
	}
	current, err := s.subs.ListItems(ctx, s.tenant, true, sub.ID)
	if err != nil {
		return nil, nil, err
	}
	change := ChangeInput{Items: items, Cause: billing.CauseCustomer, Actor: actor, RequestID: in.RequestID}
	if samePrices(current, in.PriceIDs) {
		// The current plan again: drop a change scheduled away from it.
		return sub, open, s.subscriber.ClearPlanChange(ctx, sub, change, now)
	}
	newTiming, err := s.timingOf(ctx, in.PriceIDs[0])
	if err != nil {
		return nil, nil, err
	}
	if sub.Timing == billing.BillAdvance && newTiming == billing.BillArrears {
		// Decision 5a: never bill the paid period twice; the limits hold until then.
		return sub, nil, s.subscriber.SchedulePlanChange(ctx, sub, change, now)
	}
	inv, err := s.subscriber.ChangePlan(ctx, sub, change, now)
	return sub, inv, err
}

func (s *FinancePlan) timingOf(ctx context.Context, priceID string) (billing.BillingTiming, error) {
	p, err := s.catalog.GetPrice(ctx, s.tenant, true, priceID)
	if err != nil {
		return "", err
	}
	return p.Timing, nil
}

func samePrices(items []billing.SubscriptionItem, ids []string) bool {
	if len(items) != len(ids) {
		return false
	}
	for _, it := range items {
		if !slices.Contains(ids, it.PriceID) {
			return false
		}
	}
	return true
}

// Cancel goes back to Free at the end of the period (scope decision 8).
func (s *FinancePlan) Cancel(ctx context.Context, userID, requestID string, now time.Time) (*billing.Subscription, error) {
	customer, err := s.customers.GetByUser(ctx, s.tenant, true, userID)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, ErrNoFinancePlan
	}
	if err != nil {
		return nil, err
	}
	sub, err := s.current(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrNoFinancePlan
	}
	if sub.CancelAtPeriodEnd {
		return sub, nil
	}
	return sub, s.subscriber.Cancel(ctx, sub, true, billing.CauseCustomer, "user:"+userID, requestID, now)
}
```

(`brcal.New(today.Year, today.Month+1, 1)` normalizes December → January, as `time.Date` does.)

- [ ] **Step 4: Run** the Step 2 command → PASS; `go vet ./...`.
- [ ] **Step 5: Commit**

```bash
git add api/internal/services/finance_plan.go api/tests/integration/finance_plan_test.go
git commit -m "feat(finance): a person's Finanças plan — state, choose, back to Free"
```

---

### Task 3: The plan routes

**Files:**
- Create: `api/internal/api/v1/finance_plan.go`
- Modify: `api/internal/api/v1/router.go` (`Deps.FinancePlan`), `api/internal/api/v1/finance_http.go` (`registerFinance` mounts the plan routes), `api/internal/problem/problem.go`, `api/internal/app/app.go`, `api/tests/integration/finance_shared_spaces_test.go` (`fakeAccount` counts), `api/tests/integration/finance_plan_test.go` (HTTP tests)

**Interfaces:**
- Consumes: Task 1 counts, Task 2 service, `spaceLister`, `newInvoiceResponse` (renders `checkout_url` under the links rule).
- Produces: `GET /v1.0/console/finance/plan`, `POST /v1.0/console/finance/plan`, `POST /v1.0/console/finance/plan/cancel`. Response of all three: `planResponse` —

```json
{
  "live_only": true,
  "subscription": {"id": "sub_…", "status": "ACTIVE", "cancel_at_period_end": false, "price_ids": ["…"],
                   "period": {"period_start": "2026-10-01", "period_end": "2026-11-01"},
                   "pending_change": {"price_ids": ["price_finance_ondemand_spaces", "price_finance_ondemand_people"], "at": "2026-11-01"}},
  "open_invoice": {"id": "in_…", "total_cents": 1990, "due_date": "2026-10-10", "checkout_url": "https://…"},
  "default_price_id": "price_finance_free",
  "prices": [{"id": "…", "type": "fixed", "unit_amount": 1990, "aggregation": "", "meter": "", "included_quantity": 0, "metadata": {"plan": "basic", "quota_spaces": "3", "quota_people_per_space": "5"}}],
  "usage": {"unavailable": false, "spaces": [{"id": "…", "display_name": "Casa", "people": 3, "pending_invitations": 1}]},
  "levels": {"finance_spaces": {"current": 2, "peak": 3}},
  "projected_ondemand_cents": 490
}
```

`subscription` and `open_invoice` are `null` when absent. POST bodies: `{"price_ids": [...], "name": "…", "email": "…"}` (strict decode); `/cancel` takes `{}`.

- [ ] **Step 1: Write the failing HTTP tests.** In `finance_shared_spaces_test.go`, give `fakeAccount` a `counts map[string][2]int` (ws → {people, pending}) set by `func (a *fakeAccount) count(ws string, people, pending int)`, and in the users route add `"people"`/`"pending_invitations"` to an entry when `counts[ws]` is set (switch the entry type to `map[string]any`). Append to `finance_plan_test.go`:

```go
type planEnv struct {
	*apiEnv
	account *fakeAccount
	tenant  string
}

func newPlanEnv(t *testing.T) planEnv {
	t.Helper()
	tenant := seedCTechCatalogue(t)
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}, counts: map[string][2]int{}}
	srv := acct.serve(t)
	e := newAPIWith(t, func(c *config.Config) {
		c.PortalOrganizationID = tenant
		c.AccountBaseURL, c.AccountTokenURL = srv.URL, srv.URL+"/token"
		c.AccountClientID, c.AccountClientSecret = "billing-test", "secret"
	})
	return planEnv{apiEnv: e, account: acct, tenant: tenant}
}

func (p planEnv) call(t *testing.T, token, selector, method, path, body string) apiResponse {
	t.Helper()
	return spacesEnv{apiEnv: p.apiEnv, account: p.account}.in(t, token, selector, method, path, body)
}

type planBody struct {
	Subscription *struct {
		Status   string   `json:"status"`
		PriceIDs []string `json:"price_ids"`
	} `json:"subscription"`
	OpenInvoice *struct {
		ID          string `json:"id"`
		CheckoutURL string `json:"checkout_url"`
	} `json:"open_invoice"`
	Prices []struct{ ID string `json:"id"` } `json:"prices"`
	Usage  struct {
		Unavailable bool `json:"unavailable"`
		Spaces      []struct {
			ID                 string `json:"id"`
			People             int    `json:"people"`
			PendingInvitations int    `json:"pending_invitations"`
		} `json:"spaces"`
	} `json:"usage"`
	Invoice *struct {
		ID string `json:"id"`
	} `json:"invoice"`
}

// Spec test 7.
func TestSubscribingFromThePlanScreenCreatesTheCustomerOnce(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	tok := p.token(t, sub, "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	for i := 0; i < 2; i++ {
		r := p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_basic_monthly"],"name":"Ana"}`)
		if r.status != http.StatusOK {
			t.Fatalf("choose %d: %d %s", i, r.status, r.body)
		}
	}
	var b planBody
	p.call(t, tok, "personal", http.MethodGet, "/plan", "").decode(t, &b)
	if b.Subscription == nil || b.Subscription.Status != "INCOMPLETE" || b.OpenInvoice == nil {
		t.Fatalf("state = %+v", b)
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByExternalRef(ctxT(t), p.tenant, true, "USER_"+sub); err != nil {
		t.Fatal(err)
	}
}

func TestPlanUsageComesFromOwnedPersonalSpaces(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	owned, shared, org := newSpaceOrgID(), newSpaceOrgID(), newSpaceOrgID()
	p.account.set(owned, sub, "personal", "owner")
	p.account.count(owned, 3, 1)
	p.account.set(shared, sub, "personal", "member")
	p.account.set(org, sub, "organization", "owner")
	var b planBody
	p.call(t, p.token(t, sub, "s", middleware.ScopeFinanceRead), "personal", http.MethodGet, "/plan", "").decode(t, &b)
	if b.Usage.Unavailable || len(b.Usage.Spaces) != 1 || b.Usage.Spaces[0].ID != owned ||
		b.Usage.Spaces[0].People != 3 || b.Usage.Spaces[0].PendingInvitations != 1 {
		t.Fatalf("usage = %+v", b.Usage)
	}
	if len(b.Prices) != 5 {
		t.Fatalf("%d finance prices", len(b.Prices))
	}
}

// Review Focus 2.
func TestUsageUnavailableStillAnswers(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	ws := newSpaceOrgID()
	p.account.set(ws, sub, "personal", "owner") // no counts: an older ctech-account
	var b planBody
	r := p.call(t, p.token(t, sub, "s", middleware.ScopeFinanceRead), "personal", http.MethodGet, "/plan", "")
	r.decode(t, &b)
	if r.status != 200 || !b.Usage.Unavailable || len(b.Prices) != 5 {
		t.Fatalf("%d %+v", r.status, b)
	}
}

// Review Focus 5.
func TestThePlanIsPessoalOnly(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	ws, org := newSpaceOrgID(), newSpaceOrgID()
	p.account.set(ws, sub, "personal", "owner")
	p.account.set(org, sub, "organization", "owner")
	tok := p.token(t, sub, "s", middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	for _, sel := range []string{"org:" + ws, "org:" + org, "org:" + newSpaceOrgID()} {
		if r := p.call(t, tok, sel, http.MethodGet, "/plan", ""); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %s", sel, r.status, r.body)
		}
	}
}

func TestAPendingFirstInvoiceIsA409WithItsLink(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	tok := p.token(t, sub, "s", middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_basic_monthly"],"name":"Ana"}`)
	r := p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_pro_monthly"],"name":"Ana"}`)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "plan_payment_pending") || !strings.Contains(string(r.body), "invoice_id") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
```

(imports: `net/http`, `config`, `middleware`. `checkout_url` is asserted only in a wallet-configured env — `newPayEnv` in `checkout_test.go` shows the configuration; add `TestTheChoiceCarriesTheCheckoutURL` building `newPlanEnv` with the same `Wallet*`/`CheckoutLinkSecret`/`CheckoutBaseURL` fields and a `newFakeWallet(t)`, asserting `invoice.checkout_url` starts with `https://pay.test/c`.)

- [ ] **Step 2: Run** → FAIL (404 on `/plan`).

- [ ] **Step 3: Implement.** `finance_plan.go` (v1):

```go
package v1

import (
	"errors"
	"log/slog"
	"slices"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type planHandlers struct {
	*handlers
	plan   *services.FinancePlan
	spaces spaceLister
}

type planPriceDTO struct {
	ID               string              `json:"id"`
	Type             billing.PriceType   `json:"type"`
	UnitAmount       billing.Cents       `json:"unit_amount"`
	Aggregation      billing.Aggregation `json:"aggregation"`
	Meter            string              `json:"meter"`
	IncludedQuantity int64               `json:"included_quantity"`
	Metadata         billing.Metadata    `json:"metadata"`
}

type planSubscriptionDTO struct {
	ID                string                     `json:"id"`
	Status            billing.SubscriptionStatus `json:"status"`
	CancelAtPeriodEnd bool                       `json:"cancel_at_period_end"`
	PriceIDs          []string                   `json:"price_ids"`
	Period            billing.Period             `json:"period"`
	PendingChange     *billing.PendingChange     `json:"pending_change"`
}

type planSpaceDTO struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"display_name"`
	People             int    `json:"people"`
	PendingInvitations int    `json:"pending_invitations"`
}

type planResponse struct {
	LiveOnly       bool                         `json:"live_only"`
	Subscription   *planSubscriptionDTO         `json:"subscription"`
	OpenInvoice    *entitlementInvoice          `json:"open_invoice"`
	Invoice        *invoiceResponse             `json:"invoice,omitempty"`
	DefaultPriceID string                       `json:"default_price_id"`
	Prices         []planPriceDTO               `json:"prices"`
	Usage          planUsageDTO                 `json:"usage"`
	Levels         map[string]services.LevelView `json:"levels"`
	Projected      billing.Cents                `json:"projected_ondemand_cents"`
}

type planUsageDTO struct {
	Unavailable bool           `json:"unavailable"`
	Spaces      []planSpaceDTO `json:"spaces"`
}
```

Give `services.LevelView` JSON tags (`json:"current"`, `json:"peak"`) in Task 2's file. Then:

```go
// mountPlan registers the plan routes beside /spaces: they write no finance
// data, so they are outside the route table and its ensuringSpace (a plan
// choice must not create a finance space), but behind the same scope, space
// and idempotency middleware.
func mountPlan(router fiber.Router, resolver *space.Resolver, idem fiber.Handler, h *planHandlers) {
	read := []any{middleware.RequireUserScope(middleware.ScopeFinanceRead), middleware.ResolveSpace(resolver), h.personalOnly}
	write := []any{middleware.RequireUserScope(middleware.ScopeFinanceWrite), middleware.ResolveSpace(resolver), h.personalOnly, idem}
	router.Get("/plan", read[0], append(read[1:], any(h.get))...)
	router.Post("/plan", write[0], append(write[1:], any(h.choose))...)
	router.Post("/plan/cancel", write[0], append(write[1:], any(h.cancel))...)
}

// personalOnly: the plan is the person's (D2), seen from Pessoal only.
func (h *planHandlers) personalOnly(c fiber.Ctx) error {
	if !h.plan.Enabled() || middleware.GetSpace(c).Kind() != space.KindPersonalDefault {
		return problem.NotFound("the plan is shown in Pessoal").WithCode("plan_personal_only").Send(c)
	}
	return c.Next()
}

func (h *planHandlers) get(c fiber.Ctx) error { return h.answer(c, nil) }

type choosePlanRequest struct {
	PriceIDs []string `json:"price_ids"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
}

func (h *planHandlers) choose(c fiber.Ctx) error {
	var req choosePlanRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.text("name", req.Name, true, limits.CustomerName)
	ch.email("email", req.Email, false)
	if len(req.PriceIDs) == 0 || len(req.PriceIDs) > limits.MaxSubscriptionItems {
		ch.fail("price_ids", "required", "choose a plan")
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	_, inv, err := h.plan.Choose(c.Context(), services.ChooseInput{
		UserID: middleware.GetClaims(c).Sub, Name: req.Name, Email: req.Email,
		PriceIDs: req.PriceIDs, RequestID: middleware.GetRequestID(c),
	}, h.now())
	var pending *services.PlanPaymentPending
	if errors.As(err, &pending) {
		body := fiber.Map{"code": "plan_payment_pending", "title": "Payment pending", "status": 409}
		if pending.Invoice != nil {
			r := newInvoiceResponse(pending.Invoice, nil, h.today(), h.links)
			body["invoice_id"], body["checkout_url"] = r.ID, r.CheckoutURL
		}
		return c.Status(fiber.StatusConflict).JSON(body)
	}
	if err != nil {
		return fail(c, err)
	}
	return h.answer(c, inv)
}

func (h *planHandlers) cancel(c fiber.Ctx) error {
	if _, err := h.plan.Cancel(c.Context(), middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return h.answer(c, nil)
}

// answer renders the state after a read or a write; inv is a write's invoice.
func (h *planHandlers) answer(c fiber.Ctx, inv *billing.Invoice) error {
	sub := middleware.GetClaims(c).Sub
	st, err := h.plan.State(c.Context(), sub, h.now())
	if err != nil {
		return fail(c, err)
	}
	out := planResponse{
		LiveOnly: !middleware.GetSpace(c).Livemode(), DefaultPriceID: st.DefaultPriceID,
		Prices: []planPriceDTO{}, Levels: st.Levels, Projected: st.Projected,
		Usage: h.usage(c, sub),
	}
	for _, p := range st.Prices {
		out.Prices = append(out.Prices, planPriceDTO{ID: p.ID, Type: p.Type, UnitAmount: p.UnitAmount, Aggregation: p.Aggregation,
			Meter: p.Meter, IncludedQuantity: p.IncludedQuantity, Metadata: p.Metadata})
	}
	if s := st.Subscription; s != nil {
		out.Subscription = &planSubscriptionDTO{ID: s.ID, Status: s.Status, CancelAtPeriodEnd: s.CancelAtPeriodEnd, PriceIDs: st.PriceIDs, Period: st.Period, PendingChange: st.PendingChange}
	}
	if st.OpenInvoice != nil {
		r := newInvoiceResponse(st.OpenInvoice, nil, h.today(), h.links)
		out.OpenInvoice = &entitlementInvoice{ID: r.ID, TotalCents: r.Total, DueDate: r.DueDate, CheckoutURL: r.CheckoutURL}
	}
	if inv != nil {
		r := newInvoiceResponse(inv, nil, h.today(), h.links)
		out.Invoice = &r
	}
	return c.JSON(out)
}

// usage lists the personal workspaces the person owns, with ctech-account's
// counts. Unavailable when ctech-account fails or omits a count.
func (h *planHandlers) usage(c fiber.Ctx, sub string) planUsageDTO {
	u := planUsageDTO{Spaces: []planSpaceDTO{}}
	ws, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		slog.Warn("plan: listing workspaces failed", "error", err)
		u.Unavailable = true
		return u
	}
	for _, w := range ws {
		if space.WorkspaceKind(w.Kind) != space.KindPersonal || w.Role != "owner" || !space.IsOrganizationID(w.ID) {
			continue
		}
		if w.People == nil || w.PendingInvitations == nil {
			u.Unavailable = true
			continue
		}
		u.Spaces = append(u.Spaces, planSpaceDTO{ID: w.ID, DisplayName: w.DisplayName, People: *w.People, PendingInvitations: *w.PendingInvitations})
	}
	slices.SortStableFunc(u.Spaces, func(a, b planSpaceDTO) int { return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)) })
	return u
}
```

(imports: add `strings`. Check the field names of `invoiceResponse` — `ID`, `Total`, `DueDate`, `CheckoutURL` are what `describeEntitlement` already reads — and of `checks.email`.) `problem.go`:

```go
	case errors.Is(err, services.ErrNotAPlanPrice):
		return Unprocessable(err.Error()).WithCode("not_a_plan_price")
	case errors.Is(err, services.ErrCustomerRefMismatch):
		return New(409, TypeInvalidTransition, "Customer Mismatch",
			"this account's billing customer is not set up for plans; contact support").WithCode("customer_ref_mismatch")
	case errors.Is(err, services.ErrNoFinancePlan):
		return NotFound("no plan to cancel").WithCode("no_finance_plan")
```

(if `problem` cannot import `services` without a cycle — `services` imports `repositories`, `problem` imports `repositories`; `services` does not import `problem` — it is fine; otherwise move the three errors to `internal/domain/billing`.) `router.go`: `Deps.FinancePlan *services.FinancePlan`. `finance_http.go` `registerFinance`: after `mountSpaces(fin, h)`,

```go
	if d.FinancePlan.Enabled() {
		ph := &planHandlers{handlers: &handlers{clock: clock}, plan: d.FinancePlan, spaces: d.SpaceLister}
		if checkoutMounted(d) {
			ph.handlers.links = d.Links
		}
		mountPlan(fin, d.Spaces, idem, ph)
	}
```

`app.go`: `FinancePlan: services.NewFinancePlan(customers, subs, invoices, catalog, levels, subscriber, cfg.PortalOrganizationID)`.

- [ ] **Step 4: Run** — `go test ./... && make test-integration` → PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1 api/internal/problem/problem.go api/internal/app/app.go api/internal/services/finance_plan.go api/tests/integration/finance_plan_test.go api/tests/integration/finance_shared_spaces_test.go
git commit -m "feat(api): /console/finance/plan — read, choose, back to Free, Pessoal only"
```

---

### Task 4: The plan in the console's API client and mock

**Files:** Modify `ui/src/lib/api/financeTypes.ts`, `ui/src/lib/api/finance.ts`, `ui/src/lib/api/finance.test.ts`, `ui/src/dev/financeMockData.ts`, `ui/src/dev/financeMockData.test.ts`.

**Interfaces:**
- Produces:

```ts
export interface PlanPrice {
  id: string; type: "fixed" | "metered"; unit_amount: Cents
  aggregation: "" | "sum" | "max"; meter: string; included_quantity: number
  metadata: Record<string, string>
}
export interface PlanState {
  live_only: boolean
  subscription: {
    id: string; status: string; cancel_at_period_end: boolean; price_ids: string[]
    period: {period_start: string; period_end: string}
    pending_change: {price_ids: string[]; at: string} | null
  } | null
  open_invoice: {id: string; total_cents: Cents; due_date: string; checkout_url?: string} | null
  invoice?: {id: string; checkout_url?: string}
  default_price_id: string
  prices: PlanPrice[]
  usage: {unavailable: boolean; spaces: {id: string; display_name: string; people: number; pending_invitations: number}[]}
  levels: Record<string, {current: number; peak: number}>
  projected_ondemand_cents: Cents
}
export interface ChoosePlan {price_ids: string[]; name: string; email?: string}

financeKeys.plan(mode, space)               // ["finance", mode, sel, "plan"]
getPlan(c: FinanceCtx): Promise<PlanState>
choosePlan(c: FinanceCtx, body: ChoosePlan, idempotencyKey: string): Promise<PlanState>
cancelPlan(c: FinanceCtx, idempotencyKey: string): Promise<PlanState>
isPlanPaymentPending(e: unknown): {invoice_id?: string; checkout_url?: string} | null
```

- [ ] **Step 1: Write the failing tests.** `finance.test.ts`: `getPlan` sends both headers to `/v1.0/console/finance/plan`; `choosePlan` sends `Idempotency-Key` and the body as given; `isPlanPaymentPending` reads an axios error whose `response.data.code === "plan_payment_pending"`. `financeMockData.test.ts`: on the mock, `GET /plan` in `personal` returns 5 prices and `subscription: null`; `POST /plan` with Sob demanda while on a paid Basic (scenario `plano_basic`) returns Basic still current with `pending_change.at` = its period end and no invoice; `POST /plan` with Basic returns an INCOMPLETE subscription and an `invoice.checkout_url` of `/checkout?token=mock`; the same POST again with Pro → 409 `plan_payment_pending`; `GET /plan` in `org:${FINANCE_MOCK_HOUSE}` → 404 `plan_personal_only`; a scenario `?finance=plano_acima` seeds Free with three owned spaces (over the limit) and `plano_sob_demanda` seeds Sob demanda with `projected_ondemand_cents: 2830`.

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/api/finance.test.ts src/dev/financeMockData.test.ts; echo "vitest exit=$?"` → non-zero.

- [ ] **Step 2: Implement.** Types as above in `financeTypes.ts`. In `finance.ts`:

```ts
  plan: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "plan"] as const,
```

```ts
// --- plan (Pessoal only; always tenant zero, live) ---------------------------------

export const getPlan = (c: FinanceCtx) => read<PlanState>(c, "/plan")
export const choosePlan = (c: FinanceCtx, body: ChoosePlan, idempotencyKey: string) =>
  write<PlanState>(c, "POST", "/plan", body, idempotencyKey)
export const cancelPlan = (c: FinanceCtx, idempotencyKey: string) =>
  write<PlanState>(c, "POST", "/plan/cancel", {}, idempotencyKey)

/** The 409 that says "pay the first invoice before changing plan". */
export function isPlanPaymentPending(e: unknown): {invoice_id?: string; checkout_url?: string} | null {
  const data = (e as {response?: {status?: number; data?: Record<string, string>}})?.response
  return data?.status === 409 && data.data?.code === "plan_payment_pending" ? data.data : null
}
```

In `financeMockData.ts`, before the per-space state in `financeMock` (so a plan request never seeds a finance space): when `path` starts with `/plan`, check headers, answer 404 `plan_personal_only` unless `entry.kind === "personal_default"`, apply the Idempotency-Key replay for writes as the rest of the mock does, and route to a small `planRoute(method, path, body)` holding module state `{subscription, openInvoice}` seeded from the plan-1 catalogue (the five prices with their real ids, amounts and metadata) and `usage.spaces` = the owned personal workspace `FINANCE_MOCK_HOUSE` ("Casa", 1 person, 0 pending), with the scenarios above plus `plano_sem_uso` (usage unavailable), plus `plano_basic` (an ACTIVE Basic), each added to `FinanceScenario` and `FINANCE_SCENARIOS`. `resetFinanceMock` resets the plan state.

- [ ] **Step 3: Run** the Step 1 command → `vitest exit=0`; `npx tsc --noEmit`.
- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api ui/src/dev
git commit -m "feat(ui): the plan in the finance client and the dev mock"
```

---

### Task 5: Plan cards, limits and the suggestion (pure)

**Files:** Create `ui/src/lib/finance/plan.ts`, `ui/src/lib/finance/plan.test.ts`.

**Interfaces:**
- Consumes: `PlanState`, `PlanPrice` (Task 4).
- Produces:

```ts
export type PlanKey = string // metadata.plan: "free" | "basic" | "pro" | "ondemand" | future
export interface PlanOption {
  key: PlanKey
  priceIds: string[]            // [] for the default (Free is not subscribed to)
  isDefault: boolean
  monthly: Cents | null         // fixed price amount; null for metered-only
  metered: {meter: string; unitAmount: Cents; included: number}[]
  quotaSpaces: number           // Infinity for -1; 0 for missing/malformed
  quotaPeople: number
}
export function quota(v: string | undefined): number
export function planOptions(state: PlanState): PlanOption[]  // default first, fixed by amount, metered-only last
export function currentOption(state: PlanState, options: PlanOption[]): PlanOption // default when no subscription
export interface OverLimit {resource: "spaces" | "people"; used: number; limit: number; spaceName?: string}
export function overLimits(state: PlanState, current: PlanOption): OverLimit[] // [] when usage.unavailable
export function suggestBasic(state: PlanState, options: PlanOption[], current: PlanOption): PlanOption | null
export function pendingOption(state: PlanState, options: PlanOption[]): {option: PlanOption; at: string} | null
```

- [ ] **Step 1: Write the failing tests** — `plan.test.ts`:

```ts
import {describe, expect, it} from "vitest"

import type {PlanPrice, PlanState} from "@/lib/api/financeTypes"
import {currentOption, overLimits, planOptions, quota, suggestBasic} from "./plan"

const P = (id: string, plan: string, amount: number, extra: Partial<PlanPrice> = {}, md: Record<string, string> = {}): PlanPrice => ({
  id, type: "fixed", unit_amount: amount, aggregation: "", meter: "", included_quantity: 0, metadata: {plan, ...md}, ...extra,
})
const PRICES: PlanPrice[] = [
  P("price_finance_pro_monthly", "pro", 4990, {}, {quota_spaces: "10", quota_people_per_space: "10"}),
  P("price_finance_free", "free", 0, {}, {quota_spaces: "1", quota_people_per_space: "1"}),
  P("price_finance_ondemand_spaces", "ondemand", 490, {type: "metered", aggregation: "max", meter: "finance_spaces", included_quantity: 1},
    {quota_spaces: "-1", quota_people_per_space: "-1"}),
  P("price_finance_ondemand_people", "ondemand", 290, {type: "metered", aggregation: "max", meter: "finance_people", included_quantity: 1},
    {quota_spaces: "-1", quota_people_per_space: "-1"}),
  P("price_finance_basic_monthly", "basic", 1990, {}, {quota_spaces: "3", quota_people_per_space: "5"}),
]
const state = (over: Partial<PlanState> = {}): PlanState => ({
  live_only: false, subscription: null, open_invoice: null, default_price_id: "price_finance_free", prices: PRICES,
  usage: {unavailable: false, spaces: []}, levels: {}, projected_ondemand_cents: 0, ...over,
})

describe("plan options", () => {
  it("orders Free, Basic, Pro, Sob demanda and groups the on-demand prices", () => {
    const o = planOptions(state())
    expect(o.map(x => x.key)).toEqual(["free", "basic", "pro", "ondemand"])
    expect(o[0]).toMatchObject({isDefault: true, priceIds: [], quotaSpaces: 1, quotaPeople: 1})
    expect(o[3].priceIds.sort()).toEqual(["price_finance_ondemand_people", "price_finance_ondemand_spaces"])
    expect(o[3].quotaSpaces).toBe(Infinity)
    expect(o[3].metered).toHaveLength(2)
  })

  it("reads a malformed quota as 0", () => {
    expect(quota("três")).toBe(0)
    expect(quota(undefined)).toBe(0)
    expect(quota("-1")).toBe(Infinity)
    expect(quota("3")).toBe(3)
  })

  it("is Free without a subscription and the subscription's plan with one", () => {
    const o = planOptions(state())
    expect(currentOption(state(), o).key).toBe("free")
    const s = state({subscription: {id: "s", status: "ACTIVE", cancel_at_period_end: false, price_ids: ["price_finance_basic_monthly"], period: {period_start: "2026-10-01", period_end: "2026-11-01"}, pending_change: null}})
    expect(currentOption(s, o).key).toBe("basic")
  })
})

describe("over the limit", () => {
  const spaces = (n: number, people = 0) => Array.from({length: n}, (_, i) => ({id: `w${i}`, display_name: `E${i}`, people, pending_invitations: 0}))

  it("names spaces over the plan's allowance", () => {
    const s = state({usage: {unavailable: false, spaces: spaces(6)}})
    const basic = planOptions(s)[1]
    expect(overLimits(s, basic)).toEqual([{resource: "spaces", used: 6, limit: 3}])
  })

  it("counts pending invitations as people, per space", () => {
    const s = state({usage: {unavailable: false, spaces: [{id: "w", display_name: "Casa", people: 4, pending_invitations: 2}]}})
    expect(overLimits(s, planOptions(s)[1])).toEqual([{resource: "people", used: 6, limit: 5, spaceName: "Casa"}])
  })

  it("says nothing on unknown usage", () => {
    const s = state({usage: {unavailable: true, spaces: spaces(9)}})
    expect(overLimits(s, planOptions(s)[0])).toEqual([])
  })

  it("never limits Sob demanda", () => {
    const s = state({usage: {unavailable: false, spaces: spaces(30, 30)}})
    expect(overLimits(s, planOptions(s)[3])).toEqual([])
  })
})

describe("a scheduled change", () => {
  it("names the plan and the day it starts", () => {
    const s = state({subscription: {id: "s", status: "ACTIVE", cancel_at_period_end: false, price_ids: ["price_finance_basic_monthly"],
      period: {period_start: "2026-10-01", period_end: "2026-11-01"},
      pending_change: {price_ids: ["price_finance_ondemand_spaces", "price_finance_ondemand_people"], at: "2026-11-01"}}})
    const o = planOptions(s)
    expect(currentOption(s, o).key).toBe("basic") // the limits hold until then
    expect(pendingOption(s, o)).toEqual({option: o[3], at: "2026-11-01"})
  })
})

describe("Sob demanda above Basic", () => {
  const onDemand = {id: "s", status: "ACTIVE", cancel_at_period_end: false,
    price_ids: ["price_finance_ondemand_spaces", "price_finance_ondemand_people"], period: {period_start: "2026-10-01", period_end: "2026-11-01"}, pending_change: null}
  it("suggests Basic only when the projection passes it", () => {
    const over = state({subscription: onDemand, projected_ondemand_cents: 2830})
    const o = planOptions(over)
    expect(suggestBasic(over, o, currentOption(over, o))?.key).toBe("basic")
    const under = state({subscription: onDemand, projected_ondemand_cents: 1990})
    expect(suggestBasic(under, o, currentOption(under, o))).toBeNull()
    expect(suggestBasic(state({projected_ondemand_cents: 9999}), o, o[0])).toBeNull()
  })
})
```

- [ ] **Step 2: Run** — `npx vitest run --maxWorkers=2 src/lib/finance/plan.test.ts; echo "vitest exit=$?"` → non-zero.

- [ ] **Step 3: Implement** — `plan.ts`:

```ts
import type {PlanPrice, PlanState} from "@/lib/api/financeTypes"
import type {Cents} from "@/lib/api/types"

export type PlanKey = string

export interface PlanOption {
  key: PlanKey
  priceIds: string[]
  isDefault: boolean
  monthly: Cents | null
  metered: {meter: string; unitAmount: Cents; included: number}[]
  quotaSpaces: number
  quotaPeople: number
}

/**
 * A quota from price metadata (ADR 0008: billing's Go side never reads it; this
 * screen does). -1 is unlimited; missing or not an integer is 0 — the same rule
 * as ctech-account, so a catalogue mistake shows as "no room", never "unlimited".
 */
export function quota(v: string | undefined): number {
  if (v === "-1") return Infinity
  return v !== undefined && /^\d+$/.test(v) ? Number(v) : 0
}

export function planOptions(state: PlanState): PlanOption[] {
  const groups = new Map<PlanKey, PlanPrice[]>()
  for (const p of state.prices) {
    const key = p.metadata.plan ?? p.id
    groups.set(key, [...(groups.get(key) ?? []), p])
  }
  const options = [...groups.entries()].map(([key, prices]): PlanOption => {
    const fixed = prices.find(p => p.type === "fixed")
    const withQuota = prices.find(p => p.metadata.quota_spaces !== undefined) ?? prices[0]
    const isDefault = prices.some(p => p.id === state.default_price_id)
    return {
      key,
      priceIds: isDefault ? [] : prices.map(p => p.id),
      isDefault,
      monthly: fixed ? fixed.unit_amount : null,
      metered: prices.filter(p => p.type === "metered").map(p => ({meter: p.meter, unitAmount: p.unit_amount, included: p.included_quantity})),
      quotaSpaces: quota(withQuota.metadata.quota_spaces),
      quotaPeople: quota(withQuota.metadata.quota_people_per_space),
    }
  })
  const rank = (o: PlanOption) => (o.isDefault ? -1 : o.monthly === null ? Number.MAX_SAFE_INTEGER : o.monthly)
  return options.sort((a, b) => rank(a) - rank(b))
}

export function currentOption(state: PlanState, options: PlanOption[]): PlanOption {
  const ids = state.subscription?.price_ids ?? []
  return options.find(o => !o.isDefault && o.priceIds.length > 0 && o.priceIds.every(id => ids.includes(id)))
    ?? options.find(o => o.isDefault) ?? options[0]
}

export interface OverLimit {
  resource: "spaces" | "people"
  used: number
  limit: number
  spaceName?: string
}

/** D6: what the person has beyond the plan. Nothing on unknown usage. */
export function overLimits(state: PlanState, current: PlanOption): OverLimit[] {
  if (state.usage.unavailable) return []
  const out: OverLimit[] = []
  const owned = state.usage.spaces.length
  if (owned > current.quotaSpaces) out.push({resource: "spaces", used: owned, limit: current.quotaSpaces})
  for (const s of state.usage.spaces) {
    const people = s.people + s.pending_invitations
    if (people > current.quotaPeople) out.push({resource: "people", used: people, limit: current.quotaPeople, spaceName: s.display_name})
  }
  return out
}

/** The plan a scheduled change moves to, and when (decision 5a). */
export function pendingOption(state: PlanState, options: PlanOption[]): {option: PlanOption; at: string} | null {
  const pc = state.subscription?.pending_change
  if (!pc) return null
  const option = options.find(o => !o.isDefault && o.priceIds.length > 0 && o.priceIds.every(id => pc.price_ids.includes(id)))
  return option ? {option, at: pc.at} : null
}

/** The cheapest fixed plan Sob demanda's projection already costs more than. */
export function suggestBasic(state: PlanState, options: PlanOption[], current: PlanOption): PlanOption | null {
  if (current.monthly !== null || current.isDefault) return null
  const basic = options.find(o => o.key === "basic")
  return basic && basic.monthly !== null && state.projected_ondemand_cents > basic.monthly ? basic : null
}
```


- [ ] **Step 4: Run** → `vitest exit=0`.
- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/finance/plan.ts ui/src/lib/finance/plan.test.ts
git commit -m "feat(ui): plan cards, limits and the Basic suggestion from the catalogue"
```

---

### Task 6: The screen — with `/impeccable`

**Files:**
- Create: `ui/src/components/finance/PlanView.tsx`, `ui/src/components/finance/PlanView.test.tsx`, `ui/src/app/(finance)/finance/plans/layout.tsx`, `ui/src/app/(finance)/finance/plans/page.tsx`
- Modify: `ui/src/components/finance/FinanceNav.tsx`, `ui/src/components/finance/FinanceBottomNav.tsx`, `ui/src/components/finance/FinanceNav.test.tsx`, `ui/src/locales/{pt-BR,en}/finance.json`, `ui/DESIGN.md`

**Interfaces:**
- Consumes: Tasks 4–5; `useFinanceCtx`, `useFinanceSpaces`, `useFinanceMutation`, `useAuth().name`, `money()`, `useDocumentTitle`.
- Produces: the route `/finance/plans`; `FINANCE_SECTIONS` gains `{href: "/finance/plans", key: "plan", personalOnly: true}`.

- [ ] **Step 1: Run `/impeccable`** in `shape` mode for the plan screen, giving it `ui/PRODUCT.md`, `ui/DESIGN.md` (Finance section: compact density, sienna brand, danger the only saturated colour), spec § 7, decisions 4–9 of this plan and the contract below. Check `@aoctech/ui` and ctech-ui for a plan/pricing card and a usage meter first (none exists in `@aoctech/ui` 0.4.x's exports: `Badge`, `Button`, `Alert`, `EmptyState`, `ErrorState`, `PageHeader`, `Skeleton`, `Modal`, `Drawer`, `Segmented`, `Select`); if ctech-ui has one, use it; otherwise build the card from tokens and record it in `ui/DESIGN.md` as a **candidate for `@aoctech/ui`**. Required outcomes in `DESIGN.md` under "Finance → Plano": desktop layout (usage beside or above the cards), the **phone layout** (usage first, cards stacked, one primary action — the chosen card's button; others are secondary), how the current plan is marked (shape + label, never colour alone), how "unlimited" and "R$ 4,90 por espaço além do 1º" are written, the banner's place.

Behaviour contract (tested below):
- **Usage block:** "{n} de {limite} espaços" (`∞`/"ilimitado" for Sob demanda), then one row per owned personal workspace "{nome} · {people+pending} de {limite} pessoas" (pending shown as "inclui {k} convite(s) pendente(s)" when k > 0). Unavailable → "Não foi possível ver o uso agora." and no banner.
- **Over-limit banner** (`Alert`, warning tone): the Global Constraints copy, one line per `OverLimit`. Shown for the current plan only.
- **Cards:** one per `PlanOption`, the current one marked "Seu plano". Action per card: current → none (or "Pagar fatura" when `open_invoice` has a `checkout_url`); Free while on a paid plan → "Voltar ao Free" (confirmation `Modal`: "Seu plano continua até {period_end}. Nada é removido."), hidden when `cancel_at_period_end`; any other → "Assinar" / "Mudar para {plano}". Sob demanda's card shows its two unit prices and, when current, "Previsão do mês: {money(projected)}".
- **Choosing:** `useFinanceMutation(choosePlan)` with `{price_ids, name: useAuth().name ?? "", email}` — when `name` is empty, a `Modal` asks for the name to put on the invoice first. On success with `invoice.checkout_url` → `window.location.assign(checkout_url)`; with an invoice and no link → toast "Fatura criada — pague pelo portal." and a link to `/invoice?id=…`; without an invoice → toast "Plano alterado." and refetch. On `isPlanPaymentPending` → a `Modal` "Há uma fatura do plano anterior a pagar." with **Pagar** (to its `checkout_url`) and **Fechar**.
- **Scheduled change** (decision 5a): choosing Sob demanda while on Basic/Pro first shows a confirmation `Modal`: "Seu plano {plano} continua até {period_end}. O Sob demanda começa nesse dia e é cobrado no fim de cada mês pelo maior uso." Confirmed → `POST /plan`, no redirect, toast "Mudança agendada para {at}." The current card then shows "Muda para Sob demanda em {at}" and the Sob demanda card "Começa em {at}" with **Manter {plano}** (posts the current plan's ids, which clears the change). Switching from Sob demanda to Basic/Pro is immediate and redirects to the prorated invoice's `checkout_url`.
- **Suggestion:** when `suggestBasic` returns Basic: an inline note on the Sob demanda card, "Este mês o Sob demanda deve custar {money(projected)}. O Basic custa {money(basic)} — quer mudar?" with a secondary button; never automatic.
- **Test mode:** when `live_only`, a muted line under the header: "Planos são sempre do modo real."
- **Outside Pessoal:** the page renders an `EmptyState` "O plano é da pessoa. Abra Pessoal para vê-lo." with a button that selects Pessoal (`setSpace(PERSONAL)`); the nav has no entry there.

- [ ] **Step 2: Write the failing tests** — `PlanView.test.tsx` on the dev mock (`serveFinanceMock`, `renderWithQuery`, `lastWrite` from `finance.test-utils.tsx`; mock `useAuth` to return `{name: "Ana"}`; stub `window.location.assign`):

```tsx
import "@testing-library/jest-dom/vitest"
import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {lastWrite, renderWithQuery, serveFinanceMock} from "@/components/finance/finance.test-utils"
import {PlanView} from "@/components/finance/PlanView"
import {setFinanceScenario} from "@/dev/financeMockData"

vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => ({name: "Ana", authenticated: true, loading: false})}))

describe("PlanView", () => {
  let assign: ReturnType<typeof vi.fn>
  beforeEach(() => {
    window.localStorage.clear()
    setFinanceScenario("padrao") // the override is module state: reset it between tests
    assign = vi.fn()
    vi.stubGlobal("location", {...window.location, assign})
  })
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it("shows the four plans, Free marked as current", async () => {
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    const cards = await screen.findAllByRole("article")
    expect(cards.map(c => within(c).getByRole("heading").textContent)).toEqual(["Free", "Basic", "Pro", "Sob demanda"])
    expect(within(cards[0]).getByText("Seu plano")).toBeInTheDocument()
  })

  it("subscribes and sends the person to the checkout", async () => {
    const sent = serveFinanceMock()
    renderWithQuery(<PlanView/>)
    const basic = (await screen.findAllByRole("article"))[1]
    await userEvent.click(within(basic).getByRole("button", {name: /assinar/i}))
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/checkout?token=mock"))
    expect(lastWrite(sent, "post", "/plan")).toMatchObject({price_ids: ["price_finance_basic_monthly"], name: "Ana"})
  })

  it("sends to the pending invoice on plan_payment_pending", async () => {
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    const cards = await screen.findAllByRole("article")
    await userEvent.click(within(cards[1]).getByRole("button", {name: /assinar/i}))
    await waitFor(() => expect(assign).toHaveBeenCalledTimes(1))
    await userEvent.click(within((await screen.findAllByRole("article"))[2]).getByRole("button", {name: /mudar/i}))
    const dialog = await screen.findByRole("dialog")
    await userEvent.click(within(dialog).getByRole("button", {name: /pagar/i}))
    expect(assign).toHaveBeenLastCalledWith("/checkout?token=mock")
  })

  it("explains an over-limit plan without removing anything", async () => {
    setFinanceScenario("plano_acima")
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    expect(await screen.findByText(/Você tem 3 espaços e o plano Free permite 1\. Nada foi removido/)).toBeInTheDocument()
  })

  it("shows plans and no banner when usage is unavailable", async () => {
    setFinanceScenario("plano_sem_uso")
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    expect(await screen.findByText("Não foi possível ver o uso agora.")).toBeInTheDocument()
    expect(screen.getAllByRole("article")).toHaveLength(4)
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("schedules Sob demanda from a paid Basic instead of charging now", async () => {
    setFinanceScenario("plano_basic")
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    const ondemand = (await screen.findAllByRole("article"))[3]
    await userEvent.click(within(ondemand).getByRole("button", {name: /mudar/i}))
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: /confirmar/i}))
    expect(await screen.findByText(/Muda para Sob demanda em/)).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })

  it("suggests Basic when Sob demanda will cost more", async () => {
    setFinanceScenario("plano_sob_demanda")
    serveFinanceMock()
    renderWithQuery(<PlanView/>)
    expect(await screen.findByText(/O Basic custa R\$\s?19,90/)).toBeInTheDocument()
  })
})
```

(`plano_sem_uso` is a third Task 4 scenario: usage unavailable.) `FinanceNav.test.tsx`: add `it("has no Plano entry outside Pessoal")` — with the space set to `org:${FINANCE_MOCK_HOUSE}` the nav lists no "Plano"; in Pessoal it does.

Run: `npx vitest run --maxWorkers=2 src/components/finance/PlanView.test.tsx src/components/finance/FinanceNav.test.tsx; echo "vitest exit=$?"` → non-zero.

- [ ] **Step 3: Implement** inside the design brief. `plano/layout.tsx`:

```tsx
import type {Metadata} from "next"

export const metadata: Metadata = {title: "Plano · Finanças"}

export default function Layout({children}: LayoutProps<"/finance/plans">) {
  return children
}
```

`plano/page.tsx`:

```tsx
"use client"

import {useTranslation} from "react-i18next"

import {PlanView} from "@/components/finance/PlanView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** Finanças → Plano (spec § 7): the person's plan, from Pessoal. */
export default function FinancePlanPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.plan"))
  return <PlanView/>
}
```

`PlanView.tsx`: query `financeKeys.plan(ctx.mode, ctx.space)` → `getPlan(ctx)`, enabled only when `ctx.space.kind === "personal"` (render the outside-Pessoal `EmptyState` otherwise); `planOptions`/`currentOption`/`overLimits`/`suggestBasic` from Task 5; each card an `<article>` with an `<h3>` of the plan's display name (`t("finance.plan.names.{key}")`, falling back to the key). Mutations through `useFinanceMutation(choosePlan | cancelPlan, ctx => [financeKeys.plan(ctx.mode, ctx.space)])`. Loading: `Skeleton`s in the final layout's shape; error: `ErrorState`. `FinanceNav.tsx`: add the section with `personalOnly: true` and filter `FINANCE_SECTIONS` by `useSpace().kind === "personal"` before rendering both the select and the list; `currentFinanceSection` keeps matching it. `FinanceBottomNav.tsx`: add `sheet(\`${BASE}/plano\`, "plan", <BadgeCheck/>, manage)` to the *Mais* sheet only in Pessoal. Locales: `finance.nav.plan` ("Plano" / "Plan") and the `finance.plan.*` strings of the contract, in both languages.

- [ ] **Step 4: Run** — `npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `exit=0`; `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build` → clean. Browser check on `npm run dev:mock` at 320, 375, 768 and 1280 px, with `?finance=plano_acima` and `?finance=plano_sob_demanda`: usage first on a phone, cards stacked, one primary button, banner readable, the Plano entry absent after switching to *Casa*.
- [ ] **Step 5: Commit**

```bash
git add ui/src/components/finance ui/src/app/\(finance\)/finance/plans ui/src/locales ui/DESIGN.md
git commit -m "feat(finance): Plano — usage, plans, subscribe and the over-limit banner"
```

---

### Task 7: Records

**Files:** Modify `PLAN.md`, `docs/specs/2026-10-10-plans-design.md`.

- [ ] **Step 1:** `PLAN.md`: "Plans — deploy step 3 (Plano)" with links, what shipped, and **Found on the way**: (1) the projection uses billing's levels, because per-space counts cannot give distinct people; (2) a scheduled cancellation is not undone by choosing again; (3) Basic → Sob demanda mid-period can bill the same period in advance and in arrears; (4) a customer made by hand under another external ref blocks the plan (`customer_ref_mismatch`).
- [ ] **Step 2:** Spec status line: "Deploy steps 1 and 3 implemented"; § 7 gains the `/plan` routes, live-only, `plan_payment_pending`, and "Voltar ao Free = cancel at period end".
- [ ] **Step 3: Verify** (sequentially): API `gofmt -l ./internal ./cmd`, `go vet ./...`, `go test ./...`, `make test-integration`; then UI `npx vitest run --maxWorkers=2; echo "vitest exit=$?"`, `npx tsc --noEmit`, `npm run lint`, `npm run build`.
- [ ] **Step 4: Commit**

```bash
git add PLAN.md docs/specs/2026-10-10-plans-design.md
git commit -m "docs: plans deploy step 3 recorded"
```

---

## Spec coverage (self-review)

| Spec § 7 / § 9 test 7 | Task |
|---|---|
| `/finance/plans`, Pessoal only | 3, 6 |
| Usage "2 de 3 espaços", "4 de 5 pessoas", from ctech-account's route | 1, 3, 5, 6 |
| A card per plan, current marked, subscribe/change | 4, 5, 6 |
| `USER_{sub}` created once with the portal's pointer; change-plan service; redirect to `checkout_url` | 2, 3, 6 |
| Over-limit banner (D6) | 5, 6 |
| Sob demanda above Basic → suggestion, never automatic | 2 (projection), 5, 6 |
| Phone layout | 6 |
| Amendment: advance → Sob demanda at the period end; Sob demanda → Basic/Pro immediate | 1b, 2, 5, 6 |
