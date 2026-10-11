# Console self-service 1 — Access by membership Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The billing (merchant) half of the console resolves its tenant from the selected organization (`X-Billing-Space: org:{id}`) and the person's ctech-account role, finds the tenant by a new lookup key `{mode}#ACCOUNT_ORG#{account_organization_id}`, admits only `owner`/`admin`, and the console UI gains an organization selector and a `no_merchant` empty state. `owner_user_id` stops granting access once a backfill has rewritten the lookup key of every linked tenant.

**Architecture:** The group behind `/v1.0/console` (everything except `/console/finance/*`, `/console/session` and `/v1/me`) runs `RequireUserSession → ResolveSpace → ResolveMerchantTenant`. `ResolveSpace` is the existing finance resolver (membership checked before any table read, 60 s cache, fails closed); `ResolveMerchantTenant` refuses anything that is not an organization space held as owner/admin, then reads the tenant with `OrganizationRepository.GetByAccountOrganization` and sets the same locals (`OrganizationKey`, `TenantKey`) `ResolveConsoleTenant` set, so no handler changes. `GET /v1.0/console/session` moves out of the group and lists the person's owner/admin organizations with `merchant_status`. During this deploy a tenant still carrying the legacy `{mode}#OWNER#{user}` key is reachable by that user **only if** the tenant is linked to the selected organization; a backfill command rewrites the keys and a follow-up deploy (Task 11) deletes the fallback and `GetByOwner`.

**Tech Stack:** Go 1.27.2 (`api/go.mod`), Fiber v3.5, DynamoDB through api-commons v1.15.0 (`accountorgs` membership client, `cache`, `dynamo.Base`); Next.js 16 static export, React 19, TanStack Query 5, `@aoctech/ui` 0.4.1, vitest.

**Spec:** [`docs/specs/2026-10-10-console-self-service.md`](../specs/2026-10-10-console-self-service.md) — § 3 (access by membership), § 6.1 row 1 (session), § 6.2 row 1 (tenant lookup), § 7.5 (selector), § 8 (failure), § 9 step 2, § 10.1 (spoofing) and § 10.6 (migration). Decision record: [ADR 0028](../adr/0028-console-tenant-from-membership.md). Builds on [ADR 0011](../adr/0011-console-session.md) (mode from the request), [ADR 0025](../adr/0025-spaces-personal-and-organization.md) (space selector, resolver), [ADR 0027](../adr/0027-personal-workspaces.md) (workspace kinds). Parts 2 (applications, approval, suspension) and 3 (credentials, webhooks) are later plans and consume the interfaces below.

## Interfaces for parts 2 and 3 (stable; do not rename)

These are produced by this plan and consumed by the plans for parts 2 and 3. A task that would change one of them must stop and ask.

```go
// api/internal/repositories/keys.go
func LookupOrganizationAccountPK(livemode bool, accountOrgID string) string // "{live|test}#ACCOUNT_ORG#{accountOrgID}"

// api/internal/repositories/organizations.go
func (r *OrganizationRepository) GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)
//   ErrNotFound (wrapped) when no tenant; ErrAccountOrganizationAmbiguous (wrapped) when two rows carry the key.
var ErrAccountOrganizationAmbiguous error // "more than one tenant is linked to the account organization"
var ErrAccountOrganizationTaken     error // Create/LinkAccountOrganization: the key already names another tenant

// api/internal/domain/billing/merchant.go
type MerchantStatus string
const (
	MerchantNone      MerchantStatus = "none"
	MerchantTest      MerchantStatus = "test"
	MerchantLive      MerchantStatus = "live"
	MerchantRejected  MerchantStatus = "rejected"  // computed from part 2 on
	MerchantSuspended MerchantStatus = "suspended" // computed from part 2 on
)
func (s MerchantStatus) Valid() bool
func MerchantStatusOf(test, live *Organization) MerchantStatus // part 2 extends its inputs

// api/internal/space
func (s ResolvedSpace) Role() string           // "owner" | "admin" | "member" | "viewer" | "" (personal default, jobs)
func IsMerchantRole(kind Kind, role string) bool // kind == KindOrganization && role in {owner, admin}

// api/internal/middleware/merchant.go
type MerchantTenants interface {
	GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)
}
type MerchantAccess struct {
	AccountOrganizationID string // the selected ctech-account organization (X-Billing-Space org:{id})
	Role                  string // "owner" | "admin"
	Livemode              bool   // from X-Billing-Mode
}
const MerchantAccessKey = "merchant_access"
func RequireUserSession() fiber.Handler                       // 401 no claims; 403 user_session_required for service tokens
func RequireMerchantRole() fiber.Handler                      // after ResolveSpace: personal/non-organization → 404 space_not_found; member/viewer → 403 merchant_role_required; sets MerchantAccess
func ResolveMerchantTenant(src MerchantTenants) fiber.Handler // after ResolveSpace: RequireMerchantRole + tenant → 404 no_merchant; sets MerchantAccess, OrganizationKey, TenantKey
func GetMerchantAccess(c fiber.Ctx) MerchantAccess            // zero value when neither ran
func LookupMerchantTenant(ctx context.Context, src MerchantTenants, sub, accountOrgID string, livemode bool) (*billing.Organization, error)
// Handlers also keep using the existing middleware.GetSpace(c) (space.ResolvedSpace, now with Role()),
// middleware.GetOrganization(c) and middleware.GetTenant(c).

// api/internal/problem/problem.go
const TypeNoMerchant = "/problems/no-merchant" // code "no_merchant"
```

Route chain (Task 5), which parts 2 and 3 mount under:

```go
console := v1.Group("/console", auth,
	middleware.RequireUserSession(),
	middleware.ResolveSpace(spaces),
	middleware.ResolveMerchantTenant(d.Organizations))
```

A part-2 route that must run **without** a tenant (the application) is registered before this group, like `/console/session`, with `auth, RequireUserSession(), ResolveSpace(spaces), RequireMerchantRole()`.

`GET /v1.0/console/session` (no `X-Billing-Mode`, no `X-Billing-Space`; user session with `billing:organization:read`):

```json
{
  "organizations": [
    {
      "organization_id": "01a04ed6-1af9-745e-bcf7-d3b66fe52321",
      "selector": "org:01a04ed6-1af9-745e-bcf7-d3b66fe52321",
      "display_name": "CTech",
      "role": "owner",
      "merchant_status": "live"
    }
  ],
  "organizations_unavailable": false
}
```

`organizations` holds only `kind = organization` workspaces where the role is `owner` or `admin`, ordered by `display_name` (case-insensitive, then `selector`); never `null`. `merchant_status` ∈ `none | test | live | rejected | suspended`. ctech-account unreachable → `200` with `organizations: []`, `organizations_unavailable: true`.

UI (TypeScript, `ui/src/lib/api/consoleTypes.ts`, `ui/src/lib/console/merchant.ts`, `ui/src/lib/console/scope.tsx`):

```ts
export type MerchantStatus = "none" | "test" | "live" | "rejected" | "suspended"
export interface MerchantOrganization {organization_id: string; selector: string; display_name: string; role: "owner" | "admin"; merchant_status: MerchantStatus}
export interface ConsoleSession {organizations: MerchantOrganization[]; organizations_unavailable: boolean}
export interface ConsoleTenant {organization_id: string; display_name: string; livemode: boolean; payout_status: string; can_charge: boolean} // was ConsoleSession
export type ConsoleScope = {mode: Mode; org: string}       // org = account organization id
export function tenantInMode(status: MerchantStatus, mode: Mode): boolean
export function pickMerchantOrg(orgs: MerchantOrganization[], stored: string | null, mode: Mode): MerchantOrganization | null
export function useConsoleScope(): ConsoleScope            // inside the console shell only
export function isNoMerchant(error: unknown): boolean      // ui/src/lib/api/client.ts
```

## Scope decisions (read before executing)

1. **`personal` is a 404 with the space-not-found body**, byte for byte the same as an unknown organization (spec § 3.1: "the same body as any unknown space"). Its code is therefore `space_not_found`, the code `ResolveSpace` already emits; the spec's "`not_found`" is read as "the not-found answer", not a new code. A personal **workspace** (`org:{id}` of `kind = personal`) gets the same 404: only organizations are merchants (D6).
2. **ctech-account unreachable** keeps `ResolveSpace`'s existing `503` (`type /problems/space-unavailable`, code `space_unavailable`), which the UI already recognises (`isSpaceUnavailable`). The spec's `membership_unavailable` is read as this answer; no second code for the same outage.
3. **`/console/session` leaves the tenant group.** It answers before any organization is chosen, so it cannot be behind a resolver. It is registered before the group, exactly as finance is, because Fiber mounts a group's handlers as prefix middleware (see the comment above `registerFinance` in `router.go`). Its old body (the tenant's id, name, mode, payout gate) is renamed `tenantResponse` and survives only as the `organization` block of `GET /console/settings`, which is behind the resolver.
4. **The legacy fallback is narrow.** While the backfill has not run, `LookupMerchantTenant` falls back to `GetByOwner(sub)` and accepts the tenant **only** when its `account_organization_id` equals the selected organization and its mode matches; membership and role were already checked. So after this deploy and before the backfill, tenant zero is reachable by its stored owner (who is also an owner of account org `01a04ed6-1af9-745e-bcf7-d3b66fe52321`), and its other admins get `no_merchant` until the backfill. An unlinked tenant is never reached through the fallback (ADR 0028: reachable only through M2M credentials).
5. **The owner key stops being written in Task 7**, not in Task 3. Until the switch (Task 5) the old resolver still reads it, and the integration suite runs green at every commit. `SetOwner` never overwrites an `ACCOUNT_ORG` key, from Task 3 on.
6. **One tenant per account organization per mode** is checked by `Create` and `LinkAccountOrganization` with a read of the index before the write (`ErrAccountOrganizationTaken`). Both are operator paths (the seed); the race is accepted here and closed in part 2, whose application transaction adds a conditional guard. `GetByAccountOrganization` refuses to choose when two rows carry the key (`ErrAccountOrganizationAmbiguous` → `500 organization_resolve_failed`), and the backfill reports such pairs as `conflicts` and writes neither.
7. **`/v1/me`** keeps answering "can this person open the console", now by membership: the first organization (by name) where the person is owner/admin **and** a live tenant exists; `console` is `{id: tenant id, name: tenant display name}` as before. ctech-account unreachable → `console: null` (an identity probe, not an error).
8. **The UI offers the Console entry only when an organization has a tenant** (`merchant_status` other than `none`). Part 1 has no application form, so showing the console to every organization owner would lead them to an invitation with nothing to do. Inside the console the selector lists every owner/admin organization, `none` ones included, and those render the `no_merchant` empty state. Part 2 widens the entry.
9. **The selected merchant organization is stored apart from the finance space** (`ctech-billing-console-organization`, not `ctech-billing-finance-space`): the finance half can be `personal`, the billing half never is, and spec § 7.5 keeps the finance selector as it is.
10. **Query keys carry the organization.** Every billing-half key becomes `["console", org, mode, ...]`, so two organizations are two caches and switching cannot show one organization's rows under the other's name for a frame. Every API function takes `scope: ConsoleScope` as its **first** parameter (not optional, never defaulted from storage).
11. **Deploy order** (`.github/workflows/README.md`, written in Task 8): API and UI of this plan together → backfill dry run, `-apply`, dry run again (`missing=0`, exit 0) → confirm tenant zero by one of its admins who is not `owner_user_id` → merge the follow-up (Task 11). Between the API and UI deploys, a cached old UI sends no `X-Billing-Space` and gets `400`; the static UI deploys in the same pipeline run, so this window is the CDN's.
12. **Out of this plan:** applications, review routes, approval, suspension and their statuses' computation (part 2); credentials and webhooks (part 3); the tenant-zero-only review routes' spoofing test (part 2); any change to the finance half or the portal.

## Global Constraints

- Go commands from `api/`. Go 1.27.2. Every Go task ends with `gofmt -l ./internal ./cmd ./tests` (no output), `go vet ./...` and `go test ./... -race`; tasks that touch repositories, routes or `tests/integration` also run `make test-integration` with DynamoDB Local (`docker compose -f docker-compose.test.yml up -d`, port 8124).
- UI commands from `ui/`. `npm ci` first if `node_modules/@aoctech/ui/package.json` is not 0.4.1. `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"` — **judge vitest by its exit code**, not by scanning output. Then `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`.
- **Never run the Go and UI suites at the same time** (one finishes before the other starts).
- UI tasks are executed with the `/impeccable` skill, on `@aoctech/ui` components (`Select`, `EmptyState`, `ErrorState`, `Skeleton`, `Button`); no new dependency. Every screen has **its own phone layout** (checked at 320, 375 and 768 px as well as 1280). pt-BR and en for every string. **No em dash (—) in any UI text** (i18n values); Go comments and docs may keep theirs.
- Routes and endpoints in English; Portuguese only in i18n text (spec D7).
- Header names are wire contracts: `X-Billing-Mode`, `X-Billing-Space` (`middleware.ModeHeader`, `middleware.SpaceHeader`, and the literals in `ui/src/lib/api/console.ts`). Never rename.
- Error codes, exactly: `merchant_role_required` (403), `no_merchant` (404, type `/problems/no-merchant`), `space_not_found` (404, the existing body), `space_unavailable` (503, existing), `user_session_required` (403, existing), `organization_resolve_failed` (500, existing).
- Lookup key, exactly: `{mode}#ACCOUNT_ORG#{account_organization_id}`, `mode` ∈ `live|test` (`repositories.Mode`).
- Roles that reach the billing half: `owner`, `admin` of a `kind = organization` workspace. Nothing else, ever by default.
- Production facts the migration must respect: tenant zero `ctech` is linked to account org `01a04ed6-1af9-745e-bcf7-d3b66fe52321` and carries `owner_user_id` (its rows carry `live#OWNER#…` / `test#OWNER#…` today).
- Backfill runner: `api/cmd/backfill-organization-lookup/run.sh ENV [-apply]`, AWS profile `ctech` (overridable), region from the profile, `TABLE_PREFIX={env}_billing`, secrets from SSM, never printed. Dry run by default.
- Commit messages: Conventional Commits. **No attribution of any kind** (no `Co-Authored-By`, no "Generated with", no Claude/Anthropic mention) in commits or PR bodies.

## Review Focus

1. **A person who is a member of the selected organization but whose `owner_user_id` names another tenant** — the role decides, never the stored owner: a `member` is `403 merchant_role_required` even when `GetByOwner(sub)` would find a tenant linked to that organization. — Task 4 `TestTheLegacyFallbackNeverWidensTheRole`.
2. **`SetOwner` re-run by the seed on a tenant that already carries the `ACCOUNT_ORG` key** — the key survives; the console must not lose tenant zero because `tenants/ctech.json` names an owner. — Task 3 `TestSetOwnerNeverReplacesTheAccountOrganizationKey`.
3. **Two tenants linked to the same account organization in one mode** (a mistaken seed, a manual fix) — no request is answered with "whichever row the index returned first": `500 organization_resolve_failed`, and the backfill writes neither and exits 1. — Task 3 `TestTwoTenantsOnOneKeyAreRefusedNotChosen`, Task 8 `TestBackfillReportsTwoTenantsOnOneOrganizationAndWritesNeither`.
4. **Switching organizations in the UI while a list is loading** — no row of organization A is ever rendered under organization B: keys carry the organization, and the switch removes the old organization's queries. — Task 9 `keys every query by organization and mode`, Task 10 `stores the choice and removes the previous organization's queries`.
5. **An admin whose ctech-account answer is cached as "admin" for 60 s after being demoted** — accepted limit (ADR 0025/0028), but a cached **negative** answer must not become a 503 or a 500: a just-added admin waits at most 60 s and then reaches the tenant. — Task 4 `TestACachedRefusalIsAPlain404`.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/space/space.go`, `resolver.go`, `verbs.go` (modify); `space_test.go` (modify) | `ResolvedSpace.Role()`, `IsMerchantRole` |
| `api/internal/domain/billing/merchant.go`, `merchant_test.go` (new) | `MerchantStatus`, `MerchantStatusOf` |
| `api/internal/repositories/keys.go`, `keys_test.go` (modify) | `LookupOrganizationAccountPK` |
| `api/internal/repositories/organizations.go` (modify) | `GetByAccountOrganization`, errors, key written by `Create`/`LinkAccountOrganization`, `SetOwner` guard; Task 7 drops the owner key |
| `api/internal/repositories/rows.go` (modify) | `organizationRow.LookupPK` comment |
| `api/internal/repositories/organizations_backfill.go` (new) | `BackfillAccountOrganizationLookup` |
| `api/internal/problem/problem.go` (modify) | `TypeNoMerchant` |
| `api/internal/middleware/merchant.go`, `merchant_test.go` (new) | the merchant chain |
| `api/internal/middleware/console.go` (modify) | `ResolveConsoleTenant` deleted |
| `api/internal/api/v1/router.go` (modify) | the console group's chain; `/console/session` before it |
| `api/internal/api/v1/console.go`, `console_dto.go`, `console_settings.go`, `me.go` (modify); `console_session_test.go` (new) | session DTO and handler, `tenantResponse`, `/v1/me` by membership |
| `api/internal/app/app.go` (modify) | `BuildOrganizations` |
| `api/cmd/backfill-organization-lookup/main.go`, `run.sh` (new) | the backfill command and its runner |
| `.github/workflows/README.md` (modify) | deploy sequence "Console access by membership" |
| `api/tests/integration/fake_account_test.go` (new, moved) | `fakeAccount` shared by every env |
| `api/tests/integration/api_test.go`, `repositories_test.go`, `console_test.go`, `console_writes_test.go`, `finance_shared_spaces_test.go`, `portal_test.go`, `finance_invoices_test.go` (modify) | the env linked to an account organization; console helpers send the selector |
| `api/tests/integration/console_access_test.go` (new) | § 10.1 spoofing over every billing-half route; session |
| `api/tests/integration/organization_lookup_test.go` (new) | repository lookup, backfill, § 10.6 migration |
| `ui/src/lib/api/consoleTypes.ts`, `console.ts`, `client.ts` (modify); `console.test.ts` (new) | session/tenant types, scope headers, keys |
| `ui/src/lib/console/merchant.ts`, `merchant.test.ts`, `useMerchantOrg.ts`, `scope.tsx` (new) | selection store, pure selection rules, scope context |
| `ui/src/app/(console)/console/*/page.tsx`, `ui/src/components/console/CreditNoteDialog.tsx` (modify) | `useConsoleScope()` instead of `useMode()` |
| `ui/src/app/(console)/console/layout.tsx`, `ui/src/components/console/ModeSwitch.tsx`, `ui/src/components/AccountMenu.tsx`, `ui/src/lib/console/nav.ts` (modify) | the shell |
| `ui/src/components/console/OrganizationSwitch.tsx`, `NoMerchant.tsx` and tests (new) | selector and empty state |
| `ui/src/dev/consoleMockData.ts`, `ui/src/dev/mockRuntime.ts` (modify) | mock session and the `sem_cobranca` scenario |
| `ui/src/locales/{pt-BR,en}/console.json`, `errors.json` (modify); `ui/src/lib/errors.test.ts` (modify) | copy |
| `ui/DESIGN.md` (modify) | "Console: whose billing" section |

---

### Task 1: The resolved space carries the role

**Files:**
- Modify: `api/internal/space/space.go` (struct `ResolvedSpace`, new methods after `Verbs()`)
- Modify: `api/internal/space/resolver.go:80`
- Modify: `api/internal/space/verbs.go` (append)
- Test: `api/internal/space/space_test.go` (append)

**Interfaces:**
- Consumes: nothing new.
- Produces: `func (s ResolvedSpace) Role() string`; `func IsMerchantRole(kind Kind, role string) bool`.

- [ ] **Step 1: Write the failing tests** (append to `api/internal/space/space_test.go`; `fakeSource`/`answer` live in `resolver_test.go`, same package)

```go
func TestAnOrganizationSpaceCarriesTheRoleCtechAccountSent(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{
		orgB + "|alice": {member: true, role: "admin", kind: "organization"},
	}}
	r := NewResolver(src, cache.NewMemoryBackend(10))
	sp, err := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgB}, true)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Role() != "admin" {
		t.Fatalf("role = %q, want admin", sp.Role())
	}
	// From the cache too: the second answer must not lose the role.
	sp, _ = r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgB}, true)
	if sp.Role() != "admin" || src.calls != 1 {
		t.Fatalf("cached role = %q, calls = %d", sp.Role(), src.calls)
	}
}

func TestThePersonalDefaultSpaceAndJobSpacesHaveNoRole(t *testing.T) {
	r := NewResolver(nil, nil)
	sp, err := r.Resolve(context.Background(), "alice", Selector{Personal: true}, false)
	if err != nil || sp.Role() != "" {
		t.Fatalf("personal role = %q, %v", sp.Role(), err)
	}
	job, err := ForJob(orgB, true)
	if err != nil || job.Role() != "" {
		t.Fatalf("job role = %q, %v", job.Role(), err)
	}
}

func TestNarrowKeepsTheRole(t *testing.T) {
	sp := orgSpace(orgB, KindOrganization, true, All).withRole("owner")
	if Narrow(sp, Read).Role() != "owner" {
		t.Fatal("Narrow dropped the role")
	}
}

func TestOnlyOwnersAndAdminsOfOrganizationsAreMerchants(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		role string
		want bool
	}{
		{KindOrganization, "owner", true},
		{KindOrganization, "admin", true},
		{KindOrganization, "member", false},
		{KindOrganization, "viewer", false},
		{KindOrganization, "", false},
		{KindOrganization, "Owner", false}, // roles are lower-case on the wire; no folding
		{KindPersonal, "owner", false},
		{KindPersonalDefault, "owner", false},
		{Kind("team"), "owner", false},
	} {
		if got := IsMerchantRole(c.kind, c.role); got != c.want {
			t.Errorf("IsMerchantRole(%q, %q) = %v, want %v", c.kind, c.role, got, c.want)
		}
	}
}
```

Add `"context"` and `"gopkg.aoctech.app/api-commons/cache"` to the test file's imports if absent.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd api && go test ./internal/space/ -run 'Role|Merchants' -v`
Expected: FAIL — compile errors `sp.Role undefined`, `undefined: IsMerchantRole`, `withRole undefined`.

- [ ] **Step 3: Implement**

`api/internal/space/space.go` — add the field to `ResolvedSpace` (after `verbs Verbs`):

```go
	// role is the caller's role in the workspace as ctech-account sent it; ""
	// for the default personal space and for job spaces.
	role string
```

and, after `func (s ResolvedSpace) Verbs() Verbs`:

```go
// Role is the caller's role in the workspace as ctech-account sent it
// ("owner", "admin", "member", "viewer"), and "" for the default personal space
// and for job spaces. Billing decides what a role may do (ADR 0023): read it
// through VerbsFor or IsMerchantRole, never compare it ad hoc in a handler.
func (s ResolvedSpace) Role() string { return s.role }

// withRole is set by the resolver only: a role, like verbs, is never built
// from a header.
func (s ResolvedSpace) withRole(role string) ResolvedSpace {
	s.role = role
	return s
}
```

`api/internal/space/resolver.go:80` — replace the return:

```go
	return orgSpace(sel.OrganizationID, kind, livemode, verbs).withRole(m.Role), nil
```

`api/internal/space/verbs.go` — append:

```go
// IsMerchantRole reports whether a workspace kind and role reach the billing
// (merchant) half of the console (ADR 0028): owner or admin of an
// organization. Personal workspaces are never merchants, and member and viewer
// never reach it — it holds the merchant's customers' personal data. Any other
// role or kind is refused, so a ladder that grows upstream inherits nothing.
func IsMerchantRole(kind Kind, role string) bool {
	return kind == KindOrganization && (role == "owner" || role == "admin")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go test ./internal/space/ -race -v -run 'Role|Merchants|NoExportedFields'`
Expected: PASS (including the existing `TestResolvedSpaceHasNoExportedFields`: `role` is unexported).

- [ ] **Step 5: Full package and commit**

Run: `cd api && gofmt -l ./internal && go vet ./internal/space/ && go test ./internal/space/ -race`
Expected: no gofmt output; `ok  gopkg.aoctech.app/billing/api/internal/space`.

```bash
git add api/internal/space/
git commit -m "feat(space): the resolved space carries the ctech-account role; IsMerchantRole"
```

---

### Task 2: `billing.MerchantStatus`

**Files:**
- Create: `api/internal/domain/billing/merchant.go`
- Test: `api/internal/domain/billing/merchant_test.go`

**Interfaces:**
- Consumes: `billing.Organization`.
- Produces: `type MerchantStatus string`; constants `MerchantNone`, `MerchantTest`, `MerchantLive`, `MerchantRejected`, `MerchantSuspended`; `func (s MerchantStatus) Valid() bool`; `func MerchantStatusOf(test, live *Organization) MerchantStatus`.

- [ ] **Step 1: Write the failing test**

```go
package billing

import (
	"encoding/json"
	"testing"
)

func TestMerchantStatusFromTheTenantRows(t *testing.T) {
	test := &Organization{ID: "org_1", Livemode: false}
	live := &Organization{ID: "org_1", Livemode: true}
	for _, c := range []struct {
		name       string
		test, live *Organization
		want       MerchantStatus
	}{
		{"no tenant", nil, nil, MerchantNone},
		{"test only: applied, waiting", test, nil, MerchantTest},
		{"live exists: approved", test, live, MerchantLive},
		// A live row without a test row does not happen through the product,
		// but a provisioned tenant can be live-only: it is still live.
		{"live only", nil, live, MerchantLive},
	} {
		if got := MerchantStatusOf(c.test, c.live); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The wire values are a contract with the console and with parts 2 and 3.
func TestMerchantStatusWireValues(t *testing.T) {
	all := []MerchantStatus{MerchantNone, MerchantTest, MerchantLive, MerchantRejected, MerchantSuspended}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `["none","test","live","rejected","suspended"]` {
		t.Fatalf("wire values = %s", raw)
	}
	for _, s := range all {
		if !s.Valid() {
			t.Errorf("%q is not valid", s)
		}
	}
	if MerchantStatus("approved").Valid() || MerchantStatus("").Valid() {
		t.Fatal("an unknown status is valid")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/domain/billing/ -run MerchantStatus -v`
Expected: FAIL — `undefined: MerchantStatusOf`, `undefined: MerchantNone`.

- [ ] **Step 3: Implement** `api/internal/domain/billing/merchant.go`

```go
package billing

// MerchantStatus is what the console shows about an account organization as a
// merchant (spec § 3): whether it has a tenant, in which modes, and later
// whether its application was rejected or its live tenant suspended.
//
// It is display data, never authority. Access is decided per request by
// ctech-account membership and by the tenant row the request reads (ADR 0028);
// nothing reads this value to allow anything.
type MerchantStatus string

const (
	// MerchantNone: no tenant in either mode. The console shows the invitation.
	MerchantNone MerchantStatus = "none"
	// MerchantTest: a test-mode tenant only (applied, live waiting on review).
	MerchantTest MerchantStatus = "test"
	// MerchantLive: a live tenant exists (approved, or provisioned by CTech).
	MerchantLive MerchantStatus = "live"
	// MerchantRejected: the latest application was rejected; the test tenant
	// stays. Computed from part 2 on.
	MerchantRejected MerchantStatus = "rejected"
	// MerchantSuspended: the live tenant is suspended (reads only). Computed
	// from part 2 on.
	MerchantSuspended MerchantStatus = "suspended"
)

// Valid reports one of the five wire values.
func (s MerchantStatus) Valid() bool {
	switch s {
	case MerchantNone, MerchantTest, MerchantLive, MerchantRejected, MerchantSuspended:
		return true
	}
	return false
}

// MerchantStatusOf derives the status from one account organization's tenant
// rows, nil when absent. Part 2 adds the application and the live row's
// suspension to its inputs; until then rejected and suspended cannot arise.
func MerchantStatusOf(test, live *Organization) MerchantStatus {
	switch {
	case live != nil:
		return MerchantLive
	case test != nil:
		return MerchantTest
	default:
		return MerchantNone
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/domain/billing/ -race -run MerchantStatus -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/billing/merchant.go api/internal/domain/billing/merchant_test.go
git commit -m "feat(billing): MerchantStatus (none, test, live; rejected and suspended reserved)"
```

---

### Task 3: The tenant is found by its account organization

**Files:**
- Modify: `api/internal/repositories/keys.go` (after `LookupOrganizationOwnerPK`, ~line 399)
- Modify: `api/internal/repositories/keys_test.go` (append)
- Modify: `api/internal/repositories/organizations.go` (`Create`, new `GetByAccountOrganization`, `ensureAccountOrganizationFree`, `LinkAccountOrganization`, `SetOwner`)
- Modify: `api/internal/repositories/rows.go:38-41` (comment)
- Test: `api/tests/integration/organization_lookup_test.go` (new)

**Interfaces:**
- Consumes: `repositories.Mode`, `TenantPK`, `OrganizationSK`, `IndexLookup`, `QueryOpts`.
- Produces: `func LookupOrganizationAccountPK(livemode bool, accountOrgID string) string`; `func (r *OrganizationRepository) GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)`; `var ErrAccountOrganizationAmbiguous`, `var ErrAccountOrganizationTaken`. `Create` writes the `ACCOUNT_ORG` key when `AccountOrganizationID` is set (else the owner key, until Task 7); `LinkAccountOrganization` writes it; `SetOwner` writes the owner key only on an unlinked tenant.

- [ ] **Step 1: Write the failing unit test** (append to `api/internal/repositories/keys_test.go`)

```go
func TestTheAccountOrganizationKeyIsModeScopedAndDistinctFromTheOwnerKey(t *testing.T) {
	const acct = "01a04ed6-1af9-745e-bcf7-d3b66fe52321"
	if got := LookupOrganizationAccountPK(true, acct); got != "live#ACCOUNT_ORG#"+acct {
		t.Fatalf("live key = %q", got)
	}
	if got := LookupOrganizationAccountPK(false, acct); got != "test#ACCOUNT_ORG#"+acct {
		t.Fatalf("test key = %q", got)
	}
	// A user id that happens to equal an organization id must not collide.
	if LookupOrganizationAccountPK(true, acct) == LookupOrganizationOwnerPK(true, acct) {
		t.Fatal("account and owner keys collide")
	}
}
```

- [ ] **Step 2: Write the failing integration tests** — create `api/tests/integration/organization_lookup_test.go`

```go
//go:build integration

package integration

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The console finds a tenant by the ctech-account organization it is linked to
// (ADR 0028), one per mode, and never chooses between two.

func orgRepo() *repositories.OrganizationRepository {
	return repositories.NewOrganizationRepository(testDB, testCfg)
}

// setLookup overwrites a tenant row's lookup_pk (and, when acct is not empty,
// its link) directly, the way rows written before this deploy look — or the
// way a mistaken manual fix would leave them.
func setLookup(t *testing.T, org *billing.Organization, lookup, acct string) {
	t.Helper()
	expr := "SET lookup_pk = :l"
	values := map[string]types.AttributeValue{":l": &types.AttributeValueMemberS{Value: lookup}}
	if acct != "" {
		expr += ", account_organization_id = :a"
		values[":a"] = &types.AttributeValueMemberS{Value: acct}
	}
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableOrganizations)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, org.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.OrganizationSK()},
		},
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeValues: values,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// legacyLookup gives a tenant the pre-ADR-0028 owner key, which is what every
// production row carries before the backfill.
func legacyLookup(t *testing.T, org *billing.Organization) {
	t.Helper()
	setLookup(t, org, repositories.LookupOrganizationOwnerPK(org.Livemode, org.OwnerUserID), "")
}

func TestATenantIsFoundByItsAccountOrganizationInItsModeOnly(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)

	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID {
		t.Fatalf("live = %v, %v; want %s", got, err, org.ID)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, false); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("test mode: err = %v, want ErrNotFound", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, newSpaceOrgID(), true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("unknown organization: err = %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, "", true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("empty id: err = %v", err)
	}
}

func TestLinkingWritesTheAccountOrganizationKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true) // unlinked, owner key (until Task 7)
	acct := newSpaceOrgID()
	if err := orgRepo().LinkAccountOrganization(ctx, org, acct, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID {
		t.Fatalf("after link = %v, %v", got, err)
	}
}

func TestAnAccountOrganizationNamesOneTenantPerMode(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	newLinkedOrg(t, true, acct)

	second := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "Second", Livemode: true,
		PayoutStatus: billing.PayoutEnabled, AccountOrganizationID: acct,
	}
	if err := orgRepo().Create(ctx, second, now()); !errors.Is(err, repositories.ErrAccountOrganizationTaken) {
		t.Fatalf("create on a taken key: err = %v", err)
	}
	other := newOrg(t, true)
	if err := orgRepo().LinkAccountOrganization(ctx, other, acct, "test", "req_1", now()); !errors.Is(err, repositories.ErrAccountOrganizationTaken) {
		t.Fatalf("link to a taken key: err = %v", err)
	}
	// The same account organization in the other mode is a different key.
	if newLinkedOrg(t, false, acct) == nil {
		t.Fatal("unreachable")
	}
}

func TestTwoTenantsOnOneKeyAreRefusedNotChosen(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	newLinkedOrg(t, true, acct)
	stray := newOrg(t, true)
	setLookup(t, stray, repositories.LookupOrganizationAccountPK(true, acct), acct)

	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrAccountOrganizationAmbiguous) {
		t.Fatalf("err = %v, want ErrAccountOrganizationAmbiguous", err)
	}
}

func TestSetOwnerNeverReplacesTheAccountOrganizationKey(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "API only", Livemode: true,
		PayoutStatus: billing.PayoutEnabled, AccountOrganizationID: acct,
	}
	if err := orgRepo().Create(ctx, org, now()); err != nil {
		t.Fatal(err)
	}
	owner := "usr_" + id.New()
	if err := orgRepo().SetOwner(ctx, org, owner, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := orgRepo().GetByAccountOrganization(ctx, acct, true)
	if err != nil || got.ID != org.ID || got.OwnerUserID != owner {
		t.Fatalf("after SetOwner = %+v, %v", got, err)
	}
}
```

Add `newLinkedOrg` to `api/tests/integration/repositories_test.go`, right after `newOrg`:

```go
// newLinkedOrg is newOrg linked to a ctech-account organization, which is how
// the console finds a tenant (ADR 0028).
func newLinkedOrg(t *testing.T, livemode bool, accountOrgID string) *billing.Organization {
	t.Helper()
	repo := repositories.NewOrganizationRepository(testDB, testCfg)
	org := &billing.Organization{
		ID:                    id.NewWithPrefix(id.PrefixOrganization),
		DisplayName:           "Test Org",
		Livemode:              livemode,
		PayoutStatus:          billing.PayoutEnabled,
		OwnerUserID:           "usr_" + id.New(),
		AccountOrganizationID: accountOrgID,
	}
	if err := repo.Create(ctxT(t), org, now()); err != nil {
		t.Fatal(err)
	}
	return org
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `cd api && go test ./internal/repositories/ -run AccountOrganizationKey -v`
Expected: FAIL — `undefined: LookupOrganizationAccountPK`.
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'AccountOrganization|TwoTenantsOnOneKey|SetOwnerNever|LinkingWrites' -v`
Expected: FAIL — compile errors `GetByAccountOrganization undefined`, `undefined: repositories.ErrAccountOrganizationTaken`.

- [ ] **Step 4: Implement**

`api/internal/repositories/keys.go`, after `LookupOrganizationOwnerPK`:

```go
// LookupOrganizationAccountPK resolves a ctech-account organization to its
// billing tenant in one mode (ADR 0028): the console's question is "which
// tenant is the organization I selected", and the membership that authorizes
// the question is checked before this key is ever read.
//
// The mode is in the key for the reason LookupOrganizationOwnerPK gives: the
// live and test tenants are two rows linked to the same organization.
func LookupOrganizationAccountPK(livemode bool, accountOrgID string) string {
	return Mode(livemode) + "#ACCOUNT_ORG#" + accountOrgID
}
```

`api/internal/repositories/organizations.go` — replace the lookup block in `Create`:

```go
	switch {
	case org.AccountOrganizationID != "":
		if err := r.ensureAccountOrganizationFree(ctx, org.ID, org.AccountOrganizationID, org.Livemode); err != nil {
			return err
		}
		row.LookupPK = LookupOrganizationAccountPK(org.Livemode, org.AccountOrganizationID)
	case org.OwnerUserID != "":
		// Transitional: the owner key is read by the pre-ADR-0028 console until
		// the switch, and is no longer written from the next task on.
		row.LookupPK = LookupOrganizationOwnerPK(org.Livemode, org.OwnerUserID)
	}
```

Add after `GetByOwner`:

```go
// ErrAccountOrganizationAmbiguous is two tenant rows carrying one account
// organization's key in one mode. Answering with whichever row the index
// returned first would hand one merchant another's data, so nothing is chosen.
var ErrAccountOrganizationAmbiguous = errors.New("more than one tenant is linked to the account organization")

// ErrAccountOrganizationTaken is a create or link naming an account
// organization another tenant already answers for in that mode.
var ErrAccountOrganizationTaken = errors.New("account organization is already linked to another tenant")

// GetByAccountOrganization resolves the selected ctech-account organization to
// its tenant in one mode (ADR 0028). Like GetByOwner and
// CredentialRepository.Resolve, the tenant is the answer, never part of the
// question: the caller passes an organization the resolver has already
// confirmed the person belongs to.
func (r *OrganizationRepository) GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error) {
	if accountOrgID == "" {
		return nil, fmt.Errorf("%w: no account organization", ErrNotFound)
	}
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupOrganizationAccountPK(livemode, accountOrgID),
		// Two, not one: a second row is a fault to refuse, not a row to skip.
		Limit: 2,
	})
	if err != nil {
		return nil, err
	}
	switch len(res.Items) {
	case 0:
		return nil, fmt.Errorf("%w: tenant of account organization %s in %s mode", ErrNotFound, accountOrgID, Mode(livemode))
	case 1:
	default:
		return nil, fmt.Errorf("%w: %s in %s mode", ErrAccountOrganizationAmbiguous, accountOrgID, Mode(livemode))
	}
	row, err := Decode[organizationRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	// The row must agree with the key it was found by; a hand-edited key on
	// somebody else's row is not a tenant of this organization.
	if row.AccountOrganizationID != accountOrgID || row.Livemode != livemode {
		return nil, fmt.Errorf("%w: lookup row %s disagrees with its own link", ErrNotFound, row.ID)
	}
	return &row.Organization, nil
}

// ensureAccountOrganizationFree refuses a create or link onto a key another
// tenant holds. A read before the write: both callers are the operator's seed,
// and part 2's self-service creation adds a conditional guard of its own.
func (r *OrganizationRepository) ensureAccountOrganizationFree(ctx context.Context, tenantID, accountOrgID string, livemode bool) error {
	existing, err := r.GetByAccountOrganization(ctx, accountOrgID, livemode)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case existing.ID == tenantID:
		return nil
	default:
		return fmt.Errorf("%w: %s answers for %s in %s mode", ErrAccountOrganizationTaken, existing.ID, accountOrgID, Mode(livemode))
	}
}
```

In `LinkAccountOrganization`, after the `actor == ""` check and before building the audit item:

```go
	if err := r.ensureAccountOrganizationFree(ctx, org.ID, accountOrgID, org.Livemode); err != nil {
		return err
	}
```

and replace its update expression and values:

```go
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		"SET account_organization_id = :a, lookup_pk = :l, updated_at = :now",
		"attribute_exists(pk) AND attribute_not_exists(account_organization_id)", nil,
		map[string]types.AttributeValue{
			":a":   &types.AttributeValueMemberS{Value: accountOrgID},
			":l":   &types.AttributeValueMemberS{Value: LookupOrganizationAccountPK(org.Livemode, accountOrgID)},
			":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		},
	)
```

In `SetOwner`, replace the update block:

```go
	// The owner key is written only on a tenant with no link: a linked tenant is
	// found by its account organization (ADR 0028), and overwriting that key with
	// an owner's would take it away from every admin. The condition re-checks the
	// link on the row itself, not on the copy in memory.
	expr := "SET owner_user_id = :o, updated_at = :now"
	cond := "attribute_exists(pk) AND (attribute_not_exists(owner_user_id) OR owner_user_id = :empty)"
	values := map[string]types.AttributeValue{
		":o":     &types.AttributeValueMemberS{Value: ownerUserID},
		":empty": &types.AttributeValueMemberS{Value: ""},
		":now":   &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	if org.AccountOrganizationID == "" {
		expr = "SET owner_user_id = :o, lookup_pk = :l, updated_at = :now"
		cond += " AND attribute_not_exists(account_organization_id)"
		values[":l"] = &types.AttributeValueMemberS{Value: LookupOrganizationOwnerPK(org.Livemode, ownerUserID)}
	}
	// An API-only tenant stores the owner as an empty string, not as absent.
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		expr, cond, nil, values,
	)
```

`api/internal/repositories/rows.go:38-41` — replace the `LookupPK` comment:

```go
	// LookupPK resolves the tenant for the console session: the account
	// organization it is linked to (LookupOrganizationAccountPK, ADR 0028), or,
	// on rows written before that and not yet backfilled, its owner
	// (LookupOrganizationOwnerPK, ADR 0011). Sparse: a tenant with neither is
	// reachable only through M2M credentials, which is the safe direction.
```

- [ ] **Step 5: Run to verify they pass**

Run: `cd api && go test ./internal/repositories/ -race -run AccountOrganizationKey -v`
Expected: PASS.
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'AccountOrganization|TwoTenantsOnOneKey|SetOwnerNever|LinkingWrites|Provisioning' -v`
Expected: PASS, including the existing `TestProvisioningGivesAnOwnerlessTenantItsOwnerOnce` (its tenant is unlinked, so `GetByOwner` still finds it).

- [ ] **Step 6: Full suites and commit**

Run: `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race && make test-integration`
Expected: no gofmt output; every package `ok`; integration `PASS`.

```bash
git add api/internal/repositories/ api/tests/integration/organization_lookup_test.go api/tests/integration/repositories_test.go
git commit -m "feat(organizations): find the tenant by its account organization; Create and Link write the key"
```

---

### Task 4: The merchant chain

**Files:**
- Create: `api/internal/middleware/merchant.go`
- Modify: `api/internal/problem/problem.go` (const block)
- Test: `api/internal/middleware/merchant_test.go` (new)

**Interfaces:**
- Consumes: `space.ResolvedSpace.Role()`, `space.IsMerchantRole` (Task 1); `repositories.ErrNotFound`; `middleware.GetSpace`, `GetClaims`, `spaceNotFound`, `OrganizationKey`, `TenantKey`, `Tenant`.
- Produces: everything in the "middleware/merchant.go" block of **Interfaces for parts 2 and 3**, plus `problem.TypeNoMerchant`.

- [ ] **Step 1: Write the failing tests** — `api/internal/middleware/merchant_test.go` (reuses `members`, `call`, `orgA`, `orgB` from `space_test.go`; extend `members` as shown first)

In `api/internal/middleware/space_test.go`, give the fake a kind table (no other change):

```go
type members struct {
	calls int
	m     map[string]string // "org|user" -> role
	kinds map[string]string // "org|user" -> kind; absent = "" (organization)
	err   error
}

// Membership answers organizations unless kinds says otherwise.
func (f *members) Membership(_ context.Context, org, user string) (accountorgs.Membership, error) {
	f.calls++
	if f.err != nil {
		return accountorgs.Membership{}, f.err
	}
	role, ok := f.m[org+"|"+user]
	return accountorgs.Membership{Member: ok, Role: role, Kind: f.kinds[org+"|"+user]}, nil
}
```

`api/internal/middleware/merchant_test.go`:

```go
package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// tenants answers by "account org|mode"; calls proves "no table read".
type tenants struct {
	byAccount map[string]*billing.Organization
	err       error
	calls     int
}

func (f *tenants) GetByAccountOrganization(_ context.Context, acct string, live bool) (*billing.Organization, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if o, ok := f.byAccount[acct+"|"+repositories.Mode(live)]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

// legacyTenants also answers the pre-ADR-0028 owner lookup ("user|mode").
type legacyTenants struct {
	*tenants
	byOwner map[string]*billing.Organization
}

func (f *legacyTenants) GetByOwner(_ context.Context, user string, live bool) (*billing.Organization, error) {
	if o, ok := f.byOwner[user+"|"+repositories.Mode(live)]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

var tenantA = &billing.Organization{ID: "org_a", Livemode: true, AccountOrganizationID: orgA}

// merchantApp mounts the production chain in front of a handler that echoes
// what it resolved. sid "" makes a service token.
func merchantApp(src *members, ts MerchantTenants, sub, sid string) (*fiber.App, *bool) {
	ran := false
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, &Claims{Sub: sub, SID: sid, Scope: ScopeInvoicesRead})
		return c.Next()
	})
	r := space.NewResolver(src, cache.NewMemoryBackend(100))
	app.Get("/console/invoices",
		RequireUserSession(), ResolveSpace(r), ResolveMerchantTenant(ts),
		func(c fiber.Ctx) error {
			ran = true
			a := GetMerchantAccess(c)
			return c.SendString(GetTenant(c).OrganizationID + "|" + GetOrganization(c).ID + "|" + a.AccountOrganizationID + "|" + a.Role + "|" + GetSpace(c).Role())
		})
	return app, &ran
}

func get(t *testing.T, app *fiber.App, mode, sel string) (int, string) {
	t.Helper()
	return call(t, app, "GET", "/console/invoices", mode, sel)
}

func TestOwnersAndAdminsReachTheTenantOfTheSelectedOrganization(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		src := &members{m: map[string]string{orgA + "|alice": role}}
		ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
		app, ran := merchantApp(src, ts, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if want := "org_a|org_a|" + orgA + "|" + role + "|" + role; code != 200 || body != want || !*ran {
			t.Fatalf("%s: %d %q, want 200 %q", role, code, body, want)
		}
	}
}

func TestMembersAndViewersAreRefusedBeforeAnyTableRead(t *testing.T) {
	for _, role := range []string{"member", "viewer"} {
		src := &members{m: map[string]string{orgA + "|alice": role}}
		ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
		app, ran := merchantApp(src, ts, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if code != 403 || !strings.Contains(body, `"merchant_role_required"`) || *ran || ts.calls != 0 {
			t.Fatalf("%s: %d %q ran=%v reads=%d", role, code, body, *ran, ts.calls)
		}
	}
}

// personal, a personal workspace, another organization and an invented one:
// one 404, byte for byte, and no tenant read.
func TestEveryNonMerchantSpaceIsTheSame404(t *testing.T) {
	src := &members{
		m:     map[string]string{orgB + "|bob": "owner", orgB + "|alice": "owner"},
		kinds: map[string]string{orgB + "|alice": "personal"},
	}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	var bodies []string
	for _, sel := range []string{
		"personal",
		"org:" + orgB,                           // alice's personal workspace
		"org:" + orgA,                           // an organization alice is not in (it has a tenant)
		"org:0190a1b2-c3d4-7e5f-8a9b-aaaaaaaaaaaa", // invented
	} {
		code, body := get(t, app, "live", sel)
		if code != 404 || !strings.Contains(body, "/problems/space-not-found") {
			t.Fatalf("%s: %d %q", sel, code, body)
		}
		bodies = append(bodies, body)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("refusals differ:\n%s\n%s", bodies[0], b)
		}
	}
	if *ran || ts.calls != 0 {
		t.Fatalf("ran=%v reads=%d", *ran, ts.calls)
	}
}

func TestAnOrganizationWithoutATenantInThisModeIsNoMerchant(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "owner"}}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	code, body := get(t, app, "test", "org:"+orgA) // live tenant only
	if code != 404 || !strings.Contains(body, `"no_merchant"`) || !strings.Contains(body, "/problems/no-merchant") || *ran {
		t.Fatalf("%d %q ran=%v", code, body, *ran)
	}
}

func TestServiceTokensNeverReachTheResolver(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|cli_x": "owner"}}
	ts := &tenants{}
	app, ran := merchantApp(src, ts, "cli_x", "")
	code, body := get(t, app, "live", "org:"+orgA)
	if code != 403 || !strings.Contains(body, `"user_session_required"`) || *ran || src.calls != 0 || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v membership=%d reads=%d", code, body, *ran, src.calls, ts.calls)
	}
}

func TestMembershipOutageIs503AndReadsNothing(t *testing.T) {
	src := &members{err: errors.New("timeout")}
	ts := &tenants{}
	app, ran := merchantApp(src, ts, "alice", "sess")
	code, body := get(t, app, "live", "org:"+orgA)
	if code != 503 || !strings.Contains(body, "/problems/space-unavailable") || *ran || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v reads=%d", code, body, *ran, ts.calls)
	}
}

func TestMissingHeadersAre400(t *testing.T) {
	app, ran := merchantApp(&members{}, &tenants{}, "alice", "sess")
	for _, c := range []struct{ mode, sel string }{{"", "org:" + orgA}, {"live", ""}, {"prod", "org:" + orgA}} {
		if code, _ := get(t, app, c.mode, c.sel); code != 400 {
			t.Errorf("mode %q selector %q: %d, want 400", c.mode, c.sel, code)
		}
	}
	if *ran {
		t.Fatal("the handler ran")
	}
}

func TestATenantReadFailureIs500NotAnAnswer(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "owner"}}
	for _, err := range []error{errors.New("throttled"), repositories.ErrAccountOrganizationAmbiguous} {
		app, ran := merchantApp(src, &tenants{err: err}, "alice", "sess")
		code, body := get(t, app, "live", "org:"+orgA)
		if code != 500 || !strings.Contains(body, `"organization_resolve_failed"`) || *ran {
			t.Fatalf("%v: %d %q", err, code, body)
		}
	}
}

// A cached "not a member" is a plain 404 for its 60 s, never a 503 or 500.
func TestACachedRefusalIsAPlain404(t *testing.T) {
	src := &members{m: map[string]string{}}
	app, _ := merchantApp(src, &tenants{}, "alice", "sess")
	for i := 0; i < 2; i++ {
		if code, _ := get(t, app, "live", "org:"+orgA); code != 404 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if src.calls != 1 {
		t.Fatalf("membership asked %d times, want 1 (cached)", src.calls)
	}
}

// The fallback (until Task 11): a not-yet-backfilled tenant is reachable by its
// stored owner only when it is linked to the selected organization.
func TestTheLegacyOwnerKeyReachesOnlyALinkedTenant(t *testing.T) {
	unlinked := &billing.Organization{ID: "org_u", Livemode: true}
	elsewhere := &billing.Organization{ID: "org_e", Livemode: true, AccountOrganizationID: orgB}
	for _, c := range []struct {
		name   string
		legacy *billing.Organization
		want   int
	}{
		{"linked to the selected organization", tenantA, 200},
		{"linked to another organization", elsewhere, 404},
		{"not linked at all", unlinked, 404},
	} {
		src := &members{m: map[string]string{orgA + "|alice": "owner"}}
		ts := &legacyTenants{tenants: &tenants{}, byOwner: map[string]*billing.Organization{"alice|live": c.legacy}}
		app, _ := merchantApp(src, ts, "alice", "sess")
		if code, body := get(t, app, "live", "org:"+orgA); code != c.want {
			t.Fatalf("%s: %d %q, want %d", c.name, code, body, c.want)
		}
	}
}

func TestTheLegacyFallbackNeverWidensTheRole(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "member"}}
	ts := &legacyTenants{tenants: &tenants{}, byOwner: map[string]*billing.Organization{"alice|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	if code, body := get(t, app, "live", "org:"+orgA); code != 403 || *ran {
		t.Fatalf("%d %q ran=%v", code, body, *ran)
	}
}

func TestRequireMerchantRoleWithoutASpaceIsAServerFault(t *testing.T) {
	app := fiber.New()
	app.Get("/x", RequireMerchantRole(), func(c fiber.Ctx) error { return c.SendString("ran") })
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status %d: a chain that forgot ResolveSpace must fail closed", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/middleware/ -run 'Merchant|NonMerchant|NoMerchant|ServiceTokensNever|MembershipOutage|MissingHeaders|TenantReadFailure|CachedRefusal|Legacy' -v`
Expected: FAIL — `undefined: RequireUserSession`, `undefined: ResolveMerchantTenant`, `undefined: MerchantTenants`.

- [ ] **Step 3: Implement**

`api/internal/problem/problem.go`, inside the const block after `TypeSpaceUnavailable`:

```go
	// TypeNoMerchant is an organization the person may administer that has no
	// billing tenant in the requested mode. The console renders it as the
	// invitation to bill through CTech Billing (spec § 7.1), so a client
	// branches on it; it says nothing a member of the organization does not
	// already know, and only members get this far.
	TypeNoMerchant = "/problems/no-merchant"
```

`api/internal/middleware/merchant.go`:

```go
package middleware

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// MerchantAccessKey is the Fiber locals key of the MerchantAccess.
const MerchantAccessKey = "merchant_access"

// MerchantAccess is who reached the billing half of the console and as what
// (ADR 0028). It is set by RequireMerchantRole and ResolveMerchantTenant, from
// the resolved space only, never from the request.
type MerchantAccess struct {
	// AccountOrganizationID is the selected ctech-account organization.
	AccountOrganizationID string
	// Role is "owner" or "admin": nothing else gets this far.
	Role string
	// Livemode is the mode the request asked for (X-Billing-Mode).
	Livemode bool
}

// GetMerchantAccess returns the request's MerchantAccess; the zero value when
// neither middleware ran.
func GetMerchantAccess(c fiber.Ctx) MerchantAccess {
	a, _ := c.Locals(MerchantAccessKey).(MerchantAccess)
	return a
}

// MerchantTenants is the one read the billing half makes before it knows the
// tenant (OrganizationRepository satisfies it).
type MerchantTenants interface {
	GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)
}

// RequireUserSession refuses a request that is not a person's: no claims is a
// 401, a service token (no sid) a 403. It runs before ResolveSpace so an M2M
// client id is never sent to ctech-account as if it were a person.
func RequireUserSession() fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
		}
		return c.Next()
	}
}

func merchantRoleRequired() *problem.Problem {
	return problem.Forbidden("only an owner or admin of the organization reaches billing").WithCode("merchant_role_required")
}

func noMerchant() *problem.Problem {
	return problem.New(fiber.StatusNotFound, problem.TypeNoMerchant, "No merchant",
		"this organization does not bill through CTech Billing in this mode").WithCode("no_merchant")
}

// merchantAccessOf turns the resolved space into a MerchantAccess, or the
// refusal. Only an organization space held as owner or admin is a merchant
// (ADR 0028); personal and personal workspaces get the space-not-found body
// byte for byte, so the answer does not say what kind of space was named.
func merchantAccessOf(c fiber.Ctx) (MerchantAccess, *problem.Problem) {
	sp := GetSpace(c)
	if sp.IsZero() {
		// A chain without ResolveSpace in front: a wiring bug, refused.
		slog.Error("console: merchant chain ran without a resolved space", "path", c.Path())
		return MerchantAccess{}, problem.Internal("could not resolve organization").WithCode("organization_resolve_failed")
	}
	if sp.Personal() || sp.Kind() != space.KindOrganization {
		return MerchantAccess{}, spaceNotFound()
	}
	if !space.IsMerchantRole(sp.Kind(), sp.Role()) {
		return MerchantAccess{}, merchantRoleRequired()
	}
	return MerchantAccess{AccountOrganizationID: sp.OrganizationID(), Role: sp.Role(), Livemode: sp.Livemode()}, nil
}

// RequireMerchantRole gates a route on being an owner or admin of the selected
// organization, without needing a tenant (part 2's application route). It
// runs after ResolveSpace.
func RequireMerchantRole() fiber.Handler {
	return func(c fiber.Ctx) error {
		acc, p := merchantAccessOf(c)
		if p != nil {
			return p.Send(c)
		}
		c.Locals(MerchantAccessKey, acc)
		return c.Next()
	}
}

// ResolveMerchantTenant turns a resolved organization space into the tenant the
// request acts for (ADR 0028, replacing ResolveConsoleTenant). It runs after
// ResolveSpace, which has already checked membership before any table read,
// and sets the locals every console handler reads: OrganizationKey, TenantKey,
// and MerchantAccessKey.
//
// Order of answers: not an organization space → 404 space-not-found; not owner
// or admin → 403 merchant_role_required (no table read); no tenant in this mode
// → 404 no_merchant; a read failure → 500. The mode can be either: both mode
// partitions of a tenant belong to the same organization, so the header cannot
// widen access.
func ResolveMerchantTenant(src MerchantTenants) fiber.Handler {
	return func(c fiber.Ctx) error {
		acc, p := merchantAccessOf(c)
		if p != nil {
			return p.Send(c)
		}
		sub := ""
		if cl := GetClaims(c); cl != nil {
			sub = cl.Sub
		}
		org, err := LookupMerchantTenant(c.Context(), src, sub, acc.AccountOrganizationID, acc.Livemode)
		switch {
		case err == nil:
		case errors.Is(err, repositories.ErrNotFound):
			return noMerchant().Send(c)
		default:
			slog.Error("console: resolving the merchant tenant failed",
				"account_organization", acc.AccountOrganizationID, "mode", repositories.Mode(acc.Livemode), "error", err)
			return problem.Internal("could not resolve organization").WithCode("organization_resolve_failed").Send(c)
		}
		c.Locals(MerchantAccessKey, acc)
		c.Locals(OrganizationKey, org)
		c.Locals(TenantKey, Tenant{OrganizationID: org.ID, Livemode: org.Livemode})
		return c.Next()
	}
}

// legacyOwnerTenants is the pre-ADR-0028 lookup by stored owner. It is
// consulted only while tenants still carry the {mode}#OWNER# key, and is
// deleted with GetByOwner once the backfill is confirmed (plan Task 11).
type legacyOwnerTenants interface {
	GetByOwner(ctx context.Context, userID string, livemode bool) (*billing.Organization, error)
}

// LookupMerchantTenant reads the tenant of an account organization in one
// mode. The caller has already established that sub is an owner or admin of
// accountOrgID (or, for the session, listed it from ctech-account's answer).
//
// Until the backfill runs, a tenant that still carries only the owner key is
// accepted through GetByOwner(sub) — and only when it is linked to this very
// organization in this mode. The fallback never decides the role and never
// reaches an unlinked tenant.
func LookupMerchantTenant(ctx context.Context, src MerchantTenants, sub, accountOrgID string, livemode bool) (*billing.Organization, error) {
	org, err := src.GetByAccountOrganization(ctx, accountOrgID, livemode)
	if err == nil || !errors.Is(err, repositories.ErrNotFound) {
		return org, err
	}
	legacy, ok := src.(legacyOwnerTenants)
	if !ok || sub == "" {
		return nil, err
	}
	old, lerr := legacy.GetByOwner(ctx, sub, livemode)
	switch {
	case errors.Is(lerr, repositories.ErrNotFound):
		return nil, err
	case lerr != nil:
		return nil, lerr
	case old.AccountOrganizationID != accountOrgID || old.Livemode != livemode:
		return nil, err
	}
	slog.Info("console: tenant reached through the legacy owner key; run backfill-organization-lookup",
		"tenant", old.ID, "mode", repositories.Mode(livemode))
	return old, nil
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `cd api && go test ./internal/middleware/ -race -v`
Expected: PASS (the new tests and every existing one in the package, `space_test.go` included).

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware/merchant.go api/internal/middleware/merchant_test.go api/internal/middleware/space_test.go api/internal/problem/problem.go
git commit -m "feat(middleware): merchant chain: role from membership, tenant by account organization"
```

---

### Task 5: Switch the billing half to the merchant chain

**Files:**
- Modify: `api/internal/api/v1/router.go` (comment above `registerFinance`, `registerConsole`)
- Modify: `api/internal/middleware/console.go` (delete `ResolveConsoleTenant`; keep `ModeHeader`, `RequireUserScope`, `GetOrganization`)
- Create: `api/tests/integration/fake_account_test.go` (moved from `finance_shared_spaces_test.go:28-92`, plus `name`)
- Modify: `api/tests/integration/finance_shared_spaces_test.go` (`newSpacesEnv`)
- Modify: `api/tests/integration/api_test.go` (`apiEnv`, `newAPIWith`)
- Modify: `api/tests/integration/console_test.go` (helpers; rewrite four tests)
- Modify: `api/tests/integration/console_writes_test.go` (`consoleWrite`)
- Create: `api/tests/integration/console_access_test.go`

**Interfaces:**
- Consumes: `middleware.RequireUserSession`, `ResolveSpace`, `ResolveMerchantTenant` (Task 4); `newLinkedOrg`, `newSpaceOrgID`.
- Produces: the console group chain shown in **Interfaces for parts 2 and 3**; test helpers `(*apiEnv).consoleAs(t, method, path, token, mode, selector, body string) apiResponse`, fields `apiEnv.account *fakeAccount`, `apiEnv.accountOrg string`, `(*fakeAccount).name(ws, displayName string)`, `billingHalfRoutes(t, *fiber.App) []fiber.Route`, `allConsoleScopes []string`.

- [ ] **Step 1: Move the fake ctech-account and link the env's tenant**

Create `api/tests/integration/fake_account_test.go` with the `fakeAccount` type and its methods `count`, `set`, `serve` moved **verbatim** from `finance_shared_spaces_test.go` (delete them there; keep that file's imports compiling), with this file header and one new method:

```go
//go:build integration

package integration

// fakeAccount is the ctech-account every apiEnv talks to: membership and the
// workspace list from a table, in the shapes ctech-account PR #46 implemented.

// name sets a workspace's display name, as the list route returns it.
func (a *fakeAccount) name(ws, displayName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.names[ws] = displayName
}
```

(imports: `encoding/json`, `net/http`, `net/http/httptest`, `strings`, `sync`, `testing` — whatever the moved code uses.)

In `api/tests/integration/api_test.go`, add to `apiEnv`:

```go
	// account is the fake ctech-account this env's server asks, unless the
	// configure func pointed AccountBaseURL elsewhere. accountOrg is the
	// account organization e.org is linked to, with e.org.OwnerUserID as its
	// owner: the console reaches e.org through it (ADR 0028).
	account    *fakeAccount
	accountOrg string
```

In `newAPIWith`, before `if configure != nil`:

```go
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}}
	srv := acct.serve(t)
	cfg.AccountBaseURL, cfg.AccountTokenURL = srv.URL, srv.URL+"/token"
	cfg.AccountClientID, cfg.AccountClientSecret = "billing-test", "secret"
```

replace `org := newOrg(t, true)` with:

```go
	accountOrg := newSpaceOrgID()
	org := newLinkedOrg(t, true, accountOrg)
	acct.set(accountOrg, org.OwnerUserID, "organization", "owner")
	acct.name(accountOrg, org.DisplayName)
```

and the return with:

```go
	return &apiEnv{app: server, key: key, jwksURL: jwks, org: org, client: clientID, priceID: price.ID, account: acct, accountOrg: accountOrg}
```

In `finance_shared_spaces_test.go`, `newSpacesEnv` becomes:

```go
func newSpacesEnv(t *testing.T) spacesEnv {
	t.Helper()
	e := newAPI(t)
	return spacesEnv{apiEnv: e, account: e.account}
}
```

(`newPlanEnv`, `TestTheChoiceCarriesTheCheckoutURL` and `newPortalSpaces` keep their own fake: their `configure` overrides the URL, and none of them calls a billing-half route.)

- [ ] **Step 2: Console helpers send the selector**

In `api/tests/integration/console_test.go`, replace `console` with:

```go
// console performs a GET on the console with a real signed session token, in
// e's organization space.
func (e *apiEnv) console(t *testing.T, path, token, mode string) apiResponse {
	t.Helper()
	return e.consoleAs(t, http.MethodGet, path, token, mode, "org:"+e.accountOrg, "")
}

// consoleAs is the one console request builder: any method, mode, selector
// ("" sends no X-Billing-Space) and JSON body.
func (e *apiEnv) consoleAs(t *testing.T, method, path, token, mode, selector, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if mode != "" {
		req.Header.Set(middleware.ModeHeader, mode)
	}
	if selector != "" {
		req.Header.Set(middleware.SpaceHeader, selector)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}
```

In `console_writes_test.go`, `consoleWrite` becomes:

```go
func (e *apiEnv) consoleWrite(t *testing.T, method, path, token, mode, body string) apiResponse {
	t.Helper()
	return e.consoleAs(t, method, path, token, mode, "org:"+e.accountOrg, body)
}
```

(remove imports that become unused there: `bytes`, `httptest`, `fiber`, `io` if nothing else uses them.)

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Console|Finance|Portal' 2>&1 | tail -5`
Expected: FAIL in exactly the console tests that read e.org through the owner — the old resolver cannot find a linked tenant (Task 3 writes the `ACCOUNT_ORG` key for it). This is the red bar the switch turns green.

- [ ] **Step 3: Write the spoofing tests** — `api/tests/integration/console_access_test.go`

```go
//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// Spec § 10.1: a member, a viewer, an admin of another organization, an
// invented org:{id} and personal each get 404/403 from every billing-half
// route, never data. The routes come from the running app's own table, so a
// route added later is covered without editing this file.

var allConsoleScopes = []string{
	middleware.ScopeOrganizationRead,
	middleware.ScopeInvoicesRead, middleware.ScopeInvoicesWrite,
	middleware.ScopeSubscriptionsRead, middleware.ScopeSubscriptionsWrite,
	middleware.ScopeCustomersRead, middleware.ScopeCustomersWrite,
	middleware.ScopeProductsRead, middleware.ScopeProductsWrite,
}

var routeParam = regexp.MustCompile(`:[A-Za-z_]+`)

// billingHalfRoutes is every /v1.0/console route outside finance and the
// session, with its parameters filled.
func billingHalfRoutes(t *testing.T, app *fiber.App) []fiber.Route {
	t.Helper()
	var out []fiber.Route
	for _, r := range app.GetRoutes(true) {
		if !strings.HasPrefix(r.Path, "/v1.0/console/") ||
			strings.HasPrefix(r.Path, "/v1.0/console/finance") ||
			r.Path == "/v1.0/console/session" {
			continue
		}
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
			r.Path = routeParam.ReplaceAllString(r.Path, "zz_"+id.New())
			out = append(out, r)
		}
	}
	if len(out) < 20 {
		t.Fatalf("found %d billing-half routes; the route table changed shape", len(out))
	}
	return out
}

func TestNoNonMerchantReachesAnyBillingHalfRoute(t *testing.T) {
	e := newAPI(t)
	member, viewer, otherAdmin := "usr_"+id.New(), "usr_"+id.New(), "usr_"+id.New()
	e.account.set(e.accountOrg, member, "organization", "member")
	e.account.set(e.accountOrg, viewer, "organization", "viewer")
	otherOrg := newSpaceOrgID() // has no tenant
	e.account.set(otherOrg, otherAdmin, "organization", "admin")
	personalWS := newSpaceOrgID()
	e.account.set(personalWS, e.org.OwnerUserID, "personal", "owner")
	owner := e.org.OwnerUserID

	cases := []struct {
		name, sub, selector string
		status              int
		code                string
	}{
		{"member", member, "org:" + e.accountOrg, 403, "merchant_role_required"},
		{"viewer", viewer, "org:" + e.accountOrg, 403, "merchant_role_required"},
		{"admin of another organization", otherAdmin, "org:" + e.accountOrg, 404, "space_not_found"},
		{"invented organization", owner, "org:" + newSpaceOrgID(), 404, "space_not_found"},
		{"personal", owner, "personal", 404, "space_not_found"},
		{"personal workspace", owner, "org:" + personalWS, 404, "space_not_found"},
		{"organization with no tenant", otherAdmin, "org:" + otherOrg, 404, "no_merchant"},
		{"owner in a mode with no tenant", owner, "org:" + e.accountOrg, 404, "no_merchant"},
	}
	routes := billingHalfRoutes(t, e.app)
	for _, c := range cases {
		token := e.token(t, c.sub, "sess_"+id.New(), allConsoleScopes...)
		mode := "live"
		if c.name == "owner in a mode with no tenant" {
			mode = "test" // e.org exists in live only
		}
		for _, r := range routes {
			res := e.consoleAs(t, r.Method, r.Path, token, mode, c.selector, `{}`)
			if res.status != c.status || !bytes.Contains(res.body, []byte(`"`+c.code+`"`)) {
				t.Errorf("%s: %s %s = %d %s, want %d %s", c.name, r.Method, r.Path, res.status, res.body, c.status, c.code)
			}
			if bytes.Contains(res.body, []byte(e.org.ID)) {
				t.Errorf("%s: %s %s leaked the tenant id", c.name, r.Method, r.Path)
			}
		}
	}
}

func TestServiceTokensAndMissingHeadersNeverReachTheBillingHalf(t *testing.T) {
	e := newAPI(t)
	m2m := e.token(t, e.client, "", allConsoleScopes...)
	owner := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), allConsoleScopes...)
	for _, r := range billingHalfRoutes(t, e.app) {
		if res := e.consoleAs(t, r.Method, r.Path, m2m, "live", "org:"+e.accountOrg, `{}`); res.status != 403 ||
			!bytes.Contains(res.body, []byte(`"user_session_required"`)) {
			t.Errorf("m2m %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
		if res := e.consoleAs(t, r.Method, r.Path, owner, "live", "", `{}`); res.status != 400 {
			t.Errorf("no selector %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
		if res := e.consoleAs(t, r.Method, r.Path, owner, "", "org:"+e.accountOrg, `{}`); res.status != 400 {
			t.Errorf("no mode %s %s = %d %s", r.Method, r.Path, res.status, res.body)
		}
	}
}

// An admin who is not the stored owner reaches the tenant: the role decides.
func TestAnAdminWhoIsNotTheStoredOwnerReachesTheTenant(t *testing.T) {
	e := newAPI(t)
	admin := "usr_" + id.New()
	e.account.set(e.accountOrg, admin, "organization", "admin")
	res := e.console(t, "/v1.0/console/settings", e.token(t, admin, "sess_"+id.New(), middleware.ScopeOrganizationRead), "live")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(e.org.ID)) {
		t.Fatalf("admin: %d %s", res.status, res.body)
	}
}
```

- [ ] **Step 4: Rewrite the four console tests that asserted the owner model** (`api/tests/integration/console_test.go`)

Replace `TestConsoleResolvesTheOrganizationFromTheOwner`, `TestConsoleRequiresTheModeHeader`, `TestConsoleCannotReachAModeItDoesNotOwn` and `TestConsoleCannotReachAnotherTenant` with:

```go
// The tenant is the selected organization's (ADR 0028); /settings carries its
// block, which is the console's "where am I".
func TestConsoleResolvesTheTenantOfTheSelectedOrganization(t *testing.T) {
	e := newAPI(t)
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Organization struct {
			OrganizationID string `json:"organization_id"`
			Livemode       bool   `json:"livemode"`
			CanCharge      bool   `json:"can_charge"`
		} `json:"organization"`
	}
	res.decode(t, &body)
	if body.Organization.OrganizationID != e.org.ID || !body.Organization.Livemode || !body.Organization.CanCharge {
		t.Fatalf("organization = %+v, want %s live and chargeable", body.Organization, e.org.ID)
	}
}

// The mode is required rather than defaulted (ADR 0011, unchanged).
func TestConsoleRequiresTheModeHeader(t *testing.T) {
	e := newAPI(t)
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "")
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// A mode the organization has no tenant in is no_merchant: the mode is
// caller-controlled and still cannot widen access.
func TestConsoleCannotReachAModeWithNoTenant(t *testing.T) {
	e := newAPI(t) // live only
	res := e.console(t, "/v1.0/console/settings", e.sessionToken(t, middleware.ScopeOrganizationRead), "test")
	if res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// The owner of another merchant reaches their own tenant and never this one.
func TestConsoleCannotReachAnotherTenant(t *testing.T) {
	e := newAPI(t)
	otherAcct := newSpaceOrgID()
	other := newLinkedOrg(t, true, otherAcct)
	e.account.set(otherAcct, other.OwnerUserID, "organization", "owner")
	stranger := e.token(t, other.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead)

	if res := e.console(t, "/v1.0/console/settings", stranger, "live"); res.status != http.StatusNotFound {
		t.Fatalf("someone else's organization: %d %s", res.status, res.body)
	}
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/settings", stranger, "live", "org:"+otherAcct, "")
	if res.status != http.StatusOK || !strings.Contains(string(res.body), other.ID) || strings.Contains(string(res.body), e.org.ID) {
		t.Fatalf("own organization: %d %s", res.status, res.body)
	}
}
```

Keep `TestServiceTokensAreRejectedOnConsoleRoutes` and `TestConsoleScopesAreEnforcedPerRoute` as they are (`/console/session` in the first is still behind the group in this task; it answers 403 `user_session_required` either way).

- [ ] **Step 5: Switch the router**

`api/internal/api/v1/router.go` — replace the line `console := v1.Group("/console", auth, middleware.ResolveConsoleTenant(d.Organizations))` with:

```go
	// The billing (merchant) half resolves its tenant from the selected
	// organization (ADR 0028): a person's session, the space and its
	// membership (checked before any table read, the same resolver finance
	// uses), then the owner/admin role and the tenant linked to that
	// organization in the requested mode.
	spaces := d.Spaces
	if spaces == nil {
		// No resolver configured: every organization selector fails closed (503).
		spaces = space.NewResolver(nil, nil)
	}
	console := v1.Group("/console", auth,
		middleware.RequireUserSession(),
		middleware.ResolveSpace(spaces),
		middleware.ResolveMerchantTenant(d.Organizations))
```

Update the doc comment of `registerConsole`: replace "these routes resolve the organization from the signed-in owner" with "these routes resolve the tenant from the selected organization and the person's role in it (ADR 0028)". Update the comment above `registerFinance(...)`: replace "the "/console" group's ResolveConsoleTenant (one organization per owner, mode required)" with "the "/console" group's merchant chain (space, role, tenant; mode required)". Add the `space` import if `router.go` does not have it.

In `api/internal/middleware/console.go`, delete `ResolveConsoleTenant` and its doc comment; remove the now-unused `errors` and `repositories` imports.

- [ ] **Step 6: Run to verify everything passes**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race`
Expected: no gofmt output; all `ok`.
Run: `cd api && make test-integration 2>&1 | tail -20`
Expected: `PASS`. If a test that counts Finanças postings now sees an issuer posting for `e.accountOrg` (the env's tenant is linked, spec § 3.8 posts a linked tenant's paid invoices), adjust **that test's expectation** to the linked tenant, not the harness.

- [ ] **Step 7: Commit**

```bash
git add api/internal/api/v1/router.go api/internal/middleware/console.go api/tests/integration/
git commit -m "feat(console): billing half resolves its tenant from the selected organization and role"
```

---

### Task 6: `GET /console/session` lists the person's merchant organizations; `/v1/me` by membership

**Files:**
- Modify: `api/internal/api/v1/console_dto.go:29-35` (`sessionResponse` → `tenantResponse`; new session DTOs)
- Modify: `api/internal/api/v1/console_settings.go:23,104` (`sessionResponse` → `tenantResponse`)
- Modify: `api/internal/api/v1/console.go` (`consoleHandlers` fields; `session`; `merchantStatus`)
- Modify: `api/internal/api/v1/me.go`
- Modify: `api/internal/api/v1/router.go` (`registerConsole`: handler fields, session route before the group)
- Test: `api/internal/api/v1/console_session_test.go` (new)
- Modify: `api/tests/integration/console_access_test.go` (append), `api/tests/integration/console_test.go` (`TestServiceTokensAreRejectedOnConsoleRoutes`)

**Interfaces:**
- Consumes: `middleware.LookupMerchantTenant`, `MerchantTenants` (Task 4); `billing.MerchantStatusOf` (Task 2); `space.IsMerchantRole`, `space.IsOrganizationID`, `space.WorkspaceKind`; `spaceLister` (`finance_spaces.go`).
- Produces: `consoleSessionResponse`, `merchantOrganizationDTO` (wire shape in **Interfaces for parts 2 and 3**); `(*consoleHandlers).merchantStatus(ctx, sub, accountOrgID string) (billing.MerchantStatus, error)` — the one place part 2 extends to compute `rejected`/`suspended`; `tenantResponse` (old session body).

- [ ] **Step 1: Write the failing unit tests** — `api/internal/api/v1/console_session_test.go`

```go
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/accountorgs"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const (
	acctA = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	acctB = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
	acctC = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"
)

// fakeMerchants answers by "account org|mode".
type fakeMerchants map[string]*billing.Organization

func (f fakeMerchants) GetByAccountOrganization(_ context.Context, acct string, live bool) (*billing.Organization, error) {
	if o, ok := f[acct+"|"+repositories.Mode(live)]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

type sessionBody struct {
	Organizations []struct {
		OrganizationID string `json:"organization_id"`
		Selector       string `json:"selector"`
		DisplayName    string `json:"display_name"`
		Role           string `json:"role"`
		MerchantStatus string `json:"merchant_status"`
	} `json:"organizations"`
	OrganizationsUnavailable bool `json:"organizations_unavailable"`
}

func sessionApp(h *consoleHandlers, claims *middleware.Claims) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals(middleware.ClaimsKey, claims)
		}
		return c.Next()
	})
	app.Get("/console/session", middleware.RequireUserScope(middleware.ScopeOrganizationRead), h.session)
	return app
}

func getSession(t *testing.T, app *fiber.App) (int, string, sessionBody) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/console/session", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var b sessionBody
	_ = json.Unmarshal(raw, &b)
	return resp.StatusCode, string(raw), b
}

var sessionClaims = &middleware.Claims{Sub: "alice", SID: "s", Scope: middleware.ScopeOrganizationRead}

func TestSessionListsOwnerAndAdminOrganizationsWithTheirMerchantStatus(t *testing.T) {
	l := &fakeLister{orgs: map[string][]accountorgs.Organization{"alice": {
		{ID: acctC, DisplayName: "zeta", Role: "admin", Kind: "organization"},
		{ID: acctA, DisplayName: "CTech", Role: "owner", Kind: "organization"},
		{ID: acctB, DisplayName: "beta", Role: "owner"}, // no kind: an organization
		{ID: "0190a1b2-c3d4-7e5f-8a9b-dddddddddddd", DisplayName: "Member here", Role: "member", Kind: "organization"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-eeeeeeeeeeee", DisplayName: "Viewer here", Role: "viewer", Kind: "organization"},
		{ID: "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff", DisplayName: "Casa", Role: "owner", Kind: "personal"},
		{ID: "USER#bob", DisplayName: "Bad id", Role: "owner", Kind: "organization"},
	}}}
	m := fakeMerchants{
		acctA + "|live": {ID: "ctech", Livemode: true}, acctA + "|test": {ID: "ctech"},
		acctB + "|test": {ID: "org_b"},
	}
	code, raw, b := getSession(t, sessionApp(&consoleHandlers{spaces: l, merchants: m}, sessionClaims))
	if code != 200 || b.OrganizationsUnavailable {
		t.Fatalf("%d %s", code, raw)
	}
	got := []string{}
	for _, o := range b.Organizations {
		got = append(got, o.DisplayName+"/"+o.Role+"/"+o.MerchantStatus+"/"+o.Selector)
	}
	want := []string{
		"beta/owner/test/org:" + acctB,
		"CTech/owner/live/org:" + acctA,
		"zeta/admin/none/org:" + acctC,
	}
	if len(got) != len(want) {
		t.Fatalf("organizations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("organizations = %v, want %v", got, want)
		}
	}
	if len(l.asked) != 1 || l.asked[0] != "alice" {
		t.Fatalf("asked ctech-account about %v, want only the subject", l.asked)
	}
}

func TestSessionWithNoOrganizationsIsAnEmptyListNotNull(t *testing.T) {
	_, raw, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{}, merchants: fakeMerchants{}}, sessionClaims))
	if raw != `{"organizations":[],"organizations_unavailable":false}` {
		t.Fatalf("body = %s", raw)
	}
}

func TestSessionSaysOrganizationsAreUnavailableWhenCtechAccountIsDown(t *testing.T) {
	code, raw, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{err: errors.New("timeout")}, merchants: fakeMerchants{}}, sessionClaims))
	if code != 200 || raw != `{"organizations":[],"organizations_unavailable":true}` {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestSessionRefusesServiceTokens(t *testing.T) {
	code, _, _ := getSession(t, sessionApp(&consoleHandlers{spaces: &fakeLister{}, merchants: fakeMerchants{}},
		&middleware.Claims{Sub: "cli", Scope: middleware.ScopeOrganizationRead}))
	if code != 403 {
		t.Fatalf("status %d", code)
	}
}

// The wire shape parts 2 and 3 and the console read.
func TestMerchantOrganizationWireShape(t *testing.T) {
	raw, err := json.Marshal(consoleSessionResponse{Organizations: []merchantOrganizationDTO{{
		OrganizationID: acctA, Selector: "org:" + acctA, DisplayName: "CTech", Role: "owner", MerchantStatus: billing.MerchantLive,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"organizations":[{"organization_id":"` + acctA + `","selector":"org:` + acctA +
		`","display_name":"CTech","role":"owner","merchant_status":"live"}],"organizations_unavailable":false}`
	if string(raw) != want {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/api/v1/ -run 'Session|MerchantOrganizationWireShape' -v`
Expected: FAIL — `unknown field spaces in struct literal of type consoleHandlers`, `undefined: consoleSessionResponse`.

- [ ] **Step 3: Implement**

`api/internal/api/v1/console_dto.go` — rename the type `sessionResponse` to `tenantResponse` (doc comment: "tenantResponse is the selected tenant's own block: id, name, mode and the payout gate. It was the body of /console/session before ADR 0028 and is now the `organization` of /console/settings.") and add:

```go
// consoleSessionResponse is GET /v1.0/console/session (spec § 3, ADR 0028):
// the organizations this person may run billing for, with what billing knows
// about each. Display data: every billing-half request re-authorizes.
type consoleSessionResponse struct {
	// Never null: an empty list is "owner or admin of nothing".
	Organizations []merchantOrganizationDTO `json:"organizations"`
	// ctech-account did not answer; the list is empty because it is unknown.
	OrganizationsUnavailable bool `json:"organizations_unavailable"`
}

// merchantOrganizationDTO is one entry of the console's organization selector.
type merchantOrganizationDTO struct {
	OrganizationID string `json:"organization_id"`
	// Selector is what X-Billing-Space carries for it; the console never
	// builds one from anything else.
	Selector       string                 `json:"selector"`
	DisplayName    string                 `json:"display_name"`
	Role           string                 `json:"role"`
	MerchantStatus billing.MerchantStatus `json:"merchant_status"`
}
```

`api/internal/api/v1/console_settings.go` — `Organization sessionResponse` → `Organization tenantResponse`, and `sessionResponse{` → `tenantResponse{`.

`api/internal/api/v1/console.go` — add to `consoleHandlers`:

```go
	// spaces lists the person's ctech-account organizations (session, /me).
	spaces spaceLister
	// merchants reads a tenant by its account organization (session, /me).
	merchants middleware.MerchantTenants
```

replace `session` with:

```go
// session answers "which organizations can I run billing for, and where does
// each stand" (spec § 3). It runs before any organization is chosen, so it is
// mounted outside the merchant chain; the person asked about is always the
// token's subject.
func (h *consoleHandlers) session(c fiber.Ctx) error {
	sub := middleware.GetClaims(c).Sub
	out := consoleSessionResponse{Organizations: []merchantOrganizationDTO{}}
	if h.spaces == nil {
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	workspaces, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		slog.Warn("console: listing organizations failed", "error", err)
		out.OrganizationsUnavailable = true
		return c.JSON(out)
	}
	for _, w := range workspaces {
		if !space.IsOrganizationID(w.ID) || !space.IsMerchantRole(space.WorkspaceKind(w.Kind), w.Role) {
			continue
		}
		status, err := h.merchantStatus(c.Context(), sub, w.ID)
		if err != nil {
			return fail(c, err)
		}
		out.Organizations = append(out.Organizations, merchantOrganizationDTO{
			OrganizationID: w.ID, Selector: "org:" + w.ID, DisplayName: w.DisplayName, Role: w.Role, MerchantStatus: status,
		})
	}
	slices.SortStableFunc(out.Organizations, func(a, b merchantOrganizationDTO) int {
		if c := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); c != 0 {
			return c
		}
		return strings.Compare(a.Selector, b.Selector)
	})
	return c.JSON(out)
}

// merchantStatus is where an account organization stands as a merchant, from
// its tenant rows in both modes. Part 2 adds the application and suspension.
func (h *consoleHandlers) merchantStatus(ctx context.Context, sub, accountOrgID string) (billing.MerchantStatus, error) {
	var rows [2]*billing.Organization // test, live
	for i, live := range []bool{false, true} {
		org, err := middleware.LookupMerchantTenant(ctx, h.merchants, sub, accountOrgID, live)
		switch {
		case err == nil:
			rows[i] = org
		case !errors.Is(err, repositories.ErrNotFound):
			return "", err
		}
	}
	return billing.MerchantStatusOf(rows[0], rows[1]), nil
}
```

(imports as needed: `context`, `errors`, `log/slog`, `slices`, `strings`, `billing`, `space`.)

`api/internal/api/v1/me.go` — replace the `GetByOwner` block with:

```go
	org, err := h.consoleIdentity(c.Context(), cl.Sub)
	if err != nil {
		return fail(c, err)
	}
	if org != nil {
		out.Console = &meIdentity{ID: org.ID, Name: org.DisplayName}
	}
```

and append:

```go
// consoleIdentity is the tenant this person would open the console on: the
// first organization, by name, where they are owner or admin and a live tenant
// exists (ADR 0028). ctech-account not answering is "no console identity", not
// an error: /me is a probe.
func (h *consoleHandlers) consoleIdentity(ctx context.Context, sub string) (*billing.Organization, error) {
	if h.spaces == nil {
		return nil, nil
	}
	workspaces, err := h.spaces.Organizations(ctx, sub)
	if err != nil {
		slog.Warn("me: listing organizations failed", "error", err)
		return nil, nil
	}
	ws := slices.Clone(workspaces)
	slices.SortStableFunc(ws, func(a, b accountorgs.Organization) int {
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	for _, w := range ws {
		if !space.IsOrganizationID(w.ID) || !space.IsMerchantRole(space.WorkspaceKind(w.Kind), w.Role) {
			continue
		}
		org, err := middleware.LookupMerchantTenant(ctx, h.merchants, sub, w.ID, true)
		switch {
		case err == nil:
			return org, nil
		case !errors.Is(err, repositories.ErrNotFound):
			return nil, err
		}
	}
	return nil, nil
}
```

Update the comment above the old lookup ("The console identity is looked up in live mode…") to: "The console identity is a live tenant of an organization the person administers; mode is a console concern and this route has none."

`api/internal/api/v1/router.go` — in `registerConsole`, add to the `consoleHandlers` literal:

```go
		merchants:            d.Organizations,
```

and, after the literal:

```go
	// Assigned only when set: a nil interface value in the field must stay nil.
	if d.SpaceLister != nil {
		ch.spaces = d.SpaceLister
	}
```

Mount the session **before** the group (after `v1.Get("/me", auth, ch.me)`):

```go
	// Before the group, for the reason finance is: the list is what a person
	// picks an organization from, so no organization is resolved yet, and a
	// group's handlers run as prefix middleware for every /console path.
	v1.Get("/console/session", auth,
		middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.session)
```

and delete the old `console.Get("/session", …)` line inside the group.

- [ ] **Step 4: Integration tests for the session and /me** (append to `api/tests/integration/console_access_test.go`)

```go
func TestSessionNeedsNoModeAndNoSpaceAndListsTheEnvOrganization(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session", token, "", "", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Organizations []struct {
			OrganizationID string `json:"organization_id"`
			Role           string `json:"role"`
			MerchantStatus string `json:"merchant_status"`
		} `json:"organizations"`
	}
	res.decode(t, &body)
	if len(body.Organizations) != 1 || body.Organizations[0].OrganizationID != e.accountOrg ||
		body.Organizations[0].Role != "owner" || body.Organizations[0].MerchantStatus != "live" {
		t.Fatalf("organizations = %+v", body.Organizations)
	}
}

func TestAMemberSeesNoOrganizationInTheSession(t *testing.T) {
	e := newAPI(t)
	member := "usr_" + id.New()
	e.account.set(e.accountOrg, member, "organization", "member")
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session",
		e.token(t, member, "sess_"+id.New(), middleware.ScopeOrganizationRead), "", "", "")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(`"organizations":[]`)) {
		t.Fatalf("%d %s", res.status, res.body)
	}
}

func TestAnOrganizationWithATestTenantOnlyIsTest(t *testing.T) {
	e := newAPI(t)
	acct := newSpaceOrgID()
	test := newLinkedOrg(t, false, acct)
	e.account.set(acct, test.OwnerUserID, "organization", "owner")
	res := e.consoleAs(t, http.MethodGet, "/v1.0/console/session",
		e.token(t, test.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead), "", "", "")
	if res.status != http.StatusOK || !bytes.Contains(res.body, []byte(`"merchant_status":"test"`)) {
		t.Fatalf("%d %s", res.status, res.body)
	}
}
```

In `console_test.go`, `TestServiceTokensAreRejectedOnConsoleRoutes` keeps calling `/v1.0/console/session` and still expects `403` (`RequireUserScope` refuses a token with no sid). `TestMeReportsBothIdentities` (`portal_test.go`) passes unchanged: its operator is the owner of `e.accountOrg`, whose live tenant is `e.org`.

- [ ] **Step 5: Run to verify they pass**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race`
Expected: no gofmt output; all `ok`.
Run: `cd api && make test-integration 2>&1 | tail -20`
Expected: `PASS`; `TestNoNonMerchantReachesAnyBillingHalfRoute` now excludes `/console/session` (it is outside the chain), and still finds ≥ 20 routes.

- [ ] **Step 6: Commit**

```bash
git add api/internal/api/v1/ api/tests/integration/
git commit -m "feat(console): session lists the person's merchant organizations with merchant_status"
```

---

### Task 7: The owner key is no longer written

**Files:**
- Modify: `api/internal/repositories/organizations.go` (`Create`, `SetOwner`)
- Modify: `api/tests/integration/organization_lookup_test.go` (append)
- Modify: `api/tests/integration/finance_invoices_test.go:59-62`

**Interfaces:**
- Consumes: Task 3.
- Produces: `Create` writes `lookup_pk` only for a linked tenant; `SetOwner` writes `owner_user_id` only (the field stays, unread for access — ADR 0028).

- [ ] **Step 1: Write the failing tests** (append to `organization_lookup_test.go`)

```go
func TestAnUnlinkedTenantWithAnOwnerHasNoConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true) // owner, no link
	if _, err := orgRepo().GetByOwner(ctx, org.OwnerUserID, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("GetByOwner = %v: a new tenant must not be reachable by its stored owner", err)
	}
}

func TestSetOwnerWritesNoConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "API only", Livemode: true, PayoutStatus: billing.PayoutEnabled,
	}
	if err := orgRepo().Create(ctx, org, now()); err != nil {
		t.Fatal(err)
	}
	owner := "usr_" + id.New()
	if err := orgRepo().SetOwner(ctx, org, owner, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := orgRepo().GetByOwner(ctx, owner, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("GetByOwner = %v", err)
	}
	got, err := orgRepo().Get(ctx, org.ID, true)
	if err != nil || got.OwnerUserID != owner {
		t.Fatalf("stored owner = %+v, %v", got, err)
	}
}
```

In `finance_invoices_test.go`, replace the `GetByOwner` assertion of `TestProvisioningGivesAnOwnerlessTenantItsOwnerOnce`:

```go
	found, err := repos.Organizations.Get(ctx, plan.Organization.ID, true)
	if err != nil || found.OwnerUserID != owner {
		t.Fatalf("stored owner = %+v, %v; want %s", found, err, owner)
	}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'NoConsoleKey' -v`
Expected: FAIL — `GetByOwner` finds the tenant.

- [ ] **Step 3: Implement**

`Create` — the lookup becomes:

```go
	// Only a linked tenant is reachable from the console (ADR 0028). The owner
	// is stored, never indexed: it no longer grants access.
	if org.AccountOrganizationID != "" {
		if err := r.ensureAccountOrganizationFree(ctx, org.ID, org.AccountOrganizationID, org.Livemode); err != nil {
			return err
		}
		row.LookupPK = LookupOrganizationAccountPK(org.Livemode, org.AccountOrganizationID)
	}
```

`SetOwner` — drop the `if org.AccountOrganizationID == ""` branch added in Task 3, so the update is always `expr`/`cond`/`values` without `lookup_pk`, and change its doc comment's last sentence to: "The owner is a record of who asked for the tenant; it is not indexed and grants no access (ADR 0028)."

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./tests && go vet ./... && go test ./... -race && make test-integration 2>&1 | tail -5`
Expected: no gofmt output; all `ok`; `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/organizations.go api/tests/integration/
git commit -m "feat(organizations): stop writing the owner lookup key; owner_user_id grants nothing"
```

---

### Task 8: Backfill the lookup key of linked tenants

**Files:**
- Create: `api/internal/repositories/organizations_backfill.go`
- Modify: `api/internal/app/app.go` (after `BuildSubscriptions`)
- Create: `api/cmd/backfill-organization-lookup/main.go`, `api/cmd/backfill-organization-lookup/run.sh`
- Modify: `.github/workflows/README.md` (new section after "Subscriptions by customer")
- Test: `api/tests/integration/organization_lookup_test.go` (append)

**Interfaces:**
- Consumes: `LookupOrganizationAccountPK`, `LookupOrganizationOwnerPK` (until Task 11), `Base.ScanRaw`, `Base.UpdateItemRaw`, `DecodeItems`, `IsConditionFailed`.
- Produces: `func (r *OrganizationRepository) BackfillAccountOrganizationLookup(ctx context.Context, apply bool) (OrganizationLookupReport, error)`; `type OrganizationLookupRow`, `type OrganizationLookupReport`; `func app.BuildOrganizations(ctx, cfg) (*repositories.OrganizationRepository, error)`; command `backfill-organization-lookup [-apply]`.

- [ ] **Step 1: Write the failing tests** (append to `organization_lookup_test.go`)

```go
// rowOf finds one tenant's row in a backfill report list.
func rowOf(rows []repositories.OrganizationLookupRow, orgID string, live bool) *repositories.OrganizationLookupRow {
	for i := range rows {
		if rows[i].OrganizationID == orgID && rows[i].Livemode == live {
			return &rows[i]
		}
	}
	return nil
}

func TestBackfillRewritesALegacyOwnerKeyToTheAccountOrganization(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)
	legacyLookup(t, org)
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("before: %v", err)
	}

	dry, err := orgRepo().BackfillAccountOrganizationLookup(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	r := rowOf(dry.Missing, org.ID, true)
	if r == nil || r.From != repositories.LookupOrganizationOwnerPK(true, org.OwnerUserID) ||
		r.To != repositories.LookupOrganizationAccountPK(true, acct) {
		t.Fatalf("dry run missing row = %+v", r)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatal("a dry run wrote")
	}

	if _, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := orgRepo().GetByAccountOrganization(ctx, acct, true); err != nil || got.ID != org.ID {
		t.Fatalf("after apply = %v, %v", got, err)
	}
	again, err := orgRepo().BackfillAccountOrganizationLookup(ctx, false)
	if err != nil || rowOf(again.Missing, org.ID, true) != nil {
		t.Fatalf("re-run still lists the row: %v", err)
	}
}

func TestBackfillReportsTwoTenantsOnOneOrganizationAndWritesNeither(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	a, b := newOrg(t, true), newOrg(t, true)
	setLookup(t, a, repositories.LookupOrganizationOwnerPK(true, a.OwnerUserID), acct)
	setLookup(t, b, repositories.LookupOrganizationOwnerPK(true, b.OwnerUserID), acct)

	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Conflicts, a.ID, true) == nil || rowOf(rep.Conflicts, b.ID, true) == nil {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a conflicting key was written: %v", err)
	}
}

func TestBackfillReportsAForeignKeyAndLeavesIt(t *testing.T) {
	ctx := ctxT(t)
	acct := newSpaceOrgID()
	org := newLinkedOrg(t, true, acct)
	setLookup(t, org, "live#SOMETHING#else", "")
	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Mismatched, org.ID, true) == nil || rowOf(rep.Missing, org.ID, true) != nil {
		t.Fatalf("mismatched = %+v", rep.Mismatched)
	}
}

func TestBackfillListsUnlinkedTenantsThatHadAConsoleKey(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	legacyLookup(t, org)
	rep, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(rep.Unlinked, org.ID, true) == nil {
		t.Fatalf("unlinked = %+v", rep.Unlinked)
	}
}

// Spec § 10.6, first half: tenant zero is reachable by its owner and admins
// after the backfill; before it, by its stored owner only (the fallback).
func TestTenantZeroIsReachableByItsOwnerAndAdminsAfterTheBackfill(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	legacyLookup(t, e.org) // production before the backfill
	admin, member := "usr_"+id.New(), "usr_"+id.New()
	e.account.set(e.accountOrg, admin, "organization", "admin")
	e.account.set(e.accountOrg, member, "organization", "member")
	read := func(sub string) apiResponse {
		return e.console(t, "/v1.0/console/settings", e.token(t, sub, "sess_"+id.New(), middleware.ScopeOrganizationRead), "live")
	}

	if res := read(e.org.OwnerUserID); res.status != http.StatusOK {
		t.Fatalf("owner before the backfill: %d %s", res.status, res.body)
	}
	if res := read(admin); res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("admin before the backfill: %d %s", res.status, res.body)
	}

	if _, err := orgRepo().BackfillAccountOrganizationLookup(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{e.org.OwnerUserID, admin} {
		if res := read(sub); res.status != http.StatusOK || !strings.Contains(string(res.body), e.org.ID) {
			t.Fatalf("%s after the backfill: %d %s", sub, res.status, res.body)
		}
	}
	if res := read(member); res.status != http.StatusForbidden {
		t.Fatalf("member after the backfill: %d %s", res.status, res.body)
	}
}
```

(add `"net/http"`, `"strings"` and the `middleware` import to the file.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'Backfill|TenantZero' -v`
Expected: FAIL — `BackfillAccountOrganizationLookup undefined`, `undefined: repositories.OrganizationLookupRow`.

- [ ] **Step 3: Implement** `api/internal/repositories/organizations_backfill.go`

```go
package repositories

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// OrganizationLookupRow is one tenant row the account-organization backfill
// looked at. From is its lookup_pk now; To the one its link derives ("" when
// unlinked).
type OrganizationLookupRow struct {
	PK, SK                string
	OrganizationID        string
	Livemode              bool
	AccountOrganizationID string
	From, To              string
}

// OrganizationLookupReport is what one pass saw and did.
type OrganizationLookupReport struct {
	// Scanned counts tenant rows, both modes.
	Scanned int
	// Missing are linked tenants whose lookup_pk is absent or the legacy owner
	// key — what a dry run would write.
	Missing []OrganizationLookupRow
	// Written is how many of Missing this pass wrote (0 on a dry run).
	Written int
	// Raced counts writes whose row changed under us: re-run to see it.
	Raced int
	// Mismatched are linked tenants carrying a key that is neither the legacy
	// owner key nor theirs. Reported, never overwritten.
	Mismatched []OrganizationLookupRow
	// Conflicts are two or more tenants linked to one account organization in
	// one mode. None of them is written: choosing is an operator's decision.
	Conflicts []OrganizationLookupRow
	// Unlinked are tenants with no link that carried a console key: after this
	// deploy they are reachable only through M2M credentials (ADR 0028).
	Unlinked []OrganizationLookupRow
}

// BackfillAccountOrganizationLookup gives every linked tenant the
// {mode}#ACCOUNT_ORG#{id} key (ADR 0028), replacing the pre-ADR-0028 owner key.
// Dry run unless apply.
//
// An operator's migration, run from cmd/backfill-organization-lookup with the
// operator's own credentials: it Scans, which the service role is denied.
// Idempotent: each write is conditional on the row still carrying the key it
// was read with and the link it was read with.
func (r *OrganizationRepository) BackfillAccountOrganizationLookup(ctx context.Context, apply bool) (OrganizationLookupReport, error) {
	var rep OrganizationLookupReport
	rows, err := r.scanLookups(ctx)
	if err != nil {
		return rep, err
	}
	rep.Scanned = len(rows)

	byKey := map[string][]OrganizationLookupRow{}
	for _, row := range rows {
		if row.AccountOrganizationID == "" {
			if row.From != "" {
				rep.Unlinked = append(rep.Unlinked, row)
			}
			continue
		}
		row.To = LookupOrganizationAccountPK(row.Livemode, row.AccountOrganizationID)
		byKey[row.To] = append(byKey[row.To], row)
	}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		group := byKey[key]
		if len(group) > 1 {
			rep.Conflicts = append(rep.Conflicts, group...)
			continue
		}
		row := group[0]
		switch {
		case row.From == row.To:
			continue
		case row.From != "" && !strings.HasPrefix(row.From, Mode(row.Livemode)+"#OWNER#"):
			rep.Mismatched = append(rep.Mismatched, row)
			continue
		}
		rep.Missing = append(rep.Missing, row)
		if !apply {
			continue
		}
		wrote, err := r.setAccountLookup(ctx, row)
		if err != nil {
			return rep, err
		}
		if wrote {
			rep.Written++
		} else {
			rep.Raced++
		}
	}
	return rep, nil
}

// organizationLookupProjection is what the backfill reads of a tenant row.
type organizationLookupProjection struct {
	PK                    string `dynamodbav:"pk"`
	SK                    string `dynamodbav:"sk"`
	ID                    string `dynamodbav:"id"`
	Livemode              bool   `dynamodbav:"livemode"`
	AccountOrganizationID string `dynamodbav:"account_organization_id"`
	LookupPK              string `dynamodbav:"lookup_pk"`
}

func (r *OrganizationRepository) scanLookups(ctx context.Context) ([]OrganizationLookupRow, error) {
	var out []OrganizationLookupRow
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.ScanRaw(ctx, &dynamodb.ScanInput{
			FilterExpression:         aws.String("sk = :org"),
			ProjectionExpression:     aws.String("pk, sk, #id, livemode, account_organization_id, lookup_pk"),
			ExpressionAttributeNames: map[string]string{"#id": "id"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":org": &types.AttributeValueMemberS{Value: OrganizationSK()},
			},
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, fmt.Errorf("scan organizations: %w", err)
		}
		rows, err := DecodeItems[organizationLookupProjection](res.Items)
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			out = append(out, OrganizationLookupRow{
				PK: p.PK, SK: p.SK, OrganizationID: p.ID, Livemode: p.Livemode,
				AccountOrganizationID: p.AccountOrganizationID, From: p.LookupPK,
			})
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

func (r *OrganizationRepository) setAccountLookup(ctx context.Context, row OrganizationLookupRow) (bool, error) {
	cond := "attribute_exists(pk) AND account_organization_id = :a AND attribute_not_exists(lookup_pk)"
	values := map[string]types.AttributeValue{
		":l": &types.AttributeValueMemberS{Value: row.To},
		":a": &types.AttributeValueMemberS{Value: row.AccountOrganizationID},
	}
	if row.From != "" {
		cond = "attribute_exists(pk) AND account_organization_id = :a AND lookup_pk = :old"
		values[":old"] = &types.AttributeValueMemberS{Value: row.From}
	}
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: row.PK},
			"sk": &types.AttributeValueMemberS{Value: row.SK},
		},
		UpdateExpression:          aws.String("SET lookup_pk = :l"),
		ConditionExpression:       aws.String(cond),
		ExpressionAttributeValues: values,
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set lookup_pk on %s/%s: %w", row.PK, row.SK, err)
	}
	return true, nil
}
```

`api/internal/app/app.go`, after `BuildSubscriptions`:

```go
// BuildOrganizations wires only the organizations repository, for the
// account-organization lookup backfill.
func BuildOrganizations(ctx context.Context, cfg *config.Config) (*repositories.OrganizationRepository, error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return repositories.NewOrganizationRepository(db, cfg), nil
}
```

`api/cmd/backfill-organization-lookup/main.go`:

```go
// Command backfill-organization-lookup gives every linked tenant the console
// lookup key {mode}#ACCOUNT_ORG#{account_organization_id} (ADR 0028), replacing
// the {mode}#OWNER#{user_id} key the console read before.
//
//	backfill-organization-lookup            # report only (dry run)
//	backfill-organization-lookup -apply     # write
//
// Both modes and every tenant in one pass. It Scans the organizations table,
// which the service role is denied, so it runs with an operator's credentials —
// run.sh beside this file. Idempotent: re-run until the dry run reports
// missing=0. Exit 1 while anything is missing after the pass, mismatched or in
// conflict. Unlinked tenants that had a console key are listed, not fatal.
//
// Deploy order: .github/workflows/README.md, "Console access by membership".
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	apply := flag.Bool("apply", false, "write the account organization key (default: report only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	orgs, err := app.BuildOrganizations(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	rep, err := orgs.BackfillAccountOrganizationLookup(ctx, *apply)
	for _, r := range rep.Missing {
		slog.Info("missing", "tenant", r.OrganizationID, "livemode", r.Livemode, "from", r.From, "to", r.To)
	}
	for _, r := range rep.Mismatched {
		slog.Warn("mismatched", "tenant", r.OrganizationID, "livemode", r.Livemode, "lookup_pk", r.From, "want", r.To)
	}
	for _, r := range rep.Conflicts {
		slog.Warn("conflict", "tenant", r.OrganizationID, "livemode", r.Livemode, "account_organization", r.AccountOrganizationID)
	}
	for _, r := range rep.Unlinked {
		slog.Warn("unlinked", "tenant", r.OrganizationID, "livemode", r.Livemode, "lookup_pk", r.From)
	}
	if err != nil {
		slog.Error("backfill failed", "error", err, "written", rep.Written)
		os.Exit(1)
	}
	slog.Info("backfill done", "table_prefix", cfg.TablePrefix, "applied", *apply,
		"scanned", rep.Scanned, "missing", len(rep.Missing), "written", rep.Written, "raced", rep.Raced,
		"mismatched", len(rep.Mismatched), "conflicts", len(rep.Conflicts), "unlinked", len(rep.Unlinked))
	left := len(rep.Missing) - rep.Written - rep.Raced
	if left > 0 || rep.Raced > 0 || len(rep.Mismatched) > 0 || len(rep.Conflicts) > 0 {
		os.Exit(1)
	}
}
```

`api/cmd/backfill-organization-lookup/run.sh` (then `chmod +x`):

```bash
#!/usr/bin/env bash
# Runs the console lookup backfill (ADR 0028) against an environment with its
# secret read from SSM (never printed). Needs the operator's AWS profile: the
# backfill Scans, which the service role is denied.
#   run.sh ENV            dry run: report what is missing
#   run.sh ENV -apply     write it
set -uo pipefail
env=${1:?usage: run.sh ENV [-apply]}
export AWS_PROFILE=${AWS_PROFILE:-ctech}
export AWS_REGION=${AWS_REGION:-$(aws configure get region)}
g() { aws ssm get-parameter --with-decryption --name "$1" --query Parameter.Value --output text; }
export TABLE_PREFIX="${env}_billing"
export FIELD_ENCRYPTION_KEY=$(g "/ctech-billing/$env/billing/field-encryption-key") || exit 1
cd "$(dirname "$0")/../.."
go run ./cmd/backfill-organization-lookup ${2:-}
```

`.github/workflows/README.md` — after the "Subscriptions by customer" section's last paragraph ("The same sequence applies…"), add:

````markdown
## Console access by membership (ADR 0028)

The billing half of the console finds its tenant by `{mode}#ACCOUNT_ORG#{account_organization_id}`
instead of `{mode}#OWNER#{user_id}`. Production rows (tenant zero `ctech`, linked to account
organization `01a04ed6-1af9-745e-bcf7-d3b66fe52321`) still carry the owner key, so:

1. **Deploy part 1 (API and UI together).** New and linked tenants get the new key; the console
   falls back to the owner key **only** for a tenant linked to the selected organization, so tenant
   zero stays reachable by its stored owner. Its other admins see "cobrança não ativada" until step 2.
2. **Backfill production**, from a workstation:

   ```sh
   api/cmd/backfill-organization-lookup/run.sh prod           # dry run: lists what is missing
   api/cmd/backfill-organization-lookup/run.sh prod -apply    # writes it
   api/cmd/backfill-organization-lookup/run.sh prod           # must report missing=0, exit 0
   ```

   Both modes, every tenant, one pass. It reads `FIELD_ENCRYPTION_KEY` from SSM without printing it,
   writes only `lookup_pk`, conditionally on the row being unchanged. Exit 1 means something is
   missing, mismatched or in conflict (two tenants on one organization): read the log lines first.
   `unlinked` lines are tenants that lose console access (reachable only by M2M credentials); none
   is expected in production.
3. **Confirm** tenant zero opens in the console for an admin of the CTech organization who is not
   its `owner_user_id`.
4. **Remove the fallback** (`GetByOwner`, the owner key): the follow-up pull request, merged only
   after step 2's final dry run reports `missing=0`.
````

- [ ] **Step 4: Run to verify**

Run: `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go build ./cmd/backfill-organization-lookup && bash -n cmd/backfill-organization-lookup/run.sh && test -x cmd/backfill-organization-lookup/run.sh && echo runner-ok`
Expected: no gofmt output; `runner-ok`.
Run: `cd api && go test ./... -race && make test-integration 2>&1 | tail -5`
Expected: all `ok`; `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/organizations_backfill.go api/internal/app/app.go api/cmd/backfill-organization-lookup/ .github/workflows/README.md api/tests/integration/organization_lookup_test.go
git commit -m "feat(organizations): backfill the account organization lookup key (dry run by default)"
```

---

### Task 9: UI — every billing-half call carries the organization

> Execute with the `/impeccable` skill loaded (it owns the copy rules); this task has no new screen, but its error strings are UI text: pt-BR and en, no em dash.

**Files:**
- Modify: `ui/src/lib/api/consoleTypes.ts` (`ConsoleSession` → `ConsoleTenant`; new session types; `ConsoleSettings.organization: ConsoleTenant`)
- Create: `ui/src/lib/console/merchant.ts`, `ui/src/lib/console/merchant.test.ts`, `ui/src/lib/console/useMerchantOrg.ts`, `ui/src/lib/console/scope.tsx`
- Modify: `ui/src/lib/api/console.ts` (headers, keys, every function), `ui/src/lib/api/client.ts` (append)
- Create: `ui/src/lib/api/console.test.ts`
- Modify: every caller: `ui/src/app/(console)/console/{overview,invoices,invoice,subscriptions,subscription,customers,customer,catalog,product,settings}/page.tsx`, `ui/src/components/console/CreditNoteDialog.tsx`
- Modify: `ui/src/dev/consoleMockData.ts`, `ui/src/dev/mockRuntime.ts`
- Modify: `ui/src/locales/pt-BR/errors.json`, `ui/src/locales/en/errors.json`, `ui/src/lib/errors.test.ts`
- Modify: page tests that render a console page: `ui/src/app/(console)/console/subscriptionLabels.test.tsx`, `ui/src/app/(console)/console/periods.test.tsx`, `ui/src/components/console/CreditNoteDialog.test.tsx`

**Interfaces:**
- Consumes: the session wire shape (Task 6), `X-Billing-Space`.
- Produces: the TypeScript block of **Interfaces for parts 2 and 3**; `consoleKeys.session()`; every other `consoleKeys.*(scope, …)` returns `["console", scope.org, scope.mode, …]`; every API function except `getConsoleSession` takes `scope: ConsoleScope` first; `ConsoleScopeProvider`, `useConsoleScope`, `useMerchantOrg`, `getMerchantOrg`, `setMerchantOrg`.

- [ ] **Step 1: Write the failing tests**

`ui/src/lib/console/merchant.test.ts`:

```ts
import {afterEach, describe, expect, it} from "vitest"

import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {getMerchantOrg, pickMerchantOrg, setMerchantOrg, tenantInMode} from "@/lib/console/merchant"

const A = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
const B = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
const org = (id: string, merchant_status: MerchantOrganization["merchant_status"]): MerchantOrganization =>
  ({organization_id: id, selector: `org:${id}`, display_name: id, role: "owner", merchant_status})

afterEach(() => window.localStorage.clear())

describe("tenantInMode", () => {
  it.each([
    ["none", "live", false], ["none", "test", false],
    ["test", "live", false], ["test", "test", true],
    ["live", "live", true], ["live", "test", true],
    ["rejected", "live", false], ["rejected", "test", true],
    ["suspended", "live", true], ["suspended", "test", true],
  ] as const)("%s in %s is %s", (status, mode, want) => {
    expect(tenantInMode(status, mode)).toBe(want)
  })
})

describe("pickMerchantOrg", () => {
  it("keeps the stored organization while the person still administers it", () => {
    expect(pickMerchantOrg([org(A, "live"), org(B, "none")], B, "live")?.organization_id).toBe(B)
  })
  it("falls back to the first organization with a tenant in this mode", () => {
    expect(pickMerchantOrg([org(A, "none"), org(B, "test")], "gone", "test")?.organization_id).toBe(B)
  })
  it("falls back to the first organization when none has a tenant", () => {
    expect(pickMerchantOrg([org(A, "none"), org(B, "none")], null, "live")?.organization_id).toBe(A)
  })
  it("is null with no organizations", () => {
    expect(pickMerchantOrg([], A, "live")).toBeNull()
  })
})

describe("the stored organization", () => {
  it("round-trips a canonical id and refuses anything else", () => {
    setMerchantOrg(A)
    expect(getMerchantOrg()).toBe(A)
    window.localStorage.setItem("ctech-billing-console-organization", "USER#bob")
    expect(getMerchantOrg()).toBeNull()
  })
})
```

`ui/src/lib/api/console.test.ts`:

```ts
import {afterEach, describe, expect, it, vi} from "vitest"

import {apiClient} from "@/lib/api/client"
import {
  consoleKeys, getConsoleInvoicePDF, getConsoleSession, getConsoleSettings, listConsoleInvoices, revealTaxID, voidInvoice,
} from "@/lib/api/console"
import type {ConsoleScope} from "@/lib/console/merchant"

const SCOPE: ConsoleScope = {mode: "test", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}

afterEach(() => vi.restoreAllMocks())

describe("console API", () => {
  it("sends the mode and the organization on every billing-half call", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {}})
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({data: {tax_id: "1"}})
    await getConsoleSettings(SCOPE)
    await listConsoleInvoices(SCOPE, 2026, 10)
    await getConsoleInvoicePDF(SCOPE, "in_1", "pt-BR")
    await voidInvoice(SCOPE, "in_1")
    await revealTaxID(SCOPE, "cus_1")
    const configs = [...get.mock.calls, ...post.mock.calls].map(c => c[c.length - 1] as {headers: Record<string, string>})
    expect(configs).toHaveLength(5)
    for (const config of configs) {
      expect(config.headers).toEqual(expect.objectContaining({"X-Billing-Mode": "test", "X-Billing-Space": `org:${SCOPE.org}`}))
    }
  })

  it("asks for the session with no mode and no space", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {organizations: [], organizations_unavailable: false}})
    await getConsoleSession()
    expect(get).toHaveBeenCalledWith("/v1.0/console/session")
  })

  it("keys every query by organization and mode", () => {
    expect(consoleKeys.session()).toEqual(["console", "session"])
    expect(consoleKeys.invoices(SCOPE)).toEqual(["console", SCOPE.org, "test", "invoices"])
    expect(consoleKeys.invoice(SCOPE, "in_1")).toEqual(["console", SCOPE.org, "test", "invoices", "detail", "in_1"])
    expect(consoleKeys.invoices({...SCOPE, org: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"})).not.toEqual(consoleKeys.invoices(SCOPE))
  })
})
```

In `ui/src/lib/errors.test.ts`, add `"no_merchant", "merchant_role_required"` to `BACKEND_CODES`, and change the `getConsoleInvoicePDF` call to `await getConsoleInvoicePDF({mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}, "i1", "pt-BR")`.

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/console/merchant.test.ts src/lib/api/console.test.ts src/lib/errors.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=1` (missing module `@/lib/console/merchant`; missing catalog entries).

- [ ] **Step 3: Implement the types, store and scope**

`ui/src/lib/api/consoleTypes.ts` — rename `interface ConsoleSession` to `ConsoleTenant` (same fields), change `ConsoleSettings.organization: ConsoleSession` to `organization: ConsoleTenant`, and add:

```ts
/** Where an organization stands as a merchant (spec § 3). Display data; every
 *  billing-half request is re-authorized by the server. `rejected` and
 *  `suspended` arrive with part 2. */
export type MerchantStatus = "none" | "test" | "live" | "rejected" | "suspended"

/** One organization the person may run billing for (owner or admin). */
export interface MerchantOrganization {
  organization_id: string
  /** The X-Billing-Space value; never built from anything else. */
  selector: string
  display_name: string
  role: "owner" | "admin"
  merchant_status: MerchantStatus
}

/** GET /v1.0/console/session (ADR 0028). */
export interface ConsoleSession {
  organizations: MerchantOrganization[]
  /** ctech-account did not answer: the list is empty because it is unknown. */
  organizations_unavailable: boolean
}
```

`ui/src/lib/console/merchant.ts`:

```ts
"use client"

import type {MerchantOrganization, MerchantStatus} from "@/lib/api/consoleTypes"
import type {Mode} from "@/lib/console/mode"

/**
 * The organization the billing half of the console acts for (ADR 0028): an
 * account organization id. It is a REQUEST, the `X-Billing-Space` header, and
 * the server re-authorizes it on every call. Stored apart from the finance
 * space: Finanças may be personal, billing never is.
 */
export type ConsoleScope = {mode: Mode; org: string}

const KEY = "ctech-billing-console-organization"
const EVENT = "ctech-console-organization"
const ORG_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

export function getMerchantOrg(): string | null {
  if (typeof window === "undefined") return null
  try {
    const raw = window.localStorage.getItem(KEY)
    return raw && ORG_ID.test(raw) ? raw : null
  } catch {
    return null
  }
}

export function setMerchantOrg(id: string) {
  try {
    window.localStorage.setItem(KEY, id)
  } catch {
    // Site data blocked: the switch still works for this page.
  }
  window.dispatchEvent(new CustomEvent(EVENT))
}

/** Subscribes to changes, including another tab's. */
export function onMerchantOrgChange(fn: () => void): () => void {
  window.addEventListener(EVENT, fn)
  window.addEventListener("storage", fn)
  return () => {
    window.removeEventListener(EVENT, fn)
    window.removeEventListener("storage", fn)
  }
}

/** Whether the organization has a tenant in this mode. Rejected keeps its test
 *  tenant; suspended keeps both (reads still work). */
export function tenantInMode(status: MerchantStatus, mode: Mode): boolean {
  switch (status) {
    case "live":
    case "suspended":
      return true
    case "test":
    case "rejected":
      return mode === "test"
    default:
      return false
  }
}

/** The organization to show: the stored one while it is still listed, else the
 *  first with a tenant in this mode, else the first. */
export function pickMerchantOrg(orgs: MerchantOrganization[], stored: string | null, mode: Mode): MerchantOrganization | null {
  return orgs.find(o => o.organization_id === stored)
    ?? orgs.find(o => tenantInMode(o.merchant_status, mode))
    ?? orgs[0]
    ?? null
}
```

`ui/src/lib/console/useMerchantOrg.ts`:

```ts
"use client"

import {useSyncExternalStore} from "react"

import {getMerchantOrg, onMerchantOrgChange} from "@/lib/console/merchant"

/** The stored organization as React state (the useMode pattern). */
export function useMerchantOrg(): string | null {
  return useSyncExternalStore(onMerchantOrgChange, getMerchantOrg, () => null)
}
```

`ui/src/lib/console/scope.tsx`:

```tsx
"use client"

import {createContext, type ReactNode, useContext} from "react"

import type {ConsoleScope} from "@/lib/console/merchant"

const ScopeContext = createContext<ConsoleScope | null>(null)

/** Provided by the console shell once an organization with a tenant in this
 *  mode is selected; nothing below it renders before that. */
export function ConsoleScopeProvider({value, children}: {value: ConsoleScope; children: ReactNode}) {
  return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>
}

/** The (mode, organization) every billing-half call and query key is made with. */
export function useConsoleScope(): ConsoleScope {
  const scope = useContext(ScopeContext)
  if (!scope) throw new Error("useConsoleScope: render inside the console shell, after an organization is selected")
  return scope
}
```

- [ ] **Step 4: Implement the API layer**

`ui/src/lib/api/console.ts` — replace the `Mode` import, `modeHeaders` and `consoleKeys`:

```ts
import type {ConsoleScope} from "@/lib/console/merchant"

/**
 * The billing half's calls. Every one carries the mode AND the organization,
 * added here rather than by each caller: the server resolves the tenant from
 * the selected organization and the person's role (ADR 0028) and the mode from
 * the header (ADR 0011). A scope is required, never read from storage here.
 */
function scopeHeaders(s: ConsoleScope) {
  return {headers: {"X-Billing-Mode": s.mode, "X-Billing-Space": `org:${s.org}`}}
}

/**
 * Query keys. Organization and mode are part of every key: two organizations
 * or two modes are two caches, so a switch can never show one's rows under the
 * other's name.
 */
export const consoleKeys = {
  session: () => ["console", "session"] as const,
  overview: (s: ConsoleScope, year: number, month: number) =>
    ["console", s.org, s.mode, "overview", year, month] as const,
  settings: (s: ConsoleScope) => ["console", s.org, s.mode, "settings"] as const,
  invoices: (s: ConsoleScope) => ["console", s.org, s.mode, "invoices"] as const,
  invoiceMonth: (s: ConsoleScope, year: number, month: number) =>
    ["console", s.org, s.mode, "invoices", "month", year, month] as const,
  invoice: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "invoices", "detail", id] as const,
  customers: (s: ConsoleScope) => ["console", s.org, s.mode, "customers"] as const,
  customer: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "customers", "detail", id] as const,
  subscriptions: (s: ConsoleScope) => ["console", s.org, s.mode, "subscriptions"] as const,
  subscription: (s: ConsoleScope, id: string) =>
    ["console", s.org, s.mode, "subscriptions", "detail", id] as const,
  products: (s: ConsoleScope) => ["console", s.org, s.mode, "products"] as const,
  product: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "products", "detail", id] as const,
}

/** The organizations this person may run billing for. No mode, no space: it is
 *  what an organization is chosen from. */
export async function getConsoleSession(): Promise<ConsoleSession> {
  const {data} = await apiClient.get<ConsoleSession>("/v1.0/console/session")
  return data
}
```

Then apply **one rule to every other exported function in the file** (`getConsoleOverview`, `getConsoleSettings`, `setDunningPolicy`, `setProductDunningPolicy`, `revealTaxID`, `listConsoleInvoices`, `getConsoleInvoice`, `listConsoleCustomers`, `getConsoleCustomer`, `listConsoleSubscriptions`, `getConsoleSubscription`, `cancelConsoleSubscription`, `listConsoleProducts`, `getConsoleProduct`, `createProduct`, `createPrice`, `archivePrice`, `getConsoleInvoicePDF`, `setIssuer`, `finalizeInvoice`, `voidInvoice`, `creditInvoice`, and any other function below them): delete the trailing `mode?: Mode` parameter, add `scope: ConsoleScope` as the **first** parameter, and replace `modeHeaders(mode)` with `scopeHeaders(scope)`. Three of them, in full:

```ts
export async function getConsoleSettings(scope: ConsoleScope): Promise<ConsoleSettings> {
  const {data} = await apiClient.get<ConsoleSettings>("/v1.0/console/settings", scopeHeaders(scope))
  return data
}

export async function listConsoleInvoices(
  scope: ConsoleScope,
  year: number,
  month: number,
  cursor?: string,
): Promise<ConsolePage<ConsoleInvoiceRow>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleInvoiceRow>>("/v1.0/console/invoices", {
    ...scopeHeaders(scope),
    params: {year, month, ...(cursor ? {cursor} : {})},
  })
  return data
}

export async function getConsoleInvoicePDF(
  scope: ConsoleScope,
  id: string,
  lang: SupportedLocale = currentLocale(),
): Promise<DocumentLink> {
  const {data} = await apiClient.get<DocumentLink>(
    `/v1.0/console/invoices/${id}/pdf`,
    {...scopeHeaders(scope), params: {lang}},
  )
  return data
}
```

(Keep each function's existing `params`/body exactly; only the scope parameter and the headers change. `listConsoleInvoices`' existing `params` object is kept as it is in the file if it differs from the one above.)

`ui/src/lib/api/client.ts` — append after `isSpaceNotFound`:

```ts
/** The selected organization does not bill through CTech Billing in this mode
 *  (spec § 3): the console renders its empty state, not a failure. */
export function isNoMerchant(error: unknown): boolean {
  return problemCode(error) === "no_merchant"
}
```

`ui/src/locales/pt-BR/errors.json` and `ui/src/locales/en/errors.json`, inside the same object as `no_organization`:

```json
    "no_merchant": "Esta organização ainda não cobra os próprios clientes pelo CTech Billing neste modo.",
    "merchant_role_required": "Só quem é proprietário ou administrador da organização acessa a cobrança.",
```

```json
    "no_merchant": "This organization does not bill its own customers through CTech Billing in this mode yet.",
    "merchant_role_required": "Only owners and admins of the organization can open billing.",
```

- [ ] **Step 5: Move every caller to the scope**

In each of `ui/src/app/(console)/console/{overview,invoices,invoice,subscriptions,subscription,customers,customer,catalog,product,settings}/page.tsx` and `ui/src/components/console/CreditNoteDialog.tsx`:

- `import {useMode} from "@/lib/console/useMode"` → `import {useConsoleScope} from "@/lib/console/scope"`;
- `const mode = useMode()` → `const scope = useConsoleScope()`;
- every `consoleKeys.x(mode, …)` → `consoleKeys.x(scope, …)`;
- every API call `fn(…, mode)` (or `fn(…, undefined, mode)`) → `fn(scope, …)` with the remaining arguments in their order and no trailing `undefined`;
- a `mode` that is still needed for display (e.g. a "Teste" label) is `scope.mode`.

Example, `invoices/page.tsx`:

```tsx
  const scope = useConsoleScope()
  // …
    queryKey: consoleKeys.invoiceMonth(scope, year, month),
    queryFn: () => listConsoleInvoices(scope, year, month),
```

Run: `cd ui && npx next typegen && npx tsc --noEmit`
Expected: the compiler lists every call site still passing a mode; fix each by the rule until it prints nothing.

In the three page/dialog tests, wrap the rendered tree in the provider:

```tsx
import {ConsoleScopeProvider} from "@/lib/console/scope"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const

// in wrap()/render: <QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>
```

- [ ] **Step 6: Mock runtime**

`ui/src/dev/consoleMockData.ts` — replace `MOCK_CONSOLE_SESSION` with:

```ts
export const MOCK_MERCHANT_ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
export const MOCK_NONE_ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"

export const MOCK_CONSOLE_TENANT: Record<"live" | "test", ConsoleTenant> = {
  live: {organization_id: "org_mock_ctech", display_name: "CTech Tecnologia", livemode: true, payout_status: "enabled", can_charge: true},
  test: {organization_id: "org_mock_ctech", display_name: "CTech Tecnologia", livemode: false, payout_status: "enabled", can_charge: true},
}

/** The session per scenario: `sem_cobranca` administers one organization with no tenant. */
export function consoleSession(scenario: string | null): ConsoleSession {
  const none: MerchantOrganization = {
    organization_id: MOCK_NONE_ORG, selector: `org:${MOCK_NONE_ORG}`, display_name: "Padaria Bom Dia", role: "admin", merchant_status: "none",
  }
  if (scenario === "sem_cobranca") return {organizations: [none], organizations_unavailable: false}
  return {
    organizations: [
      {organization_id: MOCK_MERCHANT_ORG, selector: `org:${MOCK_MERCHANT_ORG}`, display_name: "CTech Tecnologia", role: "owner", merchant_status: "live"},
      none,
    ],
    organizations_unavailable: false,
  }
}
```

and `organization: MOCK_CONSOLE_SESSION[mode]` (settings) → `organization: MOCK_CONSOLE_TENANT[mode]` (fix the type imports).

`ui/src/dev/mockRuntime.ts` — in the console block, replace the `sem_conta` refusal and the session branch with:

```ts
    if (url.endsWith("/v1.0/console/session")) {
      if (scenario === "sem_conta") return ok(config, {organizations: [], organizations_unavailable: false})
      return ok(config, consoleSession(scenario))
    }
    // The billing half, like the server: mode and organization required, and
    // only the merchant organization has a tenant.
    const selector = String(config.headers?.["X-Billing-Space"] ?? "")
    if (!selector.startsWith("org:") || !config.headers?.["X-Billing-Mode"]) {
      fail(config, 400, "Bad Request", "informe X-Billing-Mode e X-Billing-Space", "about:blank", "bad_request")
    }
    if (selector !== `org:${MOCK_MERCHANT_ORG}`) {
      fail(config, 404, "No merchant", "this organization does not bill through CTech Billing in this mode", "/problems/no-merchant", "no_merchant")
    }
```

(import `consoleSession`, `MOCK_MERCHANT_ORG` instead of `MOCK_CONSOLE_SESSION`.)

- [ ] **Step 7: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/console/merchant.test.ts src/lib/api/console.test.ts src/lib/errors.test.ts "src/app/(console)/console/subscriptionLabels.test.tsx" "src/app/(console)/console/periods.test.tsx" src/components/console/CreditNoteDialog.test.tsx; echo "vitest exit=$?"`
Expected: `vitest exit=0`.
Run: `cd ui && npx next typegen && npx tsc --noEmit && npm run lint`
Expected: no output from tsc; lint clean. (`layout.tsx` and `AccountMenu.tsx` are rewritten in Task 10; if tsc stops on them here, apply Task 10 Step 3's `getConsoleSession()`/`consoleKeys.session()` call change now and leave the rest to Task 10.)

- [ ] **Step 8: Commit**

```bash
git add ui/src
git commit -m "feat(console-ui): billing-half calls carry the selected organization; keys by organization and mode"
```

---

### Task 10: UI — the organization selector and the `no_merchant` state

> **Execute with the `/impeccable` skill** (`shape`, then `craft`), giving it `ui/PRODUCT.md`, `ui/DESIGN.md` ("Navigation and the shell", "On a phone", "Copy and controls"), spec § 7.5 and this task. Use `@aoctech/ui` (`Select`, `EmptyState`, `ErrorState`, `Skeleton`, `Button`); check ctech-ui for an organization switcher before drawing one (the finance `SpaceSwitch` is the local precedent). The phone has **its own layout**: header row 1 = brand + account menu; row 2 = organization selector (flex-1) + mode switch; the empty state left-aligned and stacked. No em dash in any string.

**Files:**
- Create: `ui/src/components/console/OrganizationSwitch.tsx`, `OrganizationSwitch.test.tsx`
- Create: `ui/src/components/console/NoMerchant.tsx`, `NoMerchant.test.tsx`
- Modify: `ui/src/app/(console)/console/layout.tsx`
- Modify: `ui/src/components/console/ModeSwitch.tsx` (removed query prefix)
- Modify: `ui/src/components/AccountMenu.tsx`, `ui/src/components/AccountMenu.test.tsx`
- Modify: `ui/src/lib/console/nav.ts` (doc comment only)
- Modify: `ui/src/locales/pt-BR/console.json`, `ui/src/locales/en/console.json`
- Modify: `ui/DESIGN.md` (new section "Console: whose billing (2026-10-10)")

**Interfaces:**
- Consumes: everything Task 9 produces.
- Produces: `OrganizationSwitch({organizations, selected}: {organizations: MerchantOrganization[]; selected: MerchantOrganization})`, `NoMerchant({name}: {name: string})`; i18n keys `console.organization.label`, `console.organization.unavailable`, `console.organization.retry`, `console.noMerchant.title`, `console.noMerchant.body`. Part 2 replaces `NoMerchant`'s body with the invitation and its action.

- [ ] **Step 1: Copy** — add to `ui/src/locales/pt-BR/console.json` (top level):

```json
 "organization": {
  "label": "Organização",
  "unavailable": "Não foi possível verificar suas organizações agora.",
  "retry": "Tentar de novo"
 },
 "noMerchant": {
  "title": "Cobrança não ativada",
  "body": "{{name}} ainda não cobra os próprios clientes pelo CTech Billing neste modo."
 },
```

and to `ui/src/locales/en/console.json`:

```json
 "organization": {
  "label": "Organization",
  "unavailable": "We could not check your organizations right now.",
  "retry": "Try again"
 },
 "noMerchant": {
  "title": "Billing is not on",
  "body": "{{name}} does not bill its own customers through CTech Billing in this mode yet."
 },
```

- [ ] **Step 2: Write the failing tests**

`ui/src/components/console/NoMerchant.test.tsx`:

```tsx
import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import {describe, expect, it} from "vitest"

import {NoMerchant} from "@/components/console/NoMerchant"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"

describe("NoMerchant", () => {
  it("names the organization and says billing is not on, with no action yet", () => {
    render(<NoMerchant name="Padaria Bom Dia"/>)
    expect(screen.getByText("Cobrança não ativada")).toBeInTheDocument()
    expect(screen.getByText(/Padaria Bom Dia ainda não cobra/)).toBeInTheDocument()
    expect(screen.queryByRole("button")).toBeNull()
  })

  it("has no em dash in the console copy, in either language", () => {
    for (const catalog of [ptBR, en]) {
      expect(JSON.stringify(catalog.console)).not.toContain("—")
    }
  })
})
```

`ui/src/components/console/OrganizationSwitch.test.tsx`:

```tsx
import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it} from "vitest"

import {OrganizationSwitch} from "@/components/console/OrganizationSwitch"
import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {getMerchantOrg} from "@/lib/console/merchant"

const A: MerchantOrganization = {organization_id: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", selector: "org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", display_name: "CTech Tecnologia", role: "owner", merchant_status: "live"}
const B: MerchantOrganization = {organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", selector: "org:0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", display_name: "Padaria Bom Dia", role: "admin", merchant_status: "none"}

afterEach(() => window.localStorage.clear())

describe("OrganizationSwitch", () => {
  it("shows the selected organization by name, never its id", () => {
    render(<QueryClientProvider client={new QueryClient()}><OrganizationSwitch organizations={[A, B]} selected={A}/></QueryClientProvider>)
    expect(screen.getByRole("combobox", {name: "Organização"})).toHaveTextContent("CTech Tecnologia")
    expect(screen.queryByText(A.organization_id)).toBeNull()
  })

  it("stores the choice and removes the previous organization's queries", async () => {
    const client = new QueryClient()
    client.setQueryData(["console", A.organization_id, "live", "invoices"], {data: []})
    client.setQueryData(["console", "session"], {organizations: [A, B], organizations_unavailable: false})
    render(<QueryClientProvider client={client}><OrganizationSwitch organizations={[A, B]} selected={A}/></QueryClientProvider>)
    await userEvent.click(screen.getByRole("combobox", {name: "Organização"}))
    await userEvent.click(await screen.findByRole("option", {name: "Padaria Bom Dia"}))
    expect(getMerchantOrg()).toBe(B.organization_id)
    expect(client.getQueryData(["console", A.organization_id, "live", "invoices"])).toBeUndefined()
    expect(client.getQueryData(["console", "session"])).toBeDefined()
  })
})
```

In `ui/src/components/AccountMenu.test.tsx`, replace the `person` helper's console probe:

```tsx
  const probe = vi.spyOn(consoleApi, "getConsoleSession")
  if (operator) probe.mockResolvedValue({organizations: [{organization_id: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", selector: "org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", display_name: "Acme", role: "owner", merchant_status: "live"}], organizations_unavailable: false})
  else probe.mockResolvedValue({organizations: [], organizations_unavailable: false})
```

and add a case:

```tsx
  it("offers no console to an admin whose organizations do not bill yet", async () => {
    vi.spyOn(portal, "getSession").mockResolvedValue({customer_id: "cus_1", name: FULL, email: EMAIL, terms_accepted: true})
    vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({organizations: [{organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", selector: "org:0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", display_name: "Padaria", role: "admin", merchant_status: "none"}], organizations_unavailable: false})
    renderWithQuery(<AccountMenu view="portal"/>)
    await vi.waitFor(() => expect(consoleApi.getConsoleSession).toHaveBeenCalled())
    const menu = await open()
    expect(within(menu).queryByRole("menuitem", {name: "Console"})).toBeNull()
  })
```

(The existing tests that used `mockRejectedValue(forbidden)` for a non-operator keep passing: a rejected session also means no console.)

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/console/NoMerchant.test.tsx src/components/console/OrganizationSwitch.test.tsx src/components/AccountMenu.test.tsx; echo "vitest exit=$?"`
Expected: `vitest exit=1` (missing components; AccountMenu still reads the old shape).

- [ ] **Step 3: Implement**

`ui/src/components/console/NoMerchant.tsx`:

```tsx
"use client"

import {EmptyState} from "@aoctech/ui"
import {Store} from "lucide-react"
import {useTranslation} from "react-i18next"

/**
 * An organization the person administers that has no billing tenant in this
 * mode (`no_merchant`). A beginning, not a fault: it is never drawn as an
 * error. Part 2 turns it into the invitation to apply.
 */
export function NoMerchant({name}: {name: string}) {
  const {t} = useTranslation()
  return (
    <EmptyState
      icon={<Store/>}
      title={t("console.noMerchant.title")}
      description={t("console.noMerchant.body", {name})}
      className="max-sm:items-start max-sm:text-left"
    />
  )
}
```

`ui/src/components/console/OrganizationSwitch.tsx`:

```tsx
"use client"

import {Select} from "@aoctech/ui"
import {useQueryClient} from "@tanstack/react-query"
import {useTranslation} from "react-i18next"

import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {setMerchantOrg} from "@/lib/console/merchant"
import {selectCopy} from "@/lib/selectCopy"

/**
 * Which organization the billing half acts for (ADR 0028): a closed list from
 * the session, the organizations where the person is owner or admin. The value
 * is a request the server re-authorizes on every call. Switching removes the
 * previous organization's queries, so none of its rows can show for a frame
 * under the new name.
 */
export function OrganizationSwitch({organizations, selected}: {organizations: MerchantOrganization[]; selected: MerchantOrganization}) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  return (
    <Select {...selectCopy()}
      aria-label={t("console.organization.label")}
      className="min-w-36 max-w-64 max-sm:max-w-none max-sm:flex-1"
      value={selected.organization_id}
      onValueChange={v => {
        if (v === selected.organization_id) return
        setMerchantOrg(v)
        void queryClient.removeQueries({queryKey: ["console", selected.organization_id]})
      }}
      options={organizations.map(o => ({value: o.organization_id, label: o.display_name}))}
    />
  )
}
```

`ui/src/components/console/ModeSwitch.tsx` — the switch now removes the current organization's queries for the mode it leaves. Add `import {useMerchantOrg} from "@/lib/console/useMerchantOrg"`, `const org = useMerchantOrg()`, and replace `void queryClient.removeQueries({queryKey: ["console", mode]})` with:

```tsx
              void queryClient.removeQueries({queryKey: ["console", org, mode]})
```

Update its doc comment: "the organization and the mode are part of every console query key".

`ui/src/components/AccountMenu.tsx` — the operator probe:

```tsx
  const {data: operator} = useQuery({
    queryKey: consoleKeys.session(),
    queryFn: getConsoleSession,
    enabled: authenticated,
    retry: false,
    staleTime: 60_000,
    // An organization with a tenant in either mode: the console is invoicing,
    // and an organization that does not bill yet has nothing there (part 2
    // brings the invitation and widens this).
    select: s => s.organizations.some(o => o.merchant_status !== "none"),
  })
```

and the condition `...(operator || view === "console"` stays as it is (now a boolean). Update the doc comment's "only when the operator probe (the console session, live) answers" → "only when the console session lists an organization that bills".

`ui/src/app/(console)/console/layout.tsx` — replace the session query, `noOrganization`, the display-name span and `<main>`:

```tsx
  const mode = useMode()
  const stored = useMerchantOrg()
  const {data: session, error, isPending, refetch} = useQuery({
    queryKey: consoleKeys.session(),
    queryFn: getConsoleSession,
    enabled: authenticated,
    retry: false,
    staleTime: 60_000,
  })

  const organizations = session?.organizations ?? []
  const unavailable = session?.organizations_unavailable ?? false
  const selected = session ? pickMerchantOrg(organizations, stored, mode) : null
  const active = selected !== null && tenantInMode(selected.merchant_status, mode)
  const org = selected?.organization_id
  const scope = useMemo(() => (org ? {mode, org} : null), [mode, org])

  // Owner or admin of no organization: nothing here is theirs; Finanças is.
  const noOrganization = statusOf(error) === 403 || (session !== undefined && !unavailable && organizations.length === 0)

  // A stored organization the person no longer administers is replaced once.
  useEffect(() => {
    if (org && org !== stored) setMerchantOrg(org)
  }, [org, stored])
```

(keep the two existing `useEffect`s for `/login` and `FINANCE_HREF`, and `const nav = consoleNav(active)`.)

In the header, delete the `{session && (<span …>{session.display_name}</span>)}` block, and make the controls row:

```tsx
          <div className="order-3 flex w-full min-w-0 items-center gap-x-3 gap-y-2 sm:order-2 sm:ml-auto sm:w-auto">
            {/* Whose billing, then which world: "CTech · Teste" reads as one thing. */}
            {selected && <OrganizationSwitch organizations={organizations} selected={selected}/>}
            <ModeSwitch/>
          </div>
```

and `<main>`:

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
        ) : selected && !active ? (
          <NoMerchant name={selected.display_name}/>
        ) : scope ? (
          <ConsoleScopeProvider value={scope}>{children}</ConsoleScopeProvider>
        ) : (
          <ShellSkeleton/>
        )}
      </main>
```

Imports to add: `useMemo` from react; `Button`, `ErrorState` from `@aoctech/ui`; `OrganizationSwitch`, `NoMerchant`; `pickMerchantOrg`, `setMerchantOrg`, `tenantInMode` from `@/lib/console/merchant`; `useMerchantOrg`; `ConsoleScopeProvider`. Replace the layout's doc-comment paragraph "the organization is *not* switchable — it comes from the signed-in owner…" with: "The organization is switchable: it is the selected account organization where the person is owner or admin (ADR 0028), a request the server re-authorizes on every call; the mode is the other switch."

`ui/src/lib/console/nav.ts` — doc comment: "They appear only once the session has confirmed a tenant for the selected organization in this mode."

`ui/DESIGN.md` — append a section "## Console: whose billing (2026-10-10)" recording: the selector sits left of the mode switch and reads with it as one phrase; on a phone it takes the row's remaining width (row 2), brand and account menu stay on row 1; `no_merchant` is an `EmptyState` (never an error colour), left-aligned on a phone; the Console entry in the account menu needs an organization that bills (part 2 widens it); what `/impeccable` decided beyond this.

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"`
Expected: `vitest exit=0`.
Run: `cd ui && npx next typegen && npx tsc --noEmit && npm run lint && npm run build`
Expected: clean; static export built.
Browser check on `npm run dev:mock` at 320, 375, 768 and 1280 px: default scenario opens on CTech Tecnologia (live) with invoices; switching to Padaria Bom Dia shows "Cobrança não ativada" with no request answered by a row of CTech; `?scenario=sem_cobranca` shows the empty state and the account menu has no Console entry outside the console; `?scenario=sem_conta` redirects to Finanças; on a phone the selector and mode switch share row 2 with no horizontal scroll.

- [ ] **Step 5: Commit**

```bash
git add ui/src ui/DESIGN.md
git commit -m "feat(console-ui): organization selector for the billing half and the no_merchant state"
```

---

### Task 11 (follow-up deploy): Remove the owner fallback

**Run only after** `api/cmd/backfill-organization-lookup/run.sh prod` (dry run, after `-apply`) has reported `missing=0` and exited 0, and an admin of the CTech organization who is not `owner_user_id` has opened tenant zero (README step 3). Its own branch and pull request.

**Files:**
- Modify: `api/internal/middleware/merchant.go` (delete `legacyOwnerTenants` and the fallback in `LookupMerchantTenant`)
- Modify: `api/internal/middleware/merchant_test.go` (delete `legacyTenants`, `TestTheLegacyOwnerKeyReachesOnlyALinkedTenant`; keep `TestTheLegacyFallbackNeverWidensTheRole` rewritten below)
- Modify: `api/internal/repositories/organizations.go` (delete `GetByOwner`)
- Modify: `api/internal/repositories/keys.go`, `keys_test.go` (delete `LookupOrganizationOwnerPK` and its use in the key test)
- Modify: `api/internal/repositories/organizations_backfill.go` (the legacy-prefix branch reads the literal; see below)
- Modify: `api/internal/repositories/rows.go` (comment)
- Modify: `api/tests/integration/organization_lookup_test.go` (`legacyLookup`, the § 10.6 tests)

**Interfaces:**
- Consumes: Tasks 3, 4, 8.
- Produces: `LookupMerchantTenant` with the same signature, now exactly `src.GetByAccountOrganization`; no `GetByOwner` anywhere (`grep -rn GetByOwner api/` empty).

- [ ] **Step 1: Write the failing test** (append to `organization_lookup_test.go`; spec § 10.6, second half)

```go
// Nobody reaches a tenant by owner_user_id alone: not as an owner of its
// organization while the row carries only the old key, and not as a stored
// owner who is no member of the organization. Two envs, so no membership
// answer is cached across the two halves.
func TestNobodyReachesATenantByOwnerUserIDAlone(t *testing.T) {
	read := func(e *apiEnv) apiResponse {
		token := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), middleware.ScopeOrganizationRead)
		return e.console(t, "/v1.0/console/settings", token, "live")
	}

	e := newAPI(t)
	legacyLookup(t, e.org) // an un-backfilled row
	if res := read(e); res.status != http.StatusNotFound || !strings.Contains(string(res.body), `"no_merchant"`) {
		t.Fatalf("owner on a legacy-only row: %d %s", res.status, res.body)
	}

	f := newAPI(t)
	f.account.mu.Lock()
	delete(f.account.members, f.accountOrg+"|"+f.org.OwnerUserID)
	f.account.mu.Unlock()
	if res := read(f); res.status != http.StatusNotFound || !strings.Contains(string(res.body), "/problems/space-not-found") {
		t.Fatalf("stored owner who is not a member: %d %s", res.status, res.body)
	}
}
```

Change `legacyLookup` to write the literal pre-ADR-0028 key, since the helper it called is deleted:

```go
func legacyLookup(t *testing.T, org *billing.Organization) {
	t.Helper()
	setLookup(t, org, repositories.Mode(org.Livemode)+"#OWNER#"+org.OwnerUserID, "")
}
```

and in `TestBackfillRewritesALegacyOwnerKeyToTheAccountOrganization` replace `repositories.LookupOrganizationOwnerPK(true, org.OwnerUserID)` with `"live#OWNER#" + org.OwnerUserID`; in `TestBackfillReportsTwoTenantsOnOneOrganizationAndWritesNeither` replace both `LookupOrganizationOwnerPK(true, x.OwnerUserID)` with `"live#OWNER#" + x.OwnerUserID`. Delete `TestAnUnlinkedTenantWithAnOwnerHasNoConsoleKey` and the `GetByOwner` assertion of `TestSetOwnerWritesNoConsoleKey` (keep its stored-owner assertion). In `TestTenantZeroIsReachableByItsOwnerAndAdminsAfterTheBackfill`, the owner's pre-backfill read now expects `404 no_merchant` like the admin's.

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -count=1 ./tests/integration/ -run 'NobodyReaches|TenantZeroIsReachable' -v`
Expected: FAIL — the owner on a legacy-only row gets `200` through the fallback.

- [ ] **Step 3: Remove the fallback**

`api/internal/middleware/merchant.go` — delete `legacyOwnerTenants` and replace `LookupMerchantTenant` with:

```go
// LookupMerchantTenant reads the tenant of an account organization in one
// mode. The caller has already established that sub is an owner or admin of
// accountOrgID (or listed it from ctech-account's answer); sub is kept in the
// signature for the callers' sake and for logging, and decides nothing.
func LookupMerchantTenant(ctx context.Context, src MerchantTenants, sub, accountOrgID string, livemode bool) (*billing.Organization, error) {
	_ = sub
	return src.GetByAccountOrganization(ctx, accountOrgID, livemode)
}
```

`api/internal/middleware/merchant_test.go` — delete `legacyTenants` and `TestTheLegacyOwnerKeyReachesOnlyALinkedTenant`; rewrite `TestTheLegacyFallbackNeverWidensTheRole` as:

```go
func TestAMemberIsRefusedWhateverTheTenantRows(t *testing.T) {
	src := &members{m: map[string]string{orgA + "|alice": "member"}}
	ts := &tenants{byAccount: map[string]*billing.Organization{orgA + "|live": tenantA}}
	app, ran := merchantApp(src, ts, "alice", "sess")
	if code, body := get(t, app, "live", "org:"+orgA); code != 403 || *ran || ts.calls != 0 {
		t.Fatalf("%d %q ran=%v reads=%d", code, body, *ran, ts.calls)
	}
}
```

`api/internal/repositories/organizations.go` — delete `GetByOwner` and its doc comment. `keys.go` — delete `LookupOrganizationOwnerPK`; in `keys_test.go` replace the collision assertion with `if LookupOrganizationAccountPK(true, acct) == "live#OWNER#"+acct { t.Fatal("account and owner keys collide") }`. `organizations_backfill.go` keeps its `Mode(row.Livemode)+"#OWNER#"` prefix check (it is the backfill's knowledge of the old key, documented there). `rows.go` — the `LookupPK` comment loses its "or, on rows written before that…" clause.

- [ ] **Step 4: Run to verify**

Run: `cd api && grep -rn "GetByOwner\|LookupOrganizationOwnerPK\|legacyOwnerTenants" --include=*.go . ; echo "grep exit=$?"`
Expected: `grep exit=1` (nothing found).
Run: `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race && make test-integration 2>&1 | tail -5`
Expected: no gofmt output; all `ok`; `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/
git commit -m "feat(console): remove the owner-key fallback; owner_user_id grants nothing (ADR 0028)"
```

---

## Final verification (end of Task 10, before the pull request)

Sequentially, never in parallel:

1. `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race && make test-integration` → clean, all `ok`, `PASS`.
2. `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"` → `exit=0`; `npx next typegen && npx tsc --noEmit && npm run lint && npm run build` → clean.
3. `grep -rn "ResolveConsoleTenant" api/ ui/` → nothing. `grep -rn "—" ui/src/locales/*/console.json ui/src/locales/*/errors.json` → no line added by this plan.
4. Cross-repo check (CLAUDE.md): `ResolveSpace`/`IsMerchantRole` are billing's; the membership client is `accountorgs` from api-commons (no local copy); no other service resolves a console tenant by owner — note in the PR body if ctech-dfe or ctech-wallet have an owner-keyed console lookup worth the same change.
