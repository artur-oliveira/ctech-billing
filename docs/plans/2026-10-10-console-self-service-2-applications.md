# Console self-service 2 — Applications, review and approval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An owner or admin of an organization asks for CTech Billing from the console (`/console/apply`); the test-mode tenant exists at once; CTech's owners and admins review the application in tenant zero's console (`/console/applications`); approving subscribes the organization's `ORG_` customer to `prod_billing` (R$ 0 early access) and creates the live tenant; cancelling that subscription suspends the live tenant (M2M writes `403 tenant_suspended`, reads and the console keep working, scheduled invoicing and dunning skip it).

**Architecture:** Applications are rows in the `organizations` table, in tenant zero's live partition (`APPLICATION#{id}`), with one pointer per account organization (`APPLICATION_ORG#{account_org_id}`) that is both the current-application summary and the concurrency guard: applying is one conditional transaction across the test tenant row, the application, the pointer, the audit row and (when absent) the `ORG_` customer. Listing by status rides the existing `lookup-index` (`{tz}#live#APPLICATIONS#{status}`), so there is no new table and no new index. Approval is a service (`services.Applications`) serialized by a lease on the application, reusing `Subscriber.Subscribe` and `OrganizationRepository.Create`. Suspension is an `EntitlementHook` on `SubscriptionRepository`: when a tenant-zero `prod_billing` subscription starts or stops granting service, the hook adds the live tenant's `suspended_at` change and its audit row to the subscription's **own** transaction; `app` wires it into every binary that builds the repository. A group middleware on the M2M surface refuses live writes of a suspended tenant; the sweep and the dunning job skip suspended tenants. The console UI replaces part 1's `NoMerchant` with the invitation, adds the apply form, a status band and the review screen.

**Tech Stack:** Go 1.27.2 (`api/go.mod`), Fiber v3.5, DynamoDB through api-commons v1.15.0 (`dynamo.Base`, `oauth2client`, `email`, `accountorgs`); Next.js 16 static export, React 19, TanStack Query 5, `@aoctech/ui` 0.4.1, vitest. Terraform for one SSM parameter and one environment variable.

**Spec:** [`docs/specs/2026-10-10-console-self-service.md`](../specs/2026-10-10-console-self-service.md) — § 4 (applications, review in tenant zero, approval as `prod_billing` entitlement, suspension, catalogue § 4.4), the application rows of § 6.1–6.2, § 7 items 1–3, § 8, § 9 step 3, § 10.2–10.3 (and the tenant-zero half of § 10.1). Decision record: [ADR 0028](../adr/0028-console-tenant-from-membership.md). Builds on **part 1**, [`2026-10-10-console-self-service-1-access.md`](2026-10-10-console-self-service-1-access.md), whose interfaces it consumes unchanged except where "Changes to part 1 interfaces" below says so. Part 3 (credentials, webhooks) consumes the interfaces listed in "Interfaces for part 3".

## Interfaces consumed from part 1 (exact)

```go
repositories.LookupOrganizationAccountPK(livemode bool, accountOrgID string) string
(*repositories.OrganizationRepository).GetByAccountOrganization(ctx, accountOrgID string, livemode bool) (*billing.Organization, error)
repositories.ErrAccountOrganizationTaken, repositories.ErrAccountOrganizationAmbiguous
billing.MerchantStatus (MerchantNone|MerchantTest|MerchantLive|MerchantRejected|MerchantSuspended), billing.MerchantStatusOf
space.ResolvedSpace.Role(), space.IsMerchantRole
middleware.RequireUserSession(), middleware.RequireMerchantRole(), middleware.ResolveMerchantTenant(src)
middleware.GetMerchantAccess(c) MerchantAccess{AccountOrganizationID, Role, Livemode}
middleware.GetSpace(c), middleware.GetOrganization(c), middleware.GetTenant(c), middleware.LookupMerchantTenant(...)
(*consoleHandlers).merchantStatus(ctx, sub, accountOrgID string) (billing.MerchantStatus, error)   // extended in Task 11
consoleSessionResponse, merchantOrganizationDTO                                                    // extended in Task 11
// tests: newLinkedOrg, newSpaceOrgID, fakeAccount (fake_account_test.go) with set/name/serve,
// apiEnv.account, apiEnv.accountOrg, (*apiEnv).consoleAs, billingHalfRoutes, allConsoleScopes
```

```ts
type MerchantStatus = "none" | "test" | "live" | "rejected" | "suspended"
interface MerchantOrganization {organization_id; selector; display_name; role; merchant_status}
interface ConsoleSession {organizations: MerchantOrganization[]; organizations_unavailable: boolean}
type ConsoleScope = {mode: Mode; org: string}
useConsoleScope(), ConsoleScopeProvider, pickMerchantOrg, tenantInMode, setMerchantOrg, useMerchantOrg
isNoMerchant(error), consoleKeys.session(), scopeHeaders(scope) (module-private in ui/src/lib/api/console.ts)
NoMerchant({name})            // deleted by Task 13, replaced by MerchantInvitation
i18n console.noMerchant.*, console.organization.*
```

## Changes to part 1 interfaces (additive, made by this plan)

- `billing.MerchantStatusOf(test, live *Organization, latest ApplicationStatus) MerchantStatus` — a third argument (Task 1). Part 1's two callers/tests are updated in the same task.
- `(*consoleHandlers).merchantStatus(ctx, sub, accountOrgID string) (billing.MerchantStatus, bool, error)` — the `bool` is "this organization's tenant is tenant zero" (Task 11).
- `merchantOrganizationDTO` gains `CanReviewApplications bool \`json:"can_review_applications"\``; the TS type gains `can_review_applications?: boolean` (optional so part 1 fixtures compile unchanged).
- `billingHalfRoutes` (part 1 test helper) excludes `/v1.0/console/merchant/`, which runs without a tenant and has its own spoof test (Task 11).
- `seedCTechCatalogue` (integration helper) gives each test's tenant zero a fresh account organization: after part 1 Task 3, `Create` refuses a second tenant on `01a04ed6-…`, so the helper as written fails on its second call (Task 6). If part 1's executor already fixed it, keep their fix and add only `seedTenantZero`.
- AccountMenu's console entry: shown when the session lists **any** organization (part 1: one that bills) (Task 13).

## Decisions worth the owner's attention (read before executing)

1. **ctech-account has no route that lists an organization's companies.** The spec says "the reach client already lists them"; it does not. What exists is the identity route `GET /v1.0/internal/organizations/{organization_id}/companies/{company_id}` (scope `internal:account:company-actor`, used by ctech-dfe's `accountclient.Company`). This plan validates `company_id` with that route (no dependency) and needs **one new ctech-account route for the picker**: `GET /v1.0/internal/organizations/{organization_id}/companies` → `{"companies":[{"company_id","legal_name","trade_name","tax_id","tax_id_kind"}]}`, behind the same scope. Until it ships, `GET /console/merchant/companies` answers `503 companies_unavailable` and the form says so. Both need billing's account client (`/ctech-billing/{env}/billing/account-client-id`) granted `internal:account:company-actor` (the `createclient -grant` mode from ctech-account's `2026-10-10-oauth-client-internal-route` plan).
2. **ctech-account has no route that returns a user's e-mail**, and billing's tokens carry no `email` claim (the UI asks for `openid profile` only). Instead of a new ctech-account route, the form asks for a **contact e-mail** (`contact_email`, required). It receives the decision e-mails and becomes the `ORG_` customer's e-mail. This deviates from spec § 4.2 ("the person who applied, from ctech-account's user record"); the alternative is an internal user route in ctech-account plus a client here.
3. **Suspension costs one GetItem on every live M2M write.** The M2M resolver reads the credential row, not the tenant row, so the spec's "the tenant row the request already reads" is not true there. The read is skipped for GET/HEAD and for test credentials. The alternative (copying the flag onto every credential row) couples part 3 to it.
4. **The hook is transactional and fails closed.** The `suspended_at` change commits in the subscription's own transaction; if the hook cannot read the customer or the tenant, the subscription change fails and its caller retries (the console shows 500, the jobs re-run). A cancellation never commits without its suspension.
5. **Skipped work stays in its day.** The sweep and dunning queues are partitioned by date; a suspended tenant's rows skipped on day D are not looked at on D+1. After a suspension is lifted, the operator re-runs `cmd/sweep -date` and `cmd/dunning -date` for each suspended day (both are idempotent; runbook in Task 9). `cmd/reconcile` (money that arrived) and `cmd/deliver` (webhooks about it) are **not** skipped.
6. **The mode header is still required** on `/console/merchant/*` (it runs `ResolveSpace`, which requires `X-Billing-Mode`); its value is ignored. Review routes work in either mode of tenant zero and always read the live partition.
7. **The company document is sealed** on the application row (`crypto.Sealer`, like a customer's CPF/CNPJ); it is copied in clear into the tenant's issuer block, as `SetIssuer` stores it today.
8. **`billing:organization:write` must be granted to the OAuth client `billing` at ctech-account before the UI deploy.** ctech-account fails the whole sign-in on a scope the client was not granted (`ui/src/lib/auth/scopes.ts`). Order: manifest publish → grant → API → UI (Task 11 runbook).
9. **An organization CTech provisioned by hand** (a linked tenant with no application) can apply: a test-only tenant is reused, a live one answers `409 already_merchant`.
10. **Small shared candidates:** the `accountclient` package duplicates ctech-dfe's `accountclient.Company`; both belong in api-commons `accountorgs` (proposed in the PR body, not done here). The `Textarea` component is local; ctech-ui has none (proposed upstream).

## Global Constraints

- Go commands from `api/`. Go 1.27.2. Every Go task ends with `gofmt -l ./internal ./cmd ./tests` (no output), `go vet ./...` and `go test ./... -race`; tasks that touch repositories, services with storage, routes or `tests/integration` also run `make test-integration` with DynamoDB Local (`docker compose -f docker-compose.test.yml up -d`, port 8124).
- UI commands from `ui/`. `npm ci` first if `node_modules/@aoctech/ui/package.json` is not 0.4.1. `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"` — judge vitest by its exit code. Then `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`.
- **Never run the Go and UI suites at the same time.**
- UI tasks are executed with the `/impeccable` skill loaded, on `@aoctech/ui` components; `ui/DESIGN.md` rules apply ("Copy and controls", "On a phone", "Navigation and the shell"). Every screen has **its own phone layout** (checked at 320, 375 and 768 px as well as 1280). pt-BR and en for every string. **No em dash (—) in any UI text** (i18n values). Read `node_modules/next/dist/docs/` for any Next API you touch (`ui/AGENTS.md`).
- Routes and endpoints in English (`/console/apply`, `/console/applications`, `/v1.0/console/merchant/application`); Portuguese only in i18n text (spec D7).
- Wire values, exactly: application status `pending|approved|rejected`; volume `up_to_10k|10k_to_50k|50k_to_200k|over_200k`; merchant status as part 1.
- Error codes, exactly: `tenant_suspended` (403, type `/problems/tenant-suspended`), `application_not_pending` (409), `already_merchant` (409), `application_busy` (409), `no_application` (404), `companies_unavailable` (503), `resource_not_found` (404, any tenant-zero route from another tenant), plus part 1's.
- Field error codes added: `too_short` (params `min`), `invalid_url`. Existing: `required`, `too_long`, `invalid_chars`, `invalid_id`, `invalid_email`, `unsupported_value`, `not_found`.
- Limits (one definition, `api/internal/limits` = `ui/src/lib/limits.json`): description 20–1000, website ≤ 200 (`https` only), rejection reason 10–500, contact e-mail ≤ 254.
- Catalogue: product `prod_billing` (owner key `billing`, **no** `default_price_id`), price `price_billing_early_access` (fixed, monthly, advance, `unit_amount` 0, `metadata.plan: early_access`).
- Tenant zero is `config.PortalOrganizationID` (`PORTAL_ORGANIZATION_ID`). Empty disables every route of this plan (not mounted) and the hook.
- Keys, exactly: `APPLICATION#{id}`, `APPLICATION_ORG#{account_org_id}`, `APPLICATION_LOCK#{id}` in `TenantPK(tenantZero, true)` of `organizations`; lookup `{tenantZero}#live#APPLICATIONS#{status}`.
- New user scope `billing:organization:write`: public, active, **no** `"m2m"` flag.
- Commit messages: Conventional Commits. **No attribution of any kind** (no `Co-Authored-By`, no "Generated with", no Claude/Anthropic mention) in commits or PR bodies.

## Review Focus

1. **An organization CTech provisioned by hand applies** (a linked test-only tenant, or a live one, and no pointer) — no second tenant on the same `ACCOUNT_ORG` key (which part 1 would then answer as `500 ambiguous` for every admin): a test tenant is reused, a live one is `409 already_merchant`. — Task 10 `TestAProvisionedTestTenantIsReusedNotDuplicated`, `TestAnOrganizationAlreadyLiveCannotApply`.
2. **CTech's own organization gets a `prod_billing` subscription that is later cancelled** (a test by an operator) — tenant zero is never suspended; nothing that serves the portal or ctech-dfe's integration stops. — Task 7 `TestTenantZeroIsNeverSuspended`.
3. **Two reviewers press Aprovar at once, or one double-clicks** — one subscription, one live tenant, both answers carry the same approved body or one is `409 application_busy`, never a 500. — Task 10 `TestConcurrentApprovalsMakeOneSubscriptionAndOneLiveTenant`.
4. **ctech-account goes down between the form loading and the submit** — `503`, and nothing is written: no test tenant, no pointer, no customer. — Task 11 `TestApplyingWhileCtechAccountIsDownWritesNothing`.
5. **A suspended merchant's integration keeps reading, and its test credential keeps writing** — GET with a live credential is `200`, POST with a test credential to the same organization is not refused, only live writes are `403 tenant_suspended`. — Task 8 `TestASuspendedTenantReadsAndTestModeKeepWorking`.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/domain/billing/application.go`, `application_test.go` (new) | `Application`, statuses, volumes, errors, `MerchantOwnerKey` |
| `api/internal/domain/billing/merchant.go`, `merchant_test.go` (modify) | `MerchantStatusOf` with the application status |
| `api/internal/domain/billing/organization.go` (modify) | `SuspendedAt`, `Suspended()`, `ErrTenantSuspended` |
| `api/internal/domain/billing/audit.go` (modify) | application and suspension audit actions |
| `api/internal/domain/id/id.go` (modify) | `PrefixApplication` |
| `api/internal/limits/limits.go`, `limits_test.go`, `ui/src/lib/limits.json` (modify) | the form's limits |
| `api/internal/middleware/scope.go`, `api/internal/oauthresource/scope-manifest.json` (modify) | `billing:organization:write` |
| `api/internal/accountclient/client.go`, `client_test.go` (new) | ctech-account companies (list, one) |
| `api/internal/repositories/keys.go`, `rows.go` (modify); `applications.go` (new) | application rows, pointer, lease, listing |
| `api/internal/repositories/customers.go`, `organizations.go` (modify) | `createWrites`, `organizationPut`, `suspensionWrites` |
| `api/internal/repositories/suspension.go` (new); `subscriptions.go` (modify) | `EntitlementHook`, `TenantSuspension` |
| `api/internal/email/ses.go`, `ses_test.go` (modify/new) | application messages |
| `api/internal/config/config.go` (modify) | `APPLICATIONS_NOTIFY_EMAIL` |
| `terraform/billing/locals.tf`, `ssm.tf`, `compute.tf`, `terraform/assets/bootstrap*.sh.tftpl` (modify) | the SSM parameter and env var |
| `api/tenants/ctech.json`, `api/internal/provision/plan_test.go` (modify) | `prod_billing`, early access price |
| `api/internal/services/applications.go` (new) | apply, approve, reject, notify |
| `api/internal/services/suspension.go` (new); `invoicing.go`, `dunning.go` (modify) | jobs skip suspended tenants |
| `api/internal/middleware/suspension.go`, `suspension_test.go` (new); `tenantzero.go` (new) | M2M write gate; tenant-zero gate |
| `api/internal/problem/problem.go` (modify) | new types and mappings |
| `api/internal/api/v1/console_applications.go`, `console_applications_dto.go`, `console_applications_test.go` (new); `console.go`, `console_dto.go`, `router.go`, `console_session_test.go` (modify) | routes and session |
| `api/internal/app/app.go`, `app_hook_test.go` (modify/new) | wiring: hook everywhere, services, clients |
| `api/cmd/sweep/main.go`, `api/cmd/dunning/main.go` (modify) | log the skipped count |
| `api/tests/integration/fake_account_test.go`, `finance_plan_test.go` (modify) | fake companies; `seedTenantZero` |
| `api/tests/integration/applications_test.go`, `suspension_test.go`, `console_applications_test.go` (new); `console_access_test.go` (modify) | § 10.1–10.3 |
| `.github/workflows/README.md` (modify) | prerequisites, deploy sequence, seed, lifting a suspension |
| `ui/src/lib/api/consoleTypes.ts`, `console.ts`, `console.test.ts` (modify) | types, calls, keys |
| `ui/src/lib/console/selection.tsx` (new); `nav.ts`, `nav.test.ts` (modify) | selection context; Solicitações nav |
| `ui/src/lib/auth/scopes.ts`, `scopes.test.ts` (modify) | the new scope |
| `ui/src/lib/console/applications.ts`, `applications.test.ts` (new) | pure rules (form validation, volumes) |
| `ui/src/components/ui/Textarea.tsx` (new) | multi-line input matching `Input` |
| `ui/src/components/console/MerchantInvitation.tsx`, `ApplyForm.tsx`, `ApplicationSubmitted.tsx`, `MerchantStatusBand.tsx`, `ApplicationCard.tsx`, `RejectDialog.tsx` and tests (new); `NoMerchant.tsx`, `NoMerchant.test.tsx` (delete) | screens |
| `ui/src/app/(console)/console/apply/page.tsx`, `applications/page.tsx` (new); `layout.tsx` (modify) | routes and shell |
| `ui/src/components/AccountMenu.tsx`, `AccountMenu.test.tsx` (modify) | widened console entry |
| `ui/src/dev/consoleMockData.ts`, `mockRuntime.ts`, `ui/src/lib/mockConfig.ts` (modify) | mock scenarios |
| `ui/src/locales/{pt-BR,en}/console.json`, `errors.json`; `ui/src/lib/errors.test.ts` (modify) | copy |
| `ui/DESIGN.md` (modify) | "Console: applying and reviewing (2026-10-10)" |

---
### Task 1: Domain — applications, suspension, merchant status, limits

**Files:**
- Create: `api/internal/domain/billing/application.go`, `api/internal/domain/billing/application_test.go`
- Modify: `api/internal/domain/billing/merchant.go`, `merchant_test.go` (part 1's)
- Modify: `api/internal/domain/billing/organization.go` (field and method after `DunningPolicy`)
- Modify: `api/internal/domain/billing/audit.go` (const block)
- Modify: `api/internal/domain/id/id.go` (prefix block)
- Modify: `api/internal/limits/limits.go`, `api/internal/limits/limits_test.go`, `ui/src/lib/limits.json`

**Interfaces:**
- Consumes: part 1's `MerchantStatus` constants.
- Produces: `type ApplicationStatus string` (`ApplicationPending`, `ApplicationApproved`, `ApplicationRejected`, `Valid()`); `type MonthlyVolume string` (`VolumeUpTo10k`, `Volume10kTo50k`, `Volume50kTo200k`, `VolumeOver200k`, `Valid()` where `""` is valid); `type Application struct` (fields below); `ErrApplicationNotPending`, `ErrAlreadyMerchant`, `ErrTenantSuspended`; `const MerchantOwnerKey = "billing"`; `Organization.SuspendedAt string`, `(*Organization).Suspended() bool`; `MerchantStatusOf(test, live *Organization, latest ApplicationStatus) MerchantStatus`; audit actions `AuditApplicationSubmitted`, `AuditApplicationApproved`, `AuditApplicationRejected`, `AuditOrganizationSuspended`, `AuditOrganizationReinstated`; `id.PrefixApplication = "apl_"`; limits `ApplicationDescriptionMin = 20`, `ApplicationDescription = 1000`, `Website = 200`, `RejectionReasonMin = 10` (max is the existing `Reason = 500`).

- [ ] **Step 1: Write the failing tests**

`api/internal/domain/billing/application_test.go`:

```go
package billing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationWireValues(t *testing.T) {
	raw, _ := json.Marshal([]ApplicationStatus{ApplicationPending, ApplicationApproved, ApplicationRejected})
	if string(raw) != `["pending","approved","rejected"]` {
		t.Fatalf("statuses = %s", raw)
	}
	raw, _ = json.Marshal([]MonthlyVolume{VolumeUpTo10k, Volume10kTo50k, Volume50kTo200k, VolumeOver200k})
	if string(raw) != `["up_to_10k","10k_to_50k","50k_to_200k","over_200k"]` {
		t.Fatalf("volumes = %s", raw)
	}
	for _, s := range []ApplicationStatus{ApplicationPending, ApplicationApproved, ApplicationRejected} {
		if !s.Valid() {
			t.Errorf("%q invalid", s)
		}
	}
	if ApplicationStatus("").Valid() || ApplicationStatus("PENDING").Valid() {
		t.Fatal("unknown status accepted")
	}
	if !MonthlyVolume("").Valid() {
		t.Fatal("the volume is optional: empty must be valid")
	}
	if MonthlyVolume("até R$ 10 mil").Valid() {
		t.Fatal("a label is not a wire value")
	}
}

// The company document is never on the wire from the domain struct: the DTO
// decides what a screen sees.
func TestApplicationNeverSerializesTheDocument(t *testing.T) {
	raw, _ := json.Marshal(Application{ID: "apl_1", CompanyTaxID: "12345678000190"})
	if strings.Contains(string(raw), "12345678000190") {
		t.Fatalf("tax id leaked: %s", raw)
	}
}

func TestOnlyALiveTenantCanBeSuspended(t *testing.T) {
	if (&Organization{Livemode: true}).Suspended() {
		t.Fatal("no suspended_at is not suspended")
	}
	if !(&Organization{Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}).Suspended() {
		t.Fatal("a live tenant with suspended_at is suspended")
	}
	// Test mode is never suspended (spec § 4.3), whatever a row says.
	if (&Organization{Livemode: false, SuspendedAt: "2026-10-10T12:00:00Z"}).Suspended() {
		t.Fatal("a test tenant reads as suspended")
	}
}
```

Replace part 1's `TestMerchantStatusFromTheTenantRows` in `api/internal/domain/billing/merchant_test.go` with:

```go
func TestMerchantStatusFromTheTenantRowsAndTheApplication(t *testing.T) {
	test := &Organization{ID: "org_1", Livemode: false}
	live := &Organization{ID: "org_1", Livemode: true}
	suspended := &Organization{ID: "org_1", Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}
	for _, c := range []struct {
		name       string
		test, live *Organization
		latest     ApplicationStatus
		want       MerchantStatus
	}{
		{"nothing", nil, nil, "", MerchantNone},
		{"applied, waiting", test, nil, ApplicationPending, MerchantTest},
		{"provisioned test tenant, no application", test, nil, "", MerchantTest},
		{"rejected keeps its test tenant", test, nil, ApplicationRejected, MerchantRejected},
		{"approved", test, live, ApplicationApproved, MerchantLive},
		{"provisioned live", nil, live, "", MerchantLive},
		{"suspended wins over everything", test, suspended, ApplicationApproved, MerchantSuspended},
		// A re-application after a rejection is pending again: test, not rejected.
		{"re-applied", test, nil, ApplicationPending, MerchantTest},
	} {
		if got := MerchantStatusOf(c.test, c.live, c.latest); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
```

In `api/internal/limits/limits_test.go`, extend `want`: inside `"text"` add `"applicationDescription": float64(ApplicationDescription), "website": float64(Website)`, and at top level add:

```go
		"minText": map[string]any{
			"applicationDescription": float64(ApplicationDescriptionMin),
			"rejectionReason":        float64(RejectionReasonMin),
		},
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/domain/billing/ ./internal/limits/ -run 'Application|Suspended|MerchantStatus|UIReads' -v`
Expected: FAIL — `undefined: ApplicationStatus`, `too many arguments in call to MerchantStatusOf`, `undefined: ApplicationDescription`.

- [ ] **Step 3: Implement**

`api/internal/domain/billing/application.go`:

```go
package billing

import "errors"

// MerchantOwnerKey is CTech Billing's own owner in tenant zero's catalogue
// (ADR 0016): an organization's subscription to a product with this owner is
// what makes it a merchant in live mode (spec § 4, D3).
const MerchantOwnerKey = "billing"

// ApplicationStatus is where an organization's request to bill through CTech
// Billing stands (spec § 4).
type ApplicationStatus string

const (
	ApplicationPending  ApplicationStatus = "pending"
	ApplicationApproved ApplicationStatus = "approved"
	ApplicationRejected ApplicationStatus = "rejected"
)

func (s ApplicationStatus) Valid() bool {
	switch s {
	case ApplicationPending, ApplicationApproved, ApplicationRejected:
		return true
	}
	return false
}

// MonthlyVolume is the applicant's own estimate, one of a fixed list whose
// labels live in the console's i18n. Optional: "" is valid.
type MonthlyVolume string

const (
	VolumeUpTo10k   MonthlyVolume = "up_to_10k"
	Volume10kTo50k  MonthlyVolume = "10k_to_50k"
	Volume50kTo200k MonthlyVolume = "50k_to_200k"
	VolumeOver200k  MonthlyVolume = "over_200k"
)

func (v MonthlyVolume) Valid() bool {
	switch v {
	case "", VolumeUpTo10k, Volume10kTo50k, Volume50kTo200k, VolumeOver200k:
		return true
	}
	return false
}

// ErrApplicationNotPending is a decision on an application already decided the
// other way (approving a rejected one, rejecting an approved one).
var ErrApplicationNotPending = errors.New("application is not pending")

// ErrAlreadyMerchant is an application from an organization that already has a
// live tenant: there is nothing left to ask for.
var ErrAlreadyMerchant = errors.New("organization already bills through CTech Billing in live mode")

// Application is one request, stored in tenant zero's live partition. It is
// history: a rejected one stays when the organization applies again.
//
// The organization and company names are copies taken when it was made, so the
// review screen needs no ctech-account call per row; they are display data.
type Application struct {
	ID                    string `dynamodbav:"id"                      json:"id"`
	AccountOrganizationID string `dynamodbav:"account_organization_id" json:"account_organization_id"`
	OrganizationName      string `dynamodbav:"organization_name"       json:"organization_name"`
	// TenantID is the merchant tenant's id, the same in both modes.
	TenantID         string `dynamodbav:"tenant_id"          json:"tenant_id"`
	CompanyID        string `dynamodbav:"company_id"         json:"company_id"`
	CompanyLegalName string `dynamodbav:"company_legal_name" json:"company_legal_name"`
	// CompanyTaxID is readable here and sealed in the row (repositories), like
	// a customer's. Never serialized from the domain struct.
	CompanyTaxID          string            `dynamodbav:"company_tax_id"                    json:"-"`
	Description           string            `dynamodbav:"description"                       json:"description"`
	Website               string            `dynamodbav:"website,omitempty"                 json:"website,omitempty"`
	ExpectedMonthlyVolume MonthlyVolume     `dynamodbav:"expected_monthly_volume,omitempty" json:"expected_monthly_volume,omitempty"`
	ContactEmail          string            `dynamodbav:"contact_email"                     json:"contact_email"`
	// AppliedBy is the ctech-account subject who applied.
	AppliedBy       string            `dynamodbav:"applied_by"                 json:"applied_by"`
	AppliedAt       string            `dynamodbav:"applied_at"                 json:"applied_at"`
	Status          ApplicationStatus `dynamodbav:"status"                     json:"status"`
	DecidedBy       string            `dynamodbav:"decided_by,omitempty"       json:"decided_by,omitempty"`
	DecidedAt       string            `dynamodbav:"decided_at,omitempty"       json:"decided_at,omitempty"`
	RejectionReason string            `dynamodbav:"rejection_reason,omitempty" json:"rejection_reason,omitempty"`
	// SubscriptionID is the prod_billing subscription approval created or reused.
	SubscriptionID string `dynamodbav:"subscription_id,omitempty" json:"subscription_id,omitempty"`
}
```

`api/internal/domain/billing/merchant.go` — replace `MerchantStatusOf` and update the comments of `MerchantRejected`/`MerchantSuspended` (drop "Computed from part 2 on"):

```go
// MerchantStatusOf derives the status from one account organization's tenant
// rows (nil when absent) and its latest application's status ("" when it never
// applied). A suspended live tenant wins: it is the fact a merchant must see.
func MerchantStatusOf(test, live *Organization, latest ApplicationStatus) MerchantStatus {
	switch {
	case live != nil && live.Suspended():
		return MerchantSuspended
	case live != nil:
		return MerchantLive
	case latest == ApplicationRejected:
		return MerchantRejected
	case test != nil:
		return MerchantTest
	default:
		return MerchantNone
	}
}
```

`api/internal/domain/billing/organization.go` — after `DunningPolicy`:

```go
	// SuspendedAt is set on a live tenant whose prod_billing entitlement ended
	// (spec § 4.3) and cleared when one is active again; RFC 3339 UTC. Written
	// only in the transaction of the subscription change that caused it
	// (repositories.TenantSuspension). Never on the wire.
	SuspendedAt string `dynamodbav:"suspended_at,omitempty" json:"-"`
```

and after `AuthorizeCharge`:

```go
// ErrTenantSuspended is a write refused because the live tenant is suspended.
var ErrTenantSuspended = errors.New("tenant is suspended")

// Suspended reports a live tenant whose writes are refused. Test mode is never
// suspended, whatever its row says.
func (o *Organization) Suspended() bool { return o.Livemode && o.SuspendedAt != "" }
```

`api/internal/domain/billing/audit.go` — inside the const block:

```go
	// The application lifecycle (spec § 4), audited in tenant zero's live
	// partition with the application id as the entity.
	AuditApplicationSubmitted = "application.submitted"
	AuditApplicationApproved  = "application.approved"
	AuditApplicationRejected  = "application.rejected"
	// A live tenant losing and regaining its prod_billing entitlement (spec
	// § 4.3), audited in the tenant's own live partition.
	AuditOrganizationSuspended  = "organization.suspended"
	AuditOrganizationReinstated = "organization.reinstated"
```

`api/internal/domain/id/id.go` — in the prefix block: `PrefixApplication = "apl_"`.

`api/internal/limits/limits.go` — in the const block:

```go
	// The merchant application (spec § 4.1–4.2).
	ApplicationDescriptionMin = 20
	ApplicationDescription    = 1000
	Website                   = 200
	RejectionReasonMin        = 10 // the maximum is Reason
```

`ui/src/lib/limits.json` — inside `"text"` add `"applicationDescription": 1000, "website": 200`, and at top level `"minText": {"applicationDescription": 20, "rejectionReason": 10}`.

- [ ] **Step 4: Run to verify they pass**

Run: `cd api && go test ./internal/domain/... ./internal/limits/ -race`
Expected: `ok` for every package. If `go vet ./...` reports the old two-argument `MerchantStatusOf` call in `internal/api/v1/console.go` (part 1's `merchantStatus`), pass `""` as the third argument there now; Task 11 replaces the call.

- [ ] **Step 5: Commit**

```bash
gofmt -l ./internal && go vet ./... && go test ./... -race
git add api/internal/domain api/internal/limits ui/src/lib/limits.json api/internal/api/v1/console.go
git commit -m "feat(billing): merchant applications, tenant suspension and the status they derive"
```

---

### Task 2: The `billing:organization:write` scope

**Files:**
- Modify: `api/internal/middleware/scope.go` (const block, `AllScopes`)
- Modify: `api/internal/oauthresource/scope-manifest.json` (after `billing:organization:read`)
- Test: existing `api/internal/oauthresource/resource_test.go` (`TestManifestMatchesEnforcedScopes`, `TestEveryScopeIsDescribedInBothLanguages`)

**Interfaces:**
- Produces: `middleware.ScopeOrganizationWrite = "billing:organization:write"`.

- [ ] **Step 1: Add the constant first (the failing state)** — in `scope.go`, after `ScopeOrganizationRead`:

```go
	// ScopeOrganizationWrite changes what the organization is as a merchant:
	// applying for CTech Billing and, in tenant zero, deciding applications
	// (spec § 6.1). A user scope only: never marked "m2m", an integration has no
	// business applying for its own organization.
	ScopeOrganizationWrite = "billing:organization:write"
```

and add `ScopeOrganizationWrite,` to `AllScopes` after `ScopeOrganizationRead`.

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/oauthresource/ -run TestManifestMatchesEnforcedScopes -v`
Expected: FAIL naming `billing:organization:write` as enforced but absent from the manifest.

- [ ] **Step 3: Publish it** — in `scope-manifest.json`, after the `billing:organization:read` entry:

```json
    {
      "name": "billing:organization:write",
      "descriptions": {
        "pt-BR": "Pedir acesso à cobrança para a organização e decidir pedidos.",
        "en": "Apply for billing on the organization's behalf and decide applications."
      },
      "visibility": "public",
      "status": "active"
    },
```

No `"m2m"` key.

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/oauthresource/ ./internal/middleware/ -race`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware/scope.go api/internal/oauthresource/scope-manifest.json
git commit -m "feat(scopes): billing:organization:write (user scope, not m2m)"
```

---

### Task 3: ctech-account companies client

**Files:**
- Create: `api/internal/accountclient/client.go`, `api/internal/accountclient/client_test.go`
- Modify: `api/tests/integration/fake_account_test.go` (part 1's fake: companies)

**Interfaces:**
- Consumes: api-commons `oauth2client.New(hc, cache, tokenURL, clientID, secret, scope)`, `cache.Backend`.
- Produces:

```go
package accountclient
const CompanyScope = "internal:account:company-actor"
type Config struct{ BaseURL, TokenURL, ClientID, ClientSecret string; Cache cache.Backend }
type Company struct{ ID, LegalName, TradeName, TaxID, TaxIDKind string } // json: company_id, legal_name, trade_name, tax_id, tax_id_kind
var ErrCompanyNotFound error
func New(cfg Config) *Client                                                         // nil when incomplete; every call on nil errors
func (c *Client) Companies(ctx context.Context, organizationID string) ([]Company, error)
func (c *Client) Company(ctx context.Context, organizationID, companyID string) (*Company, error) // ErrCompanyNotFound on 404 or a non-organization kind
```

Integration fake: `(*fakeAccount).company(org string, c accountclient.Company)`, field `companiesDown bool`.

- [ ] **Step 1: Write the failing tests** — `api/internal/accountclient/client_test.go`

```go
package accountclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const org = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), baseURL: srv.URL}
}

func TestCompaniesListsTheOrganizationsCompanies(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/internal/organizations/"+org+"/companies" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"companies":[{"company_id":"cmp_1","legal_name":"Padaria LTDA","trade_name":"Bom Dia","tax_id":"12345678000190","tax_id_kind":"cnpj"}]}`))
	})
	got, err := c.companiesWithToken(context.Background(), "tok", org)
	if err != nil || len(got) != 1 || got[0].ID != "cmp_1" || got[0].LegalName != "Padaria LTDA" || got[0].TaxID != "12345678000190" {
		t.Fatalf("companies = %+v, %v", got, err)
	}
}

func TestAMissingListRouteIsAnErrorNotAnEmptyList(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if got, err := c.companiesWithToken(context.Background(), "tok", org); err == nil {
		t.Fatalf("a 404 read as %v companies", got)
	}
}

func TestCompanyReadsTheIdentityRoute(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/internal/organizations/"+org+"/companies/cmp_1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"organization","tax_id":"12345678000190","tax_id_kind":"cnpj","legal_name":"Padaria LTDA","trade_name":"Bom Dia"}`))
	})
	got, err := c.companyWithToken(context.Background(), "tok", org, "cmp_1")
	if err != nil || got.ID != "cmp_1" || got.LegalName != "Padaria LTDA" {
		t.Fatalf("company = %+v, %v", got, err)
	}
	if _, err := c.companyWithToken(context.Background(), "tok", org, "cmp_other"); !errors.Is(err, ErrCompanyNotFound) {
		t.Fatalf("unknown company: %v", err)
	}
}

// A company in a personal workspace is not a merchant's billing company (D6).
func TestACompanyInAPersonalWorkspaceIsNotFound(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"personal","tax_id":"1","legal_name":"X"}`))
	})
	if _, err := c.companyWithToken(context.Background(), "tok", org, "cmp_1"); !errors.Is(err, ErrCompanyNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestANilClientRefuses(t *testing.T) {
	var c *Client
	if _, err := c.Companies(context.Background(), org); err == nil {
		t.Fatal("nil client answered")
	}
	if New(Config{BaseURL: "x"}) != nil {
		t.Fatal("an incomplete config built a client")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/accountclient/ -v`
Expected: FAIL — package has no non-test files / `undefined: Client`.

- [ ] **Step 3: Implement** — `api/internal/accountclient/client.go`

```go
// Package accountclient asks ctech-account about an organization's companies
// (ADR 0022: ctech-account owns company identity; billing reads it).
//
// It mirrors api-commons' accountorgs client (token per scope, capped bodies,
// any non-200 is an error) and ctech-dfe's accountclient.Company. Both copies
// belong in api-commons; proposed there, not done here.
package accountclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/oauth2client"
)

// CompanyScope is the internal scope both company routes sit behind.
const CompanyScope = "internal:account:company-actor"

const (
	requestTimeout = 5 * time.Second
	maxBody        = 256 << 10
)

// ErrCompanyNotFound is a company that is not one of the organization's (or
// whose workspace is personal). It is the answer to a form field, not an outage.
var ErrCompanyNotFound = errors.New("company not found in the organization")

type Config struct {
	BaseURL, TokenURL, ClientID, ClientSecret string
	Cache                                     cache.Backend
}

// Company is what billing needs to put a company on a merchant's invoices.
type Company struct {
	ID        string `json:"company_id"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name"`
	TaxID     string `json:"tax_id"`
	TaxIDKind string `json:"tax_id_kind"`
}

type Client struct {
	http    *http.Client
	tokens  *oauth2client.TokenManager
	baseURL string
}

// New returns nil when the credential is incomplete. A nil client is not
// permissive: every call errors.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	hc := &http.Client{Timeout: requestTimeout}
	return &Client{
		http:    hc,
		tokens:  oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, CompanyScope),
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
	}
}

func (c *Client) token(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ctech-account companies client is not configured")
	}
	tok, err := c.tokens.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("minting a service token: %w", err)
	}
	return tok, nil
}

// Companies lists the organization's companies (ctech-account route
// GET /v1.0/internal/organizations/{id}/companies; decision 1 of the plan).
func (c *Client) Companies(ctx context.Context, organizationID string) ([]Company, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	return c.companiesWithToken(ctx, tok, organizationID)
}

func (c *Client) companiesWithToken(ctx context.Context, token, organizationID string) ([]Company, error) {
	var out struct {
		Companies []Company `json:"companies"`
	}
	status, err := c.getJSON(ctx, token, fmt.Sprintf("%s/v1.0/internal/organizations/%s/companies",
		c.baseURL, url.PathEscape(organizationID)), &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("ctech-account answered %d listing companies", status)
	}
	if out.Companies == nil {
		out.Companies = []Company{}
	}
	return out.Companies, nil
}

// Company reads one company of the organization through the identity route.
func (c *Client) Company(ctx context.Context, organizationID, companyID string) (*Company, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	return c.companyWithToken(ctx, tok, organizationID, companyID)
}

func (c *Client) companyWithToken(ctx context.Context, token, organizationID, companyID string) (*Company, error) {
	var out struct {
		Company
		OrganizationKind string `json:"organization_kind"`
	}
	status, err := c.getJSON(ctx, token, fmt.Sprintf("%s/v1.0/internal/organizations/%s/companies/%s",
		c.baseURL, url.PathEscape(organizationID), url.PathEscape(companyID)), &out)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusNotFound:
		return nil, ErrCompanyNotFound
	case status != http.StatusOK:
		return nil, fmt.Errorf("ctech-account answered %d reading a company", status)
	case out.OrganizationKind != "" && out.OrganizationKind != "organization":
		return nil, ErrCompanyNotFound
	}
	out.Company.ID = companyID
	return &out.Company, nil
}

// getJSON returns the status, decoding the body only on 200.
func (c *Client) getJSON(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calling ctech-account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return 0, fmt.Errorf("decoding the answer: %w", err)
	}
	return resp.StatusCode, nil
}
```

- [ ] **Step 4: Teach the integration fake the two routes** — in `api/tests/integration/fake_account_test.go`, add to `fakeAccount`:

```go
	companies     map[string][]accountclient.Company // org -> companies
	companiesDown bool                               // both company routes answer 503
```

the setter:

```go
// company registers one of an organization's companies.
func (a *fakeAccount) company(org string, c accountclient.Company) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.companies == nil {
		a.companies = map[string][]accountclient.Company{}
	}
	a.companies[org] = append(a.companies[org], c)
}
```

and, in `serve`, replace the `/v1.0/internal/organizations/` case with:

```go
		case strings.HasPrefix(r.URL.Path, "/v1.0/internal/organizations/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1.0/internal/organizations/"), "/")
			switch {
			case len(parts) == 3 && parts[1] == "members":
				m, ok := a.members[parts[0]+"|"+parts[2]]
				if !ok {
					_, _ = w.Write([]byte(`{"member":false}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"member": true, "kind": m[0], "role": m[1]})
			case len(parts) >= 2 && parts[1] == "companies" && a.companiesDown:
				http.Error(w, "down", http.StatusServiceUnavailable)
			case len(parts) == 2 && parts[1] == "companies":
				list := a.companies[parts[0]]
				if list == nil {
					list = []accountclient.Company{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"companies": list})
			case len(parts) == 3 && parts[1] == "companies":
				for _, c := range a.companies[parts[0]] {
					if c.ID == parts[2] {
						_ = json.NewEncoder(w).Encode(map[string]any{
							"organization_id": parts[0], "organization_kind": "organization",
							"tax_id": c.TaxID, "tax_id_kind": c.TaxIDKind, "legal_name": c.LegalName, "trade_name": c.TradeName,
						})
						return
					}
				}
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
```

(import `gopkg.aoctech.app/billing/api/internal/accountclient`.)

- [ ] **Step 5: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./internal/accountclient/ -race -v`
Expected: PASS. Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS` (the membership branch is unchanged in behaviour).

- [ ] **Step 6: Commit**

```bash
git add api/internal/accountclient api/tests/integration/fake_account_test.go
git commit -m "feat(accountclient): read an organization's companies from ctech-account"
```

---
### Task 4: Repository — applications, the pointer and the one-transaction apply

**Files:**
- Modify: `api/internal/repositories/keys.go` (Entity list, sort-key prefixes, builders after `CustomerOrgSK`)
- Modify: `api/internal/repositories/rows.go` (two row types)
- Modify: `api/internal/repositories/customers.go` (`Create` split into `createWrites`)
- Modify: `api/internal/repositories/organizations.go` (`organizationPut`, used by `Create`)
- Create: `api/internal/repositories/applications.go`
- Test: `api/tests/integration/applications_test.go` (new)

**Interfaces:**
- Consumes: `TenantPK`, `newKeys`, `buildAuditItem`, `Encode`, `Decode`, `DecodeItems`, `IsConditionFailed`, `conflictErr`, `crypto.Sealer`, part 1's `LookupOrganizationAccountPK`.
- Produces:

```go
const EntityApplication Entity = "APPLICATION"
func ApplicationSK(applicationID string) string            // "APPLICATION#{id}"
func ApplicationOrgSK(accountOrgID string) string          // "APPLICATION_ORG#{id}"
func LookupApplicationsPK(tenantZero string, status billing.ApplicationStatus) string // "{tz}#live#APPLICATIONS#{status}"

var ErrNoTenantZero, ErrApplicationBusy error
type ApplyInput struct {
	Application        *billing.Application  // caller fills everything but TenantID, Status, AppliedAt
	ExistingTestTenant *billing.Organization // a provisioned test tenant to reuse; nil creates one
	Customer           *billing.Customer     // the ORG_ customer to create in tenant zero; nil when it exists
	Actor, RequestID   string
}
func NewApplicationRepository(db *dynamodb.Client, cfg *config.Config) *ApplicationRepository
func (r *ApplicationRepository) Apply(ctx, in ApplyInput, now time.Time) (*billing.Application, bool, error) // (current, created, err)
func (r *ApplicationRepository) Get(ctx, applicationID string) (*billing.Application, error)
func (r *ApplicationRepository) Current(ctx, accountOrgID string) (*billing.Application, error)      // ErrNotFound when it never applied
func (r *ApplicationRepository) LatestStatus(ctx, accountOrgID string) (billing.ApplicationStatus, error) // "" when it never applied; one GetItem
func (r *ApplicationRepository) List(ctx, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*Page[billing.Application], error)
func (r *ApplicationRepository) Lock(ctx, applicationID, token string, now time.Time, lease time.Duration) (func(), error) // ErrApplicationBusy
func (r *ApplicationRepository) Decide(ctx, app *billing.Application, to billing.ApplicationStatus, subscriptionID, reason, actor, requestID string, now time.Time) error // billing.ErrApplicationNotPending
// package-internal: (*CustomerRepository).createWrites, organizationPut
```

- [ ] **Step 1: Write the failing integration tests** — `api/tests/integration/applications_test.go`

```go
//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// appRepo is an application repository whose tenant zero is tz.
func appRepo(t *testing.T, tz string) *repositories.ApplicationRepository {
	t.Helper()
	cfg := *testCfg
	cfg.PortalOrganizationID = tz
	return repositories.NewApplicationRepository(testDB, &cfg)
}

func newApplication(acct string) *billing.Application {
	return &billing.Application{
		ID: id.NewWithPrefix(id.PrefixApplication), AccountOrganizationID: acct, OrganizationName: "Padaria Bom Dia",
		CompanyID: "cmp_1", CompanyLegalName: "Padaria Bom Dia LTDA", CompanyTaxID: "12345678000190",
		Description: "Assinaturas de pão para empresas do bairro", ContactEmail: "contato@padaria.example",
		AppliedBy: "usr_" + id.New(),
	}
}

func orgCustomer(tz, acct string) *billing.Customer {
	return &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: tz, Livemode: true,
		ExternalRef: repositories.OrgRefPrefix + acct, Name: "Padaria Bom Dia", Email: "contato@padaria.example",
	}
}

func apply(t *testing.T, r *repositories.ApplicationRepository, tz, acct string, withCustomer bool) (*billing.Application, bool, error) {
	t.Helper()
	in := repositories.ApplyInput{Application: newApplication(acct), Actor: "user:x", RequestID: "req_1"}
	if withCustomer {
		in.Customer = orgCustomer(tz, acct)
	}
	return r.Apply(ctxT(t), in, now())
}

func TestApplyingCreatesTheTestTenantTheApplicationAndTheCustomerAtOnce(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)

	app, created, err := apply(t, r, tz, acct, true)
	if err != nil || !created || app.Status != billing.ApplicationPending || app.TenantID == "" {
		t.Fatalf("apply = %+v, %v, %v", app, created, err)
	}
	test, err := orgRepo().GetByAccountOrganization(ctx, acct, false)
	if err != nil || test.ID != app.TenantID || test.PayoutStatus != billing.PayoutNotConfigured ||
		test.LegalName != "Padaria Bom Dia LTDA" || test.IssuerTaxID != "12345678000190" || test.DisplayName != "Padaria Bom Dia" {
		t.Fatalf("test tenant = %+v, %v", test, err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a live tenant exists before approval: %v", err)
	}
	if c, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctx, tz, true, acct); err != nil || c.Email != "contato@padaria.example" {
		t.Fatalf("ORG_ customer = %+v, %v", c, err)
	}
	got, err := r.Get(ctx, app.ID)
	if err != nil || got.CompanyTaxID != "12345678000190" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	// Sealed at rest, like a customer's document.
	raw := rawGet(t, repositories.TableOrganizations, repositories.TenantPK(tz, true), repositories.ApplicationSK(app.ID))
	if v := raw["company_tax_id"].(*types.AttributeValueMemberS).Value; v == "12345678000190" {
		t.Fatal("the company document is stored in the clear")
	}
	if st, err := r.LatestStatus(ctx, acct); err != nil || st != billing.ApplicationPending {
		t.Fatalf("LatestStatus = %q, %v", st, err)
	}
}

func TestASecondApplicationWhilePendingReturnsThePendingOne(t *testing.T) {
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	first, _, err := apply(t, r, tz, acct, true)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := apply(t, r, tz, acct, false)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second = %+v, created=%v, %v", second, created, err)
	}
}

// Spec § 10.2: two concurrent applications make one tenant and one pending application.
func TestConcurrentApplicationsMakeOneTenantAndOnePendingApplication(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	var wg sync.WaitGroup
	ids := make([]string, 5)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app, _, err := apply(t, r, tz, acct, true)
			switch {
			case err == nil:
				ids[i] = app.ID
			case errors.Is(err, repositories.ErrTransactionConflict), errors.Is(err, repositories.ErrConcurrentModification):
				// A lost race the service retries; never a second row.
			default:
				t.Errorf("apply %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	cur, err := r.Current(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ids {
		if got != "" && got != cur.ID {
			t.Fatalf("an apply answered %s, current is %s", got, cur.ID)
		}
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, false); err != nil {
		t.Fatalf("one test tenant, readable without ambiguity: %v", err)
	}
	page, err := r.List(ctx, billing.ApplicationPending, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range page.Items {
		if a.AccountOrganizationID == acct {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d pending applications for the organization, want 1", n)
	}
}

func TestReapplyingAfterARejectionKeepsTheTenantAndTheHistory(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	first, _, _ := apply(t, r, tz, acct, true)
	if err := r.Decide(ctx, first, billing.ApplicationRejected, "", "Site fora do ar", "user:rev", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.LatestStatus(ctx, acct); st != billing.ApplicationRejected {
		t.Fatalf("status after rejection = %q", st)
	}
	second, created, err := apply(t, r, tz, acct, false)
	if err != nil || !created || second.ID == first.ID || second.TenantID != first.TenantID {
		t.Fatalf("re-apply = %+v, %v, %v", second, created, err)
	}
	old, err := r.Get(ctx, first.ID)
	if err != nil || old.Status != billing.ApplicationRejected || old.RejectionReason != "Site fora do ar" {
		t.Fatalf("history = %+v, %v", old, err)
	}
	rejected, _ := r.List(ctx, billing.ApplicationRejected, 100, nil)
	found := false
	for _, a := range rejected.Items {
		found = found || a.ID == first.ID
	}
	if !found {
		t.Fatal("the rejected application left the rejected list")
	}
}

func TestADecisionIsConditionalOnPending(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	app, _, _ := apply(t, r, tz, acct, true)
	if err := r.Decide(ctx, app, billing.ApplicationApproved, "sub_1", "", "user:rev", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	stale := *app
	stale.Status = billing.ApplicationPending
	if err := r.Decide(ctx, &stale, billing.ApplicationRejected, "", "tarde demais", "user:rev2", "req_3", now()); !errors.Is(err, billing.ErrApplicationNotPending) {
		t.Fatalf("deciding an approved application: %v", err)
	}
	got, _ := r.Get(ctx, app.ID)
	if got.Status != billing.ApplicationApproved || got.SubscriptionID != "sub_1" || got.DecidedBy != "user:rev" {
		t.Fatalf("after = %+v", got)
	}
}

func TestTheDecisionLeaseAdmitsOneAtATime(t *testing.T) {
	ctx := ctxT(t)
	r := appRepo(t, "org_"+id.New())
	release, err := r.Lock(ctx, "apl_x", "a", now(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(ctx, "apl_x", "b", now(), 30*time.Second); !errors.Is(err, repositories.ErrApplicationBusy) {
		t.Fatalf("second lock: %v", err)
	}
	release()
	if _, err := r.Lock(ctx, "apl_x", "c", now(), 30*time.Second); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestApplyingWithoutATenantZeroIsRefused(t *testing.T) {
	r := appRepo(t, "")
	if _, _, err := apply(t, r, "", newSpaceOrgID(), false); !errors.Is(err, repositories.ErrNoTenantZero) {
		t.Fatalf("err = %v", err)
	}
}

// A provisioned test tenant is reused: no second row on the ACCOUNT_ORG key.
func TestApplyReusesAProvisionedTestTenant(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	existing := newLinkedOrg(t, false, acct)
	r := appRepo(t, tz)
	in := repositories.ApplyInput{Application: newApplication(acct), ExistingTestTenant: existing, Customer: orgCustomer(tz, acct), Actor: "user:x"}
	app, _, err := r.Apply(ctx, in, now())
	if err != nil || app.TenantID != existing.ID {
		t.Fatalf("apply = %+v, %v", app, err)
	}
	if got, err := orgRepo().GetByAccountOrganization(ctx, acct, false); err != nil || got.ID != existing.ID {
		t.Fatalf("lookup = %+v, %v", got, err)
	}
}
```

(`rawGet` is the existing helper in `repositories_test.go`; `orgRepo` is part 1's helper in `organization_lookup_test.go`.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Appl|Reapply|Decision|DecisionLease' -v`
Expected: FAIL — `undefined: repositories.NewApplicationRepository`, `undefined: repositories.ApplicationSK`.

- [ ] **Step 3: Keys and rows**

`api/internal/repositories/keys.go` — in the Entity block: `EntityApplication Entity = "APPLICATION"`. In the sort-key block:

```go
	skApplication     = "APPLICATION#"
	skApplicationOrg  = "APPLICATION_ORG#"
	skApplicationLock = "APPLICATION_LOCK#"
```

after `CustomerOrgSK`:

```go
// ApplicationSK, ApplicationOrgSK and the lease live in tenant zero's live
// partition of the organizations table (spec § 6.2): an application is
// something CTech's own tenant holds about a would-be merchant, like a
// customer, and it is decided there.
func ApplicationSK(applicationID string) string { return skApplication + applicationID }

// ApplicationOrgSK is the pointer from an account organization to its current
// application and its tenant id. It is also the guard: one first application
// per organization (attribute_not_exists), one re-application per rejection
// (conditional on the rejected id).
func ApplicationOrgSK(accountOrgID string) string { return skApplicationOrg + accountOrgID }

// LookupApplicationsPK partitions applications by status on lookup-index, so
// "pending, oldest first" is a key condition. The sort key is the row's own
// sk (APPLICATION# + ULID): creation order. A decision moves the row.
func LookupApplicationsPK(tenantZero string, status billing.ApplicationStatus) string {
	return TenantPK(tenantZero, true) + "#APPLICATIONS#" + string(status)
}
```

(`keys.go` gains the `billing` import.)

`api/internal/repositories/rows.go`:

```go
// applicationRow is one application. CompanyTaxID is sealed in the row.
type applicationRow struct {
	keys
	billing.Application
	LookupPK string `dynamodbav:"lookup_pk"`
}

// applicationOrgRow points an account organization at its current application.
type applicationOrgRow struct {
	keys
	AccountOrganizationID string                    `dynamodbav:"account_organization_id"`
	TenantID              string                    `dynamodbav:"tenant_id"`
	ApplicationID         string                    `dynamodbav:"application_id"`
	Status                billing.ApplicationStatus `dynamodbav:"status"`
}
```

- [ ] **Step 4: Split the two writes the transaction reuses**

`api/internal/repositories/customers.go` — move the body of `Create` that builds `writes` (from `row, err := r.customerRowOf(c, now)` through appending the audit item) into:

```go
// createWrites is Create's transaction without committing it, so a caller in
// this package can make a customer part of a larger one (the application,
// spec § 4.1). isOrg reports an ORG_ customer, for the caller's error message.
func (r *CustomerRepository) createWrites(c *billing.Customer, actor, requestID string, now time.Time) (writes []types.TransactWriteItem, isOrg bool, err error) {
	if err := c.Metadata.Validate(); err != nil {
		return nil, false, err
	}
	if actor == "" {
		return nil, false, fmt.Errorf("repositories: creating customer %s needs an actor", c.ID)
	}
	// … the existing row, user pointer, org pointer and audit item, unchanged,
	// each `return err` becoming `return nil, false, err` …
	return writes, isOrg, nil
}
```

and `Create` becomes:

```go
func (r *CustomerRepository) Create(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	writes, isOrg, err := r.createWrites(c, actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		if isOrg {
			orgID, _ := OrganizationOfRef(c.ExternalRef)
			return fmt.Errorf("%w: %s", ErrOrganizationAlreadyCustomer, orgID)
		}
		if c.UserID != "" {
			return fmt.Errorf("%w: %s", ErrUserAlreadyCustomer, c.UserID)
		}
		return fmt.Errorf("customer %s already exists", c.ID)
	}
	return conflictErr(err)
}
```

(Keep every existing comment, moved with its code.)

`api/internal/repositories/organizations.go` — add, and make `Create` use it for its put (after its own `ensureAccountOrganizationFree`):

```go
// organizationPut is the conditional put of a new tenant row with its console
// lookup key (part 1: only a linked tenant gets one). It does not check the
// key is free: Create reads first, and the application transaction is guarded
// by its pointer row instead.
func organizationPut(base Base, org *billing.Organization, now time.Time) (types.TransactWriteItem, error) {
	if org.PayoutStatus == "" {
		org.PayoutStatus = billing.PayoutNotConfigured
	}
	row := organizationRow{
		keys:         newKeys(TenantPK(org.ID, org.Livemode), OrganizationSK(), RetentionOrganization, now),
		Organization: *org,
	}
	if org.AccountOrganizationID != "" {
		row.LookupPK = LookupOrganizationAccountPK(org.Livemode, org.AccountOrganizationID)
	}
	item, err := Encode(row)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return base.BuildPutTxItemIfAbsent(item), nil
}
```

- [ ] **Step 5: The repository** — `api/internal/repositories/applications.go`

```go
package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// ErrNoTenantZero is an application where no tenant zero is configured.
var ErrNoTenantZero = errors.New("no tenant zero is configured")

// ErrApplicationBusy is a decision already in progress on the application.
var ErrApplicationBusy = errors.New("another decision on this application is in progress")

// ApplicationRepository stores merchant applications in tenant zero's live
// partition of the organizations table (spec § 4, § 6.2).
type ApplicationRepository struct {
	rows       Base // organizations: tenants, applications, pointers, leases
	audit      Base
	customers  *CustomerRepository
	seal       *crypto.Sealer
	tenantZero string
}

func NewApplicationRepository(db *dynamodb.Client, cfg *config.Config) *ApplicationRepository {
	return &ApplicationRepository{
		rows:       NewBase(db, cfg, TableOrganizations),
		audit:      NewBase(db, cfg, TableAudit),
		customers:  NewCustomerRepository(db, cfg),
		seal:       crypto.NewSealer(cfg.FieldEncryptionKey),
		tenantZero: cfg.PortalOrganizationID,
	}
}

type ApplyInput struct {
	Application        *billing.Application
	ExistingTestTenant *billing.Organization
	Customer           *billing.Customer
	Actor, RequestID   string
}

func (r *ApplicationRepository) pk() string { return TenantPK(r.tenantZero, true) }

func (r *ApplicationRepository) pointer(ctx context.Context, accountOrgID string) (*applicationOrgRow, error) {
	item, err := r.rows.GetItem(ctx, r.pk(), ApplicationOrgSK(accountOrgID))
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[applicationOrgRow](item)
}

// LatestStatus is the organization's current application status, "" when it
// never applied: one GetItem, for the console session (part 1's merchantStatus).
func (r *ApplicationRepository) LatestStatus(ctx context.Context, accountOrgID string) (billing.ApplicationStatus, error) {
	if r.tenantZero == "" {
		return "", nil
	}
	p, err := r.pointer(ctx, accountOrgID)
	if err != nil || p == nil {
		return "", err
	}
	return p.Status, nil
}

// Current is the organization's current application.
func (r *ApplicationRepository) Current(ctx context.Context, accountOrgID string) (*billing.Application, error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	p, err := r.pointer(ctx, accountOrgID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("%w: no application for %s", ErrNotFound, accountOrgID)
	}
	return r.Get(ctx, p.ApplicationID)
}

func (r *ApplicationRepository) Get(ctx context.Context, applicationID string) (*billing.Application, error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	item, err := r.rows.GetItem(ctx, r.pk(), ApplicationSK(applicationID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: application %s", ErrNotFound, applicationID)
	}
	row, err := Decode[applicationRow](item)
	if err != nil {
		return nil, err
	}
	return r.open(row)
}

func (r *ApplicationRepository) open(row *applicationRow) (*billing.Application, error) {
	a := row.Application
	plain, err := r.seal.Open(a.CompanyTaxID)
	if err != nil {
		return nil, fmt.Errorf("opening the company document of %s: %w", a.ID, err)
	}
	a.CompanyTaxID = plain
	return &a, nil
}

// Apply writes, in ONE conditional transaction (spec § 4.1): the test tenant
// (unless one exists), the application, the pointer, the audit row and the ORG_
// customer (when given). A pending application is returned as it is (created
// false); a lost race returns the winner's when it is pending, else
// ErrConcurrentModification, which the service retries.
func (r *ApplicationRepository) Apply(ctx context.Context, in ApplyInput, now time.Time) (*billing.Application, bool, error) {
	if r.tenantZero == "" {
		return nil, false, ErrNoTenantZero
	}
	app := in.Application
	prev, err := r.pointer(ctx, app.AccountOrganizationID)
	if err != nil {
		return nil, false, err
	}
	if prev != nil {
		switch prev.Status {
		case billing.ApplicationPending:
			cur, err := r.Get(ctx, prev.ApplicationID)
			return cur, false, err
		case billing.ApplicationApproved:
			return nil, false, billing.ErrAlreadyMerchant
		}
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	app.Status, app.AppliedAt = billing.ApplicationPending, stamp

	var writes []types.TransactWriteItem
	switch {
	case prev != nil:
		app.TenantID = prev.TenantID // a re-application: the test tenant stays
	case in.ExistingTestTenant != nil:
		app.TenantID = in.ExistingTestTenant.ID
	default:
		app.TenantID = id.NewWithPrefix(id.PrefixOrganization)
		put, err := organizationPut(r.rows, &billing.Organization{
			ID: app.TenantID, DisplayName: app.OrganizationName, Livemode: false,
			PayoutStatus: billing.PayoutNotConfigured, OwnerUserID: app.AppliedBy,
			AccountOrganizationID: app.AccountOrganizationID,
			LegalName:             app.CompanyLegalName, IssuerTaxID: app.CompanyTaxID,
		}, now)
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, put)
	}

	sealed, err := r.seal.Seal(app.CompanyTaxID)
	if err != nil {
		return nil, false, fmt.Errorf("sealing the company document: %w", err)
	}
	row := applicationRow{
		keys:        newKeys(r.pk(), ApplicationSK(app.ID), RetentionOrganization, now),
		Application: *app,
		LookupPK:    LookupApplicationsPK(r.tenantZero, billing.ApplicationPending),
	}
	row.Application.CompanyTaxID = sealed
	item, err := Encode(row)
	if err != nil {
		return nil, false, err
	}
	writes = append(writes, r.rows.BuildPutTxItemIfAbsent(item))

	if prev == nil {
		pointer, err := Encode(applicationOrgRow{
			keys:                  newKeys(r.pk(), ApplicationOrgSK(app.AccountOrganizationID), RetentionOrganization, now),
			AccountOrganizationID: app.AccountOrganizationID, TenantID: app.TenantID,
			ApplicationID: app.ID, Status: billing.ApplicationPending,
		})
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, r.rows.BuildPutTxItemIfAbsent(pointer))
	} else {
		sk := ApplicationOrgSK(app.AccountOrganizationID)
		writes = append(writes, r.rows.BuildRawUpdateTxItem(r.pk(), &sk,
			"SET application_id = :a, #s = :pending, updated_at = :now",
			"application_id = :prev AND #s = :rejected",
			map[string]string{"#s": "status"},
			map[string]types.AttributeValue{
				":a":        &types.AttributeValueMemberS{Value: app.ID},
				":prev":     &types.AttributeValueMemberS{Value: prev.ApplicationID},
				":pending":  &types.AttributeValueMemberS{Value: string(billing.ApplicationPending)},
				":rejected": &types.AttributeValueMemberS{Value: string(billing.ApplicationRejected)},
				":now":      &types.AttributeValueMemberS{Value: stamp},
			}))
	}

	auditItem, err := buildAuditItem(r.tenantZero, true, AuditEntry{
		Entity: EntityApplication, EntityID: app.ID, Action: billing.AuditApplicationSubmitted,
		Cause: billing.CauseManual, Actor: in.Actor, RequestID: in.RequestID, After: app.AccountOrganizationID,
	}, "", "", now)
	if err != nil {
		return nil, false, err
	}
	writes = append(writes, r.audit.BuildPutTxItemIfAbsent(auditItem))

	if in.Customer != nil {
		cw, _, err := r.customers.createWrites(in.Customer, in.Actor, in.RequestID, now)
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, cw...)
	}

	err = r.rows.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		cur, cerr := r.Current(ctx, app.AccountOrganizationID)
		if cerr == nil && cur.Status == billing.ApplicationPending {
			return cur, false, nil
		}
		return nil, false, fmt.Errorf("%w: applying for %s", ErrConcurrentModification, app.AccountOrganizationID)
	}
	if err != nil {
		return nil, false, conflictErr(err)
	}
	return app, true, nil
}

// List pages one status: pending oldest first (a queue), the others newest first.
func (r *ApplicationRepository) List(ctx context.Context, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*Page[billing.Application], error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	res, err := r.rows.Query(ctx, QueryOpts{
		IndexName: IndexLookup, PKField: "lookup_pk", PK: LookupApplicationsPK(r.tenantZero, status),
		SKPrefix: skApplication, ScanIndexForward: status == billing.ApplicationPending,
		Limit: limit, ExclusiveStartKey: start,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[applicationRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := &Page[billing.Application]{Items: make([]billing.Application, 0, len(rows)), LastEvaluatedKey: res.LastEvaluatedKey}
	for i := range rows {
		a, err := r.open(&rows[i])
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *a)
	}
	return out, nil
}

// Lock is a short lease on deciding one application (the LockPlan pattern): a
// crashed decision's lease expires on its own.
func (r *ApplicationRepository) Lock(ctx context.Context, applicationID, token string, now time.Time, lease time.Duration) (func(), error) {
	pk, sk := r.pk(), skApplicationLock+applicationID
	until := now.Add(lease)
	acquire := r.rows.BuildRawUpdateTxItem(pk, &sk,
		"SET lease_until = :until, lease_token = :tok, expires_at = :exp",
		"attribute_not_exists(pk) OR lease_until < :now", nil,
		map[string]types.AttributeValue{
			":until": &types.AttributeValueMemberN{Value: strconv.FormatInt(until.UnixMilli(), 10)},
			":now":   &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UnixMilli(), 10)},
			":tok":   &types.AttributeValueMemberS{Value: token},
			":exp":   &types.AttributeValueMemberN{Value: strconv.FormatInt(until.Add(24*time.Hour).Unix(), 10)},
		})
	err := r.rows.TransactWrite(ctx, txItems(acquire))
	if IsConditionFailed(err) {
		return nil, ErrApplicationBusy
	}
	if err != nil {
		return nil, conflictErr(err)
	}
	return func() {
		release := r.rows.BuildRawUpdateTxItem(pk, &sk, "SET lease_until = :zero", "lease_token = :tok", nil,
			map[string]types.AttributeValue{
				":zero": &types.AttributeValueMemberN{Value: "0"},
				":tok":  &types.AttributeValueMemberS{Value: token},
			})
		_ = r.rows.TransactWrite(context.WithoutCancel(ctx), txItems(release))
	}, nil
}

// Decide moves a pending application to approved or rejected, with the pointer
// and the audit row, conditional on it still being pending.
func (r *ApplicationRepository) Decide(ctx context.Context, app *billing.Application, to billing.ApplicationStatus,
	subscriptionID, reason, actor, requestID string, now time.Time) error {
	if to != billing.ApplicationApproved && to != billing.ApplicationRejected {
		return fmt.Errorf("repositories: %q is not a decision", to)
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	names := map[string]string{"#s": "status"}
	values := map[string]types.AttributeValue{
		":to":      &types.AttributeValueMemberS{Value: string(to)},
		":pending": &types.AttributeValueMemberS{Value: string(billing.ApplicationPending)},
		":lk":      &types.AttributeValueMemberS{Value: LookupApplicationsPK(r.tenantZero, to)},
		":by":      &types.AttributeValueMemberS{Value: actor},
		":at":      &types.AttributeValueMemberS{Value: stamp},
		":id":      &types.AttributeValueMemberS{Value: app.ID},
	}
	expr := "SET #s = :to, lookup_pk = :lk, decided_by = :by, decided_at = :at, updated_at = :at"
	if subscriptionID != "" {
		expr += ", subscription_id = :sub"
		values[":sub"] = &types.AttributeValueMemberS{Value: subscriptionID}
	}
	if reason != "" {
		expr += ", rejection_reason = :why"
		values[":why"] = &types.AttributeValueMemberS{Value: reason}
	}
	sk, psk := ApplicationSK(app.ID), ApplicationOrgSK(app.AccountOrganizationID)
	update := r.rows.BuildRawUpdateTxItem(r.pk(), &sk, expr, "#s = :pending", names, values)
	pointer := r.rows.BuildRawUpdateTxItem(r.pk(), &psk, "SET #s = :to, updated_at = :at", "application_id = :id", names,
		map[string]types.AttributeValue{":to": values[":to"], ":at": values[":at"], ":id": values[":id"]})
	action := billing.AuditApplicationApproved
	if to == billing.ApplicationRejected {
		action = billing.AuditApplicationRejected
	}
	auditItem, err := buildAuditItem(r.tenantZero, true, AuditEntry{
		Entity: EntityApplication, EntityID: app.ID, Action: action, Cause: billing.CauseManual,
		Actor: actor, RequestID: requestID, After: reason,
	}, string(billing.ApplicationPending), string(to), now)
	if err != nil {
		return err
	}
	err = r.rows.TransactWrite(ctx, txItems(update, pointer, r.audit.BuildPutTxItemIfAbsent(auditItem)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", billing.ErrApplicationNotPending, app.ID)
	}
	if err != nil {
		return conflictErr(err)
	}
	app.Status, app.DecidedBy, app.DecidedAt = to, actor, stamp
	if subscriptionID != "" {
		app.SubscriptionID = subscriptionID
	}
	if reason != "" {
		app.RejectionReason = reason
	}
	return nil
}
```

- [ ] **Step 6: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race`
Expected: clean, `ok`.
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Appl|Reapply|Decision|DecisionLease|Customer|OrgCustomer' -v`
Expected: PASS (the customer tests prove the `Create` split changed nothing).
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS`.

- [ ] **Step 7: Commit**

```bash
git add api/internal/repositories api/tests/integration/applications_test.go
git commit -m "feat(applications): one-transaction apply in tenant zero, pointer guard, decisions and listing by status"
```

---
### Task 5: Application e-mails, the notify address, and its SSM parameter

**Files:**
- Modify: `api/internal/email/ses.go` (two messages, two renderers)
- Create: `api/internal/email/ses_test.go`
- Modify: `api/internal/config/config.go` (after `EmailFrom`)
- Modify: `terraform/billing/locals.tf` (`ssm_paths`), `terraform/billing/ssm.tf` (after `email_from`), `terraform/billing/compute.tf` (`bootstrap_vars`), `terraform/assets/bootstrap.sh.tftpl`, `terraform/assets/bootstrap-alpine.sh.tftpl` (the `setup-ssm-env.sh` list)

**Interfaces:**
- Produces:

```go
type ApplicationReceived struct{ ApplicationID, OrganizationName, CompanyLegalName, Description, Website, ContactEmail string }
type ApplicationDecided struct{ OrganizationName string; Approved bool; Reason string }
func (c *Client) SendApplicationReceived(ctx context.Context, to string, n ApplicationReceived) error
func (c *Client) SendApplicationDecided(ctx context.Context, to string, n ApplicationDecided) error
// config.Config.ApplicationsNotifyEmail (env APPLICATIONS_NOTIFY_EMAIL, from SSM /ctech-billing/{env}/billing/applications-notify-email)
```

- [ ] **Step 1: Write the failing tests** — `api/internal/email/ses_test.go`

```go
package email

import (
	"context"
	"strings"
	"testing"
)

type sent struct{ to, subject, body string }

type fakeTransport struct{ out []sent }

func (f *fakeTransport) Send(_ context.Context, to, subject, body string) error {
	f.out = append(f.out, sent{to, subject, body})
	return nil
}

func TestTheReceivedNoticeNamesTheOrganizationAndEscapesWhatTheApplicantTyped(t *testing.T) {
	tx := &fakeTransport{}
	c := &Client{tx: tx}
	err := c.SendApplicationReceived(context.Background(), "time@ctech.example", ApplicationReceived{
		ApplicationID: "apl_1", OrganizationName: "Padaria <b>Bom</b> Dia", CompanyLegalName: "Padaria LTDA",
		Description: "<script>x</script> pão", Website: "https://padaria.example", ContactEmail: "c@padaria.example",
	})
	if err != nil || len(tx.out) != 1 {
		t.Fatalf("%v %+v", err, tx.out)
	}
	m := tx.out[0]
	if m.to != "time@ctech.example" || !strings.Contains(m.subject, "Padaria <b>Bom</b> Dia") {
		t.Fatalf("to/subject = %q / %q", m.to, m.subject)
	}
	if strings.Contains(m.body, "<script>") || strings.Contains(m.body, "<b>Bom</b>") || !strings.Contains(m.body, "apl_1") {
		t.Fatalf("body not escaped or missing the id: %s", m.body)
	}
}

func TestTheDecisionSaysWhichWayAndWhy(t *testing.T) {
	tx := &fakeTransport{}
	c := &Client{tx: tx}
	_ = c.SendApplicationDecided(context.Background(), "c@padaria.example", ApplicationDecided{OrganizationName: "Padaria", Approved: true})
	_ = c.SendApplicationDecided(context.Background(), "c@padaria.example", ApplicationDecided{OrganizationName: "Padaria", Reason: "Site <fora> do ar"})
	if !strings.Contains(tx.out[0].subject, "aprovado") || strings.Contains(tx.out[0].body, "Motivo") {
		t.Fatalf("approval = %+v", tx.out[0])
	}
	if !strings.Contains(tx.out[1].subject, "não foi aprovado") || !strings.Contains(tx.out[1].body, "Site &lt;fora&gt; do ar") {
		t.Fatalf("rejection = %+v", tx.out[1])
	}
}

func TestNoRecipientIsAnError(t *testing.T) {
	c := &Client{tx: &fakeTransport{}}
	if c.SendApplicationDecided(context.Background(), "", ApplicationDecided{}) == nil {
		t.Fatal("sent to nobody")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/email/ -v`
Expected: FAIL — `undefined: ApplicationReceived`.

- [ ] **Step 3: Implement** — append to `api/internal/email/ses.go`:

```go
// ApplicationReceived tells CTech someone asked to bill through CTech Billing
// (spec § 4.1). Every field is what the applicant typed or chose: escaped.
type ApplicationReceived struct {
	ApplicationID, OrganizationName, CompanyLegalName, Description, Website, ContactEmail string
}

// ApplicationDecided tells the applicant the outcome (spec § 4.2).
type ApplicationDecided struct {
	OrganizationName string
	Approved         bool
	Reason           string
}

func (c *Client) SendApplicationReceived(ctx context.Context, to string, n ApplicationReceived) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	subject := "Novo pedido de cobrança: " + n.OrganizationName
	body := `<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">` +
		`<p>` + html.EscapeString(n.OrganizationName) + ` pediu para cobrar os próprios clientes pelo CTech Billing.</p>` +
		`<p><strong>Empresa:</strong> ` + html.EscapeString(n.CompanyLegalName) + `</p>` +
		`<p><strong>O que vende:</strong> ` + html.EscapeString(n.Description) + `</p>` +
		`<p><strong>Site:</strong> ` + html.EscapeString(n.Website) + `</p>` +
		`<p><strong>Contato:</strong> ` + html.EscapeString(n.ContactEmail) + `</p>` +
		`<p style="color:#666;font-size:13px">Pedido ` + html.EscapeString(n.ApplicationID) + `. Decida em Console, Solicitações.</p>` +
		`</body></html>`
	return c.tx.Send(ctx, to, subject, body)
}

func (c *Client) SendApplicationDecided(ctx context.Context, to string, n ApplicationDecided) error {
	if to == "" {
		return fmt.Errorf("email: no recipient")
	}
	name := html.EscapeString(n.OrganizationName)
	subject := "Seu pedido de cobrança foi aprovado: CTech Billing"
	lead := `<p>O modo produção de ` + name + ` está pronto. Abra o console do CTech Billing para conectar suas credenciais.</p>`
	if !n.Approved {
		subject = "Seu pedido de cobrança não foi aprovado: CTech Billing"
		lead = `<p>O pedido de modo produção de ` + name + ` não foi aprovado.</p>` +
			`<p><strong>Motivo:</strong> ` + html.EscapeString(n.Reason) + `</p>` +
			`<p>O modo de teste continua disponível, e você pode pedir de novo pelo console.</p>`
	}
	return c.tx.Send(ctx, to, subject,
		`<!doctype html><html lang="pt-BR"><body style="font-family:system-ui,sans-serif;color:#1a1a1a">`+lead+`</body></html>`)
}
```

(Subjects are plain text, never HTML: the received subject carries the raw name on purpose, the transport sends it as the Subject header.)

`api/internal/config/config.go`, after `EmailFrom`:

```go
	// ApplicationsNotifyEmail receives a message for every merchant application
	// (spec § 4.1). From SSM /ctech-billing/{env}/billing/applications-notify-email.
	// Empty, or EMAIL_FROM empty, means no notice: the application is still
	// recorded and shown in Solicitações, and a missing e-mail never fails it.
	ApplicationsNotifyEmail string `env:"APPLICATIONS_NOTIFY_EMAIL"`
```

Terraform — `terraform/billing/locals.tf`, in `ssm_paths` after `email_from`:

```hcl
    # Where a new merchant application is announced (spec § 4.1). A String:
    # an address, set out of band like email_from.
    applications_notify_email = "${local.ssm_prefix}/applications-notify-email"
```

`terraform/billing/ssm.tf`, after `aws_ssm_parameter.email_from`:

```hcl
# Who at CTech hears about a new merchant application. Set out of band.
resource "aws_ssm_parameter" "applications_notify_email" {
  name  = local.ssm_paths.applications_notify_email
  type  = "String"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}
```

`terraform/billing/compute.tf`, in `bootstrap_vars` after `ssm_email_from`:

```hcl
    ssm_applications_notify_email = local.ssm_paths.applications_notify_email
```

Both bootstrap templates: in the `setup-ssm-env.sh` argument list, change the last line `EMAIL_FROM=${ssm_email_from}` to:

```sh
  EMAIL_FROM=${ssm_email_from} \
  APPLICATIONS_NOTIFY_EMAIL=${ssm_applications_notify_email}
```

- [ ] **Step 4: Run to verify**

Run: `cd api && go test ./internal/email/ ./internal/config/ -race -v`
Expected: PASS.
Run: `cd terraform/billing && terraform fmt -check -recursive .. && terraform init -backend=false -input=false >/dev/null && terraform validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 5: Commit**

```bash
git add api/internal/email api/internal/config terraform
git commit -m "feat(email): application notices; APPLICATIONS_NOTIFY_EMAIL from SSM"
```

---

### Task 6: Catalogue — `prod_billing` and the early access price; tenant zero for tests

**Files:**
- Modify: `api/tenants/ctech.json` (`products`, `prices`)
- Modify: `api/internal/provision/plan_test.go` (`TestTenantZeroPlanLinksCTechsAccountOrganization`)
- Modify: `api/tests/integration/finance_plan_test.go` (`seedCTechCatalogue`, new `seedTenantZero`)
- Modify: `.github/workflows/README.md` ("The pipeline does not seed": the re-seed after this deploy)

**Interfaces:**
- Produces: product `prod_billing` (owner `billing`, no default price); price `price_billing_early_access`; test helper `seedTenantZero(t) (tenant, accountOrg string)` — tenant zero applied in live mode from `tenants/ctech.json` with a fresh id **and** a fresh account organization; `seedCTechCatalogue(t) string` keeps its signature and calls it.

- [ ] **Step 1: Write the failing test** — append to `TestTenantZeroPlanLinksCTechsAccountOrganization`:

```go
	var bill *Product
	for i := range p.Products {
		if p.Products[i].ID == "prod_billing" {
			bill = &p.Products[i]
		}
	}
	// No default price: a default would make every customer of tenant zero
	// "entitled" to billing, which is exactly what approval decides.
	if bill == nil || bill.OwnerKey != billing.MerchantOwnerKey || bill.DefaultPriceID != "" {
		t.Fatalf("prod_billing = %+v", bill)
	}
	ea := prices["price_billing_early_access"]
	if ea.ProductID != "prod_billing" || ea.Type != billing.PriceFixed || ea.UnitAmount != 0 ||
		ea.Timing != billing.BillAdvance || ea.Recurrence.Interval != billing.IntervalMonth || ea.Recurrence.Count != 1 ||
		ea.Metadata["plan"] != "early_access" || ea.Archived {
		t.Fatalf("price_billing_early_access = %+v", ea)
	}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/provision/ -run TestTenantZeroPlan -v`
Expected: FAIL — `prod_billing = <nil>`.

- [ ] **Step 3: Implement** — `api/tenants/ctech.json`, append to `products`:

```json
    {
      "id": "prod_billing",
      "name": "CTech Billing",
      "owner_key": "billing"
    }
```

and to `prices`:

```json
    {
      "id": "price_billing_early_access",
      "product_id": "prod_billing",
      "type": "fixed",
      "unit_amount": 0,
      "recurrence": {"interval": "month", "count": 1},
      "billing_timing": "advance",
      "metadata": {"plan": "early_access"}
    }
```

(match the file's existing indentation.) No public plan list shows it: the Finanças plan reads owner `finance`, ctech-dfe reads owner `dfe`.

`api/tests/integration/finance_plan_test.go` — replace `seedCTechCatalogue` with:

```go
// seedTenantZero applies tenants/ctech.json in live mode under a fresh tenant
// id AND a fresh account organization: since part 1, two tenants may not share
// an ACCOUNT_ORG key, and every test seeds its own tenant zero.
func seedTenantZero(t *testing.T) (tenant, accountOrg string) {
	t.Helper()
	raw, err := os.ReadFile("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	tenant, accountOrg = "org_"+id.New(), newSpaceOrgID()
	doc := strings.Replace(string(raw), `"id": "ctech"`, `"id": "`+tenant+`"`, 1)
	doc = strings.Replace(doc, `"01a04ed6-1af9-745e-bcf7-d3b66fe52321"`, `"`+accountOrg+`"`, 1)
	plan, err := provision.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	// Credentials are global by client id: drop them, a test needs none.
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
	return tenant, accountOrg
}

func seedCTechCatalogue(t *testing.T) string {
	t.Helper()
	tenant, _ := seedTenantZero(t)
	return tenant
}
```

`.github/workflows/README.md`, in "The pipeline does not seed", add after the existing commands:

```markdown
**Re-seed after the applications deploy (console self-service part 2).** The plan
gained `prod_billing` and `price_billing_early_access`; approving an application
fails with `resource_not_found` on the price until it exists:

    api/cmd/seed/run.sh prod   # test then live; idempotent
```

- [ ] **Step 4: Run to verify**

Run: `cd api && go test ./internal/provision/ -race -v` → PASS.
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS` (every finance-plan test now seeds a fresh account organization).

- [ ] **Step 5: Commit**

```bash
git add api/tenants/ctech.json api/internal/provision/plan_test.go api/tests/integration/finance_plan_test.go .github/workflows/README.md
git commit -m "feat(catalogue): prod_billing and the R\$ 0 early access price in tenant zero"
```

---
### Task 7: Suspension — the entitlement hook, in the subscription's own transaction

**Files:**
- Create: `api/internal/repositories/suspension.go`
- Modify: `api/internal/repositories/subscriptions.go` (`SubscriptionRepository.hook`, `WithEntitlementHook`, `Create`, `Transition`)
- Modify: `api/internal/repositories/organizations.go` (`suspensionWrites`)
- Modify: `api/internal/app/app.go` (`newSubscriptions`, every builder uses it)
- Create: `api/internal/app/app_hook_test.go`
- Test: `api/tests/integration/suspension_test.go` (new)

**Interfaces:**
- Consumes: `billing.MerchantOwnerKey`, `(*Subscription).IsEntitled`, `OrganizationOfRef`, `GetByAccountOrganization`, `ListByCustomer`, `commitWithExtraWrites`.
- Produces:

```go
type EntitlementHook interface {
	EntitlementWrites(ctx context.Context, sub *billing.Subscription, entitled bool, cause billing.Cause, actor, requestID string, now time.Time) ([]types.TransactWriteItem, error)
}
func (r *SubscriptionRepository) WithEntitlementHook(h EntitlementHook) *SubscriptionRepository
func NewTenantSuspension(tenantZero string, customers *CustomerRepository, orgs *OrganizationRepository, subs *SubscriptionRepository) *TenantSuspension
func (r *OrganizationRepository) suspensionWrites(org *billing.Organization, suspend bool, cause billing.Cause, actor, requestID string, now time.Time) []types.TransactWriteItem + error
// app: func newSubscriptions(db *dynamodb.Client, cfg *config.Config) *repositories.SubscriptionRepository
```

Rule (spec § 4.3): a **live** subscription in **tenant zero** whose `OwnerKey` is `billing` changes entitlement → find its customer's `ORG_{account_org}` → that organization's **live** tenant → suspend when no other entitled `billing` subscription of the customer remains, reinstate when one becomes entitled. Tenant zero itself is never suspended. Test mode is never touched. The hook fires on `Create` of an entitled subscription and on any `Transition` that flips `IsEntitled()`; `ScheduleCancellation` changes nothing until the sweep cancels at period end.

- [ ] **Step 1: Write the failing integration tests** — `api/tests/integration/suspension_test.go`

```go
//go:build integration

package integration

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// hookedSubs is the subscription repository the way app wires it.
func hookedSubs(tz string) *repositories.SubscriptionRepository {
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	return subs.WithEntitlementHook(repositories.NewTenantSuspension(tz,
		repositories.NewCustomerRepository(testDB, testCfg), repositories.NewOrganizationRepository(testDB, testCfg), subs))
}

type merchantFixture struct {
	tz, acct   string
	live, test *billing.Organization
	customer   *billing.Customer
	subs       *repositories.SubscriptionRepository
	subscriber *services.Subscriber
}

// newMerchantFixture: a seeded tenant zero, and an approved merchant (test and
// live tenants linked to acct) whose ORG_ customer lives in tenant zero.
func newMerchantFixture(t *testing.T) merchantFixture {
	t.Helper()
	tz, _ := seedTenantZero(t)
	acct := newSpaceOrgID()
	live := newLinkedOrg(t, true, acct)
	test := &billing.Organization{ID: live.ID, DisplayName: live.DisplayName, Livemode: false, AccountOrganizationID: acct}
	if err := orgRepo().Create(ctxT(t), test, now()); err != nil {
		t.Fatal(err)
	}
	return withOrgCustomer(t, tz, acct, live, test)
}

func withOrgCustomer(t *testing.T, tz, acct string, live, test *billing.Organization) merchantFixture {
	t.Helper()
	c := orgCustomer(tz, acct)
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), c, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	subs := hookedSubs(tz)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	inv := services.NewInvoicer(subs, repositories.NewInvoiceRepository(testDB, testCfg), cat, repositories.NewUsageRepository(testDB, testCfg))
	return merchantFixture{tz: tz, acct: acct, live: live, test: test, customer: c, subs: subs, subscriber: services.NewSubscriber(subs, cat, inv)}
}

func (f merchantFixture) entitle(t *testing.T) *billing.Subscription {
	t.Helper()
	sub, _, err := f.subscriber.Subscribe(ctxT(t), services.SubscribeInput{
		OrganizationID: f.tz, Livemode: true, CustomerID: f.customer.ID,
		Items: []services.SubscribeItem{{PriceID: "price_billing_early_access"}}, Actor: "user:rev",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionActive {
		t.Fatalf("a R$ 0 advance plan starts ACTIVE, got %s", sub.Status)
	}
	return sub
}

func (f merchantFixture) cancel(t *testing.T, sub *billing.Subscription) {
	t.Helper()
	if err := f.subscriber.Cancel(ctxT(t), sub, false, billing.CauseManual, "user:rev", "req_c", now()); err != nil {
		t.Fatal(err)
	}
}

func suspendedAt(t *testing.T, orgID string, live bool) string {
	t.Helper()
	o, err := orgRepo().Get(ctxT(t), orgID, live)
	if err != nil {
		t.Fatal(err)
	}
	return o.SuspendedAt
}

// Spec § 10.3.
func TestCancellingTheBillingSubscriptionSuspendsTheLiveTenantAndANewOneLiftsIt(t *testing.T) {
	f := newMerchantFixture(t)
	sub := f.entitle(t)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("suspended while entitled")
	}
	f.cancel(t, sub)
	if suspendedAt(t, f.live.ID, true) == "" {
		t.Fatal("cancelling prod_billing did not suspend the live tenant")
	}
	if suspendedAt(t, f.test.ID, false) != "" {
		t.Fatal("test mode was suspended")
	}
	trail, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctxT(t), f.live.ID, true, f.live.ID, 20)
	if err != nil || !hasAction(trail, billing.AuditOrganizationSuspended) {
		t.Fatalf("audit = %+v, %v", trail, err)
	}
	f.entitle(t)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("a new entitled subscription did not lift the suspension")
	}
}

func TestAnotherEntitledBillingSubscriptionKeepsTheTenantActive(t *testing.T) {
	f := newMerchantFixture(t)
	first := f.entitle(t)
	f.entitle(t)
	f.cancel(t, first)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("suspended although another prod_billing subscription is entitled")
	}
}

func TestOtherProductsNeverSuspend(t *testing.T) {
	f := newMerchantFixture(t)
	f.entitle(t)
	sub, _, err := f.subscriber.Subscribe(ctxT(t), services.SubscribeInput{
		OrganizationID: f.tz, Livemode: true, CustomerID: f.customer.ID,
		Items: []services.SubscribeItem{{PriceID: "price_finance_free"}}, Actor: "user:x",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	f.cancel(t, sub)
	if suspendedAt(t, f.live.ID, true) != "" {
		t.Fatal("cancelling a Finanças subscription suspended billing")
	}
}

// Review Focus 2.
func TestTenantZeroIsNeverSuspended(t *testing.T) {
	tz, ctechOrg := seedTenantZero(t)
	zero, err := orgRepo().Get(ctxT(t), tz, true)
	if err != nil {
		t.Fatal(err)
	}
	f := withOrgCustomer(t, tz, ctechOrg, zero, nil)
	f.cancel(t, f.entitle(t))
	if suspendedAt(t, tz, true) != "" {
		t.Fatal("tenant zero was suspended")
	}
}

// A merchant that was never approved has no live tenant: nothing to suspend,
// and the cancellation still commits.
func TestNoLiveTenantIsNothingToSuspend(t *testing.T) {
	tz, _ := seedTenantZero(t)
	f := withOrgCustomer(t, tz, newSpaceOrgID(), nil, nil)
	f.cancel(t, f.entitle(t))
}

func hasAction(trail []billing.AuditLog, action string) bool {
	for _, a := range trail {
		if a.Action == action {
			return true
		}
	}
	return false
}
```

(`orgCustomer` is Task 4's helper. A limit of 20 on `ListForEntity` is enough: the filter is applied after the limit, and the live tenant's partition holds only this test's rows.)

`api/internal/app/app_hook_test.go`:

```go
package app

import (
	"os"
	"strings"
	"testing"
)

// Every binary that moves a subscription must keep the merchant suspension in
// step (spec § 4.3), so app builds the repository in exactly one place.
func TestTheSubscriptionRepositoryIsBuiltOnlyWithTheHook(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "repositories.NewSubscriptionRepository("); n != 1 {
		t.Fatalf("app.go builds the subscription repository %d times; use newSubscriptions", n)
	}
	if !strings.Contains(string(src), "WithEntitlementHook(") {
		t.Fatal("newSubscriptions does not attach the hook")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/app/ -run TheSubscriptionRepository -v` → FAIL (`builds the subscription repository 6 times`).
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Suspend|Entitled|OtherProducts|TenantZeroIsNever|NoLiveTenant' -v` → FAIL (`WithEntitlementHook undefined`).

- [ ] **Step 3: Implement**

`api/internal/repositories/organizations.go`:

```go
// suspensionWrites are the live tenant's suspended_at change and its audit row,
// for the transaction of the subscription change that caused them. The update
// is conditional only on the row existing: a concurrent writer of suspended_at
// must not cancel a subscription's cancellation.
func (r *OrganizationRepository) suspensionWrites(org *billing.Organization, suspend bool, cause billing.Cause,
	actor, requestID string, now time.Time) ([]types.TransactWriteItem, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	expr, action, before, after := "SET suspended_at = if_not_exists(suspended_at, :now), updated_at = :now",
		billing.AuditOrganizationSuspended, "", stamp
	if !suspend {
		expr, action, before, after = "SET updated_at = :now REMOVE suspended_at",
			billing.AuditOrganizationReinstated, org.SuspendedAt, ""
	}
	auditItem, err := buildAuditItem(org.ID, true, AuditEntry{
		Entity: EntityOrganization, EntityID: org.ID, Action: action, Cause: cause,
		Actor: actor, RequestID: requestID, Before: before, After: after,
	}, "", "", now)
	if err != nil {
		return nil, err
	}
	update := r.base.BuildRawUpdateTxItem(TenantPK(org.ID, true), new(OrganizationSK()), expr, "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":now": &types.AttributeValueMemberS{Value: stamp}})
	return txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem)), nil
}
```

`api/internal/repositories/suspension.go`:

```go
package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// EntitlementHook contributes writes to a subscription's own transaction when
// the subscription starts or stops granting service. An error fails the
// subscription change: it never commits without what the hook decided.
type EntitlementHook interface {
	EntitlementWrites(ctx context.Context, sub *billing.Subscription, entitled bool, cause billing.Cause,
		actor, requestID string, now time.Time) ([]types.TransactWriteItem, error)
}

// TenantSuspension suspends a merchant's live tenant when its prod_billing
// entitlement in tenant zero ends, and reinstates it when one is entitled
// again (spec § 4.3, D3). The flag is on the tenant row the request path
// already holds; no entitlement is read per request.
type TenantSuspension struct {
	tenantZero string
	customers  *CustomerRepository
	orgs       *OrganizationRepository
	subs       *SubscriptionRepository
}

func NewTenantSuspension(tenantZero string, customers *CustomerRepository, orgs *OrganizationRepository, subs *SubscriptionRepository) *TenantSuspension {
	return &TenantSuspension{tenantZero: tenantZero, customers: customers, orgs: orgs, subs: subs}
}

func (h *TenantSuspension) EntitlementWrites(ctx context.Context, sub *billing.Subscription, entitled bool,
	cause billing.Cause, actor, requestID string, now time.Time) ([]types.TransactWriteItem, error) {
	if h.tenantZero == "" || sub.OrganizationID != h.tenantZero || !sub.Livemode || sub.OwnerKey != billing.MerchantOwnerKey {
		return nil, nil
	}
	customer, err := h.customers.Get(ctx, h.tenantZero, true, sub.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("suspension: reading the customer of %s: %w", sub.ID, err)
	}
	acct, ok := OrganizationOfRef(customer.ExternalRef)
	if !ok {
		return nil, nil // a prod_billing subscription of a non-organization customer: nothing to suspend
	}
	live, err := h.orgs.GetByAccountOrganization(ctx, acct, true)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil // never approved: no live tenant
	case err != nil:
		return nil, fmt.Errorf("suspension: reading the live tenant of %s: %w", acct, err)
	case live.ID == h.tenantZero:
		return nil, nil // tenant zero is never suspended (Review Focus 2)
	}
	if actor == "" {
		actor = "subscription:" + sub.ID
	}
	if entitled {
		if live.SuspendedAt == "" {
			return nil, nil
		}
		return h.orgs.suspensionWrites(live, false, cause, actor, requestID, now)
	}
	if live.SuspendedAt != "" {
		return nil, nil
	}
	others, err := h.subs.ListByCustomer(ctx, h.tenantZero, true, sub.CustomerID, 100)
	if err != nil {
		return nil, fmt.Errorf("suspension: listing %s's subscriptions: %w", customer.ID, err)
	}
	for i := range others {
		if others[i].ID != sub.ID && others[i].OwnerKey == billing.MerchantOwnerKey && others[i].IsEntitled() {
			return nil, nil
		}
	}
	return h.orgs.suspensionWrites(live, true, cause, actor, requestID, now)
}
```

`api/internal/repositories/subscriptions.go` — add `hook EntitlementHook` to the struct, and:

```go
// WithEntitlementHook attaches the merchant suspension (app wires it in every
// binary). Nil leaves subscriptions unaware of merchants, as in unit tests.
func (r *SubscriptionRepository) WithEntitlementHook(h EntitlementHook) *SubscriptionRepository {
	r.hook = h
	return r
}
```

In `Create`, before `err = r.base.TransactWrite(ctx, writes)`:

```go
	if r.hook != nil && s.IsEntitled() {
		extra, err := r.hook.EntitlementWrites(ctx, s, true, billing.CauseManual, "", "", now)
		if err != nil {
			return err
		}
		writes = append(writes, extra...)
	}
```

In `Transition`, replace `if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {` with:

```go
	var extra []types.TransactWriteItem
	if r.hook != nil && s.IsEntitled() != updated.IsEntitled() {
		if extra, err = r.hook.EntitlementWrites(ctx, &updated, updated.IsEntitled(), cause, actor, requestID, now); err != nil {
			return nil, err
		}
	}
	if err := commitWithExtraWrites(ctx, r.tables(), change, now, extra...); err != nil {
```

`api/internal/app/app.go` — add:

```go
// newSubscriptions is the only constructor of the subscription repository in
// this package: every binary that moves a subscription (the API, the sweep,
// dunning, the reconciler) keeps the merchant suspension in step (spec § 4.3).
// TestTheSubscriptionRepositoryIsBuiltOnlyWithTheHook pins it.
func newSubscriptions(db *dynamodb.Client, cfg *config.Config) *repositories.SubscriptionRepository {
	subs := repositories.NewSubscriptionRepository(db, cfg)
	return subs.WithEntitlementHook(repositories.NewTenantSuspension(cfg.PortalOrganizationID,
		repositories.NewCustomerRepository(db, cfg), repositories.NewOrganizationRepository(db, cfg), subs))
}
```

and replace every other `repositories.NewSubscriptionRepository(db, cfg)` in the file (`Build`, `BuildInvoicer`, `BuildSubscriptions`, `BuildDunner`, `BuildCollector`) with `newSubscriptions(db, cfg)`.

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race` → clean, `ok`.
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories api/internal/app api/tests/integration/suspension_test.go
git commit -m "feat(suspension): prod_billing entitlement suspends and reinstates the live tenant in the same transaction"
```

---

### Task 8: M2M writes of a suspended tenant answer `403 tenant_suspended`

**Files:**
- Create: `api/internal/middleware/suspension.go`, `api/internal/middleware/suspension_test.go`
- Modify: `api/internal/problem/problem.go` (`TypeTenantSuspended`)
- Modify: `api/internal/api/v1/router.go` (the `m2m` group builder)
- Test: `api/tests/integration/suspension_test.go` (append)

**Interfaces:**
- Consumes: `GetTenant`, `OrganizationKey`, `(*billing.Organization).Suspended`.
- Produces: `type TenantRows interface{ Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error) }`; `func RefuseSuspendedWrites(orgs TenantRows) fiber.Handler`; `problem.TypeTenantSuspended = "/problems/tenant-suspended"`.

- [ ] **Step 1: Write the failing unit tests** — `api/internal/middleware/suspension_test.go`

```go
package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

type rows struct {
	org   *billing.Organization
	err   error
	reads int
}

func (r *rows) Get(context.Context, string, bool) (*billing.Organization, error) {
	r.reads++
	return r.org, r.err
}

func suspensionApp(r *rows, live bool) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(TenantKey, Tenant{OrganizationID: "org_m", Livemode: live})
		return c.Next()
	})
	app.All("/x", RefuseSuspendedWrites(r), func(c fiber.Ctx) error { return c.SendString("ran") })
	return app
}

func hit(t *testing.T, app *fiber.App, method string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return resp.StatusCode, b.String()
}

func TestASuspendedLiveTenantCannotWrite(t *testing.T) {
	r := &rows{org: &billing.Organization{ID: "org_m", Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		code, body := hit(t, suspensionApp(r, true), m)
		if code != 403 || !strings.Contains(body, `"tenant_suspended"`) || !strings.Contains(body, "/problems/tenant-suspended") {
			t.Fatalf("%s: %d %s", m, code, body)
		}
	}
}

func TestReadsAndTestModeNeverReadTheTenantRow(t *testing.T) {
	r := &rows{org: &billing.Organization{ID: "org_m", Livemode: true, SuspendedAt: "x"}}
	if code, _ := hit(t, suspensionApp(r, true), "GET"); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if code, _ := hit(t, suspensionApp(r, false), "POST"); code != 200 {
		t.Fatalf("test-mode POST: %d", code)
	}
	if r.reads != 0 {
		t.Fatalf("%d tenant reads, want 0", r.reads)
	}
}

func TestAnActiveTenantWritesAndAReadFailureFailsClosed(t *testing.T) {
	if code, _ := hit(t, suspensionApp(&rows{org: &billing.Organization{ID: "org_m", Livemode: true}}, true), "POST"); code != 200 {
		t.Fatalf("active: %d", code)
	}
	code, body := hit(t, suspensionApp(&rows{err: errors.New("throttled")}, true), "POST")
	if code != 500 || !strings.Contains(body, `"organization_resolve_failed"`) {
		t.Fatalf("read failure: %d %s", code, body)
	}
}
```

(add `"io"` to the imports.)

Append to `api/tests/integration/suspension_test.go` (Review Focus 5):

```go
// setSuspended marks a live tenant suspended directly, as the hook would.
func setSuspended(t *testing.T, org *billing.Organization) {
	t.Helper()
	setLookupAttr(t, org, "suspended_at", "2026-10-10T12:00:00Z")
}

func TestASuspendedTenantReadsAndTestModeKeepWorking(t *testing.T) {
	e := newAPI(t)
	setSuspended(t, e.org)
	live := e.token(t, e.client, "", middleware.ScopeCustomersWrite, middleware.ScopeProductsRead)

	r := e.do(t, http.MethodPost, "/v1.0/customers", live, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`)
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), `"tenant_suspended"`) {
		t.Fatalf("live write: %d %s", r.status, r.body)
	}
	if r := e.do(t, http.MethodGet, "/v1.0/products", live, "", ""); r.status != http.StatusOK {
		t.Fatalf("live read: %d %s", r.status, r.body)
	}

	// The same merchant's test credential keeps writing.
	test := &billing.Organization{ID: e.org.ID, DisplayName: e.org.DisplayName, Livemode: false, PayoutStatus: billing.PayoutEnabled}
	if err := orgRepo().Create(ctxT(t), test, now()); err != nil {
		t.Fatal(err)
	}
	testClient := "cli_" + id.New()
	if err := repositories.NewCredentialRepository(testDB, testCfg).Create(ctxT(t), &billing.APICredential{
		ClientID: testClient, OrganizationID: e.org.ID, Livemode: false, Description: "test", Active: true,
	}, now()); err != nil {
		t.Fatal(err)
	}
	tok := e.token(t, testClient, "", middleware.ScopeCustomersWrite)
	if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana Ribeiro","email":"ana@example.com"}`); r.status >= 400 {
		t.Fatalf("test-mode write: %d %s", r.status, r.body)
	}
}
```

and a small helper next to part 1's `setLookup` in `organization_lookup_test.go`:

```go
// setLookupAttr sets one string attribute on a tenant row, as a hand fix would.
func setLookupAttr(t *testing.T, org *billing.Organization, attr, value string) {
	t.Helper()
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableOrganizations)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, org.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.OrganizationSK()},
		},
		UpdateExpression:          aws.String("SET #a = :v"),
		ExpressionAttributeNames:  map[string]string{"#a": attr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberS{Value: value}},
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

(imports in `suspension_test.go`: `net/http`, `strings`, `middleware`.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/middleware/ -run 'Suspended|ReadsAndTestMode|ActiveTenant' -v` → FAIL (`undefined: RefuseSuspendedWrites`).

- [ ] **Step 3: Implement**

`api/internal/problem/problem.go`, const block: `TypeTenantSuspended = "/problems/tenant-suspended"` with a comment ("a live tenant whose prod_billing entitlement ended: writes refused, reads served (spec § 4.3)").

`api/internal/middleware/suspension.go`:

```go
package middleware

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// TenantRows reads a tenant row (OrganizationRepository satisfies it).
type TenantRows interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

// RefuseSuspendedWrites answers 403 tenant_suspended to a write of a suspended
// live tenant (spec § 4.3). After ResolveTenant. Reads pass untouched and test
// mode is never suspended, so neither reads the tenant row: the cost is one
// GetItem per live write (plan decision 3). A read failure refuses the write.
func RefuseSuspendedWrites(orgs TenantRows) fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		t := GetTenant(c)
		if !t.Livemode {
			return c.Next()
		}
		org, err := orgs.Get(c.Context(), t.OrganizationID, true)
		if err != nil {
			slog.Error("m2m: reading the tenant for the suspension gate failed", "tenant", t.OrganizationID, "error", err)
			return problem.Internal("could not resolve organization").WithCode("organization_resolve_failed").Send(c)
		}
		if org.Suspended() {
			return problem.New(fiber.StatusForbidden, problem.TypeTenantSuspended, "Tenant suspended",
				"this organization's live billing is suspended; reads still work").WithCode("tenant_suspended").Send(c)
		}
		c.Locals(OrganizationKey, org)
		return c.Next()
	}
}
```

`api/internal/api/v1/router.go` — the M2M group builder becomes:

```go
	// A suspended live tenant's writes are refused on every M2M route,
	// including those added later (spec § 4.3).
	suspended := middleware.RefuseSuspendedWrites(d.Organizations)
	m2m := func(prefix string) fiber.Router { return v1.Group(prefix, auth, tenant, suspended) }
```

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race` → clean.
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware api/internal/problem api/internal/api/v1/router.go api/tests/integration
git commit -m "feat(m2m): a suspended live tenant's writes answer 403 tenant_suspended"
```

---
### Task 9: The sweep and dunning skip a suspended tenant

**Files:**
- Create: `api/internal/services/suspension.go`
- Modify: `api/internal/services/invoicing.go` (`SweepResult.Suspended`, `RunDailySweep`)
- Modify: `api/internal/services/dunning.go` (`Dunner.orgs`, `WithOrganizations`, `DunningResult.Suspended`, `Run`)
- Modify: `api/internal/app/app.go` (`BuildDunner` wires the organizations repository)
- Modify: `api/cmd/sweep/main.go`, `api/cmd/dunning/main.go` (log `suspended`)
- Modify: `.github/workflows/README.md` (new section "Lifting a merchant suspension")
- Test: `api/tests/integration/suspension_test.go` (append)

**Interfaces:**
- Consumes: `(*Organization).Suspended`, `OrganizationRepository.Get`.
- Produces: `SweepResult.Suspended int`, `DunningResult.Suspended int`, `func (d *Dunner) WithOrganizations(orgs *repositories.OrganizationRepository) *Dunner`; package-private `newSuspensionCache(orgs *repositories.OrganizationRepository) *suspensionCache`, `(*suspensionCache).skip(ctx, orgID string, livemode bool) (bool, error)`.

A skipped subscription is **not** renewed and a skipped invoice does **not** advance its dunning step, so nothing is lost: re-running the day after the lift does the work (decision 5).

- [ ] **Step 1: Write the failing tests** (append to `api/tests/integration/suspension_test.go`)

```go
func TestTheSweepSkipsASuspendedTenantAndBillsItAfterTheLift(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "dfe") // its own live tenant
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 1000, billing.IntervalMonth)
	anchor := brcal.New(2034, time.March, 7)
	sub, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Anchor: anchor, Actor: "test"}, now())
	if err != nil {
		t.Fatal(err)
	}
	setSuspended(t, f.org)
	inv := services.NewInvoicer(f.subs, f.invoices, f.catalog, f.usage).WithOrganizations(orgRepo())
	day := sub.CurrentPeriod().End
	if res := inv.RunDailySweep(ctx, true, day, "scheduler", now()); res.Suspended < 1 || res.Failed != 0 {
		t.Fatalf("sweep while suspended = %+v", res)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PeriodIndex != sub.PeriodIndex {
		t.Fatal("a suspended tenant's subscription was renewed")
	}
	setLookupAttr(t, f.org, "suspended_at", "") // not REMOVE, but Suspended() reads "" as active
	if res := inv.RunDailySweep(ctx, true, day, "scheduler", now()); res.Failed != 0 {
		t.Fatalf("re-run after the lift = %+v", res)
	}
	if got, _ := f.subs.Get(ctx, f.org.ID, true, sub.ID); got.PeriodIndex != sub.PeriodIndex+1 {
		t.Fatal("re-running the day after the lift did not renew")
	}
}

func TestDunningSkipsASuspendedTenant(t *testing.T) {
	org := newOrg(t, true)
	inv, _, address := dunnableInvoice(t, org)
	setSuspended(t, org)
	box := &mailbox{}
	d := newDunner(t, box).WithOrganizations(orgRepo())
	day, _ := inv.Schedule().DunningDate(inv.DueDate, 0)
	res := d.Run(context.Background(), true, day, now())
	if res.Suspended < 1 || len(box.to(address)) != 0 {
		t.Fatalf("dunning while suspended = %+v, mail %d", res, len(box.to(address)))
	}
	fresh, _ := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), org.ID, true, inv.ID)
	if fresh.DunningStep != inv.DunningStep {
		t.Fatal("a skipped invoice advanced its dunning step")
	}
}
```

(imports: `context`, `time`, `brcal`.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'SweepSkips|DunningSkips' -v`
Expected: FAIL — `res.Suspended undefined`, `WithOrganizations undefined` on `*Dunner`.

- [ ] **Step 3: Implement**

`api/internal/services/suspension.go`:

```go
package services

import (
	"context"
	"errors"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type tenantReader interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

// suspensionCache answers "is this tenant suspended" once per tenant per run
// (spec § 4.3: scheduled jobs skip a suspended live tenant). Test mode is never
// suspended and is never read.
type suspensionCache struct {
	orgs tenantReader
	seen map[string]bool
}

func newSuspensionCache(orgs *repositories.OrganizationRepository) *suspensionCache {
	c := &suspensionCache{seen: map[string]bool{}}
	if orgs != nil { // a nil pointer must not become a non-nil interface
		c.orgs = orgs
	}
	return c
}

func (c *suspensionCache) skip(ctx context.Context, organizationID string, livemode bool) (bool, error) {
	if !livemode || c.orgs == nil {
		return false, nil
	}
	if v, ok := c.seen[organizationID]; ok {
		return v, nil
	}
	org, err := c.orgs.Get(ctx, organizationID, true)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		c.seen[organizationID] = false
		return false, nil
	case err != nil:
		return false, err
	}
	c.seen[organizationID] = org.Suspended()
	return c.seen[organizationID], nil
}
```

`api/internal/services/invoicing.go` — add `Suspended int` to `SweepResult` (doc: "subscriptions of a suspended live tenant, left for a re-run after the lift"); in `RunDailySweep`, create `suspended := newSuspensionCache(s.orgs)` before the loop and, at the top of the per-item body after `result.Examined++`:

```go
			if skip, err := suspended.skip(ctx, sub.OrganizationID, sub.Livemode); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Errorf("subscription %s: reading its tenant: %w", sub.ID, err))
				continue
			} else if skip {
				result.Suspended++
				continue
			}
```

`api/internal/services/dunning.go` — add `orgs *repositories.OrganizationRepository` to `Dunner`, `Suspended int` to `DunningResult`, and:

```go
// WithOrganizations lets the run skip a suspended live tenant (spec § 4.3).
func (d *Dunner) WithOrganizations(orgs *repositories.OrganizationRepository) *Dunner {
	d.orgs = orgs
	return d
}
```

In `Run`, before the loop `suspended := newSuspensionCache(d.orgs)`, and inside it before `d.step`:

```go
		if skip, err := suspended.skip(ctx, inv.OrganizationID, inv.Livemode); err != nil {
			res.fail("invoice %s: reading its tenant: %v", inv.ID, err)
			continue
		} else if skip {
			res.Suspended++
			continue
		}
```

`api/internal/app/app.go`, `BuildDunner` — `return services.NewDunner(...).WithOrganizations(repositories.NewOrganizationRepository(db, cfg)), nil`.

`cmd/sweep/main.go` and `cmd/dunning/main.go` — add `"suspended", res.Suspended` to the finished log line.

`.github/workflows/README.md` — append:

```markdown
## Lifting a merchant suspension

A live tenant whose `prod_billing` subscription in tenant zero is cancelled is
suspended: its M2M writes answer `403 tenant_suspended`, and `sweep` and
`dunning` skip it (spec § 4.3). Both jobs read one day's queue, so the days
skipped stay where they were. When a new entitled subscription lifts the
suspension, re-run each suspended day, oldest first; both are idempotent:

    # on an instance, as the job user, once per day D of the suspension
    /opt/app/job.sh sweep -date D
    /opt/app/job.sh dunning -date D

`reconcile` and `deliver` are never skipped: they record money that arrived and
tell integrations about it.
```

(Use the job invocation the existing README sections use if it differs from `/opt/app/job.sh`; check "Running a job by hand" in the same file.)

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race` → clean.
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services api/internal/app api/cmd .github/workflows/README.md api/tests/integration/suspension_test.go
git commit -m "feat(jobs): the sweep and dunning skip a suspended live tenant"
```

---

### Task 10: `services.Applications` — apply, approve, reject, notify

**Files:**
- Create: `api/internal/services/applications.go`
- Test: `api/tests/integration/applications_test.go` (append)

**Interfaces:**
- Consumes: Task 3 `accountclient.Company`, `ErrCompanyNotFound`; Task 4 `ApplicationRepository`; Task 5 `email.ApplicationReceived/Decided`; Task 7 hooked `SubscriptionRepository`; `Subscriber.Subscribe`; `OrganizationRepository.Create/Get/GetByAccountOrganization`; `CustomerRepository.GetByOrganization`.
- Produces:

```go
const EarlyAccessPriceID = "price_billing_early_access"
var ErrCompanyNotInOrganization, ErrCompaniesUnavailable error
type CompanyDirectory interface {
	Companies(ctx context.Context, organizationID string) ([]accountclient.Company, error)
	Company(ctx context.Context, organizationID, companyID string) (*accountclient.Company, error)
}
type ApplicationMailer interface {
	SendApplicationReceived(ctx context.Context, to string, n email.ApplicationReceived) error
	SendApplicationDecided(ctx context.Context, to string, n email.ApplicationDecided) error
}
type ApplyInput struct {
	AccountOrganizationID, OrganizationName, UserID, CompanyID, Description, Website string
	Volume                                                                       billing.MonthlyVolume
	ContactEmail, RequestID                                                      string
}
func NewApplications(apps *repositories.ApplicationRepository, orgs *repositories.OrganizationRepository,
	customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	subscriber *Subscriber, companies CompanyDirectory, tenantZero string) *Applications
func (s *Applications) WithMail(m ApplicationMailer, notifyTo string) *Applications
func (s *Applications) Enabled() bool
func (s *Applications) Companies(ctx, accountOrgID string) ([]accountclient.Company, error) // ErrCompaniesUnavailable
func (s *Applications) Apply(ctx, in ApplyInput, now time.Time) (*billing.Application, bool, error)
func (s *Applications) Current(ctx, accountOrgID string) (*billing.Application, error)
func (s *Applications) LatestStatus(ctx, accountOrgID string) (billing.ApplicationStatus, error)
func (s *Applications) List(ctx, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*repositories.Page[billing.Application], error)
func (s *Applications) Approve(ctx, applicationID, actor, requestID string, now time.Time) (*billing.Application, error)
func (s *Applications) Reject(ctx, applicationID, reason, actor, requestID string, now time.Time) (*billing.Application, error)
```

- [ ] **Step 1: Write the failing tests** (append to `api/tests/integration/applications_test.go`)

```go
type fakeCompanies map[string][]accountclient.Company // org -> companies

func (f fakeCompanies) Companies(_ context.Context, org string) ([]accountclient.Company, error) {
	return f[org], nil
}

func (f fakeCompanies) Company(_ context.Context, org, id string) (*accountclient.Company, error) {
	for _, c := range f[org] {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, accountclient.ErrCompanyNotFound
}

type outbox struct {
	mu       sync.Mutex
	received []string
	decided  []email.ApplicationDecided
}

func (o *outbox) SendApplicationReceived(_ context.Context, to string, _ email.ApplicationReceived) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.received = append(o.received, to)
	return nil
}

func (o *outbox) SendApplicationDecided(_ context.Context, _ string, n email.ApplicationDecided) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.decided = append(o.decided, n)
	return nil
}

type applicationsFixture struct {
	tz, acct string
	svc      *services.Applications
	subs     *repositories.SubscriptionRepository
	mail     *outbox
}

func newApplicationsFixture(t *testing.T) applicationsFixture {
	t.Helper()
	tz, _ := seedTenantZero(t)
	acct := newSpaceOrgID()
	subs := hookedSubs(tz)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	inv := services.NewInvoicer(subs, repositories.NewInvoiceRepository(testDB, testCfg), cat, repositories.NewUsageRepository(testDB, testCfg))
	box := &outbox{}
	svc := services.NewApplications(appRepo(t, tz), orgRepo(), repositories.NewCustomerRepository(testDB, testCfg), subs,
		services.NewSubscriber(subs, cat, inv),
		fakeCompanies{acct: {{ID: "cmp_1", LegalName: "Padaria Bom Dia LTDA", TaxID: "12345678000190", TaxIDKind: "cnpj"}}}, tz).
		WithMail(box, "time@ctech.example")
	return applicationsFixture{tz: tz, acct: acct, svc: svc, subs: subs, mail: box}
}

func (f applicationsFixture) apply(t *testing.T) *billing.Application {
	t.Helper()
	app, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{
		AccountOrganizationID: f.acct, OrganizationName: "Padaria Bom Dia", UserID: "usr_a", CompanyID: "cmp_1",
		Description: "Assinaturas de pão para empresas", ContactEmail: "c@padaria.example", RequestID: "req_1",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestApplyNeedsOneOfTheOrganizationsCompanies(t *testing.T) {
	f := newApplicationsFixture(t)
	_, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{AccountOrganizationID: f.acct, OrganizationName: "P",
		UserID: "u", CompanyID: "cmp_someone_elses", Description: "x", ContactEmail: "c@x.example"}, now())
	if !errors.Is(err, services.ErrCompanyNotInOrganization) {
		t.Fatalf("err = %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), f.acct, false); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatal("a refused application created a tenant")
	}
}

func TestApplyNotifiesCTechOnceAndCreatesTheCustomerOnce(t *testing.T) {
	f := newApplicationsFixture(t)
	first := f.apply(t)
	again := f.apply(t)
	if again.ID != first.ID || len(f.mail.received) != 1 || f.mail.received[0] != "time@ctech.example" {
		t.Fatalf("again=%s first=%s notices=%v", again.ID, first.ID, f.mail.received)
	}
}

// Spec § 10.2: after approval the live tenant exists once; the subscription is
// prod_billing at R$ 0 in tenant zero, live.
func TestApprovingSubscribesTheOrganizationAndCreatesTheLiveTenantOnce(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	got, err := f.svc.Approve(ctx, app.ID, "user:rev", "req_2", now())
	if err != nil || got.Status != billing.ApplicationApproved || got.SubscriptionID == "" {
		t.Fatalf("approve = %+v, %v", got, err)
	}
	again, err := f.svc.Approve(ctx, app.ID, "user:rev", "req_3", now())
	if err != nil || again.SubscriptionID != got.SubscriptionID || again.DecidedAt != got.DecidedAt {
		t.Fatalf("second approval changed something: %+v, %v", again, err)
	}
	live, err := orgRepo().GetByAccountOrganization(ctx, f.acct, true)
	if err != nil || live.ID != app.TenantID || live.PayoutStatus != billing.PayoutNotConfigured || live.LegalName != "Padaria Bom Dia LTDA" {
		t.Fatalf("live = %+v, %v", live, err)
	}
	sub, err := f.subs.Get(ctx, f.tz, true, got.SubscriptionID)
	if err != nil || sub.OwnerKey != billing.MerchantOwnerKey || sub.Status != billing.SubscriptionActive {
		t.Fatalf("subscription = %+v, %v", sub, err)
	}
	if len(f.mail.decided) != 1 || !f.mail.decided[0].Approved {
		t.Fatalf("decision mail = %+v", f.mail.decided)
	}
}

// Review Focus 3.
func TestConcurrentApprovalsMakeOneSubscriptionAndOneLiveTenant(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); err != nil && !errors.Is(err, repositories.ErrApplicationBusy) {
				t.Errorf("approve: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); err != nil {
		t.Fatal(err)
	}
	customer, _ := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctx, f.tz, true, f.acct)
	subs, _ := f.subs.ListByCustomer(ctx, f.tz, true, customer.ID, 100)
	n := 0
	for _, s := range subs {
		if s.OwnerKey == billing.MerchantOwnerKey {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d prod_billing subscriptions, want 1", n)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, f.acct, true); err != nil {
		t.Fatalf("one live tenant, readable without ambiguity: %v", err)
	}
}

func TestRejectingKeepsTheTestTenantAndAllowsANewApplication(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	got, err := f.svc.Reject(ctx, app.ID, "Site fora do ar", "user:rev", "req", now())
	if err != nil || got.Status != billing.ApplicationRejected {
		t.Fatalf("reject = %+v, %v", got, err)
	}
	if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); !errors.Is(err, billing.ErrApplicationNotPending) {
		t.Fatalf("approving a rejected application: %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, f.acct, false); err != nil {
		t.Fatalf("test tenant after rejection: %v", err)
	}
	if again := f.apply(t); again.ID == app.ID || again.Status != billing.ApplicationPending {
		t.Fatalf("re-application = %+v", again)
	}
	if len(f.mail.decided) != 1 || f.mail.decided[0].Approved || f.mail.decided[0].Reason != "Site fora do ar" {
		t.Fatalf("decision mail = %+v", f.mail.decided)
	}
}

// Review Focus 1.
func TestAProvisionedTestTenantIsReusedNotDuplicated(t *testing.T) {
	f := newApplicationsFixture(t)
	existing := newLinkedOrg(t, false, f.acct)
	if app := f.apply(t); app.TenantID != existing.ID {
		t.Fatalf("tenant = %s, want the provisioned %s", app.TenantID, existing.ID)
	}
}

func TestAnOrganizationAlreadyLiveCannotApply(t *testing.T) {
	f := newApplicationsFixture(t)
	newLinkedOrg(t, true, f.acct)
	_, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{AccountOrganizationID: f.acct, OrganizationName: "P",
		UserID: "u", CompanyID: "cmp_1", Description: "x", ContactEmail: "c@x.example"}, now())
	if !errors.Is(err, billing.ErrAlreadyMerchant) {
		t.Fatalf("err = %v", err)
	}
}
```

(imports: `context`, `accountclient`, `email`, `services`.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Apply|Approv|Reject|Provisioned|AlreadyLive' -v`
Expected: FAIL — `undefined: services.NewApplications`.

- [ ] **Step 3: Implement** — `api/internal/services/applications.go`

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// EarlyAccessPriceID is what approval subscribes an organization to (spec § 4.4).
const EarlyAccessPriceID = "price_billing_early_access"

var (
	// ErrCompanyNotInOrganization: the company_id is not one of the
	// organization's companies (a 422 on the field, never a hint of whose it is).
	ErrCompanyNotInOrganization = errors.New("company is not one of the organization's")
	// ErrCompaniesUnavailable: ctech-account could not answer about companies.
	ErrCompaniesUnavailable = errors.New("companies are unavailable")
)

type CompanyDirectory interface {
	Companies(ctx context.Context, organizationID string) ([]accountclient.Company, error)
	Company(ctx context.Context, organizationID, companyID string) (*accountclient.Company, error)
}

type ApplicationMailer interface {
	SendApplicationReceived(ctx context.Context, to string, n email.ApplicationReceived) error
	SendApplicationDecided(ctx context.Context, to string, n email.ApplicationDecided) error
}

type ApplyInput struct {
	AccountOrganizationID, OrganizationName, UserID, CompanyID, Description, Website string
	Volume                                                                       billing.MonthlyVolume
	ContactEmail, RequestID                                                      string
}

// Applications is the merchant application lifecycle (spec § 4).
type Applications struct {
	apps       *repositories.ApplicationRepository
	orgs       *repositories.OrganizationRepository
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	subscriber *Subscriber
	companies  CompanyDirectory
	mail       ApplicationMailer
	notifyTo   string
	tenantZero string
}

func NewApplications(apps *repositories.ApplicationRepository, orgs *repositories.OrganizationRepository,
	customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	subscriber *Subscriber, companies CompanyDirectory, tenantZero string) *Applications {
	return &Applications{apps: apps, orgs: orgs, customers: customers, subs: subs, subscriber: subscriber,
		companies: companies, tenantZero: tenantZero}
}

// WithMail turns on the notices. A nil mailer (no EMAIL_FROM) leaves them off.
func (s *Applications) WithMail(m ApplicationMailer, notifyTo string) *Applications {
	s.mail, s.notifyTo = m, notifyTo
	return s
}

// Enabled is false without tenant zero: no route of this lifecycle is mounted.
func (s *Applications) Enabled() bool { return s != nil && s.tenantZero != "" }

func (s *Applications) Companies(ctx context.Context, accountOrgID string) ([]accountclient.Company, error) {
	list, err := s.companies.Companies(ctx, accountOrgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompaniesUnavailable, err)
	}
	return list, nil
}

func (s *Applications) Current(ctx context.Context, acct string) (*billing.Application, error) {
	return s.apps.Current(ctx, acct)
}

func (s *Applications) LatestStatus(ctx context.Context, acct string) (billing.ApplicationStatus, error) {
	return s.apps.LatestStatus(ctx, acct)
}

func (s *Applications) List(ctx context.Context, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*repositories.Page[billing.Application], error) {
	return s.apps.List(ctx, status, limit, start)
}

// Apply validates the company with ctech-account, then writes everything in one
// transaction (ApplicationRepository.Apply). A lost race is retried: the second
// attempt finds the winner's pending application and returns it.
func (s *Applications) Apply(ctx context.Context, in ApplyInput, now time.Time) (*billing.Application, bool, error) {
	company, err := s.companies.Company(ctx, in.AccountOrganizationID, in.CompanyID)
	switch {
	case errors.Is(err, accountclient.ErrCompanyNotFound):
		return nil, false, ErrCompanyNotInOrganization
	case err != nil:
		return nil, false, fmt.Errorf("%w: %w", ErrCompaniesUnavailable, err)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		app, created, err := s.applyOnce(ctx, in, company, now)
		if errors.Is(err, repositories.ErrTransactionConflict) || errors.Is(err, repositories.ErrConcurrentModification) {
			lastErr = err
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if created {
			s.notifyReceived(ctx, app)
		}
		return app, created, nil
	}
	return nil, false, lastErr
}

func (s *Applications) applyOnce(ctx context.Context, in ApplyInput, company *accountclient.Company, now time.Time) (*billing.Application, bool, error) {
	if _, err := s.orgs.GetByAccountOrganization(ctx, in.AccountOrganizationID, true); err == nil {
		return nil, false, billing.ErrAlreadyMerchant
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, false, err
	}
	var existing *billing.Organization
	test, err := s.orgs.GetByAccountOrganization(ctx, in.AccountOrganizationID, false)
	switch {
	case err == nil:
		existing = test
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, false, err
	}
	var customer *billing.Customer
	if _, err := s.customers.GetByOrganization(ctx, s.tenantZero, true, in.AccountOrganizationID); errors.Is(err, repositories.ErrNotFound) {
		customer = &billing.Customer{
			ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: s.tenantZero, Livemode: true,
			ExternalRef: repositories.OrgRefPrefix + in.AccountOrganizationID,
			Name: in.OrganizationName, Email: in.ContactEmail, TaxID: company.TaxID,
		}
	} else if err != nil {
		return nil, false, err
	}
	return s.apps.Apply(ctx, repositories.ApplyInput{
		Application: &billing.Application{
			ID: id.NewWithPrefix(id.PrefixApplication), AccountOrganizationID: in.AccountOrganizationID,
			OrganizationName: in.OrganizationName, CompanyID: company.ID, CompanyLegalName: company.LegalName,
			CompanyTaxID: company.TaxID, Description: in.Description, Website: in.Website,
			ExpectedMonthlyVolume: in.Volume, ContactEmail: in.ContactEmail, AppliedBy: in.UserID,
		},
		ExistingTestTenant: existing, Customer: customer,
		Actor: "user:" + in.UserID, RequestID: in.RequestID,
	}, now)
}

const decisionLease = 30 * time.Second

// Approve is idempotent (spec § 4.2): it reuses an entitled prod_billing
// subscription and an existing live tenant, so a retry after a crash finishes
// the job, and an approved application is answered as it is.
func (s *Applications) Approve(ctx context.Context, applicationID, actor, requestID string, now time.Time) (*billing.Application, error) {
	release, err := s.apps.Lock(ctx, applicationID, id.New(), now, decisionLease)
	if err != nil {
		return nil, err
	}
	defer release()
	app, err := s.apps.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	switch app.Status {
	case billing.ApplicationApproved:
		return app, nil
	case billing.ApplicationRejected:
		return nil, billing.ErrApplicationNotPending
	}
	customer, err := s.customers.GetByOrganization(ctx, s.tenantZero, true, app.AccountOrganizationID)
	if err != nil {
		return nil, fmt.Errorf("the application's ORG_ customer: %w", err)
	}
	sub, err := s.entitlement(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		sub, _, err = s.subscriber.Subscribe(ctx, SubscribeInput{
			OrganizationID: s.tenantZero, Livemode: true, CustomerID: customer.ID,
			Items:    []SubscribeItem{{PriceID: EarlyAccessPriceID}},
			Metadata: billing.Metadata{"application_id": app.ID}, Actor: actor,
		}, now)
		if sub == nil {
			return nil, err
		}
		if err != nil {
			// The subscription exists and entitles; only its R$ 0 invoice failed,
			// and its generation key makes the sweep produce it exactly once.
			slog.Warn("applications: the early access invoice was not issued", "subscription", sub.ID, "error", err)
		}
	}
	if err := s.ensureLiveTenant(ctx, app, now); err != nil {
		return nil, err
	}
	if err := s.apps.Decide(ctx, app, billing.ApplicationApproved, sub.ID, "", actor, requestID, now); err != nil {
		return nil, err
	}
	s.notifyDecided(ctx, app)
	return app, nil
}

// entitlement is the customer's entitled prod_billing subscription, if any.
func (s *Applications) entitlement(ctx context.Context, customerID string) (*billing.Subscription, error) {
	subs, err := s.subs.ListByCustomer(ctx, s.tenantZero, true, customerID, 100)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].OwnerKey == billing.MerchantOwnerKey && subs[i].IsEntitled() {
			return &subs[i], nil
		}
	}
	return nil, nil
}

// ensureLiveTenant creates the live tenant with the test tenant's id, name and
// issuer block, unless it exists.
func (s *Applications) ensureLiveTenant(ctx context.Context, app *billing.Application, now time.Time) error {
	if _, err := s.orgs.Get(ctx, app.TenantID, true); err == nil || !errors.Is(err, repositories.ErrNotFound) {
		return err
	}
	test, err := s.orgs.Get(ctx, app.TenantID, false)
	if err != nil {
		return fmt.Errorf("the application's test tenant: %w", err)
	}
	return s.orgs.Create(ctx, &billing.Organization{
		ID: app.TenantID, DisplayName: test.DisplayName, Livemode: true,
		PayoutStatus: billing.PayoutNotConfigured, OwnerUserID: test.OwnerUserID,
		AccountOrganizationID: app.AccountOrganizationID, LegalName: test.LegalName, IssuerTaxID: test.IssuerTaxID,
	}, now)
}

// Reject is idempotent on a rejected application; an approved one is refused.
func (s *Applications) Reject(ctx context.Context, applicationID, reason, actor, requestID string, now time.Time) (*billing.Application, error) {
	app, err := s.apps.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	switch app.Status {
	case billing.ApplicationRejected:
		return app, nil
	case billing.ApplicationApproved:
		return nil, billing.ErrApplicationNotPending
	}
	if err := s.apps.Decide(ctx, app, billing.ApplicationRejected, "", reason, actor, requestID, now); err != nil {
		return nil, err
	}
	s.notifyDecided(ctx, app)
	return app, nil
}

// The notices are best effort (spec § 8): logged, retried by nobody.
func (s *Applications) notifyReceived(ctx context.Context, app *billing.Application) {
	if s.mail == nil || s.notifyTo == "" {
		return
	}
	if err := s.mail.SendApplicationReceived(context.WithoutCancel(ctx), s.notifyTo, email.ApplicationReceived{
		ApplicationID: app.ID, OrganizationName: app.OrganizationName, CompanyLegalName: app.CompanyLegalName,
		Description: app.Description, Website: app.Website, ContactEmail: app.ContactEmail,
	}); err != nil {
		slog.Error("applications: the notice to CTech was not sent", "application", app.ID, "error", err)
	}
}

func (s *Applications) notifyDecided(ctx context.Context, app *billing.Application) {
	if s.mail == nil {
		return
	}
	if err := s.mail.SendApplicationDecided(context.WithoutCancel(ctx), app.ContactEmail, email.ApplicationDecided{
		OrganizationName: app.OrganizationName, Approved: app.Status == billing.ApplicationApproved, Reason: app.RejectionReason,
	}); err != nil {
		slog.Error("applications: the decision e-mail was not sent", "application", app.ID, "error", err)
	}
}
```

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race` → clean.
Run: `cd api && make test-integration 2>&1 | tail -5` → `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/applications.go api/tests/integration/applications_test.go
git commit -m "feat(applications): apply with company check, idempotent approval as prod_billing entitlement, rejection, notices"
```

---
### Task 11: Console routes — applying, reviewing, and the session's statuses

**Files:**
- Create: `api/internal/api/v1/console_applications.go`, `api/internal/api/v1/console_applications_dto.go`
- Create: `api/internal/middleware/tenantzero.go`
- Modify: `api/internal/problem/problem.go` (`TypeCompaniesUnavailable`; mappings)
- Modify: `api/internal/api/v1/console.go` (`consoleHandlers.applications`; `merchantStatus`; `session`)
- Modify: `api/internal/api/v1/console_dto.go` (`merchantOrganizationDTO.CanReviewApplications`)
- Modify: `api/internal/api/v1/router.go` (`Deps.Applications`; `registerConsole`)
- Modify: `api/internal/app/app.go` (`Build`: companies client, mailer, `services.NewApplications`)
- Modify: `api/internal/api/v1/console_session_test.go` (part 1's: wire shape; new reviewer test)
- Modify: `api/tests/integration/console_access_test.go` (`billingHalfRoutes` exclusion)
- Create: `api/tests/integration/console_applications_test.go`
- Modify: `.github/workflows/README.md` (prerequisites table; deploy sequence "Console self-service part 2")

**Interfaces:**
- Consumes: Tasks 1–10; part 1's chain and session.
- Produces (wire):

| Route | Chain | Scope | Answers |
|---|---|---|---|
| `GET /v1.0/console/merchant/application` | `auth, RequireUserSession, ResolveSpace, RequireMerchantRole` | `billing:organization:read` | `200 applicationResponse`, `404 no_application` |
| `POST /v1.0/console/merchant/application` | same | `billing:organization:write` | `201` new, `200` the pending one, `409 already_merchant`, `422`, `503 companies_unavailable`/`space_unavailable` |
| `GET /v1.0/console/merchant/companies` | same | `billing:organization:read` | `200 {"data":[{company_id, legal_name, trade_name, tax_id}]}`, `503 companies_unavailable` |
| `GET /v1.0/console/applications?status=&cursor=` | console group + `RequireTenantZero` | `billing:organization:read` | `200 {"data":[applicationResponse], "has_more", "cursor"}`; other tenant `404 resource_not_found` |
| `POST /v1.0/console/applications/:id/approve` | same | `billing:organization:write` | `200 applicationResponse`, `409 application_not_pending`/`application_busy` |
| `POST /v1.0/console/applications/:id/reject` `{reason}` | same | `billing:organization:write` | `200`, `409 application_not_pending`, `422` |

```json
{"id":"apl_…","status":"pending","organization_id":"0190…","organization_name":"Padaria Bom Dia",
 "company":{"id":"cmp_1","legal_name":"Padaria Bom Dia LTDA","tax_id":"12345678000190"},
 "description":"…","website":"https://…","expected_monthly_volume":"up_to_10k","contact_email":"c@…",
 "applied_by":"usr_…","applied_at":"2026-10-10T12:00:00Z","decided_at":"…","rejection_reason":"…"}
```

Session entries gain `"can_review_applications": bool` (true for the organization whose tenant is tenant zero). `merchant_status` now yields `rejected` and `suspended`.

- [ ] **Step 1: Write the failing unit tests** — in `api/internal/api/v1/console_session_test.go` (part 1's), change `TestMerchantOrganizationWireShape`'s `want` to end the organization object with `,"merchant_status":"live","can_review_applications":false}` and add:

```go
func TestOnlyTenantZerosOrganizationReviewsApplications(t *testing.T) {
	l := &fakeLister{orgs: map[string][]accountorgs.Organization{"alice": {
		{ID: acctA, DisplayName: "CTech", Role: "admin", Kind: "organization"},
		{ID: acctB, DisplayName: "Padaria", Role: "owner", Kind: "organization"},
	}}}
	m := fakeMerchants{acctA + "|live": {ID: "ctech", Livemode: true}, acctB + "|live": {ID: "org_b", Livemode: true}}
	_, raw, _ := getSession(t, sessionApp(&consoleHandlers{spaces: l, merchants: m, portalOrganizationID: "ctech"}, sessionClaims))
	var b struct {
		Organizations []struct {
			DisplayName string `json:"display_name"`
			Reviewer    bool   `json:"can_review_applications"`
		} `json:"organizations"`
	}
	_ = json.Unmarshal([]byte(raw), &b)
	if len(b.Organizations) != 2 || !b.Organizations[0].Reviewer || b.Organizations[1].Reviewer {
		t.Fatalf("organizations = %+v", b.Organizations)
	}
}
```

Create `api/tests/integration/console_applications_test.go`:

```go
//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// merchantEnv is the whole service with a seeded tenant zero, CTech's
// organization (a reviewer is its admin) and an applicant organization with
// one company.
type merchantEnv struct {
	*apiEnv
	tz, ctechOrg, acct, reviewer, applicant string
}

func newMerchantEnv(t *testing.T) merchantEnv {
	t.Helper()
	tz, ctechOrg := seedTenantZero(t)
	e := newAPIWith(t, func(c *config.Config) { c.PortalOrganizationID = tz })
	m := merchantEnv{apiEnv: e, tz: tz, ctechOrg: ctechOrg, acct: newSpaceOrgID(),
		reviewer: "usr_" + id.New(), applicant: "usr_" + id.New()}
	e.account.name(ctechOrg, "CTech")
	e.account.set(ctechOrg, m.reviewer, "organization", "admin")
	e.account.name(m.acct, "Padaria Bom Dia")
	e.account.set(m.acct, m.applicant, "organization", "owner")
	e.account.company(m.acct, accountclient.Company{ID: "cmp_1", LegalName: "Padaria Bom Dia LTDA", TaxID: "12345678000190", TaxIDKind: "cnpj"})
	return m
}

var merchantScopes = append(append([]string{}, allConsoleScopes...), middleware.ScopeOrganizationWrite)

func (m merchantEnv) as(t *testing.T, sub, method, path, mode, org, body string) apiResponse {
	t.Helper()
	return m.consoleAs(t, method, path, m.token(t, sub, "sess_"+id.New(), merchantScopes...), mode, "org:"+org, body)
}

const applyBody = `{"company_id":"cmp_1","description":"Assinaturas de pão para empresas do bairro","website":"https://padaria.example","expected_monthly_volume":"up_to_10k","contact_email":"c@padaria.example"}`

func (m merchantEnv) apply(t *testing.T) string {
	t.Helper()
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody)
	if r.status != http.StatusCreated {
		t.Fatalf("apply: %d %s", r.status, r.body)
	}
	var b struct{ ID string `json:"id"` }
	r.decode(t, &b)
	return b.ID
}

func statusIn(t *testing.T, m merchantEnv, sub, acct string) string {
	t.Helper()
	r := m.consoleAs(t, http.MethodGet, "/v1.0/console/session", m.token(t, sub, "sess_"+id.New(), merchantScopes...), "", "", "")
	var b struct {
		Organizations []struct {
			ID     string `json:"organization_id"`
			Status string `json:"merchant_status"`
		} `json:"organizations"`
	}
	r.decode(t, &b)
	for _, o := range b.Organizations {
		if o.ID == acct {
			return o.Status
		}
	}
	return "absent"
}

func TestTheApplicantGetsTestModeAtOnceAndLiveAfterApproval(t *testing.T) {
	m := newMerchantEnv(t)
	if s := statusIn(t, m, m.applicant, m.acct); s != "none" {
		t.Fatalf("before = %s", s)
	}
	appID := m.apply(t)
	if r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody); r.status != http.StatusOK ||
		!bytes.Contains(r.body, []byte(appID)) {
		t.Fatalf("second apply: %d %s", r.status, r.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "test" {
		t.Fatalf("after applying = %s", s)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "test", m.acct, ""); r.status != http.StatusOK {
		t.Fatalf("test mode: %d %s", r.status, r.body)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "live", m.acct, ""); r.status != http.StatusNotFound ||
		!bytes.Contains(r.body, []byte(`"no_merchant"`)) {
		t.Fatalf("live before approval: %d %s", r.status, r.body)
	}

	list := m.as(t, m.reviewer, http.MethodGet, "/v1.0/console/applications?status=pending", "live", m.ctechOrg, "")
	if list.status != http.StatusOK || !bytes.Contains(list.body, []byte(appID)) || !bytes.Contains(list.body, []byte("Padaria Bom Dia LTDA")) {
		t.Fatalf("pending list: %d %s", list.status, list.body)
	}
	first := m.as(t, m.reviewer, http.MethodPost, "/v1.0/console/applications/"+appID+"/approve", "live", m.ctechOrg, "")
	second := m.as(t, m.reviewer, http.MethodPost, "/v1.0/console/applications/"+appID+"/approve", "live", m.ctechOrg, "")
	if first.status != http.StatusOK || !bytes.Equal(first.body, second.body) {
		t.Fatalf("approvals: %d %s / %d %s", first.status, first.body, second.status, second.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "live" {
		t.Fatalf("after approval = %s", s)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "live", m.acct, ""); r.status != http.StatusOK {
		t.Fatalf("live after approval: %d %s", r.status, r.body)
	}
}

func TestARejectionCarriesItsReasonAndAllowsApplyingAgain(t *testing.T) {
	m := newMerchantEnv(t)
	appID := m.apply(t)
	path := "/v1.0/console/applications/" + appID + "/reject"
	if r := m.as(t, m.reviewer, http.MethodPost, path, "live", m.ctechOrg, `{"reason":"curto"}`); r.status != http.StatusUnprocessableEntity ||
		!bytes.Contains(r.body, []byte(`"too_short"`)) {
		t.Fatalf("short reason: %d %s", r.status, r.body)
	}
	if r := m.as(t, m.reviewer, http.MethodPost, path, "live", m.ctechOrg, `{"reason":"O site não abre; envie de novo com o endereço certo."}`); r.status != http.StatusOK {
		t.Fatalf("reject: %d %s", r.status, r.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "rejected" {
		t.Fatalf("status = %s", s)
	}
	r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/merchant/application", "test", m.acct, "")
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte("O site não abre")) {
		t.Fatalf("applicant view: %d %s", r.status, r.body)
	}
	if again := m.apply(t); again == appID {
		t.Fatal("re-application returned the rejected one")
	}
}

// Spec § 10.1, tenant-zero half: a review route from any other tenant is 404.
func TestReviewRoutesAreNotFoundFromAnyOtherTenant(t *testing.T) {
	m := newMerchantEnv(t)
	appID := m.apply(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1.0/console/applications?status=pending"},
		{http.MethodPost, "/v1.0/console/applications/" + appID + "/approve"},
		{http.MethodPost, "/v1.0/console/applications/" + appID + "/reject"},
	} {
		r := m.as(t, m.applicant, c.method, c.path, "test", m.acct, `{"reason":"tentando aprovar a mim mesmo"}`)
		if r.status != http.StatusNotFound || !bytes.Contains(r.body, []byte(`"resource_not_found"`)) || bytes.Contains(r.body, []byte(appID)) {
			t.Fatalf("%s %s from the applicant's tenant: %d %s", c.method, c.path, r.status, r.body)
		}
	}
}

func TestMerchantRoutesRefuseEveryoneButOwnersAndAdmins(t *testing.T) {
	m := newMerchantEnv(t)
	member, viewer := "usr_"+id.New(), "usr_"+id.New()
	m.account.set(m.acct, member, "organization", "member")
	m.account.set(m.acct, viewer, "organization", "viewer")
	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1.0/console/merchant/application"},
		{http.MethodPost, "/v1.0/console/merchant/application"},
		{http.MethodGet, "/v1.0/console/merchant/companies"},
	}
	for _, r := range routes {
		for _, sub := range []string{member, viewer} {
			if got := m.as(t, sub, r.method, r.path, "test", m.acct, applyBody); got.status != http.StatusForbidden ||
				!bytes.Contains(got.body, []byte(`"merchant_role_required"`)) {
				t.Errorf("%s %s as %s: %d %s", r.method, r.path, sub, got.status, got.body)
			}
		}
		tok := m.token(t, m.applicant, "sess_"+id.New(), merchantScopes...)
		if got := m.consoleAs(t, r.method, r.path, tok, "test", "personal", applyBody); got.status != http.StatusNotFound {
			t.Errorf("%s %s personal: %d", r.method, r.path, got.status)
		}
		if got := m.consoleAs(t, r.method, r.path, m.token(t, m.client, "", merchantScopes...), "test", "org:"+m.acct, applyBody); got.status != http.StatusForbidden {
			t.Errorf("%s %s m2m: %d", r.method, r.path, got.status)
		}
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), m.acct, false); err == nil {
		t.Fatal("a refused request created a tenant")
	}
}

func TestApplyValidatesEveryFieldAtOnce(t *testing.T) {
	m := newMerchantEnv(t)
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct,
		`{"description":"curta","website":"http://x.example","expected_monthly_volume":"muito","contact_email":"nope"}`)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var b struct {
		Errors []struct{ Field, Code string } `json:"errors"`
	}
	_ = json.Unmarshal(r.body, &b)
	got := map[string]string{}
	for _, e := range b.Errors {
		got[e.Field] = e.Code
	}
	want := map[string]string{"company_id": "required", "description": "too_short", "website": "invalid_url",
		"expected_monthly_volume": "unsupported_value", "contact_email": "invalid_email"}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	if r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct,
		`{"company_id":"cmp_not_ours","description":"Assinaturas de pão para empresas do bairro","contact_email":"c@x.example"}`); r.status != http.StatusUnprocessableEntity ||
		!bytes.Contains(r.body, []byte(`"not_found"`)) {
		t.Fatalf("someone else's company: %d %s", r.status, r.body)
	}
}

// Review Focus 4.
func TestApplyingWhileCtechAccountIsDownWritesNothing(t *testing.T) {
	m := newMerchantEnv(t)
	m.account.mu.Lock()
	m.account.companiesDown = true
	m.account.mu.Unlock()
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody)
	if r.status != http.StatusServiceUnavailable || !bytes.Contains(r.body, []byte(`"companies_unavailable"`)) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), m.acct, false); err == nil {
		t.Fatal("a tenant was created while ctech-account was down")
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctxT(t), m.tz, true, m.acct); err == nil {
		t.Fatal("a customer was created while ctech-account was down")
	}
}

func TestApplyingNeedsTheWriteScope(t *testing.T) {
	m := newMerchantEnv(t)
	tok := m.token(t, m.applicant, "sess_"+id.New(), allConsoleScopes...)
	if r := m.consoleAs(t, http.MethodPost, "/v1.0/console/merchant/application", tok, "test", "org:"+m.acct, applyBody); r.status != http.StatusForbidden {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
```

In part 1's `api/tests/integration/console_access_test.go`, `billingHalfRoutes` skips the merchant routes too (they run without a tenant; `TestMerchantRoutesRefuseEveryoneButOwnersAndAdmins` covers them):

```go
		if !strings.HasPrefix(r.Path, "/v1.0/console/") ||
			strings.HasPrefix(r.Path, "/v1.0/console/finance") ||
			strings.HasPrefix(r.Path, "/v1.0/console/merchant/") ||
			r.Path == "/v1.0/console/session" {
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/api/v1/ -run 'Session|MerchantOrganization|Reviews' -v` → FAIL (`can_review_applications` absent; `portalOrganizationID` unused in the session).
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Applicant|ARejection|ReviewRoutes|MerchantRoutes|ApplyValidates|CtechAccountIsDown|WriteScope' -v` → FAIL (404 on every new route).

- [ ] **Step 3: Implement**

`api/internal/problem/problem.go` — const block:

```go
	// TypeCompaniesUnavailable is ctech-account not answering about an
	// organization's companies: the application form cannot be filled now.
	TypeCompaniesUnavailable = "/problems/companies-unavailable"
```

and in `FromError`, before the generic fallback:

```go
	case errors.Is(err, billing.ErrApplicationNotPending):
		return Conflict("the application was already decided").WithCode("application_not_pending")
	case errors.Is(err, billing.ErrAlreadyMerchant):
		return Conflict("this organization already bills in live mode").WithCode("already_merchant")
	case errors.Is(err, repositories.ErrApplicationBusy):
		return Conflict("another decision on this application is in progress").WithCode("application_busy")
```

`api/internal/middleware/tenantzero.go`:

```go
package middleware

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// RequireTenantZero admits only requests whose resolved tenant is tenant zero
// (spec § 4.2): the application review. Any other tenant gets the plain
// not-found body, so the route does not say it exists. After the merchant chain.
func RequireTenantZero(tenantZero string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if tenantZero == "" || GetTenant(c).OrganizationID != tenantZero {
			return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
		}
		return c.Next()
	}
}
```

`api/internal/api/v1/console_applications_dto.go`:

```go
package v1

import "gopkg.aoctech.app/billing/api/internal/domain/billing"

type applicationCompany struct {
	ID        string `json:"id"`
	LegalName string `json:"legal_name"`
	TaxID     string `json:"tax_id"`
}

// applicationResponse is one application on both screens: the applicant's own
// and tenant zero's review. Both readers are entitled to the company document.
type applicationResponse struct {
	ID                    string                    `json:"id"`
	Status                billing.ApplicationStatus `json:"status"`
	OrganizationID        string                    `json:"organization_id"`
	OrganizationName      string                    `json:"organization_name"`
	Company               applicationCompany        `json:"company"`
	Description           string                    `json:"description"`
	Website               string                    `json:"website,omitempty"`
	ExpectedMonthlyVolume billing.MonthlyVolume     `json:"expected_monthly_volume,omitempty"`
	ContactEmail          string                    `json:"contact_email"`
	AppliedBy             string                    `json:"applied_by"`
	AppliedAt             string                    `json:"applied_at"`
	DecidedAt             string                    `json:"decided_at,omitempty"`
	RejectionReason       string                    `json:"rejection_reason,omitempty"`
}

func newApplicationResponse(a *billing.Application) applicationResponse {
	return applicationResponse{
		ID: a.ID, Status: a.Status, OrganizationID: a.AccountOrganizationID, OrganizationName: a.OrganizationName,
		Company:     applicationCompany{ID: a.CompanyID, LegalName: a.CompanyLegalName, TaxID: a.CompanyTaxID},
		Description: a.Description, Website: a.Website, ExpectedMonthlyVolume: a.ExpectedMonthlyVolume,
		ContactEmail: a.ContactEmail, AppliedBy: a.AppliedBy, AppliedAt: a.AppliedAt,
		DecidedAt: a.DecidedAt, RejectionReason: a.RejectionReason,
	}
}

type companyResponse struct {
	ID        string `json:"company_id"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name,omitempty"`
	TaxID     string `json:"tax_id"`
}

type applyRequest struct {
	CompanyID             string `json:"company_id"`
	Description           string `json:"description"`
	Website               string `json:"website"`
	ExpectedMonthlyVolume string `json:"expected_monthly_volume"`
	ContactEmail          string `json:"contact_email"`
}

type rejectRequest struct {
	Reason string `json:"reason"`
}
```

`api/internal/api/v1/console_applications.go`:

```go
package v1

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// The merchant application (spec § 4) on the console. The applicant's three
// routes run without a tenant (RequireMerchantRole only); the review routes run
// behind the merchant chain and RequireTenantZero.

func applicationFail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrCompanyNotInOrganization):
		return problem.Validation([]problem.FieldError{fieldErr("company_id", "not_found", "not one of the organization's companies")}).Send(c)
	case errors.Is(err, services.ErrCompaniesUnavailable):
		return problem.New(fiber.StatusServiceUnavailable, problem.TypeCompaniesUnavailable, "Companies unavailable",
			"could not read the organization's companies right now").WithCode("companies_unavailable").Send(c)
	}
	return fail(c, err)
}

func (h *consoleHandlers) merchantCompanies(c fiber.Ctx) error {
	list, err := h.applications.Companies(c.Context(), middleware.GetMerchantAccess(c).AccountOrganizationID)
	if err != nil {
		return applicationFail(c, err)
	}
	out := make([]companyResponse, 0, len(list))
	for _, co := range list {
		out = append(out, companyResponse{ID: co.ID, LegalName: co.LegalName, TradeName: co.TradeName, TaxID: co.TaxID})
	}
	return c.JSON(listResponse[companyResponse]{Data: out})
}

func (h *consoleHandlers) getMerchantApplication(c fiber.Ctx) error {
	app, err := h.applications.Current(c.Context(), middleware.GetMerchantAccess(c).AccountOrganizationID)
	if errors.Is(err, repositories.ErrNotFound) {
		return problem.NotFound("this organization has not applied").WithCode("no_application").Send(c)
	}
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}

// httpsURL: an absolute https URL with a host and no credentials.
func httpsURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func (h *consoleHandlers) applyMerchant(c fiber.Ctx) error {
	var req applyRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	req.Description, req.Website, req.ContactEmail = strings.TrimSpace(req.Description), strings.TrimSpace(req.Website), strings.TrimSpace(req.ContactEmail)
	var ch checks
	ch.id("company_id", req.CompanyID, true)
	ch.text("description", req.Description, true, limits.ApplicationDescription)
	if n := utf8.RuneCountInString(req.Description); n > 0 && n < limits.ApplicationDescriptionMin {
		ch.fail("description", "too_short", "at least 20 characters", "min", limits.ApplicationDescriptionMin)
	}
	if req.Website != "" {
		ch.text("website", req.Website, false, limits.Website)
		if !httpsURL(req.Website) {
			ch.fail("website", "invalid_url", "an https address")
		}
	}
	if !billing.MonthlyVolume(req.ExpectedMonthlyVolume).Valid() {
		ch.fail("expected_monthly_volume", "unsupported_value", "not one of the listed ranges")
	}
	ch.email("contact_email", req.ContactEmail, true)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}

	acc := middleware.GetMerchantAccess(c)
	sub := middleware.GetClaims(c).Sub
	name, err := h.organizationName(c, sub, acc.AccountOrganizationID)
	if err != nil {
		return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable,
			"Space unavailable", "could not verify access right now").Send(c)
	}
	app, created, err := h.applications.Apply(c.Context(), services.ApplyInput{
		AccountOrganizationID: acc.AccountOrganizationID, OrganizationName: name, UserID: sub,
		CompanyID: req.CompanyID, Description: req.Description, Website: req.Website,
		Volume: billing.MonthlyVolume(req.ExpectedMonthlyVolume), ContactEmail: req.ContactEmail,
		RequestID: middleware.GetRequestID(c),
	}, h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(newApplicationResponse(app))
}

// organizationName is the account organization's display name, from the same
// list the session reads (the space resolver carries no names).
func (h *consoleHandlers) organizationName(c fiber.Ctx, sub, accountOrgID string) (string, error) {
	if h.spaces == nil {
		return "", errors.New("no organization lister")
	}
	ws, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		return "", err
	}
	for _, w := range ws {
		if w.ID == accountOrgID {
			return w.DisplayName, nil
		}
	}
	return "", errors.New("organization not in the person's list")
}

func (h *consoleHandlers) listApplications(c fiber.Ctx) error {
	status := billing.ApplicationStatus(c.Query("status", string(billing.ApplicationPending)))
	if !status.Valid() {
		return problem.BadRequest("status must be pending, approved or rejected").WithCode("invalid_status").Send(c)
	}
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("invalid cursor").WithCode("invalid_cursor").Send(c)
	}
	page, err := h.applications.List(c.Context(), status, pageLimit, start)
	if err != nil {
		return fail(c, err)
	}
	out := make([]applicationResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newApplicationResponse(&page.Items[i]))
	}
	return c.JSON(pageOf(out, page.LastEvaluatedKey))
}

func (h *consoleHandlers) approveApplication(c fiber.Ctx) error {
	app, err := h.applications.Approve(c.Context(), c.Params("id"), actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}

func (h *consoleHandlers) rejectApplication(c fiber.Ctx) error {
	var req rejectRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	req.Reason = strings.TrimSpace(req.Reason)
	var ch checks
	ch.text("reason", req.Reason, true, limits.Reason)
	if n := utf8.RuneCountInString(req.Reason); n > 0 && n < limits.RejectionReasonMin {
		ch.fail("reason", "too_short", "at least 10 characters", "min", limits.RejectionReasonMin)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	app, err := h.applications.Reject(c.Context(), c.Params("id"), req.Reason, actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}
```

(`pageOf`, `pageLimit`, `fieldErr`, `checks` exist in package `v1`; if `pageOf`'s element type is generic over another response, it is `pageOf[T]` already.)

`api/internal/api/v1/console.go` — add `applications *services.Applications` to `consoleHandlers`; replace part 1's `merchantStatus` with:

```go
// merchantStatus is where an account organization stands as a merchant, from
// its tenant rows in both modes and its latest application; the bool reports
// that its tenant is tenant zero (it reviews applications).
func (h *consoleHandlers) merchantStatus(ctx context.Context, sub, accountOrgID string) (billing.MerchantStatus, bool, error) {
	var rows [2]*billing.Organization // test, live
	for i, live := range []bool{false, true} {
		org, err := middleware.LookupMerchantTenant(ctx, h.merchants, sub, accountOrgID, live)
		switch {
		case err == nil:
			rows[i] = org
		case !errors.Is(err, repositories.ErrNotFound):
			return "", false, err
		}
	}
	var latest billing.ApplicationStatus
	if h.applications.Enabled() {
		st, err := h.applications.LatestStatus(ctx, accountOrgID)
		if err != nil {
			return "", false, err
		}
		latest = st
	}
	zero := false
	for _, r := range rows {
		zero = zero || (r != nil && h.portalOrganizationID != "" && r.ID == h.portalOrganizationID)
	}
	return billing.MerchantStatusOf(rows[0], rows[1], latest), zero, nil
}
```

and in `session`, `status, err := h.merchantStatus(...)` becomes `status, reviewer, err := h.merchantStatus(...)`, with `CanReviewApplications: reviewer` in the DTO literal.

`api/internal/api/v1/console_dto.go` — `merchantOrganizationDTO` gains, after `MerchantStatus`:

```go
	// CanReviewApplications marks the organization whose tenant is tenant zero:
	// the console shows it Solicitações. Display data; RequireTenantZero decides.
	CanReviewApplications bool `json:"can_review_applications"`
```

`api/internal/api/v1/router.go` — `Deps` gains:

```go
	// Applications is the merchant application lifecycle (spec § 4). Disabled
	// (routes not mounted) without tenant zero.
	Applications *services.Applications
```

In `registerConsole`, add `applications: d.Applications,` to the `consoleHandlers` literal, and after the `/console/session` route (before the group):

```go
	// The application runs without a tenant: an organization with none is
	// exactly who applies. Registered before the group for the reason the
	// session is (a group's handlers run as prefix middleware). The mode header
	// is required by ResolveSpace and ignored here (plan decision 6).
	if ch.applications.Enabled() {
		v1.Get("/console/merchant/application", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.getMerchantApplication)
		v1.Post("/console/merchant/application", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.applyMerchant)
		v1.Get("/console/merchant/companies", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.merchantCompanies)
	}
```

and at the end of `registerConsole`, inside the group:

```go
	// Tenant zero's review (spec § 4.2). Any other tenant: 404.
	if ch.applications.Enabled() {
		review := console.Group("/applications", middleware.RequireTenantZero(d.PortalOrganizationID))
		review.Get("", middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.listApplications)
		review.Post("/:id/approve", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.approveApplication)
		review.Post("/:id/reject", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.rejectApplication)
	}
```

`api/internal/app/app.go`, in `Build` (after `account := accountorgs.New(...)`):

```go
	// ctech-account companies (the application form): the same credential as
	// membership, its own scope. Typed nil when unconfigured: every call errors,
	// read as companies_unavailable.
	companies := accountclient.New(accountclient.Config{
		BaseURL: cfg.AccountBaseURL, TokenURL: cfg.AccountTokenURL,
		ClientID: cfg.AccountClientID, ClientSecret: cfg.AccountClientSecret, Cache: cacheBackend,
	})
	applications := services.NewApplications(repositories.NewApplicationRepository(db, cfg), orgs, customers, subs,
		subscriber, companies, cfg.PortalOrganizationID)
	if cfg.EmailFrom != "" {
		if mail, err := email.New(ctx, cfg.AWSRegion, cfg.EmailFrom); err != nil {
			slog.Warn("application notices are off: SES client", "error", err)
		} else {
			applications = applications.WithMail(mail, cfg.ApplicationsNotifyEmail)
		}
	}
```

and `Applications: applications,` in `v1.Deps`. (`subs` here is Task 7's `newSubscriptions(db, cfg)`.)

`.github/workflows/README.md` — add to the prerequisites table:

```markdown
| `/ctech-billing/{env}/billing/applications-notify-email` (String) | set out of band | the notice of a new merchant application |
| billing's account client granted `internal:account:company-actor`; ctech-account route `GET /v1.0/internal/organizations/{id}/companies` | ctech-account (`createclient -grant`; route: console self-service part 2, decision 1) | the application form's company picker and check |
| OAuth client `billing` granted `billing:organization:write` | ctech-account | **sign-in itself** once the UI asks for it |
```

and a section:

```markdown
## Deploy sequence: console self-service part 2 (applications)

1. ctech-account: the companies list route, and `internal:account:company-actor`
   granted to billing's account client (`createclient -grant`), in this env.
2. Terraform (the `applications-notify-email` parameter and the bootstrap
   variable), then set the parameter, then an instance refresh so the units
   read it.
3. Scopes job: publishes `billing:organization:write`. Then grant it to the
   OAuth client `billing` at ctech-account. **Before step 6**: the UI asks for
   it, and ctech-account fails the whole sign-in on an ungranted scope.
4. API.
5. `api/cmd/seed/run.sh prod` (adds `prod_billing` and the early access price).
6. UI.
7. Smoke: apply from an organization you administer, see test mode, approve it
   in tenant zero (Solicitações), see live; reject a second one and re-apply.
```

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race` → clean, `ok`.
Run: `cd api && make test-integration 2>&1 | tail -20` → `PASS`, including part 1's `TestNoNonMerchantReachesAnyBillingHalfRoute` (now also over the three review routes: their refusals come from the merchant chain before `RequireTenantZero`).

- [ ] **Step 5: Commit**

```bash
git add api .github/workflows/README.md
git commit -m "feat(console): apply for CTech Billing, review in tenant zero, and rejected/suspended in the session"
```

---
### Task 12: UI — data layer, selection context, scope, copy and mock

> Execute with the `/impeccable` skill loaded (it owns the copy rules). No screen yet; every string is UI text: pt-BR and en, no em dash.

**Files:**
- Modify: `ui/src/lib/api/consoleTypes.ts`, `ui/src/lib/api/console.ts`, `ui/src/lib/api/console.test.ts` (part 1's)
- Create: `ui/src/lib/console/applications.ts`, `ui/src/lib/console/applications.test.ts`
- Create: `ui/src/lib/console/selection.tsx`
- Modify: `ui/src/lib/console/nav.ts`, `ui/src/lib/console/nav.test.ts`
- Modify: `ui/src/lib/auth/scopes.ts`, `ui/src/lib/auth/scopes.test.ts`
- Modify: `ui/src/locales/pt-BR/errors.json`, `ui/src/locales/en/errors.json`, `ui/src/lib/errors.test.ts`
- Modify: `ui/src/locales/pt-BR/console.json`, `ui/src/locales/en/console.json` (all part 2 keys, used by Tasks 13–14)
- Modify: `ui/src/dev/consoleMockData.ts`, `ui/src/dev/mockRuntime.ts`, `ui/src/lib/mockConfig.ts`

**Interfaces:**
- Consumes: part 1's `ConsoleScope`, `scopeHeaders`, `consoleKeys`, `MerchantOrganization`, `problemCode`.
- Produces:

```ts
// consoleTypes.ts
export type ApplicationStatus = "pending" | "approved" | "rejected"
export type MonthlyVolume = "up_to_10k" | "10k_to_50k" | "50k_to_200k" | "over_200k"
export interface MerchantCompany {company_id: string; legal_name: string; trade_name?: string; tax_id: string}
export interface MerchantApplication {
  id: string; status: ApplicationStatus; organization_id: string; organization_name: string
  company: {id: string; legal_name: string; tax_id: string}
  description: string; website?: string; expected_monthly_volume?: MonthlyVolume; contact_email: string
  applied_by: string; applied_at: string; decided_at?: string; rejection_reason?: string
}
export interface ApplyRequest {company_id: string; description: string; website?: string; expected_monthly_volume?: MonthlyVolume; contact_email: string}
// MerchantOrganization gains: can_review_applications?: boolean
// console.ts
consoleKeys.merchantApplication(s) = ["console", s.org, "merchant", "application"]
consoleKeys.merchantCompanies(s)   = ["console", s.org, "merchant", "companies"]
consoleKeys.applications(s, status) = ["console", s.org, s.mode, "applications", status]
getMerchantApplication(scope): Promise<MerchantApplication | null>   // no_application -> null
applyForMerchant(scope, body: ApplyRequest): Promise<MerchantApplication>
listMerchantCompanies(scope): Promise<MerchantCompany[]>
listApplications(scope, status, cursor?): Promise<ConsolePage<MerchantApplication>>
approveApplication(scope, id): Promise<MerchantApplication>
rejectApplication(scope, id, reason): Promise<MerchantApplication>
// applications.ts
MONTHLY_VOLUMES: MonthlyVolume[]
type ApplyFormValues = {company_id: string; description: string; website: string; expected_monthly_volume: MonthlyVolume | ""; contact_email: string}
applyFieldErrors(v: ApplyFormValues): FieldError[]       // same codes as the API
toApplyRequest(v: ApplyFormValues): ApplyRequest
rejectReasonErrors(reason: string): FieldError[]
isHttpsURL(v: string): boolean
// selection.tsx
type MerchantSelection = {scope: ConsoleScope; organization: MerchantOrganization}
MerchantSelectionProvider({value, children}); useMerchantSelection(): MerchantSelection
// nav.ts
consoleNav(hasOrganization: boolean, reviewer?: boolean)
```

The merchant keys carry **no mode**: an application is not a mode's (spec § 4.1), so switching modes keeps them; switching organizations removes them with the rest of `["console", org]` (part 1's `OrganizationSwitch`).

- [ ] **Step 1: Write the failing tests**

`ui/src/lib/console/applications.test.ts`:

```ts
import {describe, expect, it} from "vitest"

import {applyFieldErrors, isHttpsURL, MONTHLY_VOLUMES, rejectReasonErrors, toApplyRequest} from "@/lib/console/applications"

const ok = {
  company_id: "cmp_1", description: "Assinaturas de pão para empresas do bairro", website: "",
  expected_monthly_volume: "" as const, contact_email: "c@padaria.example",
}
const codes = (errs: {field: string; code: string}[]) => Object.fromEntries(errs.map(e => [e.field, e.code]))

describe("applyFieldErrors", () => {
  it("accepts a complete form with the optional fields empty", () => {
    expect(applyFieldErrors(ok)).toEqual([])
  })
  it("names every problem at once, with the API's codes", () => {
    expect(codes(applyFieldErrors({company_id: "", description: "curta", website: "http://x.example", expected_monthly_volume: "", contact_email: "nope"})))
      .toEqual({company_id: "required", description: "too_short", website: "invalid_url", contact_email: "invalid_email"})
  })
  it("counts characters, not bytes, and trims before counting", () => {
    expect(applyFieldErrors({...ok, description: "  " + "ç".repeat(20) + "   "})).toEqual([])
    expect(codes(applyFieldErrors({...ok, description: "x".repeat(1001)}))).toEqual({description: "too_long"})
  })
})

describe("toApplyRequest", () => {
  it("trims and leaves out empty optional fields", () => {
    expect(toApplyRequest({...ok, description: "  " + ok.description + " ", website: " "})).toEqual({
      company_id: "cmp_1", description: ok.description, contact_email: "c@padaria.example",
    })
  })
})

describe("isHttpsURL", () => {
  it.each([
    ["https://padaria.example", true], ["https://padaria.example/loja?x=1", true],
    ["http://padaria.example", false], ["padaria.example", false], ["https://", false], ["https://u:p@x.example", false],
  ])("%s is %s", (v, want) => expect(isHttpsURL(v)).toBe(want))
})

describe("rejectReasonErrors", () => {
  it("wants 10 to 500 characters", () => {
    expect(codes(rejectReasonErrors("curto"))).toEqual({reason: "too_short"})
    expect(codes(rejectReasonErrors(""))).toEqual({reason: "required"})
    expect(rejectReasonErrors("O site não abre.")).toEqual([])
  })
})

it("lists the four volumes in order", () => {
  expect(MONTHLY_VOLUMES).toEqual(["up_to_10k", "10k_to_50k", "50k_to_200k", "over_200k"])
})
```

Append to part 1's `ui/src/lib/api/console.test.ts`:

```ts
describe("merchant applications", () => {
  it("send the organization, and the application keys carry no mode", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {data: []}})
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({data: {id: "apl_1"}})
    await listMerchantCompanies(SCOPE)
    await applyForMerchant(SCOPE, {company_id: "cmp_1", description: "x".repeat(20), contact_email: "c@x.example"})
    await approveApplication(SCOPE, "apl_1")
    await rejectApplication(SCOPE, "apl_1", "motivo bem claro")
    for (const c of [...get.mock.calls, ...post.mock.calls]) {
      expect((c[c.length - 1] as {headers: Record<string, string>}).headers).toEqual(expect.objectContaining({"X-Billing-Space": `org:${SCOPE.org}`}))
    }
    expect(post).toHaveBeenCalledWith("/v1.0/console/applications/apl_1/reject", {reason: "motivo bem claro"}, expect.anything())
    expect(consoleKeys.merchantApplication(SCOPE)).toEqual(["console", SCOPE.org, "merchant", "application"])
    expect(consoleKeys.merchantApplication({...SCOPE, mode: "live"})).toEqual(consoleKeys.merchantApplication(SCOPE))
    expect(consoleKeys.applications(SCOPE, "pending")).toEqual(["console", SCOPE.org, "test", "applications", "pending"])
  })

  it("reads 'never applied' as null, not as a failure", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue({response: {status: 404, data: {code: "no_application"}}})
    await expect(getMerchantApplication(SCOPE)).resolves.toBeNull()
  })
})
```

(extend the import from `@/lib/api/console` with `applyForMerchant, approveApplication, getMerchantApplication, listMerchantCompanies, rejectApplication`.)

`ui/src/lib/console/nav.test.ts` — add:

```ts
it("adds Solicitações only for tenant zero's organization", () => {
  expect(consoleNav(true, true).map(i => i.href)).toContain("/console/applications")
  expect(consoleNav(true, false).map(i => i.href)).not.toContain("/console/applications")
  expect(consoleNav(false, true)).toEqual([])
})
```

`ui/src/lib/auth/scopes.test.ts` — add to the first `describe`:

```ts
  it("requests the organization write scope (apply, review)", () => {
    expect(requested).toContain("billing:organization:write")
  })
```

and in `missingScopes`' first test, filter out and expect the new scope too:

```ts
    const old = requested.filter(s => !s.startsWith("billing:finance:") && s !== "billing:organization:write").join(" ")
    expect(missingScopes(jwt({scope: old}))).toEqual(["billing:organization:write", "billing:finance:read", "billing:finance:write"])
```

(order as `OAUTH_SCOPE` lists them: the organization scope sits before the finance ones in `CONSOLE_SCOPES` below.)

`ui/src/lib/errors.test.ts` — add to `BACKEND_CODES`: `"tenant_suspended", "application_not_pending", "already_merchant", "application_busy", "no_application", "companies_unavailable"`; add to `FIELD_CODES` (create the list's entries if absent): `"too_short", "invalid_url"`.

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/console/applications.test.ts src/lib/api/console.test.ts src/lib/console/nav.test.ts src/lib/auth/scopes.test.ts src/lib/errors.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=1`.

- [ ] **Step 3: Implement**

`ui/src/lib/api/consoleTypes.ts` — add the types in **Interfaces** above, and `can_review_applications?: boolean` on `MerchantOrganization` (doc: "Tenant zero's organization: the console shows Solicitações. Display data; the server decides.").

`ui/src/lib/api/console.ts` — in `consoleKeys` add:

```ts
  /** The application is not a mode's: no mode in the key (spec § 4.1). */
  merchantApplication: (s: ConsoleScope) => ["console", s.org, "merchant", "application"] as const,
  merchantCompanies: (s: ConsoleScope) => ["console", s.org, "merchant", "companies"] as const,
  applications: (s: ConsoleScope, status: ApplicationStatus) => ["console", s.org, s.mode, "applications", status] as const,
```

and the functions:

```ts
/** The selected organization's current application; null when it never applied. */
export async function getMerchantApplication(scope: ConsoleScope): Promise<MerchantApplication | null> {
  try {
    const {data} = await apiClient.get<MerchantApplication>("/v1.0/console/merchant/application", scopeHeaders(scope))
    return data
  } catch (error) {
    if (problemCode(error) === "no_application") return null
    throw error
  }
}

export async function applyForMerchant(scope: ConsoleScope, body: ApplyRequest): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>("/v1.0/console/merchant/application", body, scopeHeaders(scope))
  return data
}

export async function listMerchantCompanies(scope: ConsoleScope): Promise<MerchantCompany[]> {
  const {data} = await apiClient.get<{data: MerchantCompany[]}>("/v1.0/console/merchant/companies", scopeHeaders(scope))
  return data.data
}

export async function listApplications(scope: ConsoleScope, status: ApplicationStatus, cursor?: string): Promise<ConsolePage<MerchantApplication>> {
  const {data} = await apiClient.get<ConsolePage<MerchantApplication>>("/v1.0/console/applications", {
    ...scopeHeaders(scope), params: {status, ...(cursor ? {cursor} : {})},
  })
  return data
}

export async function approveApplication(scope: ConsoleScope, id: string): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>(`/v1.0/console/applications/${encodeURIComponent(id)}/approve`, {}, scopeHeaders(scope))
  return data
}

export async function rejectApplication(scope: ConsoleScope, id: string, reason: string): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>(`/v1.0/console/applications/${encodeURIComponent(id)}/reject`, {reason}, scopeHeaders(scope))
  return data
}
```

(import `problemCode` from `@/lib/api/client`, the new types from `consoleTypes`.)

`ui/src/lib/console/applications.ts`:

```ts
import type {ApplyRequest, MonthlyVolume} from "@/lib/api/consoleTypes"
import type {FieldError} from "@/lib/errors"
import limits from "@/lib/limits.json"

/** The volume ranges, in the order the select lists them (labels: console.volume.*). */
export const MONTHLY_VOLUMES: MonthlyVolume[] = ["up_to_10k", "10k_to_50k", "50k_to_200k", "over_200k"]

export type ApplyFormValues = {
  company_id: string
  description: string
  website: string
  expected_monthly_volume: MonthlyVolume | ""
  contact_email: string
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const chars = (v: string) => [...v].length

/** An absolute https address with a host and no credentials (the API's rule). */
export function isHttpsURL(v: string): boolean {
  try {
    const u = new URL(v)
    return u.protocol === "https:" && u.hostname !== "" && u.username === "" && u.password === ""
  } catch {
    return false
  }
}

function text(field: string, v: string, min: number, max: number, out: FieldError[]) {
  const n = chars(v)
  if (n === 0) out.push({field, code: "required"})
  else if (n < min) out.push({field, code: "too_short", params: {min}})
  else if (n > max) out.push({field, code: "too_long", params: {max}})
}

/** The API's checks, run before the round trip; the server re-checks all of them. */
export function applyFieldErrors(v: ApplyFormValues): FieldError[] {
  const out: FieldError[] = []
  if (!v.company_id) out.push({field: "company_id", code: "required"})
  text("description", v.description.trim(), limits.minText.applicationDescription, limits.text.applicationDescription, out)
  const site = v.website.trim()
  if (site && (chars(site) > limits.text.website || !isHttpsURL(site))) out.push({field: "website", code: "invalid_url"})
  const mail = v.contact_email.trim()
  if (!mail) out.push({field: "contact_email", code: "required"})
  else if (!EMAIL.test(mail) || mail.length > limits.text.email) out.push({field: "contact_email", code: "invalid_email"})
  return out
}

export function toApplyRequest(v: ApplyFormValues): ApplyRequest {
  const site = v.website.trim()
  return {
    company_id: v.company_id,
    description: v.description.trim(),
    ...(site ? {website: site} : {}),
    ...(v.expected_monthly_volume ? {expected_monthly_volume: v.expected_monthly_volume} : {}),
    contact_email: v.contact_email.trim(),
  }
}

export function rejectReasonErrors(reason: string): FieldError[] {
  const out: FieldError[] = []
  text("reason", reason.trim(), limits.minText.rejectionReason, limits.text.reason, out)
  return out
}
```

`ui/src/lib/console/selection.tsx`:

```tsx
"use client"

import {createContext, type ReactNode, useContext} from "react"

import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import type {ConsoleScope} from "@/lib/console/merchant"

/**
 * The selected organization, whether or not it has a tenant in this mode.
 * The invitation, the apply form and the status band read it; the billing
 * screens keep reading useConsoleScope, which exists only with a tenant.
 */
export type MerchantSelection = {scope: ConsoleScope; organization: MerchantOrganization}

const SelectionContext = createContext<MerchantSelection | null>(null)

export function MerchantSelectionProvider({value, children}: {value: MerchantSelection; children: ReactNode}) {
  return <SelectionContext.Provider value={value}>{children}</SelectionContext.Provider>
}

export function useMerchantSelection(): MerchantSelection {
  const v = useContext(SelectionContext)
  if (!v) throw new Error("useMerchantSelection: render inside the console shell, after an organization is selected")
  return v
}
```

`ui/src/lib/console/nav.ts` — add `{href: "/console/applications", label: "console.nav.applications"}` as `REVIEW_ITEM`, and:

```ts
export function consoleNav(hasOrganization: boolean, reviewer = false): {href: string; label: string}[] {
  if (!hasOrganization) return []
  return reviewer ? [...INVOICING_NAV, REVIEW_ITEM] : [...INVOICING_NAV]
}
```

`ui/src/lib/auth/scopes.ts` — `CONSOLE_SCOPES`: add `"billing:organization:write"` right after `"billing:organization:read"`, and extend the doc comment: "`billing:organization:write` applies for billing and decides applications; the `billing` client at ctech-account must be granted it before this ships (plan part 2, decision 8)."

`ui/src/locales/pt-BR/errors.json` (`code` object):

```json
    "tenant_suspended": "A cobrança desta organização está suspensa. As integrações só podem ler dados.",
    "application_not_pending": "Este pedido já foi decidido.",
    "already_merchant": "Esta organização já cobra pelo CTech Billing no modo produção.",
    "application_busy": "Outra pessoa está decidindo este pedido agora. Atualize a página.",
    "no_application": "Esta organização ainda não pediu acesso à cobrança.",
    "companies_unavailable": "Não foi possível carregar as empresas da organização agora. Tente de novo.",
```

(`field` object):

```json
    "too_short": "Use pelo menos {{min}} caracteres.",
    "invalid_url": "Informe um endereço que comece com https://.",
```

`ui/src/locales/en/errors.json`:

```json
    "tenant_suspended": "This organization's billing is suspended. Integrations can only read data.",
    "application_not_pending": "This application was already decided.",
    "already_merchant": "This organization already bills through CTech Billing in live mode.",
    "application_busy": "Someone else is deciding this application right now. Refresh the page.",
    "no_application": "This organization has not applied for billing yet.",
    "companies_unavailable": "We could not load the organization's companies right now. Try again.",
```

```json
    "too_short": "Use at least {{min}} characters.",
    "invalid_url": "Enter an address that starts with https://.",
```

`ui/src/locales/pt-BR/console.json` — add `"applications": "Solicitações"` under `nav`, and these blocks at top level (part 1's `noMerchant` block stays until Task 13 deletes `NoMerchant`):

```json
 "invitation": {
  "title": "Cobre seus clientes com o CTech Billing",
  "body": "{{name}} pode emitir faturas, cobrar assinaturas e receber avisos de pagamento pela API.",
  "points": {
   "subscriptions": "Assinaturas e faturas recorrentes",
   "api": "Integração pela API, com webhooks",
   "test": "Modo de teste na hora, para integrar sem risco"
  },
  "action": "Quero cobrar meus clientes",
  "review": "O modo produção abre depois da nossa análise.",
  "pendingTitle": "Modo produção em análise",
  "pendingBody": "Enquanto analisamos, {{name}} já pode integrar no modo de teste.",
  "rejectedTitle": "Modo produção não aprovado",
  "rejectedBody": "O modo de teste continua disponível. Veja o motivo e peça de novo quando quiser.",
  "toTest": "Ir para o modo de teste",
  "applyAgain": "Pedir de novo"
 },
 "apply": {
  "title": "Pedir acesso à cobrança",
  "description": "Conte um pouco sobre o negócio de {{name}}. O modo de teste fica pronto assim que você enviar.",
  "company": "Empresa que cobra",
  "companyHint": "O nome e o documento dela aparecem nas faturas.",
  "noCompanies": "Esta organização ainda não tem empresa cadastrada. Cadastre uma na CTech Conta e volte aqui.",
  "retry": "Tentar de novo",
  "about": "O que você vende e para quem",
  "aboutHint": "Entre 20 e 1000 caracteres.",
  "aboutPlaceholder": "Ex.: assinaturas mensais de cursos online para pessoas físicas",
  "website": "Site (opcional)",
  "volume": "Quanto espera cobrar por mês (opcional)",
  "contact": "E-mail para contato",
  "contactHint": "Avisamos aqui quando a análise terminar.",
  "submit": "Enviar pedido",
  "cancel": "Voltar",
  "submittedTitle": "Pedido enviado",
  "submittedBody": "O modo de teste de {{name}} está pronto. O modo produção está em análise; avisamos por e-mail quando terminar.",
  "openTest": "Abrir o modo de teste",
  "sentOn": "Enviado em {{date}}"
 },
 "volume": {
  "up_to_10k": "Até R$ 10 mil",
  "10k_to_50k": "De R$ 10 mil a R$ 50 mil",
  "50k_to_200k": "De R$ 50 mil a R$ 200 mil",
  "over_200k": "Acima de R$ 200 mil"
 },
 "band": {
  "pending": "Modo produção em análise. O modo de teste já está pronto para integrar.",
  "rejected": "O modo produção não foi aprovado: {{reason}}",
  "applyAgain": "Pedir de novo",
  "suspended": "A cobrança de {{name}} está suspensa. As integrações só leem dados e novas faturas estão paradas."
 },
 "applications": {
  "title": "Solicitações",
  "description": "Organizações que pediram para cobrar os próprios clientes.",
  "filter": "Situação",
  "pending": "Pendentes",
  "approved": "Aprovadas",
  "rejected": "Recusadas",
  "empty": {
   "pending": "Nenhuma solicitação pendente.",
   "approved": "Nenhuma solicitação aprovada ainda.",
   "rejected": "Nenhuma solicitação recusada."
  },
  "company": "Empresa",
  "website": "Site",
  "volume": "Volume esperado",
  "contact": "Contato",
  "appliedOn": "Pedido em {{date}}",
  "decidedOn": "Decidido em {{date}}",
  "reason": "Motivo",
  "approve": "Aprovar",
  "reject": "Recusar",
  "approveTitle": "Aprovar {{name}}?",
  "approveBody": "Isso cria o modo produção e a assinatura de acesso antecipado (R$ 0). Avisamos o contato por e-mail.",
  "approved": "{{name}} aprovada.",
  "rejectTitle": "Recusar {{name}}?",
  "rejectBody": "O modo de teste continua. A pessoa recebe o motivo por e-mail e pode pedir de novo.",
  "rejectReason": "Motivo",
  "rejectHint": "Entre 10 e 500 caracteres. Escreva o que a pessoa precisa corrigir.",
  "rejectSubmit": "Recusar pedido",
  "rejected_toast": "Pedido de {{name}} recusado.",
  "more": "Carregar mais"
 },
```

`ui/src/locales/en/console.json` — the same keys:

```json
 "invitation": {
  "title": "Bill your customers with CTech Billing",
  "body": "{{name}} can issue invoices, charge subscriptions and get payment notices through the API.",
  "points": {
   "subscriptions": "Subscriptions and recurring invoices",
   "api": "API integration, with webhooks",
   "test": "Test mode right away, to integrate safely"
  },
  "action": "I want to bill my customers",
  "review": "Live mode opens after our review.",
  "pendingTitle": "Live mode under review",
  "pendingBody": "While we review, {{name}} can already integrate in test mode.",
  "rejectedTitle": "Live mode not approved",
  "rejectedBody": "Test mode is still available. See the reason and apply again whenever you want.",
  "toTest": "Go to test mode",
  "applyAgain": "Apply again"
 },
 "apply": {
  "title": "Apply for billing",
  "description": "Tell us a little about {{name}}'s business. Test mode is ready as soon as you send this.",
  "company": "Billing company",
  "companyHint": "Its name and document appear on invoices.",
  "noCompanies": "This organization has no company yet. Add one in CTech Account and come back.",
  "retry": "Try again",
  "about": "What you sell and to whom",
  "aboutHint": "Between 20 and 1000 characters.",
  "aboutPlaceholder": "E.g. monthly subscriptions to online courses for individuals",
  "website": "Website (optional)",
  "volume": "Expected monthly billing (optional)",
  "contact": "Contact e-mail",
  "contactHint": "We let you know here when the review is done.",
  "submit": "Send application",
  "cancel": "Back",
  "submittedTitle": "Application sent",
  "submittedBody": "{{name}}'s test mode is ready. Live mode is under review; we will e-mail you when it is done.",
  "openTest": "Open test mode",
  "sentOn": "Sent on {{date}}"
 },
 "volume": {
  "up_to_10k": "Up to R$ 10k",
  "10k_to_50k": "R$ 10k to R$ 50k",
  "50k_to_200k": "R$ 50k to R$ 200k",
  "over_200k": "Over R$ 200k"
 },
 "band": {
  "pending": "Live mode under review. Test mode is ready for your integration.",
  "rejected": "Live mode was not approved: {{reason}}",
  "applyAgain": "Apply again",
  "suspended": "{{name}}'s billing is suspended. Integrations can only read data and new invoices are on hold."
 },
 "applications": {
  "title": "Applications",
  "description": "Organizations that asked to bill their own customers.",
  "filter": "Status",
  "pending": "Pending",
  "approved": "Approved",
  "rejected": "Rejected",
  "empty": {
   "pending": "No pending applications.",
   "approved": "No approved applications yet.",
   "rejected": "No rejected applications."
  },
  "company": "Company",
  "website": "Website",
  "volume": "Expected volume",
  "contact": "Contact",
  "appliedOn": "Applied on {{date}}",
  "decidedOn": "Decided on {{date}}",
  "reason": "Reason",
  "approve": "Approve",
  "reject": "Reject",
  "approveTitle": "Approve {{name}}?",
  "approveBody": "This creates live mode and the early access subscription (R$ 0). We e-mail the contact.",
  "approved": "{{name}} approved.",
  "rejectTitle": "Reject {{name}}?",
  "rejectBody": "Test mode stays. They get the reason by e-mail and can apply again.",
  "rejectReason": "Reason",
  "rejectHint": "Between 10 and 500 characters. Say what they need to fix.",
  "rejectSubmit": "Reject application",
  "rejected_toast": "{{name}}'s application rejected.",
  "more": "Load more"
 },
```

and `"applications": "Applications"` under `nav`.

Mock — `ui/src/lib/mockConfig.ts`: add `"pedido_em_analise" | "pedido_recusado" | "cobranca_suspensa"` to `MockScenario` and to `MOCK_SCENARIOS` (labels "Cobrança: pedido em análise", "Cobrança: pedido recusado", "Cobrança suspensa").

`ui/src/dev/consoleMockData.ts` — in part 1's `consoleSession(scenario)`, give the merchant organization `can_review_applications: true` (it plays tenant zero), and set the Padaria organization's `merchant_status` by scenario: `pedido_em_analise` → `"test"`, `pedido_recusado` → `"rejected"`, otherwise `"none"`; for `cobranca_suspensa` the merchant organization is `"suspended"`. Add:

```ts
export const MOCK_COMPANIES: MerchantCompany[] = [
  {company_id: "cmp_mock_padaria", legal_name: "Padaria Bom Dia LTDA", trade_name: "Padaria Bom Dia", tax_id: "12345678000190"},
]

export function mockApplication(status: ApplicationStatus): MerchantApplication {
  return {
    id: "apl_mock_padaria", status, organization_id: MOCK_NONE_ORG, organization_name: "Padaria Bom Dia",
    company: {id: "cmp_mock_padaria", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"},
    description: "Assinaturas semanais de pão e café para escritórios do bairro.",
    website: "https://padaria.example", expected_monthly_volume: "up_to_10k", contact_email: "contato@padaria.example",
    applied_by: "usr_mock", applied_at: "2026-10-09T14:20:00Z",
    ...(status === "rejected" ? {decided_at: "2026-10-10T09:00:00Z", rejection_reason: "O site não abre. Envie de novo com o endereço certo."} : {}),
  }
}
```

`ui/src/dev/mockRuntime.ts` — inside the console block, **before** part 1's "only the merchant organization has a tenant" refusal:

```ts
    // Part 2: the application runs without a tenant, for any organization.
    if (url.includes("/v1.0/console/merchant/")) {
      if (url.endsWith("/merchant/companies")) return ok(config, {data: MOCK_COMPANIES})
      if (method === "post") return ok(config, mockApplication("pending"), 201)
      if (scenario === "pedido_em_analise") return ok(config, mockApplication("pending"))
      if (scenario === "pedido_recusado") return ok(config, mockApplication("rejected"))
      fail(config, 404, "Not Found", "this organization has not applied", "about:blank", "no_application")
    }
    // Tenant zero's review: the merchant organization plays tenant zero.
    if (url.includes("/v1.0/console/applications")) {
      if (selector !== `org:${MOCK_MERCHANT_ORG}`) fail(config, 404, "Not Found", "resource not found", "about:blank", "resource_not_found")
      if (method === "post") {
        const decided = url.endsWith("/approve") ? "approved" : "rejected"
        return ok(config, {...mockApplication(decided), status: decided, decided_at: new Date().toISOString()})
      }
      const status = String((config.params as {status?: string})?.status ?? "pending")
      return ok(config, {data: status === "pending" ? [mockApplication("pending")] : [], has_more: false})
    }
```

(`selector` and `method` are the variables part 1's block already defines; if `method` is not defined there, use `(config.method ?? "get").toLowerCase()`.)

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/console/applications.test.ts src/lib/api/console.test.ts src/lib/console/nav.test.ts src/lib/auth/scopes.test.ts src/lib/errors.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=0`.
Run: `cd ui && npx next typegen && npx tsc --noEmit && npm run lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add ui/src
git commit -m "feat(console-ui): application types, calls and keys; billing:organization:write; copy and mock scenarios"
```

---
### Task 13: UI — the invitation, `/console/apply`, the status band, and the shell

> **Execute with the `/impeccable` skill** (`shape`, then `craft`), giving it `ui/PRODUCT.md`, `ui/DESIGN.md` ("Copy and controls", "On a phone", "Navigation and the shell", "Confirmations that say what they do", and part 1's "Console: whose billing"), spec § 7.1–7.2 and this task. `@aoctech/ui` only (`Button`/`buttonVariants`, `Field`, `Input`, `Select`, `Alert`, `PageHeader`, `Skeleton`, `ErrorState`); no new dependency. **Phone layouts, decided here, not squeezed:** the invitation is left-aligned and stacked, its action full width; the form is one column at every width (max `max-w-xl` from `sm`), its actions a full-width column on a phone (primary on top) and a right-aligned row from `sm`; the band's text is left-aligned with its action on its own line under `sm`. Check 320, 375, 768, 1280. No em dash.

**Files:**
- Create: `ui/src/components/ui/Textarea.tsx`
- Create: `ui/src/components/console/MerchantInvitation.tsx`, `MerchantInvitation.test.tsx`
- Create: `ui/src/components/console/ApplyForm.tsx`, `ApplyForm.test.tsx`
- Create: `ui/src/components/console/ApplicationSubmitted.tsx`
- Create: `ui/src/components/console/MerchantStatusBand.tsx`, `MerchantStatusBand.test.tsx`
- Create: `ui/src/app/(console)/console/apply/page.tsx`
- Delete: `ui/src/components/console/NoMerchant.tsx`, `NoMerchant.test.tsx` (and part 1's `console.noMerchant` keys in both catalogs)
- Modify: `ui/src/app/(console)/console/layout.tsx` (part 1's)
- Modify: `ui/src/components/AccountMenu.tsx`, `ui/src/components/AccountMenu.test.tsx`
- Modify: `ui/DESIGN.md` (new section)

**Interfaces:**
- Consumes: Task 12 everything; part 1's `useMode`, `setMode`, `tenantInMode`, `consoleKeys.session()`.
- Produces: `Textarea(props: TextareaHTMLAttributes)`; `MerchantInvitation()`; `ApplyForm({onApplied}: {onApplied: (a: MerchantApplication) => void})`; `ApplicationSubmitted({application}: {application: MerchantApplication})`; `MerchantStatusBand()`; route `/console/apply`.

- [ ] **Step 1: Write the failing tests**

A shared wrapper for this task's tests (top of each test file):

```tsx
import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import type {ReactNode} from "react"

import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {MerchantSelectionProvider} from "@/lib/console/selection"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
const org = (merchant_status: MerchantOrganization["merchant_status"]): MerchantOrganization =>
  ({organization_id: ORG, selector: `org:${ORG}`, display_name: "Padaria Bom Dia", role: "owner", merchant_status})

function wrap(ui: ReactNode, status: MerchantOrganization["merchant_status"], mode: "live" | "test" = "live") {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return (
    <QueryClientProvider client={client}>
      <MerchantSelectionProvider value={{scope: {mode, org: ORG}, organization: org(status)}}>{ui}</MerchantSelectionProvider>
    </QueryClientProvider>
  )
}
```

`ui/src/components/console/MerchantInvitation.test.tsx`:

```tsx
import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it} from "vitest"

import {MerchantInvitation} from "@/components/console/MerchantInvitation"
import {getMode} from "@/lib/console/mode"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"
// + the wrapper above

afterEach(() => window.localStorage.clear())

describe("MerchantInvitation", () => {
  it("invites an organization that does not bill yet to apply", () => {
    render(wrap(<MerchantInvitation/>, "none"))
    expect(screen.getByRole("heading", {name: "Cobre seus clientes com o CTech Billing"})).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Quero cobrar meus clientes"})).toHaveAttribute("href", "/console/apply")
  })

  it("sends an applicant waiting for live mode to test mode", async () => {
    render(wrap(<MerchantInvitation/>, "test"))
    expect(screen.getByText("Modo produção em análise")).toBeInTheDocument()
    expect(screen.queryByRole("link", {name: "Quero cobrar meus clientes"})).toBeNull()
    await userEvent.click(screen.getByRole("button", {name: "Ir para o modo de teste"}))
    expect(getMode()).toBe("test")
  })

  it("offers a rejected organization to apply again", () => {
    render(wrap(<MerchantInvitation/>, "rejected"))
    expect(screen.getByText("Modo produção não aprovado")).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Pedir de novo"})).toHaveAttribute("href", "/console/apply")
  })

  it("has no em dash in the console copy, in either language", () => {
    for (const catalog of [ptBR, en]) expect(JSON.stringify(catalog.console)).not.toContain("—")
  })
})
```

`ui/src/components/console/ApplyForm.test.tsx`:

```tsx
import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {ApplyForm} from "@/components/console/ApplyForm"
import * as api from "@/lib/api/console"
// + the wrapper above

afterEach(() => vi.restoreAllMocks())

const COMPANY = {company_id: "cmp_1", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"}

async function fill(description: string) {
  await userEvent.click(await screen.findByRole("combobox", {name: "Empresa que cobra"}))
  await userEvent.click(await screen.findByRole("option", {name: /Padaria Bom Dia LTDA/}))
  await userEvent.type(screen.getByLabelText("O que você vende e para quem"), description)
  await userEvent.type(screen.getByLabelText("E-mail para contato"), "c@padaria.example")
}

describe("ApplyForm", () => {
  it("refuses a short description before any request, saying the minimum", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue([COMPANY])
    const apply = vi.spyOn(api, "applyForMerchant")
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    await fill("curta")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    expect(await screen.findByText("Use pelo menos 20 caracteres.")).toBeInTheDocument()
    expect(apply).not.toHaveBeenCalled()
  })

  it("sends the trimmed form and hands the application back", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue([COMPANY])
    const apply = vi.spyOn(api, "applyForMerchant").mockResolvedValue({id: "apl_1", status: "pending"} as never)
    const onApplied = vi.fn()
    render(wrap(<ApplyForm onApplied={onApplied}/>, "none"))
    await fill("Assinaturas de pão para empresas do bairro")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    await vi.waitFor(() => expect(onApplied).toHaveBeenCalled())
    expect(apply).toHaveBeenCalledWith({mode: "live", org: ORG}, {
      company_id: "cmp_1", description: "Assinaturas de pão para empresas do bairro", contact_email: "c@padaria.example",
    })
  })

  it("explains an organization with no company instead of an empty select", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue([])
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    expect(await screen.findByText(/ainda não tem empresa cadastrada/)).toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Enviar pedido"})).toBeNull()
  })

  it("offers a retry when the companies cannot be loaded", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockRejectedValue({response: {status: 503, data: {code: "companies_unavailable"}}})
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    expect(await screen.findByRole("button", {name: "Tentar de novo"})).toBeInTheDocument()
  })
})
```

`ui/src/components/console/MerchantStatusBand.test.tsx`:

```tsx
import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

import {MerchantStatusBand} from "@/components/console/MerchantStatusBand"
import * as api from "@/lib/api/console"
// + the wrapper above

afterEach(() => vi.restoreAllMocks())

describe("MerchantStatusBand", () => {
  it("says a suspended merchant's billing is suspended, in either mode", () => {
    for (const mode of ["live", "test"] as const) {
      const {unmount} = render(wrap(<MerchantStatusBand/>, "suspended", mode))
      expect(screen.getByText(/A cobrança de Padaria Bom Dia está suspensa/)).toBeInTheDocument()
      unmount()
    }
  })

  it("shows the rejection's reason and the way to apply again, in test mode", async () => {
    vi.spyOn(api, "getMerchantApplication").mockResolvedValue({status: "rejected", rejection_reason: "O site não abre."} as never)
    render(wrap(<MerchantStatusBand/>, "rejected", "test"))
    expect(await screen.findByText(/O modo produção não foi aprovado: O site não abre\./)).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Pedir de novo"})).toHaveAttribute("href", "/console/apply")
  })

  it("is silent for a live merchant and for one with no tenant in this mode", () => {
    const {container, unmount} = render(wrap(<MerchantStatusBand/>, "live"))
    expect(container).toBeEmptyDOMElement()
    unmount()
    const second = render(wrap(<MerchantStatusBand/>, "test", "live")) // the invitation says it there
    expect(second.container).toBeEmptyDOMElement()
  })
})
```

In `ui/src/components/AccountMenu.test.tsx`, replace part 1's "offers no console to an admin whose organizations do not bill yet" with:

```tsx
  it("offers the console to an admin whose organization does not bill yet, to reach the invitation", async () => {
    vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: FULL, email: EMAIL, terms_accepted: true})
    vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({organizations: [{organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", selector: "org:0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", display_name: "Padaria", role: "admin", merchant_status: "none"}], organizations_unavailable: false})
    renderWithQuery(<AccountMenu view="portal"/>)
    const menu = await open()
    expect(await within(menu).findByRole("menuitem", {name: "Console"})).toBeInTheDocument()
  })
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/console/MerchantInvitation.test.tsx src/components/console/ApplyForm.test.tsx src/components/console/MerchantStatusBand.test.tsx src/components/AccountMenu.test.tsx; echo "vitest exit=$?"`
Expected: `vitest exit=1` (missing components; AccountMenu still hides the entry).

- [ ] **Step 3: Implement**

`ui/src/components/ui/Textarea.tsx` (ctech-ui has none; proposed upstream, plan decision 10):

```tsx
import {cn} from "@aoctech/ui"
import type {TextareaHTMLAttributes} from "react"

/** A multi-line Input: the same border, focus ring, invalid state and density. */
export function Textarea({className, ...props}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "min-h-28 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground outline-none",
        "placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/35",
        "disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-danger aria-invalid:ring-3 aria-invalid:ring-danger/20",
        "in-data-[density=compact]:px-2.5 in-data-[density=compact]:py-1.5",
        className,
      )}
      {...props}
    />
  )
}
```

`ui/src/components/console/MerchantInvitation.tsx`:

```tsx
"use client"

import {Button, buttonVariants} from "@aoctech/ui"
import {Check} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {setMode} from "@/lib/console/mode"
import {useMerchantSelection} from "@/lib/console/selection"

/**
 * What an organization without a tenant in this mode sees (spec § 7.1): the
 * invitation to apply, or, once it applied, where its live mode stands. A
 * beginning, never drawn as an error.
 */
export function MerchantInvitation() {
  const {t} = useTranslation()
  const {organization} = useMerchantSelection()
  const name = organization.display_name
  const status = organization.merchant_status

  if (status === "test" || status === "rejected") {
    const key = status === "test" ? "pending" : "rejected"
    return (
      <section className="max-w-xl space-y-3">
        <h1 className="text-xl font-semibold text-foreground">{t(`console.invitation.${key}Title`)}</h1>
        <p className="text-sm text-muted-foreground">
          {status === "test" ? t("console.invitation.pendingBody", {name}) : t("console.invitation.rejectedBody")}
        </p>
        <div className="flex flex-col gap-2 pt-2 sm:flex-row">
          <Button variant={status === "test" ? "brand" : "outline"} className="max-sm:w-full" onClick={() => setMode("test")}>
            {t("console.invitation.toTest")}
          </Button>
          {status === "rejected" && (
            <Link href="/console/apply" className={buttonVariants({variant: "brand", className: "max-sm:w-full"})}>
              {t("console.invitation.applyAgain")}
            </Link>
          )}
        </div>
      </section>
    )
  }

  return (
    <section className="max-w-xl space-y-4">
      <h1 className="text-2xl font-semibold tracking-[-0.02em] text-foreground">{t("console.invitation.title")}</h1>
      <p className="text-sm text-muted-foreground">{t("console.invitation.body", {name})}</p>
      <ul className="space-y-2">
        {(["subscriptions", "api", "test"] as const).map(p => (
          <li key={p} className="flex items-start gap-2 text-sm text-foreground">
            <Check aria-hidden className="mt-0.5 size-4 shrink-0 text-brand-600"/>
            {t(`console.invitation.points.${p}`)}
          </li>
        ))}
      </ul>
      <div className="space-y-2 pt-2">
        <Link href="/console/apply" className={buttonVariants({variant: "brand", className: "max-sm:w-full"})}>
          {t("console.invitation.action")}
        </Link>
        <p className="text-xs text-muted-foreground">{t("console.invitation.review")}</p>
      </div>
    </section>
  )
}
```

`ui/src/components/console/ApplyForm.tsx`:

```tsx
"use client"

import {Button, Field, Input, Select} from "@aoctech/ui"
import {useMutation, useQuery} from "@tanstack/react-query"
import {useRouter} from "next/navigation"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {Textarea} from "@/components/ui/Textarea"
import {messageFor} from "@/lib/api/client"
import {applyForMerchant, consoleKeys, listMerchantCompanies} from "@/lib/api/console"
import type {MerchantApplication, MonthlyVolume} from "@/lib/api/consoleTypes"
import {type ApplyFormValues, applyFieldErrors, MONTHLY_VOLUMES, toApplyRequest} from "@/lib/console/applications"
import {useMerchantSelection} from "@/lib/console/selection"
import {fieldErrorMessage} from "@/lib/errors"
import limits from "@/lib/limits.json"
import {selectCopy} from "@/lib/selectCopy"
import {useFieldErrors} from "@/lib/useFieldErrors"

const FIELDS = ["company_id", "description", "website", "expected_monthly_volume", "contact_email"] as const

/** The application (spec § 4.1). Every rule is the API's; the server re-checks. */
export function ApplyForm({onApplied}: {onApplied: (a: MerchantApplication) => void}) {
  const {t} = useTranslation()
  const router = useRouter()
  const {scope, organization} = useMerchantSelection()
  const [values, setValues] = useState<ApplyFormValues>({
    company_id: "", description: "", website: "", expected_monthly_volume: "", contact_email: "",
  })
  const [local, setLocal] = useState<Record<string, string>>({})
  const fe = useFieldErrors([...FIELDS], {keepOthers: false})

  const companies = useQuery({
    queryKey: consoleKeys.merchantCompanies(scope),
    queryFn: () => listMerchantCompanies(scope),
    retry: false,
  })

  const apply = useMutation({
    mutationFn: () => applyForMerchant(scope, toApplyRequest(values)),
    onSuccess: onApplied,
    onError: error => { fe.set(error) },
  })

  const set = <K extends keyof ApplyFormValues>(k: K, v: ApplyFormValues[K]) => {
    setValues(s => ({...s, [k]: v}))
    setLocal(s => ({...s, [k]: ""}))
    fe.clear(k)
  }
  const errorOf = (f: string) => local[f] || fe.of(f)

  const submit = () => {
    fe.reset()
    const errs = applyFieldErrors(values)
    setLocal(Object.fromEntries(errs.map(e => [e.field, fieldErrorMessage(e)])))
    if (errs.length === 0) apply.mutate()
  }

  if (companies.isPending) return <div className="h-40" aria-busy/>
  if (companies.isError) {
    return (
      <div className="max-w-xl space-y-3">
        <p className="text-sm text-foreground">{messageFor(companies.error)}</p>
        <Button variant="outline" onClick={() => void companies.refetch()}>{t("console.apply.retry")}</Button>
      </div>
    )
  }
  if (companies.data.length === 0) {
    return <p className="max-w-xl text-sm text-foreground">{t("console.apply.noCompanies")}</p>
  }

  return (
    <form className="max-w-xl space-y-5" noValidate onSubmit={e => { e.preventDefault(); submit() }}>
      <Field label={t("console.apply.company")} htmlFor="apply-company" hint={t("console.apply.companyHint")} error={errorOf("company_id")}>
        <Select {...selectCopy()}
          id="apply-company"
          aria-label={t("console.apply.company")}
          className="w-full"
          value={values.company_id}
          onValueChange={v => set("company_id", v)}
          options={companies.data.map(c => ({value: c.company_id, label: `${c.legal_name} • ${c.tax_id}`}))}
        />
      </Field>
      <Field label={t("console.apply.about")} htmlFor="apply-about" hint={t("console.apply.aboutHint")} error={errorOf("description")}>
        <Textarea id="apply-about" maxLength={limits.text.applicationDescription}
          placeholder={t("console.apply.aboutPlaceholder")} aria-invalid={!!errorOf("description") || undefined}
          value={values.description} onChange={e => set("description", e.target.value)}/>
      </Field>
      <Field label={t("console.apply.website")} htmlFor="apply-website" error={errorOf("website")}>
        <Input id="apply-website" type="url" inputMode="url" placeholder="https://" maxLength={limits.text.website}
          aria-invalid={!!errorOf("website") || undefined}
          value={values.website} onChange={e => set("website", e.target.value)}/>
      </Field>
      <Field label={t("console.apply.volume")} htmlFor="apply-volume" error={errorOf("expected_monthly_volume")}>
        <Select {...selectCopy()}
          id="apply-volume"
          aria-label={t("console.apply.volume")}
          className="w-full"
          value={values.expected_monthly_volume}
          onValueChange={v => set("expected_monthly_volume", v as MonthlyVolume)}
          options={MONTHLY_VOLUMES.map(v => ({value: v, label: t(`console.volume.${v}`)}))}
        />
      </Field>
      <Field label={t("console.apply.contact")} htmlFor="apply-contact" hint={t("console.apply.contactHint")} error={errorOf("contact_email")}>
        <Input id="apply-contact" type="email" inputMode="email" autoComplete="email" maxLength={limits.text.email}
          aria-invalid={!!errorOf("contact_email") || undefined}
          value={values.contact_email} onChange={e => set("contact_email", e.target.value)}/>
      </Field>
      {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      <div className="flex flex-col gap-2 sm:flex-row-reverse sm:justify-start">
        <Button type="submit" variant="brand" loading={apply.isPending} className="max-sm:w-full">{t("console.apply.submit")}</Button>
        <Button type="button" variant="ghost" className="max-sm:w-full" onClick={() => router.back()}>{t("console.apply.cancel")}</Button>
      </div>
      <p className="sr-only">{organization.display_name}</p>
    </form>
  )
}
```

(If `Button` has no `loading` prop in 0.4.1, use `disabled={apply.isPending}`; `/impeccable` decides whether the organization name belongs in visible copy here, the page header already carries it.)

`ui/src/components/console/ApplicationSubmitted.tsx`:

```tsx
"use client"

import {Button} from "@aoctech/ui"
import {useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {setMode} from "@/lib/console/mode"
import {formatDate} from "@/lib/format"

/** After applying (spec § 7.1): test mode is ready, live is under review. */
export function ApplicationSubmitted({application}: {application: MerchantApplication}) {
  const {t} = useTranslation()
  const router = useRouter()
  return (
    <section className="max-w-xl space-y-3" aria-live="polite">
      <h1 className="text-xl font-semibold text-foreground">{t("console.apply.submittedTitle")}</h1>
      <p className="text-sm text-foreground">{t("console.apply.submittedBody", {name: application.organization_name})}</p>
      <p className="text-xs text-muted-foreground">{t("console.apply.sentOn", {date: formatDate(application.applied_at)})}</p>
      <Button variant="brand" className="max-sm:w-full" onClick={() => { setMode("test"); router.push("/console/overview") }}>
        {t("console.apply.openTest")}
      </Button>
    </section>
  )
}
```

(Use the date formatter `ui/src/lib/format.ts` exports for an ISO instant; if it is named differently, use that one.)

`ui/src/components/console/MerchantStatusBand.tsx`:

```tsx
"use client"

import {useQuery} from "@tanstack/react-query"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {consoleKeys, getMerchantApplication} from "@/lib/api/console"
import {tenantInMode} from "@/lib/console/merchant"
import {useMerchantSelection} from "@/lib/console/selection"

/**
 * One line under the header for an organization whose billing is not simply
 * live (spec § 7.2): live under review, live refused (with its reason), or
 * suspended. Silent where the invitation already says it (no tenant in this
 * mode) and for a live merchant.
 */
export function MerchantStatusBand() {
  const {t} = useTranslation()
  const {scope, organization} = useMerchantSelection()
  const status = organization.merchant_status
  const visible = status === "suspended" || ((status === "test" || status === "rejected") && tenantInMode(status, scope.mode))
  const {data: application} = useQuery({
    queryKey: consoleKeys.merchantApplication(scope),
    queryFn: () => getMerchantApplication(scope),
    enabled: visible && status === "rejected",
  })
  if (!visible) return null

  const tone = status === "suspended" ? "bg-danger/10 border-danger/30" : "bg-surface border-border"
  return (
    <div role={status === "suspended" ? "alert" : "status"} className={`border-b ${tone}`}>
      <div className="mx-auto flex max-w-6xl flex-col gap-1 px-4 py-2 text-sm text-foreground sm:flex-row sm:items-center sm:justify-between sm:gap-4">
        <p>
          {status === "suspended" && t("console.band.suspended", {name: organization.display_name})}
          {status === "test" && t("console.band.pending")}
          {status === "rejected" && application?.rejection_reason && t("console.band.rejected", {reason: application.rejection_reason})}
        </p>
        {status === "rejected" && (
          <Link href="/console/apply" className="shrink-0 font-medium text-brand-600 underline-offset-4 hover:underline touch:min-h-11 touch:inline-flex touch:items-center">
            {t("console.band.applyAgain")}
          </Link>
        )}
      </div>
    </div>
  )
}
```

`ui/src/app/(console)/console/apply/page.tsx`:

```tsx
"use client"

import {ErrorState, PageHeader, Skeleton} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {ApplicationSubmitted} from "@/components/console/ApplicationSubmitted"
import {ApplyForm} from "@/components/console/ApplyForm"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, getMerchantApplication} from "@/lib/api/console"
import {useMerchantSelection} from "@/lib/console/selection"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** /console/apply: the form, or the pending application it already sent. */
export default function ApplyPage() {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const {scope, organization} = useMerchantSelection()
  useDocumentTitle(t("console.apply.title"))
  const live = organization.merchant_status === "live" || organization.merchant_status === "suspended"

  useEffect(() => {
    if (live) router.replace("/console/overview")
  }, [live, router])

  const {data: application, isPending, error, refetch} = useQuery({
    queryKey: consoleKeys.merchantApplication(scope),
    queryFn: () => getMerchantApplication(scope),
    enabled: !live,
  })

  if (live || isPending) return <Skeleton className="h-40 max-w-xl"/>
  if (error) return <ErrorState status={statusOf(error) === 503 ? 503 : 500} action={<button onClick={() => void refetch()}>{t("console.apply.retry")}</button>}/>
  if (application?.status === "pending") return <ApplicationSubmitted application={application}/>

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.apply.title")} description={t("console.apply.description", {name: organization.display_name})}/>
      <ApplyForm onApplied={app => {
        queryClient.setQueryData(consoleKeys.merchantApplication(scope), app)
        void queryClient.invalidateQueries({queryKey: consoleKeys.session()})
      }}/>
    </div>
  )
}
```

(Use `@aoctech/ui`'s `Button variant="outline"` for the retry if `/impeccable` prefers; `useDocumentTitle`'s signature is in `ui/src/lib/hooks/useDocumentTitle.ts`.)

`ui/src/app/(console)/console/layout.tsx` (part 1's) — after part 1's `scope` memo:

```tsx
  const selection = useMemo(
    () => (selected && org ? {scope: {mode, org}, organization: selected} : null),
    [selected, org, mode],
  )
  const onApply = pathname.startsWith("/console/apply")
  const reviewer = selected?.can_review_applications === true
```

`const nav = consoleNav(active)` → `const nav = consoleNav(active, reviewer)`. Under the test-mode banner:

```tsx
      {selection && !onApply && (
        <MerchantSelectionProvider value={selection}><MerchantStatusBand/></MerchantSelectionProvider>
      )}
```

and `<main>` becomes:

```tsx
      <main className="mx-auto max-w-6xl px-4 py-8 pb-20">
        {loading || !authenticated || noOrganization || (session === undefined && isPending) ? (
          <ShellSkeleton/>
        ) : unavailable ? (
          <ErrorState
            status={503}
            description={t("console.organization.unavailable")}
            action={<Button variant="outline" onClick={() => void refetch()}>{t("console.organization.retry")}</Button>}
          />
        ) : selection ? (
          <MerchantSelectionProvider value={selection}>
            {onApply ? children : !active ? <MerchantInvitation/> : (
              <ConsoleScopeProvider value={selection.scope}>{children}</ConsoleScopeProvider>
            )}
          </MerchantSelectionProvider>
        ) : (
          <ShellSkeleton/>
        )}
      </main>
```

(imports: `MerchantSelectionProvider`, `MerchantInvitation`, `MerchantStatusBand`; drop `NoMerchant`. Delete `NoMerchant.tsx`, its test and the `console.noMerchant` keys.)

`ui/src/components/AccountMenu.tsx` — the probe's `select` becomes `s => s.organizations.length > 0`, and its comment: "Any organization the person runs: one that bills opens its console, one that does not opens the invitation to apply (part 2)."

`ui/DESIGN.md` — append "## Console: applying and reviewing (2026-10-10)" recording: the invitation is a beginning (heading, one paragraph, three checked points, one brand action, the review note under it), never an error state; left-aligned and full-width action on a phone; after applying, the same place says where live mode stands and offers test mode; the form is one column at every width, its actions a column on a phone (primary first) and a right-aligned row from `sm`; the status band sits under the test-mode band, `surface` for under review or refused, `danger` tint for suspended (the only status fill on the page), its action on its own line on a phone; the Console entry in the account menu now shows for any organization the person runs; and whatever `/impeccable` decided beyond this.

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `vitest exit=0`.
Run: `cd ui && npx next typegen && npx tsc --noEmit && npm run lint && npm run build` → clean; static export built (`/console/apply` included).
Browser check on `npm run dev:mock` at 320, 375, 768, 1280: default scenario, switch to Padaria Bom Dia → invitation; "Quero cobrar meus clientes" → form, submit → "Pedido enviado"; `?scenario=pedido_em_analise` in live → "Modo produção em análise" with the test-mode button, in test → the band; `?scenario=pedido_recusado` in test → the band with the reason and "Pedir de novo"; `?scenario=cobranca_suspensa` → the danger band on CTech Tecnologia; no horizontal scroll anywhere.

- [ ] **Step 5: Commit**

```bash
git add -A ui/src ui/DESIGN.md
git commit -m "feat(console-ui): invitation, /console/apply and the merchant status band"
```

---

### Task 14: UI — `/console/applications` (tenant zero's review)

> **Execute with the `/impeccable` skill** as in Task 13, with spec § 7.3. **Phone layout:** each application is a stacked card (organization name and date on top, company and document, description, then a definition list for site, volume and contact), its two actions sharing a full-width row at the bottom (Recusar outline, Aprovar brand); from `sm` the same card with the actions right-aligned in its header. The filter is `Segmented`, `fill` on a phone. Pending is the default and lists oldest first (a queue). No em dash.

**Files:**
- Create: `ui/src/app/(console)/console/applications/page.tsx`
- Create: `ui/src/components/console/ApplicationCard.tsx`, `ApplicationCard.test.tsx`
- Create: `ui/src/components/console/RejectDialog.tsx`

**Interfaces:**
- Consumes: Task 12 `listApplications`, `approveApplication`, `rejectApplication`, `consoleKeys.applications`, `rejectReasonErrors`; part 1's `useConsoleScope`.
- Produces: `ApplicationCard({application}: {application: MerchantApplication})`; `RejectDialog({application, open, onClose}: {...})`; route `/console/applications`.

- [ ] **Step 1: Write the failing test** — `ui/src/components/console/ApplicationCard.test.tsx`

```tsx
import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {ApplicationCard} from "@/components/console/ApplicationCard"
import * as api from "@/lib/api/console"
import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {ConsoleScopeProvider} from "@/lib/console/scope"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const
const APP: MerchantApplication = {
  id: "apl_1", status: "pending", organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", organization_name: "Padaria Bom Dia",
  company: {id: "cmp_1", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"},
  description: "Assinaturas de pão para empresas do bairro", website: "https://padaria.example",
  expected_monthly_volume: "up_to_10k", contact_email: "c@padaria.example", applied_by: "usr_1", applied_at: "2026-10-09T14:20:00Z",
}

function show(app = APP) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ConsoleScopeProvider value={SCOPE}><ApplicationCard application={app}/></ConsoleScopeProvider>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.restoreAllMocks())

describe("ApplicationCard", () => {
  it("shows what a reviewer decides on, with the volume's label, never its key", () => {
    show()
    expect(screen.getByText("Padaria Bom Dia")).toBeInTheDocument()
    expect(screen.getByText(/Padaria Bom Dia LTDA/)).toBeInTheDocument()
    expect(screen.getByText("Até R$ 10 mil")).toBeInTheDocument()
    expect(screen.queryByText("up_to_10k")).toBeNull()
  })

  it("approves only after the confirmation that says what approving does", async () => {
    const approve = vi.spyOn(api, "approveApplication").mockResolvedValue({...APP, status: "approved"})
    show()
    await userEvent.click(screen.getByRole("button", {name: "Aprovar"}))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText(/assinatura de acesso antecipado \(R\$ 0\)/)).toBeInTheDocument()
    expect(approve).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole("button", {name: "Aprovar"}))
    await vi.waitFor(() => expect(approve).toHaveBeenCalledWith(SCOPE, "apl_1"))
  })

  it("will not reject without a reason of at least 10 characters", async () => {
    const reject = vi.spyOn(api, "rejectApplication").mockResolvedValue({...APP, status: "rejected"})
    show()
    await userEvent.click(screen.getByRole("button", {name: "Recusar"}))
    const dialog = await screen.findByRole("dialog")
    await userEvent.type(within(dialog).getByLabelText("Motivo"), "curto")
    await userEvent.click(within(dialog).getByRole("button", {name: "Recusar pedido"}))
    expect(reject).not.toHaveBeenCalled()
    await userEvent.type(within(dialog).getByLabelText("Motivo"), " demais, o site não abre")
    await userEvent.click(within(dialog).getByRole("button", {name: "Recusar pedido"}))
    await vi.waitFor(() => expect(reject).toHaveBeenCalledWith(SCOPE, "apl_1", "curto demais, o site não abre"))
  })

  it("has no actions on a decided application, and shows the reason of a rejected one", () => {
    show({...APP, status: "rejected", decided_at: "2026-10-10T09:00:00Z", rejection_reason: "O site não abre."})
    expect(screen.queryByRole("button", {name: "Aprovar"})).toBeNull()
    expect(screen.getByText("O site não abre.")).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/console/ApplicationCard.test.tsx; echo "vitest exit=$?"` → `vitest exit=1`.

- [ ] **Step 3: Implement**

`ui/src/components/console/RejectDialog.tsx`:

```tsx
"use client"

import {Field, Modal} from "@aoctech/ui"
import {useMutation, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {Textarea} from "@/components/ui/Textarea"
import {messageFor} from "@/lib/api/client"
import {rejectApplication} from "@/lib/api/console"
import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {rejectReasonErrors} from "@/lib/console/applications"
import {useConsoleScope} from "@/lib/console/scope"
import {fieldErrorMessage} from "@/lib/errors"
import limits from "@/lib/limits.json"

/** Rejecting needs a reason the applicant will read (spec § 4.2: 10 to 500). */
export function RejectDialog({application, open, onClose}: {application: MerchantApplication; open: boolean; onClose: () => void}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const [reason, setReason] = useState("")
  const [error, setError] = useState("")
  const reject = useMutation({
    mutationFn: () => rejectApplication(scope, application.id, reason.trim()),
    onSuccess: () => {
      toast.success(t("console.applications.rejected_toast", {name: application.organization_name}))
      void queryClient.invalidateQueries({queryKey: ["console", scope.org, scope.mode, "applications"]})
      onClose()
    },
    onError: e => setError(messageFor(e)),
  })
  return (
    <Modal
      open={open}
      onClose={onClose}
      danger
      title={t("console.applications.rejectTitle", {name: application.organization_name})}
      description={t("console.applications.rejectBody")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.applications.rejectSubmit")}
      loading={reject.isPending}
      onSubmit={() => {
        const errs = rejectReasonErrors(reason)
        if (errs.length) { setError(fieldErrorMessage(errs[0])); return }
        setError("")
        reject.mutate()
      }}
    >
      <Field label={t("console.applications.rejectReason")} htmlFor="reject-reason" hint={t("console.applications.rejectHint")} error={error || undefined}>
        <Textarea id="reject-reason" maxLength={limits.text.reason} value={reason}
          aria-invalid={!!error || undefined} onChange={e => { setReason(e.target.value); setError("") }}/>
      </Field>
    </Modal>
  )
}
```

`ui/src/components/console/ApplicationCard.tsx`:

```tsx
"use client"

import {Button, Modal} from "@aoctech/ui"
import {useMutation, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {RejectDialog} from "@/components/console/RejectDialog"
import {messageFor} from "@/lib/api/client"
import {approveApplication} from "@/lib/api/console"
import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {useConsoleScope} from "@/lib/console/scope"
import {formatDate} from "@/lib/format"

/** One application, as tenant zero's reviewers decide on it (spec § 7.3). */
export function ApplicationCard({application: a}: {application: MerchantApplication}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [rejecting, setRejecting] = useState(false)
  const approve = useMutation({
    mutationFn: () => approveApplication(scope, a.id),
    onSuccess: () => {
      toast.success(t("console.applications.approved", {name: a.organization_name}))
      void queryClient.invalidateQueries({queryKey: ["console", scope.org, scope.mode, "applications"]})
      setConfirming(false)
    },
    onError: e => toast.error(messageFor(e)),
  })
  const pending = a.status === "pending"

  return (
    <article className="space-y-3 rounded-xl border border-border p-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-foreground">{a.organization_name}</h2>
          <p className="text-xs text-muted-foreground">
            {pending || !a.decided_at
              ? t("console.applications.appliedOn", {date: formatDate(a.applied_at)})
              : t("console.applications.decidedOn", {date: formatDate(a.decided_at)})}
          </p>
        </div>
        {pending && (
          <div className="grid grid-cols-2 gap-2 sm:flex">
            <Button variant="outline" onClick={() => setRejecting(true)}>{t("console.applications.reject")}</Button>
            <Button variant="brand" onClick={() => setConfirming(true)}>{t("console.applications.approve")}</Button>
          </div>
        )}
      </header>
      <p className="text-sm text-foreground">{a.company.legal_name} • {a.company.tax_id}</p>
      <p className="text-sm text-foreground">{a.description}</p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-3">
        {a.website && (<div><dt className="text-xs text-muted-foreground">{t("console.applications.website")}</dt><dd className="break-all">{a.website}</dd></div>)}
        {a.expected_monthly_volume && (<div><dt className="text-xs text-muted-foreground">{t("console.applications.volume")}</dt><dd>{t(`console.volume.${a.expected_monthly_volume}`)}</dd></div>)}
        <div><dt className="text-xs text-muted-foreground">{t("console.applications.contact")}</dt><dd className="break-all">{a.contact_email}</dd></div>
      </dl>
      {a.rejection_reason && (
        <p className="text-sm text-muted-foreground"><span className="font-medium text-foreground">{t("console.applications.reason")}: </span>{a.rejection_reason}</p>
      )}
      <Modal
        open={confirming}
        onClose={() => setConfirming(false)}
        title={t("console.applications.approveTitle", {name: a.organization_name})}
        description={t("console.applications.approveBody")}
        cancelLabel={t("common.cancel")}
        submitLabel={t("console.applications.approve")}
        loading={approve.isPending}
        onSubmit={() => approve.mutate()}
      />
      <RejectDialog application={a} open={rejecting} onClose={() => setRejecting(false)}/>
    </article>
  )
}
```

`ui/src/app/(console)/console/applications/page.tsx`:

```tsx
"use client"

import {Button, EmptyState, ErrorState, PageHeader, Segmented, Skeleton} from "@aoctech/ui"
import {useInfiniteQuery} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {ApplicationCard} from "@/components/console/ApplicationCard"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, listApplications} from "@/lib/api/console"
import type {ApplicationStatus} from "@/lib/api/consoleTypes"
import {useConsoleScope} from "@/lib/console/scope"
import {useMediaQuery} from "@/lib/hooks/useMediaQuery"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

const STATUSES: ApplicationStatus[] = ["pending", "approved", "rejected"]

/** Solicitações: tenant zero only; any other tenant gets the server's 404. */
export default function ApplicationsPage() {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  useDocumentTitle(t("console.applications.title"))
  const phone = useMediaQuery("(max-width: 639px)")
  const [status, setStatus] = useState<ApplicationStatus>("pending")
  const q = useInfiniteQuery({
    queryKey: consoleKeys.applications(scope, status),
    queryFn: ({pageParam}) => listApplications(scope, status, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  })
  const items = q.data?.pages.flatMap(p => p.data) ?? []

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.applications.title")} description={t("console.applications.description")}/>
      <Segmented
        label={t("console.applications.filter")}
        value={status}
        onValueChange={setStatus}
        fill={phone}
        options={STATUSES.map(s => ({value: s, label: t(`console.applications.${s}`)}))}
      />
      {q.isPending ? (
        <div className="space-y-3" aria-busy><Skeleton className="h-36 w-full"/><Skeleton className="h-36 w-full"/></div>
      ) : q.isError ? (
        <ErrorState status={statusOf(q.error) === 404 ? 404 : 500}/>
      ) : items.length === 0 ? (
        <EmptyState title={t(`console.applications.empty.${status}`)} className="max-sm:items-start max-sm:text-left"/>
      ) : (
        <div className="space-y-3">
          {items.map(a => <ApplicationCard key={a.id} application={a}/>)}
          {q.hasNextPage && (
            <Button variant="outline" className="max-sm:w-full" disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
              {t("console.applications.more")}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}
```

(`useMediaQuery` is `ui/src/lib/hooks/useMediaQuery.ts`; match its exact signature.)

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `vitest exit=0`.
Run: `cd ui && npx next typegen && npx tsc --noEmit && npm run lint && npm run build` → clean.
Browser check on `npm run dev:mock` (CTech Tecnologia, live) at 320, 375, 768, 1280: "Solicitações" in the nav; the pending Padaria card; Aprovar opens the confirmation and then the card leaves Pendentes; Recusar refuses "curto" and sends a full reason; on Padaria Bom Dia the nav has no Solicitações and `/console/applications` shows the not-found state.

- [ ] **Step 5: Commit**

```bash
git add ui/src
git commit -m "feat(console-ui): Solicitações, tenant zero's review of applications"
```

---

## Final verification (end of Task 14, before the pull request)

Sequentially, never in parallel:

1. `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race && make test-integration` → clean, all `ok`, `PASS`.
2. `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `exit=0`; `npx next typegen && npx tsc --noEmit && npm run lint && npm run build` → clean.
3. `cd terraform/billing && terraform validate` → valid.
4. `grep -rn "—" ui/src/locales/*/console.json ui/src/locales/*/errors.json` → no line added by this plan. `grep -rn "NoMerchant\|noMerchant" ui/src` → nothing.
5. `grep -n '"m2m"' api/internal/oauthresource/scope-manifest.json` → no entry on `billing:organization:write`.
6. Cross-repo check (CLAUDE.md): the PR body proposes (a) `Company`/`Companies` in api-commons `accountorgs` (billing and ctech-dfe each carry one), (b) `Textarea` in ctech-ui, and lists the ctech-account prerequisites (companies list route; `internal:account:company-actor` and `billing:organization:write` grants).

## Interfaces for part 3 (stable; do not rename)

```go
// Suspension: the flag is on the tenant row.
billing.Organization.SuspendedAt string; (*billing.Organization).Suspended() bool  // live only
billing.ErrTenantSuspended
middleware.RefuseSuspendedWrites(orgs middleware.TenantRows) fiber.Handler          // already on every M2M route; code "tenant_suspended"
problem.TypeTenantSuspended = "/problems/tenant-suspended"
// Part 3 decides whether console credential/webhook writes of a suspended tenant are refused;
// GetOrganization(c).Suspended() is available behind ResolveMerchantTenant.

// Live tenant existence ("live mode requires the live tenant, approved"):
// the merchant chain already answers 404 no_merchant when X-Billing-Mode: live has no tenant;
// outside the chain: (*OrganizationRepository).GetByAccountOrganization(ctx, acct, true).
billing.MerchantStatus values now all computed; MerchantStatusOf(test, live, latest ApplicationStatus)

// Approval:
billing.MerchantOwnerKey = "billing"; services.EarlyAccessPriceID
(*services.Applications).LatestStatus(ctx, acct) (billing.ApplicationStatus, error)
middleware.RequireTenantZero(tenantZero string) fiber.Handler
middleware.ScopeOrganizationWrite = "billing:organization:write"  // credentials/webhooks writes may reuse it

// ctech-account client pattern (part 3 adds OAuthClient(ctx, clientID) next to it):
package accountclient: New(Config) *Client (nil when unconfigured), getJSON(ctx, token, path, out) (status, error)

// Entitlement hook, if part 3 needs another reaction to prod_billing changes:
repositories.EntitlementHook; (*SubscriptionRepository).WithEntitlementHook — one hook today; compose, do not replace.
```

```ts
// UI
useMerchantSelection(): {scope: ConsoleScope; organization: MerchantOrganization}   // also without a tenant
MerchantOrganization.can_review_applications?: boolean
consoleNav(hasOrganization, reviewer?)        // part 3 appends its items to INVOICING_NAV
Textarea                                      // ui/src/components/ui/Textarea.tsx
```
