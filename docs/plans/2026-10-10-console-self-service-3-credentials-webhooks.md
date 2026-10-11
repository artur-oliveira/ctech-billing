# Console self-service 3: Credentials and webhooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An owner or admin of a merchant organization connects their own ctech-account OAuth client as the tenant's API credential (billing verifies the owner, the scopes and that the client is free through ctech-account's internal route) and manages the tenant's webhook endpoints (list, create, delete, rotate secret, test) from `/console/credentials` and `/console/webhooks`. Billing marks its integration scopes `"m2m": true` so ctech-account issues `client_credentials` tokens to those clients.

**Architecture:** Two new pieces of shared code first: a `oauthclients` package in ctech-go-common (`gopkg.aoctech.app/api-commons`, tag `v1.16.0`) that reads `GET /v1.0/internal/oauth-clients/:client_id`, and a `?new=1&scopes=` preselection on ctech-account's developer page. In billing, `services.CredentialConnector` holds the four connect rules (owner, billing scope, m2m-only and active, not admitted anywhere) and the repositories gain console writes with their audit rows: a credential plus a per-client **guard row** in one transaction (two tenants cannot admit one client at once), and webhook endpoints counted under a per-tenant **revision row** (two concurrent creates cannot pass the limit of 5). Console routes mount under part 1's merchant chain, so tenant, role and mode are already resolved; reads take `billing:organization:read`, writes `billing:organization:write` (part 2). Merchant-created endpoints are delivered through an SSRF-guarded HTTP client; operator-provisioned ones keep today's client.

**Tech Stack:** Go 1.27.2 (`api/go.mod`), Fiber v3.5, DynamoDB through api-commons (`v1.15.0` → `v1.16.0`), Next.js 16 static export, React 19, TanStack Query 5, `@aoctech/ui` 0.4.1, vitest. Cross-repo: ctech-go-common (Go), ctech-account `ui/` (Next.js, vitest).

**Spec:** [`docs/specs/2026-10-10-console-self-service.md`](../specs/2026-10-10-console-self-service.md): § 5 (credentials and webhooks), § 6.1 credentials/webhooks rows, § 6.2 credential row (`connected_by`, `connected_at`), § 6.3 (the internal route and the 2026-10-10 m2m decision), § 7 item 4, § 9 step 4, § 10.4 and § 10.5. Builds on: [part 1 plan](2026-10-10-console-self-service-1-access.md) (merged; its **Interfaces for parts 2 and 3** are consumed exactly), the part 2 plan (applications, live tenant, suspension, `billing:organization:write`; written in parallel, assumptions listed below), and ctech-account's [`docs/plans/2026-10-10-oauth-client-internal-route.md`](https://github.com/artur-oliveira/ctech-account/blob/main/docs/plans/2026-10-10-oauth-client-internal-route.md) (route contract, `"m2m": true`, "What ctech-billing must publish").

## Dependencies on other plans (check before Task 3)

From **part 1** (merged plan, implemented first), exactly as named there:

```go
console := v1.Group("/console", auth,
	middleware.RequireUserSession(),
	middleware.ResolveSpace(spaces),
	middleware.ResolveMerchantTenant(d.Organizations))
func GetMerchantAccess(c fiber.Ctx) MerchantAccess // {AccountOrganizationID, Role, Livemode}
func GetTenant(c fiber.Ctx) Tenant                 // {OrganizationID, Livemode}
func GetOrganization(c fiber.Ctx) *billing.Organization
```

Integration helpers from part 1 Task 5: `(*apiEnv).consoleAs(t, method, path, token, mode, selector, body string) apiResponse`, `(*apiEnv).consoleWrite(t, method, path, token, mode, body string) apiResponse`, `(*apiEnv).console(t, path, token, mode string) apiResponse`, `apiEnv.account *fakeAccount`, `apiEnv.accountOrg string`, `(*apiEnv).sessionToken(t, scopes...)`, `allConsoleScopes`, `billingHalfRoutes`, `TestNoNonMerchantReachesAnyBillingHalfRoute` (it enumerates the route table, so this plan's routes are covered by § 10.1 without editing it). UI from part 1 Task 9: `ConsoleScope {mode, org}`, `useConsoleScope()`, `scopeHeaders(scope)` (private to `ui/src/lib/api/console.ts`), keys `["console", scope.org, scope.mode, ...]`, `getConsoleSession()`, `consoleKeys.session()`, `MerchantOrganization.merchant_status`.

From **part 2** (assumed; if its plan names differ, change only the marked call sites):

- `middleware.ScopeOrganizationWrite = "billing:organization:write"` (public, user scope, never m2m) in `api/internal/middleware/scope.go` and the manifest, and in `allConsoleScopes` and the UI's `CONSOLE_SCOPES`. Check: `grep -n ScopeOrganizationWrite api/internal/middleware/scope.go`.
- The live tenant row carries `suspended_at` (spec § 6.2) and `func (o *Organization) Suspended() bool` exists in `api/internal/domain/billing`. Check: `grep -rn 'func (o \*Organization) Suspended' api/internal/domain/billing/`. If the method has another name, use it in `isSuspended` (Task 6) only.
- Problem code `tenant_suspended` (403) and its UI catalog entry. If part 2 did not add the UI entry, Task 11 adds it.
- `ResolveMerchantTenant` answers `no_merchant` in live mode until the application is approved (spec § 5.1 "Live mode requires the live tenant" is therefore enforced by the chain, not by this plan).

From **ctech-account** (its plan, deployed before this plan's deploy): `GET /v1.0/internal/oauth-clients/:client_id` → `200 {client_id, owner_user_id, allowed_scopes, active}`, `404` problem+json for an unknown or over-long id, scope `internal:account:oauth-client-read` granted to billing's client (`/ctech-billing/{env}/billing/account-client-id`), and third-party `client_credentials` for clients whose every scope is `"m2m": true`.

## Scope decisions (read before executing)

1. **A suspended live tenant may shrink or secure its integration, never grow or exercise it.** Allowed: list, disconnect a credential, delete an endpoint, rotate a secret (a leaked secret must be replaceable during a dispute). Refused with `403 tenant_suspended`: connect a credential, create an endpoint, test an endpoint. Test mode is never suspended.
2. **Non-billing scopes are refused.** Billing knows only its own manifest's `m2m` marks. A client holding `dfe:nfe:read` (m2m in ctech-dfe's manifest, unknowable here) is refused with `client_not_machine_only`, as is `openid`, `internal:*` or any `billing:` scope not marked `m2m`. Merchants use one client per product for machine access. Relaxing this later needs ctech-account to report `m2m` per scope on the internal route.
3. **Rule order makes the refusals oracle-free.** (1) unknown client or `owner_user_id` ≠ the person → `client_not_yours`; (2) no `billing:` scope → `client_has_no_billing_scope`; (3) any scope outside billing's m2m set, or `active: false` → `client_not_machine_only`; (4) admitted anywhere (global `Resolve`, inactive rows included) → `client_already_connected`. Only the client's owner ever reaches (4), so "connected somewhere" is said only to the person who owns the client, and never where.
4. **`active: false` maps to `client_not_machine_only`.** The four spec codes cover it: an inactive client is a public client (no secret) or one whose owner is locked, and neither can obtain a `client_credentials` token. No fifth code.
5. **Reconnecting the same client to the same tenant and mode is `200` with the existing credential**, not a refusal: it is a double click.
6. **One client is admitted once, enforced by a guard row.** `CredentialRepository` writes `pk = CLIENT#{client_id}, sk = GUARD` (no `lookup_pk`, so it never enters the lookup index) in the same transaction as the credential, conditional on absence; `Disconnect` deletes both. `Create` (the seed path) writes the guard too. Credentials seeded before this plan have no guard; `Resolve` still finds them, so a console connect of one is refused at rule (4), and they are first-party clients owned by `system` anyway (rule 1).
7. **Provisioned credentials and endpoints are read-only in the console.** A credential with no `connected_by` or an endpoint with no `added_by` came from the tenant plan (`tenants/*.json`: tenant zero's `dfe-billing`, `account-billing`, `whe_dfe`). The console lists them (`provisioned: true`) and refuses delete, rotate and test with `409 credential_provisioned` / `409 webhook_endpoint_provisioned`. Removing one from a browser would cut a CTech service off.
8. **The limit of 5 endpoints counts every endpoint of the tenant and mode**, provisioned ones included, and is enforced with a revision row (`ENDPOINT_REV`) the create transaction conditions on; up to three attempts, then `409 concurrent_update`.
9. **Webhook URLs are checked against SSRF twice.** At create: `https`, a host, no userinfo, no IP literal outside public unicast, no `localhost`, `*.localhost`, `*.local`, `*.internal`, no single-label host (`422`, field `url`, codes `invalid_format` / `unsupported_value`). At delivery: merchant endpoints go through a client whose dialer refuses any non-public address after DNS resolution (DNS rebinding) and which follows no redirect (a 3xx is a failed attempt). Provisioned endpoints keep the plain client: `whe_dfe` points at `dfe.internal.aoctech.app`.
10. **Rotation has no overlap window.** The old secret stops signing at once; deliveries that fail meanwhile are retried (8 attempts over about two days) and signed with the new one. The UI says so before confirming.
11. **The test is a `ping` event through the normal path.** `Event.Type = "ping"`, `EndpointID` set, `Entity = "webhook_endpoint"`; `WebhookEndpoint.Wants` sends a ping only to its own endpoint, whatever the endpoint's event filter. The response is `202 {"event_id"}`; `cmd/deliver` runs every minute. `ping` is not subscribable (not in the event list). A disabled endpoint refuses the test with `409 webhook_endpoint_disabled`; re-enabling is out of scope (delete and create again).
12. **The event list is a catalogue in the domain**, `billing.WebhookEventTypes()`, pinned by a test that parses the package for every `EventType` constant. `GET /console/webhooks` returns it with `max_endpoints`, so the UI never keeps a second copy.
13. **Who connected it is answered as `connected_by_me`.** Billing has no names for people; the list returns `connected_by` (a user id) and `connected_by_me`, and the UI says "Você" or "Outra pessoa da organização". Names would need a ctech-account read per row; out of scope.
14. **ctech-account unreachable on connect is `503 account_unavailable`** (`type /problems/account-unavailable`), "tente de novo". Not part 1's `space_unavailable`: the membership was already resolved; it is the client read that failed.
15. **The `oauthclients` client is shared code** (family rule, CLAUDE.md): same shape as `accountorgs`, a client for a ctech-account internal route that any product admitting merchant clients would need. It lives in ctech-go-common, released as `v1.16.0`, and billing bumps its pin.
16. **ctech-account's developer page has no preselection today** (`/account/oauth-clients` opens with `openid profile email`, which would make the client non-m2m). Task 2 adds `?new=1&scopes=a,b,c`, which opens the create dialog with exactly those scopes. Without it deployed, the billing link still lands on the page and the console says which scopes to tick. The page still requires a redirect URI even for a machine-only client; that is ctech-account's to relax and is noted, not changed, here.
17. **The m2m marks ship with this code but must not reach an environment before ctech-account's change.** `deploy.yml` publishes the manifest whenever `scope-manifest.json` changes on `main`/`staging`/`dev`; an older ctech-account drops the field and a later identical publish does not resend it. Gate: this PR merges only after ctech-account § 9 step 1 is live in every environment its branch deploys to; recovery is `workflow_dispatch` with `scopes: true`. The connect check reads the embedded manifest, so it is right whether or not the publish happened.

## Global Constraints

- Go commands from `api/` (ctech-go-common from its root). Every Go task ends with `gofmt -l ./internal ./cmd ./tests` (no output), `go vet ./...`, `go test ./... -race`; tasks touching repositories, routes or `tests/integration` also run `make test-integration` against DynamoDB Local (`docker compose -f docker-compose.test.yml up -d`, port 8124).
- UI commands from `ui/`. `npm ci` first if `node_modules/@aoctech/ui/package.json` is not 0.4.1. `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"`; judge vitest by its exit code. Then `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`.
- **Never run the Go and UI suites at the same time** (one finishes before the other starts).
- UI tasks are executed with the `/impeccable` skill on `@aoctech/ui` components; every screen has **its own phone layout** (checked at 320, 375, 768 and 1280 px). pt-BR and en for every string. **No em dash (—) in any UI string**; the lone `—` for an empty table cell stays (DESIGN.md "Copy and controls").
- Routes and endpoints in English; Portuguese only in i18n text (spec D7). Routes, exactly: `GET/POST /v1.0/console/credentials`, `DELETE /v1.0/console/credentials/:client_id`, `GET/POST /v1.0/console/webhooks`, `DELETE /v1.0/console/webhooks/:id`, `POST /v1.0/console/webhooks/:id/rotate-secret`, `POST /v1.0/console/webhooks/:id/test`; UI pages `/console/credentials`, `/console/webhooks`.
- Scopes: reads `billing:organization:read`; writes `billing:organization:write`.
- Connect refusal codes, exactly, all `422`: `client_not_yours`, `client_has_no_billing_scope`, `client_not_machine_only`, `client_already_connected`. Other new codes: `credential_provisioned` (409), `webhook_endpoint_provisioned` (409), `webhook_endpoint_disabled` (409), `webhook_endpoint_limit` (422), `account_unavailable` (503). Reused: `tenant_suspended` (403, part 2), `concurrent_update` (409), `validation_error` (422), `resource_not_found` (404).
- m2m scopes, exactly (spec § 6.3): `billing:subscriptions:read`, `billing:subscriptions:write`, `billing:invoices:read`, `billing:invoices:write`, `billing:usage:write`, `billing:customers:read`, `billing:customers:write`, `billing:entitlements:read`, `billing:products:read`, `billing:products:write`, `billing:organization:read`. Never on `billing:my-*`, `billing:finance:*`, `billing:organization:write`.
- At most **5** webhook endpoints per tenant per mode; URL `https` only; signing secret returned once on create and once per rotate, stored sealed (`crypto.Sealer`, as today), never in any list or detail body. Endpoint ids `whe_{ULID}`.
- Credential row adds `connected_by` (user id) and `connected_at` (RFC 3339); endpoint row adds `added_by`, `added_at`.
- ctech-go-common: `oauthclients.ReadScope = "internal:account:oauth-client-read"`; release tag `v1.16.0`.
- Commit messages: Conventional Commits. **No attribution of any kind** (no `Co-Authored-By`, no "Generated with", no Claude/Anthropic mention) in commits or PR bodies, in every repository.

## Review Focus

1. **A person connecting a client they do not own, which is already connected to another merchant.** They must get `client_not_yours`, byte-identical to an unknown client, never `client_already_connected` (which would confirm the client is in use). Task 6 `TestConnectRefusalsNeverSayWhereAClientIs`.
2. **Two organizations connecting the same client at the same moment** (the owner admins both). Exactly one admission, the other `client_already_connected`; the M2M token then resolves to one tenant only. Task 4 `TestTwoTenantsConnectingOneClientAtOnceAdmitOne`.
3. **A webhook URL that looks public but resolves to a private address, or redirects to one.** No delivery reaches it; the attempt fails like an unreachable endpoint. Task 9 `TestAMerchantEndpointNeverDialsAPrivateAddress`, `TestAMerchantEndpointFollowsNoRedirect`.
4. **Eight endpoint creates fired at once on an empty tenant.** At most 5 exist afterwards. Task 8 `TestConcurrentAddsNeverPassTheLimit`.
5. **The secret leaking after its one response**: in the list, in the rotate's next list, in a delete body, or stored in the clear. Task 10 `TestTheSecretAppearsInExactlyOneResponsePerCreateOrRotate`.

---

## File Structure

| File | Responsibility |
|---|---|
| ctech-go-common `oauthclients/client.go`, `client_test.go` (new); `AGENTS.md`, `README.md` (modify) | shared reader of one OAuth client |
| ctech-account `ui/src/app/account/oauth-clients/prefill.ts`, `prefill.test.ts`, `oauth-clients.test.tsx` (new); `page.tsx`, `oauth-client-actions.tsx`, `ui/src/locales/{pt-BR,en}.json` (modify) | `?new=1&scopes=` preselection |
| `api/internal/oauthresource/scope-manifest.json`, `resource.go`, `resource_test.go` | `m2m` marks, `MachineScopes()` |
| `.github/workflows/README.md` | deploy section "Credentials, webhooks and m2m scopes" |
| `api/internal/domain/billing/credential.go`, `credential_test.go` | `ConnectedBy`, `ConnectedAt`, `Provisioned()` |
| `api/internal/domain/billing/audit.go` | five audit actions |
| `api/internal/repositories/credentials.go`, `keys.go` | guard row, `Connect`, `Get`, `ListForTenant`, `Disconnect`, `ErrCredentialTaken`; `EntityCredential`, `EntityWebhookEndpoint` |
| `api/internal/services/credentials.go`, `credentials_test.go` (new) | `CredentialConnector` and the connect rules |
| `api/go.mod`, `go.sum`; `api/internal/app/app.go`; `api/internal/api/v1/router.go` | `api-commons v1.16.0`, wiring, route registration |
| `api/internal/api/v1/console_credentials.go`, `console_integrations.go` (new); `validate.go`; `api/internal/problem/problem.go` | credential handlers, shared suspension check, `clientID` check, `TypeAccountUnavailable` |
| `api/internal/domain/billing/webhook.go`, `webhook_events.go`, `webhook_url.go` and tests (new/modify) | event catalogue, `EventPing`, `Event.EndpointID`, `AddedBy/AddedAt`, `Provisioned()`, `CheckWebhookURL`, `PublicAddr`, `NewWebhookSecret` |
| `api/internal/domain/id/id.go` | `PrefixWebhookEndpoint` |
| `api/internal/limits/limits.go`, `limits_test.go`; `ui/src/lib/limits.json` | `ClientID`, `WebhookURL`, `MaxWebhookEndpoints` |
| `api/internal/repositories/webhooks.go` | `AddEndpoint`, `RemoveEndpoint`, `RotateEndpointSecret`, `EmitPing`, errors |
| `api/internal/services/delivering.go`, `delivering_test.go` (new) | guarded client for merchant endpoints |
| `api/internal/api/v1/console_webhooks.go` (new) | webhook handlers |
| `api/tests/integration/fake_account_test.go` (modify) | the fake serves the OAuth client route |
| `api/tests/integration/credentials_repo_test.go`, `console_credentials_test.go`, `webhooks_repo_test.go`, `console_webhooks_test.go` (new) | § 10.4, § 10.5 and the rest |
| `README.md` | console route tables |
| `ui/src/lib/api/consoleTypes.ts`, `console.ts`, `console.test.ts`; `ui/src/lib/console/developerLink.ts`, `developerLink.test.ts`, `addsRefused.ts` (new); `ui/src/lib/format.ts`; `ui/src/locales/{pt-BR,en}/errors.json`; `ui/src/lib/errors.test.ts`; `ui/src/dev/consoleMockData.ts`, `mockRuntime.ts` | API layer, link, mock |
| `ui/src/app/(console)/console/credentials/{layout,page}.tsx`, `credentials.test.tsx` (new) | `/console/credentials` |
| `ui/src/app/(console)/console/webhooks/{layout,page}.tsx`, `webhooks.test.tsx`; `ui/src/components/console/SecretReveal.tsx` (new) | `/console/webhooks` |
| `ui/src/lib/console/nav.ts`, `nav.test.ts`; `ui/src/locales/{pt-BR,en}/console.json`; `ui/DESIGN.md` | nav entries, copy, design note |

---

## Cross-repo tasks

### Task 1: ctech-go-common — `oauthclients`, released as `v1.16.0`

Work in `/home/artur-revgas/Documents/Projects/Ctech/ctech-go-common` on a branch from `origin/main` (`feat/oauthclients`), PR into `main`, then tag.

**Files:**
- Create: `oauthclients/client.go`, `oauthclients/client_test.go`
- Modify: `AGENTS.md` (package list, after the `accountorgs` bullet), `README.md` (package table, after the `accountorgs` row)

**Interfaces:**
- Consumes: `gopkg.aoctech.app/api-commons/cache`, `oauth2client.New(hc, backend, tokenURL, clientID, clientSecret, scope)`, `(*oauth2client.TokenManager).Get(ctx)`.
- Produces (billing Task 5 uses these exactly):

```go
package oauthclients
const ReadScope = "internal:account:oauth-client-read"
var ErrNotFound error
type Config struct{ BaseURL, TokenURL, ClientID, ClientSecret string; Cache cache.Backend }
type OAuthClient struct {
	ClientID      string   `json:"client_id"`
	OwnerUserID   string   `json:"owner_user_id"`
	AllowedScopes []string `json:"allowed_scopes"` // never nil
	Active        bool     `json:"active"`
}
func New(cfg Config) *Client                    // nil when the config is incomplete
func (c *Client) Enabled() bool
func (c *Client) Get(ctx context.Context, clientID string) (OAuthClient, error)
```

- [ ] **Step 1: Write the failing tests** — `oauthclients/client_test.go`

```go
package oauthclients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve answers every request with status, content type and body, and records
// the request URI. Tests call getWithToken, so no token endpoint is needed.
func serve(t *testing.T, status int, contentType, body string) (*Client, *string) {
	t.Helper()
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), baseURL: srv.URL}, &gotURI
}

func TestGetReadsTheFourFieldsAndEscapesTheID(t *testing.T) {
	c, uri := serve(t, http.StatusOK, "application/json",
		`{"client_id":"a b","owner_user_id":"usr_1","allowed_scopes":["billing:invoices:read"],"active":true}`)
	got, err := c.getWithToken(context.Background(), "tok", "a b")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "a b" || got.OwnerUserID != "usr_1" || !got.Active ||
		len(got.AllowedScopes) != 1 || got.AllowedScopes[0] != "billing:invoices:read" {
		t.Fatalf("got %+v", got)
	}
	if *uri != "/v1.0/internal/oauth-clients/a%20b" {
		t.Fatalf("request URI = %s", *uri)
	}
}

func TestNullScopesBecomeAnEmptyList(t *testing.T) {
	c, _ := serve(t, http.StatusOK, "application/json", `{"client_id":"c","owner_user_id":"u","allowed_scopes":null,"active":false}`)
	got, err := c.getWithToken(context.Background(), "tok", "c")
	if err != nil || got.AllowedScopes == nil || len(got.AllowedScopes) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// ctech-account answers an unknown client with an RFC 7807 404. A route that
// does not exist answers Fiber's plain-text 404: that is a broken deployment,
// and reading it as "unknown client" would refuse every merchant silently.
func TestAProblem404IsNotFoundAndAPlain404IsAnError(t *testing.T) {
	c, _ := serve(t, http.StatusNotFound, "application/problem+json; charset=utf-8", `{"status":404}`)
	if _, err := c.getWithToken(context.Background(), "tok", "c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("problem 404: err = %v, want ErrNotFound", err)
	}
	c, _ = serve(t, http.StatusNotFound, "text/plain; charset=utf-8", "Cannot GET")
	if _, err := c.getWithToken(context.Background(), "tok", "c"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("plain 404: err = %v, want a non-NotFound error", err)
	}
}

func TestEveryOtherFailureIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name, ct, body string
		status         int
	}{
		{"forbidden", "application/problem+json", `{}`, http.StatusForbidden},
		{"outage", "text/plain", "boom", http.StatusBadGateway},
		{"garbage", "application/json", "{", http.StatusOK},
		{"another client", "application/json", `{"client_id":"other","owner_user_id":"u","allowed_scopes":[],"active":true}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := serve(t, tc.status, tc.ct, tc.body)
			if _, err := c.getWithToken(context.Background(), "tok", "c"); err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want a non-NotFound error", err)
			}
		})
	}
}

func TestANilClientIsNeverPermissive(t *testing.T) {
	var c *Client
	if c.Enabled() {
		t.Fatal("nil client enabled")
	}
	if _, err := c.Get(context.Background(), "c"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if New(Config{BaseURL: "x"}) != nil {
		t.Fatal("incomplete config built a client")
	}
}

// No creation path at ctech-account makes an id longer than 128 characters or
// an empty one; asking would cost a token and a round trip to learn "404".
func TestImpossibleIDsAreNotFoundWithoutARequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the client asked ctech-account")
	}))
	t.Cleanup(srv.Close)
	c := &Client{http: srv.Client(), baseURL: srv.URL}
	for _, id := range []string{"", strings.Repeat("a", 129)} {
		if _, err := c.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id of length %d: err = %v", len(id), err)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./oauthclients/ -race -v`
Expected: FAIL, `undefined: Client` (package does not exist yet).

- [ ] **Step 3: Implement** — `oauthclients/client.go`

```go
// Package oauthclients is the M2M client for ctech-account's read of one OAuth
// client:
//
//	GET /v1.0/internal/oauth-clients/{client_id}  scope internal:account:oauth-client-read
//
// A product calls it before it accepts a client as somebody's credential, to
// learn who owns it, what it was granted and whether it can still authenticate
// as itself. The secret never crosses this route.
//
// It fails closed like accountorgs: a nil client, a token failure, an outage,
// a non-200 other than a problem+json 404, an unreadable body and an answer
// about another client are errors. An unknown client is ErrNotFound, told apart
// from a missing route by the problem+json body ctech-account answers it with.
package oauthclients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/oauth2client"
)

// ReadScope is what the route requires.
const ReadScope = "internal:account:oauth-client-read"

const (
	requestTimeout = 6 * time.Second
	maxBody        = 64 << 10
	// maxClientIDLength is the longest id any ctech-account creation path makes.
	maxClientIDLength = 128
)

// ErrNotFound is an unknown client (or an id no creation path can produce).
var ErrNotFound = errors.New("oauth client not found")

// Config is what the client needs to reach ctech-account.
type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Cache        cache.Backend
}

// Client reads OAuth clients from ctech-account.
type Client struct {
	http    *http.Client
	tokens  *oauth2client.TokenManager
	baseURL string
}

// OAuthClient is ctech-account's answer. AllowedScopes is never nil.
type OAuthClient struct {
	ClientID      string   `json:"client_id"`
	OwnerUserID   string   `json:"owner_user_id"`
	AllowedScopes []string `json:"allowed_scopes"`
	Active        bool     `json:"active"`
}

// New builds a client, or returns nil when the credential is not configured.
// A nil client is NOT permissive: every call on nil is an error.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	hc := &http.Client{Timeout: requestTimeout}
	return &Client{
		http:    hc,
		tokens:  oauth2client.New(hc, cfg.Cache, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, ReadScope),
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
	}
}

// Enabled reports a client that can actually ask.
func (c *Client) Enabled() bool { return c != nil }

// Get reads one client. ErrNotFound for an unknown client; any other error is
// an outage the caller must refuse on.
func (c *Client) Get(ctx context.Context, clientID string) (OAuthClient, error) {
	if c == nil {
		return OAuthClient{}, fmt.Errorf("ctech-account oauth client reader is not configured")
	}
	if clientID == "" || len(clientID) > maxClientIDLength {
		return OAuthClient{}, ErrNotFound
	}
	token, err := c.tokens.Get(ctx)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("minting a service token: %w", err)
	}
	return c.getWithToken(ctx, token, clientID)
}

func (c *Client) getWithToken(ctx context.Context, token, clientID string) (OAuthClient, error) {
	path := fmt.Sprintf("%s/v1.0/internal/oauth-clients/%s", c.baseURL, url.PathEscape(clientID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("calling ctech-account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound && isProblem(resp.Header.Get("Content-Type")):
		return OAuthClient{}, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return OAuthClient{}, fmt.Errorf("ctech-account answered %d", resp.StatusCode)
	}
	var out OAuthClient
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return OAuthClient{}, fmt.Errorf("decoding the answer: %w", err)
	}
	if out.ClientID != clientID {
		return OAuthClient{}, fmt.Errorf("ctech-account answered for client %q, asked about %q", out.ClientID, clientID)
	}
	if out.AllowedScopes == nil {
		out.AllowedScopes = []string{}
	}
	return out, nil
}

func isProblem(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "application/problem+json"
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l . && go vet ./... && go test ./... -race`
Expected: no gofmt output; `ok gopkg.aoctech.app/api-commons/oauthclients`.

- [ ] **Step 5: Document** — `AGENTS.md`, after the `accountorgs` bullet:

```markdown
- `oauthclients` — M2M client for ctech-account's `GET /v1.0/internal/oauth-clients/{client_id}` (scope
  `ReadScope` = `internal:account:oauth-client-read`): `Get` returns `OAuthClient{ClientID, OwnerUserID,
  AllowedScopes, Active}` (never the secret; `AllowedScopes` never nil). An unknown client is `ErrNotFound`, told
  apart from a missing route by ctech-account's `application/problem+json` 404; every other non-200, a bad body
  and an answer about another id are errors. Empty or >128-character ids are `ErrNotFound` without a request.
  `New` returns nil on an incomplete `Config` and a nil `*Client` errors (never permission). No answer caching.
  Since `v1.16.0`; first consumer ctech-billing (console credentials).
```

`README.md`, package table, after `accountorgs`:

```markdown
| `oauthclients` | M2M reader of one ctech-account OAuth client (`owner_user_id`, `allowed_scopes`, `active`, never the secret) for products that admit a person's client as a credential; fail-closed, unknown client is `ErrNotFound` |
```

- [ ] **Step 6: Commit, PR, release**

```bash
git add oauthclients AGENTS.md README.md
git commit -m "feat(oauthclients): shared reader for ctech-account's internal OAuth client route"
git push -u origin feat/oauthclients
gh pr create --title "feat(oauthclients): shared reader for ctech-account's internal OAuth client route" --body "Reader for GET /v1.0/internal/oauth-clients/{client_id} (scope internal:account:oauth-client-read), same fail-closed shape as accountorgs. First consumer: ctech-billing console credentials (console self-service spec part 3)."
```

After the PR is merged:

```bash
git checkout main && git pull --ff-only
git tag v1.16.0 && git push origin v1.16.0
GOPROXY=https://proxy.golang.org go list -m gopkg.aoctech.app/api-commons@v1.16.0
```

Expected: the last command prints `gopkg.aoctech.app/api-commons v1.16.0`.

---

### Task 2: ctech-account — the developer page opens "new client" with given scopes

Work in `/home/artur-revgas/Documents/Projects/Ctech/ctech-account` on a branch from `origin/main` (`feat/oauth-client-prefill`), PR into `main`. Independent of Task 1; billing works without it (Task 12 says which scopes to tick).

**Files:**
- Create: `ui/src/app/account/oauth-clients/prefill.ts`, `ui/src/app/account/oauth-clients/prefill.test.ts`, `ui/src/app/account/oauth-clients/oauth-clients.test.tsx`
- Modify: `ui/src/app/account/oauth-clients/page.tsx`, `ui/src/app/account/oauth-clients/oauth-client-actions.tsx` (`CreateOAuthClientDialog`), `ui/src/locales/pt-BR.json`, `ui/src/locales/en.json`

**Interfaces:**
- Consumes: `CreateOAuthClientDialog`, `ScopePicker`, `useSearchParams` (existing).
- Produces: URL contract `https://{accounts}/account/oauth-clients?new=1&scopes=a,b,c` (billing Task 11 builds it); `readClientPrefill(params: URLSearchParams): ClientPrefill`; `CreateOAuthClientDialog({prefill, onClosed}: {prefill?: ClientPrefill; onClosed?: () => void})`.

- [ ] **Step 1: Write the failing tests**

`ui/src/app/account/oauth-clients/prefill.test.ts`:

```ts
import {describe, expect, it} from 'vitest'
import {readClientPrefill} from './prefill'

describe('readClientPrefill', () => {
  it('opens with exactly the given scopes, deduplicated', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=billing:invoices:read,billing:usage:write,billing:invoices:read')))
      .toEqual({open: true, scopes: ['billing:invoices:read', 'billing:usage:write']})
  })
  it('drops anything that is not shaped like a scope', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=openid,<script>,billing:products:read')).scopes)
      .toEqual(['billing:products:read'])
  })
  it('keeps the defaults when no usable scope is given', () => {
    expect(readClientPrefill(new URLSearchParams('new=1&scopes=,,'))).toEqual({open: true, scopes: null})
  })
  it('does nothing without new=1', () => {
    expect(readClientPrefill(new URLSearchParams('scopes=billing:invoices:read'))).toEqual({open: false, scopes: null})
  })
  it('caps the list at 32', () => {
    const many = Array.from({length: 40}, (_, i) => `billing:s${i}:read`).join(',')
    expect(readClientPrefill(new URLSearchParams(`new=1&scopes=${many}`)).scopes).toHaveLength(32)
  })
})
```

(`openid` has no colon, so it is not scope-shaped here: a product link asks for resource scopes only; identity scopes stay the dialog's default when nothing is given.)

`ui/src/app/account/oauth-clients/oauth-clients.test.tsx`:

```tsx
import {cleanup, render, screen} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {CreateOAuthClientDialog} from './oauth-client-actions'

vi.mock('@/components/scope-picker', () => ({
  ScopePicker: ({value}: {value: string[]}) => <output data-testid="scopes">{value.join(' ')}</output>,
}))

afterEach(cleanup)

function renderDialog(prefill?: {open: boolean; scopes: string[] | null}) {
  const client = new QueryClient()
  return render(<QueryClientProvider client={client}><CreateOAuthClientDialog prefill={prefill}/></QueryClientProvider>)
}

describe('CreateOAuthClientDialog prefill', () => {
  it('opens at once with the given scopes and says where they came from', async () => {
    renderDialog({open: true, scopes: ['billing:invoices:read']})
    expect(await screen.findByTestId('scopes')).toHaveTextContent('billing:invoices:read')
    expect(screen.getByTestId('scopes')).not.toHaveTextContent('openid')
    expect(screen.getByText(/acesso de máquina|machine access/i)).toBeInTheDocument()
  })
  it('stays closed without a prefill', () => {
    renderDialog()
    expect(screen.queryByTestId('scopes')).toBeNull()
  })
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/app/account/oauth-clients/; echo "vitest exit=$?"`
Expected: `vitest exit=1` (missing `./prefill`; `prefill` prop ignored).

- [ ] **Step 3: Implement**

`ui/src/app/account/oauth-clients/prefill.ts`:

```ts
/**
 * A product's link to "new client with these scopes" (ctech-billing console
 * self-service spec § 5.1): `?new=1&scopes=a,b`. Only scope-shaped tokens are
 * kept; the server validates every scope again on create, so this is a
 * convenience, never authority.
 */
const SCOPE = /^[a-z][a-z0-9-]*:[a-z0-9:_-]+$/
const MAX = 32

export type ClientPrefill = {open: boolean; scopes: string[] | null}

export function readClientPrefill(params: URLSearchParams): ClientPrefill {
  const open = params.get('new') === '1'
  const raw = params.get('scopes')
  if (!open || raw === null) return {open, scopes: null}
  const scopes = [...new Set(raw.split(',').map((s) => s.trim()).filter((s) => SCOPE.test(s)))].slice(0, MAX)
  return {open, scopes: scopes.length > 0 ? scopes : null}
}
```

`oauth-client-actions.tsx`, `CreateOAuthClientDialog`:

```tsx
export function CreateOAuthClientDialog({prefill, onClosed}: {prefill?: ClientPrefill; onClosed?: () => void} = {}) {
  const { t } = useTranslation()
  const initialScopes = prefill?.scopes ?? DEFAULT_CLIENT_SCOPES
  const [open, setOpen] = useState(prefill?.open ?? false)
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)
  const [scopes, setScopes] = useState<string[]>(initialScopes)
  // … unchanged mutation and handleSubmit …

  function handleClose() {
    setOpen(false)
    setCreatedSecret(null)
    setScopes(DEFAULT_CLIENT_SCOPES)
    reset()
    onClosed?.()
  }
```

and, inside the form, directly above `<ClientFormFields …/>`:

```tsx
            {prefill?.scopes && (
              <Alert>
                <AlertDescription className="text-xs">{t('oauthClients.dialog.prefillHint')}</AlertDescription>
              </Alert>
            )}
```

(import `type ClientPrefill` from `./prefill`.)

`page.tsx`: `useSearchParams` needs a Suspense boundary in a static export. Rename the current component to `OAuthClients`, and:

```tsx
import { Suspense } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { readClientPrefill } from './prefill'

export default function OAuthClientsPage() {
  return (
    <Suspense fallback={<div className="h-24 animate-pulse bg-muted rounded-lg" />}>
      <OAuthClients />
    </Suspense>
  )
}

function OAuthClients() {
  const router = useRouter()
  const prefill = readClientPrefill(useSearchParams())
  // … existing body; the header's dialog becomes:
  //   <CreateOAuthClientDialog prefill={prefill} onClosed={() => {
  //     if (prefill.open) router.replace('/account/oauth-clients')
  //   }} />
```

(The `replace` drops the parameters, so closing and pressing "Novo cliente" again starts from the defaults, and Back does not reopen the dialog.)

Locales (`oauthClients.dialog` object):

```json
"prefillHint": "Escopos sugeridos pelo produto que trouxe você aqui. Para acesso de máquina, mantenha só esses escopos e escolha o tipo confidencial."
```

```json
"prefillHint": "Scopes suggested by the product that sent you here. For machine access, keep only these scopes and choose the confidential type."
```

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 src/app/account/oauth-clients/; echo "vitest exit=$?"` then `npx tsc --noEmit && npm run lint && npm run build`
Expected: `vitest exit=0`; tsc, lint and build clean.

- [ ] **Step 5: Commit and PR**

```bash
git add ui/src/app/account/oauth-clients ui/src/locales
git commit -m "feat(oauth-clients): open the new-client dialog with scopes from ?new=1&scopes="
git push -u origin feat/oauth-client-prefill
gh pr create --title "feat(oauth-clients): open the new-client dialog with scopes from ?new=1&scopes=" --body "ctech-billing's console links merchants here to create a machine-only client with billing's m2m scopes preselected (billing console self-service spec part 3, § 5.1). Scope-shaped tokens only, capped at 32; the server still validates every scope."
```

---

## Billing API tasks (`api/`)

Before Task 3, implement nothing of this plan until part 1 and part 2 are merged on `main`, and run the two `grep` checks in **Dependencies on other plans**.

### Task 3: The m2m marks and `MachineScopes`

**Files:**
- Modify: `api/internal/oauthresource/scope-manifest.json` (11 entries)
- Modify: `api/internal/oauthresource/resource.go` (`Scope.M2M`, `MachineScopes`)
- Test: `api/internal/oauthresource/resource_test.go`
- Modify: `.github/workflows/README.md` (new section, end of file)

**Interfaces:**
- Consumes: the embedded manifest.
- Produces: `Scope.M2M bool` (`json:"m2m,omitempty"`); `func MachineScopes() ([]string, error)` — the public, active, `m2m` scopes, sorted. Tasks 5 and 6 use it.

- [ ] **Step 1: Write the failing tests** — append to `resource_test.go`

```go
// Spec § 6.3: the scopes a merchant's machine client may hold. ctech-account
// issues a client_credentials token to a developer-page client only when every
// scope it holds is one of these.
func TestMachineScopesAreExactlyTheIntegrationScopes(t *testing.T) {
	want := []string{
		"billing:customers:read", "billing:customers:write",
		"billing:entitlements:read",
		"billing:invoices:read", "billing:invoices:write",
		"billing:organization:read",
		"billing:products:read", "billing:products:write",
		"billing:subscriptions:read", "billing:subscriptions:write",
		"billing:usage:write",
	}
	got, err := MachineScopes()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("MachineScopes() = %v\nwant %v", got, want)
	}
}

// A user scope marked m2m would let a merchant's server act as a person, and
// ctech-account refuses m2m on anything but a public scope.
func TestNoUserScopeIsAMachineScope(t *testing.T) {
	m, err := ManifestDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Scopes {
		user := strings.HasPrefix(s.Name, "billing:my-") || strings.HasPrefix(s.Name, "billing:finance:") ||
			s.Name == "billing:organization:write"
		if s.M2M && (user || s.Visibility != "public") {
			t.Errorf("%s is marked m2m", s.Name)
		}
	}
}
```

(imports `slices`, `strings`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/oauthresource/ -run 'Machine|UserScope' -v`
Expected: FAIL, `undefined: MachineScopes`.

- [ ] **Step 3: Implement**

`resource.go`:

```go
type Scope struct {
	Name         string            `json:"name"`
	Descriptions map[string]string `json:"descriptions"`
	Visibility   string            `json:"visibility"`
	Status       string            `json:"status"`
	// M2M marks a scope ctech-account may grant to a merchant's own machine
	// client through client_credentials (console self-service spec § 6.3). A
	// developer-page client gets a token only when every scope it holds is one.
	M2M bool `json:"m2m,omitempty"`
}

// MachineScopes returns the public, active scopes marked m2m, sorted. The
// console admits a client as a credential only if it holds nothing else.
func MachineScopes() ([]string, error) {
	m, err := ManifestDocument()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, s := range m.Scopes {
		if s.M2M && s.Visibility == "public" && s.Status == "active" {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}
```

`scope-manifest.json`: add `"m2m": true` as the last key of exactly the 11 entries in **Global Constraints**, for example:

```json
    {
      "name": "billing:subscriptions:read",
      "descriptions": {
        "pt-BR": "Consultar assinaturas da organização.",
        "en": "Read the organization's subscriptions."
      },
      "visibility": "public",
      "status": "active",
      "m2m": true
    },
```

Leave `billing:my-*`, `billing:finance:*` and `billing:organization:write` untouched.

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/oauthresource/ -race -v && gofmt -l ./internal && go vet ./...`
Expected: PASS (the existing manifest tests too); no gofmt output.

- [ ] **Step 5: Deploy section** — append to `.github/workflows/README.md`:

```markdown
## Credentials, webhooks and m2m scopes (console self-service part 3)

This change marks billing's integration scopes `"m2m": true` in
`api/internal/oauthresource/scope-manifest.json`, which the **OAuth scopes**
stage publishes to ctech-account on every branch it reaches. ctech-account
must already understand the field in that environment: an older ctech-account
stores the manifest without it, and a later identical publish does not resend
it (same hash).

Per environment, in this order (`dev`, then `stage`, then `prod`):

1. ctech-account's console self-service deploy (its plan
   `docs/plans/2026-10-10-oauth-client-internal-route.md`, "Deploy") is live:
   the internal route answers, `createclient -grant` has given billing's client
   (`/ctech-billing/{env}/billing/account-client-id`)
   `internal:account:oauth-client-read`, and ctech-account's runbook step 6 said
   billing may publish.
2. Only then does this change reach the branch that deploys the environment
   (`dev` / `staging` / `main`). The pipeline publishes the scopes before the
   API.
3. Check the publish:
   `curl -s https://<accounts-api>/v1.0/scopes | jq '..|objects|select(.scope? == "billing:invoices:read")'`
   shows `"m2m": true`. If it does not, ctech-account was older at publish
   time: run **Deploy** by hand with only `scopes` ticked.
4. Smoke test in **test mode** with a person who administers a merchant
   organization: create a confidential client on the developer page with only
   billing m2m scopes, connect it on `/console/credentials`, get a token with
   `grant_type=client_credentials`, call `GET /v1.0/invoices` and expect `200`.
   Add a webhook to a request bin over https, press **Testar**, and expect one
   `ping` within two minutes.

If the internal route is missing or billing's client lacks the scope, connect
answers `503 account_unavailable`; nothing else is affected. Rollback is a
revert: connected credentials keep working (they are ordinary credential rows)
and the published marks stay until the next publish.
```

- [ ] **Step 6: Commit**

```bash
git add api/internal/oauthresource .github/workflows/README.md
git commit -m "feat(scopes): mark billing's integration scopes m2m for merchant machine clients"
```

---

### Task 4: Credentials are connected and disconnected, one tenant per client

**Files:**
- Modify: `api/internal/domain/billing/credential.go`; Test: `api/internal/domain/billing/credential_test.go` (create if absent)
- Modify: `api/internal/domain/billing/audit.go`
- Modify: `api/internal/repositories/keys.go` (two `Entity` values), `api/internal/repositories/credentials.go`
- Test: `api/tests/integration/credentials_repo_test.go` (new)

**Interfaces:**
- Consumes: `LookupCredentialPK`, `TenantPK`, `CredentialSK`, `buildAuditItem`, `AuditEntry`, `RetentionCredential`, `newKeys`, `Encode`, `DecodeItems`, `IsConditionFailed`, `TableAudit`.
- Produces:

```go
// billing
type APICredential struct { /* existing */ ConnectedBy string; ConnectedAt string }
func (c *APICredential) Provisioned() bool
const AuditCredentialConnected = "credential.connected"
const AuditCredentialDisconnected = "credential.disconnected"
const AuditWebhookEndpointCreated = "webhook_endpoint.created"
const AuditWebhookEndpointDeleted = "webhook_endpoint.deleted"
const AuditWebhookEndpointSecretRotated = "webhook_endpoint.secret_rotated"
// repositories
const EntityCredential Entity = "CREDENTIAL"
const EntityWebhookEndpoint Entity = "WEBHOOK_ENDPOINT"
var ErrCredentialTaken error
var ErrCredentialProvisioned error
func (r *CredentialRepository) Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error
func (r *CredentialRepository) Get(ctx context.Context, organizationID string, livemode bool, clientID string) (*billing.APICredential, error)
func (r *CredentialRepository) ListForTenant(ctx context.Context, organizationID string, livemode bool) ([]billing.APICredential, error)
func (r *CredentialRepository) Disconnect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error
// Create keeps its signature and now writes the guard; a taken client is ErrCredentialTaken (wrapped).
```

- [ ] **Step 1: Write the failing tests**

`api/internal/domain/billing/credential_test.go`:

```go
package billing

import "testing"

func TestACredentialWithoutAConnectorWasProvisioned(t *testing.T) {
	if !(&APICredential{ClientID: "dfe-billing"}).Provisioned() {
		t.Fatal("a seeded credential is not provisioned")
	}
	if (&APICredential{ClientID: "c", ConnectedBy: "usr_1"}).Provisioned() {
		t.Fatal("a console credential is provisioned")
	}
}
```

`api/tests/integration/credentials_repo_test.go`:

```go
//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func consoleCred(org *billing.Organization, clientID string) *billing.APICredential {
	return &billing.APICredential{
		ClientID: clientID, OrganizationID: org.ID, Livemode: org.Livemode, Active: true,
		ConnectedBy: "usr_" + id.New(), ConnectedAt: now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func TestAConnectedCredentialResolvesListsAndIsAudited(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	org := newOrg(t, true)
	clientID := "cli_" + id.New()
	cred := consoleCred(org, clientID)
	if err := repo.Connect(ctx, cred, "user:"+cred.ConnectedBy, "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Resolve(ctx, clientID)
	if err != nil || got.OrganizationID != org.ID || got.ConnectedBy != cred.ConnectedBy {
		t.Fatalf("Resolve = %+v, %v", got, err)
	}
	list, err := repo.ListForTenant(ctx, org.ID, true)
	if err != nil || len(list) != 1 || list[0].ClientID != clientID {
		t.Fatalf("ListForTenant = %+v, %v", list, err)
	}
	audit := repositories.NewAuditRepository(testDB, testCfg)
	entries, err := audit.ListForEntity(ctx, org.ID, true, clientID, 10)
	if err != nil || len(entries) != 1 || entries[0].Action != billing.AuditCredentialConnected {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
}

// Review Focus 2: the owner administers two organizations and connects one
// client to both at the same moment.
func TestTwoTenantsConnectingOneClientAtOnceAdmitOne(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, org := range []*billing.Organization{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.Connect(ctx, consoleCred(org, clientID), "user:u", "req", now())
		}()
	}
	wg.Wait()
	ok, taken := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, repositories.ErrCredentialTaken):
			taken++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("ok=%d taken=%d, want 1 and 1", ok, taken)
	}
}

func TestDisconnectFreesTheClientForAnotherTenant(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	cred := consoleCred(a, clientID)
	if err := repo.Connect(ctx, cred, "user:u", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Disconnect(ctx, cred, "user:u", "req", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(ctx, clientID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("Resolve after disconnect: %v", err)
	}
	if err := repo.Connect(ctx, consoleCred(b, clientID), "user:u", "req", now()); err != nil {
		t.Fatalf("reconnect elsewhere: %v", err)
	}
}

func TestAProvisionedCredentialIsNeverDisconnected(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	org := newOrg(t, true)
	cred := &billing.APICredential{ClientID: "cli_" + id.New(), OrganizationID: org.ID, Livemode: true, Active: true}
	if err := repo.Create(ctx, cred, now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Disconnect(ctx, cred, "user:u", "req", now()); !errors.Is(err, repositories.ErrCredentialProvisioned) {
		t.Fatalf("err = %v, want ErrCredentialProvisioned", err)
	}
	if _, err := repo.Resolve(ctx, cred.ClientID); err != nil {
		t.Fatalf("the provisioned credential is gone: %v", err)
	}
}

// The seed path writes the guard too: a console connect cannot slip a second
// row in behind a seeded client.
func TestCreateClaimsTheClientToo(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	if err := repo.Create(ctx, &billing.APICredential{ClientID: clientID, OrganizationID: a.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Connect(ctx, consoleCred(b, clientID), "user:u", "req", now()); !errors.Is(err, repositories.ErrCredentialTaken) {
		t.Fatalf("err = %v, want ErrCredentialTaken", err)
	}
}
```


- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/domain/billing/ -run Provisioned -v` and `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Credential|TwoTenants|Disconnect|ClaimsTheClient' -v`
Expected: build failures (`Provisioned`, `Connect`, `ErrCredentialTaken` undefined).

- [ ] **Step 3: Implement**

`credential.go`, inside `APICredential` after `Active`:

```go
	// ConnectedBy is the ctech-account user who connected this client from the
	// console (spec § 5.1), ConnectedAt when (RFC 3339). Both are empty on a
	// credential the tenant plan provisioned. The credential belongs to the
	// tenant: the connector leaving the organization does not remove it.
	ConnectedBy string `dynamodbav:"connected_by,omitempty" json:"connected_by,omitempty"`
	ConnectedAt string `dynamodbav:"connected_at,omitempty" json:"connected_at,omitempty"`
```

and below `Allows`:

```go
// Provisioned reports a credential the tenant plan created. The console lists
// it and never removes it: it is how a CTech service reaches tenant zero.
func (c *APICredential) Provisioned() bool { return c.ConnectedBy == "" }
```

`audit.go`, inside the constants block:

```go
	// AuditCredentialConnected and AuditCredentialDisconnected record a
	// merchant's own OAuth client being admitted to, or removed from, a tenant
	// from the console (spec § 5.1).
	AuditCredentialConnected    = "credential.connected"
	AuditCredentialDisconnected = "credential.disconnected"
	// The console's webhook endpoint writes (spec § 5.2). The secret is never
	// in the audit row; only that it changed.
	AuditWebhookEndpointCreated       = "webhook_endpoint.created"
	AuditWebhookEndpointDeleted       = "webhook_endpoint.deleted"
	AuditWebhookEndpointSecretRotated = "webhook_endpoint.secret_rotated"
```

`keys.go`, in the `Entity` block after `EntityAudit`:

```go
	// EntityCredential and EntityWebhookEndpoint name audit subjects only;
	// neither row type is in a period index.
	EntityCredential      Entity = "CREDENTIAL"
	EntityWebhookEndpoint Entity = "WEBHOOK_ENDPOINT"
```

`credentials.go` — the repository gains the audit table, the guard and four methods. Replace the struct, constructor and `Create`, and append the rest:

```go
type CredentialRepository struct {
	base  Base
	audit Base
}

func NewCredentialRepository(db *dynamodb.Client, cfg *config.Config) *CredentialRepository {
	return &CredentialRepository{base: NewBase(db, cfg, TableCredentials), audit: NewBase(db, cfg, TableAudit)}
}

// skCredentialGuard is the sort key of the row that claims a client id for one
// tenant. Resolve is a query on a global index, and an index cannot hold a
// condition: two tenants admitting the same client at once would both find
// nothing and both write. The guard is a plain item keyed by the client id, so
// "absent" is a condition DynamoDB checks inside the transaction. It carries
// no lookup_pk, so it never enters the lookup index.
const skCredentialGuard = "GUARD"

// ErrCredentialTaken reports a client id already admitted to a tenant.
var ErrCredentialTaken = errors.New("client already admitted to a tenant")

// ErrCredentialProvisioned reports a credential the tenant plan created, which
// no console route removes.
var ErrCredentialProvisioned = errors.New("credential provisioned by the tenant plan")

type credentialGuardRow struct {
	keys
	ClientID string `dynamodbav:"client_id"`
}

func (r *CredentialRepository) admitItems(cred *billing.APICredential, now time.Time) ([]types.TransactWriteItem, error) {
	if cred.ClientID == "" || cred.OrganizationID == "" {
		return nil, fmt.Errorf("credential needs a client id and an organization")
	}
	item, err := Encode(credentialRow{
		keys:          newKeys(TenantPK(cred.OrganizationID, cred.Livemode), CredentialSK(cred.ClientID), RetentionCredential, now),
		APICredential: *cred,
		LookupPK:      LookupCredentialPK(cred.ClientID),
	})
	if err != nil {
		return nil, err
	}
	guard, err := Encode(credentialGuardRow{
		keys:     newKeys(LookupCredentialPK(cred.ClientID), skCredentialGuard, RetentionCredential, now),
		ClientID: cred.ClientID,
	})
	if err != nil {
		return nil, err
	}
	return txItems(r.base.BuildPutTxItemIfAbsent(item), r.base.BuildPutTxItemIfAbsent(guard)), nil
}

// Create registers a credential for a tenant from the tenant plan (ADR 0007).
func (r *CredentialRepository) Create(ctx context.Context, cred *billing.APICredential, now time.Time) error {
	items, err := r.admitItems(cred, now)
	if err != nil {
		return err
	}
	return r.admit(ctx, cred.ClientID, items)
}

// admit writes the admission. Two admissions of one client racing on the
// guard can cancel each other with a TransactionConflict rather than a
// condition failure; retrying turns the loser's answer into the condition
// failure it really is ("taken").
func (r *CredentialRepository) admit(ctx context.Context, clientID string, items []types.TransactWriteItem) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = r.base.TransactWrite(ctx, items)
		if !IsTransactionConflict(err) {
			break
		}
	}
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrCredentialTaken, clientID)
	}
	return conflictErr(err)
}

// Connect admits a merchant's own client from the console: the credential,
// the guard and the audit row, in one transaction. The caller has already
// checked the client with ctech-account (services.CredentialConnector).
func (r *CredentialRepository) Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error {
	if cred.ConnectedBy == "" || actor == "" {
		return fmt.Errorf("repositories: connecting credential %s needs who connected it", cred.ClientID)
	}
	items, err := r.admitItems(cred, now)
	if err != nil {
		return err
	}
	audit, err := buildAuditItem(cred.OrganizationID, cred.Livemode, AuditEntry{
		Entity: EntityCredential, EntityID: cred.ClientID, Action: billing.AuditCredentialConnected,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, "", "connected", now)
	if err != nil {
		return err
	}
	return r.admit(ctx, cred.ClientID, append(items, r.audit.BuildPutTxItemIfAbsent(audit)))
}

// Get reads one credential of a tenant.
func (r *CredentialRepository) Get(ctx context.Context, organizationID string, livemode bool, clientID string) (*billing.APICredential, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), CredentialSK(clientID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: credential %s", ErrNotFound, clientID)
	}
	row, err := Decode[credentialRow](item)
	if err != nil {
		return nil, err
	}
	return &row.APICredential, nil
}

// ListForTenant returns every credential of one tenant and mode, provisioned
// and inactive ones included, in client id order. Every page is read: a
// merchant's credential list is short, and a silently truncated one would hide
// the credential somebody is looking for.
func (r *CredentialRepository) ListForTenant(ctx context.Context, organizationID string, livemode bool) ([]billing.APICredential, error) {
	out := []billing.APICredential{}
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.Query(ctx, QueryOpts{
			PK: TenantPK(organizationID, livemode), SKPrefix: skCredential,
			ScanIndexForward: true, Limit: 100, ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[credentialRow](res.Items)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.APICredential)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// Disconnect removes a console-connected credential and its guard, with the
// audit row. A provisioned credential is refused here as well as in the
// handler: the condition on connected_by is what holds if a handler forgets.
func (r *CredentialRepository) Disconnect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error {
	if cred.Provisioned() {
		return fmt.Errorf("%w: %s", ErrCredentialProvisioned, cred.ClientID)
	}
	sk := CredentialSK(cred.ClientID)
	del := types.TransactWriteItem{Delete: &types.Delete{
		TableName: aws.String(r.base.TableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: TenantPK(cred.OrganizationID, cred.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(connected_by)"),
	}}
	audit, err := buildAuditItem(cred.OrganizationID, cred.Livemode, AuditEntry{
		Entity: EntityCredential, EntityID: cred.ClientID, Action: billing.AuditCredentialDisconnected,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, "connected", "disconnected", now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(
		del,
		r.base.BuildDeleteTxItem(LookupCredentialPK(cred.ClientID), skCredentialGuard),
		r.audit.BuildPutTxItemIfAbsent(audit),
	))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: credential %s", ErrNotFound, cred.ClientID)
	}
	return err
}
```

(imports: add `github.com/aws/aws-sdk-go-v2/aws`.) `Resolve`, `Deactivate`, `SetOwnerKey` are unchanged.

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/... -race` then `make test-integration`
Expected: PASS, including every existing test that seeds credentials through `Create`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/billing api/internal/repositories api/tests/integration/credentials_repo_test.go
git commit -m "feat(credentials): connect and disconnect from the console, one tenant per client by a guard row"
```

---

### Task 5: `CredentialConnector` — the connect rules, on the shared ctech-account reader

**Files:**
- Modify: `api/go.mod`, `api/go.sum` (`gopkg.aoctech.app/api-commons v1.16.0`)
- Create: `api/internal/services/credentials.go`, `api/internal/services/credentials_test.go`
- Modify: `api/internal/app/app.go` (build the reader and the connector), `api/internal/api/v1/router.go` (`Deps.Connector`, `Deps.Webhooks`)

**Interfaces:**
- Consumes: `oauthclients.New`, `oauthclients.Config`, `oauthclients.OAuthClient`, `oauthclients.ErrNotFound` (Task 1); `oauthresource.MachineScopes` (Task 3); `CredentialRepository.Resolve`, `Connect`, `ErrCredentialTaken` (Task 4); `repositories.ErrNotFound`, `billing.ErrCredentialInactive`.
- Produces:

```go
package services
type OAuthClientReader interface { Get(ctx context.Context, clientID string) (oauthclients.OAuthClient, error) }
type CredentialStore interface {
	Resolve(ctx context.Context, clientID string) (*billing.APICredential, error)
	Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error
}
type ConnectRefusal string // "client_not_yours" | "client_has_no_billing_scope" | "client_not_machine_only" | "client_already_connected"
const RefusedNotYours, RefusedNoBillingScope, RefusedNotMachineOnly, RefusedAlreadyConnected ConnectRefusal
type ConnectRefusedError struct{ Code ConnectRefusal }
var ErrAccountUnavailable error
type ConnectRequest struct { OrganizationID string; Livemode bool; ClientID, UserID, RequestID string; Now time.Time }
func NewCredentialConnector(clients OAuthClientReader, creds CredentialStore, machineScopes []string) *CredentialConnector
func (s *CredentialConnector) Connect(ctx context.Context, r ConnectRequest) (cred *billing.APICredential, created bool, err error)
func (s *CredentialConnector) MachineScopes() []string // sorted copy
// v1.Deps
Connector *services.CredentialConnector
Webhooks  *repositories.WebhookRepository
MachineScopes []string
```

- [ ] **Step 1: Bump the shared module**

Run: `go get gopkg.aoctech.app/api-commons@v1.16.0 && go mod tidy && go build ./...`
Expected: `go.mod` requires `gopkg.aoctech.app/api-commons v1.16.0`; build passes.

- [ ] **Step 2: Write the failing tests** — `api/internal/services/credentials_test.go`

```go
package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/oauthclients"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var machine = []string{"billing:invoices:read", "billing:subscriptions:write", "billing:usage:write"}

type fakeClients struct {
	got   map[string]oauthclients.OAuthClient
	err   error
	asked int
}

func (f *fakeClients) Get(_ context.Context, id string) (oauthclients.OAuthClient, error) {
	f.asked++
	if f.err != nil {
		return oauthclients.OAuthClient{}, f.err
	}
	c, ok := f.got[id]
	if !ok {
		return oauthclients.OAuthClient{}, oauthclients.ErrNotFound
	}
	return c, nil
}

type fakeStore struct {
	existing   *billing.APICredential
	resolveErr error
	connectErr error
	connected  []*billing.APICredential
}

func (f *fakeStore) Resolve(context.Context, string) (*billing.APICredential, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	if f.existing == nil {
		return nil, repositories.ErrNotFound
	}
	return f.existing, nil
}

func (f *fakeStore) Connect(_ context.Context, c *billing.APICredential, _, _ string, _ time.Time) error {
	if f.connectErr != nil {
		return f.connectErr
	}
	f.connected = append(f.connected, c)
	return nil
}

func req(client string) ConnectRequest {
	return ConnectRequest{OrganizationID: "org_a", Livemode: false, ClientID: client, UserID: "usr_me", RequestID: "req", Now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

func mine(scopes ...string) oauthclients.OAuthClient {
	return oauthclients.OAuthClient{ClientID: "c", OwnerUserID: "usr_me", AllowedScopes: scopes, Active: true}
}

func refusal(t *testing.T, err error) ConnectRefusal {
	t.Helper()
	var r *ConnectRefusedError
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return r.Code
}

func TestTheConnectRulesInOrder(t *testing.T) {
	someoneElses := mine("billing:invoices:read")
	someoneElses.OwnerUserID = "usr_other"
	inactive := mine("billing:invoices:read")
	inactive.Active = false
	for _, tc := range []struct {
		name   string
		client *oauthclients.OAuthClient
		store  fakeStore
		want   ConnectRefusal
	}{
		{name: "unknown", client: nil, want: RefusedNotYours},
		{name: "someone else's", client: &someoneElses, want: RefusedNotYours},
		// Ownership first: a stranger never learns the client is in use.
		{name: "someone else's and connected elsewhere", client: &someoneElses,
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_b"}}, want: RefusedNotYours},
		{name: "sign-in only", client: new(mine("openid", "profile")), want: RefusedNoBillingScope},
		{name: "no scopes", client: new(mine()), want: RefusedNoBillingScope},
		{name: "billing and openid", client: new(mine("billing:invoices:read", "openid")), want: RefusedNotMachineOnly},
		{name: "a user billing scope", client: new(mine("billing:my-invoices:read")), want: RefusedNotMachineOnly},
		{name: "another product's scope", client: new(mine("billing:invoices:read", "dfe:nfe:read")), want: RefusedNotMachineOnly},
		{name: "an internal scope", client: new(mine("billing:invoices:read", "internal:account:org-member")), want: RefusedNotMachineOnly},
		{name: "inactive", client: &inactive, want: RefusedNotMachineOnly},
		{name: "connected to another tenant", client: new(mine("billing:invoices:read")),
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_b"}}, want: RefusedAlreadyConnected},
		{name: "connected to this tenant in the other mode", client: new(mine("billing:invoices:read")),
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_a", Livemode: true}}, want: RefusedAlreadyConnected},
		{name: "switched off by billing", client: new(mine("billing:invoices:read")),
			store: fakeStore{resolveErr: billing.ErrCredentialInactive}, want: RefusedAlreadyConnected},
		{name: "lost the race", client: new(mine("billing:invoices:read")),
			store: fakeStore{connectErr: repositories.ErrCredentialTaken}, want: RefusedAlreadyConnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := &fakeClients{got: map[string]oauthclients.OAuthClient{}}
			if tc.client != nil {
				clients.got["c"] = *tc.client
			}
			store := tc.store
			_, _, err := NewCredentialConnector(clients, &store, machine).Connect(context.Background(), req("c"))
			if got := refusal(t, err); got != tc.want {
				t.Fatalf("refusal = %s, want %s", got, tc.want)
			}
			if len(store.connected) != 0 {
				t.Fatal("a refused client was written")
			}
		})
	}
}

func TestAMachineOnlyClientOfTheConnectorIsAdmitted(t *testing.T) {
	clients := &fakeClients{got: map[string]oauthclients.OAuthClient{"c": mine("billing:invoices:read", "billing:usage:write")}}
	store := &fakeStore{}
	cred, created, err := NewCredentialConnector(clients, store, machine).Connect(context.Background(), req("c"))
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if cred.OrganizationID != "org_a" || cred.Livemode || !cred.Active || cred.ConnectedBy != "usr_me" ||
		cred.ConnectedAt != "2026-10-10T12:00:00Z" || len(store.connected) != 1 {
		t.Fatalf("cred = %+v", cred)
	}
}

// A double click: the same client, the same tenant and mode.
func TestReconnectingHereReturnsTheCredential(t *testing.T) {
	existing := &billing.APICredential{ClientID: "c", OrganizationID: "org_a", Livemode: false, Active: true, ConnectedBy: "usr_me"}
	clients := &fakeClients{got: map[string]oauthclients.OAuthClient{"c": mine("billing:invoices:read")}}
	store := &fakeStore{existing: existing}
	cred, created, err := NewCredentialConnector(clients, store, machine).Connect(context.Background(), req("c"))
	if err != nil || created || cred != existing || len(store.connected) != 0 {
		t.Fatalf("cred=%v created=%v err=%v", cred, created, err)
	}
}

func TestAnAccountOutageIsNotARefusal(t *testing.T) {
	clients := &fakeClients{err: errors.New("ctech-account answered 503")}
	_, _, err := NewCredentialConnector(clients, &fakeStore{}, machine).Connect(context.Background(), req("c"))
	if !errors.Is(err, ErrAccountUnavailable) {
		t.Fatalf("err = %v, want ErrAccountUnavailable", err)
	}
}
```

(`new(expr)` is Go 1.26+ and allowed by `go 1.27.2`.)

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/services/ -run 'Connect|Reconnect|AccountOutage' -v`
Expected: FAIL, `undefined: NewCredentialConnector`.

- [ ] **Step 4: Implement** — `api/internal/services/credentials.go`

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/oauthclients"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// CredentialConnector admits a merchant's own ctech-account OAuth client as the
// tenant's API credential (console self-service spec § 5.1, D5).
//
// The client secret never passes through billing: the merchant creates the
// client on ctech-account's developer page and gives billing its id; billing
// asks ctech-account who owns it and what it holds, and admits it only when a
// client_credentials token for it would be issued and would act for this
// tenant alone.
type CredentialConnector struct {
	clients OAuthClientReader
	creds   CredentialStore
	machine map[string]bool
	sorted  []string
}

// OAuthClientReader is ctech-account's read of one client (api-commons
// oauthclients).
type OAuthClientReader interface {
	Get(ctx context.Context, clientID string) (oauthclients.OAuthClient, error)
}

// CredentialStore is the slice of the credential repository the rules need.
type CredentialStore interface {
	Resolve(ctx context.Context, clientID string) (*billing.APICredential, error)
	Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error
}

// ConnectRefusal is why a client was not admitted; the value is the wire code.
type ConnectRefusal string

const (
	// RefusedNotYours: unknown, or owned by somebody else. One answer for both,
	// so the route is no oracle for which client ids exist.
	RefusedNotYours ConnectRefusal = "client_not_yours"
	// RefusedNoBillingScope: the client holds no billing: scope at all.
	RefusedNoBillingScope ConnectRefusal = "client_has_no_billing_scope"
	// RefusedNotMachineOnly: it holds a scope billing does not mark m2m (a
	// sign-in scope, a user scope, another product's or an internal one), or it
	// cannot authenticate as itself (inactive). ctech-account would never issue
	// it a client_credentials token, so admitting it would admit nothing.
	RefusedNotMachineOnly ConnectRefusal = "client_not_machine_only"
	// RefusedAlreadyConnected: admitted to a tenant (any, never said which).
	RefusedAlreadyConnected ConnectRefusal = "client_already_connected"
)

// ConnectRefusedError carries a refusal; the handler answers it as a 422.
type ConnectRefusedError struct{ Code ConnectRefusal }

func (e *ConnectRefusedError) Error() string { return "client not admitted: " + string(e.Code) }

// ErrAccountUnavailable is ctech-account not answering about the client. It is
// never a refusal: the client may be perfectly good.
var ErrAccountUnavailable = errors.New("ctech-account could not be asked about the client")

// ConnectRequest is one connect, as the console resolved it.
type ConnectRequest struct {
	OrganizationID string
	Livemode       bool
	ClientID       string
	UserID         string
	RequestID      string
	Now            time.Time
}

func NewCredentialConnector(clients OAuthClientReader, creds CredentialStore, machineScopes []string) *CredentialConnector {
	set := make(map[string]bool, len(machineScopes))
	for _, s := range machineScopes {
		set[s] = true
	}
	sorted := slices.Clone(machineScopes)
	slices.Sort(sorted)
	return &CredentialConnector{clients: clients, creds: creds, machine: set, sorted: sorted}
}

// MachineScopes is the m2m set, sorted: what the console's link to the
// developer page preselects.
func (s *CredentialConnector) MachineScopes() []string { return slices.Clone(s.sorted) }

func refuse(code ConnectRefusal) (*billing.APICredential, bool, error) {
	return nil, false, &ConnectRefusedError{Code: code}
}

// Connect applies the rules in an order that leaks nothing: ownership first, so
// only the client's owner ever learns it is connected somewhere.
func (s *CredentialConnector) Connect(ctx context.Context, r ConnectRequest) (*billing.APICredential, bool, error) {
	oc, err := s.clients.Get(ctx, r.ClientID)
	switch {
	case errors.Is(err, oauthclients.ErrNotFound):
		return refuse(RefusedNotYours)
	case err != nil:
		return nil, false, fmt.Errorf("%w: %v", ErrAccountUnavailable, err)
	}
	if oc.OwnerUserID == "" || oc.OwnerUserID != r.UserID {
		return refuse(RefusedNotYours)
	}
	if code := s.scopeVerdict(oc.AllowedScopes); code != "" {
		return refuse(code)
	}
	if !oc.Active {
		return refuse(RefusedNotMachineOnly)
	}

	existing, err := s.creds.Resolve(ctx, r.ClientID)
	switch {
	case err == nil:
		if existing.OrganizationID == r.OrganizationID && existing.Livemode == r.Livemode {
			return existing, false, nil
		}
		return refuse(RefusedAlreadyConnected)
	case errors.Is(err, billing.ErrCredentialInactive):
		// Billing's own kill switch is on for it somewhere: still taken.
		return refuse(RefusedAlreadyConnected)
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, false, err
	}

	cred := &billing.APICredential{
		ClientID: r.ClientID, OrganizationID: r.OrganizationID, Livemode: r.Livemode, Active: true,
		ConnectedBy: r.UserID, ConnectedAt: r.Now.UTC().Format(time.RFC3339),
	}
	if err := s.creds.Connect(ctx, cred, "user:"+r.UserID, r.RequestID, r.Now); err != nil {
		if errors.Is(err, repositories.ErrCredentialTaken) {
			return refuse(RefusedAlreadyConnected)
		}
		return nil, false, err
	}
	return cred, true, nil
}

func (s *CredentialConnector) scopeVerdict(scopes []string) ConnectRefusal {
	if !slices.ContainsFunc(scopes, func(sc string) bool { return strings.HasPrefix(sc, "billing:") }) {
		return RefusedNoBillingScope
	}
	for _, sc := range scopes {
		if !s.machine[sc] {
			return RefusedNotMachineOnly
		}
	}
	return ""
}
```

- [ ] **Step 5: Wire it** — `router.go` `Deps`, after `Credentials`:

```go
	// Connector admits a merchant's own OAuth client from the console (spec
	// § 5.1). Webhooks is the endpoint store the console manages (§ 5.2).
	Connector *services.CredentialConnector
	Webhooks  *repositories.WebhookRepository
```

`app.go`, in `Build` after `account := accountorgs.New(...)`:

```go
	machineScopes, err := oauthresource.MachineScopes()
	if err != nil {
		return nil, err
	}
	// Same ctech-account credential as the membership client; its own token,
	// minted for internal:account:oauth-client-read only. A nil reader (no
	// credential configured) answers every connect with ErrAccountUnavailable.
	clientReader := oauthclients.New(oauthclients.Config{
		BaseURL: cfg.AccountBaseURL, TokenURL: cfg.AccountTokenURL,
		ClientID: cfg.AccountClientID, ClientSecret: cfg.AccountClientSecret, Cache: cacheBackend,
	})
```

and in `v1.Deps{…}`:

```go
		Connector: services.NewCredentialConnector(clientReader, creds, machineScopes),
		Webhooks:  repositories.NewWebhookRepository(db, cfg),
```

(`creds` is the `CredentialRepository` already built at the top of `Build`.)

A nil `*oauthclients.Client` stored in the `OAuthClientReader` interface is a non-nil interface whose `Get` returns an error, which `Connect` turns into `ErrAccountUnavailable`: the intended fail-closed path.

- [ ] **Step 6: Run to verify**

Run: `go test ./internal/services/ -race -run 'Connect|Reconnect|AccountOutage' -v && go build ./... && go vet ./... && gofmt -l ./internal`
Expected: PASS; build and vet clean.

- [ ] **Step 7: Commit**

```bash
git add api/go.mod api/go.sum api/internal/services/credentials.go api/internal/services/credentials_test.go api/internal/app/app.go api/internal/api/v1/router.go
git commit -m "feat(credentials): the connect rules, on api-commons oauthclients v1.16.0"
```

---

### Task 6: Console credential routes

**Files:**
- Create: `api/internal/api/v1/console_integrations.go` (route registration, suspension check), `api/internal/api/v1/console_credentials.go`
- Modify: `api/internal/api/v1/console.go` (`consoleHandlers` fields), `api/internal/api/v1/router.go` (`registerConsole`), `api/internal/api/v1/validate.go` (`clientID`), `api/internal/problem/problem.go` (`TypeAccountUnavailable`), `api/internal/limits/limits.go`, `api/internal/limits/limits_test.go`, `ui/src/lib/limits.json`
- Modify: `api/tests/integration/fake_account_test.go`
- Test: `api/tests/integration/console_credentials_test.go` (new)
- Modify: `README.md` (console route tables)

**Interfaces:**
- Consumes: Task 5 (`Connector`, `ConnectRefusedError`, `ErrAccountUnavailable`, `MachineScopes()`), Task 4 (`Get`, `ListForTenant`, `Disconnect`, `ErrCredentialProvisioned`), part 1 (`GetTenant`, `GetClaims`, `GetOrganization`, chain), part 2 (`ScopeOrganizationWrite`, `Organization.Suspended()`).
- Produces: wire shapes (UI Task 11 mirrors them):

```json
GET /v1.0/console/credentials → 200
{"data": [{"client_id": "…", "livemode": false, "active": true, "provisioned": false,
           "connected_by": "usr_…", "connected_by_me": true, "connected_at": "2026-10-10T12:00:00Z",
           "description": "…"}],
 "machine_scopes": ["billing:customers:read", "…"]}
POST /v1.0/console/credentials {"client_id": "…"} → 201 (new) | 200 (already here), one credential object
DELETE /v1.0/console/credentials/:client_id → 204
```

Go: `func isSuspended(c fiber.Ctx) bool`, `func refuseSuspended(c fiber.Ctx) error`, `func registerIntegrations(console fiber.Router, ch *consoleHandlers)`; `limits.ClientID = 128`, `limits.WebhookURL = 2048`, `limits.MaxWebhookEndpoints = 5`; `(*fakeAccount).client(id, owner string, active bool, scopes ...string)`, `fakeAccount.clientsDown bool`.

- [ ] **Step 1: Teach the fake ctech-account the client route** — `fake_account_test.go`

Add fields to `fakeAccount`:

```go
	clients     map[string]fakeOAuthClient // client id -> the internal route's answer
	clientsDown bool                       // the client route answers 503
```

```go
type fakeOAuthClient struct {
	owner  string
	active bool
	scopes []string
}

// client registers an OAuth client as ctech-account's internal route reports it.
func (a *fakeAccount) client(id, owner string, active bool, scopes ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clients == nil {
		a.clients = map[string]fakeOAuthClient{}
	}
	a.clients[id] = fakeOAuthClient{owner: owner, active: active, scopes: scopes}
}
```

and a case in `serve`'s switch, before `default`:

```go
		case strings.HasPrefix(r.URL.Path, "/v1.0/internal/oauth-clients/"):
			if a.clientsDown {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			id := strings.TrimPrefix(r.URL.Path, "/v1.0/internal/oauth-clients/")
			c, ok := a.clients[id]
			if !ok {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"type":"about:blank","status":404}`))
				return
			}
			scopes := c.scopes
			if scopes == nil {
				scopes = []string{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id": id, "owner_user_id": c.owner, "allowed_scopes": scopes, "active": c.active,
			})
```

- [ ] **Step 2: Write the failing tests** — `api/tests/integration/console_credentials_test.go`

```go
//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const credsPath = "/v1.0/console/credentials"

type credentialBody struct {
	ClientID      string `json:"client_id"`
	Livemode      bool   `json:"livemode"`
	Active        bool   `json:"active"`
	Provisioned   bool   `json:"provisioned"`
	ConnectedBy   string `json:"connected_by"`
	ConnectedByMe bool   `json:"connected_by_me"`
	ConnectedAt   string `json:"connected_at"`
}

type credentialList struct {
	Data          []credentialBody `json:"data"`
	MachineScopes []string         `json:"machine_scopes"`
}

type problemBody struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (e *apiEnv) writeToken(t *testing.T) string {
	t.Helper()
	return e.sessionToken(t, middleware.ScopeOrganizationRead, middleware.ScopeOrganizationWrite)
}

func (e *apiEnv) connect(t *testing.T, token, clientID string) apiResponse {
	t.Helper()
	return e.consoleWrite(t, http.MethodPost, credsPath, token, "live", `{"client_id":"`+clientID+`"}`)
}

// suspendLive sets suspended_at on e's live tenant, the attribute part 2 writes
// (spec § 6.2), directly: this plan tests what the console does with it.
func suspendLive(t *testing.T, org *billing.Organization) {
	t.Helper()
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableOrganizations)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, org.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.OrganizationSK()},
		},
		UpdateExpression:          aws.String("SET suspended_at = :s"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":s": &types.AttributeValueMemberS{Value: "2026-10-10T12:00:00Z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAConnectedClientActsForTheTenant(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	clientID := "cli_" + id.New()
	e.account.client(clientID, e.org.OwnerUserID, true, "billing:invoices:read", "billing:subscriptions:write")

	res := e.connect(t, tok, clientID)
	if res.status != http.StatusCreated {
		t.Fatalf("connect: %d %s", res.status, res.body)
	}
	var got credentialBody
	res.decode(t, &got)
	if got.ClientID != clientID || !got.Livemode || got.Provisioned || !got.ConnectedByMe || got.ConnectedAt == "" {
		t.Fatalf("connect body = %+v", got)
	}

	// The integration now resolves to this tenant.
	m2m := e.token(t, clientID, "", middleware.ScopeInvoicesRead)
	if r := e.do(t, http.MethodGet, "/v1.0/invoices", m2m, "", ""); r.status != http.StatusOK {
		t.Fatalf("M2M call with the connected client: %d %s", r.status, r.body)
	}

	var list credentialList
	e.console(t, credsPath, tok, "live").decode(t, &list)
	if len(list.MachineScopes) != 11 {
		t.Fatalf("machine_scopes = %v", list.MachineScopes)
	}
	found := false
	for _, c := range list.Data {
		if c.ClientID == clientID && c.ConnectedByMe {
			found = true
		}
	}
	if !found {
		t.Fatalf("list = %+v", list.Data)
	}

	// A double click answers 200 with the same credential.
	if again := e.connect(t, tok, clientID); again.status != http.StatusOK {
		t.Fatalf("reconnect: %d %s", again.status, again.body)
	}
}

// Spec § 10.4 and Review Focus 1.
func TestConnectRefusalsNeverSayWhereAClientIs(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	other := newOrg(t, true)
	creds := repositories.NewCredentialRepository(testDB, testCfg)

	strangers := "cli_" + id.New() // owned by someone else, connected to `other`
	e.account.client(strangers, "usr_"+id.New(), true, "billing:invoices:read")
	if err := creds.Create(ctxT(t), &billing.APICredential{ClientID: strangers, OrganizationID: other.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	unknown := "cli_" + id.New()

	var a, b problemBody
	ra, rb := e.connect(t, tok, strangers), e.connect(t, tok, unknown)
	ra.decode(t, &a)
	rb.decode(t, &b)
	if ra.status != 422 || a.Code != "client_not_yours" || a != b {
		t.Fatalf("stranger's = %d %+v, unknown = %d %+v; want the same client_not_yours", ra.status, a, rb.status, b)
	}

	mineElsewhere := "cli_" + id.New()
	e.account.client(mineElsewhere, e.org.OwnerUserID, true, "billing:invoices:read")
	if err := creds.Create(ctxT(t), &billing.APICredential{ClientID: mineElsewhere, OrganizationID: other.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	res := e.connect(t, tok, mineElsewhere)
	var p problemBody
	res.decode(t, &p)
	if res.status != 422 || p.Code != "client_already_connected" {
		t.Fatalf("already connected: %d %+v", res.status, p)
	}
	if bytes.Contains(res.body, []byte(other.ID)) || bytes.Contains(res.body, []byte(other.DisplayName)) {
		t.Fatalf("the refusal names the other tenant: %s", res.body)
	}
}

func TestClientsThatCannotGetAMachineTokenAreRefused(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	for _, tc := range []struct {
		name   string
		active bool
		scopes []string
		code   string
	}{
		{"sign-in only", true, []string{"openid", "profile"}, "client_has_no_billing_scope"},
		{"billing plus openid", true, []string{"billing:invoices:read", "openid"}, "client_not_machine_only"},
		{"a portal scope", true, []string{"billing:my-invoices:read"}, "client_not_machine_only"},
		{"the console write scope", true, []string{"billing:organization:write"}, "client_not_machine_only"},
		{"another product", true, []string{"billing:invoices:read", "dfe:nfe:read"}, "client_not_machine_only"},
		{"inactive", false, []string{"billing:invoices:read"}, "client_not_machine_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientID := "cli_" + id.New()
			e.account.client(clientID, e.org.OwnerUserID, tc.active, tc.scopes...)
			res := e.connect(t, tok, clientID)
			var p problemBody
			res.decode(t, &p)
			if res.status != 422 || p.Code != tc.code {
				t.Fatalf("%d %+v, want 422 %s", res.status, p, tc.code)
			}
		})
	}
}

func TestDisconnectStopsTheIntegrationAtOnce(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	clientID := "cli_" + id.New()
	e.account.client(clientID, e.org.OwnerUserID, true, "billing:invoices:read")
	if r := e.connect(t, tok, clientID); r.status != http.StatusCreated {
		t.Fatalf("connect: %d %s", r.status, r.body)
	}
	if r := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+clientID, tok, "live", ""); r.status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.status, r.body)
	}
	m2m := e.token(t, clientID, "", middleware.ScopeInvoicesRead)
	if r := e.do(t, http.MethodGet, "/v1.0/invoices", m2m, "", ""); r.status != http.StatusForbidden {
		t.Fatalf("M2M after disconnect: %d %s", r.status, r.body)
	}
}

func TestProvisionedCredentialsAreListedNeverRemoved(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var list credentialList
	e.console(t, credsPath, tok, "live").decode(t, &list)
	var seeded *credentialBody
	for i := range list.Data {
		if list.Data[i].ClientID == e.client {
			seeded = &list.Data[i]
		}
	}
	if seeded == nil || !seeded.Provisioned || seeded.ConnectedByMe {
		t.Fatalf("seeded credential = %+v", seeded)
	}
	res := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+e.client, tok, "live", "")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusConflict || p.Code != "credential_provisioned" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

// Decision 1: a suspended live tenant may disconnect, never connect.
func TestASuspendedTenantCanOnlyShrinkItsCredentials(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	connected := "cli_" + id.New()
	e.account.client(connected, e.org.OwnerUserID, true, "billing:invoices:read")
	if r := e.connect(t, tok, connected); r.status != http.StatusCreated {
		t.Fatal(r.status)
	}
	suspendLive(t, e.org)

	fresh := "cli_" + id.New()
	e.account.client(fresh, e.org.OwnerUserID, true, "billing:invoices:read")
	res := e.connect(t, tok, fresh)
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusForbidden || p.Code != "tenant_suspended" {
		t.Fatalf("connect while suspended: %d %+v", res.status, p)
	}
	if r := e.console(t, credsPath, tok, "live"); r.status != http.StatusOK {
		t.Fatalf("list while suspended: %d", r.status)
	}
	if r := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+connected, tok, "live", ""); r.status != http.StatusNoContent {
		t.Fatalf("disconnect while suspended: %d %s", r.status, r.body)
	}
}

func TestAnAccountOutageIsA503(t *testing.T) {
	e := newAPI(t)
	e.account.clientsDown = true
	res := e.connect(t, e.writeToken(t), "cli_"+id.New())
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusServiceUnavailable || p.Code != "account_unavailable" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

func TestConnectNeedsTheWriteScopeAndAWellFormedID(t *testing.T) {
	e := newAPI(t)
	readOnly := e.sessionToken(t, middleware.ScopeOrganizationRead)
	if r := e.connect(t, readOnly, "cli_x"); r.status != http.StatusForbidden {
		t.Fatalf("read-only token: %d", r.status)
	}
	for _, body := range []string{`{}`, `{"client_id":"a/b"}`, `{"client_id":"` + string(bytes.Repeat([]byte("a"), 129)) + `"}`} {
		r := e.consoleWrite(t, http.MethodPost, credsPath, e.writeToken(t), "live", body)
		if r.status != 422 {
			t.Fatalf("%s: %d %s", body, r.status, r.body)
		}
	}
}
```

(`e.account.clientsDown = true` writes under no lock; it is set before any request. If the race detector flags it, add a `setClientsDown` method that takes `a.mu`.)

- [ ] **Step 3: Run to verify they fail**

Run: `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Connect|Disconnect|Provisioned|Suspended|AccountOutage' -v`
Expected: FAIL with `404` on `/v1.0/console/credentials` (routes not registered).

- [ ] **Step 4: Implement**

`limits.go`, after `TaxID`:

```go
	ClientID   = 128  // ctech-account's longest client id (operator ids 3–128, UUIDs)
	WebhookURL = 2048 // a URL longer than this is not an endpoint somebody typed
```

and after `MaxUsageQuantity`:

```go
	// MaxWebhookEndpoints is per tenant and mode (spec § 5.2).
	MaxWebhookEndpoints = 5
```

`limits_test.go` `want`: add `"clientID": float64(ClientID), "webhookURL": float64(WebhookURL)` inside `"text"`, and `"maxWebhookEndpoints": float64(MaxWebhookEndpoints)` at the top level. `ui/src/lib/limits.json`: add `"clientID": 128, "webhookURL": 2048` inside `"text"` and `"maxWebhookEndpoints": 5` at the top level.

`validate.go`:

```go
var clientIDChars = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// clientID: a ctech-account client id as either creation path makes it.
// Checked before the id reaches a URL path at ctech-account.
func (c *checks) clientID(field, v string) {
	switch {
	case strings.TrimSpace(v) == "":
		c.fail(field, "required", "required")
	case len(v) > limits.ClientID:
		c.fail(field, "too_long", fmt.Sprintf("at most %d characters", limits.ClientID), "max", limits.ClientID)
	case !clientIDChars.MatchString(v):
		c.fail(field, "invalid_chars", "contains invalid characters")
	}
}
```

`problem.go`, with the other types:

```go
	// TypeAccountUnavailable: ctech-account did not answer a question billing
	// must ask before acting (the owner of an OAuth client). Retryable.
	TypeAccountUnavailable = "/problems/account-unavailable"
```

`console.go`, `consoleHandlers` gains:

```go
	// creds, connector and hooks serve the integration tools (spec § 5).
	creds     *repositories.CredentialRepository
	connector *services.CredentialConnector
	hooks     *repositories.WebhookRepository
```

`router.go`, in `registerConsole`, set them in the struct literal (`creds: d.Credentials, connector: d.Connector, hooks: d.Webhooks`) and, at the end of the function (after the products routes), `registerIntegrations(console, ch)`.

`console_integrations.go`:

```go
package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// registerIntegrations mounts the merchant's integration tools (spec § 5) on
// the console group, which already resolved the organization, the person's
// role (owner/admin) and the mode. Reads take the organization read scope;
// every write the organization write scope.
func registerIntegrations(console fiber.Router, ch *consoleHandlers) {
	read := middleware.RequireUserScope(middleware.ScopeOrganizationRead)
	write := middleware.RequireUserScope(middleware.ScopeOrganizationWrite)

	console.Get("/credentials", read, ch.listCredentials)
	console.Post("/credentials", write, ch.connectCredential)
	console.Delete("/credentials/:client_id", write, ch.disconnectCredential)
	// Task 10 adds the webhook routes here.
}

// isSuspended reports a suspended live tenant (part 2, spec § 4.3). Scope
// decision 1: such a tenant may disconnect, delete and rotate, never connect,
// create or test. Part 2 owns the flag; this is the one call site.
func isSuspended(c fiber.Ctx) bool {
	org := middleware.GetOrganization(c)
	return org != nil && org.Suspended()
}

func refuseSuspended(c fiber.Ctx) error {
	return problem.Forbidden("this organization's live mode is suspended").WithCode("tenant_suspended").Send(c)
}
```

`console_credentials.go`:

```go
package v1

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type credentialResponse struct {
	ClientID      string `json:"client_id"`
	Livemode      bool   `json:"livemode"`
	Active        bool   `json:"active"`
	Provisioned   bool   `json:"provisioned"`
	ConnectedBy   string `json:"connected_by,omitempty"`
	ConnectedByMe bool   `json:"connected_by_me"`
	ConnectedAt   string `json:"connected_at,omitempty"`
	Description   string `json:"description,omitempty"`
}

func newCredentialResponse(c *billing.APICredential, me string) credentialResponse {
	return credentialResponse{
		ClientID: c.ClientID, Livemode: c.Livemode, Active: c.Active, Provisioned: c.Provisioned(),
		ConnectedBy: c.ConnectedBy, ConnectedByMe: c.ConnectedBy != "" && c.ConnectedBy == me,
		ConnectedAt: c.ConnectedAt, Description: c.Description,
	}
}

type credentialListResponse struct {
	Data []credentialResponse `json:"data"`
	// MachineScopes is what the console's link to the developer page
	// preselects: the scopes a connectable client may hold.
	MachineScopes []string `json:"machine_scopes"`
}

type connectCredentialRequest struct {
	ClientID string `json:"client_id"`
}

func me(c fiber.Ctx) string {
	if cl := middleware.GetClaims(c); cl != nil {
		return cl.Sub
	}
	return ""
}

func (h *consoleHandlers) listCredentials(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	creds, err := h.creds.ListForTenant(c.Context(), t.OrganizationID, t.Livemode)
	if err != nil {
		return fail(c, err)
	}
	out := credentialListResponse{Data: make([]credentialResponse, 0, len(creds)), MachineScopes: h.connector.MachineScopes()}
	for i := range creds {
		out.Data = append(out.Data, newCredentialResponse(&creds[i], me(c)))
	}
	return c.JSON(out)
}

func (h *consoleHandlers) connectCredential(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	var req connectCredentialRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.clientID("client_id", req.ClientID)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	t := middleware.GetTenant(c)
	cred, created, err := h.connector.Connect(c.Context(), services.ConnectRequest{
		OrganizationID: t.OrganizationID, Livemode: t.Livemode, ClientID: req.ClientID,
		UserID: me(c), RequestID: middleware.GetRequestID(c), Now: h.now(),
	})
	var refused *services.ConnectRefusedError
	switch {
	case errors.As(err, &refused):
		return problem.Unprocessable("the client cannot be connected").WithCode(string(refused.Code)).Send(c)
	case errors.Is(err, services.ErrAccountUnavailable):
		return problem.New(http.StatusServiceUnavailable, problem.TypeAccountUnavailable, "Account Unavailable",
			"CTech Account did not answer; try again").WithCause(err).WithCode("account_unavailable").Send(c)
	case err != nil:
		return fail(c, err)
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	return c.Status(status).JSON(newCredentialResponse(cred, me(c)))
}

// disconnectCredential is allowed on a suspended tenant (scope decision 1).
func (h *consoleHandlers) disconnectCredential(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	cred, err := h.creds.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("client_id"))
	if err != nil {
		return fail(c, err)
	}
	err = h.creds.Disconnect(c.Context(), cred, actorOfUser(c), middleware.GetRequestID(c), h.now())
	switch {
	case errors.Is(err, repositories.ErrCredentialProvisioned):
		return problem.Conflict("this credential is managed by CTech").WithCode("credential_provisioned").Send(c)
	case err != nil:
		return fail(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}
```

The one detail string for all four refusals is deliberate: the code carries the reason, and an identical body for `client_not_yours` whatever the cause is what the oracle test compares.

`README.md`: in "Console writes", add rows

```markdown
| `POST /v1.0/console/credentials` · `DELETE /v1.0/console/credentials/:client_id` | `billing:organization:write` |
```

and in the console read table:

```markdown
| `GET /v1.0/console/credentials` | `billing:organization:read` |
```

with one paragraph below the writes table:

```markdown
A credential is connected, never created: the merchant creates a machine-only
client on ctech-account's developer page and gives billing its id; billing asks
ctech-account who owns it (`GET /v1.0/internal/oauth-clients/:client_id`) and
admits it only if the person connecting owns it, it holds only billing scopes
marked `m2m`, it is active and no tenant has it (console self-service spec § 5.1).
Credentials and endpoints from the tenant plan are listed as `provisioned` and
cannot be removed from the console.
```

- [ ] **Step 5: Run to verify**

Run: `go test ./internal/... -race && gofmt -l ./internal ./tests && go vet ./...` then `make test-integration`
Expected: PASS, including part 1's `TestNoNonMerchantReachesAnyBillingHalfRoute` (now also covering the three credential routes) and `TestTheUIReadsTheseLimits`.

- [ ] **Step 6: Commit**

```bash
git add api/internal api/tests/integration ui/src/lib/limits.json README.md
git commit -m "feat(console): connect, list and disconnect the merchant's own OAuth clients"
```

---
### Task 7: Webhook domain — the event list, `ping`, safe URLs, secrets

**Files:**
- Modify: `api/internal/domain/billing/webhook.go` (`EventPing`, `Event.EndpointID`, `WebhookEndpoint.AddedBy/AddedAt`, `Provisioned`, `Wants`)
- Create: `api/internal/domain/billing/webhook_events.go`, `api/internal/domain/billing/webhook_url.go`
- Test: `api/internal/domain/billing/webhook_events_test.go`, `api/internal/domain/billing/webhook_url_test.go`, `api/internal/domain/billing/webhook_test.go` (create if absent)
- Modify: `api/internal/domain/id/id.go`

**Interfaces:**
- Consumes: the existing `EventType` constants.
- Produces:

```go
const EventPing EventType = "ping"
// Event gains:            EndpointID string `dynamodbav:"endpoint_id,omitempty" json:"-"`
// WebhookEndpoint gains:  AddedBy string `dynamodbav:"added_by,omitempty"`; AddedAt string `dynamodbav:"added_at,omitempty"`
func (e *WebhookEndpoint) Provisioned() bool
func WebhookEventTypes() []EventType          // sorted copy; never contains EventPing
func IsWebhookEventType(t EventType) bool
var ErrWebhookHostNotAllowed error
func CheckWebhookURL(raw string) error         // wraps ErrInvalidEndpoint (shape) or ErrWebhookHostNotAllowed (host)
func PublicAddr(ip netip.Addr) bool
func NewWebhookSecret() string                 // "whsec_" + 64 hex characters
// id
const PrefixWebhookEndpoint = "whe_"
```

- [ ] **Step 1: Write the failing tests**

`webhook_events_test.go`:

```go
package billing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The catalogue the console offers is every EventType constant in this
// package except ping. Read from the source, so a new event cannot be added
// without either appearing in the console or failing here.
func TestTheWebhookEventListIsEveryEventConstant(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []EventType
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				typ, ok := spec.Type.(*ast.Ident)
				if !ok || typ.Name != "EventType" {
					return true
				}
				for _, v := range spec.Values {
					if lit, ok := v.(*ast.BasicLit); ok {
						s, _ := strconv.Unquote(lit.Value)
						if EventType(s) != EventPing {
							declared = append(declared, EventType(s))
						}
					}
				}
				return true
			})
		}
	}
	slices.Sort(declared)
	if got := WebhookEventTypes(); !slices.Equal(got, declared) {
		t.Fatalf("WebhookEventTypes() = %v\ndeclared %v", got, declared)
	}
	if IsWebhookEventType(EventPing) || !IsWebhookEventType(EventInvoicePaid) {
		t.Fatal("IsWebhookEventType disagrees with the list")
	}
}
```

(imports also `io/fs`. `parser.ParseDir` is deprecated in recent Go but present; if `go vet` flags it, use `filepath.Glob("*.go")` plus `parser.ParseFile` per file, same walk.)

`webhook_url_test.go`:

```go
package billing

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestCheckWebhookURL(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want error // nil, ErrInvalidEndpoint or ErrWebhookHostNotAllowed
	}{
		{"https://hooks.example.com/billing", nil},
		{"https://example.com:8443/x?y=1", nil},
		{"https://203.0.113.10/hook", nil},
		{"http://hooks.example.com/billing", ErrInvalidEndpoint},
		{"hooks.example.com/billing", ErrInvalidEndpoint},
		{"https://user:pw@hooks.example.com/", ErrInvalidEndpoint},
		{"https:///nohost", ErrInvalidEndpoint},
		{"https://127.0.0.1/hook", ErrWebhookHostNotAllowed},
		{"https://[::1]/hook", ErrWebhookHostNotAllowed},
		{"https://10.0.0.5/hook", ErrWebhookHostNotAllowed},
		{"https://169.254.169.254/latest/meta-data", ErrWebhookHostNotAllowed},
		{"https://100.64.1.1/hook", ErrWebhookHostNotAllowed},
		{"https://[::ffff:192.168.0.1]/hook", ErrWebhookHostNotAllowed},
		{"https://localhost/hook", ErrWebhookHostNotAllowed},
		{"https://api.localhost/hook", ErrWebhookHostNotAllowed},
		{"https://printer.local/hook", ErrWebhookHostNotAllowed},
		{"https://dfe.internal/hook", ErrWebhookHostNotAllowed},
		{"https://intranet/hook", ErrWebhookHostNotAllowed},
	} {
		err := CheckWebhookURL(tc.url)
		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: %v, want accepted", tc.url, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: %v, want %v", tc.url, err, tc.want)
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2001:4860:4860::8888": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fc00::1": false, "224.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestNewWebhookSecretIsLongUniqueAndValid(t *testing.T) {
	a, b := NewWebhookSecret(), NewWebhookSecret()
	if a == b || !strings.HasPrefix(a, "whsec_") || len(a) != len("whsec_")+64 {
		t.Fatalf("secrets %q %q", a, b)
	}
	e := WebhookEndpoint{ID: "whe_1", OrganizationID: "org_1", URL: "https://example.com", Secret: a}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
```

`webhook_test.go` (append if the file exists):

```go
func TestAPingGoesOnlyToItsOwnEndpoint(t *testing.T) {
	ping := &Event{ID: "evt_1", Type: EventPing, EndpointID: "whe_a", Entity: "webhook_endpoint", EntityID: "whe_a"}
	target := WebhookEndpoint{ID: "whe_a", Status: EndpointActive, Events: []EventType{EventInvoicePaid}}
	other := WebhookEndpoint{ID: "whe_b", Status: EndpointActive}
	disabled := WebhookEndpoint{ID: "whe_a", Status: EndpointDisabled}
	if !target.Wants(ping) {
		t.Fatal("the target's event filter kept its own ping out")
	}
	if other.Wants(ping) || disabled.Wants(ping) {
		t.Fatal("a ping reached another or a disabled endpoint")
	}
	// An ordinary event is unaffected by EndpointID routing.
	if !other.Wants(&Event{Type: EventInvoicePaid}) {
		t.Fatal("ordinary routing changed")
	}
}

func TestAnEndpointWithoutAnAuthorWasProvisioned(t *testing.T) {
	if !(&WebhookEndpoint{ID: "whe_dfe"}).Provisioned() || (&WebhookEndpoint{ID: "whe_1", AddedBy: "usr_1"}).Provisioned() {
		t.Fatal("Provisioned is wrong")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/domain/billing/ -run 'WebhookEvent|CheckWebhookURL|PublicAddr|NewWebhookSecret|Ping|Provisioned' -v`
Expected: FAIL, undefined `EventPing`, `WebhookEventTypes`, `CheckWebhookURL`, …

- [ ] **Step 3: Implement**

`webhook.go` — after the `EndpointStatus` constants:

```go
// EventPing is the console's test call (spec § 5.2): sent through the normal
// delivery path to one endpoint, whatever that endpoint's event filter says.
// It is not in WebhookEventTypes: nobody subscribes to it.
const EventPing EventType = "ping"
```

In `WebhookEndpoint`, after `DisabledAt`:

```go
	// AddedBy is the ctech-account user who created the endpoint in the console,
	// AddedAt when (RFC 3339). Empty on an endpoint the tenant plan provisioned,
	// which the console lists and never changes.
	AddedBy string `dynamodbav:"added_by,omitempty" json:"added_by,omitempty"`
	AddedAt string `dynamodbav:"added_at,omitempty" json:"added_at,omitempty"`
```

(`added_at`, not `created_at`: the row's `keys` already writes `created_at`, and two fields with one attribute name collide in the marshaller.)

Below `Validate`:

```go
// Provisioned reports an endpoint from the tenant plan (scope decision 7).
func (e *WebhookEndpoint) Provisioned() bool { return e.AddedBy == "" }
```

`Wants` becomes:

```go
func (e *WebhookEndpoint) Wants(ev *Event) bool {
	if e.Status != EndpointActive {
		return false
	}
	// A ping names its endpoint. It is the one event routed by id: a test
	// must reach exactly the endpoint the person pressed it on, including one
	// whose filter would never take a "ping".
	if ev.Type == EventPing {
		return ev.EndpointID == e.ID
	}
	if e.OwnerKey != "" && e.OwnerKey != ev.OwnerKey {
		return false
	}
	if len(e.Events) > 0 && !slices.Contains(e.Events, ev.Type) {
		return false
	}
	return true
}
```

In `Event`, after `OwnerKey`:

```go
	// EndpointID is set on a ping only: the one endpoint it is for.
	EndpointID string `dynamodbav:"endpoint_id,omitempty" json:"-"`
```

`webhook_events.go`:

```go
package billing

import "slices"

// webhookEventTypes is what a merchant may subscribe an endpoint to (spec
// § 5.2): every event the domain emits. Pinned to the source by
// TestTheWebhookEventListIsEveryEventConstant.
var webhookEventTypes = func() []EventType {
	list := []EventType{
		EventInvoiceCreated, EventInvoiceFinalized, EventInvoicePaid, EventInvoicePaymentFailed,
		EventInvoiceUncollectible, EventInvoiceVoided,
		EventSubscriptionCreated, EventSubscriptionActivated, EventSubscriptionPastDue,
		EventSubscriptionRecovered, EventSubscriptionRenewed, EventSubscriptionPaused,
		EventSubscriptionResumed, EventSubscriptionCanceled, EventSubscriptionUpdated,
		EventPaymentAttempted, EventPaymentSucceeded, EventPaymentFailed, EventPaymentAbandoned,
		EventCheckoutCreated, EventCheckoutCompleted, EventCheckoutExpired, EventCheckoutCanceled,
		EventCreditNoteCreated,
	}
	slices.Sort(list)
	return list
}()

// WebhookEventTypes returns the subscribable events, sorted. A copy.
func WebhookEventTypes() []EventType { return slices.Clone(webhookEventTypes) }

// IsWebhookEventType reports a subscribable event.
func IsWebhookEventType(t EventType) bool {
	_, ok := slices.BinarySearch(webhookEventTypes, t)
	return ok
}
```

`webhook_url.go`:

```go
package billing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ErrWebhookHostNotAllowed reports a URL whose host is not on the public
// internet. Since merchants register their own endpoints (spec § 5.2), billing
// would otherwise make signed requests, on a schedule, to whatever address a
// merchant names, CTech's own private network included.
var ErrWebhookHostNotAllowed = errors.New("webhook host is not a public address")

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PublicAddr reports an address a merchant's endpoint may resolve to: global
// unicast, and none of private, loopback, link-local, shared (CGNAT) or an
// IPv4-mapped form of those.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !cgnat.Contains(ip)
}

// CheckWebhookURL is the console's rule for a merchant endpoint, on top of
// WebhookEndpoint.Validate: https, a host, no credentials in the URL, and a
// host that names the public internet. Resolution is checked again at
// delivery (services.Deliverer), where DNS can no longer change the answer.
func CheckWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%w: %q is not a URL", ErrInvalidEndpoint, raw)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: %q is not https", ErrInvalidEndpoint, raw)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials do not belong in the URL", ErrInvalidEndpoint)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !PublicAddr(ip) {
			return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
		}
		return nil
	}
	if !strings.Contains(host, ".") || host == "localhost" {
		return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
	}
	for _, suffix := range []string{".localhost", ".local", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
		}
	}
	return nil
}

// NewWebhookSecret returns a signing secret: 256 random bits, hex, prefixed so
// a leaked one is recognisable in a log or a repository scan.
func NewWebhookSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	return "whsec_" + hex.EncodeToString(b)
}
```

`id.go`, in the prefix block: `PrefixWebhookEndpoint = "whe_"`.

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/domain/... -race && gofmt -l ./internal && go vet ./...`
Expected: PASS; no output from gofmt.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain
git commit -m "feat(webhooks): event list, ping routed by endpoint, public-only URLs, generated secrets"
```

---

### Task 8: Webhook repository — add, remove, rotate, ping

**Files:**
- Modify: `api/internal/repositories/webhooks.go`
- Test: `api/tests/integration/webhooks_repo_test.go` (new)

**Interfaces:**
- Consumes: Task 7 (`EventPing`, `Event.EndpointID`, `AddedBy`, `Provisioned`), Task 4 (`EntityWebhookEndpoint`, audit actions), `buildAuditItem`, `BuildEventTxItem`, `str`, `numberValue`.
- Produces:

```go
var ErrEndpointLimit, ErrEndpointProvisioned, ErrEndpointsChanged error
func (r *WebhookRepository) AddEndpoint(ctx context.Context, e *billing.WebhookEndpoint, max int, actor, requestID string, now time.Time) error
func (r *WebhookRepository) RemoveEndpoint(ctx context.Context, e *billing.WebhookEndpoint, actor, requestID string, now time.Time) error
func (r *WebhookRepository) RotateEndpointSecret(ctx context.Context, e *billing.WebhookEndpoint, secret, actor, requestID string, now time.Time) error
func (r *WebhookRepository) EmitPing(ctx context.Context, e *billing.WebhookEndpoint, now time.Time) (*billing.Event, error)
```

- [ ] **Step 1: Write the failing tests** — `api/tests/integration/webhooks_repo_test.go`

```go
//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func merchantEndpoint(org *billing.Organization) *billing.WebhookEndpoint {
	return &billing.WebhookEndpoint{
		ID: id.NewWithPrefix(id.PrefixWebhookEndpoint), OrganizationID: org.ID, Livemode: org.Livemode,
		URL: "https://hooks.example.com/billing", Secret: billing.NewWebhookSecret(),
		Status: billing.EndpointActive, AddedBy: "usr_1", AddedAt: "2026-10-10T12:00:00Z",
	}
}

func TestAddCountsEveryEndpointOfTheTenantAndMode(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	// A provisioned one counts too (scope decision 8).
	if err := repo.CreateEndpoint(ctx, &billing.WebhookEndpoint{
		ID: "whe_" + id.New(), OrganizationID: org.ID, Livemode: true,
		URL: "https://dfe.example.com/hook", Secret: billing.NewWebhookSecret(),
	}, now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); !errors.Is(err, repositories.ErrEndpointLimit) {
		t.Fatalf("sixth: %v, want ErrEndpointLimit", err)
	}
	// The other mode is another tenant.
	test := *org
	test.Livemode = false
	if err := repo.AddEndpoint(ctx, merchantEndpoint(&test), 5, "user:usr_1", "req", now()); err != nil {
		t.Fatalf("test mode: %v", err)
	}
}

// Review Focus 4.
func TestConcurrentAddsNeverPassTheLimit(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now())
			if err != nil && !errors.Is(err, repositories.ErrEndpointLimit) && !errors.Is(err, repositories.ErrEndpointsChanged) {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	list, err := repo.ListEndpoints(ctx, org.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) > 5 || len(list) == 0 {
		t.Fatalf("%d endpoints after 8 concurrent adds", len(list))
	}
}

func TestRemoveFreesASlotAndRefusesProvisioned(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	var added []*billing.WebhookEndpoint
	for i := 0; i < 5; i++ {
		e := merchantEndpoint(org)
		if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
			t.Fatal(err)
		}
		added = append(added, e)
	}
	if err := repo.RemoveEndpoint(ctx, added[0], "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); err != nil {
		t.Fatalf("add after remove: %v", err)
	}
	provisioned := &billing.WebhookEndpoint{ID: "whe_" + id.New(), OrganizationID: org.ID, Livemode: true}
	if err := repo.RemoveEndpoint(ctx, provisioned, "user:usr_1", "req", now()); !errors.Is(err, repositories.ErrEndpointProvisioned) {
		t.Fatalf("provisioned: %v", err)
	}
}

func TestRotateReplacesTheSealedSecret(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	e := merchantEndpoint(org)
	if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	next := billing.NewWebhookSecret()
	if err := repo.RotateEndpointSecret(ctx, e, next, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetEndpoint(ctx, org.ID, true, e.ID)
	if err != nil || got.Secret != next {
		t.Fatalf("after rotate: %v, secret changed=%v", err, got != nil && got.Secret == next)
	}
}

func TestAPingIsQueuedForItsEndpoint(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	e := merchantEndpoint(org)
	if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	ev, err := repo.EmitPing(ctx, e, now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetEvent(ctx, org.ID, true, ev.ID)
	if err != nil || got.Type != billing.EventPing || got.EndpointID != e.ID || got.EntityID != e.ID {
		t.Fatalf("event = %+v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'AddCounts|ConcurrentAdds|RemoveFrees|RotateReplaces|APingIsQueued' -v`
Expected: build failure (`AddEndpoint` undefined).

- [ ] **Step 3: Implement** — `webhooks.go`

Struct and constructor gain the audit table:

```go
type WebhookRepository struct {
	base  Base
	audit Base
	seal  *crypto.Sealer
}

func NewWebhookRepository(db *dynamodb.Client, cfg *config.Config) *WebhookRepository {
	return &WebhookRepository{
		base:  NewBase(db, cfg, TableWebhooks),
		audit: NewBase(db, cfg, TableAudit),
		seal:  crypto.NewSealer(cfg.FieldEncryptionKey),
	}
}
```

Append a console section:

```go
// ── Console writes (spec § 5.2) ─────────────────────────────────────────────

// skEndpointRev is one row per tenant and mode whose `rev` every console add
// and remove moves. An add conditions on the rev it read before counting, so
// two concurrent adds cannot both see four endpoints and both write a fifth.
// Not "ENDPOINT#…": ListEndpoints reads that prefix.
const skEndpointRev = "ENDPOINT_REV"

var (
	// ErrEndpointLimit: the tenant already has the most endpoints a mode may.
	ErrEndpointLimit = errors.New("webhook endpoint limit reached")
	// ErrEndpointProvisioned: the endpoint came from the tenant plan.
	ErrEndpointProvisioned = errors.New("webhook endpoint provisioned by the tenant plan")
	// ErrEndpointsChanged: other adds kept moving the count; the caller retries later.
	ErrEndpointsChanged = errors.New("the tenant's webhook endpoints changed during the request")
)

type endpointRevRow struct {
	Rev int64 `dynamodbav:"rev"`
}

func (r *WebhookRepository) endpointRev(ctx context.Context, organizationID string, livemode bool) (int64, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), skEndpointRev)
	if err != nil || item == nil {
		return 0, err
	}
	row, err := Decode[endpointRevRow](item)
	if err != nil {
		return 0, err
	}
	return row.Rev, nil
}

// bumpRev moves the revision. seen < 0 means unconditional (a removal only
// makes room, so it need not win a race; it must only be seen by one).
func (r *WebhookRepository) bumpRev(organizationID string, livemode bool, seen int64, now time.Time) types.TransactWriteItem {
	sk := skEndpointRev
	values := map[string]types.AttributeValue{":one": numberValue(1), ":now": str(now.UTC().Format(time.RFC3339Nano))}
	cond := ""
	switch {
	case seen == 0:
		cond = "attribute_not_exists(rev)"
	case seen > 0:
		cond = "rev = :seen"
		values[":seen"] = numberValue(seen)
	}
	return r.base.BuildRawUpdateTxItem(TenantPK(organizationID, livemode), &sk,
		"SET updated_at = :now ADD rev :one", cond, nil, values)
}

func (r *WebhookRepository) endpointAudit(e *billing.WebhookEndpoint, action, before, after, actor, requestID string, now time.Time) (types.TransactWriteItem, error) {
	item, err := buildAuditItem(e.OrganizationID, e.Livemode, AuditEntry{
		Entity: EntityWebhookEndpoint, EntityID: e.ID, Action: action,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, before, after, now)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.audit.BuildPutTxItemIfAbsent(item), nil
}

// AddEndpoint registers a merchant's endpoint from the console: at most max per
// tenant and mode, counting every endpoint there (provisioned ones too), with
// the audit row. Up to three attempts when concurrent adds move the count.
func (r *WebhookRepository) AddEndpoint(ctx context.Context, e *billing.WebhookEndpoint, max int, actor, requestID string, now time.Time) error {
	if e.AddedBy == "" || actor == "" {
		return fmt.Errorf("repositories: adding endpoint %s needs who added it", e.ID)
	}
	if e.Status == "" {
		e.Status = billing.EndpointActive
	}
	if err := e.Validate(); err != nil {
		return err
	}
	sealed, err := r.seal.Seal(e.Secret)
	if err != nil {
		return fmt.Errorf("sealing endpoint secret: %w", err)
	}
	row := endpointRow{
		keys:            newKeys(TenantPK(e.OrganizationID, e.Livemode), EndpointSK(e.ID), RetentionCredential, now),
		WebhookEndpoint: *e,
	}
	row.WebhookEndpoint.Secret = sealed
	item, err := Encode(row)
	if err != nil {
		return err
	}
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointCreated, "", e.URL, actor, requestID, now)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		rev, err := r.endpointRev(ctx, e.OrganizationID, e.Livemode)
		if err != nil {
			return err
		}
		existing, err := r.ListEndpoints(ctx, e.OrganizationID, e.Livemode)
		if err != nil {
			return err
		}
		if len(existing) >= max {
			return fmt.Errorf("%w: %d", ErrEndpointLimit, max)
		}
		err = r.base.TransactWrite(ctx, txItems(
			r.base.BuildPutTxItemIfAbsent(item),
			r.bumpRev(e.OrganizationID, e.Livemode, rev, now),
			audit,
		))
		// Lost to a concurrent add or remove (the rev moved, or the two
		// transactions collided): count again.
		if !IsConditionFailed(err) && !IsTransactionConflict(err) {
			return err
		}
	}
	return ErrEndpointsChanged
}

// RemoveEndpoint deletes a console endpoint. Its queued deliveries close as
// "endpoint no longer exists" on the delivery job's next pass.
func (r *WebhookRepository) RemoveEndpoint(ctx context.Context, e *billing.WebhookEndpoint, actor, requestID string, now time.Time) error {
	if e.Provisioned() {
		return fmt.Errorf("%w: %s", ErrEndpointProvisioned, e.ID)
	}
	del := types.TransactWriteItem{Delete: &types.Delete{
		TableName: aws.String(r.base.TableName),
		Key: map[string]types.AttributeValue{
			"pk": str(TenantPK(e.OrganizationID, e.Livemode)),
			"sk": str(EndpointSK(e.ID)),
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(added_by)"),
	}}
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointDeleted, e.URL, "", actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(del, r.bumpRev(e.OrganizationID, e.Livemode, -1, now), audit))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: webhook endpoint %s", ErrNotFound, e.ID)
	}
	return conflictErr(err)
}

// RotateEndpointSecret replaces the signing secret at once (scope decision 10).
func (r *WebhookRepository) RotateEndpointSecret(ctx context.Context, e *billing.WebhookEndpoint, secret, actor, requestID string, now time.Time) error {
	if e.Provisioned() {
		return fmt.Errorf("%w: %s", ErrEndpointProvisioned, e.ID)
	}
	if len(secret) < 32 {
		return fmt.Errorf("%w: secret must be at least 32 characters", billing.ErrInvalidEndpoint)
	}
	sealed, err := r.seal.Seal(secret)
	if err != nil {
		return fmt.Errorf("sealing endpoint secret: %w", err)
	}
	sk := EndpointSK(e.ID)
	update := r.base.BuildRawUpdateTxItem(TenantPK(e.OrganizationID, e.Livemode), &sk,
		"SET secret = :s, updated_at = :now", "attribute_exists(pk) AND attribute_exists(added_by)", nil,
		map[string]types.AttributeValue{":s": str(sealed), ":now": str(now.UTC().Format(time.RFC3339Nano))})
	audit, err := r.endpointAudit(e, billing.AuditWebhookEndpointSecretRotated, "", "", actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(update, audit))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: webhook endpoint %s", ErrNotFound, e.ID)
	}
	if err == nil {
		e.Secret = secret
	}
	return conflictErr(err)
}

// EmitPing queues the console's test event for one endpoint. It goes through
// the same fan-out and delivery as any event (spec § 5.2), signed with the
// endpoint's secret, retried on failure, counted in the endpoint's health.
func (r *WebhookRepository) EmitPing(ctx context.Context, e *billing.WebhookEndpoint, now time.Time) (*billing.Event, error) {
	ev := &billing.Event{
		ID: id.NewWithPrefix(id.PrefixEvent), OrganizationID: e.OrganizationID, Livemode: e.Livemode,
		Type: billing.EventPing, Entity: "webhook_endpoint", EntityID: e.ID, EndpointID: e.ID,
	}
	item, err := r.BuildEventTxItem(ev, now)
	if err != nil {
		return nil, err
	}
	if err := r.base.TransactWrite(ctx, txItems(item)); err != nil {
		return nil, err
	}
	return ev, nil
}
```

(imports: `errors`, `github.com/aws/aws-sdk-go-v2/aws`, `gopkg.aoctech.app/billing/api/internal/domain/id`. `IsTransactionConflict` and `conflictErr` are the package's existing helpers; `conflictErr(nil)` is nil.)

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/... -race` then `make test-integration`
Expected: PASS, the existing `webhooks_test.go` delivery tests included.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/webhooks.go api/tests/integration/webhooks_repo_test.go
git commit -m "feat(webhooks): console add, remove, rotate and ping, at most five per tenant and mode"
```

---

### Task 9: Merchant endpoints are delivered through an SSRF-guarded client

**Files:**
- Modify: `api/internal/services/delivering.go`
- Test: `api/internal/services/delivering_test.go` (new)

**Interfaces:**
- Consumes: `billing.PublicAddr`, `WebhookEndpoint.Provisioned` (Task 7).
- Produces: `Deliverer.merchant *http.Client`; `func newMerchantClient() *http.Client`; `var errPrivateAddress`; `(*Deliverer).clientFor(e) *http.Client`. `SetHTTPClient` now replaces both clients (tests).

- [ ] **Step 1: Write the failing tests** — `api/internal/services/delivering_test.go`

```go
package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// Review Focus 3: a merchant URL that resolves to a private address. The test
// server listens on 127.0.0.1, which is exactly such an address.
func TestAMerchantEndpointNeverDialsAPrivateAddress(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	d := NewDeliverer(nil)
	e := &billing.WebhookEndpoint{ID: "whe_1", URL: srv.URL, Secret: billing.NewWebhookSecret(), AddedBy: "usr_1"}
	_, err := d.post(context.Background(), e, &billing.Event{ID: "evt_1", Type: billing.EventPing}, time.Now())
	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("err = %v, want errPrivateAddress", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the request reached the private address")
	}
}

// A provisioned endpoint (tenant zero's whe_dfe is on CTech's private network)
// keeps the plain client.
func TestAProvisionedEndpointKeepsThePlainClient(t *testing.T) {
	d := NewDeliverer(nil)
	if d.clientFor(&billing.WebhookEndpoint{ID: "whe_dfe"}) != d.client {
		t.Fatal("a provisioned endpoint got the guarded client")
	}
	if d.clientFor(&billing.WebhookEndpoint{ID: "whe_1", AddedBy: "usr_1"}) != d.merchant {
		t.Fatal("a merchant endpoint got the plain client")
	}
}

func TestAMerchantEndpointFollowsNoRedirect(t *testing.T) {
	c := newMerchantClient()
	if c.CheckRedirect == nil || !errors.Is(c.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("the merchant client follows redirects")
	}
	if tr, ok := c.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatal("the merchant client may go through a proxy, which the dial guard would not see")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/services/ -run 'MerchantEndpoint|ProvisionedEndpoint' -v`
Expected: FAIL, undefined `errPrivateAddress`, `clientFor`, `newMerchantClient`.

- [ ] **Step 3: Implement** — `delivering.go`

```go
type Deliverer struct {
	hooks  *repositories.WebhookRepository
	client *http.Client
	// merchant delivers to endpoints a merchant registered in the console. Its
	// dialer refuses any non-public address after DNS resolution, so neither a
	// hostname pointed at 10.x nor a DNS answer that changes after the create
	// check reaches CTech's network; it follows no redirect (a 3xx is a failed
	// attempt) and uses no proxy.
	merchant *http.Client
}

var errPrivateAddress = errors.New("webhook host resolves to a non-public address")

func newMerchantClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: refusePrivate}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	return &http.Client{
		Timeout:   deliveryTimeout,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// refusePrivate runs after resolution, on the address actually dialled.
func refusePrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("webhook dial %q: %w", address, err)
	}
	if !billing.PublicAddr(ap.Addr()) {
		return errPrivateAddress
	}
	return nil
}

func NewDeliverer(hooks *repositories.WebhookRepository) *Deliverer {
	return &Deliverer{
		hooks:    hooks,
		client:   &http.Client{Timeout: deliveryTimeout},
		merchant: newMerchantClient(),
	}
}

// SetHTTPClient replaces both clients. Tests only: they deliver to httptest
// servers on 127.0.0.1, which the merchant client would refuse by design.
func (d *Deliverer) SetHTTPClient(c *http.Client) { d.client, d.merchant = c, c }

// clientFor picks the guarded client for anything a merchant registered.
func (d *Deliverer) clientFor(e *billing.WebhookEndpoint) *http.Client {
	if e.Provisioned() {
		return d.client
	}
	return d.merchant
}
```

and in `post`, `resp, err := d.client.Do(req)` becomes `resp, err := d.clientFor(e).Do(req)`. Imports: `errors`, `net`, `net/netip`, `syscall`.

A 3xx answered under `ErrUseLastResponse` has status 3xx and falls into the existing "endpoint answered %d" branch: a failed attempt, retried and counted in the endpoint's health.

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/services/ -race && gofmt -l ./internal && go vet ./...` then `make test-integration` (the existing delivery tests use `SetHTTPClient` and must stay green)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/delivering.go api/internal/services/delivering_test.go
git commit -m "feat(webhooks): deliver to merchant endpoints through a client that never dials a private address"
```

---

### Task 10: Console webhook routes

**Files:**
- Create: `api/internal/api/v1/console_webhooks.go`
- Modify: `api/internal/api/v1/console_integrations.go` (the five routes)
- Test: `api/tests/integration/console_webhooks_test.go` (new)
- Modify: `README.md` (route tables)

**Interfaces:**
- Consumes: Tasks 6 (`registerIntegrations`, `isSuspended`, `refuseSuspended`, `me`, `problemBody`, `writeToken`, `suspendLive`), 7, 8; `limits.WebhookURL`, `limits.MaxWebhookEndpoints`; `newConsumer`, `runDelivery`, `registerEndpoint`, `services.Sign` (existing test helpers).
- Produces: wire shapes (UI Task 11 mirrors them):

```json
GET /v1.0/console/webhooks → 200
{"data": [{"id": "whe_…", "url": "https://…", "events": [], "status": "active", "failure_run": 0,
           "disabled_at": "", "provisioned": false, "added_at": "2026-10-10T12:00:00Z", "owner_key": ""}],
 "event_types": ["checkout.session.canceled", "…"], "max_endpoints": 5}
POST /v1.0/console/webhooks {"url": "https://…", "events": ["invoice.paid"]} → 201 {…endpoint, "secret": "whsec_…"}
POST /v1.0/console/webhooks/:id/rotate-secret → 200 {…endpoint, "secret": "whsec_…"}
POST /v1.0/console/webhooks/:id/test → 202 {"event_id": "evt_…"}
DELETE /v1.0/console/webhooks/:id → 204
```

`events: []` means every event. `secret` appears only in the create and rotate bodies.

- [ ] **Step 1: Write the failing tests** — `api/tests/integration/console_webhooks_test.go`

```go
//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

const hooksPath = "/v1.0/console/webhooks"

type endpointBody struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Status      string   `json:"status"`
	Provisioned bool     `json:"provisioned"`
	Secret      string   `json:"secret"`
}

type endpointList struct {
	Data         []endpointBody `json:"data"`
	EventTypes   []string       `json:"event_types"`
	MaxEndpoints int            `json:"max_endpoints"`
}

func (e *apiEnv) addHook(t *testing.T, token, url string, events ...string) apiResponse {
	t.Helper()
	if events == nil {
		events = []string{}
	}
	body, _ := json.Marshal(map[string]any{"url": url, "events": events})
	return e.consoleWrite(t, http.MethodPost, hooksPath, token, "live", string(body))
}

// deliverTo sends requests for example.com to c and trusts c's certificate
// (httptest's certificate is valid for example.com). Other hosts are dialled
// as they are: the delivery queue is shared with every other test's tenants.
func deliverTo(t *testing.T, c *consumer) *services.Deliverer {
	t.Helper()
	tr := c.server.Client().Transport.(*http.Transport).Clone()
	addr := c.server.Listener.Addr().String()
	tr.DialTLSContext = nil
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "example.com:443" {
			address = addr
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	tr.TLSClientConfig = &tls.Config{RootCAs: tr.TLSClientConfig.RootCAs, ServerName: "example.com"}
	d := services.NewDeliverer(repositories.NewWebhookRepository(testDB, testCfg))
	d.SetHTTPClient(&http.Client{Timeout: 5 * time.Second, Transport: tr})
	return d
}

// Spec § 10.5 and Review Focus 5.
func TestTheSecretAppearsInExactlyOneResponsePerCreateOrRotate(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	res := e.addHook(t, tok, "https://example.com/hooks")
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.body)
	}
	var created endpointBody
	res.decode(t, &created)
	if len(created.Secret) != len("whsec_")+64 {
		t.Fatalf("secret = %q", created.Secret)
	}

	list := e.console(t, hooksPath, tok, "live")
	if bytes.Contains(list.body, []byte(created.Secret)) || bytes.Contains(list.body, []byte(`"secret"`)) {
		t.Fatalf("the list carries a secret: %s", list.body)
	}

	rot := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+created.ID+"/rotate-secret", tok, "live", "")
	var rotated endpointBody
	rot.decode(t, &rotated)
	if rot.status != http.StatusOK || rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatalf("rotate: %d %+v", rot.status, rotated)
	}
	list = e.console(t, hooksPath, tok, "live")
	if bytes.Contains(list.body, []byte(rotated.Secret)) || bytes.Contains(list.body, []byte(created.Secret)) {
		t.Fatalf("the list after rotate carries a secret: %s", list.body)
	}

	// Sealed at rest.
	item, err := testDB.GetItem(ctxT(t), &dynamodb.GetItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableWebhooks)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(e.org.ID, true)},
			"sk": &types.AttributeValueMemberS{Value: repositories.EndpointSK(created.ID)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored := item.Item["secret"].(*types.AttributeValueMemberS).Value; stored == rotated.Secret {
		t.Fatal("the secret is stored in the clear")
	}

	del := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+created.ID, tok, "live", "")
	if del.status != http.StatusNoContent || len(del.body) != 0 {
		t.Fatalf("delete: %d %s", del.status, del.body)
	}
}

func TestWebhookURLsMustBeHTTPSAndPublic(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	for url, code := range map[string]string{
		"http://example.com/hooks":     "invalid_format",
		"not a url":                    "invalid_format",
		"https://127.0.0.1/hooks":      "unsupported_value",
		"https://localhost/hooks":      "unsupported_value",
		"https://billing.internal/x":   "unsupported_value",
		"https://169.254.169.254/meta": "unsupported_value",
	} {
		res := e.addHook(t, tok, url)
		var p struct {
			Code   string `json:"code"`
			Errors []struct {
				Field, Code string
			} `json:"errors"`
		}
		res.decode(t, &p)
		if res.status != 422 || len(p.Errors) != 1 || p.Errors[0].Field != "url" || p.Errors[0].Code != code {
			t.Errorf("%s: %d %s", url, res.status, res.body)
		}
	}
}

func TestOnlyListedEventsAreAccepted(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	for _, events := range [][]string{{"invoice.paid", "nope.never"}, {"ping"}} {
		if res := e.addHook(t, tok, "https://example.com/hooks", events...); res.status != 422 {
			t.Errorf("%v: %d %s", events, res.status, res.body)
		}
	}
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if list.MaxEndpoints != 5 || len(list.EventTypes) != len(billing.WebhookEventTypes()) {
		t.Fatalf("list meta = %d %v", list.MaxEndpoints, list.EventTypes)
	}
}

func TestTheSixthEndpointIsRefused(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var first endpointBody
	for i := 0; i < 5; i++ {
		res := e.addHook(t, tok, fmt.Sprintf("https://example.com/hooks/%d", i))
		if res.status != http.StatusCreated {
			t.Fatalf("create %d: %d %s", i, res.status, res.body)
		}
		if i == 0 {
			res.decode(t, &first)
		}
	}
	res := e.addHook(t, tok, "https://example.com/hooks/6")
	var p problemBody
	res.decode(t, &p)
	if res.status != 422 || p.Code != "webhook_endpoint_limit" {
		t.Fatalf("sixth: %d %+v", res.status, p)
	}
	e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+first.ID, tok, "live", "")
	if res := e.addHook(t, tok, "https://example.com/hooks/7"); res.status != http.StatusCreated {
		t.Fatalf("after a delete: %d", res.status)
	}
}

// The test call is a ping through the normal fan-out and delivery, signed with
// the endpoint's secret, and only that endpoint receives it.
func TestTheTestCallSendsAPingThroughNormalDelivery(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	c := newConsumer(t, http.StatusOK)

	var target, other endpointBody
	e.addHook(t, tok, "https://example.com/target", "invoice.paid").decode(t, &target)
	e.addHook(t, tok, "https://example.com/other").decode(t, &other)

	res := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+target.ID+"/test", tok, "live", "")
	var accepted struct {
		EventID string `json:"event_id"`
	}
	res.decode(t, &accepted)
	if res.status != http.StatusAccepted || accepted.EventID == "" {
		t.Fatalf("test: %d %s", res.status, res.body)
	}

	runDelivery(t, deliverTo(t, c), e.org, now())
	// Both endpoints point at c; the ping must arrive once (the target), never twice.
	var pings []received
	for _, d := range c.deliveries() {
		if d.payload.ID == accepted.EventID {
			pings = append(pings, d)
		}
	}
	if len(pings) != 1 {
		t.Fatalf("the ping arrived %d times, want 1 (the target only)", len(pings))
	}
	d := pings[0]
	if d.payload.Type != billing.EventPing ||
		d.payload.Data.Object != "webhook_endpoint" || d.payload.Data.ID != target.ID {
		t.Fatalf("payload = %+v", d.payload)
	}
	if d.signature != "v1="+services.Sign(target.Secret, d.timestamp, d.body) {
		t.Fatal("the ping is not signed with the endpoint's secret")
	}
}

func TestProvisionedEndpointsAreReadOnlyInTheConsole(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	seeded := registerEndpoint(t, e.org, newConsumer(t, http.StatusOK), "dfe")
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if len(list.Data) != 1 || !list.Data[0].Provisioned {
		t.Fatalf("list = %+v", list.Data)
	}
	for _, path := range []string{"/" + seeded.ID + "/rotate-secret", "/" + seeded.ID + "/test"} {
		res := e.consoleWrite(t, http.MethodPost, hooksPath+path, tok, "live", "")
		var p problemBody
		res.decode(t, &p)
		if res.status != http.StatusConflict || p.Code != "webhook_endpoint_provisioned" {
			t.Errorf("%s: %d %+v", path, res.status, p)
		}
	}
	res := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+seeded.ID, tok, "live", "")
	if res.status != http.StatusConflict {
		t.Errorf("delete: %d", res.status)
	}
}

func TestADisabledEndpointRefusesTheTest(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var ep endpointBody
	e.addHook(t, tok, "https://example.com/hooks").decode(t, &ep)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	full, err := repo.GetEndpoint(ctxT(t), e.org.ID, true, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	full.FailureRun = billing.MaxFailureRun - 1
	if err := repo.RecordEndpointHealth(ctxT(t), full, false, now()); err != nil {
		t.Fatal(err)
	}
	res := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", "")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusConflict || p.Code != "webhook_endpoint_disabled" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

// Decision 1 for webhooks: delete and rotate yes; create and test no.
func TestASuspendedTenantCanOnlyShrinkOrSecureItsWebhooks(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var ep endpointBody
	e.addHook(t, tok, "https://example.com/hooks").decode(t, &ep)
	suspendLive(t, e.org)

	for _, res := range []apiResponse{
		e.addHook(t, tok, "https://example.com/other"),
		e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", ""),
	} {
		var p problemBody
		res.decode(t, &p)
		if res.status != http.StatusForbidden || p.Code != "tenant_suspended" {
			t.Errorf("%d %+v", res.status, p)
		}
	}
	if r := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/rotate-secret", tok, "live", ""); r.status != http.StatusOK {
		t.Errorf("rotate while suspended: %d", r.status)
	}
	if r := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+ep.ID, tok, "live", ""); r.status != http.StatusNoContent {
		t.Errorf("delete while suspended: %d", r.status)
	}
}

func TestConcurrentConsoleCreatesStayWithinTheLimit(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := e.addHook(t, tok, fmt.Sprintf("https://example.com/c/%d", i))
			if res.status != http.StatusCreated && res.status != 422 && res.status != http.StatusConflict {
				t.Errorf("%d %s", res.status, res.body)
			}
		}()
	}
	wg.Wait()
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if len(list.Data) > 5 {
		t.Fatalf("%d endpoints", len(list.Data))
	}
}
```

(`registerEndpoint` creates through `CreateEndpoint` with no `AddedBy`: a provisioned endpoint. `RecordEndpointHealth` with `FailureRun = MaxFailureRun-1` and a failure disables it.)

- [ ] **Step 2: Run to verify they fail**

Run: `DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/ -run 'Secret|WebhookURLs|ListedEvents|SixthEndpoint|TestCall|ReadOnlyInTheConsole|DisabledEndpoint|ShrinkOrSecure|ConcurrentConsoleCreates' -v`
Expected: FAIL, `404` on `/v1.0/console/webhooks`.

- [ ] **Step 3: Implement**

`console_integrations.go`, replacing the `// Task 10 adds the webhook routes here.` line:

```go
	console.Get("/webhooks", read, ch.listWebhooks)
	console.Post("/webhooks", write, ch.createWebhook)
	console.Delete("/webhooks/:id", write, ch.deleteWebhook)
	console.Post("/webhooks/:id/rotate-secret", write, ch.rotateWebhookSecret)
	console.Post("/webhooks/:id/test", write, ch.testWebhook)
```

`console_webhooks.go`:

```go
package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// webhookEndpointResponse never has a secret field: the secret is only in
// webhookSecretResponse, which only create and rotate answer with.
type webhookEndpointResponse struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"` // [] is every event
	Status      string   `json:"status"`
	FailureRun  int      `json:"failure_run"`
	DisabledAt  string   `json:"disabled_at,omitempty"`
	Provisioned bool     `json:"provisioned"`
	AddedAt     string   `json:"added_at,omitempty"`
	OwnerKey    string   `json:"owner_key,omitempty"`
}

type webhookSecretResponse struct {
	webhookEndpointResponse
	Secret string `json:"secret"`
}

type webhookListResponse struct {
	Data         []webhookEndpointResponse `json:"data"`
	EventTypes   []billing.EventType       `json:"event_types"`
	MaxEndpoints int                       `json:"max_endpoints"`
}

type createWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

func newWebhookEndpointResponse(e *billing.WebhookEndpoint) webhookEndpointResponse {
	events := make([]string, 0, len(e.Events))
	for _, t := range e.Events {
		events = append(events, string(t))
	}
	return webhookEndpointResponse{
		ID: e.ID, URL: e.URL, Events: events, Status: string(e.Status), FailureRun: e.FailureRun,
		DisabledAt: e.DisabledAt, Provisioned: e.Provisioned(), AddedAt: e.AddedAt, OwnerKey: e.OwnerKey,
	}
}

func (h *consoleHandlers) listWebhooks(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	endpoints, err := h.hooks.ListEndpoints(c.Context(), t.OrganizationID, t.Livemode)
	if err != nil {
		return fail(c, err)
	}
	out := webhookListResponse{
		Data:         make([]webhookEndpointResponse, 0, len(endpoints)),
		EventTypes:   billing.WebhookEventTypes(),
		MaxEndpoints: limits.MaxWebhookEndpoints,
	}
	for i := range endpoints {
		out.Data = append(out.Data, newWebhookEndpointResponse(&endpoints[i]))
	}
	return c.JSON(out)
}

func (h *consoleHandlers) createWebhook(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	var req createWebhookRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.webhookURL("url", req.URL)
	events := ch.webhookEvents("events", req.Events)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	t := middleware.GetTenant(c)
	now := h.now()
	e := &billing.WebhookEndpoint{
		ID: id.NewWithPrefix(id.PrefixWebhookEndpoint), OrganizationID: t.OrganizationID, Livemode: t.Livemode,
		URL: req.URL, Secret: billing.NewWebhookSecret(), Events: events, Status: billing.EndpointActive,
		AddedBy: me(c), AddedAt: now.UTC().Format(time.RFC3339),
	}
	err := h.hooks.AddEndpoint(c.Context(), e, limits.MaxWebhookEndpoints, actorOfUser(c), middleware.GetRequestID(c), now)
	switch {
	case errors.Is(err, repositories.ErrEndpointLimit):
		return problem.Unprocessable("this mode already has the most webhook endpoints allowed").WithCode("webhook_endpoint_limit").Send(c)
	case errors.Is(err, repositories.ErrEndpointsChanged):
		return problem.New(http.StatusConflict, problem.TypeConcurrentUpdate, "Concurrent Update",
			"the endpoints changed while saving; try again").WithCode("concurrent_update").Send(c)
	case err != nil:
		return fail(c, err)
	}
	return c.Status(http.StatusCreated).JSON(webhookSecretResponse{newWebhookEndpointResponse(e), e.Secret})
}

// consoleEndpoint reads the endpoint named in the path and refuses a
// provisioned one: every write below is for merchant endpoints only.
func (h *consoleHandlers) consoleEndpoint(c fiber.Ctx) (*billing.WebhookEndpoint, error) {
	t := middleware.GetTenant(c)
	e, err := h.hooks.GetEndpoint(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return nil, fail(c, err)
	}
	if e.Provisioned() {
		return nil, problem.Conflict("this endpoint is managed by CTech").WithCode("webhook_endpoint_provisioned").Send(c)
	}
	return e, nil
}

// deleteWebhook is allowed on a suspended tenant (scope decision 1).
func (h *consoleHandlers) deleteWebhook(c fiber.Ctx) error {
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	if err := h.hooks.RemoveEndpoint(c.Context(), e, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

// rotateWebhookSecret is allowed on a suspended tenant: a leaked secret must be
// replaceable whatever the account's standing.
func (h *consoleHandlers) rotateWebhookSecret(c fiber.Ctx) error {
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	secret := billing.NewWebhookSecret()
	if err := h.hooks.RotateEndpointSecret(c.Context(), e, secret, actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(webhookSecretResponse{newWebhookEndpointResponse(e), secret})
}

func (h *consoleHandlers) testWebhook(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	e, sent := h.consoleEndpoint(c)
	if e == nil {
		return sent
	}
	if e.Status != billing.EndpointActive {
		return problem.Conflict("this endpoint is disabled").WithCode("webhook_endpoint_disabled").Send(c)
	}
	ev, err := h.hooks.EmitPing(c.Context(), e, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(http.StatusAccepted).JSON(fiber.Map{"event_id": ev.ID})
}
```

`validate.go`:

```go
// webhookURL: a merchant endpoint URL (spec § 5.2, scope decision 9).
func (c *checks) webhookURL(field, v string) {
	c.text(field, v, true, limits.WebhookURL)
	if len(c.errs) > 0 && c.errs[len(c.errs)-1].Field == field {
		return
	}
	switch err := billing.CheckWebhookURL(v); {
	case errors.Is(err, billing.ErrWebhookHostNotAllowed):
		c.fail(field, "unsupported_value", "the host must be on the public internet")
	case err != nil:
		c.fail(field, "invalid_format", "an https URL is required")
	}
}

// webhookEvents: each from the event list, duplicates dropped, [] for all.
func (c *checks) webhookEvents(field string, in []string) []billing.EventType {
	out := make([]billing.EventType, 0, len(in))
	for _, raw := range in {
		t := billing.EventType(raw)
		if !billing.IsWebhookEventType(t) {
			c.fail(field, "unsupported_value", "not a webhook event type")
			return nil
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
```

(Check `problem.FieldError`'s field name for the attribute: if it is not `Field`, use the struct's name; `problem.Field(field, …)` builds it. Imports `errors`, `slices` in `validate.go`.)

`README.md`: add to "Console writes"

```markdown
| `POST /v1.0/console/webhooks` · `DELETE /v1.0/console/webhooks/:id` · `POST /v1.0/console/webhooks/:id/rotate-secret` · `POST /v1.0/console/webhooks/:id/test` | `billing:organization:write` |
```

and change the read row to `| `GET /v1.0/console/credentials` · `GET /v1.0/console/webhooks` | `billing:organization:read` |`, with a paragraph:

```markdown
Webhook endpoints from the console are `https` URLs on the public internet, at
most five per tenant and mode. The signing secret is in the create and
rotate-secret responses only. The test call queues a `ping` event for that
endpoint through the ordinary delivery job (every minute). Merchant endpoints
are delivered by a client that never dials a non-public address and follows no
redirect.
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/... -race && gofmt -l ./internal ./tests && go vet ./...` then `make test-integration`
Expected: PASS, including part 1's spoofing test over the five new routes.

- [ ] **Step 5: Commit**

```bash
git add api/internal api/tests/integration/console_webhooks_test.go README.md
git commit -m "feat(console): list, create, delete, rotate and test webhook endpoints"
```

---
## Billing UI tasks (`ui/`)

Run no UI command while a Go suite is running. Read `ui/AGENTS.md` first: this is Next.js 16; check `node_modules/next/dist/docs/` before using any Next API not already used in `ui/src`.

### Task 11: UI — the API layer, the developer link and the error catalogue

> Execute with `/impeccable` loaded (it owns the copy rules). No screen here; the strings are UI text: pt-BR and en, no em dash.

**Files:**
- Modify: `ui/src/lib/api/consoleTypes.ts`, `ui/src/lib/api/console.ts`, `ui/src/lib/api/console.test.ts`
- Create: `ui/src/lib/console/developerLink.ts`, `ui/src/lib/console/developerLink.test.ts`, `ui/src/lib/console/addsRefused.ts`
- Modify: `ui/src/lib/format.ts` (`instantDate`)
- Modify: `ui/src/locales/pt-BR/errors.json`, `ui/src/locales/en/errors.json`, `ui/src/lib/errors.test.ts`
- Modify: `ui/src/dev/consoleMockData.ts`, `ui/src/dev/mockRuntime.ts`

**Interfaces:**
- Consumes: part 1's `ConsoleScope`, `scopeHeaders`, `consoleKeys`, `getConsoleSession`, `useConsoleScope`; the wire shapes of Tasks 6 and 10.
- Produces:

```ts
export interface ConsoleCredential {client_id: string; livemode: boolean; active: boolean; provisioned: boolean; connected_by?: string; connected_by_me: boolean; connected_at?: string; description?: string}
export interface ConsoleCredentialList {data: ConsoleCredential[]; machine_scopes: string[]}
export type WebhookStatus = "active" | "disabled"
export interface ConsoleWebhookEndpoint {id: string; url: string; events: string[]; status: WebhookStatus; failure_run: number; disabled_at?: string; provisioned: boolean; added_at?: string; owner_key?: string}
export interface ConsoleWebhookList {data: ConsoleWebhookEndpoint[]; event_types: string[]; max_endpoints: number}
export interface ConsoleWebhookWithSecret extends ConsoleWebhookEndpoint {secret: string}
consoleKeys.credentials(scope) // ["console", org, mode, "credentials"]
consoleKeys.webhooks(scope)    // ["console", org, mode, "webhooks"]
listConsoleCredentials(scope): Promise<ConsoleCredentialList>
connectCredential(scope, clientId: string): Promise<ConsoleCredential>
disconnectCredential(scope, clientId: string): Promise<void>
listConsoleWebhooks(scope): Promise<ConsoleWebhookList>
createWebhook(scope, body: {url: string; events: string[]}): Promise<ConsoleWebhookWithSecret>
deleteWebhook(scope, id: string): Promise<void>
rotateWebhookSecret(scope, id: string): Promise<ConsoleWebhookWithSecret>
testWebhook(scope, id: string): Promise<{event_id: string}>
developerClientURL(scopes: string[]): string
useAddsRefused(): boolean   // live mode and the selected organization is suspended
instantDate(ts: string): string // "10/10/2026", in America/Sao_Paulo
```

- [ ] **Step 1: Write the failing tests**

Append to `ui/src/lib/api/console.test.ts` (inside the existing `describe("console API")`):

```ts
  it("sends the scope on every integration call, to English routes", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {}})
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({data: {}})
    const del = vi.spyOn(apiClient, "delete").mockResolvedValue({data: undefined})
    await listConsoleCredentials(SCOPE)
    await connectCredential(SCOPE, "cli_1")
    await disconnectCredential(SCOPE, "a/b")
    await listConsoleWebhooks(SCOPE)
    await createWebhook(SCOPE, {url: "https://example.com", events: []})
    await rotateWebhookSecret(SCOPE, "whe_1")
    await testWebhook(SCOPE, "whe_1")
    await deleteWebhook(SCOPE, "whe_1")
    const urls = [...get.mock.calls, ...post.mock.calls, ...del.mock.calls].map(c => c[0])
    expect(urls).toEqual(expect.arrayContaining([
      "/v1.0/console/credentials", "/v1.0/console/credentials/a%2Fb", "/v1.0/console/webhooks",
      "/v1.0/console/webhooks/whe_1/rotate-secret", "/v1.0/console/webhooks/whe_1/test", "/v1.0/console/webhooks/whe_1",
    ]))
    for (const call of [...get.mock.calls, ...post.mock.calls, ...del.mock.calls]) {
      const config = call[call.length - 1] as {headers: Record<string, string>}
      expect(config.headers).toEqual(expect.objectContaining({"X-Billing-Mode": "test", "X-Billing-Space": `org:${SCOPE.org}`}))
    }
    expect(post).toHaveBeenCalledWith("/v1.0/console/credentials", {client_id: "cli_1"}, expect.anything())
  })

  it("keys the integration lists by organization and mode", () => {
    expect(consoleKeys.credentials(SCOPE)).toEqual(["console", SCOPE.org, "test", "credentials"])
    expect(consoleKeys.webhooks(SCOPE)).toEqual(["console", SCOPE.org, "test", "webhooks"])
  })
```

(extend the file's import from `@/lib/api/console` with the eight functions.)

`ui/src/lib/console/developerLink.test.ts`:

```ts
import {describe, expect, it} from "vitest"

import {developerClientURL} from "@/lib/console/developerLink"

describe("developerClientURL", () => {
  it("opens ctech-account's new-client dialog with exactly these scopes", () => {
    const u = new URL(developerClientURL(["billing:invoices:read", "billing:usage:write"]))
    expect(u.pathname).toBe("/account/oauth-clients")
    expect(u.searchParams.get("new")).toBe("1")
    expect(u.searchParams.get("scopes")).toBe("billing:invoices:read,billing:usage:write")
  })
})
```

`ui/src/lib/errors.test.ts`, `BACKEND_CODES`: add

```ts
  "client_not_yours", "client_has_no_billing_scope", "client_not_machine_only", "client_already_connected",
  "credential_provisioned", "account_unavailable", "webhook_endpoint_limit", "webhook_endpoint_provisioned",
  "webhook_endpoint_disabled", "tenant_suspended",
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/api/console.test.ts src/lib/console/developerLink.test.ts src/lib/errors.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=1` (missing exports, module and catalog entries).

- [ ] **Step 3: Implement**

`consoleTypes.ts`: append the five types of **Interfaces** above, each with a one-line doc comment (`ConsoleWebhookWithSecret`: "Only create and rotate answer with this; the secret is never shown again.").

`console.ts`: in `consoleKeys` add

```ts
  credentials: (s: ConsoleScope) => ["console", s.org, s.mode, "credentials"] as const,
  webhooks: (s: ConsoleScope) => ["console", s.org, s.mode, "webhooks"] as const,
```

and append:

```ts
/** The merchant's API credentials: their own ctech-account OAuth clients (spec § 5.1). */
export async function listConsoleCredentials(scope: ConsoleScope): Promise<ConsoleCredentialList> {
  const {data} = await apiClient.get<ConsoleCredentialList>("/v1.0/console/credentials", scopeHeaders(scope))
  return data
}

export async function connectCredential(scope: ConsoleScope, clientId: string): Promise<ConsoleCredential> {
  const {data} = await apiClient.post<ConsoleCredential>("/v1.0/console/credentials", {client_id: clientId}, scopeHeaders(scope))
  return data
}

export async function disconnectCredential(scope: ConsoleScope, clientId: string): Promise<void> {
  await apiClient.delete(`/v1.0/console/credentials/${encodeURIComponent(clientId)}`, scopeHeaders(scope))
}

export async function listConsoleWebhooks(scope: ConsoleScope): Promise<ConsoleWebhookList> {
  const {data} = await apiClient.get<ConsoleWebhookList>("/v1.0/console/webhooks", scopeHeaders(scope))
  return data
}

export async function createWebhook(scope: ConsoleScope, body: {url: string; events: string[]}): Promise<ConsoleWebhookWithSecret> {
  const {data} = await apiClient.post<ConsoleWebhookWithSecret>("/v1.0/console/webhooks", body, scopeHeaders(scope))
  return data
}

export async function deleteWebhook(scope: ConsoleScope, id: string): Promise<void> {
  await apiClient.delete(`/v1.0/console/webhooks/${encodeURIComponent(id)}`, scopeHeaders(scope))
}

export async function rotateWebhookSecret(scope: ConsoleScope, id: string): Promise<ConsoleWebhookWithSecret> {
  const {data} = await apiClient.post<ConsoleWebhookWithSecret>(
    `/v1.0/console/webhooks/${encodeURIComponent(id)}/rotate-secret`, {}, scopeHeaders(scope))
  return data
}

export async function testWebhook(scope: ConsoleScope, id: string): Promise<{event_id: string}> {
  const {data} = await apiClient.post<{event_id: string}>(
    `/v1.0/console/webhooks/${encodeURIComponent(id)}/test`, {}, scopeHeaders(scope))
  return data
}
```

`ui/src/lib/console/developerLink.ts`:

```ts
/**
 * ctech-account's developer page, opened on "new client" with billing's
 * machine scopes ticked (ctech-account `?new=1&scopes=`). The scopes come from
 * the API's own list (`machine_scopes`), never from a copy here. The merchant
 * creates the client there and gives billing only its id: the secret never
 * passes through billing (spec D5).
 */
const ACCOUNTS = process.env.NEXT_PUBLIC_CTECH_CLIENT_URL || "https://accounts.aoctech.app"

export function developerClientURL(scopes: string[]): string {
  const u = new URL("/account/oauth-clients", ACCOUNTS)
  u.searchParams.set("new", "1")
  u.searchParams.set("scopes", scopes.join(","))
  return u.toString()
}
```

`ui/src/lib/console/addsRefused.ts`:

```ts
"use client"

import {useQuery} from "@tanstack/react-query"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useConsoleScope} from "@/lib/console/scope"

/**
 * Whether this screen should offer to ADD integration surface (connect, create,
 * test). A suspended live tenant may only remove or secure (scope decision 1);
 * the server refuses anyway with `tenant_suspended`, this only avoids offering
 * a button that cannot work. Display data, never authority.
 */
export function useAddsRefused(): boolean {
  const scope = useConsoleScope()
  const session = useQuery({queryKey: consoleKeys.session(), queryFn: getConsoleSession})
  const org = session.data?.organizations.find(o => o.organization_id === scope.org)
  return scope.mode === "live" && org?.merchant_status === "suspended"
}
```

`format.ts`:

```ts
/** "10/10/2026" for an RFC 3339 instant, on São Paulo's calendar (a connect at
 *  22:00 local is that day, not the next UTC one). */
export const instantDate = (ts: string) =>
  intl("instant-short", l => new Intl.DateTimeFormat(l, {day: "2-digit", month: "2-digit", year: "numeric", timeZone: "America/Sao_Paulo"}))
    .format(new Date(ts))
```

(`intl(key, make)` is the file's existing memoising helper; keep its signature.)

`errors.json` (`code` object). pt-BR:

```json
    "client_not_yours": "Esse cliente não existe ou não é seu. Confira o ID na CTech Account; só quem criou o cliente pode conectá-lo.",
    "client_has_no_billing_scope": "Esse cliente não tem nenhum escopo do CTech Billing. Adicione os escopos de integração na CTech Account.",
    "client_not_machine_only": "Esse cliente não serve para acesso de máquina: ele tem escopos que não são de integração do CTech Billing, ou não é confidencial. Crie um cliente só com os escopos indicados.",
    "client_already_connected": "Esse cliente já está conectado. Desconecte-o de onde estiver ou crie outro cliente.",
    "credential_provisioned": "Essa credencial é gerenciada pela CTech e não pode ser removida aqui.",
    "account_unavailable": "Não foi possível confirmar o cliente com a CTech Account agora. Tente de novo em instantes.",
    "webhook_endpoint_limit": "Este modo já tem o máximo de 5 endpoints. Exclua um para adicionar outro.",
    "webhook_endpoint_provisioned": "Esse endpoint é gerenciado pela CTech e não pode ser alterado aqui.",
    "webhook_endpoint_disabled": "Esse endpoint foi desativado depois de falhas seguidas. Exclua e cadastre de novo para voltar a receber.",
    "tenant_suspended": "A cobrança ao vivo desta organização está suspensa. Dá para remover integrações, não para adicionar.",
```

en:

```json
    "client_not_yours": "This client does not exist or is not yours. Check the ID in CTech Account; only the person who created a client can connect it.",
    "client_has_no_billing_scope": "This client has no CTech Billing scope. Add the integration scopes in CTech Account.",
    "client_not_machine_only": "This client cannot be used for machine access: it holds scopes that are not CTech Billing integration scopes, or it is not confidential. Create a client with only the listed scopes.",
    "client_already_connected": "This client is already connected. Disconnect it where it is, or create another client.",
    "credential_provisioned": "This credential is managed by CTech and cannot be removed here.",
    "account_unavailable": "CTech Account could not confirm the client right now. Try again in a moment.",
    "webhook_endpoint_limit": "This mode already has the maximum of 5 endpoints. Delete one to add another.",
    "webhook_endpoint_provisioned": "This endpoint is managed by CTech and cannot be changed here.",
    "webhook_endpoint_disabled": "This endpoint was disabled after repeated failures. Delete it and add it again to receive events.",
    "tenant_suspended": "This organization's live billing is suspended. You can remove integrations, not add them.",
```

(If part 2 already added `tenant_suspended`, keep its text and skip that line.)

Mock runtime — `consoleMockData.ts`:

```ts
export const MOCK_MACHINE_SCOPES = [
  "billing:customers:read", "billing:customers:write", "billing:entitlements:read", "billing:invoices:read",
  "billing:invoices:write", "billing:organization:read", "billing:products:read", "billing:products:write",
  "billing:subscriptions:read", "billing:subscriptions:write", "billing:usage:write",
]
export const MOCK_EVENT_TYPES = [
  "checkout.session.canceled", "checkout.session.completed", "checkout.session.created", "checkout.session.expired",
  "credit_note.created", "invoice.created", "invoice.finalized", "invoice.paid", "invoice.payment_failed",
  "invoice.uncollectible", "invoice.voided", "payment.abandoned", "payment.attempted", "payment.failed",
  "payment.succeeded", "subscription.activated", "subscription.canceled", "subscription.created",
  "subscription.past_due", "subscription.paused", "subscription.recovered", "subscription.renewed",
  "subscription.resumed", "subscription.updated",
]
/** Mutable per page load: the mock is a demo, not a store. */
export const mockCredentials: ConsoleCredential[] = [
  {client_id: "dfe-billing", livemode: true, active: true, provisioned: true, connected_by_me: false, description: "CTech DF-e"},
]
export const mockWebhooks: ConsoleWebhookEndpoint[] = [
  {id: "whe_dfe", url: "https://dfe.internal.aoctech.app/v1/internal/webhooks/billing", events: [], status: "active", failure_run: 0, provisioned: true, owner_key: "dfe"},
]
```

`mockRuntime.ts`, in the console block after the merchant check part 1 added:

```ts
    if (url.endsWith("/v1.0/console/credentials") && method === "get") {
      return ok(config, {data: mockCredentials, machine_scopes: MOCK_MACHINE_SCOPES})
    }
    if (url.endsWith("/v1.0/console/credentials") && method === "post") {
      const clientId = String(JSON.parse(String(config.data ?? "{}")).client_id ?? "")
      if (!clientId.startsWith("cli_")) fail(config, 422, "Unprocessable Entity", "the client cannot be connected", "about:blank", "client_not_yours")
      const cred = {client_id: clientId, livemode: mode === "live", active: true, provisioned: false, connected_by: "usr_mock", connected_by_me: true, connected_at: new Date().toISOString()}
      mockCredentials.push(cred)
      return ok(config, cred, 201)
    }
    if (url.includes("/v1.0/console/credentials/") && method === "delete") {
      const clientId = decodeURIComponent(url.split("/v1.0/console/credentials/")[1])
      const i = mockCredentials.findIndex(c => c.client_id === clientId)
      if (i >= 0 && mockCredentials[i].provisioned) fail(config, 409, "Conflict", "managed by CTech", "about:blank", "credential_provisioned")
      if (i >= 0) mockCredentials.splice(i, 1)
      return ok(config, undefined, 204)
    }
    if (url.endsWith("/v1.0/console/webhooks") && method === "get") {
      return ok(config, {data: mockWebhooks, event_types: MOCK_EVENT_TYPES, max_endpoints: 5})
    }
    if (url.endsWith("/v1.0/console/webhooks") && method === "post") {
      if (mockWebhooks.length >= 5) fail(config, 422, "Unprocessable Entity", "limit", "about:blank", "webhook_endpoint_limit")
      const body = JSON.parse(String(config.data ?? "{}")) as {url: string; events: string[]}
      const created = {id: `whe_mock${mockWebhooks.length}`, url: body.url, events: body.events, status: "active" as const, failure_run: 0, provisioned: false, added_at: new Date().toISOString()}
      mockWebhooks.push(created)
      return ok(config, {...created, secret: `whsec_${"ab".repeat(32)}`}, 201)
    }
    const hook = url.match(/\/v1\.0\/console\/webhooks\/([^/?]+)(\/rotate-secret|\/test)?$/)
    if (hook) {
      const e = mockWebhooks.find(w => w.id === decodeURIComponent(hook[1]))
      if (!e) fail(config, 404, "Not Found", "resource not found", "about:blank", "resource_not_found")
      if (e!.provisioned) fail(config, 409, "Conflict", "managed by CTech", "about:blank", "webhook_endpoint_provisioned")
      if (hook[2] === "/rotate-secret") return ok(config, {...e!, secret: `whsec_${"cd".repeat(32)}`})
      if (hook[2] === "/test") return ok(config, {event_id: "evt_mock"}, 202)
      if (method === "delete") {
        mockWebhooks.splice(mockWebhooks.indexOf(e!), 1)
        return ok(config, undefined, 204)
      }
    }
```

(`method`, `mode`, `ok`, `fail` are the block's existing names; `fail` throws. Adjust the variable names to the file if part 1 renamed them.)

- [ ] **Step 4: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/api/console.test.ts src/lib/console/developerLink.test.ts src/lib/errors.test.ts; echo "vitest exit=$?"` then `npx next typegen && npx tsc --noEmit && npm run lint`
Expected: `vitest exit=0`; tsc and lint clean. Check every new string: `grep -n "—" src/locales/*/errors.json` prints nothing new.

- [ ] **Step 5: Commit**

```bash
git add ui/src
git commit -m "feat(console-ui): credentials and webhooks API layer, developer link, error copy"
```

---

### Task 12: UI — `/console/credentials`

> **Execute with the `/impeccable` skill** (`shape`, then `craft`), giving it `ui/PRODUCT.md`, `ui/DESIGN.md` ("Copy and controls", "On a phone", "Navigation and the shell", "Confirmations that say what they do"), spec § 5.1, § 7 item 4, and this task. Use `@aoctech/ui` (`Button`, `Field`, `Input`, `Modal`, `EmptyState`, `Skeleton`, `Badge`) and `ErrorBlock`; no new dependency. **The phone has its own layout**, described below; check 320, 375, 768 and 1280 px.

**Files:**
- Create: `ui/src/app/(console)/console/credentials/layout.tsx`, `ui/src/app/(console)/console/credentials/page.tsx`, `ui/src/app/(console)/console/credentials/credentials.test.tsx`
- Modify: `ui/src/lib/console/nav.ts`, `ui/src/lib/console/nav.test.ts`
- Modify: `ui/src/locales/pt-BR/console.json`, `ui/src/locales/en/console.json`

**Interfaces:**
- Consumes: Task 11 (`listConsoleCredentials`, `connectCredential`, `disconnectCredential`, `consoleKeys.credentials`, `developerClientURL`, `useAddsRefused`, `instantDate`), part 1 (`useConsoleScope`, `ConsoleScopeProvider`), `messageFor`.
- Produces: page `/console/credentials`; nav entries `console.nav.credentials`, `console.nav.webhooks` (the webhooks page lands in Task 13; the entry is added here with it so the nav changes once).

**Layout.** Laptop (from `sm`): title "Credenciais" and one sentence under it. A `surface` box "Conectar um cliente" with two numbered steps side by side: (1) "Crie um cliente na CTech Account" with the outline button **Criar cliente na CTech Account** (opens a new tab, `rel="noopener"`), and a `<details>` "Escopos que o cliente deve ter" listing `machine_scopes` in mono; (2) "Cole o ID do cliente" with the field and **Conectar** on one line. Below, the list: one row per credential, client id in mono (wraps, `break-all`), a meta line ("Conectado por você em 10/10/2026" / "Conectado por outra pessoa da organização em …" / "Gerenciado pela CTech"), a `Badge tone="attention"` **Desativado** when `active` is false, and **Desconectar** (ghost, danger text) at the row's end, absent on provisioned rows. Phone (under `sm`): the steps stack; the button and the field are full width, **Conectar** under the field; each row is the id, then the meta, then **Desconectar** on its own line, left-aligned, 44px target. Empty list: `EmptyState` "Nenhuma credencial conectada neste modo." In a suspended live tenant the connect box is replaced by one muted line (the `tenant_suspended` copy) and disconnect stays.

**Confirmation** (DESIGN.md "Confirmations that say what they do"): **Desconectar** opens a danger `Modal` titled "Desconectar este cliente?" whose body names the id and the consequence: "As chamadas de `<id>` ao CTech Billing param na hora. O cliente continua existindo na CTech Account." Buttons **Desconectar** / **Voltar**.

- [ ] **Step 1: Copy** — `console.json`. In `nav`, between `catalog` and `settings`: pt-BR `"credentials": "Credenciais", "webhooks": "Webhooks"`; en `"credentials": "Credentials", "webhooks": "Webhooks"`. Top level, pt-BR:

```json
 "credentials": {
  "title": "Credenciais",
  "intro": "Sua integração chama a API do CTech Billing com um cliente OAuth seu, criado na CTech Account. O segredo do cliente fica só com você.",
  "connectTitle": "Conectar um cliente",
  "step1": "Crie um cliente confidencial na CTech Account só com os escopos de integração.",
  "createClient": "Criar cliente na CTech Account",
  "scopesSummary": "Escopos que o cliente deve ter",
  "step2": "Cole aqui o ID do cliente.",
  "clientId": "ID do cliente",
  "connect": "Conectar",
  "connected": "Cliente conectado.",
  "listTitle": "Clientes conectados",
  "empty": "Nenhuma credencial conectada neste modo.",
  "byMe": "Conectado por você em {{date}}",
  "byOther": "Conectado por outra pessoa da organização em {{date}}",
  "provisioned": "Gerenciado pela CTech",
  "inactive": "Desativado",
  "disconnect": "Desconectar",
  "disconnectTitle": "Desconectar este cliente?",
  "disconnectBody": "As chamadas de {{id}} ao CTech Billing param na hora. O cliente continua existindo na CTech Account.",
  "back": "Voltar",
  "disconnected": "Cliente desconectado."
 },
```

en:

```json
 "credentials": {
  "title": "Credentials",
  "intro": "Your integration calls the CTech Billing API with your own OAuth client, created in CTech Account. Its secret stays with you.",
  "connectTitle": "Connect a client",
  "step1": "Create a confidential client in CTech Account with only the integration scopes.",
  "createClient": "Create a client in CTech Account",
  "scopesSummary": "Scopes the client must have",
  "step2": "Paste the client ID here.",
  "clientId": "Client ID",
  "connect": "Connect",
  "connected": "Client connected.",
  "listTitle": "Connected clients",
  "empty": "No credential connected in this mode.",
  "byMe": "Connected by you on {{date}}",
  "byOther": "Connected by someone else in the organization on {{date}}",
  "provisioned": "Managed by CTech",
  "inactive": "Disabled",
  "disconnect": "Disconnect",
  "disconnectTitle": "Disconnect this client?",
  "disconnectBody": "Calls from {{id}} to CTech Billing stop at once. The client still exists in CTech Account.",
  "back": "Back",
  "disconnected": "Client disconnected."
 },
```

- [ ] **Step 2: Write the failing tests** — `credentials.test.tsx`

```tsx
import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type {ReactNode} from "react"
import {afterEach, describe, expect, it, vi} from "vitest"

import ConsoleCredentialsPage from "@/app/(console)/console/credentials/page"
import * as consoleApi from "@/lib/api/console"
import type {MerchantStatus} from "@/lib/api/consoleTypes"
import {ConsoleScopeProvider} from "@/lib/console/scope"
import ptBR from "@/locales/pt-BR"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const

function session(status: MerchantStatus) {
  vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({
    organizations: [{organization_id: SCOPE.org, selector: `org:${SCOPE.org}`, display_name: "CTech", role: "owner", merchant_status: status}],
    organizations_unavailable: false,
  })
}

function list() {
  vi.spyOn(consoleApi, "listConsoleCredentials").mockResolvedValue({
    data: [
      {client_id: "dfe-billing", livemode: true, active: true, provisioned: true, connected_by_me: false},
      {client_id: "cli_mine", livemode: true, active: true, provisioned: false, connected_by: "usr_1", connected_by_me: true, connected_at: "2026-10-10T12:00:00Z"},
    ],
    machine_scopes: ["billing:invoices:read", "billing:usage:write"],
  })
}

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>)
}

afterEach(() => vi.restoreAllMocks())

describe("/console/credentials", () => {
  it("offers disconnect only on a connected client and says who connected it", async () => {
    session("live")
    list()
    wrap(<ConsoleCredentialsPage/>)
    const mine = (await screen.findByText("cli_mine")).closest("li")!
    expect(within(mine).getByText(/Conectado por você em 10\/10\/2026/)).toBeInTheDocument()
    expect(within(mine).getByRole("button", {name: "Desconectar"})).toBeInTheDocument()
    const seeded = screen.getByText("dfe-billing").closest("li")!
    expect(within(seeded).getByText("Gerenciado pela CTech")).toBeInTheDocument()
    expect(within(seeded).queryByRole("button")).toBeNull()
  })

  it("links to the developer page with the API's machine scopes", async () => {
    session("live")
    list()
    wrap(<ConsoleCredentialsPage/>)
    const link = await screen.findByRole("link", {name: "Criar cliente na CTech Account"})
    const href = new URL(link.getAttribute("href")!)
    expect(href.searchParams.get("scopes")).toBe("billing:invoices:read,billing:usage:write")
    expect(link).toHaveAttribute("target", "_blank")
  })

  it("connects the trimmed id and shows a refusal under the field", async () => {
    session("live")
    list()
    const connect = vi.spyOn(consoleApi, "connectCredential").mockRejectedValue(
      {response: {status: 422, data: {type: "about:blank", status: 422, title: "T", code: "client_not_yours"}}})
    wrap(<ConsoleCredentialsPage/>)
    await userEvent.type(await screen.findByLabelText("ID do cliente"), "  cli_x  ")
    await userEvent.click(screen.getByRole("button", {name: "Conectar"}))
    expect(connect).toHaveBeenCalledWith(SCOPE, "cli_x")
    expect(await screen.findByText(ptBR.errors.code.client_not_yours)).toBeInTheDocument()
  })

  it("asks before disconnecting and names the client", async () => {
    session("live")
    list()
    const disconnect = vi.spyOn(consoleApi, "disconnectCredential").mockResolvedValue()
    wrap(<ConsoleCredentialsPage/>)
    const mine = (await screen.findByText("cli_mine")).closest("li")!
    await userEvent.click(within(mine).getByRole("button", {name: "Desconectar"}))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText(/cli_mine/)).toBeInTheDocument()
    expect(disconnect).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole("button", {name: "Desconectar"}))
    expect(disconnect).toHaveBeenCalledWith(SCOPE, "cli_mine")
  })

  it("offers no connect while live is suspended", async () => {
    session("suspended")
    list()
    wrap(<ConsoleCredentialsPage/>)
    await screen.findByText("cli_mine")
    expect(screen.queryByRole("button", {name: "Conectar"})).toBeNull()
    expect(screen.getByText(ptBR.errors.code.tenant_suspended)).toBeInTheDocument()
  })
})
```

Append to `nav.test.ts`:

```ts
  it("lists the integration tools before the settings", () => {
    const labels = consoleNav(true).map(i => t(i.label))
    expect(labels.slice(-3)).toEqual(["Credenciais", "Webhooks", "Configurações"])
  })
```

(If `@testing-library/user-event` is not in `devDependencies`, use `fireEvent` from `@testing-library/react` instead; do not add a dependency.)

- [ ] **Step 3: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 "src/app/(console)/console/credentials/credentials.test.tsx" src/lib/console/nav.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=1` (no page; nav lacks the entries).

- [ ] **Step 4: Implement**

`nav.ts`, `INVOICING_NAV`, between catalog and settings:

```ts
  {href: "/console/credentials", label: "console.nav.credentials"},
  {href: "/console/webhooks", label: "console.nav.webhooks"},
```

`credentials/layout.tsx`:

```tsx
import type {Metadata} from "next"

export const metadata: Metadata = {title: "Credenciais · Console"}

export default function Layout({children}: LayoutProps<"/console/credentials">) {
  return children
}
```

`credentials/page.tsx` (the structure and behaviour are fixed by the tests and the layout above; `/impeccable` owns spacing, type and the phone pass):

```tsx
"use client"

import limits from "@/lib/limits.json"
import {Badge, Button, EmptyState, Field, Input, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {KeyRound} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor} from "@/lib/api/client"
import {connectCredential, consoleKeys, disconnectCredential, listConsoleCredentials} from "@/lib/api/console"
import type {ConsoleCredential} from "@/lib/api/consoleTypes"
import {useAddsRefused} from "@/lib/console/addsRefused"
import {developerClientURL} from "@/lib/console/developerLink"
import {useConsoleScope} from "@/lib/console/scope"
import {instantDate} from "@/lib/format"

/**
 * The merchant's API credentials (spec § 5.1). A credential is the merchant's
 * own ctech-account OAuth client, connected by id: billing never sees its
 * secret. Provisioned ones (tenant zero's CTech services) are listed, never
 * removable.
 */
export default function ConsoleCredentialsPage() {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const addsRefused = useAddsRefused()
  const query = useQuery({queryKey: consoleKeys.credentials(scope), queryFn: () => listConsoleCredentials(scope)})
  const [leaving, setLeaving] = useState<ConsoleCredential | null>(null)
  const creds = query.data?.data ?? []

  return (
    <div className="space-y-8">
      <header className="space-y-1">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("console.credentials.title")}</h1>
        <p className="max-w-prose text-sm text-muted-foreground">{t("console.credentials.intro")}</p>
      </header>

      {addsRefused
        ? <p className="text-sm text-muted-foreground">{t("errors.code.tenant_suspended")}</p>
        : <ConnectBox scopes={query.data?.machine_scopes ?? []}/>}

      <section aria-labelledby="creds-list" className="space-y-3">
        <h2 id="creds-list" className="text-sm font-medium text-foreground">{t("console.credentials.listTitle")}</h2>
        {query.isPending && <div className="space-y-2" aria-busy>{[0, 1].map(i => <Skeleton key={i} className="h-12 w-full"/>)}</div>}
        {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}
        {query.isSuccess && creds.length === 0 && <EmptyState icon={<KeyRound/>} title={t("console.credentials.empty")}/>}
        {creds.length > 0 && (
          <ul className="divide-y divide-border border-y border-border">
            {creds.map(c => (
              <li key={c.client_id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4">
                <div className="min-w-0 flex-1 space-y-0.5">
                  <p className="font-mono text-sm break-all text-foreground">{c.client_id}</p>
                  <p className="text-xs text-muted-foreground">
                    {c.provisioned
                      ? t("console.credentials.provisioned")
                      : t(c.connected_by_me ? "console.credentials.byMe" : "console.credentials.byOther",
                        {date: c.connected_at ? instantDate(c.connected_at) : "—"})}
                    {!c.active && <Badge tone="attention" className="ml-2">{t("console.credentials.inactive")}</Badge>}
                  </p>
                </div>
                {!c.provisioned && (
                  <Button variant="ghost" className="self-start text-danger sm:self-auto" onClick={() => setLeaving(c)}>
                    {t("console.credentials.disconnect")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <DisconnectDialog credential={leaving} onClose={() => setLeaving(null)}/>
    </div>
  )
}

function ConnectBox({scopes}: {scopes: string[]}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const [clientId, setClientId] = useState("")
  const connect = useMutation({
    mutationFn: () => connectCredential(scope, clientId.trim()),
    onSuccess: () => {
      toast.success(t("console.credentials.connected"))
      setClientId("")
      void queryClient.invalidateQueries({queryKey: consoleKeys.credentials(scope)})
    },
  })

  return (
    <section aria-labelledby="connect-title" className="space-y-4 rounded-lg bg-surface p-4">
      <h2 id="connect-title" className="text-sm font-medium text-foreground">{t("console.credentials.connectTitle")}</h2>
      <ol className="grid gap-4 sm:grid-cols-2">
        <li className="space-y-2">
          <p className="text-sm">1. {t("console.credentials.step1")}</p>
          <a href={developerClientURL(scopes)} target="_blank" rel="noopener noreferrer"
             className="inline-flex min-h-11 items-center rounded-md border border-border px-3 text-sm sm:min-h-8">
            {t("console.credentials.createClient")}
          </a>
          <details className="text-xs text-muted-foreground">
            <summary className="cursor-pointer">{t("console.credentials.scopesSummary")}</summary>
            <ul className="mt-2 space-y-0.5 font-mono">{scopes.map(s => <li key={s}>{s}</li>)}</ul>
          </details>
        </li>
        <li className="space-y-2">
          <p className="text-sm">2. {t("console.credentials.step2")}</p>
          <form className="flex flex-col gap-2 sm:flex-row sm:items-start"
                onSubmit={e => { e.preventDefault(); if (clientId.trim()) connect.mutate() }}>
            <Field label={t("console.credentials.clientId")} htmlFor="client-id" className="flex-1"
                   error={connect.isError ? messageFor(connect.error) : undefined}>
              <Input id="client-id" value={clientId} maxLength={limits.text.clientID} autoComplete="off" spellCheck={false}
                     className="font-mono" onChange={e => { setClientId(e.target.value); connect.reset() }}/>
            </Field>
            <Button type="submit" className="w-full sm:mt-6 sm:w-auto" disabled={!clientId.trim()} loading={connect.isPending}>
              {t("console.credentials.connect")}
            </Button>
          </form>
        </li>
      </ol>
    </section>
  )
}

function DisconnectDialog({credential, onClose}: {credential: ConsoleCredential | null; onClose: () => void}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const disconnect = useMutation({
    mutationFn: (id: string) => disconnectCredential(scope, id),
    onSuccess: () => {
      toast.success(t("console.credentials.disconnected"))
      void queryClient.invalidateQueries({queryKey: consoleKeys.credentials(scope)})
      onClose()
    },
    onError: e => toast.error(messageFor(e)),
  })
  return (
    <Modal open={credential !== null} onClose={onClose} danger
           title={t("console.credentials.disconnectTitle")}
           submitLabel={t("console.credentials.disconnect")} cancelLabel={t("console.credentials.back")}
           loading={disconnect.isPending}
           onSubmit={() => credential && disconnect.mutate(credential.client_id)}>
      <p className="text-sm">{t("console.credentials.disconnectBody", {id: credential?.client_id ?? ""})}</p>
    </Modal>
  )
}
```

Check `Field`'s props in `node_modules/@aoctech/ui/dist/components/field.d.ts` (`className` may not exist; wrap in a `div className="flex-1"` instead) and `Button`'s (`loading` may not exist; use `disabled={… || connect.isPending}`). Fix to the real props; the tests do not depend on either.

- [ ] **Step 5: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 "src/app/(console)/console/credentials/credentials.test.tsx" src/lib/console/nav.test.ts; echo "vitest exit=$?"` then `npx next typegen && npx tsc --noEmit && npm run lint && npm run build`
Expected: `vitest exit=0`; all clean. Then `NEXT_PUBLIC_MOCK_API=true npm run dev` and check `/console/credentials` at 320, 375, 768, 1280 px (connect a `cli_demo`, disconnect it, try `other` for the refusal). `grep -n "—" src/locales/*/console.json` shows no new em dash.

- [ ] **Step 6: Commit**

```bash
git add ui/src
git commit -m "feat(console-ui): /console/credentials, connect a client created in CTech Account"
```

---

### Task 13: UI — `/console/webhooks`

> **Execute with the `/impeccable` skill** (`shape`, then `craft`), with `ui/PRODUCT.md`, `ui/DESIGN.md` ("Copy and controls", "On a phone" — the swipe and "⋯" rule —, "Confirmations that say what they do"), spec § 5.2 and this task. `@aoctech/ui`: `Button`, `Field`, `Input`, `Modal`, `Switch`, `Checkbox`, `Badge`, `RowMenu`, `SwipeRow`, `EmptyState`, `Skeleton`. **Own phone layout**; check 320, 375, 768, 1280 px. Then add a DESIGN.md section.

**Files:**
- Create: `ui/src/app/(console)/console/webhooks/layout.tsx`, `ui/src/app/(console)/console/webhooks/page.tsx`, `ui/src/app/(console)/console/webhooks/webhooks.test.tsx`, `ui/src/components/console/SecretReveal.tsx`
- Modify: `ui/src/locales/pt-BR/console.json`, `ui/src/locales/en/console.json`, `ui/DESIGN.md`

**Interfaces:**
- Consumes: Task 11 (`listConsoleWebhooks`, `createWebhook`, `deleteWebhook`, `rotateWebhookSecret`, `testWebhook`, `consoleKeys.webhooks`, `useAddsRefused`, `instantDate`), part 1 (`useConsoleScope`).
- Produces: page `/console/webhooks`; `SecretReveal({secret, open, onDone}: {secret: string | null; open: boolean; onDone: () => void})`.

**Layout.** Laptop: title "Webhooks", one sentence, and **Novo endpoint** (brand) at the title's right; under it "3 de 5 endpoints neste modo". Each endpoint is a row: URL (mono, wraps), meta ("Todos os eventos" or "3 eventos", the date it was added or "Gerenciado pela CTech"), a status `Badge` (**Ativo** neutral, **Desativado** urgent, with "Desativado depois de falhas seguidas. Exclua e cadastre de novo." under it), and inline **Testar**, **Trocar segredo**, **Excluir** (provisioned rows: no actions). Phone: rows stack (URL, meta, badge); the primary **Testar** stays on the row; **Trocar segredo** and **Excluir** are a left swipe (`SwipeRow`, Excluir in `danger`) **and** a visible "⋯" (`RowMenu`, label "Mais ações: <url>") with the same two items, per DESIGN.md. **Novo endpoint** is full width under the title. At the limit the button is disabled and the counter line says "Limite de 5 endpoints neste modo. Exclua um para adicionar outro." Suspended live: no **Novo endpoint**, no **Testar**; the `tenant_suspended` line under the title.

**New endpoint** (`Modal`, submit **Adicionar**): URL field (`https://`, placeholder `https://sua-api.com.br/webhooks/billing`, hint "Só https, em um endereço público."), a `Switch` **Todos os eventos** (on by default), and when off a checkbox list of `event_types` grouped by the part before the first dot, with group headings in Portuguese (Faturas, Assinaturas, Pagamentos, Checkout, Notas de crédito) and the event code in mono beside each label. Field errors from the 422 land on the URL field (`useFieldErrors(["url", "events"])`).

**The secret, once** (`SecretReveal`, a `Modal` with no cancel): title "Copie o segredo agora", body "Ele assina cada entrega (cabeçalho X-Billing-Signature) e não aparece de novo. Se perder, troque o segredo.", the secret in a read-only mono `Input` with **Copiar** beside it (clipboard; on refusal a toast "Não foi possível copiar. Selecione e copie à mão."), and the only way out **Já copiei**. Closing drops the secret from state.

**Rotate confirmation** (danger `Modal`): "Trocar o segredo de <url>?" / "O segredo atual para de valer na hora. Entregas que falharem enquanto você atualiza a sua API são tentadas de novo, já com o segredo novo." / **Trocar segredo** · **Voltar**; on success → `SecretReveal`. **Delete confirmation**: "Excluir <url>?" / "Nenhum evento é enviado para ele depois disso, nem os que estão na fila." / **Excluir** · **Voltar**. **Testar** sends at once (it changes nothing) and toasts "Ping enviado. Ele chega em até 2 minutos."

- [ ] **Step 1: Copy** — `console.json` top level, pt-BR:

```json
 "webhooks": {
  "title": "Webhooks",
  "intro": "O CTech Billing avisa a sua API quando algo muda (uma fatura paga, uma assinatura cancelada). Cada entrega é assinada com o segredo do endpoint.",
  "new": "Novo endpoint",
  "count": "{{count}} de {{max}} endpoints neste modo",
  "atLimit": "Limite de {{max}} endpoints neste modo. Exclua um para adicionar outro.",
  "empty": "Nenhum endpoint neste modo.",
  "allEvents": "Todos os eventos",
  "someEvents_one": "{{count}} evento",
  "someEvents_other": "{{count}} eventos",
  "addedOn": "Adicionado em {{date}}",
  "provisioned": "Gerenciado pela CTech",
  "active": "Ativo",
  "disabled": "Desativado",
  "disabledHint": "Desativado depois de falhas seguidas. Exclua e cadastre de novo.",
  "test": "Testar",
  "tested": "Ping enviado. Ele chega em até 2 minutos.",
  "rotate": "Trocar segredo",
  "delete": "Excluir",
  "more": "Mais ações: {{url}}",
  "back": "Voltar",
  "form": {
   "title": "Novo endpoint",
   "url": "URL",
   "urlHint": "Só https, em um endereço público.",
   "urlPlaceholder": "https://sua-api.com.br/webhooks/billing",
   "all": "Todos os eventos",
   "submit": "Adicionar"
  },
  "groups": {"invoice": "Faturas", "subscription": "Assinaturas", "payment": "Pagamentos", "checkout": "Checkout", "credit_note": "Notas de crédito"},
  "secret": {
   "title": "Copie o segredo agora",
   "body": "Ele assina cada entrega (cabeçalho X-Billing-Signature) e não aparece de novo. Se perder, troque o segredo.",
   "label": "Segredo de assinatura",
   "copy": "Copiar",
   "copied": "Segredo copiado.",
   "copyFailed": "Não foi possível copiar. Selecione e copie à mão.",
   "done": "Já copiei"
  },
  "rotateTitle": "Trocar o segredo de {{url}}?",
  "rotateBody": "O segredo atual para de valer na hora. Entregas que falharem enquanto você atualiza a sua API são tentadas de novo, já com o segredo novo.",
  "deleteTitle": "Excluir {{url}}?",
  "deleteBody": "Nenhum evento é enviado para ele depois disso, nem os que estão na fila.",
  "deleted": "Endpoint excluído."
 },
```

en (same keys):

```json
 "webhooks": {
  "title": "Webhooks",
  "intro": "CTech Billing tells your API when something changes (an invoice paid, a subscription canceled). Every delivery is signed with the endpoint's secret.",
  "new": "New endpoint",
  "count": "{{count}} of {{max}} endpoints in this mode",
  "atLimit": "Limit of {{max}} endpoints in this mode. Delete one to add another.",
  "empty": "No endpoint in this mode.",
  "allEvents": "All events",
  "someEvents_one": "{{count}} event",
  "someEvents_other": "{{count}} events",
  "addedOn": "Added on {{date}}",
  "provisioned": "Managed by CTech",
  "active": "Active",
  "disabled": "Disabled",
  "disabledHint": "Disabled after repeated failures. Delete it and add it again.",
  "test": "Test",
  "tested": "Ping sent. It arrives within 2 minutes.",
  "rotate": "Rotate secret",
  "delete": "Delete",
  "more": "More actions: {{url}}",
  "back": "Back",
  "form": {
   "title": "New endpoint",
   "url": "URL",
   "urlHint": "https only, on a public address.",
   "urlPlaceholder": "https://your-api.com/webhooks/billing",
   "all": "All events",
   "submit": "Add"
  },
  "groups": {"invoice": "Invoices", "subscription": "Subscriptions", "payment": "Payments", "checkout": "Checkout", "credit_note": "Credit notes"},
  "secret": {
   "title": "Copy the secret now",
   "body": "It signs every delivery (X-Billing-Signature header) and is not shown again. If you lose it, rotate it.",
   "label": "Signing secret",
   "copy": "Copy",
   "copied": "Secret copied.",
   "copyFailed": "Could not copy. Select it and copy by hand.",
   "done": "I copied it"
  },
  "rotateTitle": "Rotate the secret of {{url}}?",
  "rotateBody": "The current secret stops working at once. Deliveries that fail while you update your API are retried, signed with the new secret.",
  "deleteTitle": "Delete {{url}}?",
  "deleteBody": "No event is sent to it after this, including the ones already queued.",
  "deleted": "Endpoint deleted."
 },
```

- [ ] **Step 2: Write the failing tests** — `webhooks.test.tsx`

```tsx
import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type {ReactNode} from "react"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import ConsoleWebhooksPage from "@/app/(console)/console/webhooks/page"
import * as consoleApi from "@/lib/api/console"
import type {ConsoleWebhookEndpoint} from "@/lib/api/consoleTypes"
import {ConsoleScopeProvider} from "@/lib/console/scope"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const
const SECRET = `whsec_${"ab".repeat(32)}`

const ep = (id: string, extra: Partial<ConsoleWebhookEndpoint> = {}): ConsoleWebhookEndpoint =>
  ({id, url: `https://example.com/${id}`, events: [], status: "active", failure_run: 0, provisioned: false, added_at: "2026-10-10T12:00:00Z", ...extra})

function list(data: ConsoleWebhookEndpoint[]) {
  vi.spyOn(consoleApi, "listConsoleWebhooks").mockResolvedValue({data, event_types: ["invoice.paid", "subscription.canceled"], max_endpoints: 5})
}

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}>{ui}</ConsoleScopeProvider></QueryClientProvider>)
}

beforeEach(() => {
  vi.spyOn(consoleApi, "getConsoleSession").mockResolvedValue({
    organizations: [{organization_id: SCOPE.org, selector: `org:${SCOPE.org}`, display_name: "CTech", role: "owner", merchant_status: "live"}],
    organizations_unavailable: false,
  })
})
afterEach(() => vi.restoreAllMocks())

describe("/console/webhooks", () => {
  it("shows the new secret once, and forgets it when closed", async () => {
    list([])
    const create = vi.spyOn(consoleApi, "createWebhook").mockResolvedValue({...ep("whe_1"), secret: SECRET})
    wrap(<ConsoleWebhooksPage/>)
    await userEvent.click(await screen.findByRole("button", {name: "Novo endpoint"}))
    const form = await screen.findByRole("dialog")
    await userEvent.type(within(form).getByLabelText("URL"), "https://example.com/hooks")
    await userEvent.click(within(form).getByRole("button", {name: "Adicionar"}))
    expect(create).toHaveBeenCalledWith(SCOPE, {url: "https://example.com/hooks", events: []})
    expect(await screen.findByDisplayValue(SECRET)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", {name: "Já copiei"}))
    expect(screen.queryByDisplayValue(SECRET)).toBeNull()
  })

  it("sends only the chosen events when 'Todos os eventos' is off", async () => {
    list([])
    const create = vi.spyOn(consoleApi, "createWebhook").mockResolvedValue({...ep("whe_1"), secret: SECRET})
    wrap(<ConsoleWebhooksPage/>)
    await userEvent.click(await screen.findByRole("button", {name: "Novo endpoint"}))
    const form = await screen.findByRole("dialog")
    await userEvent.type(within(form).getByLabelText("URL"), "https://example.com/hooks")
    await userEvent.click(within(form).getByRole("switch", {name: "Todos os eventos"}))
    await userEvent.click(within(form).getByRole("checkbox", {name: /invoice\.paid/}))
    await userEvent.click(within(form).getByRole("button", {name: "Adicionar"}))
    expect(create).toHaveBeenCalledWith(SCOPE, {url: "https://example.com/hooks", events: ["invoice.paid"]})
  })

  it("disables 'Novo endpoint' at the limit and says why", async () => {
    list([ep("a"), ep("b"), ep("c"), ep("d"), ep("e")])
    wrap(<ConsoleWebhooksPage/>)
    expect(await screen.findByRole("button", {name: "Novo endpoint"})).toBeDisabled()
    expect(screen.getByText(/Limite de 5 endpoints/)).toBeInTheDocument()
  })

  it("tests at once, and rotates only after confirming", async () => {
    list([ep("whe_1")])
    const test = vi.spyOn(consoleApi, "testWebhook").mockResolvedValue({event_id: "evt_1"})
    const rotate = vi.spyOn(consoleApi, "rotateWebhookSecret").mockResolvedValue({...ep("whe_1"), secret: SECRET})
    wrap(<ConsoleWebhooksPage/>)
    const row = (await screen.findByText("https://example.com/whe_1")).closest("li")!
    await userEvent.click(within(row).getByRole("button", {name: "Testar"}))
    expect(test).toHaveBeenCalledWith(SCOPE, "whe_1")
    await userEvent.click(within(row).getAllByRole("button", {name: "Trocar segredo"})[0])
    expect(rotate).not.toHaveBeenCalled()
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: "Trocar segredo"}))
    expect(rotate).toHaveBeenCalledWith(SCOPE, "whe_1")
    expect(await screen.findByDisplayValue(SECRET)).toBeInTheDocument()
  })

  it("lists a provisioned endpoint with no action", async () => {
    list([ep("whe_dfe", {provisioned: true, added_at: undefined, owner_key: "dfe"})])
    wrap(<ConsoleWebhooksPage/>)
    const row = (await screen.findByText("https://example.com/whe_dfe")).closest("li")!
    expect(within(row).getByText("Gerenciado pela CTech")).toBeInTheDocument()
    expect(within(row).queryByRole("button")).toBeNull()
  })
})
```

- [ ] **Step 3: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 "src/app/(console)/console/webhooks/webhooks.test.tsx"; echo "vitest exit=$?"`
Expected: `vitest exit=1` (no page).

- [ ] **Step 4: Implement**

`webhooks/layout.tsx` as the credentials one with `title: "Webhooks · Console"` and `LayoutProps<"/console/webhooks">`.

`ui/src/components/console/SecretReveal.tsx`:

```tsx
"use client"

import {Button, Input, Modal} from "@aoctech/ui"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

/**
 * The webhook signing secret, shown exactly once (spec § 5.2). The parent
 * holds it in state only while this is open and drops it on `onDone`; nothing
 * caches it, and no list ever returns it again.
 */
export function SecretReveal({secret, open, onDone}: {secret: string | null; open: boolean; onDone: () => void}) {
  const {t} = useTranslation()
  async function copy() {
    if (!secret) return
    try {
      await navigator.clipboard.writeText(secret)
      toast.success(t("console.webhooks.secret.copied"))
    } catch {
      toast.error(t("console.webhooks.secret.copyFailed"))
    }
  }
  return (
    <Modal open={open && secret !== null} onClose={onDone} title={t("console.webhooks.secret.title")}
           description={t("console.webhooks.secret.body")}
           submitLabel={t("console.webhooks.secret.done")} onSubmit={onDone}>
      <div className="flex flex-col gap-2 sm:flex-row">
        <Input readOnly aria-label={t("console.webhooks.secret.label")} value={secret ?? ""}
               className="min-w-0 flex-1 font-mono text-xs" onFocus={e => e.currentTarget.select()}/>
        <Button variant="outline" onClick={copy}>{t("console.webhooks.secret.copy")}</Button>
      </div>
    </Modal>
  )
}
```

(If `Modal` always renders a cancel button, pass `cancelLabel={t("console.webhooks.secret.done")}` too, so both ways out say "Já copiei"; both call `onDone`.)

`webhooks/page.tsx` — the page follows the credentials page's structure (header, list of `<li>` rows, dialogs at the end) with these parts:

```tsx
"use client"

import limits from "@/lib/limits.json"
import {Badge, Button, Checkbox, EmptyState, Field, Input, Modal, RowMenu, Skeleton, Switch, SwipeRow} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {Webhook} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {SecretReveal} from "@/components/console/SecretReveal"
import {messageFor} from "@/lib/api/client"
import {consoleKeys, createWebhook, deleteWebhook, listConsoleWebhooks, rotateWebhookSecret, testWebhook} from "@/lib/api/console"
import type {ConsoleWebhookEndpoint} from "@/lib/api/consoleTypes"
import {useAddsRefused} from "@/lib/console/addsRefused"
import {useConsoleScope} from "@/lib/console/scope"
import {instantDate} from "@/lib/format"
import {useFieldErrors} from "@/lib/useFieldErrors"

type Pending = {kind: "rotate" | "delete"; endpoint: ConsoleWebhookEndpoint} | null

export default function ConsoleWebhooksPage() {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const addsRefused = useAddsRefused()
  const query = useQuery({queryKey: consoleKeys.webhooks(scope), queryFn: () => listConsoleWebhooks(scope)})
  const [creating, setCreating] = useState(false)
  const [pending, setPending] = useState<Pending>(null)
  const [secret, setSecret] = useState<string | null>(null)
  const refresh = () => queryClient.invalidateQueries({queryKey: consoleKeys.webhooks(scope)})

  const endpoints = query.data?.data ?? []
  const max = query.data?.max_endpoints ?? limits.maxWebhookEndpoints
  const atLimit = endpoints.length >= max

  const test = useMutation({
    mutationFn: (id: string) => testWebhook(scope, id),
    onSuccess: () => toast.success(t("console.webhooks.tested")),
    onError: e => toast.error(messageFor(e)),
  })
  const act = useMutation({
    mutationFn: async (p: NonNullable<Pending>) =>
      p.kind === "rotate" ? (await rotateWebhookSecret(scope, p.endpoint.id)).secret : (await deleteWebhook(scope, p.endpoint.id), null),
    onSuccess: (newSecret, p) => {
      setPending(null)
      void refresh()
      if (p.kind === "rotate") setSecret(newSecret)
      else toast.success(t("console.webhooks.deleted"))
    },
    onError: e => toast.error(messageFor(e)),
  })

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1">
          <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("console.webhooks.title")}</h1>
          <p className="max-w-prose text-sm text-muted-foreground">{t("console.webhooks.intro")}</p>
          {query.isSuccess && (
            <p className="text-xs text-muted-foreground">
              {atLimit ? t("console.webhooks.atLimit", {max}) : t("console.webhooks.count", {count: endpoints.length, max})}
            </p>
          )}
          {addsRefused && <p className="text-sm text-muted-foreground">{t("errors.code.tenant_suspended")}</p>}
        </div>
        {!addsRefused && (
          <Button onClick={() => setCreating(true)} disabled={!query.isSuccess || atLimit} className="w-full sm:w-auto">
            {t("console.webhooks.new")}
          </Button>
        )}
      </header>

      {query.isPending && <div className="space-y-2" aria-busy>{[0, 1].map(i => <Skeleton key={i} className="h-14 w-full"/>)}</div>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}
      {query.isSuccess && endpoints.length === 0 && <EmptyState icon={<Webhook/>} title={t("console.webhooks.empty")}/>}
      {endpoints.length > 0 && (
        <ul className="divide-y divide-border border-y border-border">
          {endpoints.map(e => (
            <EndpointRow key={e.id} endpoint={e} canTest={!addsRefused}
                         onTest={() => test.mutate(e.id)}
                         onRotate={() => setPending({kind: "rotate", endpoint: e})}
                         onDelete={() => setPending({kind: "delete", endpoint: e})}/>
          ))}
        </ul>
      )}

      <NewEndpointDialog open={creating} eventTypes={query.data?.event_types ?? []} onClose={() => setCreating(false)}
                         onCreated={s => { setCreating(false); setSecret(s); void refresh() }}/>
      <Modal open={pending !== null} onClose={() => setPending(null)} danger
             title={pending ? t(pending.kind === "rotate" ? "console.webhooks.rotateTitle" : "console.webhooks.deleteTitle", {url: pending.endpoint.url}) : ""}
             submitLabel={t(pending?.kind === "rotate" ? "console.webhooks.rotate" : "console.webhooks.delete")}
             cancelLabel={t("console.webhooks.back")} loading={act.isPending}
             onSubmit={() => pending && act.mutate(pending)}>
        <p className="text-sm">{t(pending?.kind === "rotate" ? "console.webhooks.rotateBody" : "console.webhooks.deleteBody")}</p>
      </Modal>
      <SecretReveal secret={secret} open={secret !== null} onDone={() => setSecret(null)}/>
    </div>
  )
}

function EndpointRow({endpoint: e, canTest, onTest, onRotate, onDelete}: {
  endpoint: ConsoleWebhookEndpoint; canTest: boolean; onTest: () => void; onRotate: () => void; onDelete: () => void
}) {
  const {t} = useTranslation()
  const testable = canTest && !e.provisioned && e.status === "active"
  const meta = [
    e.events.length === 0 ? t("console.webhooks.allEvents") : t("console.webhooks.someEvents", {count: e.events.length}),
    e.provisioned ? t("console.webhooks.provisioned") : e.added_at ? t("console.webhooks.addedOn", {date: instantDate(e.added_at)}) : null,
  ].filter(Boolean).join(" • ")
  const front = (
    <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 flex-1 space-y-1">
        <p className="font-mono text-sm break-all text-foreground">{e.url}</p>
        <p className="text-xs text-muted-foreground">{meta}</p>
        {e.status === "disabled" && <p className="text-xs text-danger">{t("console.webhooks.disabledHint")}</p>}
      </div>
      <Badge tone={e.status === "active" ? "neutral" : "urgent"} className="self-start sm:self-auto">
        {t(e.status === "active" ? "console.webhooks.active" : "console.webhooks.disabled")}
      </Badge>
      {!e.provisioned && (
        <div className="flex items-center gap-2">
          {testable && <Button variant="outline" onClick={onTest}>{t("console.webhooks.test")}</Button>}
          <Button variant="ghost" className="max-sm:hidden" onClick={onRotate}>{t("console.webhooks.rotate")}</Button>
          <Button variant="ghost" className="text-danger max-sm:hidden" onClick={onDelete}>{t("console.webhooks.delete")}</Button>
          <RowMenu className="sm:hidden" label={t("console.webhooks.more", {url: e.url})} items={[
            {key: "rotate", label: t("console.webhooks.rotate"), onSelect: onRotate},
            {key: "delete", label: t("console.webhooks.delete"), onSelect: onDelete, destructive: true},
          ]}/>
        </div>
      )}
    </div>
  )
  // Under `sm` the same two actions are also a left swipe (DESIGN.md "On a phone");
  // wrap `front` in SwipeRow with the rotate (surface) and delete (danger) actions.
  return <li>{front}</li>
}
```

Read `SwipeRow`'s props in `node_modules/@aoctech/ui/dist/components/swipe-row.d.ts` and the finance `LedgerRow` (`ui/src/components/finance/LedgerRow.tsx`) for the established usage; render the phone and laptop action sets with `sm:hidden` / `max-sm:hidden` so the test's `getAllByRole("button", {name: "Trocar segredo"})[0]` finds the laptop one in jsdom.

`NewEndpointDialog` (inside the page file):

```tsx
function NewEndpointDialog({open, eventTypes, onClose, onCreated}: {
  open: boolean; eventTypes: string[]; onClose: () => void; onCreated: (secret: string) => void
}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const [url, setUrl] = useState("")
  const [all, setAll] = useState(true)
  const [chosen, setChosen] = useState<string[]>([])
  const fe = useFieldErrors(["url", "events"], {keepOthers: false})
  const create = useMutation({
    mutationFn: () => createWebhook(scope, {url: url.trim(), events: all ? [] : chosen}),
    onSuccess: res => { setUrl(""); setAll(true); setChosen([]); onCreated(res.secret) },
    onError: e => { if (!fe.set(e)) toast.error(messageFor(e)) },
  })
  const groups = Object.entries(Object.groupBy(eventTypes, e => e.split(".")[0]))
  return (
    <Modal open={open} onClose={onClose} title={t("console.webhooks.form.title")}
           submitLabel={t("console.webhooks.form.submit")} cancelLabel={t("console.webhooks.back")}
           submitDisabled={url.trim() === "" || (!all && chosen.length === 0)} loading={create.isPending}
           onSubmit={() => { fe.reset(); create.mutate() }}>
      <div className="space-y-4">
        <Field label={t("console.webhooks.form.url")} htmlFor="hook-url" hint={t("console.webhooks.form.urlHint")} error={fe.of("url")}>
          <Input id="hook-url" type="url" inputMode="url" maxLength={limits.text.webhookURL} value={url}
                 placeholder={t("console.webhooks.form.urlPlaceholder")} {...fe.props("url", "hook-url")}
                 onChange={e => { setUrl(e.target.value); fe.clear("url") }}/>
        </Field>
        <label className="flex min-h-11 items-center gap-3 text-sm sm:min-h-8">
          <Switch checked={all} onCheckedChange={setAll} aria-label={t("console.webhooks.form.all")}/>
          {t("console.webhooks.form.all")}
        </label>
        {!all && groups.map(([group, events]) => (
          <fieldset key={group} className="space-y-1">
            <legend className="text-xs font-medium text-muted-foreground">{t(`console.webhooks.groups.${group}`, {defaultValue: group})}</legend>
            {events!.map(ev => (
              <label key={ev} className="flex min-h-11 items-center gap-2 text-sm sm:min-h-8">
                <Checkbox checked={chosen.includes(ev)}
                          onCheckedChange={on => setChosen(c => on ? [...c, ev] : c.filter(x => x !== ev))}/>
                <span className="font-mono text-xs">{ev}</span>
              </label>
            ))}
          </fieldset>
        ))}
        {fe.of("events") && <p role="alert" className="text-sm text-danger">{fe.of("events")}</p>}
      </div>
    </Modal>
  )
}
```

(`Object.groupBy` needs `lib: ["ES2024"]` or later in `tsconfig.json`; if tsc rejects it, group with a `reduce` into a `Record<string, string[]>`.)

`ui/DESIGN.md`, new section at the end:

```markdown
## Console: integration tools (2026-10-10)

**A credential is connected, never created here.** `/console/credentials` sends the merchant to CTech
Account's developer page in a new tab, with billing's machine scopes already ticked (the list comes from
the API), and takes back only the client id. The page never asks for or shows a client secret. Rows
from the tenant plan say **Gerenciado pela CTech** and have no action.

**A secret is shown once, in its own dialog.** Creating an endpoint or rotating its secret opens
`SecretReveal`: the secret in a read-only mono field with **Copiar**, and **Já copiei** as the only way
out. The secret lives in component state while the dialog is open and nowhere else.

**Testing changes nothing, so it does not ask.** **Testar** sends a ping at once and says when it
arrives. Rotating and deleting each confirm, naming the URL and saying what stops.

**On a phone** a webhook row keeps **Testar** on its line; **Trocar segredo** and **Excluir** are the
swipe and the "⋯", like finance rows. The connect step's field and button are full width, the button
under the field.

**Suspended live mode removes the add buttons, not the remove ones.** Connect, **Novo endpoint** and
**Testar** are not offered; one muted line says why. Disconnect, rotate and delete stay.
```

- [ ] **Step 5: Run to verify**

Run: `cd ui && npx vitest run --maxWorkers=2 "src/app/(console)/console/webhooks/webhooks.test.tsx" "src/app/(console)/console/credentials/credentials.test.tsx"; echo "vitest exit=$?"` then `npx next typegen && npx tsc --noEmit && npm run lint && npm run build`
Expected: `vitest exit=0`; all clean. With `NEXT_PUBLIC_MOCK_API=true npm run dev`, check `/console/webhooks` at 320, 375, 768 and 1280 px: add an endpoint (the secret dialog), test, rotate (confirm, then the new secret), delete; fill to 5 and see the limit line; swipe a row on a touch emulation and open its "⋯". `grep -n "—" src/locales/*/console.json` shows no new em dash.

- [ ] **Step 6: Commit**

```bash
git add ui/src ui/DESIGN.md
git commit -m "feat(console-ui): /console/webhooks, create, test, rotate and delete endpoints; the secret shown once"
```

---

## Final verification (end of Task 13, before the pull request)

Sequentially, never in parallel:

1. `cd api && gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race` — no gofmt output, PASS.
2. `cd api && make test-integration` — PASS, including part 1's `TestNoNonMerchantReachesAnyBillingHalfRoute` (now over eight more routes) and § 10.4 / § 10.5 here.
3. `cd ui && npx vitest run --maxWorkers=2; echo "vitest exit=$?"` — `vitest exit=0`. Then `npx next typegen && npx tsc --noEmit && npm run lint && npm run build`.
4. `grep -rn "api-commons v1.16.0" api/go.mod` prints the require line; `grep -c '"m2m": true' api/internal/oauthresource/scope-manifest.json` prints `11`.
5. Open the pull request with a body that lists the deploy gate (ctech-account § 9 step 1 live in the target environment; `.github/workflows/README.md` "Credentials, webhooks and m2m scopes") and the two cross-repo PRs (ctech-go-common `oauthclients` with tag `v1.16.0`; ctech-account `?new=1&scopes=`). No attribution of any kind.

## Spec coverage

| Spec | Where |
|---|---|
| § 5.1 list (client id, who, when) | Tasks 4, 6, 12 |
| § 5.1 connect, ctech-account check, four refusals, no oracle | Tasks 1, 5, 6 (decisions 2–6) |
| § 5.1 live requires the live tenant | part 1/2 chain (`no_merchant`), asserted indirectly by part 1's tests |
| § 5.1 delete, audited | Tasks 4, 6 |
| § 5.1 link with billing scopes preselected | Tasks 2, 11, 12 |
| § 5.1 credential belongs to the tenant | Task 4 (no link to the connector's membership) |
| § 5.2 routes, https, events from the list, ≤ 5, secret once and sealed, ping through delivery | Tasks 7–10, 13 |
| § 6.1 rows, scopes | Tasks 6, 10 |
| § 6.2 `connected_by`, `connected_at` | Task 4 |
| § 6.3 m2m marks, exactly | Task 3 |
| § 7 item 4 | Tasks 12, 13 |
| § 9 step 4, publish after ctech-account | Task 3 deploy section, decision 17 |
| § 10.4 credential hijack | Task 6 `TestConnectRefusalsNeverSayWhereAClientIs`, Task 4 race test |
| § 10.5 secret in exactly one response | Task 10 `TestTheSecretAppearsInExactlyOneResponsePerCreateOrRotate` |
| § 10.1 spoofing for the new routes | part 1's route-table test, unchanged |
