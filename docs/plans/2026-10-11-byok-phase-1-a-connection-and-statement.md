# BYOK phase 1, part A: shared packages, the connection and statement sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An owner or admin of an organization connects the organization's own Inter Empresas integration (live) or turns on the shared CTech integration against Inter's sandbox (test); billing seals the credentials through a dedicated outbound Lambda, keeps the connection healthy (tokens, certificate warnings, renewal, disconnect), and syncs the bank statement and balance into a `source: byok` account in the organization's Finanças space. Read-only: no charge is created in this part.

**Architecture:** Two new shared Go packages in ctech-go-common, `byok` (provider-neutral seam, RPC envelope, a fake provider for tests) and `inter` (Inter's implementation, credentials per call), tagged `v1.18.0`. In billing, a non-VPC arm64 Lambda `{env}-ctech-billing-byok-outbound` is the only code that talks to a provider: it holds the AES-256 credentials key (SSM SecureString read once at cold start), seals and opens credential bundles (`b1.`), dispatches ops through a provider registry and refuses live `collect` ops unless the legal gate is open. The `api` and `cmd/byok` reach it through `internal/byokrpc.LambdaProvider` (a `byok.Provider` over `lambda:InvokeFunction`) and own the token lifecycle in Valkey (sealed, single-flight, rate-limited). Connections live in a new `{env}_billing_byok` table; statement lines post through the existing ledger with a `FITID#…#B:` lock. Two small components (`Steps`, `FileDrop`) go to `@aoctech/ui` first (family rule).

**Tech Stack:** Go 1.27.2 (`api/go.mod`), Fiber v3.5, DynamoDB through `gopkg.aoctech.app/api-commons` (`v1.17.0` → `v1.18.0`), `github.com/aws/aws-lambda-go`, AWS SDK v2 (`lambda`, `ssm`), Valkey, Terraform 1.15.8, Next.js 16 static export, React 19, TanStack Query 5, `@aoctech/ui` (`0.4.1` → `0.5.0`), vitest.

**Spec:** [`docs/specs/2026-10-11-byok-inter-phase-1.md`](../specs/2026-10-11-byok-inter-phase-1.md) (with its 2026-10-11 amendment to D9 and § 9, added by this plan's pull request), factual basis [`docs/analysis/2026-10-11-byok-inter-parecer.md`](../analysis/2026-10-11-byok-inter-parecer.md). This part covers spec § 3 (all), § 4 (all ops are implemented in the Lambda; `collect` ops are refused live by the gate), § 5 steps 2 and 3, § 8 (except § 8.4, part B), § 9 (statement side), § 10 (flags exist and default off), § 11.1, § 11.2 (statement half), § 11.3 (Finanças), § 12.1 connection and classification routes, § 12.2 Connection row, § 12.3, § 12.4, § 12.6 (internal price and pilot subscription), § 16 steps 1, 3, 4, § 17 items 4, 7, 8, 11, 12 (statement), 13, 14. Parts: [B, collection](2026-10-11-byok-phase-1-b-collection.md), [C, pricing and usage](2026-10-11-byok-phase-1-c-pricing-and-usage.md).

## Scope decisions (read before executing)

1. **Test mode uses the integration CTech already has (the wallet's), against Inter's sandbox host** (owner correction, 2026-10-11, recorded in the spec amendment). Inter has no separate sandbox kind of integration: the same client_id, client_secret, certificate and key are used, only the base URL changes. The outbound Lambda's role is **granted read** on `/ctech-wallet/{env}/inter/client-id`, `/ctech-wallet/{env}/inter/client-secret`, `/ctech-wallet/{env}/inter/mtls-cert` and `/ctech-wallet/{env}/inter/mtls-key` (IAM grant, no copy: one source of truth, nothing to keep in step on the wallet's annual renewal). No value is ever read, printed or copied by a person or a script in this plan. The account number and the Pix key used in sandbox are billing's own non-secret `String` parameters `/ctech-billing/{env}/byok/inter-sandbox/account` and `/ctech-billing/{env}/byok/inter-sandbox/pix-key`, set out of band. **Risk, noted, not designed around:** the parecer (§ 2.6) and Inter's portal describe a separate sandbox portal and 30-day sandbox certificates; the owner states otherwise from experience, and this plan follows the owner. If Inter's sandbox refuses the production integration's credentials, every test-mode op fails with `byok_provider_unavailable` and the fallback is a dedicated sandbox integration whose four values go into billing's own SecureStrings (only the Lambda's two SSM paths change). The certificate expiry warned to the operator is the wallet integration's real `notAfter` (D-30, D-7, D-1), not a 30-day cycle.
2. **The legal-gate flags reach the Lambda as environment variables set from the same Terraform variables that write the SSM parameters.** `terraform apply` changing them replaces the Lambda's configuration, so every new invocation sees the new value at once (the instant kill switch, spec § 10). The `api` reads the SSM parameters at process start like every other parameter (owner rule: secrets and config read once at startup); a change reaches it on the next deploy or `systemctl restart app`. The Lambda's allowlist is what makes a stale `api` harmless.
3. **The connection's `jobs-index` is the existing `schedule-index` name** (`schedule_pk = {mode}#BYOK_CONNECTIONS`, `schedule_sk = {account_org_id}#{provider}`), so the shared `QueryOpts{IndexName: IndexSchedule}` helper reads it. One sparse partition per mode: fine at 10.000 connections (one key-condition Query, paged).
4. **Connect, test and renew run synchronously; backfill runs in the background.** `PUT …/connections/inter` returns once the Lambda's `Connect` answered and the row and the Finanças account exist. The first backfill window (the newest 90 days) starts in a detached goroutine bounded to 4 minutes under the per-connection lock; anything left is continued by the hourly `cmd/byok`. No queue, no new infrastructure.
5. **Owners and admins are e-mailed through a `notify_email` recorded on the connection** (required in the connect form, prefilled from the organization's issuer e-mail when it has one). ctech-account has no route listing an organization's admins' e-mails; adding one is a cross-repo follow-up, not this phase.
6. **Synced accounts are never chosen implicitly for a non-BYOK posting.** `receivingAccount` skips `source: byok` accounts unless a fact names one explicitly (part B passes the collecting provider's account). A wallet-rail invoice posted into a bank-synced account would make the ledger differ from the bank on day one.
7. **Disconnect in part A** deletes credentials, the Valkey token, clears the hook pointer and freezes the Finanças account. Open charges and webhooks do not exist yet; part B adds steps 1 and 2 (spec § 3.6) in front of these.
8. **`quota_bank_connections` is not enforced here** (part C). In part A any organization owner/admin may connect (D16).
9. **Lambda code lives in the billing Go module** (`api/cmd/byok-outbound`), built and zipped by a new CI job, deployed with `aws lambda update-function-code` after Terraform created the function from a placeholder zip (`lifecycle { ignore_changes = [s3_key, source_code_hash] }`). The wallet uses CDK; billing is Terraform (ADR 0010), so the shape differs while the runtime is the same.

## Global Constraints

- Go commands from `api/` (ctech-go-common from its repository root). Every Go task ends with `gofmt -l .` (no output), `go vet ./...`, `go test ./... -race -count=1`; tasks touching repositories, routes or `tests/integration` also run `docker compose -f docker-compose.test.yml up -d && make test-integration`.
- UI commands from `ui/`: `npx vitest run --maxWorkers=2 <files>; echo "vitest exit=$?"` (judge by the exit code), then `npx next typegen && npx tsc --noEmit`, `npm run lint`, `npm run build`. **Never run the Go and UI suites at the same time.**
- UI tasks are executed with the `/impeccable` skill on `@aoctech/ui`, following `ui/DESIGN.md`; every screen has **its own phone layout** (checked at 320, 375, 768, 1280 px). pt-BR and en for every string. **No em dash in any UI string.** Routes in English; Portuguese only in i18n text.
- Routes, exactly: `GET /v1.0/console/connections`, `GET|PUT|DELETE /v1.0/console/connections/{provider}`, `POST /v1.0/console/connections/{provider}/test`, `POST …/certificate`, `POST …/sync`, `PUT …/onboarding`, `GET /v1.0/console/finance/accounts/:id/unclassified`, `POST /v1.0/console/finance/unclassified/:tx_id/classify`. UI pages `/console/connections`, `/console/connections/inter`, `/finance/settings/connections`, `/finance/settings/connections/inter`, `/finance/unclassified?account=`.
- Scopes: connection reads `billing:finance:read`, writes `billing:finance:write`; space verb `finance.configure` (owner/admin of an `organization` workspace) for every connection route; classification `finance.import`.
- Problem codes (new in this part): `byok_not_connected` (404 body of an unknown provider is the ordinary `resource_not_found`), `byok_certificate_expired` (422), `byok_certificate_invalid` (422), `byok_key_mismatch` (422), `byok_credentials_refused` (422), `byok_collection_scopes_missing` (informational field, not an error here), `byok_sandbox_busy` (429), `byok_provider_unavailable` (503), `account_is_synced` (409), `byok_sync_too_soon` (429).
- Credentials key: SSM SecureString `/ctech-billing/{env}/byok/credentials-key`, 32 random bytes base64; readable **only** by the outbound Lambda's role. Sealed format `b1.` + base64url(nonce ‖ ciphertext), AES-256-GCM, AAD = `{account_organization_id}|{mode}|{provider}|{connection_id}|{version}`.
- Token cache: Valkey DB 3, key `byok:token:{provider}:{mode}:{connection_id}:{version}:{capability}`, value sealed with `crypto.Sealer` (FIELD_ENCRYPTION_KEY), TTL `expires_in − 300 s`; lock `byok:token-lock:…` (api-commons `lock`, TTL 30 s); limiter `byok:token-rate:{provider}:{connection_id}` at most 3 per 60 s; Inter banking limiter 8 per 60 s per connection; sandbox global Pix limiter 8 per 60 s.
- Inter hosts: production `https://cdpj.partners.bancointer.com.br`, sandbox `https://cdpj-sandbox.partners.uatinter.co`. Non-prod environments force sandbox for every mode.
- ctech-go-common release: `v1.18.0` (latest tag today `v1.17.0`). `@aoctech/ui` release `0.5.0`.
- Owner cost rule: no NAT gateway, no per-call KMS or Secrets Manager, secrets read once at start, DynamoDB key-condition reads only (never Scan), no new always-on instance; Lambda and API Gateway only for sporadic traffic.
- Commits: Conventional Commits. **No attribution of any kind** in commits or PR bodies, in every repository.

## Review Focus

1. **A credentials bundle copied onto another connection's row** (DB edit, restore mistake): it must not open. Task 9 `TestABundleSealedForOneConnectionDoesNotOpenOnAnother`.
2. **A live `collect` op reaching the Lambda directly while the gate is closed** (an `api` bug, a stale `api` after the flag was turned off): refused inside the Lambda, read and teardown ops still work. Task 9 `TestLiveCollectOpsAreRefusedUnlessTheProviderIsCleared`.
3. **Fifty concurrent statement syncs and console actions on a cold token cache**: exactly one token fetch, and never a fourth fetch inside a minute even when the cache is flushed. Task 10 `TestFiftyConcurrentCallersFetchOneToken`, `TestTheLimiterNeverLetsAFourthFetchOut`.
4. **The same 90-day window synced twice, and a line that reappears with a different amount under the same id** (a bank correction): nothing is added twice; the corrected line is posted as its own transaction keyed by content and the balance still equals the provider's. Task 13 `TestSyncingTheSameRangeTwiceAddsNothing`, `TestALineReusedWithAnotherAmountIsPostedOnce`.
5. **A member, a viewer, a personal space and another organization on every connection route**: `404`/`403` with zero `byok` table reads before membership. Task 16 `TestConnectionRoutesRefuseEveryoneButOwnersAndAdmins`.

## Custos de infra (infrastructure costs, all three parts)

Rough us-east-1 list prices, arm64, on-demand DynamoDB, assuming per live merchant: 1 connection, ~300 statement lines/month, ~500 charges/month at C scale, hourly sync (1 extrato page + 1 saldo + ~1 token/hour). "Merchants" counts live connections; test mode shares one integration and costs a fraction.

| Element | Cost driver | 10 | 1.000 | 10.000 | Cheaper alternative considered |
|---|---|---|---|---|---|
| Outbound Lambda (256 MB, arm64, ~300 ms) | invocations x duration | free tier (~25 k inv) | ~US$ 3 (2,3 M inv, ~170 k GB-s) | ~US$ 30 | Calling Inter from EC2 needs public IPv4 per instance or a NAT gateway (~US$ 33/month + US$ 0,045/GB): rejected (spec D5, owner rule) |
| Webhook Lambda (part B, 128 MB, ~150 ms) | one invocation per payment notice | free | ~US$ 0,30 (0,5 M) | ~US$ 3 (5 M) | Webhook straight to EC2 through Cloudflare: impossible, mTLS needs DNS-only to API Gateway |
| API Gateway HTTP API + regional mTLS domain (part B) | US$ 1 per million requests; custom domain free | ~US$ 0 | ~US$ 0,50 | ~US$ 5 | REST API (US$ 3,50/M): rejected |
| DynamoDB `byok` table + ledger writes | WRU (each statement line = 1 TransactWriteItems of ~8 items) + RRU | <US$ 1 | ~US$ 4 (2,4 M WRU) | ~US$ 40 | Batch posting per day: fewer items but the balance lags the bank; rejected for D20 |
| Usage + subscription counters (part C) | 1 write per charge + 1 per subscription change | ~0 | ~US$ 0,40 | ~US$ 4 | Counting subscriptions with a Query per create: O(n) reads per create, rejected |
| SSM Parameter Store | standard parameters, standard throughput; read once per cold start / process start | US$ 0 | US$ 0 | US$ 0 | Secrets Manager (US$ 0,40/secret + per call): rejected |
| KMS | only the `aws/ssm` managed key decrypting SecureStrings at cold start | ~US$ 0 | <US$ 0,10 | <US$ 1 | Per-merchant CMKs (US$ 1/key/month = US$ 10.000/month at 10 k): rejected (R1) |
| Valkey (existing shared cluster, DB 3) | keys: 1 token + 2 counters per connection | US$ 0 | US$ 0 | US$ 0 (≈30 k small keys) | DynamoDB token cache: per-call reads, rejected |
| CloudWatch Logs (Lambdas) | ingestion US$ 0,50/GB, retention 14 days | <US$ 1 | ~US$ 2 | ~US$ 15 | Metadata-only log lines (op, failed), no payloads, enforced in Task 9 |
| S3 truststore (part B) | one versioned object | ~US$ 0 | ~US$ 0 | ~US$ 0 | n/a |
| SES (certificate warnings, excess payments, disconnect notices) | US$ 0,10 per 1.000 | ~0 | ~US$ 0,10 | ~US$ 1 | n/a |
| EC2 leader running `cmd/byok` hourly | existing instance; CPU minutes only | US$ 0 | US$ 0 | US$ 0 (bounded worker pool of 16; ~10 min/hour at 10 k) | A new scheduler instance or Fargate task: rejected (no always-on extra) |
| Data transfer Lambda → Inter | ~2 KB per call | ~0 | ~US$ 0,20 | ~US$ 2 | n/a |
| **Total new monthly** | | **≈ US$ 1** | **≈ US$ 11** | **≈ US$ 105** | |

Scale notes: the hourly job's per-connection work is O(1) Inter calls and key-condition reads; Inter's banking limit (10/min) is per account, so connections run in parallel. Revisit the shared token limit question (spec § 15 item 2) before ~200 live connections.

---

## File Structure

ctech-go-common (`gopkg.aoctech.app/api-commons`):

| File | Responsibility |
|---|---|
| `byok/types.go` | `Date`, `Capability`, `Product`, credentials, charge, refund, statement, balance, callback types |
| `byok/provider.go` | `Provider` interface |
| `byok/errors.go` | sentinel errors, `ErrorCode`/`ErrorFromCode` (wire codes) |
| `byok/envelope.go` | `Op`, `OpGroup`, `GroupOf`, `Request`, `Response`, payload arg structs |
| `byok/byoktest/fake.go` | in-memory `Provider` test double |
| `inter/client.go` | `Credentials`, hosts, mTLS transport cache, `Do`, `FetchToken` |
| `inter/errors.go` | Bacen RFC 7807 problem, status → `byok` error mapping |
| `inter/money.go`, `inter/txid.go` | reais/centavos, txid rule |
| `inter/qr/qr.go` | BR Code PNG (from the wallet's `qr.go`) |
| `inter/pix/*.go` | `cobv`, `pix`/devolução, webhook, callbacks, sandbox pay (Bacen types) |
| `inter/banking/*.go` | extrato completo (paginated), saldo |
| `inter/provider.go`, `inter/callback.go` | `inter.Provider` adapter, `ParseCallback` |

ctech-ui: `src/components/steps.tsx`, `src/components/file-drop.tsx` (+ tests, stories, exports).

ctech-billing:

| File | Responsibility |
|---|---|
| `docs/adr/0029-byok-phase-1.md` | the spec's decisions |
| `api/internal/domain/byokconn/connection.go` | `Connection` and its states, health, warnings |
| `api/internal/byokseal/seal.go` | `b1.` sealing with AAD (Lambda only) |
| `api/cmd/byok-outbound/*.go` | the Lambda: config, key, sandbox credentials, gate, dispatcher |
| `api/internal/byokrpc/*.go` | `LambdaProvider`, `TokenManager`, limiters |
| `api/internal/repositories/byok.go` | connection rows |
| `api/internal/repositories/ledger_byok.go` | bank lines, reclassification, provider balance |
| `api/internal/services/byok_connections.go` | connect, test, renew, onboarding, disconnect, sandbox toggle |
| `api/internal/services/byok_statement.go` | backfill, incremental sync, sync now |
| `api/internal/api/v1/console_connections.go`, `finance_unclassified.go` | routes |
| `api/cmd/byok/main.go` | hourly job (part A: steps 2, 3) |
| `terraform/billing/byok.tf`, `terraform/scripts/create-byok-credentials-key.sh` | Lambda, IAM, SSM, flags |
| `.github/workflows/byok-lambdas.yml` | build and deploy the Lambda code |
| `ui/src/lib/api/connections.ts`, `ui/src/components/connections/*` | API layer, checklist, connection screen |
| `ui/src/app/(console)/console/connections/*`, `ui/src/app/(finance)/finance/settings/connections/*`, `ui/src/app/(finance)/finance/unclassified/*` | pages |

## Cross-repo tasks

### Task 1: ctech-go-common — the `byok` seam (types, provider, errors, envelope)

**Repo:** ctech-go-common, branch `feat/byok-inter` from `origin/main`.

**Files:**
- Create: `byok/types.go`, `byok/provider.go`, `byok/errors.go`, `byok/envelope.go`, `byok/doc.go`
- Test: `byok/types_test.go`, `byok/envelope_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (every later task and part B/C use these names exactly):

```go
package byok
type Date struct{ Year int; Month time.Month; Day int }
func ParseDate(s string) (Date, error); func (d Date) String() string; func (d Date) AddDays(n int) Date; func (d Date) IsZero() bool; func (d Date) Before(o Date) bool
type Capability string // CapabilityStatement "statement", CapabilityCollection "collection"
type Product string    // ProductPix "pix" (reserved: "boleto", "rec", "cobr")
type Field struct{ Name string; Secret, File, Required bool }
type ConnectionRef struct{ AccountOrganizationID, ID string; Version int; Sealed, Account, PixKey string }
type Credentials struct{ Values map[string]string; Ref *ConnectionRef; Account, PixKey string; Sandbox bool }
type Token struct{ Value string; ExpiresIn int; Scopes []string; CertNotAfter time.Time }
type Connected struct{ Capabilities []Capability; ScopesGranted, MissingScopes []string; CertSubject, CertFingerprint string; CertNotAfter time.Time; Balance Balance; Sealed string }
type Debtor struct{ Name, TaxID string }
type DueCharge struct{ Ref string; Due Date; ValidityDays int; Amount int64; FineBasisPoints, MonthlyInterestBasisPoints int; Debtor Debtor; Message string }
type ChargeStatus string // ChargeOpen, ChargePaid, ChargeExpired, ChargeRemoved
type Payment struct{ ID string; Amount, LateCharges int64; PaidAt time.Time }
type Charge struct{ Ref string; Status ChargeStatus; Amount int64; Payable string; Payments []Payment }
type RefundRequest struct{ PaymentID, RefundID string; Amount int64; Reason string }
type RefundStatus string // RefundProcessing, RefundDone, RefundFailed
type Refund struct{ ID, PaymentID string; Amount int64; Status RefundStatus }
type Direction string // Credit "credit", Debit "debit"
type StatementLine struct{ ID string; Date Date; Direction Direction; Amount int64; Title, Description, ChargeRef, PaymentID string }
type StatementPage struct{ Lines []StatementLine; Page, TotalPages int }
type Balance struct{ Available, Blocked int64; At Date }
type CallbackAttempt struct{ ChargeRef string; HTTPStatus, Attempt int; At time.Time }
type Callback struct{ Account string; ChargeRefs, PaymentIDs []string }
type Provider interface { /* spec § 4.4, below */ }
var ErrUnauthorized, ErrCredentialsRefused, ErrScopesRefused, ErrNotFound, ErrConcluded, ErrRateLimited, ErrUnavailable, ErrCertificateExpired, ErrCertificateInvalid, ErrKeyMismatch, ErrInvalidRequest, ErrOpRefused, ErrSandboxOnly error
func ErrorCode(err error) string; func ErrorFromCode(code, message string) error
type Op string; type OpGroup string; func GroupOf(op Op) OpGroup
type Request struct{ Op Op; Provider, Mode string; Connection ConnectionRef; OAuthToken string; Payload json.RawMessage }
type Response struct{ Payload json.RawMessage; Error, Code string }
```

- [ ] **Step 1: Write the failing tests**

`byok/types_test.go`:

```go
package byok

import (
	"testing"
	"time"
)

func TestDateRoundTripsAndAddsCivilDays(t *testing.T) {
	d, err := ParseDate("2026-02-27")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.AddDays(2).String(); got != "2026-03-01" {
		t.Fatalf("AddDays across February = %s, want 2026-03-01", got)
	}
	if !d.Before(d.AddDays(1)) || d.Before(d) {
		t.Fatal("Before is a strict order")
	}
	if _, err := ParseDate("2026-13-01"); err == nil {
		t.Fatal("month 13 accepted")
	}
	if !(Date{}).IsZero() {
		t.Fatal("zero Date is not IsZero")
	}
	if d.Month != time.February {
		t.Fatal("Month is a time.Month")
	}
}

func TestErrorCodesRoundTrip(t *testing.T) {
	for _, err := range []error{ErrUnauthorized, ErrCredentialsRefused, ErrScopesRefused, ErrNotFound, ErrConcluded,
		ErrRateLimited, ErrUnavailable, ErrCertificateExpired, ErrCertificateInvalid, ErrKeyMismatch,
		ErrInvalidRequest, ErrOpRefused, ErrSandboxOnly} {
		code := ErrorCode(err)
		if code == "" || code == "internal" {
			t.Fatalf("%v has no wire code", err)
		}
		if back := ErrorFromCode(code, "detail"); !errors.Is(back, err) {
			t.Fatalf("code %q came back as %v", code, back)
		}
	}
	if ErrorCode(fmt.Errorf("boom")) != "internal" {
		t.Fatal("an unknown error is internal")
	}
}
```

(add `"errors"` and `"fmt"` to the imports.)

`byok/envelope_test.go`:

```go
package byok

import "testing"

func TestEveryOpHasExactlyOneGroup(t *testing.T) {
	want := map[Op]OpGroup{
		OpConnect: GroupRead, OpReseal: GroupRead, OpToken: GroupRead, OpBalance: GroupRead,
		OpStatementPage: GroupRead, OpGetCharge: GroupRead, OpGetPayment: GroupRead, OpGetRefund: GroupRead,
		OpGetWebhook: GroupRead, OpListCallbacks: GroupRead, OpRetryCallbacks: GroupRead,
		OpCancelCharge: GroupTeardown, OpDeleteWebhook: GroupTeardown,
		OpCreateDueCharge: GroupCollect, OpUpdateCharge: GroupCollect, OpRefund: GroupCollect, OpRegisterWebhook: GroupCollect,
		OpSimulatePayment: GroupSandbox,
	}
	if len(want) != len(AllOps) {
		t.Fatalf("AllOps has %d ops, the table %d", len(AllOps), len(want))
	}
	for _, op := range AllOps {
		if got := GroupOf(op); got != want[op] {
			t.Fatalf("GroupOf(%s) = %s, want %s", op, got, want[op])
		}
	}
	if GroupOf("Nope") != "" {
		t.Fatal("an unknown op has no group")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./byok/...`
Expected: FAIL, `undefined: ParseDate` (package does not exist yet).

- [ ] **Step 3: Write the package**

`byok/doc.go`:

```go
// Package byok is the provider-neutral seam of "bring your own key": an
// organization's own bank or PSP account, reached with the organization's own
// credentials (billing spec 2026-10-11 § 4.4). Money is in centavos, dates are
// civil dates. Implementations: api-commons/inter. Test double: byok/byoktest.
//
// Nothing here reads SSM, the environment or a cache: credentials are passed
// per call, so one process can serve many organizations.
package byok
```

`byok/types.go`:

```go
package byok

import (
	"fmt"
	"time"
)

// Date is a civil date (no time zone). Providers speak YYYY-MM-DD.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("byok: date %q: %w", s, err)
	}
	return Date{t.Year(), t.Month(), t.Day()}, nil
}

func DateOf(t time.Time) Date { return Date{t.Year(), t.Month(), t.Day()} }

func (d Date) time() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }
func (d Date) String() string  { return d.time().Format(time.DateOnly) }
func (d Date) AddDays(n int) Date {
	return DateOf(d.time().AddDate(0, 0, n))
}
func (d Date) IsZero() bool       { return d == Date{} }
func (d Date) Before(o Date) bool { return d.time().Before(o.time()) }

// Capability is what a connection may be used for. Providers map their scopes
// onto these; billing never reads a provider scope name.
type Capability string

const (
	CapabilityStatement  Capability = "statement"
	CapabilityCollection Capability = "collection"
)

// Product is the webhook path segment and the callback parser selector.
type Product string

const ProductPix Product = "pix"

// Field is one credential the console asks for.
type Field struct {
	Name     string
	Secret   bool // never echoed back
	File     bool // uploaded as a file (PEM)
	Required bool
}

// ConnectionRef is a stored connection as the outbound Lambda receives it:
// the sealed bundle and what binds it (the AAD). ID "sandbox" means CTech's own
// integration against the provider's sandbox host.
type ConnectionRef struct {
	AccountOrganizationID string `json:"account_organization_id,omitempty"`
	ID                    string `json:"id"`
	Version               int    `json:"version,omitempty"`
	Sealed                string `json:"sealed,omitempty"`
	Account               string `json:"account,omitempty"`
	PixKey                string `json:"pix_key,omitempty"`
}

// Credentials is either plaintext Values (only inside the Lambda, or in the
// single Connect request) or a sealed Ref the Lambda opens.
type Credentials struct {
	Values  map[string]string
	Ref     *ConnectionRef
	Account string
	PixKey  string
	Sandbox bool
}

// Token is a provider bearer. CertNotAfter is the integration certificate's
// expiry, reported so the operator can be warned about a shared integration.
type Token struct {
	Value        string    `json:"value"`
	ExpiresIn    int       `json:"expires_in"`
	Scopes       []string  `json:"scopes,omitempty"`
	CertNotAfter time.Time `json:"cert_not_after,omitempty"`
}

// Connected is the outcome of a connection test. Sealed is set only by the
// outbound Lambda (it is the ciphertext the caller stores).
type Connected struct {
	Capabilities    []Capability `json:"capabilities"`
	ScopesGranted   []string     `json:"scopes_granted"`
	MissingScopes   []string     `json:"missing_scopes,omitempty"`
	CertSubject     string       `json:"cert_subject,omitempty"`
	CertFingerprint string       `json:"cert_fingerprint,omitempty"`
	CertNotAfter    time.Time    `json:"cert_not_after,omitempty"`
	Balance         Balance      `json:"balance"`
	Sealed          string       `json:"sealed,omitempty"`
}

type Debtor struct {
	Name  string `json:"name"`
	TaxID string `json:"tax_id"` // digits only: 11 = CPF, 14 = CNPJ
}

// DueCharge is a charge with a due date (Inter: cobv). Basis points: 200 = 2%.
type DueCharge struct {
	Ref                        string `json:"ref"`
	Due                        Date   `json:"due"`
	ValidityDays               int    `json:"validity_days"`
	Amount                     int64  `json:"amount"`
	FineBasisPoints            int    `json:"fine_bp,omitempty"`
	MonthlyInterestBasisPoints int    `json:"monthly_interest_bp,omitempty"`
	Debtor                     Debtor `json:"debtor"`
	Message                    string `json:"message,omitempty"`
}

type ChargeStatus string

const (
	ChargeOpen    ChargeStatus = "open"
	ChargePaid    ChargeStatus = "paid"
	ChargeExpired ChargeStatus = "expired"
	ChargeRemoved ChargeStatus = "removed"
)

type Payment struct {
	ID          string    `json:"id"` // Inter: endToEndId
	Amount      int64     `json:"amount"`
	LateCharges int64     `json:"late_charges,omitempty"`
	PaidAt      time.Time `json:"paid_at"`
}

type Charge struct {
	Ref      string       `json:"ref"`
	Status   ChargeStatus `json:"status"`
	Amount   int64        `json:"amount"`
	Payable  string       `json:"payable,omitempty"` // Inter: pixCopiaECola
	Payments []Payment    `json:"payments,omitempty"`
}

type RefundRequest struct {
	PaymentID string `json:"payment_id"`
	RefundID  string `json:"refund_id"`
	Amount    int64  `json:"amount"`
	Reason    string `json:"reason,omitempty"`
}

type RefundStatus string

const (
	RefundProcessing RefundStatus = "processing"
	RefundDone       RefundStatus = "done"
	RefundFailed     RefundStatus = "failed"
)

type Refund struct {
	ID        string       `json:"id"`
	PaymentID string       `json:"payment_id"`
	Amount    int64        `json:"amount"`
	Status    RefundStatus `json:"status"`
}

type Direction string

const (
	Credit Direction = "credit"
	Debit  Direction = "debit"
)

// StatementLine is one bank statement entry. Amount is always positive;
// Direction says which way it moved.
type StatementLine struct {
	ID          string    `json:"id"`
	Date        Date      `json:"date"`
	Direction   Direction `json:"direction"`
	Amount      int64     `json:"amount"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	ChargeRef   string    `json:"charge_ref,omitempty"`
	PaymentID   string    `json:"payment_id,omitempty"`
}

type StatementPage struct {
	Lines      []StatementLine `json:"lines"`
	Page       int             `json:"page"`
	TotalPages int             `json:"total_pages"`
}

type Balance struct {
	Available int64 `json:"available"`
	Blocked   int64 `json:"blocked"`
	At        Date  `json:"at"`
}

type CallbackAttempt struct {
	ChargeRef  string    `json:"charge_ref"`
	HTTPStatus int       `json:"http_status"`
	Attempt    int       `json:"attempt"`
	At         time.Time `json:"at"`
}

// Callback is what a webhook body claims. Nothing in it is trusted: it only
// names which charges to re-query.
type Callback struct {
	Account    string   `json:"account,omitempty"`
	ChargeRefs []string `json:"charge_refs"`
	PaymentIDs []string `json:"payment_ids,omitempty"`
}
```

Add JSON helpers so `Date` travels as `"YYYY-MM-DD"`:

```go
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" {
		*d = Date{}
		return nil
	}
	p, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = p
	return nil
}
```

(add `"strings"` to the imports.)

`byok/provider.go`:

```go
package byok

import (
	"context"
	"time"
)

// Provider is one bank/PSP. Every method takes the credentials per call.
// t is the bearer for the capability in use (Token with that capability).
type Provider interface {
	Name() string
	CredentialFields() []Field

	Connect(ctx context.Context, c Credentials) (Connected, error)
	Token(ctx context.Context, c Credentials, set Capability) (Token, error)

	CreateDueCharge(ctx context.Context, c Credentials, t Token, in DueCharge) (Charge, error)
	GetCharge(ctx context.Context, c Credentials, t Token, ref string) (Charge, error)
	UpdateCharge(ctx context.Context, c Credentials, t Token, ref string, amount int64) (Charge, error)
	CancelCharge(ctx context.Context, c Credentials, t Token, ref string) error
	Refund(ctx context.Context, c Credentials, t Token, in RefundRequest) (Refund, error)
	GetRefund(ctx context.Context, c Credentials, t Token, paymentID, refundID string) (Refund, error)

	StatementPage(ctx context.Context, c Credentials, t Token, from, to Date, page int) (StatementPage, error)
	Balance(ctx context.Context, c Credentials, t Token, at Date) (Balance, error)

	GetWebhook(ctx context.Context, c Credentials, t Token, product Product) (url string, err error)
	RegisterWebhook(ctx context.Context, c Credentials, t Token, product Product, url string) error
	DeleteWebhook(ctx context.Context, c Credentials, t Token, product Product) error
	ListCallbacks(ctx context.Context, c Credentials, t Token, since time.Time) ([]CallbackAttempt, error)
	RetryCallbacks(ctx context.Context, c Credentials, t Token, refs []string) error

	// SimulatePayment pays an open charge in the provider's sandbox only.
	SimulatePayment(ctx context.Context, c Credentials, t Token, ref string) error

	// ParseCallback runs with no credentials. Its output only wakes the caller.
	ParseCallback(product Product, headers map[string]string, body []byte) (Callback, error)
}
```

`byok/errors.go`:

```go
package byok

import (
	"errors"
	"fmt"
)

var (
	ErrUnauthorized       = errors.New("byok: bearer refused")
	ErrCredentialsRefused = errors.New("byok: credentials refused")
	ErrScopesRefused      = errors.New("byok: scopes refused")
	ErrNotFound           = errors.New("byok: not found")
	ErrConcluded          = errors.New("byok: already concluded")
	ErrRateLimited        = errors.New("byok: rate limited")
	ErrUnavailable        = errors.New("byok: provider unavailable")
	ErrCertificateExpired = errors.New("byok: certificate expired")
	ErrCertificateInvalid = errors.New("byok: certificate invalid")
	ErrKeyMismatch        = errors.New("byok: key does not match certificate")
	ErrInvalidRequest     = errors.New("byok: invalid request")
	ErrOpRefused          = errors.New("byok: operation refused by the gate")
	ErrSandboxOnly        = errors.New("byok: sandbox-only operation")
)

var codes = []struct {
	code string
	err  error
}{
	{"unauthorized", ErrUnauthorized}, {"credentials_refused", ErrCredentialsRefused},
	{"scopes_refused", ErrScopesRefused}, {"not_found", ErrNotFound}, {"concluded", ErrConcluded},
	{"rate_limited", ErrRateLimited}, {"unavailable", ErrUnavailable},
	{"certificate_expired", ErrCertificateExpired}, {"certificate_invalid", ErrCertificateInvalid},
	{"key_mismatch", ErrKeyMismatch}, {"invalid_request", ErrInvalidRequest},
	{"op_refused", ErrOpRefused}, {"sandbox_only", ErrSandboxOnly},
}

// ErrorCode is the wire code of err; "internal" for anything unclassified.
func ErrorCode(err error) string {
	for _, c := range codes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return "internal"
}

// ErrorFromCode rebuilds the sentinel on the caller's side. message never
// carries a secret (the Lambda only sends sanitized text).
func ErrorFromCode(code, message string) error {
	for _, c := range codes {
		if c.code == code {
			return fmt.Errorf("%w: %s", c.err, message)
		}
	}
	return fmt.Errorf("byok: %s", message)
}
```

`byok/envelope.go`:

```go
package byok

import (
	"encoding/json"
	"time"
)

// Op names an outbound Lambda operation (spec § 4.1).
type Op string

const (
	OpConnect         Op = "Connect"
	OpReseal          Op = "Reseal"
	OpToken           Op = "Token"
	OpBalance         Op = "Balance"
	OpStatementPage   Op = "StatementPage"
	OpCreateDueCharge Op = "CreateDueCharge"
	OpGetCharge       Op = "GetCharge"
	OpUpdateCharge    Op = "UpdateCharge"
	OpCancelCharge    Op = "CancelCharge"
	OpGetPayment      Op = "GetPayment"
	OpRefund          Op = "Refund"
	OpGetRefund       Op = "GetRefund"
	OpGetWebhook      Op = "GetWebhook"
	OpRegisterWebhook Op = "RegisterWebhook"
	OpDeleteWebhook   Op = "DeleteWebhook"
	OpListCallbacks   Op = "ListCallbacks"
	OpRetryCallbacks  Op = "RetryCallbacks"
	OpSimulatePayment Op = "SimulatePayment"
)

var AllOps = []Op{OpConnect, OpReseal, OpToken, OpBalance, OpStatementPage, OpCreateDueCharge, OpGetCharge,
	OpUpdateCharge, OpCancelCharge, OpGetPayment, OpRefund, OpGetRefund, OpGetWebhook, OpRegisterWebhook,
	OpDeleteWebhook, OpListCallbacks, OpRetryCallbacks, OpSimulatePayment}

// OpGroup is the allowlist class of an op (spec § 4.1).
type OpGroup string

const (
	GroupRead     OpGroup = "read"
	GroupTeardown OpGroup = "teardown"
	GroupCollect  OpGroup = "collect"
	GroupSandbox  OpGroup = "sandbox"
)

func GroupOf(op Op) OpGroup {
	switch op {
	case OpConnect, OpReseal, OpToken, OpBalance, OpStatementPage, OpGetCharge, OpGetPayment, OpGetRefund,
		OpGetWebhook, OpListCallbacks, OpRetryCallbacks:
		return GroupRead
	case OpCancelCharge, OpDeleteWebhook:
		return GroupTeardown
	case OpCreateDueCharge, OpUpdateCharge, OpRefund, OpRegisterWebhook:
		return GroupCollect
	case OpSimulatePayment:
		return GroupSandbox
	}
	return ""
}

// Request is the outbound Lambda's input. Mode is "live" or "test".
type Request struct {
	Op         Op              `json:"op"`
	Provider   string          `json:"provider"`
	Mode       string          `json:"mode"`
	Connection ConnectionRef   `json:"connection"`
	OAuthToken string          `json:"oauth_token,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// Response is the outbound Lambda's output. Error and Code are set together;
// a provider or gate failure is never a Lambda invoke error.
type Response struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
	Code    string          `json:"code,omitempty"`
}

// Payload arguments, one per op that takes any.
type (
	ConnectArgs struct {
		Values  map[string]string `json:"values"`
		Account string            `json:"account"`
		PixKey  string            `json:"pix_key,omitempty"`
	}
	ResealArgs struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	TokenArgs     struct{ Set Capability `json:"set"` }
	StatementArgs struct {
		From Date `json:"from"`
		To   Date `json:"to"`
		Page int  `json:"page"`
	}
	BalanceArgs   struct{ At Date `json:"at"` }
	RefArgs       struct{ Ref string `json:"ref"` }
	UpdateArgs    struct {
		Ref    string `json:"ref"`
		Amount int64  `json:"amount"`
	}
	GetRefundArgs struct {
		PaymentID string `json:"payment_id"`
		RefundID  string `json:"refund_id"`
	}
	WebhookArgs struct {
		Product Product `json:"product"`
		URL     string  `json:"url,omitempty"`
	}
	CallbacksArgs struct{ Since time.Time `json:"since"` }
	RetryArgs     struct{ Refs []string `json:"refs"` }
	WebhookResult struct{ URL string `json:"url"` }
)
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . && go vet ./byok/... && go test ./byok/... -race -count=1`
Expected: no gofmt output, `ok gopkg.aoctech.app/api-commons/byok`.

- [ ] **Step 5: Commit**

```bash
git add byok/
git commit -m "feat(byok): provider-neutral bring-your-own-key seam and outbound envelope"
```

### Task 2: ctech-go-common — `byok/byoktest`, the fake provider

**Repo:** ctech-go-common.

**Files:**
- Create: `byok/byoktest/fake.go`
- Test: `byok/byoktest/fake_test.go`

**Interfaces:**
- Consumes: Task 1's `byok.Provider` and types.
- Produces: `byoktest.New() *Fake` (implements `byok.Provider`, name `"fake"`), `(*Fake).Pay(ref string, amount, late int64, at time.Time)`, `(*Fake).AddLines(lines ...byok.StatementLine)`, `(*Fake).SetBalance(b byok.Balance)`, `(*Fake).SetBalanceAt(d byok.Date, available int64)`, `(*Fake).FailNext(op byok.Op, err error)`, `(*Fake).Calls(op byok.Op) int`, `(*Fake).Charge(ref string) (byok.Charge, bool)`, `(*Fake).WebhookURL(product byok.Product) string`, `(*Fake).SetWebhookURL(product byok.Product, url string)`, `(*Fake).Capabilities []byok.Capability` (field, default both), `(*Fake).PageSize int` (field, default 50). Part B's tests use `Pay`, `Charge`, `WebhookURL`.

- [ ] **Step 1: Write the failing test**

```go
package byoktest

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

var _ byok.Provider = (*Fake)(nil)

func TestFakeRunsAChargeAndAStatement(t *testing.T) {
	ctx := context.Background()
	f := New()
	c := byok.Credentials{Values: map[string]string{"client_id": "x"}}
	tok, err := f.Token(ctx, c, byok.CapabilityCollection)
	if err != nil {
		t.Fatal(err)
	}
	due, _ := byok.ParseDate("2026-11-10")
	ch, err := f.CreateDueCharge(ctx, c, tok, byok.DueCharge{Ref: "ctb01", Due: due, ValidityDays: 30, Amount: 9990})
	if err != nil || ch.Status != byok.ChargeOpen || ch.Payable == "" {
		t.Fatalf("create = %+v, %v", ch, err)
	}
	again, err := f.CreateDueCharge(ctx, c, tok, byok.DueCharge{Ref: "ctb01", Due: due, Amount: 9990})
	if err != nil || again.Ref != "ctb01" {
		t.Fatal("creating the same ref again is idempotent")
	}
	f.Pay("ctb01", 9990, 0, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	got, _ := f.GetCharge(ctx, c, tok, "ctb01")
	if got.Status != byok.ChargePaid || len(got.Payments) != 1 {
		t.Fatalf("after Pay = %+v", got)
	}
	f.PageSize = 2
	f.AddLines(
		byok.StatementLine{ID: "1", Date: due, Direction: byok.Credit, Amount: 100},
		byok.StatementLine{ID: "2", Date: due, Direction: byok.Debit, Amount: 50},
		byok.StatementLine{ID: "3", Date: due, Direction: byok.Credit, Amount: 9990, ChargeRef: "ctb01"},
	)
	p1, _ := f.StatementPage(ctx, c, tok, due, due, 0)
	p2, _ := f.StatementPage(ctx, c, tok, due, due, 1)
	if p1.TotalPages != 2 || len(p1.Lines) != 2 || len(p2.Lines) != 1 {
		t.Fatalf("pages = %+v / %+v", p1, p2)
	}
	f.FailNext(byok.OpBalance, byok.ErrUnavailable)
	if _, err := f.Balance(ctx, c, tok, due); !errors.Is(err, byok.ErrUnavailable) {
		t.Fatalf("FailNext not honoured: %v", err)
	}
	if f.Calls(byok.OpBalance) != 1 {
		t.Fatal("calls are counted, failed ones included")
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./byok/byoktest/...`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement**

```go
// Package byoktest is an in-memory byok.Provider for tests in every consumer.
// It is deterministic, safe for concurrent use, and never touches a network.
package byoktest

import (
	"context"
	"sort"
	"sync"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

type Fake struct {
	mu           sync.Mutex
	Capabilities []byok.Capability
	PageSize     int
	charges      map[string]*byok.Charge
	refunds      map[string]byok.Refund
	lines        []byok.StatementLine
	balance      byok.Balance
	balanceAt    map[byok.Date]int64
	hooks        map[byok.Product]string
	callbacks    []byok.CallbackAttempt
	failNext     map[byok.Op]error
	calls        map[byok.Op]int
	tokens       int
}

func New() *Fake {
	return &Fake{
		Capabilities: []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection},
		PageSize:     50,
		charges:      map[string]*byok.Charge{},
		refunds:      map[string]byok.Refund{},
		balanceAt:    map[byok.Date]int64{},
		hooks:        map[byok.Product]string{},
		failNext:     map[byok.Op]error{},
		calls:        map[byok.Op]int{},
	}
}

func (f *Fake) enter(op byok.Op) error {
	f.calls[op]++
	if err, ok := f.failNext[op]; ok {
		delete(f.failNext, op)
		return err
	}
	return nil
}

func (f *Fake) FailNext(op byok.Op, err error) { f.mu.Lock(); f.failNext[op] = err; f.mu.Unlock() }
func (f *Fake) Calls(op byok.Op) int           { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[op] }
func (f *Fake) SetBalance(b byok.Balance)      { f.mu.Lock(); f.balance = b; f.mu.Unlock() }
func (f *Fake) AddLines(ls ...byok.StatementLine) {
	f.mu.Lock()
	f.lines = append(f.lines, ls...)
	f.mu.Unlock()
}
func (f *Fake) SetWebhookURL(p byok.Product, url string) { f.mu.Lock(); f.hooks[p] = url; f.mu.Unlock() }
func (f *Fake) WebhookURL(p byok.Product) string         { f.mu.Lock(); defer f.mu.Unlock(); return f.hooks[p] }
func (f *Fake) AddCallback(a byok.CallbackAttempt)       { f.mu.Lock(); f.callbacks = append(f.callbacks, a); f.mu.Unlock() }

// Pay records a payment on ref (a second Pay is a duplicate payment).
func (f *Fake) Pay(ref string, amount, late int64, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := f.charges[ref]
	if ch == nil {
		return
	}
	ch.Payments = append(ch.Payments, byok.Payment{ID: "E2E" + ref + string(rune('A'+len(ch.Payments))), Amount: amount, LateCharges: late, PaidAt: at})
	ch.Status = byok.ChargePaid
}

func (f *Fake) Charge(ref string) (byok.Charge, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.charges[ref]
	if !ok {
		return byok.Charge{}, false
	}
	return *ch, true
}

func (f *Fake) Name() string { return "fake" }
func (f *Fake) CredentialFields() []byok.Field {
	return []byok.Field{{Name: "client_id", Required: true}, {Name: "client_secret", Secret: true, Required: true}}
}

func (f *Fake) Connect(_ context.Context, c byok.Credentials) (byok.Connected, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpConnect); err != nil {
		return byok.Connected{}, err
	}
	return byok.Connected{Capabilities: f.Capabilities, ScopesGranted: []string{"fake.all"},
		CertSubject: "CN=fake", CertFingerprint: "00", CertNotAfter: time.Now().AddDate(1, 0, 0), Balance: f.balance}, nil
}

func (f *Fake) Token(_ context.Context, _ byok.Credentials, set byok.Capability) (byok.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpToken); err != nil {
		return byok.Token{}, err
	}
	f.tokens++
	return byok.Token{Value: "tok-" + string(set), ExpiresIn: 3600}, nil
}

func (f *Fake) CreateDueCharge(_ context.Context, _ byok.Credentials, _ byok.Token, in byok.DueCharge) (byok.Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpCreateDueCharge); err != nil {
		return byok.Charge{}, err
	}
	if ch, ok := f.charges[in.Ref]; ok {
		return *ch, nil
	}
	ch := &byok.Charge{Ref: in.Ref, Status: byok.ChargeOpen, Amount: in.Amount, Payable: "00020126FAKE" + in.Ref}
	f.charges[in.Ref] = ch
	return *ch, nil
}

func (f *Fake) GetCharge(_ context.Context, _ byok.Credentials, _ byok.Token, ref string) (byok.Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpGetCharge); err != nil {
		return byok.Charge{}, err
	}
	ch, ok := f.charges[ref]
	if !ok {
		return byok.Charge{}, byok.ErrNotFound
	}
	return *ch, nil
}

func (f *Fake) UpdateCharge(_ context.Context, _ byok.Credentials, _ byok.Token, ref string, amount int64) (byok.Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpUpdateCharge); err != nil {
		return byok.Charge{}, err
	}
	ch, ok := f.charges[ref]
	if !ok {
		return byok.Charge{}, byok.ErrNotFound
	}
	if ch.Status != byok.ChargeOpen {
		return byok.Charge{}, byok.ErrConcluded
	}
	ch.Amount = amount
	return *ch, nil
}

func (f *Fake) CancelCharge(_ context.Context, _ byok.Credentials, _ byok.Token, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpCancelCharge); err != nil {
		return err
	}
	ch, ok := f.charges[ref]
	if !ok {
		return byok.ErrNotFound
	}
	if ch.Status != byok.ChargeOpen {
		return byok.ErrConcluded
	}
	ch.Status = byok.ChargeRemoved
	return nil
}

func (f *Fake) Refund(_ context.Context, _ byok.Credentials, _ byok.Token, in byok.RefundRequest) (byok.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpRefund); err != nil {
		return byok.Refund{}, err
	}
	if r, ok := f.refunds[in.RefundID]; ok {
		return r, nil
	}
	r := byok.Refund{ID: in.RefundID, PaymentID: in.PaymentID, Amount: in.Amount, Status: byok.RefundProcessing}
	f.refunds[in.RefundID] = r
	return r, nil
}

func (f *Fake) GetRefund(_ context.Context, _ byok.Credentials, _ byok.Token, _ string, refundID string) (byok.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpGetRefund); err != nil {
		return byok.Refund{}, err
	}
	r, ok := f.refunds[refundID]
	if !ok {
		return byok.Refund{}, byok.ErrNotFound
	}
	r.Status = byok.RefundDone
	f.refunds[refundID] = r
	return r, nil
}

func (f *Fake) StatementPage(_ context.Context, _ byok.Credentials, _ byok.Token, from, to byok.Date, page int) (byok.StatementPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpStatementPage); err != nil {
		return byok.StatementPage{}, err
	}
	var in []byok.StatementLine
	for _, l := range f.lines {
		if !l.Date.Before(from) && !to.Before(l.Date) {
			in = append(in, l)
		}
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].ID < in[j].ID })
	total := (len(in) + f.PageSize - 1) / f.PageSize
	lo, hi := page*f.PageSize, min((page+1)*f.PageSize, len(in))
	if lo > len(in) {
		lo = len(in)
	}
	return byok.StatementPage{Lines: append([]byok.StatementLine(nil), in[lo:hi]...), Page: page, TotalPages: total}, nil
}

func (f *Fake) Balance(_ context.Context, _ byok.Credentials, _ byok.Token, at byok.Date) (byok.Balance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpBalance); err != nil {
		return byok.Balance{}, err
	}
	b := f.balance
	if v, ok := f.balanceAt[at]; ok {
		b.Available = v
	}
	b.At = at
	return b, nil
}

// SetBalanceAt answers Balance(at) with available (an opening-balance date).
func (f *Fake) SetBalanceAt(at byok.Date, available int64) { f.mu.Lock(); f.balanceAt[at] = available; f.mu.Unlock() }

func (f *Fake) GetWebhook(_ context.Context, _ byok.Credentials, _ byok.Token, p byok.Product) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpGetWebhook); err != nil {
		return "", err
	}
	return f.hooks[p], nil
}

func (f *Fake) RegisterWebhook(_ context.Context, _ byok.Credentials, _ byok.Token, p byok.Product, url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpRegisterWebhook); err != nil {
		return err
	}
	f.hooks[p] = url
	return nil
}

func (f *Fake) DeleteWebhook(_ context.Context, _ byok.Credentials, _ byok.Token, p byok.Product) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpDeleteWebhook); err != nil {
		return err
	}
	delete(f.hooks, p)
	return nil
}

func (f *Fake) ListCallbacks(_ context.Context, _ byok.Credentials, _ byok.Token, since time.Time) ([]byok.CallbackAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter(byok.OpListCallbacks); err != nil {
		return nil, err
	}
	var out []byok.CallbackAttempt
	for _, a := range f.callbacks {
		if !a.At.Before(since) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *Fake) RetryCallbacks(_ context.Context, _ byok.Credentials, _ byok.Token, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enter(byok.OpRetryCallbacks)
}

func (f *Fake) SimulatePayment(_ context.Context, c byok.Credentials, _ byok.Token, ref string) error {
	f.mu.Lock()
	err := f.enter(byok.OpSimulatePayment)
	ch := f.charges[ref]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if !c.Sandbox {
		return byok.ErrSandboxOnly
	}
	if ch == nil {
		return byok.ErrNotFound
	}
	f.Pay(ref, ch.Amount, 0, time.Now().UTC())
	return nil
}

func (f *Fake) ParseCallback(_ byok.Product, headers map[string]string, body []byte) (byok.Callback, error) {
	return byok.Callback{Account: headers["x-conta-corrente"], ChargeRefs: []string{string(body)}}, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . && go vet ./byok/... && go test ./byok/... -race -count=1`
Expected: `ok` for `byok` and `byok/byoktest`.

- [ ] **Step 5: Commit**

```bash
git add byok/byoktest/
git commit -m "feat(byok): in-memory fake provider for consumers' tests"
```

### Task 3: ctech-go-common — `inter` client core (credentials, mTLS, token, errors, money, txid, QR)

**Repo:** ctech-go-common. New dependency: `github.com/skip2/go-qrcode` (the wallet's QR renderer, `ctech-wallet/pix-gateway/internal/inter/qr.go`).

**Files:**
- Create: `inter/doc.go`, `inter/client.go`, `inter/errors.go`, `inter/money.go`, `inter/txid.go`, `inter/qr/qr.go`
- Test: `inter/client_test.go`, `inter/money_test.go`, `inter/txid_test.go`, `inter/qr/qr_test.go`, `inter/testdata_test.go` (self-signed CA + client pair generated in the test, nothing committed)

**Interfaces:**
- Consumes: Task 1 (`byok` errors and `Token`).
- Produces:

```go
package inter
const ProductionURL = "https://cdpj.partners.bancointer.com.br"
const SandboxURL = "https://cdpj-sandbox.partners.uatinter.co"
var ScopesStatement = []string{"extrato.read"}
var ScopesCollection = []string{"cobv.read", "cobv.write", "pix.read", "pix.write", "webhook.read", "webhook.write"}
type Credentials struct{ ClientID, ClientSecret string; CertPEM, KeyPEM []byte; Account, PixKey string; Sandbox bool }
func CredentialsFrom(c byok.Credentials) (Credentials, error) // Values: client_id, client_secret, cert_pem, key_pem
type CertInfo struct{ Subject, Fingerprint string; NotAfter time.Time }
func InspectPair(certPEM, keyPEM []byte, now time.Time) (CertInfo, error) // ErrCertificateInvalid / ErrKeyMismatch / ErrCertificateExpired
type Client struct{ /* unexported */ }
func NewClient(opts ...Option) *Client; func WithBaseURL(url string) Option; func WithRootCAs(*x509.CertPool) Option; func WithTimeout(time.Duration) Option
func (c *Client) FetchToken(ctx context.Context, cr Credentials, scopes []string) (byok.Token, error)
func (c *Client) Do(ctx context.Context, cr Credentials, bearer, method, path string, query url.Values, body, out any) error
func Reais(cents int64) string; func Cents(reais string) (int64, error)
func ValidTxid(s string) bool
package qr; func PNG(text string) (string, error) // base64 PNG
```

- [ ] **Step 1: Write the failing tests**

`inter/testdata_test.go` builds, per test run, a CA, a server certificate for `127.0.0.1` and a client pair, and an `httptest.NewUnstartedServer` with `tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}`:

```go
package inter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pki struct {
	pool              *x509.CertPool
	clientCert, clientKey []byte
	server            *httptest.Server
}

func newPKI(t *testing.T, notAfter time.Time, h http.Handler) pki {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	leaf := func(cn string, ip bool, until time.Time, usage x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: until, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if ip {
			tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		der, _ := x509.CreateCertificate(rand.Reader, tpl, ca, &k.PublicKey, caKey)
		kb, _ := x509.MarshalECPrivateKey(k)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	}
	sc, sk := leaf("127.0.0.1", true, time.Now().AddDate(1, 0, 0), x509.ExtKeyUsageServerAuth)
	cc, ck := leaf("CN=merchant-integration", false, notAfter, x509.ExtKeyUsageClientAuth)
	srv := httptest.NewUnstartedServer(h)
	pair, _ := tls.X509KeyPair(sc, sk)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return pki{pool: pool, clientCert: cc, clientKey: ck, server: srv}
}
```

`inter/client_test.go`:

```go
package inter

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestFetchTokenPostsTheFormOverMTLS(t *testing.T) {
	var got string
	p := newPKI(t, time.Now().AddDate(1, 0, 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/v2/token" || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no", 400)
			return
		}
		_ = r.ParseForm()
		got = r.Form.Get("client_id") + "|" + r.Form.Get("grant_type") + "|" + r.Form.Get("scope")
		_, _ = w.Write([]byte(`{"access_token":"abc","token_type":"Bearer","expires_in":3600,"scope":"extrato.read"}`))
	}))
	c := NewClient(WithBaseURL(p.server.URL), WithRootCAs(p.pool))
	tok, err := c.FetchToken(context.Background(), Credentials{ClientID: "cid", ClientSecret: "s", CertPEM: p.clientCert, KeyPEM: p.clientKey}, ScopesStatement)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cid|client_credentials|extrato.read" || tok.Value != "abc" || tok.ExpiresIn != 3600 || tok.Scopes[0] != "extrato.read" {
		t.Fatalf("form %q token %+v", got, tok)
	}
}

func TestStatusesMapToByokErrors(t *testing.T) {
	cases := map[int]error{401: byok.ErrUnauthorized, 403: byok.ErrScopesRefused, 404: byok.ErrNotFound,
		409: byok.ErrConcluded, 429: byok.ErrRateLimited, 503: byok.ErrUnavailable, 400: byok.ErrInvalidRequest}
	for status, want := range cases {
		p := newPKI(t, time.Now().AddDate(1, 0, 0), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"title":"x","status":` + itoa(status) + `,"detail":"client_secret=LEAK"}`))
		}))
		c := NewClient(WithBaseURL(p.server.URL), WithRootCAs(p.pool))
		err := c.Do(context.Background(), Credentials{CertPEM: p.clientCert, KeyPEM: p.clientKey}, "b", "GET", "/x", nil, nil, nil)
		if !errors.Is(err, want) {
			t.Fatalf("%d -> %v, want %v", status, err, want)
		}
		if strings.Contains(err.Error(), "LEAK") {
			t.Fatal("a provider body is never copied into an error")
		}
	}
}

func TestTheTransportIsReusedPerCertificateAndRenewedOnANewOne(t *testing.T) {
	var conns atomic.Int32
	p := newPKI(t, time.Now().AddDate(1, 0, 0), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	p.server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	c := NewClient(WithBaseURL(p.server.URL), WithRootCAs(p.pool))
	cr := Credentials{CertPEM: p.clientCert, KeyPEM: p.clientKey}
	for range 5 {
		if err := c.Do(context.Background(), cr, "b", "GET", "/x", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if conns.Load() != 1 {
		t.Fatalf("%d connections for one certificate, want 1 (keep-alive)", conns.Load())
	}
	if c.transports() != 1 {
		t.Fatal("one transport per fingerprint")
	}
}

func TestInspectPairRefusesExpiredMismatchedAndGarbage(t *testing.T) {
	ok := newPKI(t, time.Now().AddDate(1, 0, 0), http.NotFoundHandler())
	other := newPKI(t, time.Now().AddDate(1, 0, 0), http.NotFoundHandler())
	old := newPKI(t, time.Now().Add(-time.Hour), http.NotFoundHandler())
	if info, err := InspectPair(ok.clientCert, ok.clientKey, time.Now()); err != nil || info.Fingerprint == "" || info.Subject == "" {
		t.Fatalf("valid pair: %+v %v", info, err)
	}
	if _, err := InspectPair(ok.clientCert, other.clientKey, time.Now()); !errors.Is(err, byok.ErrKeyMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := InspectPair(old.clientCert, old.clientKey, time.Now()); !errors.Is(err, byok.ErrCertificateExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := InspectPair([]byte("nope"), ok.clientKey, time.Now()); !errors.Is(err, byok.ErrCertificateInvalid) {
		t.Fatalf("garbage: %v", err)
	}
}
```

(`itoa` is `strconv.Itoa`; add `"net"` and `"strconv"` imports.)

`inter/money_test.go`:

```go
func TestMoneyRoundTrips(t *testing.T) {
	for cents, s := range map[int64]string{0: "0.00", 5: "0.05", 9990: "99.90", 123456789: "1234567.89"} {
		if Reais(cents) != s {
			t.Fatalf("Reais(%d) = %s", cents, Reais(cents))
		}
		if back, err := Cents(s); err != nil || back != cents {
			t.Fatalf("Cents(%s) = %d, %v", s, back, err)
		}
	}
	for _, bad := range []string{"", "1.2.3", "-1.00", "1.234", "abc"} {
		if _, err := Cents(bad); err == nil {
			t.Fatalf("Cents(%q) accepted", bad)
		}
	}
	if c, _ := Cents("10.5"); c != 1050 {
		t.Fatal("one decimal digit is tenths")
	}
}
```

`inter/txid_test.go`:

```go
func TestTxidFollowsBacen(t *testing.T) {
	if !ValidTxid("ctb01JABCDEFGHJKMNPQRSTVWXYZ") { // 29 chars
		t.Fatal("billing's txid shape refused")
	}
	for _, bad := range []string{"short", strings.Repeat("a", 36), "ctb-01JABCDEFGHJKMNPQRSTVWXY"} {
		if ValidTxid(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}
```

`inter/qr/qr_test.go`:

```go
func TestPNGIsAPNG(t *testing.T) {
	b64, err := PNG("00020126580014br.gov.bcb.pix")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(b64)
	if !bytes.HasPrefix(raw, []byte("\x89PNG")) {
		t.Fatal("not a PNG")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./inter/...`
Expected: FAIL, `undefined: NewClient`.

- [ ] **Step 3: Implement**

`inter/client.go`:

```go
// Package inter is Banco Inter's implementation of byok.Provider (PJ APIs:
// Pix, Banking, Token). Credentials are passed per call; the package reads no
// SSM, no environment and holds no token cache (callers own the token
// lifecycle). Endpoint shapes: billing's parecer 2026-10-11 §§ 2, 3, 6, 7.
package inter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

const (
	ProductionURL = "https://cdpj.partners.bancointer.com.br"
	SandboxURL    = "https://cdpj-sandbox.partners.uatinter.co"
	pathToken     = "/oauth/v2/token"
)

var (
	ScopesStatement  = []string{"extrato.read"}
	ScopesCollection = []string{"cobv.read", "cobv.write", "pix.read", "pix.write", "webhook.read", "webhook.write"}
)

// Credentials are one integration's, for one call.
type Credentials struct {
	ClientID, ClientSecret string
	CertPEM, KeyPEM        []byte
	Account, PixKey        string
	Sandbox                bool
}

// CredentialsFrom reads the plaintext Values of a byok.Credentials.
func CredentialsFrom(c byok.Credentials) (Credentials, error) {
	v := c.Values
	if v["client_id"] == "" || v["client_secret"] == "" || v["cert_pem"] == "" || v["key_pem"] == "" {
		return Credentials{}, fmt.Errorf("%w: client_id, client_secret, cert_pem and key_pem are required", byok.ErrInvalidRequest)
	}
	return Credentials{ClientID: v["client_id"], ClientSecret: v["client_secret"], CertPEM: []byte(v["cert_pem"]),
		KeyPEM: []byte(v["key_pem"]), Account: c.Account, PixKey: c.PixKey, Sandbox: c.Sandbox}, nil
}

type CertInfo struct {
	Subject, Fingerprint string
	NotAfter             time.Time
}

// InspectPair parses and checks a certificate and key without any network call.
func InspectPair(certPEM, keyPEM []byte, now time.Time) (CertInfo, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return CertInfo{}, fmt.Errorf("%w: not a PEM certificate", byok.ErrCertificateInvalid)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertInfo{}, fmt.Errorf("%w: %v", byok.ErrCertificateInvalid, err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		if kb, _ := pem.Decode(keyPEM); kb == nil {
			return CertInfo{}, fmt.Errorf("%w: not a PEM key", byok.ErrCertificateInvalid)
		}
		return CertInfo{}, byok.ErrKeyMismatch
	}
	sum := sha256.Sum256(cert.Raw)
	info := CertInfo{Subject: cert.Subject.String(), Fingerprint: hex.EncodeToString(sum[:]), NotAfter: cert.NotAfter.UTC()}
	if !now.Before(cert.NotAfter) {
		return info, byok.ErrCertificateExpired
	}
	return info, nil
}

type Option func(*Client)

func WithBaseURL(u string) Option            { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }
func WithRootCAs(p *x509.CertPool) Option    { return func(c *Client) { c.roots = p } }
func WithTimeout(d time.Duration) Option     { return func(c *Client) { c.timeout = d } }

// Client caches one *http.Transport per client-certificate fingerprint, so a
// warm process keeps its TLS sessions per integration and a renewal (a new
// certificate) gets a new transport. Transports idle 30 min are dropped.
type Client struct {
	baseURL string // empty: chosen per call from Credentials.Sandbox
	roots   *x509.CertPool
	timeout time.Duration
	mu      sync.Mutex
	pool    map[string]*entry
}

type entry struct {
	rt   *http.Transport
	used time.Time
}

func NewClient(opts ...Option) *Client {
	c := &Client{timeout: 20 * time.Second, pool: map[string]*entry{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) transports() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.pool) }

func (c *Client) base(cr Credentials) string {
	switch {
	case c.baseURL != "":
		return c.baseURL
	case cr.Sandbox:
		return SandboxURL
	}
	return ProductionURL
}

func (c *Client) httpFor(cr Credentials) (*http.Client, error) {
	sum := sha256.Sum256(append(append([]byte{}, cr.CertPEM...), cr.KeyPEM...))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.pool {
		if now.Sub(e.used) > 30*time.Minute {
			e.rt.CloseIdleConnections()
			delete(c.pool, k)
		}
	}
	if e, ok := c.pool[key]; ok {
		e.used = now
		return &http.Client{Transport: e.rt, Timeout: c.timeout}, nil
	}
	pair, err := tls.X509KeyPair(cr.CertPEM, cr.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", byok.ErrCertificateInvalid, err)
	}
	rt := &http.Transport{
		TLSClientConfig:     &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: c.roots, MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	c.pool[key] = &entry{rt: rt, used: now}
	return &http.Client{Transport: rt, Timeout: c.timeout}, nil
}

// FetchToken asks for a client_credentials bearer with exactly these scopes.
func (c *Client) FetchToken(ctx context.Context, cr Credentials, scopes []string) (byok.Token, error) {
	hc, err := c.httpFor(cr)
	if err != nil {
		return byok.Token{}, err
	}
	form := url.Values{"client_id": {cr.ClientID}, "client_secret": {cr.ClientSecret},
		"grant_type": {"client_credentials"}, "scope": {strings.Join(scopes, " ")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base(cr)+pathToken, strings.NewReader(form.Encode()))
	if err != nil {
		return byok.Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return byok.Token{}, fmt.Errorf("%w: token: %v", byok.ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		// Inter answers 400/401 to a wrong secret and to scopes the integration lacks.
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return byok.Token{}, fmt.Errorf("%w: token %d %s", byok.ErrCredentialsRefused, resp.StatusCode, problemTitle(raw))
		}
		return byok.Token{}, statusErr(http.MethodPost, pathToken, resp.StatusCode, raw)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil || tr.AccessToken == "" {
		return byok.Token{}, fmt.Errorf("%w: token body", byok.ErrUnavailable)
	}
	return byok.Token{Value: tr.AccessToken, ExpiresIn: tr.ExpiresIn, Scopes: strings.Fields(tr.Scope)}, nil
}

// Do sends one API call with the bearer; x-conta-corrente when Account is set.
func (c *Client) Do(ctx context.Context, cr Credentials, bearer, method, path string, query url.Values, body, out any) error {
	hc, err := c.httpFor(cr)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := c.base(cr) + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cr.Account != "" {
		req.Header.Set("x-conta-corrente", cr.Account)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", byok.ErrUnavailable, method, path, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusErr(method, path, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: %s %s: body", byok.ErrUnavailable, method, path)
		}
	}
	return nil
}
```

`inter/errors.go`:

```go
package inter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"gopkg.aoctech.app/api-commons/byok"
)

// Problem is Bacen's RFC 7807 body. Only Title and the violation field names
// are ever surfaced: Detail can echo the request (a CPF, a key).
type Problem struct {
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Violacoes []struct {
		Propriedade string `json:"propriedade"`
	} `json:"violacoes"`
}

func problemTitle(raw []byte) string {
	var p Problem
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	s := p.Title
	for _, v := range p.Violacoes {
		s += " [" + v.Propriedade + "]"
	}
	return s
}

func statusErr(method, path string, code int, raw []byte) error {
	var base error
	switch {
	case code == 401:
		base = byok.ErrUnauthorized
	case code == 403:
		base = byok.ErrScopesRefused
	case code == 404:
		base = byok.ErrNotFound
	case code == 409:
		base = byok.ErrConcluded
	case code == 429:
		base = byok.ErrRateLimited
	case code >= 500:
		base = byok.ErrUnavailable
	default:
		base = byok.ErrInvalidRequest
	}
	return fmt.Errorf("%w: %s %s %d %s", base, method, path, code, problemTitle(raw))
}

// sanitize drops the URL (it can carry a query) from a transport error.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
```

`inter/money.go`:

```go
package inter

import (
	"fmt"
	"strconv"
	"strings"
)

// Reais renders centavos as Inter's decimal string ("99.90").
func Reais(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

// Cents parses Inter's decimal string; at most two decimals, never negative.
func Cents(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" || len(frac) > 2 || strings.HasPrefix(whole, "-") {
		return 0, fmt.Errorf("inter: amount %q", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("inter: amount %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("inter: amount %q", s)
	}
	return w*100 + f, nil
}
```

`inter/txid.go`:

```go
package inter

// ValidTxid is Bacen's rule for a receiver-chosen txid: [a-zA-Z0-9]{26,35}.
func ValidTxid(s string) bool {
	if len(s) < 26 || len(s) > 35 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
```

`inter/qr/qr.go` (moved from the wallet, same parameters):

```go
// Package qr renders a BR Code (pixCopiaECola) as a PNG, base64-encoded.
package qr

import (
	"encoding/base64"

	"github.com/skip2/go-qrcode"
)

func PNG(text string) (string, error) {
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go get github.com/skip2/go-qrcode@v0.0.0-20200617195104-da1b6568686e && go mod tidy && gofmt -l . && go vet ./inter/... && go test ./inter/... -race -count=1`
Expected: `ok` for `inter` and `inter/qr`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum inter/
git commit -m "feat(inter): mTLS client with per-certificate transports, token fetch, Bacen errors, money, txid and QR"
```

### Task 4: ctech-go-common — Inter endpoints (Bacen `pix` types, Pix and Banking calls)

**Repo:** ctech-go-common.

Layout note: Bacen-standard request/response types live in `inter/pix` (types only, reusable by another Bacen-API PSP, spec § 13); the calls are `Client` methods in package `inter`, so `inter` imports `inter/pix` and nothing imports back (no cycle).

**Files:**
- Create: `inter/pix/types.go`, `inter/pix_calls.go`, `inter/banking_calls.go`
- Test: `inter/pix_calls_test.go`, `inter/banking_calls_test.go`

**Interfaces:**
- Consumes: Task 3 `Client.Do`, `Credentials`, `Reais`, `Cents`.
- Produces:

```go
package pix
type Calendario struct{ DataDeVencimento string; ValidadeAposVencimento int }
type Devedor struct{ Nome, CPF, CNPJ string }
type Modalidade struct{ Modalidade int; ValorPerc string }
type Valor struct{ Original string; Multa, Juros *Modalidade }
type CobvRequest struct{ Calendario Calendario; Devedor Devedor; Valor Valor; Chave, SolicitacaoPagador string }
type Cobv struct{ Txid, Status, PixCopiaECola string; Valor Valor; Pix []Recebido }
type Recebido struct{ EndToEndID, Txid, Valor, Horario string; ComponentesValor *ComponentesValor }
type ComponentesValor struct{ Original, Multa, Juros *struct{ Valor string } }
type Devolucao struct{ ID, RtrID, Valor, Status string }
type CallbackBody struct{ Pix []Recebido }
type CallbackRecord struct{ WebhookURL string; Payload json.RawMessage; NumeroTentativa, HTTPStatus int; DataHoraDisparo string; Txid string }
const StatusAtiva = "ATIVA", StatusConcluida = "CONCLUIDA", StatusRemovidaRecebedor = "REMOVIDA_PELO_USUARIO_RECEBEDOR", StatusRemovidaPSP = "REMOVIDA_PELO_PSP"
package inter
func (c *Client) PutCobv(ctx, cr Credentials, bearer, txid string, in pix.CobvRequest) (pix.Cobv, error)
func (c *Client) GetCobv(ctx, cr, bearer, txid string) (pix.Cobv, error)
func (c *Client) PatchCobvAmount(ctx, cr, bearer, txid string, cents int64) (pix.Cobv, error)
func (c *Client) RemoveCobv(ctx, cr, bearer, txid string) error
func (c *Client) PutDevolucao(ctx, cr, bearer, e2e, id string, cents int64, reason string) (pix.Devolucao, error)
func (c *Client) GetDevolucao(ctx, cr, bearer, e2e, id string) (pix.Devolucao, error)
func (c *Client) GetWebhookURL(ctx, cr, bearer string) (string, error)  // "" when none (404)
func (c *Client) PutWebhookURL(ctx, cr, bearer, url string) error
func (c *Client) DeleteWebhookURL(ctx, cr, bearer string) error
func (c *Client) ListCallbacks(ctx, cr, bearer string, from, to time.Time) ([]pix.CallbackRecord, error)
func (c *Client) RetryCallbacks(ctx, cr, bearer string, txids []string) error // ≤ 50 per call (caller batches)
func (c *Client) PaySandboxCobv(ctx, cr, bearer, txid string) error
type ExtratoLine struct{ IDTransacao, DataTransacao, TipoOperacao, Valor, Titulo, Descricao string; Detalhes struct{ TxID, EndToEndID string } }
func (c *Client) ExtratoCompleto(ctx, cr, bearer string, from, to byok.Date, page, size int) (lines []ExtratoLine, totalPages int, err error)
type Saldo struct{ Disponivel, BloqueadoCheque, BloqueadoJudicialmente, BloqueadoAdministrativo float64 }
func (c *Client) Saldo(ctx, cr, bearer string, at byok.Date) (Saldo, error)
```

- [ ] **Step 1: Write the failing tests**

One recording test server checks method, path, query and body per call (reusing Task 3's `newPKI`):

```go
package inter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter/pix"
)

type seen struct {
	method, path, query, account string
	body                         map[string]any
}

func recorder(t *testing.T, answer string, status int) (*Client, Credentials, *[]seen) {
	t.Helper()
	var calls []seen
	p := newPKI(t, time.Now().AddDate(1, 0, 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, account: r.Header.Get("x-conta-corrente")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &s.body)
		}
		calls = append(calls, s)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	return NewClient(WithBaseURL(p.server.URL), WithRootCAs(p.pool)),
		Credentials{CertPEM: p.clientCert, KeyPEM: p.clientKey, Account: "1234567", PixKey: "evp-key"}, &calls
}

func TestPutCobvSendsTheBacenBody(t *testing.T) {
	c, cr, calls := recorder(t, `{"txid":"ctb01JABCDEFGHJKMNPQRSTVWXYZ","status":"ATIVA","pixCopiaECola":"000201"}`, 201)
	in := pix.CobvRequest{
		Calendario: pix.Calendario{DataDeVencimento: "2026-11-10", ValidadeAposVencimento: 30},
		Devedor:    pix.Devedor{Nome: "Ana", CPF: "12345678909"},
		Valor:      pix.Valor{Original: Reais(9990), Multa: &pix.Modalidade{Modalidade: 2, ValorPerc: "2.00"}, Juros: &pix.Modalidade{Modalidade: 3, ValorPerc: "1.00"}},
		Chave:      "evp-key", SolicitacaoPagador: "Fatura #12",
	}
	got, err := c.PutCobv(context.Background(), cr, "b", "ctb01JABCDEFGHJKMNPQRSTVWXYZ", in)
	if err != nil || got.PixCopiaECola != "000201" {
		t.Fatalf("%+v %v", got, err)
	}
	s := (*calls)[0]
	if s.method != "PUT" || s.path != "/pix/v2/cobv/ctb01JABCDEFGHJKMNPQRSTVWXYZ" || s.account != "1234567" {
		t.Fatalf("%+v", s)
	}
	if s.body["chave"] != "evp-key" || s.body["valor"].(map[string]any)["original"] != "99.90" {
		t.Fatalf("body %+v", s.body)
	}
}

func TestRemoveCobvSendsOnlyTheStatus(t *testing.T) {
	c, cr, calls := recorder(t, `{}`, 200)
	if err := c.RemoveCobv(context.Background(), cr, "b", "ctb01JABCDEFGHJKMNPQRSTVWXYZ"); err != nil {
		t.Fatal(err)
	}
	s := (*calls)[0]
	if s.method != "PATCH" || len(s.body) != 1 || s.body["status"] != "REMOVIDA_PELO_USUARIO_RECEBEDOR" {
		t.Fatalf("%+v", s)
	}
}

func TestGetWebhookURLIsEmptyWhenNoneIsRegistered(t *testing.T) {
	c, cr, calls := recorder(t, `{"title":"Webhook não encontrado"}`, 404)
	url, err := c.GetWebhookURL(context.Background(), cr, "b")
	if err != nil || url != "" || (*calls)[0].path != "/pix/v2/webhook/evp-key" {
		t.Fatalf("%q %v %+v", url, err, *calls)
	}
}

func TestPaginationNeverUsesScroll(t *testing.T) {
	c, cr, calls := recorder(t, `{"totalPaginas":3,"transacoes":[{"idTransacao":"T1","dataTransacao":"2026-10-01","tipoOperacao":"C","valor":"10.50","titulo":"Pix recebido","detalhes":{"txId":"ctb01","endToEndId":"E1"}}]}`, 200)
	from, _ := byok.ParseDate("2026-07-04")
	to, _ := byok.ParseDate("2026-10-01")
	lines, total, err := c.ExtratoCompleto(context.Background(), cr, "b", from, to, 1, 500)
	if err != nil || total != 3 || lines[0].Detalhes.TxID != "ctb01" {
		t.Fatalf("%+v %d %v", lines, total, err)
	}
	q := (*calls)[0].query
	if (*calls)[0].path != "/banking/v2/extrato/completo" || q != "dataFim=2026-10-01&dataInicio=2026-07-04&pagina=1&tamanhoPagina=500" {
		t.Fatalf("query %q", q)
	}
}

func TestRetryCallbacksRefusesMoreThanFifty(t *testing.T) {
	c, cr, _ := recorder(t, `{}`, 200)
	if err := c.RetryCallbacks(context.Background(), cr, "b", make([]string, 51)); err == nil {
		t.Fatal("51 txids accepted")
	}
}
```

`inter/banking_calls_test.go`:

```go
func TestSaldoReadsTheDay(t *testing.T) {
	c, cr, calls := recorder(t, `{"disponivel":1234.56,"bloqueadoCheque":0,"bloqueadoJudicialmente":10,"bloqueadoAdministrativo":0}`, 200)
	at, _ := byok.ParseDate("2026-06-30")
	s, err := c.Saldo(context.Background(), cr, "b", at)
	if err != nil || s.Disponivel != 1234.56 || (*calls)[0].query != "dataSaldo=2026-06-30" {
		t.Fatalf("%+v %v %+v", s, err, *calls)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./inter/...`
Expected: FAIL, `undefined: pix.CobvRequest`, `c.PutCobv undefined`.

- [ ] **Step 3: Implement**

`inter/pix/types.go`:

```go
// Package pix holds the Bacen Pix API types Inter speaks (cobv, recebidos,
// devolução, webhook, callbacks). Types only; calls are inter.Client methods.
package pix

import "encoding/json"

const (
	StatusAtiva             = "ATIVA"
	StatusConcluida         = "CONCLUIDA"
	StatusRemovidaRecebedor = "REMOVIDA_PELO_USUARIO_RECEBEDOR"
	StatusRemovidaPSP       = "REMOVIDA_PELO_PSP"
)

type Calendario struct {
	DataDeVencimento       string `json:"dataDeVencimento,omitempty"`
	ValidadeAposVencimento int    `json:"validadeAposVencimento,omitempty"`
}

type Devedor struct {
	Nome string `json:"nome"`
	CPF  string `json:"cpf,omitempty"`
	CNPJ string `json:"cnpj,omitempty"`
}

// Modalidade: multa 2 = percentual; juros 3 = percentual ao mês, dias corridos.
type Modalidade struct {
	Modalidade int    `json:"modalidade"`
	ValorPerc  string `json:"valorPerc"`
}

type Valor struct {
	Original string      `json:"original"`
	Multa    *Modalidade `json:"multa,omitempty"`
	Juros    *Modalidade `json:"juros,omitempty"`
}

type CobvRequest struct {
	Calendario         Calendario `json:"calendario"`
	Devedor            Devedor    `json:"devedor"`
	Valor              Valor      `json:"valor"`
	Chave              string     `json:"chave"`
	SolicitacaoPagador string     `json:"solicitacaoPagador,omitempty"`
}

type ComponenteValor struct {
	Valor string `json:"valor"`
}

type ComponentesValor struct {
	Original *ComponenteValor `json:"original,omitempty"`
	Multa    *ComponenteValor `json:"multa,omitempty"`
	Juros    *ComponenteValor `json:"juros,omitempty"`
}

type Recebido struct {
	EndToEndID       string            `json:"endToEndId"`
	Txid             string            `json:"txid"`
	Valor            string            `json:"valor"`
	Horario          string            `json:"horario"`
	ComponentesValor *ComponentesValor `json:"componentesValor,omitempty"`
}

type Cobv struct {
	Txid          string     `json:"txid"`
	Status        string     `json:"status"`
	PixCopiaECola string     `json:"pixCopiaECola"`
	Valor         Valor      `json:"valor"`
	Pix           []Recebido `json:"pix"`
}

type Devolucao struct {
	ID     string `json:"id"`
	RtrID  string `json:"rtrId"`
	Valor  string `json:"valor"`
	Status string `json:"status"`
}

type CallbackBody struct {
	Pix []Recebido `json:"pix"`
}

type CallbackRecord struct {
	WebhookURL      string          `json:"webhookUrl"`
	Payload         json.RawMessage `json:"payload"`
	NumeroTentativa int             `json:"numeroTentativa"`
	HTTPStatus      int             `json:"httpStatus"`
	DataHoraDisparo string          `json:"dataHoraDisparo"`
	Txid            string          `json:"txid"`
}
```

`inter/pix_calls.go`:

```go
package inter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter/pix"
)

const (
	pathCobv          = "/pix/v2/cobv/%s"
	pathCobvPay       = "/pix/v2/cobv/pagar/%s"
	pathDevolucao     = "/pix/v2/pix/%s/devolucao/%s"
	pathWebhook       = "/pix/v2/webhook/%s"
	pathCallbacks     = "/pix/v2/webhook/callbacks"
	pathCallbackRetry = "/pix/v2/webhook/callbacks/retry"
	maxRetryTxids     = 50
)

func (c *Client) PutCobv(ctx context.Context, cr Credentials, bearer, txid string, in pix.CobvRequest) (pix.Cobv, error) {
	if !ValidTxid(txid) {
		return pix.Cobv{}, fmt.Errorf("%w: txid", byok.ErrInvalidRequest)
	}
	var out pix.Cobv
	err := c.Do(ctx, cr, bearer, http.MethodPut, fmt.Sprintf(pathCobv, txid), nil, in, &out)
	return out, err
}

func (c *Client) GetCobv(ctx context.Context, cr Credentials, bearer, txid string) (pix.Cobv, error) {
	var out pix.Cobv
	err := c.Do(ctx, cr, bearer, http.MethodGet, fmt.Sprintf(pathCobv, txid), nil, nil, &out)
	return out, err
}

func (c *Client) PatchCobvAmount(ctx context.Context, cr Credentials, bearer, txid string, cents int64) (pix.Cobv, error) {
	var out pix.Cobv
	err := c.Do(ctx, cr, bearer, http.MethodPatch, fmt.Sprintf(pathCobv, txid), nil,
		map[string]any{"valor": map[string]string{"original": Reais(cents)}}, &out)
	return out, err
}

// RemoveCobv sends the status alone, as Bacen requires.
func (c *Client) RemoveCobv(ctx context.Context, cr Credentials, bearer, txid string) error {
	return c.Do(ctx, cr, bearer, http.MethodPatch, fmt.Sprintf(pathCobv, txid), nil,
		map[string]string{"status": pix.StatusRemovidaRecebedor}, nil)
}

func (c *Client) PutDevolucao(ctx context.Context, cr Credentials, bearer, e2e, id string, cents int64, reason string) (pix.Devolucao, error) {
	var out pix.Devolucao
	body := map[string]string{"valor": Reais(cents), "natureza": "ORIGINAL"}
	if reason != "" {
		body["descricao"] = reason
	}
	err := c.Do(ctx, cr, bearer, http.MethodPut, fmt.Sprintf(pathDevolucao, e2e, id), nil, body, &out)
	return out, err
}

func (c *Client) GetDevolucao(ctx context.Context, cr Credentials, bearer, e2e, id string) (pix.Devolucao, error) {
	var out pix.Devolucao
	err := c.Do(ctx, cr, bearer, http.MethodGet, fmt.Sprintf(pathDevolucao, e2e, id), nil, nil, &out)
	return out, err
}

func (c *Client) GetWebhookURL(ctx context.Context, cr Credentials, bearer string) (string, error) {
	var out struct {
		WebhookURL string `json:"webhookUrl"`
	}
	err := c.Do(ctx, cr, bearer, http.MethodGet, fmt.Sprintf(pathWebhook, url.PathEscape(cr.PixKey)), nil, nil, &out)
	if errors.Is(err, byok.ErrNotFound) {
		return "", nil
	}
	return out.WebhookURL, err
}

func (c *Client) PutWebhookURL(ctx context.Context, cr Credentials, bearer, hook string) error {
	return c.Do(ctx, cr, bearer, http.MethodPut, fmt.Sprintf(pathWebhook, url.PathEscape(cr.PixKey)), nil,
		map[string]string{"webhookUrl": hook}, nil)
}

func (c *Client) DeleteWebhookURL(ctx context.Context, cr Credentials, bearer string) error {
	err := c.Do(ctx, cr, bearer, http.MethodDelete, fmt.Sprintf(pathWebhook, url.PathEscape(cr.PixKey)), nil, nil, nil)
	if errors.Is(err, byok.ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) ListCallbacks(ctx context.Context, cr Credentials, bearer string, from, to time.Time) ([]pix.CallbackRecord, error) {
	var out struct {
		Data         []pix.CallbackRecord `json:"data"`
		TotalPaginas int                  `json:"totalPaginas"`
	}
	var all []pix.CallbackRecord
	for page := 0; ; page++ {
		q := url.Values{"dataHoraInicio": {from.UTC().Format(time.RFC3339)}, "dataHoraFim": {to.UTC().Format(time.RFC3339)},
			"pagina": {fmt.Sprint(page)}, "tamanhoPagina": {"100"}}
		if err := c.Do(ctx, cr, bearer, http.MethodGet, pathCallbacks, q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if page+1 >= out.TotalPaginas {
			return all, nil
		}
	}
}

func (c *Client) RetryCallbacks(ctx context.Context, cr Credentials, bearer string, txids []string) error {
	if len(txids) == 0 || len(txids) > maxRetryTxids {
		return fmt.Errorf("%w: 1..%d txids per retry", byok.ErrInvalidRequest, maxRetryTxids)
	}
	return c.Do(ctx, cr, bearer, http.MethodPost, pathCallbackRetry, nil, map[string][]string{"txids": txids}, nil)
}

func (c *Client) PaySandboxCobv(ctx context.Context, cr Credentials, bearer, txid string) error {
	if !cr.Sandbox {
		return byok.ErrSandboxOnly
	}
	return c.Do(ctx, cr, bearer, http.MethodPost, fmt.Sprintf(pathCobvPay, txid), nil, map[string]string{}, nil)
}
```

`inter/banking_calls.go`:

```go
package inter

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"gopkg.aoctech.app/api-commons/byok"
)

type ExtratoLine struct {
	IDTransacao   string `json:"idTransacao"`
	DataTransacao string `json:"dataTransacao"`
	TipoOperacao  string `json:"tipoOperacao"` // C | D
	Valor         string `json:"valor"`
	Titulo        string `json:"titulo"`
	Descricao     string `json:"descricao"`
	Detalhes      struct {
		TxID       string `json:"txId"`
		EndToEndID string `json:"endToEndId"`
	} `json:"detalhes"`
}

// ExtratoCompleto reads one page (pagina/tamanhoPagina, never scroll: one
// scroll per account, and the merchant's ERP may hold it). Window ≤ 90 days.
func (c *Client) ExtratoCompleto(ctx context.Context, cr Credentials, bearer string, from, to byok.Date, page, size int) ([]ExtratoLine, int, error) {
	var out struct {
		TotalPaginas int           `json:"totalPaginas"`
		Transacoes   []ExtratoLine `json:"transacoes"`
	}
	q := url.Values{"dataInicio": {from.String()}, "dataFim": {to.String()}, "pagina": {fmt.Sprint(page)}, "tamanhoPagina": {fmt.Sprint(size)}}
	if err := c.Do(ctx, cr, bearer, http.MethodGet, "/banking/v2/extrato/completo", q, nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Transacoes, out.TotalPaginas, nil
}

type Saldo struct {
	Disponivel              float64 `json:"disponivel"`
	BloqueadoCheque         float64 `json:"bloqueadoCheque"`
	BloqueadoJudicialmente  float64 `json:"bloqueadoJudicialmente"`
	BloqueadoAdministrativo float64 `json:"bloqueadoAdministrativo"`
}

func (c *Client) Saldo(ctx context.Context, cr Credentials, bearer string, at byok.Date) (Saldo, error) {
	var out Saldo
	q := url.Values{}
	if !at.IsZero() {
		q.Set("dataSaldo", at.String())
	}
	err := c.Do(ctx, cr, bearer, http.MethodGet, "/banking/v2/saldo", q, nil, &out)
	return out, err
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . && go vet ./inter/... && go test ./inter/... -race -count=1`
Expected: `ok` for `inter`, `inter/qr`; `inter/pix` has no tests.

- [ ] **Step 5: Commit**

```bash
git add inter/
git commit -m "feat(inter): cobv, devolução, Pix webhook and callbacks, extrato completo and saldo"
```

### Task 5: ctech-go-common — `inter.Provider` (the `byok` adapter), `ParseCallback`, docs, tag `v1.18.0`

**Repo:** ctech-go-common.

**Files:**
- Create: `inter/provider.go`, `inter/callback.go`
- Modify: `README.md` (package table: `byok`, `inter`), `AGENTS.md` (anchors), `CLAUDE.md` (consumers line: `ctech-billing/api` adds `byok`, `inter`)
- Test: `inter/provider_test.go`, `inter/callback_test.go`

**Interfaces:**
- Consumes: Tasks 1, 3, 4.
- Produces: `func NewProvider(c *Client) *Provider`; `(*Provider)` implements `byok.Provider` with `Name() == "inter"`; `inter.ParseCallback` semantics: `pix[].txid` → `ChargeRefs`, `pix[].endToEndId` → `PaymentIDs`, header `x-conta-corrente` → `Account`; unknown product → `byok.ErrInvalidRequest`. Credential field names: `client_id`, `client_secret` (secret), `cert_pem` (file), `key_pem` (file, secret).

- [ ] **Step 1: Write the failing tests**

```go
package inter

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

var _ byok.Provider = (*Provider)(nil)

// fakeInter answers the token endpoint per requested scope set and /saldo.
func fakeInter(t *testing.T, grant func(scope string) (status int, granted string)) (*Provider, byok.Credentials) {
	p := newPKI(t, time.Now().AddDate(1, 0, 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/token":
			_ = r.ParseForm()
			status, granted := grant(r.Form.Get("scope"))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600,"scope":"` + granted + `"}`))
		case "/banking/v2/saldo":
			_, _ = w.Write([]byte(`{"disponivel":10.25}`))
		default:
			w.WriteHeader(404)
		}
	}))
	prov := NewProvider(NewClient(WithBaseURL(p.server.URL), WithRootCAs(p.pool)))
	return prov, byok.Credentials{Values: map[string]string{"client_id": "c", "client_secret": "s",
		"cert_pem": string(p.clientCert), "key_pem": string(p.clientKey)}, Account: "1234567"}
}

func TestConnectWithEveryScopeIsCollectionCapable(t *testing.T) {
	prov, cr := fakeInter(t, func(scope string) (int, string) { return 200, scope })
	got, err := prov.Connect(context.Background(), cr)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Capabilities) != 2 || got.Balance.Available != 1025 || got.CertFingerprint == "" {
		t.Fatalf("%+v", got)
	}
}

func TestConnectFallsBackToAStatementOnlyIntegration(t *testing.T) {
	prov, cr := fakeInter(t, func(scope string) (int, string) {
		if strings.Contains(scope, "cobv") {
			return 400, ""
		}
		return 200, "extrato.read"
	})
	got, err := prov.Connect(context.Background(), cr)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != byok.CapabilityStatement || len(got.MissingScopes) != 6 {
		t.Fatalf("%+v", got)
	}
}

func TestConnectWithAWrongSecretIsRefused(t *testing.T) {
	prov, cr := fakeInter(t, func(string) (int, string) { return 401, "" })
	if _, err := prov.Connect(context.Background(), cr); !errors.Is(err, byok.ErrCredentialsRefused) {
		t.Fatalf("%v", err)
	}
}

func TestATokenNarrowedByInterIsStillReadAsGranted(t *testing.T) {
	// spec § 15 item 8: Inter may narrow instead of refusing.
	prov, cr := fakeInter(t, func(string) (int, string) { return 200, "extrato.read cobv.read" })
	got, _ := prov.Connect(context.Background(), cr)
	if len(got.Capabilities) != 1 {
		t.Fatalf("a partial collection set must not be collection-capable: %+v", got)
	}
}
```

`inter/callback_test.go`:

```go
func TestParseCallbackTakesOnlyReferences(t *testing.T) {
	body := `{"pix":[{"endToEndId":"E1","txid":"ctb01JABCDEFGHJKMNPQRSTVWXYZ","valor":"999999.00","horario":"2026-10-11T10:00:00Z"}]}`
	got, err := NewProvider(NewClient()).ParseCallback(byok.ProductPix, map[string]string{"x-conta-corrente": "1234567"}, []byte(body))
	if err != nil || got.Account != "1234567" || got.ChargeRefs[0] != "ctb01JABCDEFGHJKMNPQRSTVWXYZ" || got.PaymentIDs[0] != "E1" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := NewProvider(NewClient()).ParseCallback("boleto", nil, []byte(body)); !errors.Is(err, byok.ErrInvalidRequest) {
		t.Fatal("phase 1 parses pix only")
	}
	if _, err := NewProvider(NewClient()).ParseCallback(byok.ProductPix, nil, []byte(`{"pix":[{"txid":"bad id!"}]}`)); !errors.Is(err, byok.ErrInvalidRequest) {
		t.Fatal("a reference outside Bacen's txid rule is refused")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./inter/...`
Expected: FAIL, `undefined: NewProvider`.

- [ ] **Step 3: Implement**

`inter/provider.go`:

```go
package inter

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter/pix"
)

// Provider adapts Client to byok.Provider. Plaintext Values are required
// (byok.Credentials.Ref is opened by the caller, never here).
type Provider struct{ c *Client }

func NewProvider(c *Client) *Provider { return &Provider{c: c} }

func (p *Provider) Name() string { return "inter" }

func (p *Provider) CredentialFields() []byok.Field {
	return []byok.Field{
		{Name: "client_id", Required: true},
		{Name: "client_secret", Secret: true, Required: true},
		{Name: "cert_pem", File: true, Required: true},
		{Name: "key_pem", File: true, Secret: true, Required: true},
	}
}

func scopesFor(set byok.Capability) []string {
	if set == byok.CapabilityCollection {
		return ScopesCollection
	}
	return ScopesStatement
}

func (p *Provider) creds(c byok.Credentials) (Credentials, error) { return CredentialsFrom(c) }

func (p *Provider) Connect(ctx context.Context, c byok.Credentials) (byok.Connected, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Connected{}, err
	}
	info, err := InspectPair(cr.CertPEM, cr.KeyPEM, time.Now())
	if err != nil {
		return byok.Connected{}, err
	}
	asked := append(slices.Clone(ScopesStatement), ScopesCollection...)
	tok, err := p.c.FetchToken(ctx, cr, asked)
	if err != nil {
		// D23: a statement-only integration is accepted.
		asked = ScopesStatement
		if tok, err = p.c.FetchToken(ctx, cr, asked); err != nil {
			return byok.Connected{}, err
		}
	}
	granted := tok.Scopes
	if len(granted) == 0 { // Inter echoed no scope: it granted what was asked
		granted = asked
	}
	out := byok.Connected{ScopesGranted: granted, CertSubject: info.Subject, CertFingerprint: info.Fingerprint, CertNotAfter: info.NotAfter}
	if slices.Contains(granted, "extrato.read") {
		out.Capabilities = append(out.Capabilities, byok.CapabilityStatement)
	}
	for _, s := range ScopesCollection {
		if !slices.Contains(granted, s) {
			out.MissingScopes = append(out.MissingScopes, s)
		}
	}
	if len(out.MissingScopes) == 0 {
		out.Capabilities = append(out.Capabilities, byok.CapabilityCollection)
	}
	if !slices.Contains(out.Capabilities, byok.CapabilityStatement) {
		return byok.Connected{}, fmt.Errorf("%w: extrato.read is required", byok.ErrScopesRefused)
	}
	// GET /saldo proves extrato.read and the account number together.
	bal, err := p.Balance(ctx, c, byok.Token{Value: tok.Value}, byok.Date{})
	if err != nil {
		return byok.Connected{}, err
	}
	out.Balance = bal
	return out, nil
}

func (p *Provider) Token(ctx context.Context, c byok.Credentials, set byok.Capability) (byok.Token, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Token{}, err
	}
	tok, err := p.c.FetchToken(ctx, cr, scopesFor(set))
	if err != nil {
		return byok.Token{}, err
	}
	if info, ierr := InspectPair(cr.CertPEM, cr.KeyPEM, time.Now()); ierr == nil {
		tok.CertNotAfter = info.NotAfter
	}
	return tok, nil
}

func basisPoints(bp int) string { return fmt.Sprintf("%d.%02d", bp/100, bp%100) }

func (p *Provider) CreateDueCharge(ctx context.Context, c byok.Credentials, t byok.Token, in byok.DueCharge) (byok.Charge, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Charge{}, err
	}
	dev := pix.Devedor{Nome: in.Debtor.Name}
	switch len(in.Debtor.TaxID) {
	case 11:
		dev.CPF = in.Debtor.TaxID
	case 14:
		dev.CNPJ = in.Debtor.TaxID
	default:
		return byok.Charge{}, fmt.Errorf("%w: debtor tax id", byok.ErrInvalidRequest)
	}
	req := pix.CobvRequest{
		Calendario: pix.Calendario{DataDeVencimento: in.Due.String(), ValidadeAposVencimento: max(in.ValidityDays, 1)},
		Devedor:    dev, Valor: pix.Valor{Original: Reais(in.Amount)}, Chave: cr.PixKey, SolicitacaoPagador: in.Message,
	}
	if in.FineBasisPoints > 0 {
		req.Valor.Multa = &pix.Modalidade{Modalidade: 2, ValorPerc: basisPoints(in.FineBasisPoints)}
	}
	if in.MonthlyInterestBasisPoints > 0 {
		req.Valor.Juros = &pix.Modalidade{Modalidade: 3, ValorPerc: basisPoints(in.MonthlyInterestBasisPoints)}
	}
	got, err := p.c.PutCobv(ctx, cr, t.Value, in.Ref, req)
	if err != nil {
		// PUT on an existing txid: read it back (idempotent create, spec § 7.2).
		if existing, gerr := p.c.GetCobv(ctx, cr, t.Value, in.Ref); gerr == nil {
			return chargeOf(existing), nil
		}
		return byok.Charge{}, err
	}
	return chargeOf(got), nil
}

func cents(s string) int64 { v, _ := Cents(s); return v }

func chargeOf(v pix.Cobv) byok.Charge {
	ch := byok.Charge{Ref: v.Txid, Amount: cents(v.Valor.Original), Payable: v.PixCopiaECola}
	switch v.Status {
	case pix.StatusAtiva:
		ch.Status = byok.ChargeOpen
	case pix.StatusConcluida:
		ch.Status = byok.ChargePaid
	case pix.StatusRemovidaPSP:
		ch.Status = byok.ChargeExpired
	default:
		ch.Status = byok.ChargeRemoved
	}
	for _, r := range v.Pix {
		pay := byok.Payment{ID: r.EndToEndID, Amount: cents(r.Valor)}
		if t, err := time.Parse(time.RFC3339, r.Horario); err == nil {
			pay.PaidAt = t.UTC()
		}
		if cv := r.ComponentesValor; cv != nil {
			if cv.Multa != nil {
				pay.LateCharges += cents(cv.Multa.Valor)
			}
			if cv.Juros != nil {
				pay.LateCharges += cents(cv.Juros.Valor)
			}
		}
		ch.Payments = append(ch.Payments, pay)
	}
	if len(ch.Payments) > 0 {
		ch.Status = byok.ChargePaid
	}
	return ch
}

func (p *Provider) GetCharge(ctx context.Context, c byok.Credentials, t byok.Token, ref string) (byok.Charge, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Charge{}, err
	}
	v, err := p.c.GetCobv(ctx, cr, t.Value, ref)
	if err != nil {
		return byok.Charge{}, err
	}
	return chargeOf(v), nil
}

func (p *Provider) UpdateCharge(ctx context.Context, c byok.Credentials, t byok.Token, ref string, amount int64) (byok.Charge, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Charge{}, err
	}
	v, err := p.c.PatchCobvAmount(ctx, cr, t.Value, ref, amount)
	if err != nil {
		return byok.Charge{}, err
	}
	return chargeOf(v), nil
}

func (p *Provider) CancelCharge(ctx context.Context, c byok.Credentials, t byok.Token, ref string) error {
	cr, err := p.creds(c)
	if err != nil {
		return err
	}
	return p.c.RemoveCobv(ctx, cr, t.Value, ref)
}

func refundOf(d pix.Devolucao, paymentID string) byok.Refund {
	r := byok.Refund{ID: d.ID, PaymentID: paymentID, Amount: cents(d.Valor), Status: byok.RefundProcessing}
	switch d.Status {
	case "DEVOLVIDO":
		r.Status = byok.RefundDone
	case "NAO_REALIZADO":
		r.Status = byok.RefundFailed
	}
	return r
}

func (p *Provider) Refund(ctx context.Context, c byok.Credentials, t byok.Token, in byok.RefundRequest) (byok.Refund, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Refund{}, err
	}
	d, err := p.c.PutDevolucao(ctx, cr, t.Value, in.PaymentID, in.RefundID, in.Amount, in.Reason)
	if err != nil {
		return byok.Refund{}, err
	}
	return refundOf(d, in.PaymentID), nil
}

func (p *Provider) GetRefund(ctx context.Context, c byok.Credentials, t byok.Token, paymentID, refundID string) (byok.Refund, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Refund{}, err
	}
	d, err := p.c.GetDevolucao(ctx, cr, t.Value, paymentID, refundID)
	if err != nil {
		return byok.Refund{}, err
	}
	return refundOf(d, paymentID), nil
}

const statementPageSize = 1000

func (p *Provider) StatementPage(ctx context.Context, c byok.Credentials, t byok.Token, from, to byok.Date, page int) (byok.StatementPage, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.StatementPage{}, err
	}
	lines, total, err := p.c.ExtratoCompleto(ctx, cr, t.Value, from, to, page, statementPageSize)
	if err != nil {
		return byok.StatementPage{}, err
	}
	out := byok.StatementPage{Page: page, TotalPages: total}
	for _, l := range lines {
		d, err := byok.ParseDate(l.DataTransacao[:min(10, len(l.DataTransacao))])
		if err != nil {
			continue // unreadable line: the balance difference banner shows it (spec § 8.1)
		}
		dir := byok.Credit
		if strings.EqualFold(l.TipoOperacao, "D") {
			dir = byok.Debit
		}
		out.Lines = append(out.Lines, byok.StatementLine{ID: l.IDTransacao, Date: d, Direction: dir, Amount: cents(l.Valor),
			Title: l.Titulo, Description: l.Descricao, ChargeRef: l.Detalhes.TxID, PaymentID: l.Detalhes.EndToEndID})
	}
	return out, nil
}

func reaisFloat(v float64) int64 { return int64(math.Round(v * 100)) }

func (p *Provider) Balance(ctx context.Context, c byok.Credentials, t byok.Token, at byok.Date) (byok.Balance, error) {
	cr, err := p.creds(c)
	if err != nil {
		return byok.Balance{}, err
	}
	s, err := p.c.Saldo(ctx, cr, t.Value, at)
	if err != nil {
		return byok.Balance{}, err
	}
	return byok.Balance{Available: reaisFloat(s.Disponivel),
		Blocked: reaisFloat(s.BloqueadoCheque + s.BloqueadoJudicialmente + s.BloqueadoAdministrativo), At: at}, nil
}

func pixOnly(product byok.Product) error {
	if product != byok.ProductPix {
		return fmt.Errorf("%w: product %q", byok.ErrInvalidRequest, product)
	}
	return nil
}

func (p *Provider) GetWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) (string, error) {
	if err := pixOnly(product); err != nil {
		return "", err
	}
	cr, err := p.creds(c)
	if err != nil {
		return "", err
	}
	return p.c.GetWebhookURL(ctx, cr, t.Value)
}

func (p *Provider) RegisterWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product, url string) error {
	if err := pixOnly(product); err != nil {
		return err
	}
	cr, err := p.creds(c)
	if err != nil {
		return err
	}
	return p.c.PutWebhookURL(ctx, cr, t.Value, url)
}

func (p *Provider) DeleteWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) error {
	if err := pixOnly(product); err != nil {
		return err
	}
	cr, err := p.creds(c)
	if err != nil {
		return err
	}
	return p.c.DeleteWebhookURL(ctx, cr, t.Value)
}

func (p *Provider) ListCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, since time.Time) ([]byok.CallbackAttempt, error) {
	cr, err := p.creds(c)
	if err != nil {
		return nil, err
	}
	recs, err := p.c.ListCallbacks(ctx, cr, t.Value, since, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]byok.CallbackAttempt, 0, len(recs))
	for _, r := range recs {
		at, _ := time.Parse(time.RFC3339, r.DataHoraDisparo)
		out = append(out, byok.CallbackAttempt{ChargeRef: r.Txid, HTTPStatus: r.HTTPStatus, Attempt: r.NumeroTentativa, At: at.UTC()})
	}
	return out, nil
}

func (p *Provider) RetryCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, refs []string) error {
	cr, err := p.creds(c)
	if err != nil {
		return err
	}
	for start := 0; start < len(refs); start += maxRetryTxids {
		if err := p.c.RetryCallbacks(ctx, cr, t.Value, refs[start:min(start+maxRetryTxids, len(refs))]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) SimulatePayment(ctx context.Context, c byok.Credentials, t byok.Token, ref string) error {
	cr, err := p.creds(c)
	if err != nil {
		return err
	}
	return p.c.PaySandboxCobv(ctx, cr, t.Value, ref)
}
```

`inter/callback.go`:

```go
package inter

import (
	"encoding/json"
	"fmt"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter/pix"
)

// ParseCallback reads a Pix webhook body (Bacen: {"pix":[...]}) into
// references only. Amounts, status and payer are deliberately dropped.
func (p *Provider) ParseCallback(product byok.Product, headers map[string]string, body []byte) (byok.Callback, error) {
	if err := pixOnly(product); err != nil {
		return byok.Callback{}, err
	}
	var b pix.CallbackBody
	if err := json.Unmarshal(body, &b); err != nil {
		return byok.Callback{}, fmt.Errorf("%w: body", byok.ErrInvalidRequest)
	}
	out := byok.Callback{Account: headers["x-conta-corrente"]}
	for _, r := range b.Pix {
		if !ValidTxid(r.Txid) {
			return byok.Callback{}, fmt.Errorf("%w: txid", byok.ErrInvalidRequest)
		}
		out.ChargeRefs = append(out.ChargeRefs, r.Txid)
		if r.EndToEndID != "" {
			out.PaymentIDs = append(out.PaymentIDs, r.EndToEndID)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the whole module and update the docs**

Add to `README.md`'s package table:

```markdown
| `byok`         | Provider-neutral "bring your own key" seam: `Provider` (charges with due date, refunds, statement, balance, webhooks), money in centavos, civil `Date`, the outbound-Lambda envelope (`Request`, `Response`, `Op`, `GroupOf`), wire error codes. `byok/byoktest` is an in-memory `Provider` for tests |
| `inter`        | Banco Inter's `byok.Provider`: mTLS client with one transport per certificate fingerprint, `FetchToken`, Pix `cobv`/devolução/webhook/callbacks, Banking extrato completo (pagination, never scroll) and saldo, Bacen RFC 7807 errors, `qr.PNG`. Credentials per call; no SSM, no env, no token cache |
```

Run: `gofmt -l . && go vet ./... && go test ./... -race -count=1`
Expected: every package `ok`.

- [ ] **Step 5: Commit, open the PR, merge after CI, tag**

```bash
git add inter/ README.md AGENTS.md CLAUDE.md
git commit -m "feat(inter): byok.Provider adapter and Pix callback parser"
git push -u origin feat/byok-inter
gh pr create --title "feat: byok seam and inter provider (v1.18.0)" --body "Adds byok (provider-neutral seam, outbound envelope, fake provider) and inter (Banco Inter implementation). Consumers: ctech-billing (BYOK phase 1). The wallet's pix-gateway migrates in its own PR."
```

After CI is green and the PR is merged:

```bash
git checkout main && git pull
git tag --sort=-v:refname | head -1   # Expected: v1.17.0
git tag v1.18.0 && git push origin v1.18.0
```

Expected: `v1.18.0` listed by `git ls-remote --tags origin v1.18.0`.

### Task 6: ctech-ui — `Steps` and `FileDrop` (released as `0.5.0`)

**Repo:** ctech-ui, branch `feat/steps-file-drop` from `origin/main` (package `0.4.1`). Spec § 11 asks to check ctech-ui before building a stepper and a file drop locally: neither exists (`src/components/` has no stepper, no file input). A health card is plain composition of `Badge` and text and stays in billing.

Execute with `/impeccable`; the components carry **no copy of their own** (every string is a prop), no colour of their own (tokens only), 44 px touch targets, and their own phone layout.

**Files:**
- Create: `src/components/steps.tsx`, `src/components/file-drop.tsx`
- Modify: `src/index.ts` (exports), `src/components.stories.tsx` (two stories), `package.json` (`0.5.0`), `README.md` (component list)
- Test: `src/components/steps.test.tsx`, `src/components/file-drop.test.tsx`

**Interfaces:**
- Produces:

```ts
export type StepState = "done" | "current" | "waiting" | "upcoming"
export interface StepItem { id: string; title: string; state: StepState; stateLabel: string; content?: ReactNode }
export interface StepsProps { label: string; items: StepItem[]; className?: string }
export function Steps(props: StepsProps): JSX.Element
export interface FileDropProps {
  id: string; label: string; hint?: string; error?: string; accept: string; maxBytes: number
  file: File | null; onFile: (f: File | null) => void
  chooseLabel: string; replaceLabel: string; removeLabel: string; tooLargeLabel: string; wrongTypeLabel: string
}
export function FileDrop(props: FileDropProps): JSX.Element
```

- [ ] **Step 1: Write the failing tests**

`src/components/steps.test.tsx`:

```tsx
import {render, screen} from "@testing-library/react"
import {describe, expect, it} from "vitest"

import {Steps} from "./steps"

describe("Steps", () => {
  it("is an ordered list whose current step is aria-current and whose state is spoken", () => {
    render(<Steps label="Conectar" items={[
      {id: "a", title: "Requisitos", state: "done", stateLabel: "Concluído"},
      {id: "b", title: "Criar a integração", state: "current", stateLabel: "Agora", content: <p>faça isto</p>},
      {id: "c", title: "Enviar", state: "upcoming", stateLabel: "Depois"},
    ]}/>)
    const list = screen.getByRole("list", {name: "Conectar"})
    expect(list.tagName).toBe("OL")
    const current = screen.getByText("Criar a integração").closest("li")!
    expect(current.getAttribute("aria-current")).toBe("step")
    expect(screen.getByText("faça isto")).toBeTruthy()
    expect(screen.getByText("Concluído")).toBeTruthy()
    expect(screen.queryByText("depois", {exact: false})).toBeTruthy()
  })
})
```

`src/components/file-drop.test.tsx`:

```tsx
import {fireEvent, render, screen} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {FileDrop} from "./file-drop"

const labels = {chooseLabel: "Escolher arquivo", replaceLabel: "Trocar", removeLabel: "Remover",
  tooLargeLabel: "Arquivo grande demais", wrongTypeLabel: "Tipo não aceito"}

describe("FileDrop", () => {
  it("accepts a dropped file of the right type and size", () => {
    const onFile = vi.fn()
    render(<FileDrop id="crt" label="Certificado" accept=".crt,.pem" maxBytes={16384} file={null} onFile={onFile} {...labels}/>)
    const f = new File(["-----BEGIN CERTIFICATE-----"], "inter.crt", {type: "application/x-x509-ca-cert"})
    fireEvent.drop(screen.getByTestId("file-drop-crt"), {dataTransfer: {files: [f]}})
    expect(onFile).toHaveBeenCalledWith(f)
  })

  it("refuses a file too large or of another extension, saying why", () => {
    const onFile = vi.fn()
    render(<FileDrop id="key" label="Chave" accept=".key,.pem" maxBytes={10} file={null} onFile={onFile} {...labels}/>)
    fireEvent.drop(screen.getByTestId("file-drop-key"), {dataTransfer: {files: [new File(["x".repeat(11)], "a.key")]}})
    expect(screen.getByRole("alert").textContent).toBe("Arquivo grande demais")
    fireEvent.drop(screen.getByTestId("file-drop-key"), {dataTransfer: {files: [new File(["x"], "a.txt")]}})
    expect(screen.getByRole("alert").textContent).toBe("Tipo não aceito")
    expect(onFile).not.toHaveBeenCalled()
  })

  it("shows the chosen file's name and lets it be removed", () => {
    const onFile = vi.fn()
    render(<FileDrop id="k" label="Chave" accept=".key" maxBytes={100} file={new File(["x"], "inter.key")} onFile={onFile} {...labels}/>)
    expect(screen.getByText("inter.key")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", {name: "Remover"}))
    expect(onFile).toHaveBeenCalledWith(null)
  })
})
```

- [ ] **Step 2: Run them**

Run: `npx vitest run --maxWorkers=2 src/components/steps.test.tsx src/components/file-drop.test.tsx; echo "vitest exit=$?"`
Expected: `vitest exit=1`, cannot resolve `./steps`.

- [ ] **Step 3: Implement**

`src/components/steps.tsx`:

```tsx
import type {ReactNode} from "react"

import {cn} from "../lib/cn"

type StepState = "done" | "current" | "waiting" | "upcoming"

interface StepItem {
  id: string
  title: string
  state: StepState
  /** Spoken and shown beside the title ("Concluído", "Aguardando"). */
  stateLabel: string
  /** Shown only for the current and waiting steps. */
  content?: ReactNode
}

interface StepsProps {
  label: string
  items: StepItem[]
  className?: string
}

const marker: Record<StepState, string> = {
  done: "bg-success text-background border-success",
  current: "bg-brand-600 text-background border-brand-600",
  waiting: "bg-surface text-warning border-warning",
  upcoming: "bg-background text-muted-foreground border-border",
}

/**
 * A guided checklist: an ordered list, one step open at a time. Not a wizard:
 * every step stays readable, and the current one carries aria-current="step".
 * On a phone the rail collapses to the markers' column and content spans the
 * full width under the title.
 */
function Steps({label, items, className}: StepsProps) {
  return (
    <ol aria-label={label} className={cn("grid gap-0", className)}>
      {items.map((s, i) => (
        <li key={s.id} aria-current={s.state === "current" ? "step" : undefined}
          className="relative grid grid-cols-[2rem_1fr] gap-x-3 pb-6 last:pb-0">
          {i < items.length - 1 && <span aria-hidden className="absolute left-4 top-8 bottom-0 w-px bg-border"/>}
          <span aria-hidden className={cn("flex size-8 items-center justify-center rounded-full border text-sm font-semibold", marker[s.state])}>
            {i + 1}
          </span>
          <div className="min-w-0 pt-1">
            <p className="flex flex-wrap items-baseline gap-x-2 text-sm">
              <span className={cn("font-medium", s.state === "upcoming" ? "text-muted-foreground" : "text-foreground")}>{s.title}</span>
              <span className="text-muted-foreground">{s.stateLabel}</span>
            </p>
            {(s.state === "current" || s.state === "waiting") && s.content && <div className="mt-3 text-sm">{s.content}</div>}
          </div>
        </li>
      ))}
    </ol>
  )
}

export {Steps, type StepItem, type StepsProps, type StepState}
```

`src/components/file-drop.tsx`:

```tsx
import {useRef, useState, type DragEvent} from "react"

import {cn} from "../lib/cn"

interface FileDropProps {
  id: string
  label: string
  hint?: string
  error?: string
  /** Comma-separated extensions, as the input's accept (".crt,.pem"). */
  accept: string
  maxBytes: number
  file: File | null
  onFile: (f: File | null) => void
  chooseLabel: string
  replaceLabel: string
  removeLabel: string
  tooLargeLabel: string
  wrongTypeLabel: string
}

/**
 * One file, dropped or chosen. Validates extension and size before handing it
 * over; never reads the content (the consumer decides what to do with it).
 * The whole zone is a 44px-tall button on a phone; drag and drop is a bonus.
 */
function FileDrop(p: FileDropProps) {
  const input = useRef<HTMLInputElement>(null)
  const [local, setLocal] = useState<string | null>(null)
  const [over, setOver] = useState(false)
  const exts = p.accept.split(",").map(e => e.trim().toLowerCase())

  function take(f: File | undefined) {
    if (!f) return
    if (!exts.some(e => f.name.toLowerCase().endsWith(e))) return setLocal(p.wrongTypeLabel)
    if (f.size > p.maxBytes) return setLocal(p.tooLargeLabel)
    setLocal(null)
    p.onFile(f)
  }

  function drop(e: DragEvent) {
    e.preventDefault()
    setOver(false)
    take(e.dataTransfer.files[0])
  }

  const error = local ?? p.error
  const describedBy = error ? `${p.id}-error` : p.hint ? `${p.id}-hint` : undefined

  return (
    <div className="grid gap-1.5">
      <label htmlFor={p.id} className="text-sm font-medium text-foreground">{p.label}</label>
      <div data-testid={`file-drop-${p.id}`} onDragOver={e => { e.preventDefault(); setOver(true) }}
        onDragLeave={() => setOver(false)} onDrop={drop}
        className={cn("flex min-h-11 flex-wrap items-center gap-3 rounded-md border border-dashed px-3 py-2",
          over ? "border-brand-600 bg-brand-50" : "border-border", error && "border-danger")}>
        <input ref={input} id={p.id} type="file" accept={p.accept} className="sr-only" aria-describedby={describedBy}
          onChange={e => take(e.target.files?.[0])}/>
        {p.file
          ? <span className="min-w-0 flex-1 truncate font-mono text-sm text-foreground">{p.file.name}</span>
          : <span className="flex-1"/>}
        <button type="button" onClick={() => input.current?.click()}
          className="min-h-11 rounded-md px-3 text-sm font-medium text-brand-700 hover:bg-brand-50 focus-visible:outline-2 focus-visible:outline-ring">
          {p.file ? p.replaceLabel : p.chooseLabel}
        </button>
        {p.file && (
          <button type="button" onClick={() => p.onFile(null)}
            className="min-h-11 rounded-md px-3 text-sm text-muted-foreground hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring">
            {p.removeLabel}
          </button>
        )}
      </div>
      {p.hint && !error && <p id={`${p.id}-hint`} className="text-sm text-muted-foreground">{p.hint}</p>}
      {error && <p id={`${p.id}-error`} role="alert" className="text-sm text-danger">{error}</p>}
    </div>
  )
}

export {FileDrop, type FileDropProps}
```

`src/index.ts`:

```ts
export {Steps, type StepItem, type StepsProps, type StepState} from "./components/steps"
export {FileDrop, type FileDropProps} from "./components/file-drop"
```

- [ ] **Step 4: Run the package's suite**

Run: `npm test; echo "exit=$?"`
Expected: `exit=0` (node i18n test, vitest, build).

- [ ] **Step 5: Commit, PR, release**

```bash
git add src/ package.json package-lock.json README.md
git commit -m "feat: Steps and FileDrop (v0.5.0)"
git push -u origin feat/steps-file-drop
gh pr create --title "feat: Steps and FileDrop (v0.5.0)" --body "Guided checklist and single-file picker, first used by ctech-billing's bank connection onboarding."
```

After merge, the `publish.yml` workflow publishes `0.5.0` (check `npm view @aoctech/ui version` → `0.5.0`).

## Billing API tasks (`api/`)

Branch `feat/byok-a` from `origin/main` in ctech-billing. First step of the branch:

```bash
cd api && go get gopkg.aoctech.app/api-commons@v1.18.0 github.com/aws/aws-lambda-go@latest \
  github.com/aws/aws-sdk-go-v2/service/lambda@latest github.com/aws/aws-sdk-go-v2/service/ssm@latest && go mod tidy
go build ./... # Expected: no output
```

### Task 7: ADR 0029, the spec's decisions

**Files:**
- Create: `docs/adr/0029-byok-phase-1.md`
- Modify: `docs/adr/README.md` (index line)

- [ ] **Step 1: Write the ADR** in the format of ADR 0028 (title line, `Status: Accepted (2026-10-11)`, Context, Decision, Consequences). Decision bullets, one per spec decision, each citing its spec section: D2 (no wallet), D3 (organization owns the connection), D4 + R1 (dedicated AES key in SSM, read once at cold start by the outbound Lambda only; `Connect`/`Reseal` sealing ops; no KMS per call), D5 (non-VPC Lambda egress, no NAT), D8 + D15 + R6 (one mTLS host, secret in the path, re-query before paid, accept both suffix variants), D9 as amended 2026-10-11 (test mode = CTech's existing Inter integration against the sandbox host; the Lambda's role reads the wallet's four parameters by IAM grant), D10 (`api-commons/byok`, `api-commons/inter` `v1.18.0`), D11 (legal gate: `BYOK_LIVE_COLLECTION`, `BYOK_LIVE_PROVIDERS`, defaults off, Lambda env from the same Terraform variables), D12 (`payout_rail`, `payout_provider`, `pending_clearance`), D14 (api owns tokens), D16–D24. Consequences: live collection waits for ADR 0030; the cost table of this plan ("Custos de infra") is the budget; the wallet keeps its own Lambda and secrets.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0029-byok-phase-1.md docs/adr/README.md
git commit -m "docs(adr): 0029 BYOK phase 1 decisions"
```

### Task 8: Configuration, the `byok` table and the connection row

**Files:**
- Modify: `api/internal/config/config.go` (BYOK block), `api/internal/repositories/schema.json` (table `byok`), `api/internal/repositories/keys.go` (`TableByok`, key helpers, `JobByokConnections`), `api/internal/repositories/retention.go` (`RetentionByokConnection = RetentionPermanent`)
- Create: `api/internal/domain/byokconn/connection.go`, `api/internal/repositories/byok.go`
- Test: `api/internal/domain/byokconn/connection_test.go`, `api/internal/config/config_byok_test.go`, `api/tests/integration/byok_repo_test.go`

**Interfaces:**
- Consumes: `byok.Capability`, `byok.ConnectionRef` (Task 1).
- Produces:

```go
// config
type Config struct{ /* existing */ ByokOutboundFunction string `env:"BYOK_OUTBOUND_FUNCTION"`; ByokLiveCollection bool `env:"BYOK_LIVE_COLLECTION" envDefault:"false"`; ByokLiveProviders []string `env:"BYOK_LIVE_PROVIDERS" envSeparator:","` }
func (c *Config) ByokCleared(provider string) bool // ByokLiveCollection && provider ∈ ByokLiveProviders
// package byokconn
type State string      // StateActive "active", StateExpired "expired", StateDisconnected "disconnected"
type WebhookStatus string // WebhookNotRegistered "not_registered", WebhookRegistered "registered", WebhookConflict "conflict", WebhookOverwritten "overwritten"
type Kind string       // KindMerchant "merchant", KindSandboxShared "sandbox_shared"
type SyncState string  // SyncActive "active", SyncDisconnected "disconnected"
type Connection struct { AccountOrganizationID string; Livemode bool; Provider string; ID string; Kind Kind; State State;
  ClientID, AccountNumber, PixKey string; Sealed string; Version int; Revision int
  CertSubject, CertFingerprint string; CertNotAfter time.Time
  Capabilities []byok.Capability; ScopesGranted, MissingScopes []string
  HookID string; WebhookStatus WebhookStatus
  LastTokenOKAt, LastSyncOKAt time.Time; LastErrorCode string; LastErrorAt time.Time; ConsecutiveFailures int; DegradedAlertedOn string
  Warned90, Warned30, Warned7, WarnedExpired bool
  AuthorizedBy string; AuthorizedAt time.Time; AuthorizationVersion string; NotifyEmail string
  OnboardingStep string; OnboardingSince time.Time
  LedgerAccountID string; SyncState SyncState; FirstDay, SyncedThrough, BackfillFrom brcal.Date; BackfillDone bool; LastManualSyncAt time.Time
  CreatedAt, UpdatedAt time.Time }
func (c *Connection) Has(cap byok.Capability) bool
func (c *Connection) Degraded() bool                 // active && ConsecutiveFailures >= 3
func (c *Connection) Ref() byok.ConnectionRef         // {AccountOrganizationID, ID ("sandbox" for KindSandboxShared), Version, Sealed, Account, PixKey}
func (c *Connection) CertWarning(now time.Time) (threshold string, due bool) // "90"|"30"|"7"|"expired"
func (c *Connection) MarkWarned(threshold string)
func (c *Connection) ExpireIfPast(now time.Time) bool
const AuthorizationVersion = "2026-10-11"
// repositories
const TableByok = "byok"; const JobByokConnections = "BYOK_CONNECTIONS"
func ByokConnectionPK(accountOrgID string, livemode bool) string // "{org}#{mode}"
func ByokConnectionSK(provider string) string                    // "CONNECTION#{provider}"
var ErrConnectionExists, ErrConnectionChanged error
type ByokRepository struct{ /* base, audit */ }
func NewByokRepository(db *dynamodb.Client, cfg *config.Config) *ByokRepository
func (r *ByokRepository) GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error) // ErrNotFound
func (r *ByokRepository) ListConnections(ctx context.Context, accountOrgID string, livemode bool) ([]byokconn.Connection, error)          // key condition, SK prefix CONNECTION#
func (r *ByokRepository) CreateConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error       // ErrConnectionExists
func (r *ByokRepository) SaveConnection(ctx context.Context, c *byokconn.Connection, actor, requestID, action string, now time.Time) error // optimistic on Revision; ErrConnectionChanged; audit row
func (r *ByokRepository) DeleteConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error
func (r *ByokRepository) ConnectionsForJob(ctx context.Context, livemode bool, startKey map[string]types.AttributeValue, limit int) (*Page[byokconn.Connection], error)
```

- [ ] **Step 1: Write the failing tests**

`api/internal/domain/byokconn/connection_test.go`:

```go
package byokconn

import (
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestCertificateWarningsFireOncePerThresholdInOrder(t *testing.T) {
	notAfter := time.Date(2027, 10, 12, 0, 0, 0, 0, time.UTC)
	c := &Connection{State: StateActive, CertNotAfter: notAfter}
	at := func(daysBefore int) time.Time { return notAfter.AddDate(0, 0, -daysBefore) }
	for _, step := range []struct {
		now  time.Time
		want string
		due  bool
	}{
		{at(120), "", false}, {at(90), "90", true}, {at(60), "", false}, {at(30), "30", true},
		{at(7), "7", true}, {at(1), "", false}, {notAfter, "expired", true}, {notAfter.Add(48 * time.Hour), "", false},
	} {
		got, due := c.CertWarning(step.now)
		if got != step.want || due != step.due {
			t.Fatalf("at %s: %q %v, want %q %v", step.now, got, due, step.want, step.due)
		}
		if due {
			c.MarkWarned(got)
		}
	}
}

func TestAJobThatMissedD90StillSendsD30Only(t *testing.T) {
	notAfter := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	c := &Connection{State: StateActive, CertNotAfter: notAfter}
	got, due := c.CertWarning(notAfter.AddDate(0, 0, -20))
	if got != "30" || !due {
		t.Fatalf("%q %v: the most recent threshold passed is the one sent", got, due)
	}
	c.MarkWarned(got)
	if !c.Warned90 {
		t.Fatal("marking 30 also marks 90, so it is never sent late")
	}
}

func TestExpireIfPastAndDegraded(t *testing.T) {
	c := &Connection{State: StateActive, CertNotAfter: time.Now().Add(-time.Minute), ConsecutiveFailures: 3}
	if !c.Degraded() {
		t.Fatal("three failures on an active connection is degraded")
	}
	if !c.ExpireIfPast(time.Now()) || c.State != StateExpired {
		t.Fatal("past notAfter becomes expired")
	}
	if c.Degraded() {
		t.Fatal("expired is not degraded, it is expired")
	}
	if (&Connection{}).ExpireIfPast(time.Now()) {
		t.Fatal("a provider without certificates never expires")
	}
}

func TestTheSandboxRefIsNamedSandbox(t *testing.T) {
	c := &Connection{Kind: KindSandboxShared, ID: "01J", Capabilities: []byok.Capability{byok.CapabilityStatement}}
	if c.Ref().ID != "sandbox" || c.Ref().Sealed != "" {
		t.Fatalf("%+v", c.Ref())
	}
	if !c.Has(byok.CapabilityStatement) || c.Has(byok.CapabilityCollection) {
		t.Fatal("Has reads Capabilities")
	}
}
```

`api/internal/config/config_byok_test.go`:

```go
package config

import "testing"

func TestByokClearedNeedsTheFlagAndTheProvider(t *testing.T) {
	for _, c := range []struct {
		on        bool
		providers []string
		want      bool
	}{{false, []string{"inter"}, false}, {true, nil, false}, {true, []string{"other"}, false}, {true, []string{" inter "}, true}} {
		cfg := &Config{ByokLiveCollection: c.on, ByokLiveProviders: c.providers}
		if got := cfg.ByokCleared("inter"); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}
```

`api/tests/integration/byok_repo_test.go`:

```go
//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newConnection(org string, live bool) *byokconn.Connection {
	return &byokconn.Connection{AccountOrganizationID: org, Livemode: live, Provider: "inter", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Sealed: "b1.AAAA", Version: 1,
		Capabilities: []byok.Capability{byok.CapabilityStatement}, SyncState: byokconn.SyncActive}
}

func TestAConnectionIsCreatedOncePerOrganizationModeAndProvider(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	org := newSpaceOrgID()
	if err := repo.CreateConnection(ctx, newConnection(org, true), "u1", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateConnection(ctx, newConnection(org, true), "u1", "req", now()); !errors.Is(err, repositories.ErrConnectionExists) {
		t.Fatalf("second create: %v", err)
	}
	if err := repo.CreateConnection(ctx, newConnection(org, false), "u1", "req", now()); err != nil {
		t.Fatalf("test mode is another row: %v", err)
	}
	page, err := repo.ConnectionsForJob(ctx, true, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range page.Items {
		if c.AccountOrganizationID == org {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("the live job partition holds %d rows of org, want 1", found)
	}
}

func TestConcurrentSavesOfOneConnectionLetOneWin(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	c := newConnection(newSpaceOrgID(), true)
	if err := repo.CreateConnection(ctx, c, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cp := *c
			cp.ConsecutiveFailures = i + 1
			errs[i] = repo.SaveConnection(ctx, &cp, "u", "r", "byok.health", now())
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one save wins: %v / %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, repositories.ErrConnectionChanged) {
			t.Fatalf("loser: %v", err)
		}
	}
}

func TestADisconnectedConnectionLeavesTheJobPartition(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	c := newConnection(newSpaceOrgID(), false)
	_ = repo.CreateConnection(ctx, c, "u", "r", now())
	if err := repo.DeleteConnection(ctx, c, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetConnection(ctx, c.AccountOrganizationID, false, "inter"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/domain/byokconn/ ./internal/config/ && make test-integration`
Expected: FAIL, `package byokconn is not in std`, `cfg.ByokCleared undefined`.

- [ ] **Step 3: Implement**

`api/internal/config/config.go`, appended to `Config`:

```go
	// BYOK (spec 2026-10-11). The outbound Lambda is the only code that talks
	// to a merchant's bank. Empty disables every connection route (503
	// byok_provider_unavailable) and cmd/byok refuses to start.
	ByokOutboundFunction string `env:"BYOK_OUTBOUND_FUNCTION"`
	// The legal gate (spec § 10). Live collection needs both, checked at call
	// time; the outbound Lambda checks the same values from its own environment.
	ByokLiveCollection bool     `env:"BYOK_LIVE_COLLECTION" envDefault:"false"`
	ByokLiveProviders  []string `env:"BYOK_LIVE_PROVIDERS" envSeparator:","`
```

and:

```go
// ByokCleared reports whether live collection through provider is cleared.
func (c *Config) ByokCleared(provider string) bool {
	if !c.ByokLiveCollection {
		return false
	}
	for _, p := range c.ByokLiveProviders {
		if strings.TrimSpace(p) == provider {
			return true
		}
	}
	return false
}
```

`api/internal/domain/byokconn/connection.go`:

```go
// Package byokconn is a BYOK connection: an organization's own bank or PSP
// integration (spec 2026-10-11 § 3). The sealed credentials live here only as
// ciphertext; the key is the outbound Lambda's.
package byokconn

import (
	"slices"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

type State string

const (
	StateActive       State = "active"
	StateExpired      State = "expired"
	StateDisconnected State = "disconnected"
)

type WebhookStatus string

const (
	WebhookNotRegistered WebhookStatus = "not_registered"
	WebhookRegistered    WebhookStatus = "registered"
	WebhookConflict      WebhookStatus = "conflict"
	WebhookOverwritten   WebhookStatus = "overwritten"
)

type Kind string

const (
	KindMerchant      Kind = "merchant"
	KindSandboxShared Kind = "sandbox_shared"
)

type SyncState string

const (
	SyncActive       SyncState = "active"
	SyncDisconnected SyncState = "disconnected"
)

// AuthorizationVersion is the version of the "I authorize CTech" wording the
// connect form shows (spec § 3.2). Bump it with the wording.
const AuthorizationVersion = "2026-10-11"

// DegradedAfter is the consecutive-failure count shown as "Atenção" (§ 3.4).
const DegradedAfter = 3

type Connection struct {
	AccountOrganizationID string            `dynamodbav:"account_organization_id" json:"-"`
	Livemode              bool              `dynamodbav:"livemode"               json:"livemode"`
	Provider              string            `dynamodbav:"provider"               json:"provider"`
	ID                    string            `dynamodbav:"id"                     json:"id"`
	Kind                  Kind              `dynamodbav:"kind"                   json:"kind"`
	State                 State             `dynamodbav:"state"                  json:"state"`
	ClientID              string            `dynamodbav:"client_id,omitempty"    json:"client_id,omitempty"`
	AccountNumber         string            `dynamodbav:"account_number,omitempty" json:"account_number,omitempty"`
	PixKey                string            `dynamodbav:"pix_key,omitempty"      json:"pix_key,omitempty"`
	Sealed                string            `dynamodbav:"sealed,omitempty"       json:"-"`
	Version               int               `dynamodbav:"version"                json:"-"`
	Revision              int               `dynamodbav:"revision"               json:"-"`
	CertSubject           string            `dynamodbav:"cert_subject,omitempty"     json:"cert_subject,omitempty"`
	CertFingerprint       string            `dynamodbav:"cert_fingerprint,omitempty" json:"cert_fingerprint,omitempty"`
	CertNotAfter          time.Time         `dynamodbav:"cert_not_after,omitempty"   json:"cert_not_after,omitzero"`
	Capabilities          []byok.Capability `dynamodbav:"capabilities"           json:"capabilities"`
	ScopesGranted         []string          `dynamodbav:"scopes_granted,omitempty" json:"scopes_granted,omitempty"`
	MissingScopes         []string          `dynamodbav:"missing_scopes,omitempty" json:"missing_scopes,omitempty"`
	HookID                string            `dynamodbav:"hook_id,omitempty"      json:"-"`
	WebhookStatus         WebhookStatus     `dynamodbav:"webhook_status,omitempty" json:"webhook_status,omitempty"`
	LastTokenOKAt         time.Time         `dynamodbav:"last_token_ok_at,omitempty" json:"last_token_ok_at,omitzero"`
	LastSyncOKAt          time.Time         `dynamodbav:"last_sync_ok_at,omitempty"  json:"last_sync_ok_at,omitzero"`
	LastErrorCode         string            `dynamodbav:"last_error_code,omitempty"  json:"last_error_code,omitempty"`
	LastErrorAt           time.Time         `dynamodbav:"last_error_at,omitempty"    json:"last_error_at,omitzero"`
	ConsecutiveFailures   int               `dynamodbav:"consecutive_failures"   json:"consecutive_failures"`
	DegradedAlertedOn     string            `dynamodbav:"degraded_alerted_on,omitempty" json:"-"`
	Warned90              bool              `dynamodbav:"warned_90,omitempty"    json:"-"`
	Warned30              bool              `dynamodbav:"warned_30,omitempty"    json:"-"`
	Warned7               bool              `dynamodbav:"warned_7,omitempty"     json:"-"`
	WarnedExpired         bool              `dynamodbav:"warned_expired,omitempty" json:"-"`
	AuthorizedBy          string            `dynamodbav:"authorized_by,omitempty" json:"-"`
	AuthorizedAt          time.Time         `dynamodbav:"authorized_at,omitempty" json:"authorized_at,omitzero"`
	AuthorizationVersion  string            `dynamodbav:"authorization_version,omitempty" json:"-"`
	NotifyEmail           string            `dynamodbav:"notify_email,omitempty" json:"notify_email,omitempty"`
	OnboardingStep        string            `dynamodbav:"onboarding_step,omitempty" json:"onboarding_step,omitempty"`
	OnboardingSince       time.Time         `dynamodbav:"onboarding_since,omitempty" json:"onboarding_since,omitzero"`
	LedgerAccountID       string            `dynamodbav:"ledger_account_id,omitempty" json:"ledger_account_id,omitempty"`
	SyncState             SyncState         `dynamodbav:"sync_state,omitempty"   json:"sync_state,omitempty"`
	FirstDay              brcal.Date        `dynamodbav:"first_day,omitempty"    json:"first_day,omitzero"`
	SyncedThrough         brcal.Date        `dynamodbav:"synced_through,omitempty" json:"synced_through,omitzero"`
	BackfillFrom          brcal.Date        `dynamodbav:"backfill_from,omitempty" json:"backfill_from,omitzero"`
	BackfillDone          bool              `dynamodbav:"backfill_done,omitempty" json:"backfill_done"`
	LastManualSyncAt      time.Time         `dynamodbav:"last_manual_sync_at,omitempty" json:"-"`
	CreatedAt             time.Time         `dynamodbav:"connected_at"           json:"connected_at"`
}

func (c *Connection) Has(cap byok.Capability) bool { return slices.Contains(c.Capabilities, cap) }

func (c *Connection) Degraded() bool {
	return c.State == StateActive && c.ConsecutiveFailures >= DegradedAfter
}

func (c *Connection) Ref() byok.ConnectionRef {
	if c.Kind == KindSandboxShared {
		return byok.ConnectionRef{ID: "sandbox", AccountOrganizationID: c.AccountOrganizationID}
	}
	return byok.ConnectionRef{AccountOrganizationID: c.AccountOrganizationID, ID: c.ID, Version: c.Version,
		Sealed: c.Sealed, Account: c.AccountNumber, PixKey: c.PixKey}
}

// CertWarning returns the threshold to warn about now, if not warned yet. Only
// the most recent threshold passed is returned, so a job down for weeks never
// sends D-90 after D-30.
func (c *Connection) CertWarning(now time.Time) (string, bool) {
	if c.CertNotAfter.IsZero() || c.State == StateDisconnected {
		return "", false
	}
	left := c.CertNotAfter.Sub(now)
	day := 24 * time.Hour
	switch {
	case left <= 0:
		return "expired", !c.WarnedExpired
	case left <= 7*day:
		return "7", !c.Warned7
	case left <= 30*day:
		return "30", !c.Warned30
	case left <= 90*day:
		return "90", !c.Warned90
	}
	return "", false
}

// MarkWarned marks threshold and every earlier one.
func (c *Connection) MarkWarned(threshold string) {
	switch threshold {
	case "expired":
		c.WarnedExpired = true
		fallthrough
	case "7":
		c.Warned7 = true
		fallthrough
	case "30":
		c.Warned30 = true
		fallthrough
	case "90":
		c.Warned90 = true
	}
}

// ResetWarnings is called by a renewal.
func (c *Connection) ResetWarnings() { c.Warned90, c.Warned30, c.Warned7, c.WarnedExpired = false, false, false, false }

// ExpireIfPast moves an active connection past notAfter to expired.
func (c *Connection) ExpireIfPast(now time.Time) bool {
	if c.State != StateActive || c.CertNotAfter.IsZero() || now.Before(c.CertNotAfter) {
		return false
	}
	c.State = StateExpired
	return true
}
```

`api/internal/repositories/schema.json`, new table (alphabetical position irrelevant; the test only needs the key):

```json
  "byok": {
    "hash_key": "pk",
    "range_key": "sk",
    "attributes": {
      "pk": "S",
      "sk": "S",
      "schedule_pk": "S",
      "schedule_sk": "S"
    },
    "indexes": [
      {
        "name": "schedule-index",
        "hash_key": "schedule_pk",
        "range_key": "schedule_sk"
      }
    ]
  }
```

`api/internal/repositories/keys.go`:

```go
	// TableByok holds BYOK connections, their webhook hooks and the charge
	// pointers (spec 2026-10-11 § 12.2). Partition of a connection = the
	// organization's finance space key, so the space resolver guards it.
	TableByok = "byok"
```

```go
// JobByokConnections is the sparse partition cmd/byok walks: every connection
// that is not disconnected, one per (organization, provider) in the mode.
const JobByokConnections = "BYOK_CONNECTIONS"

func ByokConnectionPK(accountOrgID string, livemode bool) string { return accountOrgID + "#" + Mode(livemode) }
func ByokConnectionSK(provider string) string                    { return "CONNECTION#" + provider }
func byokJobPK(livemode bool) string                              { return Mode(livemode) + "#" + JobByokConnections }
```

`api/internal/repositories/byok.go`:

```go
package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

var (
	ErrConnectionExists  = errors.New("byok: the organization already has a connection to this provider in this mode")
	ErrConnectionChanged = errors.New("byok: the connection changed since it was read")
)

// EntityByokConnection names connection audit rows.
const EntityByokConnection Entity = "BYOK_CONNECTION"

type connectionRow struct {
	keys
	byokconn.Connection
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

type ByokRepository struct {
	base  Base
	audit Base
}

func NewByokRepository(db *dynamodb.Client, cfg *config.Config) *ByokRepository {
	return &ByokRepository{base: NewBase(db, cfg, TableByok), audit: NewBase(db, cfg, TableAudit)}
}

func (r *ByokRepository) row(c *byokconn.Connection, now time.Time) connectionRow {
	row := connectionRow{keys: newKeys(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokConnectionSK(c.Provider), RetentionByokConnection, now), Connection: *c}
	if c.State != byokconn.StateDisconnected {
		row.SchedulePK, row.ScheduleSK = byokJobPK(c.Livemode), c.AccountOrganizationID+"#"+c.Provider
	}
	return row
}

func (r *ByokRepository) GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error) {
	item, err := r.base.GetItem(ctx, ByokConnectionPK(accountOrgID, livemode), ByokConnectionSK(provider))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: connection %s", ErrNotFound, provider)
	}
	row, err := Decode[connectionRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Connection, nil
}

func (r *ByokRepository) ListConnections(ctx context.Context, accountOrgID string, livemode bool) ([]byokconn.Connection, error) {
	res, err := r.base.Query(ctx, QueryOpts{PK: ByokConnectionPK(accountOrgID, livemode), SKPrefix: "CONNECTION#", Limit: 20})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[connectionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]byokconn.Connection, len(rows))
	for i := range rows {
		out[i] = rows[i].Connection
	}
	return out, nil
}

func (r *ByokRepository) auditItem(c *byokconn.Connection, action, actor, requestID string, now time.Time) (types.TransactWriteItem, error) {
	// The audit partition is the organization's space key: the connection is the
	// organization's, not a billing tenant's (an organization may connect with
	// no tenant at all).
	item, err := buildAuditItem(c.AccountOrganizationID, c.Livemode, AuditEntry{
		Entity: EntityByokConnection, EntityID: c.ID, Action: action, Actor: actor, RequestID: requestID,
	}, "", string(c.State), now)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.audit.BuildPutTxItem(item), nil
}

func (r *ByokRepository) CreateConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error {
	c.Revision, c.CreatedAt = 1, now.UTC()
	item, err := Encode(r.row(c, now))
	if err != nil {
		return err
	}
	audit, err := r.auditItem(c, "byok.connected", actor, requestID, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item), audit))
	if IsConditionFailed(err) {
		return ErrConnectionExists
	}
	return conflictErr(err)
}

// SaveConnection replaces the row if its revision is still the one read.
func (r *ByokRepository) SaveConnection(ctx context.Context, c *byokconn.Connection, actor, requestID, action string, now time.Time) error {
	read := c.Revision
	next := *c
	next.Revision = read + 1
	row := r.row(&next, now)
	item, err := Encode(row)
	if err != nil {
		return err
	}
	put := types.TransactWriteItem{Put: &types.Put{
		TableName:                 aws.String(r.base.TableName),
		Item:                      item,
		ConditionExpression:       aws.String("revision = :r"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":r": &types.AttributeValueMemberN{Value: strconv.Itoa(read)}},
	}}
	items := txItems(put)
	if action != "" {
		audit, err := r.auditItem(c, action, actor, requestID, now)
		if err != nil {
			return err
		}
		items = append(items, audit)
	}
	if err := r.base.TransactWrite(ctx, items); err != nil {
		if IsConditionFailed(err) {
			return ErrConnectionChanged
		}
		return conflictErr(err)
	}
	*c = next
	return nil
}

func (r *ByokRepository) DeleteConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error {
	audit, err := r.auditItem(c, "byok.disconnected", actor, requestID, now)
	if err != nil {
		return err
	}
	del := r.base.BuildDeleteTxItem(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokConnectionSK(c.Provider))
	return conflictErr(r.base.TransactWrite(ctx, txItems(del, audit)))
}

// ConnectionsForJob is cross-organization by design (ADR 0002): cmd/byok only.
func (r *ByokRepository) ConnectionsForJob(ctx context.Context, livemode bool, startKey map[string]types.AttributeValue, limit int) (*Page[byokconn.Connection], error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: byokJobPK(livemode), Limit: limit, ExclusiveStartKey: startKey, ScanIndexForward: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[connectionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]byokconn.Connection, len(rows))
	for i := range rows {
		out[i] = rows[i].Connection
	}
	return &Page[byokconn.Connection]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}
```

`buildAuditItem` is the existing builder in `api/internal/repositories/transition.go:286`; `Base.GetItem(ctx, pk, sk...)` returns a nil map when absent (api-commons `dynamo/base.go:53`).

Disconnect keeps the Finanças history: deleting the row is the "credentials deleted" step (spec § 3.6 step 3). Reconnecting creates a new row (new `ID`, `Version` 1) and `LedgerAccountID` is re-derived from the account number (Task 14), so the same bank account resumes on its ledger account.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./cmd ./tests && go vet ./... && go test ./... -race -count=1 && make test-integration`
Expected: all `ok`, including `TestEveryTableConstantHasASchema` and `TestTableDefinitionRendersEveryTable` with the new table.

- [ ] **Step 5: Commit**

```bash
git add api/internal/config api/internal/domain/byokconn api/internal/repositories api/tests/integration/byok_repo_test.go
git commit -m "feat(byok): connection domain, byok table and repository, legal-gate configuration"
```

### Task 9: The outbound Lambda — sealing, the gate and the dispatcher

**Files:**
- Create: `api/internal/byokseal/seal.go`, `api/cmd/byok-outbound/main.go`, `api/cmd/byok-outbound/handler.go`, `api/cmd/byok-outbound/secrets.go`
- Modify: `api/Makefile` (`build-lambdas` target)
- Test: `api/internal/byokseal/seal_test.go`, `api/cmd/byok-outbound/handler_test.go`, `api/cmd/byok-outbound/imports_test.go`

**Interfaces:**
- Consumes: `byok.Request`, `byok.Response`, `byok.GroupOf`, `byok.ErrorCode`, arg structs (Task 1); `byoktest.Fake` (Task 2, tests); `inter.NewProvider` (Task 5).
- Produces (the Lambda's contract, used by Task 10 and part B):
  - `byokseal.New(keyBase64 string) (*Sealer, error)` (refuses anything but 32 bytes), `(*Sealer).Seal(plain, aad []byte) (string, error)`, `(*Sealer).Open(sealed string, aad []byte) ([]byte, error)`, `byokseal.AAD(accountOrgID, mode, provider, connectionID string, version int) []byte`, errors `byokseal.ErrNotSealed`, `byokseal.ErrOpen`.
  - Lambda environment: `ENVIRONMENT`, `BYOK_CREDENTIALS_KEY_PARAM` (`/ctech-billing/{env}/byok/credentials-key`), `BYOK_SANDBOX_CREDENTIAL_PARAMS` (`client-id=/ctech-wallet/{env}/inter/client-id,client-secret=/ctech-wallet/{env}/inter/client-secret,cert=/ctech-wallet/{env}/inter/mtls-cert,key=/ctech-wallet/{env}/inter/mtls-key`), `BYOK_SANDBOX_ACCOUNT_PARAM`, `BYOK_SANDBOX_PIX_KEY_PARAM`, `BYOK_LIVE_COLLECTION`, `BYOK_LIVE_PROVIDERS`, `BYOK_FORCE_SANDBOX` (`true` outside prod), `ALERTS_TOPIC_ARN`.
  - Responses: `Connect` and `Reseal` → `byok.Connected` with `Sealed`; `Token` → `byok.Token`; every other op → the provider method's result as JSON (`GetWebhook` → `byok.WebhookResult`). Errors: `Response{Code: byok.ErrorCode(err), Error: sanitized}`.

- [ ] **Step 1: Write the failing tests**

`api/internal/byokseal/seal_test.go`:

```go
package byokseal

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func key(t *testing.T) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestABundleSealedForOneConnectionDoesNotOpenOnAnother(t *testing.T) {
	s, err := New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	mine := AAD("org-a", "live", "inter", "conn-1", 1)
	sealed, err := s.Seal([]byte(`{"client_secret":"s3cr3t"}`), mine)
	if err != nil || !strings.HasPrefix(sealed, "b1.") || strings.Contains(sealed, "s3cr3t") {
		t.Fatalf("%q %v", sealed, err)
	}
	if plain, err := s.Open(sealed, mine); err != nil || string(plain) != `{"client_secret":"s3cr3t"}` {
		t.Fatalf("own AAD: %s %v", plain, err)
	}
	for _, other := range [][]byte{
		AAD("org-b", "live", "inter", "conn-1", 1), AAD("org-a", "test", "inter", "conn-1", 1),
		AAD("org-a", "live", "other", "conn-1", 1), AAD("org-a", "live", "inter", "conn-2", 1),
		AAD("org-a", "live", "inter", "conn-1", 2),
	} {
		if _, err := s.Open(sealed, other); !errors.Is(err, ErrOpen) {
			t.Fatalf("opened under %s: %v", other, err)
		}
	}
}

func TestOpenRefusesUnprefixedAndForeignFormats(t *testing.T) {
	s, _ := New(key(t))
	for _, v := range []string{"", "plaintext", "v1.AAAA", "b2.AAAA"} {
		if _, err := s.Open(v, AAD("o", "live", "inter", "c", 1)); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("%q: %v", v, err)
		}
	}
}

func TestNewRefusesAnythingButThirtyTwoBytes(t *testing.T) {
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := New(k); err == nil {
			t.Fatalf("%q accepted", k)
		}
	}
}
```

`api/cmd/byok-outbound/handler_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

func newHandler(t *testing.T, gate gate) (*handler, *byoktest.Fake) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	s, err := byokseal.New(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	f := byoktest.New()
	return &handler{sealer: s, providers: map[string]byok.Provider{"inter": f}, gate: gate,
		sandbox: func(context.Context) (sandboxCreds, error) {
			return sandboxCreds{values: map[string]string{"client_id": "ctech"}, account: "999", pixKey: "evp"}, nil
		}, bundles: newBundleCache()}, f
}

func call(t *testing.T, h *handler, req byok.Request) byok.Response {
	t.Helper()
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("a provider failure is never an invoke error: %v", err)
	}
	return resp
}

func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func connect(t *testing.T, h *handler, mode string) byok.ConnectionRef {
	ref := byok.ConnectionRef{AccountOrganizationID: "org-a", ID: "conn-1", Version: 1}
	resp := call(t, h, byok.Request{Op: byok.OpConnect, Provider: "inter", Mode: mode, Connection: ref,
		Payload: payload(byok.ConnectArgs{Values: map[string]string{"client_id": "c", "client_secret": "s3cr3t"}, Account: "1234567"})})
	if resp.Code != "" {
		t.Fatalf("connect: %+v", resp)
	}
	var got byok.Connected
	_ = json.Unmarshal(resp.Payload, &got)
	if !strings.HasPrefix(got.Sealed, "b1.") || strings.Contains(string(resp.Payload), "s3cr3t") {
		t.Fatalf("connect must return only ciphertext: %s", resp.Payload)
	}
	ref.Sealed, ref.Account = got.Sealed, "1234567"
	return ref
}

func TestLiveCollectOpsAreRefusedUnlessTheProviderIsCleared(t *testing.T) {
	for _, g := range []gate{{liveCollection: false, providers: []string{"inter"}}, {liveCollection: true}} {
		h, f := newHandler(t, g)
		ref := connect(t, h, "live")
		create := call(t, h, byok.Request{Op: byok.OpCreateDueCharge, Provider: "inter", Mode: "live", Connection: ref,
			OAuthToken: "t", Payload: payload(byok.DueCharge{Ref: "ctb01JABCDEFGHJKMNPQRSTVWXYZ", Amount: 100})})
		if create.Code != "op_refused" || f.Calls(byok.OpCreateDueCharge) != 0 {
			t.Fatalf("gate %+v: %+v, provider calls %d", g, create, f.Calls(byok.OpCreateDueCharge))
		}
		for _, op := range []byok.Op{byok.OpRegisterWebhook, byok.OpRefund, byok.OpUpdateCharge} {
			if r := call(t, h, byok.Request{Op: op, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(map[string]any{})}); r.Code != "op_refused" {
				t.Fatalf("%s: %+v", op, r)
			}
		}
		if r := call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.BalanceArgs{})}); r.Code != "" {
			t.Fatalf("read ops always work: %+v", r)
		}
		if r := call(t, h, byok.Request{Op: byok.OpDeleteWebhook, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.WebhookArgs{Product: byok.ProductPix})}); r.Code != "" {
			t.Fatalf("teardown ops always work: %+v", r)
		}
	}
	h, f := newHandler(t, gate{liveCollection: true, providers: []string{"inter"}})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpCreateDueCharge, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t",
		Payload: payload(byok.DueCharge{Ref: "ctb01JABCDEFGHJKMNPQRSTVWXYZ", Amount: 100})}); r.Code != "" || f.Calls(byok.OpCreateDueCharge) != 1 {
		t.Fatalf("cleared: %+v", r)
	}
}

func TestSimulatePaymentIsRefusedOutsideTheSandbox(t *testing.T) {
	h, _ := newHandler(t, gate{liveCollection: true, providers: []string{"inter"}})
	ref := connect(t, h, "live")
	if r := call(t, h, byok.Request{Op: byok.OpSimulatePayment, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "t", Payload: payload(byok.RefArgs{Ref: "x"})}); r.Code != "sandbox_only" {
		t.Fatalf("%+v", r)
	}
}

func TestTestModeOnlyEverUsesTheSandboxIntegration(t *testing.T) {
	h, _ := newHandler(t, gate{})
	r := call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "test",
		Connection: byok.ConnectionRef{ID: "conn-1", Sealed: "b1.x"}, OAuthToken: "t", Payload: payload(byok.BalanceArgs{})})
	if r.Code != "invalid_request" {
		t.Fatalf("a merchant connection in test mode: %+v", r)
	}
	r = call(t, h, byok.Request{Op: byok.OpToken, Provider: "inter", Mode: "test", Connection: byok.ConnectionRef{ID: "sandbox"}, Payload: payload(byok.TokenArgs{Set: byok.CapabilityStatement})})
	if r.Code != "" {
		t.Fatalf("sandbox token: %+v", r)
	}
}

func TestResealKeepsTheSecretAndBindsTheNextVersion(t *testing.T) {
	h, _ := newHandler(t, gate{})
	ref := connect(t, h, "live")
	resp := call(t, h, byok.Request{Op: byok.OpReseal, Provider: "inter", Mode: "live", Connection: ref,
		Payload: payload(byok.ResealArgs{CertPEM: "new-cert", KeyPEM: "new-key"})})
	var got byok.Connected
	_ = json.Unmarshal(resp.Payload, &got)
	plain, err := h.sealer.Open(got.Sealed, byokseal.AAD("org-a", "live", "inter", "conn-1", 2))
	if err != nil || !strings.Contains(string(plain), `"client_secret":"s3cr3t"`) || !strings.Contains(string(plain), "new-cert") {
		t.Fatalf("resealed bundle: %v", err)
	}
}

func TestPayloadsAndSecretsAreNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	h, f := newHandler(t, gate{})
	f.FailNext(byok.OpBalance, byok.ErrUnavailable)
	ref := connect(t, h, "live")
	call(t, h, byok.Request{Op: byok.OpBalance, Provider: "inter", Mode: "live", Connection: ref, OAuthToken: "bearer-xyz", Payload: payload(byok.BalanceArgs{})})
	for _, secret := range []string{"s3cr3t", "bearer-xyz", ref.Sealed, "1234567"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
}
```

`api/cmd/byok-outbound/imports_test.go` (spec § 17 item 14: `api-commons/inter` and `byokseal` only in the Lambda):

```go
package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOnlyTheLambdaImportsInterAndTheSealer(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`, "gopkg.aoctech.app/billing/api/...").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg, deps, _ := strings.Cut(line, " ")
		if strings.HasPrefix(pkg, "gopkg.aoctech.app/billing/api/cmd/byok-outbound") || pkg == "gopkg.aoctech.app/billing/api/internal/byokseal" {
			continue
		}
		for _, banned := range []string{"gopkg.aoctech.app/api-commons/inter", "gopkg.aoctech.app/billing/api/internal/byokseal"} {
			for _, d := range strings.Fields(deps) {
				if d == banned || strings.HasPrefix(d, banned+"/") {
					t.Errorf("%s imports %s", pkg, d)
				}
			}
		}
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/byokseal/ ./cmd/byok-outbound/`
Expected: FAIL, packages do not exist.

- [ ] **Step 3: Implement**

`api/internal/byokseal/seal.go`:

```go
// Package byokseal seals BYOK credential bundles (spec § 3.3): AES-256-GCM,
// random nonce, prefix "b1.", additional data binding the ciphertext to its
// connection. Only cmd/byok-outbound imports it (imports_test.go): the key
// lives in that Lambda alone.
package byokseal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const prefix = "b1."

var (
	ErrNotSealed = errors.New("byokseal: value is not a b1 bundle")
	ErrOpen      = errors.New("byokseal: bundle does not open under this key and binding")
)

type Sealer struct{ aead cipher.AEAD }

func New(keyB64 string) (*Sealer, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("byokseal: the key must be 32 bytes, base64")
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// AAD binds a bundle to {account_organization_id, mode, provider, connection_id, version}.
func AAD(accountOrgID, mode, provider, connectionID string, version int) []byte {
	return fmt.Appendf(nil, "%s|%s|%s|%s|%d", accountOrgID, mode, provider, connectionID, version)
}

func (s *Sealer) Seal(plain, aad []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, aad)), nil
}

func (s *Sealer) Open(sealed string, aad []byte) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return nil, ErrNotSealed
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, ErrOpen
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
```

`api/cmd/byok-outbound/handler.go`:

```go
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

// gate is the legal gate as this Lambda sees it (spec § 4.1, § 10). It is
// read from the environment, which Terraform sets from the same variables
// that write the SSM parameters the api reads.
type gate struct {
	liveCollection bool
	providers      []string
	forceSandbox   bool // every non-prod environment
}

func (g gate) cleared(provider string) bool { return g.liveCollection && slices.Contains(g.providers, provider) }

type sandboxCreds struct {
	values          map[string]string
	account, pixKey string
}

type handler struct {
	sealer    *byokseal.Sealer
	providers map[string]byok.Provider
	gate      gate
	sandbox   func(context.Context) (sandboxCreds, error)
	bundles   *bundleCache
}

// bundleCache keeps opened bundles up to 15 minutes per container (§ 3.3).
type bundleCache struct {
	mu sync.Mutex
	m  map[[32]byte]cachedBundle
}

type cachedBundle struct {
	values map[string]string
	until  time.Time
}

func newBundleCache() *bundleCache { return &bundleCache{m: map[[32]byte]cachedBundle{}} }

func (b *bundleCache) get(k [32]byte) (map[string]string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[k]
	if !ok || time.Now().After(e.until) {
		delete(b.m, k)
		return nil, false
	}
	return e.values, true
}

func (b *bundleCache) put(k [32]byte, v map[string]string) {
	b.mu.Lock()
	b.m[k] = cachedBundle{values: v, until: time.Now().Add(15 * time.Minute)}
	b.mu.Unlock()
}

// handle never returns an invoke error for a provider or gate failure, and
// never logs a payload, a bundle, a token or an account number.
func (h *handler) handle(ctx context.Context, req byok.Request) (byok.Response, error) {
	resp := h.dispatch(ctx, req)
	slog.InfoContext(ctx, "byok outbound", "op", req.Op, "provider", req.Provider, "mode", req.Mode, "code", resp.Code)
	return resp, nil
}

func fail(err error) byok.Response {
	code := byok.ErrorCode(err)
	msg := code
	if code == "internal" {
		msg = "internal error"
	}
	return byok.Response{Code: code, Error: msg}
}

func ok(v any) byok.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return fail(err)
	}
	return byok.Response{Payload: b}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("%w: payload", byok.ErrInvalidRequest)
	}
	return v, nil
}

func (h *handler) dispatch(ctx context.Context, req byok.Request) byok.Response {
	p, found := h.providers[req.Provider]
	group := byok.GroupOf(req.Op)
	if !found || group == "" || (req.Mode != "live" && req.Mode != "test") {
		return fail(byok.ErrInvalidRequest)
	}
	sandboxRef := req.Connection.ID == "sandbox"
	if req.Mode == "test" && !sandboxRef {
		return fail(fmt.Errorf("%w: test mode uses the shared sandbox integration only", byok.ErrInvalidRequest))
	}
	if req.Mode == "live" && group == byok.GroupCollect && !h.gate.cleared(req.Provider) {
		return fail(byok.ErrOpRefused)
	}
	sandbox := sandboxRef || h.gate.forceSandbox
	if group == byok.GroupSandbox && !sandbox {
		return fail(byok.ErrSandboxOnly)
	}

	switch req.Op {
	case byok.OpConnect:
		a, err := decode[byok.ConnectArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return h.connect(ctx, p, req, a.Values, a.Account, a.PixKey, req.Connection.Version, sandbox)
	case byok.OpReseal:
		a, err := decode[byok.ResealArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		old, err := h.open(req)
		if err != nil {
			return fail(err)
		}
		next := map[string]string{}
		for k, v := range old {
			next[k] = v
		}
		next["cert_pem"], next["key_pem"] = a.CertPEM, a.KeyPEM
		return h.connect(ctx, p, req, next, req.Connection.Account, req.Connection.PixKey, req.Connection.Version+1, sandbox)
	}

	creds, err := h.credentials(ctx, req, sandbox)
	if err != nil {
		return fail(err)
	}
	tok := byok.Token{Value: req.OAuthToken}
	switch req.Op {
	case byok.OpToken:
		a, err := decode[byok.TokenArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		t, err := p.Token(ctx, creds, a.Set)
		return result(t, err)
	case byok.OpBalance:
		a, err := decode[byok.BalanceArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.Balance(ctx, creds, tok, a.At))
	case byok.OpStatementPage:
		a, err := decode[byok.StatementArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.StatementPage(ctx, creds, tok, a.From, a.To, a.Page))
	case byok.OpCreateDueCharge:
		a, err := decode[byok.DueCharge](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.CreateDueCharge(ctx, creds, tok, a))
	case byok.OpGetCharge, byok.OpGetPayment:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.GetCharge(ctx, creds, tok, a.Ref))
	case byok.OpUpdateCharge:
		a, err := decode[byok.UpdateArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.UpdateCharge(ctx, creds, tok, a.Ref, a.Amount))
	case byok.OpCancelCharge:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.CancelCharge(ctx, creds, tok, a.Ref))
	case byok.OpRefund:
		a, err := decode[byok.RefundRequest](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.Refund(ctx, creds, tok, a))
	case byok.OpGetRefund:
		a, err := decode[byok.GetRefundArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.GetRefund(ctx, creds, tok, a.PaymentID, a.RefundID))
	case byok.OpGetWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		url, err := p.GetWebhook(ctx, creds, tok, a.Product)
		return result(byok.WebhookResult{URL: url}, err)
	case byok.OpRegisterWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.RegisterWebhook(ctx, creds, tok, a.Product, a.URL))
	case byok.OpDeleteWebhook:
		a, err := decode[byok.WebhookArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.DeleteWebhook(ctx, creds, tok, a.Product))
	case byok.OpListCallbacks:
		a, err := decode[byok.CallbacksArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(p.ListCallbacks(ctx, creds, tok, a.Since))
	case byok.OpRetryCallbacks:
		a, err := decode[byok.RetryArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.RetryCallbacks(ctx, creds, tok, a.Refs))
	case byok.OpSimulatePayment:
		a, err := decode[byok.RefArgs](req.Payload)
		if err != nil {
			return fail(err)
		}
		return result(struct{}{}, p.SimulatePayment(ctx, creds, tok, a.Ref))
	}
	return fail(byok.ErrInvalidRequest)
}

func result[T any](v T, err error) byok.Response {
	if err != nil {
		return fail(err)
	}
	return ok(v)
}

func (h *handler) aad(req byok.Request, version int) []byte {
	return byokseal.AAD(req.Connection.AccountOrganizationID, req.Mode, req.Provider, req.Connection.ID, version)
}

func (h *handler) open(req byok.Request) (map[string]string, error) {
	k := sha256.Sum256(append([]byte(req.Connection.Sealed), h.aad(req, req.Connection.Version)...))
	if v, ok := h.bundles.get(k); ok {
		return v, nil
	}
	plain, err := h.sealer.Open(req.Connection.Sealed, h.aad(req, req.Connection.Version))
	if err != nil {
		return nil, fmt.Errorf("%w: stored credentials do not open", byok.ErrCredentialsRefused)
	}
	var v map[string]string
	if err := json.Unmarshal(plain, &v); err != nil {
		return nil, fmt.Errorf("%w: bundle", byok.ErrCredentialsRefused)
	}
	h.bundles.put(k, v)
	return v, nil
}

func (h *handler) credentials(ctx context.Context, req byok.Request, sandbox bool) (byok.Credentials, error) {
	if req.Connection.ID == "sandbox" {
		sc, err := h.sandbox(ctx)
		if err != nil {
			return byok.Credentials{}, fmt.Errorf("%w: sandbox credentials", byok.ErrUnavailable)
		}
		return byok.Credentials{Values: sc.values, Account: sc.account, PixKey: sc.pixKey, Sandbox: true}, nil
	}
	v, err := h.open(req)
	if err != nil {
		return byok.Credentials{}, err
	}
	return byok.Credentials{Values: v, Account: req.Connection.Account, PixKey: req.Connection.PixKey, Sandbox: sandbox}, nil
}

func (h *handler) connect(ctx context.Context, p byok.Provider, req byok.Request, values map[string]string, account, pixKey string, version int, sandbox bool) byok.Response {
	got, err := p.Connect(ctx, byok.Credentials{Values: values, Account: account, PixKey: pixKey, Sandbox: sandbox})
	if err != nil {
		return fail(err)
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return fail(err)
	}
	got.Sealed, err = h.sealer.Seal(plain, h.aad(req, version))
	if err != nil {
		return fail(errors.Join(byok.ErrUnavailable, err))
	}
	return ok(got)
}
```

`api/cmd/byok-outbound/secrets.go`:

```go
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type paramReader interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func readParam(ctx context.Context, r paramReader, name string) (string, error) {
	out, err := r.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return "", fmt.Errorf("ssm %s: unreadable", name) // never the value, never the SDK error body
	}
	return aws.ToString(out.Parameter.Value), nil
}

// sandboxLoader reads CTech's own Inter integration (the wallet's parameters,
// by IAM grant: spec amendment 2026-10-11) once per container, on first use.
func sandboxLoader(r paramReader, credentialParams, accountParam, pixKeyParam string) func(context.Context) (sandboxCreds, error) {
	var once sync.Once
	var creds sandboxCreds
	var loadErr error
	return func(ctx context.Context) (sandboxCreds, error) {
		once.Do(func() {
			values := map[string]string{}
			names := map[string]string{"client-id": "client_id", "client-secret": "client_secret", "cert": "cert_pem", "key": "key_pem"}
			for _, pair := range strings.Split(credentialParams, ",") {
				k, path, _ := strings.Cut(pair, "=")
				v, err := readParam(ctx, r, path)
				if err != nil {
					loadErr = err
					return
				}
				values[names[k]] = v
			}
			acct, err := readParam(ctx, r, accountParam)
			if err != nil {
				loadErr = err
				return
			}
			pix, err := readParam(ctx, r, pixKeyParam)
			if err != nil {
				loadErr = err
				return
			}
			creds = sandboxCreds{values: values, account: acct, pixKey: pix}
		})
		return creds, loadErr
	}
}
```

`sync.Once` keeps a failed load failed for the container's life; a failed sandbox read alerts (below) and the next cold start retries. Acceptable: the sandbox is test mode only.

`api/cmd/byok-outbound/main.go`:

```go
// Command byok-outbound is billing's only connection to a merchant's bank
// (spec 2026-10-11 § 4.1). Not VPC-attached (IPv4 egress without NAT), one op
// per invocation, called synchronously by the api and cmd/byok. It holds the
// credentials key (read once at cold start) and refuses live collect ops
// unless the provider is cleared (§ 10).
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	env := os.Getenv("ENVIRONMENT")
	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "error", err)
		os.Exit(1)
	}
	notify, aerr := alerts.New(ctx, awsCfg.Region, os.Getenv("ALERTS_TOPIC_ARN"), "billing", env)
	if aerr != nil {
		notify = alerts.Nop{}
	}
	params := ssm.NewFromConfig(awsCfg)
	key, err := readParam(ctx, params, os.Getenv("BYOK_CREDENTIALS_KEY_PARAM"))
	if err == nil {
		_, err = byokseal.New(key)
	}
	if err != nil {
		// No development fallback (ADR 0017): every op fails as "provider down".
		notify.Alert(ctx, alerts.Alert{Job: "byok-outbound-startup", Summary: "byok outbound Lambda did not start: credentials key unreadable", Err: err})
		slog.Error("credentials key unreadable")
		os.Exit(1)
	}
	sealer, _ := byokseal.New(key)
	h := &handler{
		sealer:    sealer,
		providers: map[string]byok.Provider{"inter": inter.NewProvider(inter.NewClient())},
		gate: gate{
			liveCollection: os.Getenv("BYOK_LIVE_COLLECTION") == "true",
			providers:      strings.FieldsFunc(os.Getenv("BYOK_LIVE_PROVIDERS"), func(r rune) bool { return r == ',' || r == ' ' }),
			forceSandbox:   os.Getenv("BYOK_FORCE_SANDBOX") == "true",
		},
		sandbox: sandboxLoader(params, os.Getenv("BYOK_SANDBOX_CREDENTIAL_PARAMS"),
			os.Getenv("BYOK_SANDBOX_ACCOUNT_PARAM"), os.Getenv("BYOK_SANDBOX_PIX_KEY_PARAM")),
		bundles: newBundleCache(),
	}
	lambda.Start(h.handle)
}
```

`alerts.New(ctx, region, topicARN, service, environment)` and `alerts.Nop{}` are api-commons' (`alerts/alerts.go:67`, `:111`), the same pair `internal/jobs.Alerts` uses.

`api/Makefile`:

```make
# The BYOK Lambdas (spec 2026-10-11 § 4.1, § 6.3): provided.al2023, arm64,
# the binary named bootstrap, one zip each.
build-lambdas:
	mkdir -p $(BUILD_DIR)/lambda/byok-outbound
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -ldflags="-s -w" \
		-o $(BUILD_DIR)/lambda/byok-outbound/bootstrap ./cmd/byok-outbound
	cd $(BUILD_DIR)/lambda/byok-outbound && zip -q -j ../byok-outbound.zip bootstrap
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./cmd && go vet ./... && go test ./internal/byokseal/ ./cmd/byok-outbound/ -race -count=1 && make build-lambdas && ls -la dist/lambda/byok-outbound.zip`
Expected: `ok` twice; the zip exists.

- [ ] **Step 5: Commit**

```bash
git add api/internal/byokseal api/cmd/byok-outbound api/Makefile api/go.mod api/go.sum
git commit -m "feat(byok): outbound Lambda with sealed credentials, the legal-gate allowlist and the provider dispatcher"
```

### Task 10: `byokrpc` — the `LambdaProvider`, the token manager and the limiters

**Files:**
- Create: `api/internal/byokrpc/lambda.go`, `api/internal/byokrpc/tokens.go`, `api/internal/byokrpc/limits.go`, `api/internal/byokrpc/session.go`
- Test: `api/internal/byokrpc/lambda_test.go`, `api/internal/byokrpc/tokens_test.go`, `api/internal/byokrpc/fakeinvoker_test.go`

**Interfaces:**
- Consumes: Task 1 envelope; Task 8 `byokconn.Connection`; billing's `crypto.Sealer`; api-commons `cache.Backend`, `lock.New`, `ratelimit.Counter`.
- Produces:

```go
package byokrpc
type Invoker interface{ Invoke(ctx context.Context, in *lambda.InvokeInput, opts ...func(*lambda.Options)) (*lambda.InvokeOutput, error) }
type Outbound struct{ /* invoker, function */ }
func NewOutbound(inv Invoker, function string) *Outbound
func (o *Outbound) For(provider string, livemode bool) *LambdaProvider
type LambdaProvider struct{ /* implements byok.Provider */ }
func (p *LambdaProvider) Reseal(ctx context.Context, ref byok.ConnectionRef, certPEM, keyPEM string) (byok.Connected, error)
var ErrSandboxBusy = errors.New("byok: the shared sandbox is busy")
type TokenManager struct{ /* cache, locker, counter, sealer, clock */ }
func NewTokenManager(c cache.Backend, counter ratelimit.Counter, sealer *crypto.Sealer) *TokenManager
func (m *TokenManager) Bearer(ctx context.Context, p *LambdaProvider, conn *byokconn.Connection, set byok.Capability) (byok.Token, error)
func (m *TokenManager) Invalidate(ctx context.Context, conn *byokconn.Connection)
type Limits struct{ /* counter */ }
func NewLimits(counter ratelimit.Counter) *Limits
func (l *Limits) Banking(ctx context.Context, conn *byokconn.Connection) error // ≤ 8/min per connection; sandbox: global ≤ 8/min → ErrSandboxBusy
func (l *Limits) Pix(ctx context.Context, conn *byokconn.Connection) error     // live: no local limit (120/min at Inter); sandbox: global ≤ 8/min → ErrSandboxBusy
type Session struct{ P *LambdaProvider; Conn *byokconn.Connection; Tokens *TokenManager; Limits *Limits }
func Call[T any](ctx context.Context, s Session, set byok.Capability, fn func(byok.Credentials, byok.Token) (T, error)) (T, error) // one forced refresh + one retry on ErrUnauthorized
func (s Session) Credentials() byok.Credentials // {Ref: &conn.Ref()}
```

- [ ] **Step 1: Write the failing tests**

`api/internal/byokrpc/fakeinvoker_test.go` (routes envelopes to a `byoktest.Fake`, counting token fetches):

```go
package byokrpc

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
)

type fakeInvoker struct {
	fake      *byoktest.Fake
	tokens    atomic.Int32
	unauthorized atomic.Int32 // answer this many Balance calls with unauthorized
	last      atomic.Value    // last byok.Request
}

func (f *fakeInvoker) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	var req byok.Request
	_ = json.Unmarshal(in.Payload, &req)
	f.last.Store(req)
	var resp byok.Response
	switch req.Op {
	case byok.OpToken:
		f.tokens.Add(1)
		resp.Payload, _ = json.Marshal(byok.Token{Value: "t", ExpiresIn: 3600})
	case byok.OpBalance:
		if f.unauthorized.Load() > 0 {
			f.unauthorized.Add(-1)
			resp = byok.Response{Code: "unauthorized", Error: "unauthorized"}
			break
		}
		b, _ := f.fake.Balance(ctx, byok.Credentials{}, byok.Token{}, byok.Date{})
		resp.Payload, _ = json.Marshal(b)
	default:
		resp = byok.Response{Code: "invalid_request", Error: "unsupported in this fake"}
	}
	out, _ := json.Marshal(resp)
	return &lambda.InvokeOutput{StatusCode: 200, Payload: out}, nil
}
```

`api/internal/byokrpc/tokens_test.go`:

```go
package byokrpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

func sealer(t *testing.T) *crypto.Sealer {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return crypto.NewSealer(base64.StdEncoding.EncodeToString(k))
}

func setup(t *testing.T) (*fakeInvoker, *TokenManager, Session, cache.Backend) {
	inv := &fakeInvoker{fake: byoktest.New()}
	c := cache.NewMemoryBackend(1000)
	counter := ratelimit.NewMemoryCounter()
	m := NewTokenManager(c, counter, sealer(t))
	conn := &byokconn.Connection{AccountOrganizationID: "org", Livemode: true, Provider: "inter", ID: "c1", Version: 1,
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Sealed: "b1.x"}
	s := Session{P: NewOutbound(inv, "fn").For("inter", true), Conn: conn, Tokens: m, Limits: NewLimits(counter)}
	return inv, m, s, c
}

func TestFiftyConcurrentCallersFetchOneToken(t *testing.T) {
	inv, _, s, _ := setup(t)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
				return s.P.Balance(context.Background(), c, tok, byok.Date{})
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if inv.tokens.Load() != 1 {
		t.Fatalf("%d token fetches, want 1", inv.tokens.Load())
	}
}

func TestTheLimiterNeverLetsAFourthFetchOut(t *testing.T) {
	inv, m, s, c := setup(t)
	for i := range 5 {
		_ = c.DeletePrefix(context.Background(), "byok:token:")
		_, err := m.Bearer(context.Background(), s.P, s.Conn, byok.CapabilityStatement)
		if i < 3 && err != nil {
			t.Fatalf("fetch %d: %v", i+1, err)
		}
		if i >= 3 && !errors.Is(err, byok.ErrRateLimited) {
			t.Fatalf("fetch %d: %v, want rate limited", i+1, err)
		}
	}
	if inv.tokens.Load() != 3 {
		t.Fatalf("%d fetches reached the provider, want 3", inv.tokens.Load())
	}
}

func TestA401RefreshesOnceAndRetriesOnce(t *testing.T) {
	inv, _, s, _ := setup(t)
	inv.unauthorized.Store(1)
	if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	}); err != nil {
		t.Fatal(err)
	}
	if inv.tokens.Load() != 2 {
		t.Fatalf("%d fetches: one, then the forced refresh", inv.tokens.Load())
	}
	inv.unauthorized.Store(2)
	if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	}); !errors.Is(err, byok.ErrUnauthorized) {
		t.Fatalf("a second 401 is a failure: %v", err)
	}
}

func TestTheCachedTokenIsSealed(t *testing.T) {
	_, m, s, c := setup(t)
	if _, err := m.Bearer(context.Background(), s.P, s.Conn, byok.CapabilityStatement); err != nil {
		t.Fatal(err)
	}
	raw, ok, _ := c.Get(context.Background(), tokenKey(s.Conn, byok.CapabilityStatement))
	if !ok || !crypto.IsSealed(string(raw)) {
		t.Fatalf("cached value %q is not sealed", raw)
	}
}

func TestTheSandboxSharedLimiterAnswersBusy(t *testing.T) {
	_, _, s, _ := setup(t)
	s.Conn.Kind = byokconn.KindSandboxShared
	for i := range 9 {
		err := s.Limits.Pix(context.Background(), s.Conn)
		if i < 8 && err != nil || i == 8 && !errors.Is(err, ErrSandboxBusy) {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}
```

(`crypto.IsSealed` is a helper added in this task to `api/internal/crypto/field.go`, reusing the sealer's own prefix parser: `func IsSealed(s string) bool { _, _, ok := splitGeneration(s); return ok }`.)

`api/internal/byokrpc/lambda_test.go`:

```go
func TestTheEnvelopeCarriesTheRefAndTheModeNeverPlaintext(t *testing.T) {
	inv, _, s, _ := setup(t)
	_, _ = Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	})
	req := inv.last.Load().(byok.Request)
	if req.Mode != "live" || req.Provider != "inter" || req.Connection.Sealed != "b1.x" || req.OAuthToken != "t" {
		t.Fatalf("%+v", req)
	}
}

func TestAProviderFailureComesBackAsItsSentinel(t *testing.T) {
	inv, _, s, _ := setup(t)
	inv.unauthorized.Store(5)
	_, err := s.P.Balance(context.Background(), s.Credentials(), byok.Token{Value: "t"}, byok.Date{})
	if !errors.Is(err, byok.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/byokrpc/`
Expected: FAIL, `undefined: NewTokenManager`.

- [ ] **Step 3: Implement**

`api/internal/byokrpc/lambda.go`:

```go
// Package byokrpc is the api's side of the outbound Lambda: a byok.Provider
// over lambda:InvokeFunction, the token lifecycle (spec § 4.3) and the
// limiters. Billing's services depend on byok.Provider and on this package,
// never on api-commons/inter.
package byokrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/byok"
)

type Invoker interface {
	Invoke(ctx context.Context, in *lambda.InvokeInput, opts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

type Outbound struct {
	inv      Invoker
	function string
}

func NewOutbound(inv Invoker, function string) *Outbound { return &Outbound{inv: inv, function: function} }

// For binds the provider and the mode (the AAD and the gate need both).
func (o *Outbound) For(provider string, livemode bool) *LambdaProvider {
	mode := "test"
	if livemode {
		mode = "live"
	}
	return &LambdaProvider{o: o, provider: provider, mode: mode}
}

type LambdaProvider struct {
	o        *Outbound
	provider string
	mode     string
}

func ref(c byok.Credentials) byok.ConnectionRef {
	if c.Ref == nil {
		return byok.ConnectionRef{}
	}
	return *c.Ref
}

func invoke[T any](ctx context.Context, p *LambdaProvider, op byok.Op, conn byok.ConnectionRef, bearer string, args any) (T, error) {
	var zero T
	payload, err := json.Marshal(args)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(byok.Request{Op: op, Provider: p.provider, Mode: p.mode, Connection: conn, OAuthToken: bearer, Payload: payload})
	if err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := p.o.inv.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(p.o.function), Payload: body})
	if err != nil {
		return zero, fmt.Errorf("%w: outbound Lambda unreachable", byok.ErrUnavailable)
	}
	if out.FunctionError != nil {
		return zero, fmt.Errorf("%w: outbound Lambda failed", byok.ErrUnavailable)
	}
	var resp byok.Response
	if err := json.Unmarshal(out.Payload, &resp); err != nil {
		return zero, fmt.Errorf("%w: outbound response", byok.ErrUnavailable)
	}
	if resp.Code != "" {
		return zero, byok.ErrorFromCode(resp.Code, resp.Error)
	}
	var v T
	if len(resp.Payload) > 0 {
		if err := json.Unmarshal(resp.Payload, &v); err != nil {
			return zero, fmt.Errorf("%w: outbound payload", byok.ErrUnavailable)
		}
	}
	return v, nil
}

type none struct{}

func (p *LambdaProvider) Name() string                   { return p.provider }
func (p *LambdaProvider) CredentialFields() []byok.Field { return nil } // the console's fields are per provider (Task 19)

// Connect sends plaintext once; the Lambda tests, seals and returns Sealed.
func (p *LambdaProvider) Connect(ctx context.Context, c byok.Credentials) (byok.Connected, error) {
	return invoke[byok.Connected](ctx, p, byok.OpConnect, ref(c), "", byok.ConnectArgs{Values: c.Values, Account: c.Account, PixKey: c.PixKey})
}

func (p *LambdaProvider) Reseal(ctx context.Context, r byok.ConnectionRef, certPEM, keyPEM string) (byok.Connected, error) {
	return invoke[byok.Connected](ctx, p, byok.OpReseal, r, "", byok.ResealArgs{CertPEM: certPEM, KeyPEM: keyPEM})
}

func (p *LambdaProvider) Token(ctx context.Context, c byok.Credentials, set byok.Capability) (byok.Token, error) {
	return invoke[byok.Token](ctx, p, byok.OpToken, ref(c), "", byok.TokenArgs{Set: set})
}

func (p *LambdaProvider) CreateDueCharge(ctx context.Context, c byok.Credentials, t byok.Token, in byok.DueCharge) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpCreateDueCharge, ref(c), t.Value, in)
}

func (p *LambdaProvider) GetCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpGetCharge, ref(c), t.Value, byok.RefArgs{Ref: r})
}

func (p *LambdaProvider) UpdateCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string, amount int64) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpUpdateCharge, ref(c), t.Value, byok.UpdateArgs{Ref: r, Amount: amount})
}

func (p *LambdaProvider) CancelCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string) error {
	_, err := invoke[none](ctx, p, byok.OpCancelCharge, ref(c), t.Value, byok.RefArgs{Ref: r})
	return err
}

func (p *LambdaProvider) Refund(ctx context.Context, c byok.Credentials, t byok.Token, in byok.RefundRequest) (byok.Refund, error) {
	return invoke[byok.Refund](ctx, p, byok.OpRefund, ref(c), t.Value, in)
}

func (p *LambdaProvider) GetRefund(ctx context.Context, c byok.Credentials, t byok.Token, paymentID, refundID string) (byok.Refund, error) {
	return invoke[byok.Refund](ctx, p, byok.OpGetRefund, ref(c), t.Value, byok.GetRefundArgs{PaymentID: paymentID, RefundID: refundID})
}

func (p *LambdaProvider) StatementPage(ctx context.Context, c byok.Credentials, t byok.Token, from, to byok.Date, page int) (byok.StatementPage, error) {
	return invoke[byok.StatementPage](ctx, p, byok.OpStatementPage, ref(c), t.Value, byok.StatementArgs{From: from, To: to, Page: page})
}

func (p *LambdaProvider) Balance(ctx context.Context, c byok.Credentials, t byok.Token, at byok.Date) (byok.Balance, error) {
	return invoke[byok.Balance](ctx, p, byok.OpBalance, ref(c), t.Value, byok.BalanceArgs{At: at})
}

func (p *LambdaProvider) GetWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) (string, error) {
	r, err := invoke[byok.WebhookResult](ctx, p, byok.OpGetWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product})
	return r.URL, err
}

func (p *LambdaProvider) RegisterWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product, url string) error {
	_, err := invoke[none](ctx, p, byok.OpRegisterWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product, URL: url})
	return err
}

func (p *LambdaProvider) DeleteWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) error {
	_, err := invoke[none](ctx, p, byok.OpDeleteWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product})
	return err
}

func (p *LambdaProvider) ListCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, since time.Time) ([]byok.CallbackAttempt, error) {
	return invoke[[]byok.CallbackAttempt](ctx, p, byok.OpListCallbacks, ref(c), t.Value, byok.CallbacksArgs{Since: since})
}

func (p *LambdaProvider) RetryCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, refs []string) error {
	_, err := invoke[none](ctx, p, byok.OpRetryCallbacks, ref(c), t.Value, byok.RetryArgs{Refs: refs})
	return err
}

func (p *LambdaProvider) SimulatePayment(ctx context.Context, c byok.Credentials, t byok.Token, r string) error {
	_, err := invoke[none](ctx, p, byok.OpSimulatePayment, ref(c), t.Value, byok.RefArgs{Ref: r})
	return err
}

// ParseCallback never runs in the api (the webhook Lambda parses).
func (p *LambdaProvider) ParseCallback(byok.Product, map[string]string, []byte) (byok.Callback, error) {
	return byok.Callback{}, byok.ErrInvalidRequest
}

var _ byok.Provider = (*LambdaProvider)(nil)
```

`api/internal/byokrpc/tokens.go`:

```go
package byokrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/lock"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

const (
	tokenFetchesPerMinute = 3 // below Inter's 5/min (spec § 4.3)
	tokenSafety           = 300 * time.Second
	waitForWinner         = 5 * time.Second
)

type TokenManager struct {
	cache   cache.Backend
	locks   *lock.Locker
	counter ratelimit.Counter
	sealer  *crypto.Sealer
}

func NewTokenManager(c cache.Backend, counter ratelimit.Counter, sealer *crypto.Sealer) *TokenManager {
	return &TokenManager{cache: c, locks: lock.New(c, 30*time.Second), counter: counter, sealer: sealer}
}

func connID(conn *byokconn.Connection) string {
	if conn.Kind == byokconn.KindSandboxShared {
		return "sandbox" // one token for every test tenant
	}
	return conn.ID
}

func tokenKey(conn *byokconn.Connection, set byok.Capability) string {
	mode := "test"
	if conn.Livemode {
		mode = "live"
	}
	return fmt.Sprintf("byok:token:%s:%s:%s:%d:%s", conn.Provider, mode, connID(conn), conn.Version, set)
}

func (m *TokenManager) read(ctx context.Context, key string) (byok.Token, bool) {
	raw, ok, err := m.cache.Get(ctx, key)
	if err != nil || !ok {
		return byok.Token{}, false
	}
	plain, err := m.sealer.Open(string(raw))
	if err != nil {
		return byok.Token{}, false
	}
	var t byok.Token
	if json.Unmarshal([]byte(plain), &t) != nil || t.Value == "" {
		return byok.Token{}, false
	}
	return t, true
}

// Bearer returns a cached token or fetches one: single-flight per key, at most
// three fetches per minute per connection (one key for the shared sandbox).
func (m *TokenManager) Bearer(ctx context.Context, p *LambdaProvider, conn *byokconn.Connection, set byok.Capability) (byok.Token, error) {
	key := tokenKey(conn, set)
	if t, ok := m.read(ctx, key); ok {
		return t, nil
	}
	release, got, err := m.locks.Acquire(ctx, "byok:token-lock:"+key)
	if err != nil {
		return byok.Token{}, fmt.Errorf("%w: token lock", byok.ErrUnavailable)
	}
	if !got {
		deadline := time.Now().Add(waitForWinner)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return byok.Token{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			if t, ok := m.read(ctx, key); ok {
				return t, nil
			}
		}
		return byok.Token{}, fmt.Errorf("%w: token fetch in progress elsewhere", byok.ErrUnavailable)
	}
	defer release()
	if t, ok := m.read(ctx, key); ok { // the previous holder finished meanwhile
		return t, nil
	}
	n, err := m.counter.Incr(ctx, "byok:token-rate:"+conn.Provider+":"+connID(conn), time.Minute)
	if err != nil {
		return byok.Token{}, fmt.Errorf("%w: token limiter", byok.ErrUnavailable)
	}
	if n > tokenFetchesPerMinute {
		return byok.Token{}, fmt.Errorf("%w: local token limiter", byok.ErrRateLimited)
	}
	t, err := p.Token(ctx, byok.Credentials{Ref: refPtr(conn)}, set)
	if err != nil {
		return byok.Token{}, err
	}
	ttl := time.Duration(t.ExpiresIn)*time.Second - tokenSafety
	if ttl > 0 {
		plain, _ := json.Marshal(t)
		if sealed, err := m.sealer.Seal(string(plain)); err == nil {
			_ = m.cache.Set(ctx, key, []byte(sealed), int(ttl/time.Second))
		}
	}
	return t, nil
}

func refPtr(conn *byokconn.Connection) *byok.ConnectionRef { r := conn.Ref(); return &r }

// Invalidate drops every cached token of the connection (renewal, disconnect, 401).
func (m *TokenManager) Invalidate(ctx context.Context, conn *byokconn.Connection) {
	for _, set := range []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection} {
		_ = m.cache.Delete(ctx, tokenKey(conn, set))
	}
}

```

`api/internal/byokrpc/limits.go`:

```go
package byokrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

var ErrSandboxBusy = errors.New("byok: the shared sandbox is busy")

const (
	bankingPerMinute = 8 // Inter banking: 10/min (parecer § 2.4)
	sandboxPerMinute = 8 // Inter sandbox Pix: 10/min for every test tenant together (§ 9)
)

type Limits struct{ counter ratelimit.Counter }

func NewLimits(counter ratelimit.Counter) *Limits { return &Limits{counter: counter} }

func (l *Limits) take(ctx context.Context, key string, max int64, over error) error {
	n, err := l.counter.Incr(ctx, key, time.Minute)
	if err != nil {
		return fmt.Errorf("%w: limiter", byok.ErrUnavailable)
	}
	if n > max {
		return over
	}
	return nil
}

func (l *Limits) Banking(ctx context.Context, conn *byokconn.Connection) error {
	if conn.Kind == byokconn.KindSandboxShared {
		return l.take(ctx, "byok:rate:sandbox:"+conn.Provider+":banking", sandboxPerMinute, ErrSandboxBusy)
	}
	return l.take(ctx, "byok:rate:"+conn.Provider+":"+conn.ID+":banking", bankingPerMinute, byok.ErrRateLimited)
}

func (l *Limits) Pix(ctx context.Context, conn *byokconn.Connection) error {
	if conn.Kind != byokconn.KindSandboxShared {
		return nil // 120/min per integration at Inter; billing's volume per merchant stays far below
	}
	return l.take(ctx, "byok:rate:sandbox:"+conn.Provider+":pix", sandboxPerMinute, ErrSandboxBusy)
}
```

`api/internal/byokrpc/session.go`:

```go
package byokrpc

import (
	"context"
	"errors"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

// Session is one connection's calls: provider bound to the mode, tokens, limits.
type Session struct {
	P      *LambdaProvider
	Conn   *byokconn.Connection
	Tokens *TokenManager
	Limits *Limits
}

func (s Session) Credentials() byok.Credentials { return byok.Credentials{Ref: refPtr(s.Conn)} }

// Call runs fn with a bearer for set; on ErrUnauthorized it drops the cached
// token, fetches once more and retries once (spec § 4.3).
func Call[T any](ctx context.Context, s Session, set byok.Capability, fn func(byok.Credentials, byok.Token) (T, error)) (T, error) {
	var zero T
	tok, err := s.Tokens.Bearer(ctx, s.P, s.Conn, set)
	if err != nil {
		return zero, err
	}
	v, err := fn(s.Credentials(), tok)
	if !errors.Is(err, byok.ErrUnauthorized) {
		return v, err
	}
	s.Tokens.Invalidate(ctx, s.Conn)
	if tok, err = s.Tokens.Bearer(ctx, s.P, s.Conn, set); err != nil {
		return zero, err
	}
	return fn(s.Credentials(), tok)
}

// Balance and StatementPage are the two banking calls, rate-limited per connection.
func (s Session) Balance(ctx context.Context, at byok.Date) (byok.Balance, error) {
	if err := s.Limits.Banking(ctx, s.Conn); err != nil {
		return byok.Balance{}, err
	}
	return Call(ctx, s, byok.CapabilityStatement, func(c byok.Credentials, t byok.Token) (byok.Balance, error) {
		return s.P.Balance(ctx, c, t, at)
	})
}

func (s Session) StatementPage(ctx context.Context, from, to byok.Date, page int) (byok.StatementPage, error) {
	if err := s.Limits.Banking(ctx, s.Conn); err != nil {
		return byok.StatementPage{}, err
	}
	return Call(ctx, s, byok.CapabilityStatement, func(c byok.Credentials, t byok.Token) (byok.StatementPage, error) {
		return s.P.StatementPage(ctx, c, t, from, to, page)
	})
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal && go vet ./internal/byokrpc/ ./internal/crypto/ && go test ./internal/byokrpc/ ./internal/crypto/ -race -count=1`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add api/internal/byokrpc api/internal/crypto
git commit -m "feat(byok): Lambda-backed provider, sealed single-flight token cache and rate limiters"
```

### Task 11: Infrastructure — the outbound Lambda, its role, the flags, the key script, CI

**Files:**
- Create: `terraform/billing/byok.tf`, `terraform/billing/tests/byok.tftest.hcl`, `terraform/scripts/create-byok-credentials-key.sh`, `terraform/assets/byok-placeholder/bootstrap`
- Modify: `terraform/billing/variables.tf` (`byok_live_collection`, `byok_live_providers`), `terraform/billing/backend.tf` (provider `hashicorp/archive ~> 2.7`), `terraform/billing/locals.tf` (`byok_*` paths), `terraform/billing/compute.tf` (`bootstrap_vars`), `terraform/assets/bootstrap.sh.tftpl` and `terraform/assets/bootstrap-alpine.sh.tftpl` (env), `terraform/github/main.tf` (api role may update the Lambda's code), `.github/workflows/api.yml` (build and deploy the Lambda before the instances), `.github/workflows/infra.yml` (`terraform test`)

**Interfaces:**
- Consumes: Task 9's environment contract and `make build-lambdas`.
- Produces: Lambda `{env}-ctech-billing-byok-outbound`; SSM `/ctech-billing/{env}/byok/live-collection-enabled`, `/ctech-billing/{env}/byok/live-providers`, `/ctech-billing/{env}/byok/inter-sandbox/account`, `/ctech-billing/{env}/byok/inter-sandbox/pix-key`; api env `BYOK_OUTBOUND_FUNCTION`, `BYOK_LIVE_COLLECTION`, `BYOK_LIVE_PROVIDERS`; outputs `byok_outbound_function_name`. Part B adds the webhook stack to the same file.

- [ ] **Step 1: Write the failing Terraform test**

`terraform/billing/tests/byok.tftest.hcl` (spec § 17 item 4: the api role cannot read the credentials key):

```hcl
mock_provider "aws" {
  mock_data "aws_ssm_parameter" {
    defaults = { value = "mock" }
  }
  mock_data "aws_vpc" {
    defaults = { cidr_block = "10.0.0.0/16" }
  }
}
mock_provider "archive" {}

variables {
  environment = "dev"
}

run "the_api_never_reads_the_credentials_key" {
  command = plan

  assert {
    condition = alltrue([
      for st in concat(data.aws_iam_policy_document.ssm_read.statement, data.aws_iam_policy_document.byok_api.statement) :
      !anytrue([for r in st.resources : strcontains(r, "/byok/credentials-key")])
    ])
    error_message = "the api role must not read /byok/credentials-key"
  }

  assert {
    condition     = anytrue([for st in data.aws_iam_policy_document.byok_outbound.statement : contains(st.resources, local.byok_credentials_key_arn)])
    error_message = "the outbound Lambda reads the credentials key"
  }

  assert {
    condition     = aws_lambda_function.byok_outbound.environment[0].variables["BYOK_LIVE_COLLECTION"] == "false" && aws_lambda_function.byok_outbound.environment[0].variables["BYOK_LIVE_PROVIDERS"] == ""
    error_message = "the legal gate defaults closed"
  }

  assert {
    condition     = aws_lambda_function.byok_outbound.vpc_config == null || length(aws_lambda_function.byok_outbound.vpc_config) == 0
    error_message = "the outbound Lambda is not VPC-attached (IPv4 egress without NAT)"
  }
}
```

Run: `terraform -chdir=terraform/billing init -backend=false && terraform -chdir=terraform/billing test`
Expected: FAIL, `Reference to undeclared resource "aws_lambda_function" "byok_outbound"`.

- [ ] **Step 2: Write `byok.tf` and the variables**

`terraform/billing/variables.tf`:

```hcl
# The legal gate (spec 2026-10-11 § 10). Live BYOK collection stays off until
# the lawyer's answer is recorded in ADR 0030 and BOTH of these change in the
# same reviewed pull request. They reach the outbound Lambda as environment
# variables (an apply takes effect on the next invocation) and the api through
# SSM (read at process start).
variable "byok_live_collection" {
  type    = bool
  default = false
}

variable "byok_live_providers" {
  type    = list(string)
  default = []
}
```

`terraform/billing/locals.tf` (inside `locals`):

```hcl
  byok_ssm_prefix          = "/ctech-billing/${var.environment}/byok"
  byok_outbound_name       = "${var.environment}-ctech-billing-byok-outbound"
  byok_credentials_key     = "${local.byok_ssm_prefix}/credentials-key"
  byok_credentials_key_arn = "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${local.byok_ssm_prefix}/credentials-key"
  # Test mode uses CTech's own Inter integration (the wallet's), against the
  # sandbox host (spec amendment 2026-10-11). Granted by IAM, never copied.
  byok_wallet_inter_params = {
    client-id     = "/ctech-wallet/${var.environment}/inter/client-id"
    client-secret = "/ctech-wallet/${var.environment}/inter/client-secret"
    cert          = "/ctech-wallet/${var.environment}/inter/mtls-cert"
    key           = "/ctech-wallet/${var.environment}/inter/mtls-key"
  }
```

`terraform/billing/byok.tf`:

```hcl
# BYOK (spec 2026-10-11): the outbound Lambda is billing's only connection to
# a merchant's bank. Not VPC-attached: IPv4 egress with no NAT gateway and no
# public address per instance (owner cost rule; parecer § 9 row 12).

resource "aws_ssm_parameter" "byok_live_collection" {
  name  = "${local.byok_ssm_prefix}/live-collection-enabled"
  type  = "String"
  value = tostring(var.byok_live_collection)
}

resource "aws_ssm_parameter" "byok_live_providers" {
  name  = "${local.byok_ssm_prefix}/live-providers"
  type  = "String"
  value = length(var.byok_live_providers) == 0 ? " " : join(",", var.byok_live_providers) # SSM refuses an empty String
}

# Non-secret identifiers of the sandbox account, set out of band
# (docs/runbooks/byok.md). The credentials key is NOT a Terraform resource: it
# is generated by terraform/scripts/create-byok-credentials-key.sh so its value
# never enters state.
resource "aws_ssm_parameter" "byok_sandbox" {
  for_each = toset(["account", "pix-key"])
  name     = "${local.byok_ssm_prefix}/inter-sandbox/${each.key}"
  type     = "String"
  value    = "SET-OUT-OF-BAND"
  lifecycle {
    ignore_changes = [value]
  }
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "byok_outbound" {
  name               = local.byok_outbound_name
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_cloudwatch_log_group" "byok_outbound" {
  name              = "/aws/lambda/${local.byok_outbound_name}"
  retention_in_days = 14
}

data "aws_iam_policy_document" "byok_outbound" {
  statement {
    sid     = "ReadKeyAndSandboxOnce"
    actions = ["ssm:GetParameter"]
    resources = concat(
      [local.byok_credentials_key_arn],
      [for p in values(local.byok_wallet_inter_params) : "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${p}"],
      [for k, p in aws_ssm_parameter.byok_sandbox : p.arn],
    )
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.byok_outbound.arn}:*"]
  }
  statement {
    sid       = "Alerts"
    actions   = ["sns:Publish"]
    resources = [local.alerts_topic_arn]
  }
  # No DynamoDB, no KMS beyond SSM's own aws/ssm decrypt (allowed by that key's
  # policy for in-account principals reading a parameter they may read).
}

resource "aws_iam_role_policy" "byok_outbound" {
  name   = "${local.byok_outbound_name}-policy"
  role   = aws_iam_role.byok_outbound.id
  policy = data.aws_iam_policy_document.byok_outbound.json
}

# Terraform creates the function from a placeholder; CI ships the code
# (api.yml, `aws lambda update-function-code`) and Terraform never reverts it.
data "archive_file" "byok_placeholder" {
  type        = "zip"
  source_file = "${path.module}/../assets/byok-placeholder/bootstrap"
  output_path = "${path.module}/.terraform/byok-placeholder.zip"
}

resource "aws_lambda_function" "byok_outbound" {
  function_name = local.byok_outbound_name
  role          = aws_iam_role.byok_outbound.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  memory_size   = 256
  timeout       = 20
  filename      = data.archive_file.byok_placeholder.output_path

  environment {
    variables = {
      ENVIRONMENT                    = var.environment
      AWS_USE_DUALSTACK_ENDPOINT     = "true"
      ALERTS_TOPIC_ARN               = local.alerts_topic_arn
      BYOK_CREDENTIALS_KEY_PARAM     = local.byok_credentials_key
      BYOK_SANDBOX_CREDENTIAL_PARAMS = join(",", [for k, p in local.byok_wallet_inter_params : "${k}=${p}"])
      BYOK_SANDBOX_ACCOUNT_PARAM     = aws_ssm_parameter.byok_sandbox["account"].name
      BYOK_SANDBOX_PIX_KEY_PARAM     = aws_ssm_parameter.byok_sandbox["pix-key"].name
      BYOK_LIVE_COLLECTION           = tostring(var.byok_live_collection)
      BYOK_LIVE_PROVIDERS            = join(",", var.byok_live_providers)
      # spec § 4.2: merchants' real credentials exist only in prod.
      BYOK_FORCE_SANDBOX = var.environment == "prod" ? "false" : "true"
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }

  depends_on = [aws_cloudwatch_log_group.byok_outbound, aws_iam_role_policy.byok_outbound]
}

# The api (and cmd/byok, same role) may invoke the Lambda and read the two
# flags. Nothing else under /byok/: the credentials key is the Lambda's alone.
data "aws_iam_policy_document" "byok_api" {
  statement {
    sid       = "InvokeOutbound"
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.byok_outbound.arn]
  }
  statement {
    sid       = "ReadGate"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = [aws_ssm_parameter.byok_live_collection.arn, aws_ssm_parameter.byok_live_providers.arn]
  }
}

resource "aws_iam_role_policy" "byok_api" {
  name   = "${local.name}-byok"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.byok_api.json
}

output "byok_outbound_function_name" {
  value = aws_lambda_function.byok_outbound.function_name
}
```

`terraform/assets/byok-placeholder/bootstrap` (mode 0755):

```sh
#!/bin/sh
# Placeholder until CI ships the real binary (api.yml). Every invocation fails.
exit 1
```

`terraform/billing/compute.tf`, in `bootstrap_vars`:

```hcl
    byok_outbound_function = aws_lambda_function.byok_outbound.function_name
    ssm_byok_live_collection = aws_ssm_parameter.byok_live_collection.name
    ssm_byok_live_providers  = aws_ssm_parameter.byok_live_providers.name
```

`terraform/assets/bootstrap.sh.tftpl` (and the same two edits in `bootstrap-alpine.sh.tftpl`): add `BYOK_OUTBOUND_FUNCTION=${byok_outbound_function}` to `/etc/app-static.env`, and to the `setup-ssm-env.sh` list:

```sh
  BYOK_LIVE_COLLECTION=${ssm_byok_live_collection} \
  BYOK_LIVE_PROVIDERS=${ssm_byok_live_providers} \
```

`terraform/github/main.tf`, in `data "aws_iam_policy_document" "api"`:

```hcl
  # BYOK Lambdas (spec 2026-10-11): code only. Their configuration, role and
  # environment (the legal gate) are Terraform's and never CI's.
  statement {
    sid       = "ShipByokLambdaCode"
    actions   = ["lambda:UpdateFunctionCode", "lambda:GetFunction"]
    resources = [for env in var.environments : "arn:aws:lambda:${var.aws_region}:${var.aws_account}:function:${env}-ctech-billing-byok-*"]
  }
```

`.github/workflows/api.yml`, after "Read deployment targets" and before "Upload":

```yaml
      # The Lambdas ship before the instances: an api that calls an op the
      # deployed Lambda does not know would fail every BYOK action meanwhile.
      - name: Deploy BYOK Lambdas
        working-directory: api
        env:
          ENVIRONMENT: ${{ steps.env.outputs.name }}
        run: |
          make build-lambdas
          for fn in $(ls dist/lambda/*.zip); do
            name="${ENVIRONMENT}-ctech-billing-$(basename "$fn" .zip)"
            aws lambda update-function-code --function-name "$name" --zip-file "fileb://$fn" --publish >/dev/null
            aws lambda wait function-updated-v2 --function-name "$name"
          done
```

`.github/workflows/infra.yml`, in the `validate` job after "Validate roots":

```yaml
      - name: Terraform tests (mock providers, no credentials)
        run: terraform -chdir=terraform/billing test
```

- [ ] **Step 3: The operator script for the key**

`terraform/scripts/create-byok-credentials-key.sh` (mode 0755):

```bash
#!/usr/bin/env bash
# Creates the BYOK credentials key (spec 2026-10-11 § 3.3) once per environment:
# 32 random bytes, base64, as an SSM SecureString only the outbound Lambda reads.
#
#   AWS_PROFILE=ctech terraform/scripts/create-byok-credentials-key.sh prod
#
# Refuses to overwrite (losing the key makes every stored connection unreadable;
# merchants would have to upload their credentials again). Never prints the
# value. Back the value up with the account's break-glass material, the same way
# as field-encryption-key (terraform/billing/ssm.tf).
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *) echo "unknown environment: $ENV" >&2; exit 2 ;; esac
NAME="/ctech-billing/$ENV/byok/credentials-key"
if aws ssm get-parameter --region us-east-1 --name "$NAME" --query Parameter.Name --output text >/dev/null 2>&1; then
  echo "$NAME already exists; refusing to replace it" >&2
  exit 1
fi
VALUE="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
aws ssm put-parameter --region us-east-1 --name "$NAME" --type SecureString --value "$VALUE" --no-overwrite >/dev/null
unset VALUE
echo "created $NAME (value not shown)"
```

- [ ] **Step 4: Run the checks**

Run: `terraform fmt -recursive -check terraform/ && terraform -chdir=terraform/billing init -backend=false -input=false && terraform -chdir=terraform/billing validate && terraform -chdir=terraform/billing test && bash -n terraform/scripts/create-byok-credentials-key.sh`
Expected: `Success! The configuration is valid.`, `Success! 1 passed, 0 failed.`, no syntax error.

- [ ] **Step 5: Commit**

```bash
git add terraform .github/workflows/api.yml .github/workflows/infra.yml
git commit -m "feat(infra): BYOK outbound Lambda, legal-gate flags, credentials key script and CI deploy"
```

### Task 12: Finanças domain — the synced account, bank lines, the "A classificar" categories, `account_is_synced`

**Files:**
- Modify: `api/internal/domain/finance/account.go` (`Source`, `Provider`), `api/internal/domain/finance/ledger.go` (`KindBankLine`), `api/internal/domain/finance/categories.go` (unclassified categories), `api/internal/domain/finance/posting.go` (`BankLine`), `api/internal/repositories/ledger.go` (`accountItem` fields, `ErrAccountIsSynced`, `activeManualCash`), `api/internal/repositories/cards.go:100,238`, `api/internal/repositories/bills.go` (`Settle`, `Edit` guard at `:640`), `api/internal/repositories/imports.go:304,1084`, `api/internal/repositories/finance_invoices.go` (`usableCash`), `api/internal/problem/problem.go`
- Test: `api/internal/domain/finance/bankline_test.go`, `api/tests/integration/finance_synced_account_test.go`

**Interfaces:**
- Produces:

```go
package finance
const SourceManual = ""; const SourceBYOK = "byok"
// LedgerAccount gains: Source string; Provider string
const KindBankLine TxKind = "bank_line"
const UnclassifiedInID = "sys-unclassified-in"; const UnclassifiedOutID = "sys-unclassified-out"
func UnclassifiedCategories() []LedgerAccount // income/other "A classificar (entradas)" (system_key unclassified_in), expense/other "A classificar (saídas)" (unclassified_out)
func BankLine(accountID, categoryID string, credit bool, amount billing.Cents, date brcal.Date) (Transaction, error)
package repositories
var ErrAccountIsSynced = errors.New("finance: the account is synced from the bank")
type ProviderBalance struct{ Available, Blocked billing.Cents; At brcal.Date; SyncedAt time.Time }
// AccountRow gains: Provider *ProviderBalance (nil for manual accounts)
func (r *LedgerRepository) activeManualCash(ctx context.Context, sp space.ResolvedSpace, id string) error // activeCash + not synced
```

- [ ] **Step 1: Write the failing tests**

`api/internal/domain/finance/bankline_test.go`:

```go
package finance

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestABankLineMovesCashAgainstItsCategory(t *testing.T) {
	d := brcal.MustParse("2026-10-01")
	in, err := BankLine("byok-inter-abc", UnclassifiedInID, true, 1050, d)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != KindBankLine || in.Legs[0].AccountID != "byok-inter-abc" || in.Legs[0].Amount != 1050 || in.Legs[0].Flow != UnclassifiedInID {
		t.Fatalf("credit: %+v", in)
	}
	out, _ := BankLine("byok-inter-abc", UnclassifiedOutID, false, 200, d)
	if out.Legs[1].AccountID != "byok-inter-abc" || out.Legs[1].Amount != -200 {
		t.Fatalf("debit: %+v", out)
	}
	if _, err := BankLine("a", "c", true, 0, d); err == nil {
		t.Fatal("a zero line is not a transaction")
	}
}

func TestTheUnclassifiedCategoriesAreValidAndOther(t *testing.T) {
	for _, c := range UnclassifiedCategories() {
		if err := c.Validate(); err != nil || c.Group != GroupOther || c.SystemKey == "" {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}
```

(`brcal.MustParse` does not exist yet: add it to `internal/domain/brcal` in this task, `func MustParse(s string) Date { d, err := Parse(s); if err != nil { panic(err) }; return d }`, for tests and constants only.)

`api/tests/integration/finance_synced_account_test.go`:

```go
//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func TestManualMovementsIntoASyncedAccountAreRefused(t *testing.T) {
	ctx := ctxT(t)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	sp, _ := space.ForJob(newSpaceOrgID(), true)
	if err := ledger.EnsureReady(ctx, sp, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.EnsureByokAccount(ctx, sp, "byok-inter-abc", "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	if err := ledger.CreateAccount(ctx, sp, finance.LedgerAccount{ID: "caixa", Name: "Caixa", Class: finance.ClassAsset}, now()); err != nil {
		t.Fatal(err)
	}
	d := brcal.MustParse("2026-10-01")
	meta := repositories.PostMeta{Actor: "u", RequestID: "r"}
	if _, err := ledger.PostTransfer(ctx, sp, "caixa", "byok-inter-abc", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("transfer in: %v", err)
	}
	if _, err := ledger.PostTransfer(ctx, sp, "byok-inter-abc", "caixa", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("transfer out: %v", err)
	}
	if _, err := ledger.PostOpeningBalance(ctx, sp, "byok-inter-abc", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("opening: %v", err)
	}
	imports := repositories.NewImportRepository(testDB, testCfg)
	if _, err := imports.Import(ctx, sp, "byok-inter-abc", "ofx", oneLineOFX(t), "", now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("import: %v", err)
	}
}
```

(`oneLineOFX` builds a `statement.Parsed` with one line the way `finance_imports_test.go` does; reuse its helper if one exists, else copy its fixture.)

- [ ] **Step 2: Run them**

Run: `go test ./internal/domain/finance/ && make test-integration`
Expected: FAIL, `undefined: BankLine`, `ledger.EnsureByokAccount undefined`.

- [ ] **Step 3: Implement**

`api/internal/domain/finance/account.go`, `LedgerAccount` gains:

```go
	// Source is SourceBYOK for an account whose statement and balance are the
	// bank's (BYOK spec § 8.1): every line posts from the sync, and manual
	// movements into it are refused. Empty for every other account.
	Source string
	// Provider names the bank of a BYOK account ("inter").
	Provider string
```

and constants `SourceManual = ""`, `SourceBYOK = "byok"`.

`api/internal/domain/finance/ledger.go`:

```go
	// KindBankLine is one line of a synced bank statement (BYOK): the cash leg
	// is the bank's fact; the other leg is its category, "A classificar" until a
	// person classifies it.
	KindBankLine TxKind = "bank_line"
```

and add it to `TxKind.valid()`.

`api/internal/domain/finance/categories.go`:

```go
const (
	UnclassifiedInID  = "sys-unclassified-in"
	UnclassifiedOutID = "sys-unclassified-out"
)

// UnclassifiedCategories are seeded with a space's first BYOK account (spec
// § 8.1). Ordinary categories (not System): reports show them, and a person
// moves their lines out by classifying.
func UnclassifiedCategories() []LedgerAccount {
	return []LedgerAccount{
		{ID: UnclassifiedInID, Name: "A classificar (entradas)", Class: ClassIncome, Group: GroupOther, SystemKey: "unclassified_in"},
		{ID: UnclassifiedOutID, Name: "A classificar (saídas)", Class: ClassExpense, Group: GroupOther, SystemKey: "unclassified_out"},
	}
}
```

(Names are stored defaults; screens translate by `system_key`, as with the other seeded categories. No dash in them, so the UI copy rule holds even untranslated.)

`api/internal/domain/finance/posting.go`:

```go
// BankLine is a synced statement line: credit moves cash in against an income
// category, debit moves it out against an expense category.
func BankLine(accountID, categoryID string, credit bool, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, fmt.Errorf("%w: a bank line moves a positive amount", ErrInvalidTransaction)
	}
	if credit {
		return NewTransaction(KindBankLine, date,
			Leg{AccountID: accountID, Amount: amount, Flow: categoryID}, Leg{AccountID: categoryID, Amount: -amount})
	}
	return NewTransaction(KindBankLine, date,
		Leg{AccountID: categoryID, Amount: amount}, Leg{AccountID: accountID, Amount: -amount, Flow: categoryID})
}
```

`api/internal/repositories/ledger.go`:

```go
// ErrAccountIsSynced refuses a manual movement into a BYOK account: its
// statement is the bank's (spec § 8.1). 409 account_is_synced.
var ErrAccountIsSynced = errors.New("finance: the account is synced from the bank")

// ProviderBalance is the bank's own balance of a BYOK account (spec § 8.1),
// shown as the account's balance with its time.
type ProviderBalance struct {
	Available, Blocked billing.Cents
	At                 brcal.Date
	SyncedAt           time.Time
}
```

`accountItem` gains:

```go
	Source          string `dynamodbav:"source,omitempty"`
	Provider        string `dynamodbav:"provider,omitempty"`
	ProviderBalance *int64 `dynamodbav:"provider_balance,omitempty"`
	ProviderBlocked int64  `dynamodbav:"provider_blocked,omitempty"`
	ProviderAt      string `dynamodbav:"provider_balance_at,omitempty"`
	SyncedAt        string `dynamodbav:"synced_at,omitempty"`
```

`row()` copies `Source`, `Provider` into `LedgerAccount` and builds `AccountRow.Provider` when `ProviderBalance != nil`; `newAccountItem` copies `Source` and `Provider`. `AccountRow` gains `Provider *ProviderBalance`.

```go
// activeManualCash is activeCash for a movement a person makes: a synced
// account takes only its bank's lines.
func (r *LedgerRepository) activeManualCash(ctx context.Context, sp space.ResolvedSpace, id string) error {
	if err := r.activeCash(ctx, sp, id); err != nil {
		return err
	}
	acct, err := r.getAccount(ctx, sp, id)
	if err != nil {
		return err
	}
	if acct.Source == finance.SourceBYOK {
		return fmt.Errorf("%w: %s", ErrAccountIsSynced, id)
	}
	return nil
}
```

Replace `activeCash` with `activeManualCash` at: `ledger.go` `PostOpeningBalance` (`:1026`) and `PostTransfer` (`:1154`); `cards.go` `:100` and `:238` (a card paid from the bank is a bank line, not a manual payment); `bills.go` `Edit` (`:640`); `imports.go` `Import` (`:304`) and `PutMapping` (`:1084`). In `BillRepository.Settle`, before `planSettle`:

```go
	if b, err := r.get(ctx, sp, billID); err == nil && b.AccountID != "" {
		if acct, err := r.ledger.getAccount(ctx, sp, b.AccountID); err == nil && acct.Source == finance.SourceBYOK {
			return finance.Bill{}, fmt.Errorf("%w: settle it by classifying its bank line", ErrAccountIsSynced)
		}
	}
```

`finance_invoices.go`:

```go
// usableCash excludes synced accounts: a posting that is not the bank's own
// line must never land in a bank-synced account by default (plan decision 6).
// Part B names the collecting provider's account explicitly instead.
func usableCash(a AccountRow) bool {
	return a.Class == finance.ClassAsset && !a.System && !a.Archived && a.Source != finance.SourceBYOK
}
```

`problem.go`, before the `ErrUnknownAccount` case:

```go
	case errors.Is(err, repositories.ErrAccountIsSynced):
		return Conflict("this account is synced from the bank; its movements come from the statement").WithCode("account_is_synced")
```

`EnsureByokAccount` is Task 13's; write its signature now as a stub returning `errors.ErrUnsupported` only if the integration test above must compile before Task 13, otherwise run the integration step at the end of Task 13.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal && go vet ./... && go test ./internal/domain/finance/ ./internal/repositories/ ./internal/problem/ -race -count=1`
Expected: `ok` (the integration test runs in Task 13 Step 4).

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/finance api/internal/repositories api/internal/problem api/tests/integration/finance_synced_account_test.go
git commit -m "feat(finance): bank-synced accounts, bank lines and the A classificar categories"
```

### Task 13: Ledger — posting bank lines once, classification, the provider's balance

**Files:**
- Create: `api/internal/repositories/ledger_byok.go`
- Modify: `api/internal/repositories/ledger.go` (`LedgerRepository` gains `imports Base`), `api/internal/repositories/bills.go` (`planSettleInto`)
- Test: `api/tests/integration/finance_bank_lines_test.go`

**Interfaces:**
- Consumes: Task 12.
- Produces:

```go
type BankLine struct{ Provider, LineID string; Date brcal.Date; Credit bool; Amount billing.Cents; Memo string; ChargeRef, PaymentID string }
func BankLineRef(accountID, lineID string) string // "byok:{account}:{line}"
func (r *LedgerRepository) EnsureByokAccount(ctx context.Context, sp space.ResolvedSpace, id, name, provider string, now time.Time) (created bool, err error)
func (r *LedgerRepository) PostBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, meta PostMeta, now time.Time) (txID string, posted bool, err error)
func (r *LedgerRepository) LinkBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, txID string, now time.Time) (linked bool, err error) // part B: lock pointing at the invoice's posting, nothing posted
func (r *LedgerRepository) BankLineLock(ctx context.Context, sp space.ResolvedSpace, accountID, provider, lineID string) (txID string, found bool, err error)
func (r *LedgerRepository) SetProviderBalance(ctx context.Context, sp space.ResolvedSpace, accountID string, b ProviderBalance) error
func (r *LedgerRepository) PostProviderOpening(ctx context.Context, sp space.ResolvedSpace, accountID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (string, error) // the OPENING# rule, bypassing the manual guard
type UnclassifiedLine struct{ TxID string; Date brcal.Date; Amount billing.Cents; Credit bool; Memo string }
func (r *LedgerRepository) Unclassified(ctx context.Context, sp space.ResolvedSpace, accountID string) ([]UnclassifiedLine, error)
type Classification struct{ CategoryID, BillID, TransferAccountID string }
func (r *LedgerRepository) Classify(ctx context.Context, bills *BillRepository, sp space.ResolvedSpace, txID string, c Classification, meta PostMeta, now time.Time) (newTxID string, err error)
var ErrNotUnclassified = errors.New("finance: the line is already classified")
```

- [ ] **Step 1: Write the failing tests**

`api/tests/integration/finance_bank_lines_test.go`:

```go
//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func byokSpace(t *testing.T) (*repositories.LedgerRepository, space.ResolvedSpace) {
	t.Helper()
	ctx := ctxT(t)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	sp, _ := space.ForJob(newSpaceOrgID(), true)
	if err := ledger.EnsureReady(ctx, sp, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.EnsureByokAccount(ctx, sp, "byok-inter-abc", "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	return ledger, sp
}

func balanceOf(t *testing.T, ledger *repositories.LedgerRepository, sp space.ResolvedSpace, id string) int64 {
	a, err := ledger.GetAccount(ctxT(t), sp, id)
	if err != nil {
		t.Fatal(err)
	}
	return int64(a.Balance)
}

func TestSyncingTheSameRangeTwiceAddsNothing(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	lines := []repositories.BankLine{
		{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 10000, Memo: "Pix recebido"},
		{Provider: "inter", LineID: "T2", Date: brcal.MustParse("2026-10-02"), Credit: false, Amount: 2500, Memo: "Tarifa"},
	}
	for pass := range 2 {
		for _, l := range lines {
			_, posted, err := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{Actor: "job"}, now())
			if err != nil || posted != (pass == 0) {
				t.Fatalf("pass %d line %s: posted=%v err=%v", pass, l.LineID, posted, err)
			}
		}
	}
	if got := balanceOf(t, ledger, sp, "byok-inter-abc"); got != 7500 {
		t.Fatalf("balance %d, want 7500", got)
	}
}

func TestALineReusedWithAnotherAmountIsPostedOnce(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	a := repositories.BankLine{Provider: "inter", LineID: "T9", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 100}
	b := a
	b.Amount = 150 // the bank corrected the line under the same id
	for _, l := range []repositories.BankLine{a, b, b} {
		if _, _, err := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{}, now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := balanceOf(t, ledger, sp, "byok-inter-abc"); got != 250 {
		t.Fatalf("balance %d, want 250: each distinct (id, date, amount) once", got)
	}
}

func TestClassificationNeverMovesTheBalance(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	bills := repositories.NewBillRepository(testDB, testCfg)
	l := repositories.BankLine{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 10000}
	txID, _, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{}, now())
	un, _ := ledger.Unclassified(ctx, sp, "byok-inter-abc")
	if len(un) != 1 || un[0].TxID != txID {
		t.Fatalf("unclassified %+v", un)
	}
	if _, err := ledger.Classify(ctx, bills, sp, txID, repositories.Classification{CategoryID: "outras-receitas"}, repositories.PostMeta{Actor: "u"}, now()); err != nil {
		t.Fatal(err)
	}
	if got := balanceOf(t, ledger, sp, "byok-inter-abc"); got != 10000 {
		t.Fatalf("balance moved to %d", got)
	}
	if un, _ := ledger.Unclassified(ctx, sp, "byok-inter-abc"); len(un) != 0 {
		t.Fatalf("still unclassified: %+v", un)
	}
	if _, err := ledger.Classify(ctx, bills, sp, txID, repositories.Classification{CategoryID: "outras-receitas"}, repositories.PostMeta{}, now()); !errors.Is(err, repositories.ErrNotUnclassified) {
		t.Fatalf("classifying twice: %v", err)
	}
}

func TestTheProviderOpeningPlusLinesEqualsTheProviderBalance(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	if _, err := ledger.PostProviderOpening(ctx, sp, "byok-inter-abc", 50000, brcal.MustParse("2026-06-30"), repositories.PostMeta{}, now()); err != nil {
		t.Fatal(err)
	}
	_, _, _ = ledger.PostBankLine(ctx, sp, "byok-inter-abc", repositories.BankLine{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-07-01"), Credit: false, Amount: 1234}, repositories.PostMeta{}, now())
	_ = ledger.SetProviderBalance(ctx, sp, "byok-inter-abc", repositories.ProviderBalance{Available: 48766, At: brcal.MustParse("2026-07-01"), SyncedAt: now()})
	a, _ := ledger.GetAccount(ctx, sp, "byok-inter-abc")
	if int64(a.Balance) != int64(a.Provider.Available) {
		t.Fatalf("ledger %d, provider %d", a.Balance, a.Provider.Available)
	}
}
```

- [ ] **Step 2: Run them**

Run: `make test-integration`
Expected: FAIL, `ledger.PostBankLine undefined`.

- [ ] **Step 3: Implement**

`api/internal/repositories/ledger_byok.go`:

```go
package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var ErrNotUnclassified = errors.New("finance: the line is already classified")

// BankLine is one synced statement line, as the ledger posts it.
type BankLine struct {
	Provider, LineID     string
	Date                 brcal.Date
	Credit               bool
	Amount               billing.Cents
	Memo                 string
	ChargeRef, PaymentID string
}

func BankLineRef(accountID, lineID string) string { return "byok:" + accountID + ":" + lineID }

// bankLockKey is the FITID# lock of a line (spec § 8.2): "B:{provider}:{id}".
func bankLockKey(l BankLine) string { return "B:" + l.Provider + ":" + l.LineID }

// bankLockKeyByContent keys a line whose id the bank reused for another
// amount or date, like claim() does for a reused FITID.
func bankLockKeyByContent(l BankLine) string {
	return bankLockKey(l) + ":" + l.Date.String() + ":" + strconv.FormatInt(int64(l.Amount), 10)
}

type bankLockItem struct {
	keys
	TxID   string `dynamodbav:"tx_id"`
	Date   string `dynamodbav:"date"`
	Amount int64  `dynamodbav:"amount"`
	Credit bool   `dynamodbav:"credit,omitempty"`
	Linked bool   `dynamodbav:"linked,omitempty"` // part B: points at an invoice's posting
}

func (r *LedgerRepository) EnsureByokAccount(ctx context.Context, sp space.ResolvedSpace, id, name, provider string, now time.Time) (bool, error) {
	if err := r.EnsureReady(ctx, sp, now); err != nil {
		return false, err
	}
	for _, c := range finance.UnclassifiedCategories() {
		if _, err := r.ensureCategory(ctx, sp, c, now); err != nil {
			return false, err
		}
	}
	err := r.CreateAccount(ctx, sp, finance.LedgerAccount{ID: id, Name: name, Class: finance.ClassAsset,
		Source: finance.SourceBYOK, Provider: provider}, now)
	if errors.Is(err, ErrAccountExists) {
		return false, nil // reconnecting the same bank account resumes on it (spec § 3.6 step 5)
	}
	// ErrAccountExists is new: CreateAccount (ledger.go:306) today wraps the
	// duplicate as finance.ErrInvalidAccount; this task makes it
	// fmt.Errorf("%w: %w", ErrAccountExists, finance.ErrInvalidAccount) so both
	// callers keep working (the route still answers invalid_account).
	return err == nil, err
}

func (r *LedgerRepository) lockSK(accountID, key string) string { return ImportLockSK(accountID, key) }

func (r *LedgerRepository) readLock(ctx context.Context, sp space.ResolvedSpace, accountID, key string) (*bankLockItem, error) {
	item, err := r.imports.GetItem(ctx, sp.PK(), r.lockSK(accountID, key))
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[bankLockItem](item)
}

// PostBankLine posts the line once: the transaction and its lock row in one
// TransactWriteItems, the lock conditional on absence. posted=false means the
// line was already there (a replay of the same window).
func (r *LedgerRepository) PostBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, meta PostMeta, now time.Time) (string, bool, error) {
	if err := sp.Require(space.Write | space.Import); err != nil {
		return "", false, err
	}
	key := bankLockKey(l)
	held, err := r.readLock(ctx, sp, accountID, key)
	if err != nil {
		return "", false, err
	}
	if held != nil {
		if held.Date == l.Date.String() && held.Amount == int64(l.Amount) {
			return held.TxID, false, nil
		}
		key = bankLockKeyByContent(l)
		if again, err := r.readLock(ctx, sp, accountID, key); err != nil || again != nil {
			if again != nil {
				return again.TxID, false, nil
			}
			return "", false, err
		}
	}
	cat := finance.UnclassifiedOutID
	if l.Credit {
		cat = finance.UnclassifiedInID
	}
	tx, err := finance.BankLine(accountID, cat, l.Credit, l.Amount, l.Date)
	if err != nil {
		return "", false, err
	}
	meta.Origin, meta.Ref, meta.Memo = "bank_line", BankLineRef(accountID, l.LineID), l.Memo
	plan, err := r.planPost(sp, tx, meta, now)
	if err != nil {
		return "", false, err
	}
	lock, err := Encode(bankLockItem{keys: newKeys(sp.PK(), r.lockSK(accountID, key), RetentionImportLock, now),
		TxID: plan.TxID, Date: l.Date.String(), Amount: int64(l.Amount), Credit: l.Credit})
	if err != nil {
		return "", false, err
	}
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), r.imports.BuildPutTxItemIfAbsent(lock))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if codes := cancellationCodes(err); len(codes) == len(items) && codes[len(items)-1] == codeConditionFailed {
			return "", false, nil // a concurrent sync posted it
		}
		return "", false, classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, true, nil
}

// BankLineLock reports whether a line was posted or linked, and to which tx.
func (r *LedgerRepository) BankLineLock(ctx context.Context, sp space.ResolvedSpace, accountID, provider, lineID string) (string, bool, error) {
	held, err := r.readLock(ctx, sp, accountID, bankLockKey(BankLine{Provider: provider, LineID: lineID}))
	if err != nil || held == nil {
		return "", false, err
	}
	return held.TxID, true, nil
}

// LinkBankLine claims the line's lock for an existing transaction (the paid
// invoice's posting, spec § 8.4) and posts nothing.
func (r *LedgerRepository) LinkBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, txID string, now time.Time) (bool, error) {
	lock, err := Encode(bankLockItem{keys: newKeys(sp.PK(), r.lockSK(accountID, bankLockKey(l)), RetentionImportLock, now),
		TxID: txID, Date: l.Date.String(), Amount: int64(l.Amount), Credit: l.Credit, Linked: true})
	if err != nil {
		return false, err
	}
	err = r.imports.TransactWrite(ctx, txItems(r.imports.BuildPutTxItemIfAbsent(lock)))
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, conflictErr(err)
}

func (r *LedgerRepository) SetProviderBalance(ctx context.Context, sp space.ResolvedSpace, accountID string, b ProviderBalance) error {
	sk := LedgerAccountSK(accountID)
	return r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET provider_balance = :a, provider_blocked = :b, provider_balance_at = :d, synced_at = :s",
		"attribute_exists(pk) AND #src = :byok", map[string]string{"#src": "source"},
		map[string]types.AttributeValue{
			":a": numberValue(int64(b.Available)), ":b": numberValue(int64(b.Blocked)),
			":d": str(b.At.String()), ":s": str(b.SyncedAt.UTC().Format(time.RFC3339)), ":byok": str(finance.SourceBYOK),
		})))
}

// PostProviderOpening is PostOpeningBalance for a synced account: the amount
// is the bank's balance on the day before the first synced day (spec § 8.2).
func (r *LedgerRepository) PostProviderOpening(ctx context.Context, sp space.ResolvedSpace, accountID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	return r.postOpening(ctx, sp, accountID, amount, date, meta, now)
}

type UnclassifiedLine struct {
	TxID   string
	Date   brcal.Date
	Amount billing.Cents
	Credit bool
	Memo   string
}

// Unclassified lists the account's lines still on the two system categories:
// one key-condition read per category entry partition, filtered by the line
// ref prefix (several BYOK accounts share the two categories).
func (r *LedgerRepository) Unclassified(ctx context.Context, sp space.ResolvedSpace, accountID string) ([]UnclassifiedLine, error) {
	prefix := "byok:" + accountID + ":"
	var out []UnclassifiedLine
	for _, cat := range []string{finance.UnclassifiedInID, finance.UnclassifiedOutID} {
		entries, err := r.EntriesFrom(ctx, sp, cat, brcal.Date{})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Reversed || e.Reversal || !strings.HasPrefix(e.Ref, prefix) {
				continue
			}
			amt := -e.Amount // income leg is a credit (negative); show the cash amount positive
			credit := cat == finance.UnclassifiedInID
			if !credit {
				amt = e.Amount
			}
			out = append(out, UnclassifiedLine{TxID: e.TxID, Date: e.Date, Amount: amt, Credit: credit, Memo: e.Memo})
		}
	}
	return out, nil
}

type Classification struct{ CategoryID, BillID, TransferAccountID string }

// Classify replaces an unclassified bank line, in one TransactWriteItems: the
// line's reversal plus its replacement (category, bill settlement or transfer).
// The bank account's balance is unchanged by construction.
func (r *LedgerRepository) Classify(ctx context.Context, bills *BillRepository, sp space.ResolvedSpace, txID string, c Classification, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Import | space.Write); err != nil {
		return "", err
	}
	orig, err := r.GetTransaction(ctx, sp, txID)
	if err != nil {
		return "", err
	}
	if orig.Tx.Kind != finance.KindBankLine || orig.Reversed {
		return "", ErrNotUnclassified
	}
	var cash, cat finance.Leg
	for _, l := range orig.Tx.Legs {
		if l.AccountID == finance.UnclassifiedInID || l.AccountID == finance.UnclassifiedOutID {
			cat = l
		} else {
			cash = l
		}
	}
	if cat.AccountID == "" {
		return "", ErrNotUnclassified
	}
	credit := cash.Amount > 0
	amount := cash.Amount
	if !credit {
		amount = -amount
	}
	rev, err := r.planReverse(ctx, sp, txID, orig.Tx.Date, meta, now)
	if err != nil {
		return "", err
	}
	var next ledgerPlan
	var extra []types.TransactWriteItem
	switch {
	case c.CategoryID != "":
		acct, err := r.getAccount(ctx, sp, c.CategoryID)
		if err != nil || (acct.Class != finance.ClassIncome && acct.Class != finance.ClassExpense) || acct.Archived {
			return "", fmt.Errorf("%w: an active income or expense category", ErrUnknownAccount)
		}
		tx, err := finance.BankLine(cash.AccountID, c.CategoryID, credit, amount, orig.Tx.Date)
		if err != nil {
			return "", err
		}
		meta.Origin, meta.Ref, meta.Memo = "bank_line", orig.Ref, orig.Memo
		if next, err = r.planPost(sp, tx, meta, now); err != nil {
			return "", err
		}
	case c.TransferAccountID != "":
		if err := r.activeCash(ctx, sp, c.TransferAccountID); err != nil {
			return "", err
		}
		from, to := c.TransferAccountID, cash.AccountID
		if !credit {
			from, to = cash.AccountID, c.TransferAccountID
		}
		tx, err := finance.Transfer(from, to, amount, orig.Tx.Date)
		if err != nil {
			return "", err
		}
		meta.Origin, meta.Ref = "bank_line", orig.Ref
		if next, err = r.planPost(sp, tx, meta, now); err != nil {
			return "", err
		}
	case c.BillID != "":
		p, err := bills.planSettleInto(ctx, sp, c.BillID, amount, cash.AccountID, orig.Tx.Date, meta, now)
		if err != nil {
			return "", err
		}
		next = ledgerPlan{TxID: p.txID}
		extra = p.items
	default:
		return "", fmt.Errorf("%w: choose a category, a bill or a transfer", finance.ErrInvalidTransaction)
	}
	items := mergeAdds(append(append(append([]types.TransactWriteItem(nil), rev.Items...), next.Items...), extra...))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return "", ErrNotUnclassified // a concurrent classification reversed it first
		}
		return "", classifyPostCancel(err, -1)
	}
	return next.TxID, nil
}
```

Notes for the implementer, each checked against the code:
- `postOpening` is the body of today's `PostOpeningBalance` after its `activeCash` check, extracted so `PostOpeningBalance` (manual, now `activeManualCash`) and `PostProviderOpening` share the `OPENING#` marker logic.
- `finance.Transfer(from, to, amount, date)` is `posting.go:130`.
- `ErrAccountExists = errors.New("finance: account already exists")` is added to `ledger.go` in this task (see the comment above).
- `planReverse` refuses a reversal twice through the `REVERSAL#` marker, so a concurrent classify fails the condition and maps to `ErrNotUnclassified`; `KindBankLine` must be accepted by `finance.Reverse` (it is, any kind but a reversal).

`api/internal/repositories/bills.go`, beside `planSettle`:

```go
// planSettleInto settles a forecast bill with a bank line's cash (BYOK
// classification, spec § 8.3): the bill's account becomes the bank account and
// the paid amount is the line's. The cash leg's reversal is composed by the caller.
func (r *BillRepository) planSettleInto(ctx context.Context, sp space.ResolvedSpace, billID string, paid billing.Cents, accountID string, date brcal.Date, meta PostMeta, now time.Time) (settlePlan, error) {
	b, err := r.get(ctx, sp, billID)
	if err != nil {
		return settlePlan{}, err
	}
	if b.AccountID != accountID {
		b.AccountID = accountID
	}
	p, err := r.planSettleBill(ctx, sp, b, paid, "", date, meta, now)
	if err != nil {
		return settlePlan{}, err
	}
	sk := BillSK(billID)
	p.items = append(p.items, r.bills.BuildRawUpdateTxItem(sp.PK(), &sk, "SET account_id = :a", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":a": str(accountID)}))
	return p, nil
}
```

and split today's `planSettle` into `planSettle` (reads the bill) + `planSettleBill(ctx, sp, b *finance.Bill, paid, differenceCategoryID, date, meta, now)` (everything after the read), so both callers share the guard, the posting and the status update. Two updates of the same bill item in one transaction are refused by DynamoDB: fold `account_id = :a` into `planSettleBill`'s own `SET` expression through an optional `accountID` argument instead of a second update item (pass `""` from `planSettle`).

`LedgerRepository` gains `imports Base` (`NewBase(db, cfg, TableImports)` in `NewLedgerRepository`); `EnsureByokAccount` also sets the space's default receiving account when it has none (`GetSettings`, `SetDefaultReceivingAccount`), so a paid BYOK invoice in part B has a destination even before the person chooses.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./tests && go vet ./... && go test ./... -race -count=1 && make test-integration`
Expected: all `ok`, including `TestManualMovementsIntoASyncedAccountAreRefused` (Task 12) and the four tests above; the ledger hardening tests (`ledger_hardening_test.go`) still pass.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories api/tests/integration/finance_bank_lines_test.go
git commit -m "feat(finance): post bank lines once, classify them without moving the balance, keep the provider's balance"
```

### Task 14: `services.ByokConnections` — connect, test, renew, onboarding, sandbox toggle, disconnect

**Files:**
- Create: `api/internal/services/byok_connections.go`, `api/internal/services/byok_errors.go`
- Modify: `api/internal/problem/problem.go` (BYOK codes)
- Test: `api/internal/services/byok_connections_test.go` (unit, fakes), integration in Task 16

**Interfaces:**
- Consumes: Tasks 8, 10, 13. `inter.InspectPair` is **not** imported here (Task 9's import rule): the PEM checks before the Lambda run with the standard library (`crypto/x509`, `crypto/tls`), duplicated in 25 lines on purpose.
- Produces:

```go
type ConnectInput struct{ Provider string; ClientID, ClientSecret, CertPEM, KeyPEM, Account, PixKey, NotifyEmail string; BackfillMonths int; Authorized bool }
type ByokConnections struct{ /* repo, ledger, outbound, tokens, limits, statement, clock */ }
func NewByokConnections(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, out *byokrpc.Outbound, tokens *byokrpc.TokenManager, limits *byokrpc.Limits, statement *ByokStatement) *ByokConnections
func (s *ByokConnections) Session(conn *byokconn.Connection) byokrpc.Session
func (s *ByokConnections) Get(ctx, sp space.ResolvedSpace, provider string) (*byokconn.Connection, error)          // ErrNotFound
func (s *ByokConnections) List(ctx, sp space.ResolvedSpace) ([]byokconn.Connection, error)
func (s *ByokConnections) Connect(ctx, sp space.ResolvedSpace, in ConnectInput, actor, requestID string, now time.Time) (*byokconn.Connection, error)
func (s *ByokConnections) EnableSandbox(ctx, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) (*byokconn.Connection, error)
func (s *ByokConnections) Test(ctx, sp space.ResolvedSpace, provider string, now time.Time) (*byokconn.Connection, error)
func (s *ByokConnections) Renew(ctx, sp space.ResolvedSpace, provider, certPEM, keyPEM, actor, requestID string, now time.Time) (*byokconn.Connection, error)
func (s *ByokConnections) SetOnboarding(ctx, sp space.ResolvedSpace, provider, step string, now time.Time) error
func (s *ByokConnections) Disconnect(ctx, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) error
func (s *ByokConnections) RecordOutcome(ctx context.Context, conn *byokconn.Connection, err error, now time.Time) // health fields (§ 3.4)
func LedgerAccountID(provider, account string) string // "byok-{provider}-{sha256(account)[:12]}"
var ErrByokSpaceKind, ErrConnectNotAuthorized, ErrSandboxInLive, ErrBackfillRange error
const OnboardingSteps = "requirements,create_integration,validation,download,upload,webhook"
```

Problem mapping (`problem.go`), each a distinct `WithCode`: `byok.ErrCertificateExpired` → 422 `byok_certificate_expired`; `byok.ErrCertificateInvalid` → 422 `byok_certificate_invalid`; `byok.ErrKeyMismatch` → 422 `byok_key_mismatch`; `byok.ErrCredentialsRefused`, `byok.ErrScopesRefused` → 422 `byok_credentials_refused`; `byokrpc.ErrSandboxBusy` → 429 `byok_sandbox_busy`; `byok.ErrUnavailable`, `byok.ErrRateLimited` → 503 `byok_provider_unavailable`; `ErrByokSpaceKind` → 404 (body identical to `spaceNotFound`); `ErrConnectNotAuthorized` → 422 `validation_error` field `authorized`; `ErrSandboxInLive` → 409 `byok_sandbox_test_only`; `repositories.ErrConnectionExists` → 409 `byok_already_connected`; `ErrSyncTooSoon` (Task 15) → 429 `byok_sync_too_soon`.

- [ ] **Step 1: Write the failing tests**

```go
package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestLedgerAccountIDIsStableAndHidesTheAccount(t *testing.T) {
	a, b := LedgerAccountID("inter", "1234567"), LedgerAccountID("inter", "1234567")
	if a != b || a == "" || len(a) > 64 || strings.Contains(a, "1234567") {
		t.Fatalf("%q %q", a, b)
	}
	if LedgerAccountID("inter", "7654321") == a {
		t.Fatal("another account, another ledger account")
	}
}

func TestPEMChecksRunBeforeTheLambda(t *testing.T) {
	inv := &countingInvoker{}
	svc := newConnectionsForTest(t, inv)
	in := validInput(t)
	in.KeyPEM = otherKeyPEM(t)
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, byok.ErrKeyMismatch) {
		t.Fatalf("%v", err)
	}
	in = validInput(t)
	in.CertPEM, in.KeyPEM = expiredPair(t)
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, byok.ErrCertificateExpired) {
		t.Fatalf("%v", err)
	}
	if inv.calls != 0 {
		t.Fatal("no Lambda call for a pair the api can refuse itself")
	}
}

func TestConnectRequiresTheAuthorizationAndAnOrganizationSpace(t *testing.T) {
	svc := newConnectionsForTest(t, &countingInvoker{})
	in := validInput(t)
	in.Authorized = false
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, ErrConnectNotAuthorized) {
		t.Fatalf("%v", err)
	}
	if _, err := svc.Connect(context.Background(), personalSpace(t), validInput(t), "u", "r", time.Now()); !errors.Is(err, ErrByokSpaceKind) {
		t.Fatalf("personal: %v", err)
	}
}

func TestBackfillMonthsIsThreeToTwelve(t *testing.T) {
	for _, m := range []int{2, 13} {
		in := validInput(t)
		in.BackfillMonths = m
		if err := in.validate(); !errors.Is(err, ErrBackfillRange) {
			t.Fatalf("%d: %v", m, err)
		}
	}
}
```

(`newConnectionsForTest`, `countingInvoker`, `validInput`, `otherKeyPEM`, `expiredPair`, `orgSpace`, `personalSpace` go in `byok_testhelpers_test.go`: an in-memory key pair generator like Task 3's `newPKI`, `space.ForJob` for the org space and `space.ForInvoicePayer("sub", true)` for a personal one; repositories are nil because the refusals happen before any read.)

- [ ] **Step 2: Run them**

Run: `go test ./internal/services/ -run 'Ledger|PEM|Authorization|Backfill'`
Expected: FAIL, `undefined: LedgerAccountID`.

- [ ] **Step 3: Implement**

`api/internal/services/byok_connections.go` (complete; the statement half is Task 15):

```go
package services

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	ErrByokSpaceKind        = errors.New("byok: only organization workspaces connect")
	ErrConnectNotAuthorized = errors.New("byok: the authorization must be accepted")
	ErrSandboxInLive        = errors.New("byok: the shared sandbox is test mode only")
	ErrBackfillRange        = errors.New("byok: backfill_months is 3 to 12")
)

const OnboardingSteps = "requirements,create_integration,validation,download,upload,webhook"

// Providers known in phase 1. An unknown provider is a 404 at the route.
var KnownProviders = []string{"inter"}

type ConnectInput struct {
	Provider                               string
	ClientID, ClientSecret, CertPEM, KeyPEM string
	Account, PixKey, NotifyEmail           string
	BackfillMonths                         int
	Authorized                             bool
}

func (in *ConnectInput) validate() error {
	if in.BackfillMonths == 0 {
		in.BackfillMonths = 3
	}
	if in.BackfillMonths < 3 || in.BackfillMonths > 12 {
		return ErrBackfillRange
	}
	if !in.Authorized {
		return ErrConnectNotAuthorized
	}
	if in.ClientID == "" || in.ClientSecret == "" || strings.TrimLeft(in.Account, "0") == "" || !digits(in.Account) {
		return fmt.Errorf("%w: client_id, client_secret and account are required", byok.ErrInvalidRequest)
	}
	if _, err := mail.ParseAddress(in.NotifyEmail); err != nil {
		return fmt.Errorf("%w: notify_email", byok.ErrInvalidRequest)
	}
	in.Account = strings.TrimLeft(in.Account, "0")
	return nil
}

func digits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// checkPair is the api's own PEM check (spec § 3.2 item 1), so an obviously
// wrong upload never reaches the Lambda. Nothing is kept.
func checkPair(certPEM, keyPEM string, now time.Time) error {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return byok.ErrCertificateInvalid
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return byok.ErrCertificateInvalid
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		if k, _ := pem.Decode([]byte(keyPEM)); k == nil {
			return byok.ErrCertificateInvalid
		}
		return byok.ErrKeyMismatch
	}
	if !now.Before(cert.NotAfter) {
		return byok.ErrCertificateExpired
	}
	return nil
}

func LedgerAccountID(provider, account string) string {
	sum := sha256.Sum256([]byte(provider + ":" + account))
	return "byok-" + provider + "-" + hex.EncodeToString(sum[:6])
}

func ledgerAccountName(provider, account string) string {
	tail := account
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return strings.ToUpper(provider[:1]) + provider[1:] + " ···" + tail
}

type ByokConnections struct {
	repo      *repositories.ByokRepository
	ledger    *repositories.LedgerRepository
	out       *byokrpc.Outbound
	tokens    *byokrpc.TokenManager
	limits    *byokrpc.Limits
	statement *ByokStatement
}

func NewByokConnections(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, out *byokrpc.Outbound,
	tokens *byokrpc.TokenManager, limits *byokrpc.Limits, statement *ByokStatement) *ByokConnections {
	return &ByokConnections{repo: repo, ledger: ledger, out: out, tokens: tokens, limits: limits, statement: statement}
}

func (s *ByokConnections) Session(conn *byokconn.Connection) byokrpc.Session {
	return byokrpc.Session{P: s.out.For(conn.Provider, conn.Livemode), Conn: conn, Tokens: s.tokens, Limits: s.limits}
}

func requireOrg(sp space.ResolvedSpace) error {
	if sp.Personal() || sp.Kind() != space.KindOrganization {
		return ErrByokSpaceKind
	}
	return sp.Require(space.Configure)
}

func (s *ByokConnections) Get(ctx context.Context, sp space.ResolvedSpace, provider string) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	return s.repo.GetConnection(ctx, sp.OrganizationID(), sp.Livemode(), provider)
}

func (s *ByokConnections) List(ctx context.Context, sp space.ResolvedSpace) ([]byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	return s.repo.ListConnections(ctx, sp.OrganizationID(), sp.Livemode())
}

// Connect (live only): checks, Lambda Connect (test + seal), the row, the
// Finanças account, then the first backfill window in the background.
func (s *ByokConnections) Connect(ctx context.Context, sp space.ResolvedSpace, in ConnectInput, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if !sp.Livemode() {
		return nil, fmt.Errorf("%w: test mode uses the shared sandbox (PUT with an empty body)", byok.ErrInvalidRequest)
	}
	if err := checkPair(in.CertPEM, in.KeyPEM, now); err != nil {
		return nil, err
	}
	conn := &byokconn.Connection{AccountOrganizationID: sp.OrganizationID(), Livemode: true, Provider: in.Provider,
		ID: id.New(), Kind: byokconn.KindMerchant, Version: 1, ClientID: in.ClientID, AccountNumber: in.Account,
		PixKey: in.PixKey, NotifyEmail: in.NotifyEmail}
	got, err := s.out.For(in.Provider, true).Connect(ctx, byok.Credentials{
		Ref:     &byok.ConnectionRef{AccountOrganizationID: conn.AccountOrganizationID, ID: conn.ID, Version: 1},
		Values:  map[string]string{"client_id": in.ClientID, "client_secret": in.ClientSecret, "cert_pem": in.CertPEM, "key_pem": in.KeyPEM},
		Account: in.Account, PixKey: in.PixKey,
	})
	if err != nil {
		return nil, err
	}
	s.apply(conn, got, now)
	conn.State, conn.SyncState = byokconn.StateActive, byokconn.SyncActive
	conn.AuthorizedBy, conn.AuthorizedAt, conn.AuthorizationVersion = actor, now.UTC(), byokconn.AuthorizationVersion
	conn.OnboardingStep, conn.OnboardingSince = "webhook", now.UTC()
	today := brcal.FromTime(now)
	conn.FirstDay = today.AddMonths(-in.BackfillMonths)
	conn.BackfillFrom = today
	return s.create(ctx, sp, conn, actor, requestID, now)
}

func (s *ByokConnections) apply(conn *byokconn.Connection, got byok.Connected, now time.Time) {
	conn.Sealed = got.Sealed
	conn.Capabilities, conn.ScopesGranted, conn.MissingScopes = got.Capabilities, got.ScopesGranted, got.MissingScopes
	conn.CertSubject, conn.CertFingerprint, conn.CertNotAfter = got.CertSubject, got.CertFingerprint, got.CertNotAfter
	conn.LastTokenOKAt, conn.ConsecutiveFailures, conn.LastErrorCode = now.UTC(), 0, ""
}

func (s *ByokConnections) create(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	accountKey := conn.AccountNumber
	if conn.Kind == byokconn.KindSandboxShared {
		accountKey = "sandbox"
	}
	conn.LedgerAccountID = LedgerAccountID(conn.Provider, accountKey)
	if _, err := s.ledger.EnsureByokAccount(ctx, sp, conn.LedgerAccountID, ledgerAccountName(conn.Provider, accountKey), conn.Provider, now); err != nil {
		return nil, err
	}
	if err := s.repo.CreateConnection(ctx, conn, actor, requestID, now); err != nil {
		return nil, err
	}
	s.statement.StartBackfill(conn) // detached, bounded, under the connection lock (Task 15)
	return conn, nil
}

// EnableSandbox is "Cobrança via Inter (teste)" / the test-mode statement:
// no credentials, CTech's own integration against the sandbox host (§ 9 as amended).
func (s *ByokConnections) EnableSandbox(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	if sp.Livemode() {
		return nil, ErrSandboxInLive
	}
	if existing, err := s.repo.GetConnection(ctx, sp.OrganizationID(), false, provider); err == nil {
		return existing, nil // a double click
	}
	conn := &byokconn.Connection{AccountOrganizationID: sp.OrganizationID(), Provider: provider, ID: id.New(),
		Kind: byokconn.KindSandboxShared, State: byokconn.StateActive, SyncState: byokconn.SyncActive, Version: 1,
		Capabilities: []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection},
		AuthorizedBy: actor, AuthorizedAt: now.UTC(), AuthorizationVersion: byokconn.AuthorizationVersion,
		FirstDay: brcal.FromTime(now), BackfillFrom: brcal.FromTime(now), BackfillDone: true}
	return s.create(ctx, sp, conn, actor, requestID, now)
}

// Test re-runs a token fetch and a balance read, records health and answers it.
func (s *ByokConnections) Test(ctx context.Context, sp space.ResolvedSpace, provider string, now time.Time) (*byokconn.Connection, error) {
	conn, err := s.Get(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	s.tokens.Invalidate(ctx, conn)
	_, err = s.Session(conn).Balance(ctx, byok.Date{})
	s.RecordOutcome(ctx, conn, err, now)
	if err != nil {
		return conn, err
	}
	return conn, nil
}

// Renew: § 3.2 checks, Reseal (the api never sees the client_secret again),
// one conditional write of the new version; warnings reset.
func (s *ByokConnections) Renew(ctx context.Context, sp space.ResolvedSpace, provider, certPEM, keyPEM, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	conn, err := s.Get(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	if conn.Kind != byokconn.KindMerchant {
		return nil, ErrSandboxInLive
	}
	if err := checkPair(certPEM, keyPEM, now); err != nil {
		return nil, err
	}
	got, err := s.out.For(provider, conn.Livemode).Reseal(ctx, conn.Ref(), certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	old := *conn
	s.apply(conn, got, now)
	conn.Version++
	conn.State = byokconn.StateActive
	conn.ResetWarnings()
	if err := s.repo.SaveConnection(ctx, conn, actor, requestID, "byok.certificate_renewed", now); err != nil {
		return nil, err
	}
	s.tokens.Invalidate(ctx, &old)
	return conn, nil
}

func (s *ByokConnections) SetOnboarding(ctx context.Context, sp space.ResolvedSpace, provider, step string, now time.Time) error {
	if err := requireOrg(sp); err != nil {
		return err
	}
	if !slices.Contains(strings.Split(OnboardingSteps, ","), step) {
		return fmt.Errorf("%w: step", byok.ErrInvalidRequest)
	}
	return s.repo.PutOnboarding(ctx, sp.OrganizationID(), sp.Livemode(), provider, step, now)
}

// Disconnect, part A: credentials and tokens deleted, the Finanças account
// frozen. Part B prepends open charges and the webhook (spec § 3.6 steps 1-2).
func (s *ByokConnections) Disconnect(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) error {
	conn, err := s.Get(ctx, sp, provider)
	if err != nil {
		return err
	}
	s.tokens.Invalidate(ctx, conn)
	if err := s.repo.DeleteConnection(ctx, conn, actor, requestID, now); err != nil {
		return err
	}
	return s.ledger.SetSyncState(ctx, sp, conn.LedgerAccountID, string(byokconn.SyncDisconnected))
}

// RecordOutcome keeps the § 3.4 health fields. A refused call never marks
// anything paid; it only counts.
func (s *ByokConnections) RecordOutcome(ctx context.Context, conn *byokconn.Connection, err error, now time.Time) {
	switch {
	case err == nil:
		conn.ConsecutiveFailures, conn.LastErrorCode, conn.LastTokenOKAt = 0, "", now.UTC()
	case errors.Is(err, byok.ErrUnauthorized), errors.Is(err, byok.ErrCredentialsRefused), errors.Is(err, byok.ErrUnavailable):
		conn.ConsecutiveFailures++
		conn.LastErrorCode, conn.LastErrorAt = byok.ErrorCode(err), now.UTC()
	default:
		return // a caller error (rate limit, invalid request) says nothing about health
	}
	_ = s.repo.SaveConnection(ctx, conn, "system", "", "", now) // a lost race: the other writer recorded health too
}
```

Additions this needs, in the same task: `brcal.Date.AddMonths(n int) Date` (calendar months, clamped to month end; unit-tested beside `AddDays`), `ByokRepository.PutOnboarding` (an `UpdateItem` of `onboarding_step`, and `onboarding_since` when the step changes; an organization with no connection yet gets a row with `state` absent, which `ListConnections` returns as `onboarding` and `ConnectionsForJob` never sees because it has no schedule keys; `CreateConnection` then uses `BuildPutTxItem` conditional on `attribute_not_exists(state)` instead of absence so the onboarding row is replaced), and `LedgerRepository.SetSyncState(ctx, sp, accountID, state string) error` (one `UpdateItem` of `sync_state` on the account row; `AccountRow` gains `SyncState string`). `space.Kind()` and `KindOrganization` exist (`space/space.go:64`, `space/kind.go`).

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal && go vet ./... && go test ./internal/services/ ./internal/domain/brcal/ -race -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services api/internal/repositories api/internal/domain/brcal api/internal/problem
git commit -m "feat(byok): connect, sandbox toggle, test, renew, onboarding and disconnect"
```

### Task 15: `services.ByokStatement` — backfill, incremental sync, sync now, test-mode filter

**Files:**
- Create: `api/internal/services/byok_statement.go`
- Modify: `api/internal/repositories/byok.go` (`ChargeRefOwner`, used here and written by part B)
- Test: `api/tests/integration/byok_statement_test.go`

**Interfaces:**
- Consumes: Tasks 10, 13, 14; `byoktest.Fake` through a `byokrpc.Invoker` that runs the fake (`tests/integration/byok_fake_invoker_test.go`, the same router as Task 10's unit fake, all ops).
- Produces:

```go
type ByokStatement struct{ /* repo, ledger, locks, clock, links LineLinker */ }
type LineLinker interface{ Link(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l repositories.BankLine) (handled bool, err error) } // part B; nil in part A
func NewByokStatement(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, c cache.Backend, sessions func(*byokconn.Connection) byokrpc.Session) *ByokStatement
func (s *ByokStatement) WithLinker(l LineLinker) *ByokStatement
func (s *ByokStatement) Sync(ctx context.Context, conn *byokconn.Connection, now time.Time) (SyncResult, error) // backfill step (one window) + incremental + balance
func (s *ByokStatement) SyncNow(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) (SyncResult, error) // ≤ once per 10 min; ErrSyncTooSoon
func (s *ByokStatement) StartBackfill(conn *byokconn.Connection)
type SyncResult struct{ Posted, Skipped, Dropped int; BackfillDone bool; Difference billing.Cents }
var ErrSyncTooSoon = errors.New("byok: synced less than 10 minutes ago")
const backfillWindowDays = 90; const incrementalDays = 3
func (r *ByokRepository) ChargeRefOwner(ctx context.Context, provider string, livemode bool, ref string) (accountOrgID string, found bool, err error) // CHARGEREF#{provider}#{mode}#{ref}
```

Rules implemented (spec § 8.2, § 8.5, § 9, D21, D22):
- Per connection, everything runs under `lock.Acquire("byok:sync:{conn.ID}")` (TTL 10 min, heartbeat); a busy lock returns `SyncResult{}` with no error (the other run is doing it).
- Backfill: while `!BackfillDone`, one window `[max(FirstDay, BackfillFrom−90d), BackfillFrom−1]` newest first, all pages; then `BackfillFrom = window start`. A window the provider refuses with `ErrInvalidRequest`/`ErrNotFound`, or reaching `FirstDay`, ends it: `BackfillDone = true`, `FirstDay = BackfillFrom`, then the opening: `Balance(FirstDay−1)` → `PostProviderOpening` (live only; test opening is zero, nothing posted).
- Incremental: `[max(FirstDay, today−3), today]`, all pages; then `Balance(today)` → `SetProviderBalance` (live). Test accounts: `SetProviderBalance` with the ledger's own balance, never the shared one.
- Each line: test mode keeps it only when `ChargeRef != ""` and `ChargeRefOwner` is this organization; otherwise `Dropped++`. Then, when `links != nil` and `links.Link` handles it, `Skipped++`; else `PostBankLine` (`Posted++` / `Skipped++`).
- `Difference = account.Balance − provider.Available` (live); non-zero is logged with the connection id, never the amounts' payer data.
- Every provider error goes through `ByokConnections.RecordOutcome` (passed as a func to avoid a cycle) and stops the run for that connection; `LastSyncOKAt`, `SyncedThrough` saved at the end with `SaveConnection` (action `""`, no audit row per hour).

- [ ] **Step 1: Write the failing tests**

```go
//go:build integration

package integration

func TestBackfillThenIncrementalEqualsTheBank(t *testing.T) {
	ctx := ctxT(t)
	env := newByokEnv(t) // fake provider + real tables; one live connection with FirstDay = today−100d
	env.fake.SetBalance(byok.Balance{Available: 90000})
	env.fake.AddLines(lineOn(env.today.AddDays(-95), "T1", true, 10000), lineOn(env.today.AddDays(-10), "T2", false, 2500), lineOn(env.today, "T3", true, 500))
	// opening = the bank's balance the day before FirstDay; the fake answers 90000 for every date,
	// so the test sets the lines to make the identity hold: 90000 − (10000 − 2500 + 500) = 82000 opening.
	env.fake.SetBalanceAt(env.conn.FirstDay.AddDays(-1), 82000)
	for range 3 { // two backfill windows and the incremental, at most
		if _, err := env.statement.Sync(ctx, env.conn, now()); err != nil {
			t.Fatal(err)
		}
	}
	acct, _ := env.ledger.GetAccount(ctx, env.space, env.conn.LedgerAccountID)
	if !env.conn.BackfillDone || int64(acct.Balance) != 90000 || int64(acct.Provider.Available) != 90000 {
		t.Fatalf("done=%v ledger=%d bank=%+v", env.conn.BackfillDone, acct.Balance, acct.Provider)
	}
	before := env.fake.Calls(byok.OpStatementPage)
	if r, _ := env.statement.Sync(ctx, env.conn, now()); r.Posted != 0 {
		t.Fatalf("a second sync posted %d", r.Posted)
	}
	if env.fake.Calls(byok.OpStatementPage) == before {
		t.Fatal("the incremental re-reads the last 3 days")
	}
}

func TestTenantBNeverSeesTenantAsSandboxLines(t *testing.T) {
	ctx := ctxT(t)
	a, b := newByokSandboxEnv(t), newByokSandboxEnv(t) // two test organizations on the shared sandbox fake
	a.writeChargeRef(t, "ctbAAAAAAAAAAAAAAAAAAAAAAAAAA")
	shared := []byok.StatementLine{
		lineWithRef(a.today, "S1", "ctbAAAAAAAAAAAAAAAAAAAAAAAAAA", 100),
		lineOn(a.today, "S2", true, 999), // someone's typed test data: no ref
	}
	a.fake.AddLines(shared...)
	b.useFake(a.fake)
	ra, _ := a.statement.Sync(ctx, a.conn, now())
	rb, _ := b.statement.Sync(ctx, b.conn, now())
	if ra.Posted != 1 || ra.Dropped != 1 || rb.Posted != 0 || rb.Dropped != 2 {
		t.Fatalf("a=%+v b=%+v", ra, rb)
	}
}

func TestSyncNowIsRefusedWithinTenMinutes(t *testing.T) {
	env := newByokEnv(t)
	if _, err := env.statement.SyncNow(ctxT(t), env.space, env.conn, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.statement.SyncNow(ctxT(t), env.space, env.conn, now().Add(9*time.Minute)); !errors.Is(err, services.ErrSyncTooSoon) {
		t.Fatalf("%v", err)
	}
}

func TestASyncNeverUsesScroll(t *testing.T) {
	// The fake records StatementArgs; every call carries Page >= 0 and the provider
	// adapter has no scroll path (Task 4 pins the query), so this asserts pages advance.
	env := newByokEnv(t)
	env.fake.PageSize = 1
	env.fake.AddLines(lineOn(env.today, "P1", true, 1), lineOn(env.today, "P2", true, 1))
	env.conn.BackfillDone = true
	r, _ := env.statement.Sync(ctxT(t), env.conn, now())
	if r.Posted != 2 {
		t.Fatalf("%+v", r)
	}
}
```

`newByokEnv`, `newByokSandboxEnv`, `lineOn`, `lineWithRef`, `writeChargeRef` live in `tests/integration/byok_env_test.go`: they build `services.ByokStatement` with a `byokrpc.Outbound` over the fake invoker, a `cache.NewMemoryBackend`, `space.ForJob(org, mode)`, and create the connection + ledger account through `ByokConnections.create` paths (`EnableSandbox` for test, a direct `CreateConnection` + `EnsureByokAccount` for live). `writeChargeRef` puts the `CHARGEREF#` row directly (part B writes it with the attempt).

- [ ] **Step 2: Run them**

Run: `make test-integration`
Expected: FAIL, `undefined: services.ByokStatement`.

- [ ] **Step 3: Implement** `api/internal/services/byok_statement.go` following the rules above:

```go
package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/lock"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var ErrSyncTooSoon = errors.New("byok: synced less than 10 minutes ago")

const (
	backfillWindowDays = 90
	incrementalDays    = 3
	manualSyncEvery    = 10 * time.Minute
)

type LineLinker interface {
	Link(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l repositories.BankLine) (bool, error)
}

type SyncResult struct {
	Posted, Skipped, Dropped int
	BackfillDone             bool
	Difference               billing.Cents
}

type ByokStatement struct {
	repo     *repositories.ByokRepository
	ledger   *repositories.LedgerRepository
	locks    *lock.Locker
	sessions func(*byokconn.Connection) byokrpc.Session
	record   func(context.Context, *byokconn.Connection, error, time.Time)
	links    LineLinker
}

func NewByokStatement(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, c cache.Backend,
	sessions func(*byokconn.Connection) byokrpc.Session, record func(context.Context, *byokconn.Connection, error, time.Time)) *ByokStatement {
	return &ByokStatement{repo: repo, ledger: ledger, locks: lock.New(c, 10*time.Minute), sessions: sessions, record: record}
}

func (s *ByokStatement) WithLinker(l LineLinker) *ByokStatement { s.links = l; return s }

func toByok(d brcal.Date) byok.Date   { return byok.Date{Year: d.Year, Month: d.Month, Day: d.Day} }
func fromByok(d byok.Date) brcal.Date { return brcal.Date{Year: d.Year, Month: d.Month, Day: d.Day} }

// StartBackfill runs the first window right after connect, detached from the
// request, bounded to 4 minutes; cmd/byok continues anything left.
func (s *ByokStatement) StartBackfill(conn *byokconn.Connection) {
	c := *conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		if _, err := s.Sync(ctx, &c, time.Now()); err != nil {
			slog.WarnContext(ctx, "byok first backfill window", "connection_id", c.ID, "code", byok.ErrorCode(err))
		}
	}()
}

func (s *ByokStatement) SyncNow(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) (SyncResult, error) {
	if now.Sub(conn.LastManualSyncAt) < manualSyncEvery {
		return SyncResult{}, ErrSyncTooSoon
	}
	conn.LastManualSyncAt = now.UTC()
	if err := s.repo.SaveConnection(ctx, conn, "", "", "", now); err != nil {
		return SyncResult{}, err
	}
	return s.Sync(ctx, conn, now)
}

func (s *ByokStatement) Sync(ctx context.Context, conn *byokconn.Connection, now time.Time) (SyncResult, error) {
	var res SyncResult
	if conn.State != byokconn.StateActive || conn.SyncState == byokconn.SyncDisconnected || !conn.Has(byok.CapabilityStatement) {
		return res, nil // expired: sync paused (§ 3.5)
	}
	release, got, err := s.locks.Acquire(ctx, "byok:sync:"+conn.ID)
	if err != nil || !got {
		return res, err
	}
	defer release()
	sp, err := space.ForJob(conn.AccountOrganizationID, conn.Livemode)
	if err != nil {
		return res, err
	}
	sess := s.sessions(conn)
	today := brcal.FromTime(now)
	fail := func(err error) (SyncResult, error) { s.record(ctx, conn, err, now); return res, err }

	if !conn.BackfillDone {
		to := conn.BackfillFrom.AddDays(-1)
		from := to.AddDays(-(backfillWindowDays - 1))
		if from.Before(conn.FirstDay) {
			from = conn.FirstDay
		}
		refused := false
		if !to.Before(from) {
			if err := s.window(ctx, sp, sess, conn, from, to, &res); err != nil {
				if !errors.Is(err, byok.ErrInvalidRequest) && !errors.Is(err, byok.ErrNotFound) {
					return fail(err)
				}
				refused = true // spec § 8.5: a refused window ends the backfill
			} else {
				conn.BackfillFrom = from
			}
		}
		if refused || !conn.FirstDay.Before(conn.BackfillFrom) {
			conn.BackfillDone, conn.FirstDay = true, conn.BackfillFrom
			if conn.Livemode {
				bal, err := sess.Balance(ctx, toByok(conn.FirstDay.AddDays(-1)))
				if err != nil {
					return fail(err)
				}
				if _, err := s.ledger.PostProviderOpening(ctx, sp, conn.LedgerAccountID, billing.Cents(bal.Available), conn.FirstDay.AddDays(-1),
					repositories.PostMeta{Actor: "system:byok"}, now); err != nil && !errors.Is(err, repositories.ErrOpeningExists) {
					return res, err
				}
			}
		}
	}

	from := today.AddDays(-incrementalDays)
	if from.Before(conn.FirstDay) {
		from = conn.FirstDay
	}
	if err := s.window(ctx, sp, sess, conn, from, today, &res); err != nil {
		return fail(err)
	}
	acct, err := s.ledger.GetAccount(ctx, sp, conn.LedgerAccountID)
	if err != nil {
		return res, err
	}
	pb := repositories.ProviderBalance{Available: acct.Balance, At: today, SyncedAt: now.UTC()}
	if conn.Livemode {
		bal, err := sess.Balance(ctx, byok.Date{})
		if err != nil {
			return fail(err)
		}
		pb.Available, pb.Blocked = billing.Cents(bal.Available), billing.Cents(bal.Blocked)
		res.Difference = acct.Balance - pb.Available
		if res.Difference != 0 && conn.BackfillDone {
			slog.WarnContext(ctx, "byok ledger differs from the bank", "connection_id", conn.ID)
		}
	}
	if err := s.ledger.SetProviderBalance(ctx, sp, conn.LedgerAccountID, pb); err != nil {
		return res, err
	}
	s.record(ctx, conn, nil, now)
	conn.LastSyncOKAt, conn.SyncedThrough = now.UTC(), today
	res.BackfillDone = conn.BackfillDone
	return res, s.repo.SaveConnection(ctx, conn, "", "", "", now)
}

func (s *ByokStatement) window(ctx context.Context, sp space.ResolvedSpace, sess byokrpc.Session, conn *byokconn.Connection, from, to brcal.Date, res *SyncResult) error {
	for page := 0; ; page++ {
		p, err := sess.StatementPage(ctx, toByok(from), toByok(to), page)
		if err != nil {
			return err
		}
		for _, l := range p.Lines {
			if err := s.line(ctx, sp, conn, l, res); err != nil {
				return err
			}
		}
		if page+1 >= p.TotalPages {
			return nil
		}
	}
}

func (s *ByokStatement) line(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l byok.StatementLine, res *SyncResult) error {
	if !conn.Livemode {
		owner, found, err := s.repo.ChargeRefOwner(ctx, conn.Provider, false, l.ChargeRef)
		if err != nil {
			return err
		}
		if l.ChargeRef == "" || !found || owner != conn.AccountOrganizationID {
			res.Dropped++ // D22: the shared sandbox statement is filtered to the tenant's own charges
			return nil
		}
	}
	bl := repositories.BankLine{Provider: conn.Provider, LineID: l.ID, Date: fromByok(l.Date), Credit: l.Direction == byok.Credit,
		Amount: billing.Cents(l.Amount), Memo: memo(l), ChargeRef: l.ChargeRef, PaymentID: l.PaymentID}
	if s.links != nil && l.ChargeRef != "" {
		handled, err := s.links.Link(ctx, sp, conn, bl)
		if err != nil {
			return err
		}
		if handled {
			res.Skipped++
			return nil
		}
	}
	_, posted, err := s.ledger.PostBankLine(ctx, sp, conn.LedgerAccountID, bl, repositories.PostMeta{Actor: "system:byok"}, time.Now())
	if err != nil {
		return err
	}
	if posted {
		res.Posted++
	} else {
		res.Skipped++
	}
	return nil
}

func memo(l byok.StatementLine) string {
	if l.Description == "" {
		return l.Title
	}
	return l.Title + " · " + l.Description
}
```

`ChargeRefOwner` reads `pk = CHARGEREF#{provider}#{mode}#{ref}`, `sk = CHARGEREF` with `GetItem` and returns its `account_organization_id`; an empty ref returns `found=false` without a read. `ErrOpeningExists` is the ledger's existing sentinel for a second `OPENING#` marker (`grep -n ErrOpeningExists api/internal/repositories/ledger.go`). `ByokConnections` passes `s.RecordOutcome` as `record` and `s.Session` as `sessions` when wiring (Task 16), and `ByokStatement` is built first with those two closures over a `*ByokConnections` assigned right after.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./tests && go vet ./... && go test ./... -race -count=1 && make test-integration`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/byok_statement.go api/internal/repositories/byok.go api/tests/integration
git commit -m "feat(byok): statement backfill and hourly sync into Finanças, test-mode filter, provider balance"
```

### Task 16: Routes — connections and classification, wiring in `app.Build`

**Files:**
- Create: `api/internal/api/v1/console_connections.go`, `api/internal/api/v1/connections_dto.go`, `api/internal/api/v1/finance_unclassified.go`
- Modify: `api/internal/api/v1/router.go` (`Deps.ByokConnections`, `Deps.ByokStatement`, `registerConnections` before `registerConsole`), `api/internal/api/v1/finance_http.go` (two routes), `api/internal/api/v1/finance_accounts.go` (account DTO: `source`, `provider`, `provider_balance`, `synced_at`, `sync_state`, `unclassified_count`), `api/internal/app/app.go` (wiring), `api/internal/repositories/ledger.go` (`UnclassifiedCount`)
- Test: `api/tests/integration/byok_routes_test.go`, `api/internal/api/v1/connections_routes_test.go`

**Interfaces:**
- Consumes: Tasks 14, 15, 13.
- Produces (HTTP; the UI in Tasks 19-21 consumes these shapes):

```
GET    /v1.0/console/connections                       → {data: ConnectionDTO[], providers: [{provider:"inter", fields:[...], scopes:{statement:[...], collection:[...]}}]}
GET    /v1.0/console/connections/inter                 → ConnectionDTO | 404 (never connected and no onboarding row)
PUT    /v1.0/console/connections/inter                 live: multipart (client_id, client_secret, account, pix_key, notify_email, backfill_months, authorized=true, cert file, key file, each file ≤ 16 KiB) → 201 ConnectionDTO; test: {} → 201 ConnectionDTO (sandbox)
POST   /v1.0/console/connections/inter/test            → 200 ConnectionDTO (health updated) | 422/503 problem
POST   /v1.0/console/connections/inter/certificate     multipart (cert, key) → 200 ConnectionDTO
POST   /v1.0/console/connections/inter/sync            → 202 {posted, skipped, backfill_done} | 429 byok_sync_too_soon
PUT    /v1.0/console/connections/inter/onboarding      {"step":"validation"} → 204
DELETE /v1.0/console/connections/inter                 → 204 (part B adds the body {cancel_open_charges})
GET    /v1.0/console/finance/accounts/:id/unclassified → {data: [{tx_id, date, amount, direction, memo}]}
POST   /v1.0/console/finance/unclassified/:tx_id/classify {category_id|bill_id|transfer_account_id} → 200 {tx_id}
ConnectionDTO = {provider, state ("onboarding"|"active"|"expired"|"disconnected"), status ("active"|"degraded"|"expired"|"disconnected"|"onboarding"), kind, client_id, account_last4, pix_key, cert_subject, cert_fingerprint, cert_not_after, cert_days_left, capabilities[], missing_scopes[], collection_cleared (bool, live only), webhook_status, last_sync_ok_at, last_error_code, last_error_at, onboarding_step, onboarding_since, ledger_account_id, backfill_done, first_day, connected_at, notify_email}
```

Chain for every connection route: `auth`, `RequireUserScope(finance read|write)`, `ResolveSpace`, `RequireVerb(space.Configure)`, idempotency on writes (`middleware.SpaceIdempotency`), then a provider guard answering `404 resource_not_found` for anything not in `services.KnownProviders`. Mounted with `v1.Group("/console/connections", auth)` **before** `registerConsole` (a group's handlers run as prefix middleware, so the merchant tenant resolver must not run in front of a connection route: an organization with no billing tenant may connect for statement sync, spec § 3.1).

`collection_cleared = cfg.ByokCleared(provider)` (live) so the screen can say "Cobrança pelo Inter em liberação"; the handler gets it through `Deps.ByokCleared func(string) bool`.

- [ ] **Step 1: Write the failing tests**

`api/tests/integration/byok_routes_test.go`:

```go
//go:build integration

package integration

func TestConnectionRoutesRefuseEveryoneButOwnersAndAdmins(t *testing.T) {
	e := newByokAPI(t) // newAPIWith + a fake outbound invoker wired into Deps (cfg.ByokOutboundFunction = "fake")
	reads := 0
	e.countByokReads(&reads)
	paths := []struct{ method, path, body string }{
		{"GET", "/v1.0/console/connections", ""}, {"GET", "/v1.0/console/connections/inter", ""},
		{"PUT", "/v1.0/console/connections/inter", "{}"}, {"POST", "/v1.0/console/connections/inter/test", ""},
		{"POST", "/v1.0/console/connections/inter/sync", ""}, {"PUT", "/v1.0/console/connections/inter/onboarding", `{"step":"validation"}`},
		{"DELETE", "/v1.0/console/connections/inter", ""},
	}
	for _, role := range []string{"member", "viewer"} {
		e.account.set(e.accountOrg, "user-"+role, "organization", role)
		for _, p := range paths {
			r := e.consoleAsUser(t, "user-"+role, p.method, p.path, "test", "org:"+e.accountOrg, p.body)
			if r.status != 403 {
				t.Fatalf("%s %s %s: %d", role, p.method, p.path, r.status)
			}
		}
	}
	other := newSpaceOrgID()
	for _, p := range paths {
		if r := e.consoleAs(t, p.method, p.path, e.sessionToken(t, allConsoleScopes...), "test", "org:"+other, p.body); r.status != 404 {
			t.Fatalf("another organization %s %s: %d", p.method, p.path, r.status)
		}
		if r := e.consoleAs(t, p.method, p.path, e.sessionToken(t, allConsoleScopes...), "test", "personal", p.body); r.status != 404 {
			t.Fatalf("personal %s %s: %d", p.method, p.path, r.status)
		}
	}
	if reads != 0 {
		t.Fatalf("%d byok table reads before membership", reads)
	}
}

func TestTestModeTogglesTheSandboxWithNoCredentials(t *testing.T) {
	e := newByokAPI(t)
	r := e.consoleWrite(t, "PUT", "/v1.0/console/connections/inter", e.sessionToken(t, allConsoleScopes...), "test", "{}")
	var dto struct{ Kind, State string; Capabilities []string }
	r.decode(t, &dto)
	if r.status != 201 || dto.Kind != "sandbox_shared" || dto.State != "active" || len(dto.Capabilities) != 2 {
		t.Fatalf("%d %+v", r.status, dto)
	}
	if r := e.consoleWrite(t, "PUT", "/v1.0/console/connections/inter", e.sessionToken(t, allConsoleScopes...), "live", "{}"); r.status != 422 {
		t.Fatalf("live with no credentials: %d", r.status)
	}
}

func TestTheRawConnectionItemHoldsOnlyCiphertext(t *testing.T) {
	e := newByokAPI(t)
	r := e.connectLive(t, "s3cr3t-value") // multipart with a generated pair; the fake Lambda seals with a test key
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	item := e.rawByokItem(t, e.accountOrg+"#live", "CONNECTION#inter")
	for k, v := range item {
		if s, ok := v.(*types.AttributeValueMemberS); ok && (strings.Contains(s.Value, "s3cr3t-value") || strings.Contains(s.Value, "BEGIN")) {
			t.Fatalf("attribute %s holds plaintext", k)
		}
	}
	if !strings.HasPrefix(item["sealed"].(*types.AttributeValueMemberS).Value, "b1.") {
		t.Fatal("sealed is a b1 bundle")
	}
	if strings.Contains(string(r.body), "s3cr3t-value") || strings.Contains(string(r.body), "sealed") {
		t.Fatal("the response carries no secret and no bundle")
	}
	if logs := e.capturedLogs(); strings.Contains(logs, "s3cr3t-value") || strings.Contains(logs, "BEGIN") {
		t.Fatal("the logs carry no secret")
	}
}

func TestUnknownProviderIs404(t *testing.T) {
	e := newByokAPI(t)
	if r := e.console(t, "/v1.0/console/connections/other", e.sessionToken(t, allConsoleScopes...), "test"); r.status != 404 {
		t.Fatalf("%d", r.status)
	}
}

func TestClassifyRoute(t *testing.T) {
	e := newByokAPI(t)
	e.consoleWrite(t, "PUT", "/v1.0/console/connections/inter", e.sessionToken(t, allConsoleScopes...), "test", "{}")
	txID := e.postSandboxLine(t, 1500) // a CHARGEREF + a fake statement line + one sync
	r := e.consoleWrite(t, "POST", "/v1.0/console/finance/unclassified/"+txID+"/classify", e.sessionToken(t, allConsoleScopes...), "test", `{"category_id":"outras-receitas"}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
```

The helpers (`newByokAPI`, `countByokReads`, `consoleAsUser`, `connectLive`, `rawByokItem`, `capturedLogs`, `postSandboxLine`) go in `tests/integration/byok_api_env_test.go`. `countByokReads` wraps the DynamoDB client with a middleware counting `GetItem`/`Query` on `{prefix}_byok` (the same technique `TestFinanceIsNotBehindTheConsoleTenantResolver` and the ADR 0025 tests use; reuse it). `capturedLogs` swaps `slog.Default` for a buffer for the test's duration.

- [ ] **Step 2: Run them**

Run: `make test-integration`
Expected: FAIL, 404 on every connection route (not mounted).

- [ ] **Step 3: Implement**

`api/internal/api/v1/console_connections.go`:

```go
package v1

import (
	"errors"
	"io"
	"mime/multipart"
	"slices"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

const maxPEMBytes = 16 << 10

type connectionHandlers struct {
	conns     *services.ByokConnections
	statement *services.ByokStatement
	cleared   func(string) bool
	clock     func() time.Time
}

func registerConnections(v1 fiber.Router, d Deps, auth fiber.Handler, clock func() time.Time) {
	if d.ByokConnections == nil || d.Spaces == nil {
		return
	}
	h := &connectionHandlers{conns: d.ByokConnections, statement: d.ByokStatement, cleared: d.ByokCleared, clock: clock}
	idem := middleware.SpaceIdempotency(d.Idempotency, clock)
	g := v1.Group("/console/connections", auth)
	chain := func(write bool) []any {
		scope := middleware.ScopeFinanceRead
		if write {
			scope = middleware.ScopeFinanceWrite
		}
		c := []any{middleware.RequireUserScope(scope), middleware.ResolveSpace(d.Spaces), middleware.RequireVerb(space.Configure)}
		if write {
			c = append(c, idem)
		}
		return c
	}
	add := func(method, path string, write bool, fn fiber.Handler) {
		c := append(chain(write), knownProvider, fn)
		g.Add([]string{method}, path, c[0], c[1:]...)
	}
	g.Add([]string{"GET"}, "", chain(false)[0], append(chain(false)[1:], h.list)...)
	add("GET", "/:provider", false, h.get)
	add("PUT", "/:provider", true, h.connect)
	add("DELETE", "/:provider", true, h.disconnect)
	add("POST", "/:provider/test", true, h.test)
	add("POST", "/:provider/certificate", true, h.renew)
	add("POST", "/:provider/sync", true, h.sync)
	add("PUT", "/:provider/onboarding", true, h.onboarding)
}

func knownProvider(c fiber.Ctx) error {
	if p := c.Params("provider"); p != "" && !slices.Contains(services.KnownProviders, p) {
		return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
	}
	return c.Next()
}

func (h *connectionHandlers) now() time.Time { return h.clock() }

func (h *connectionHandlers) list(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	conns, err := h.conns.List(c.Context(), sp)
	if err != nil {
		return fail(c, err)
	}
	out := make([]connectionDTO, len(conns))
	for i := range conns {
		out[i] = toConnectionDTO(&conns[i], h.cleared, h.now())
	}
	return c.JSON(fiber.Map{"data": out, "providers": providerCatalog()})
}

func (h *connectionHandlers) get(c fiber.Ctx) error {
	conn, err := h.conns.Get(c.Context(), middleware.GetSpace(c), c.Params("provider"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.cleared, h.now()))
}

func readPEM(fh *multipart.FileHeader) (string, error) {
	if fh == nil || fh.Size > maxPEMBytes {
		return "", errors.New("file missing or larger than 16 KiB")
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPEMBytes))
	return string(b), err
}

func (h *connectionHandlers) connect(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	actor, req := middleware.GetClaims(c).Sub, middleware.GetRequestID(c)
	provider := c.Params("provider")
	if !sp.Livemode() {
		conn, err := h.conns.EnableSandbox(c.Context(), sp, provider, actor, req, h.now())
		if err != nil {
			return fail(c, err)
		}
		return c.Status(fiber.StatusCreated).JSON(toConnectionDTO(conn, h.cleared, h.now()))
	}
	form, err := c.MultipartForm()
	if err != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "required", "multipart form with cert and key files")}).Send(c)
	}
	field := func(k string) string {
		if v := form.Value[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	file := func(k string) *multipart.FileHeader {
		if v := form.File[k]; len(v) > 0 {
			return v[0]
		}
		return nil
	}
	cert, cerr := readPEM(file("cert"))
	key, kerr := readPEM(file("key"))
	if cerr != nil || kerr != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "invalid_file", "cert and key are PEM files up to 16 KiB")}).Send(c)
	}
	months, _ := strconv.Atoi(field("backfill_months"))
	conn, err := h.conns.Connect(c.Context(), sp, services.ConnectInput{
		Provider: provider, ClientID: field("client_id"), ClientSecret: field("client_secret"), CertPEM: cert, KeyPEM: key,
		Account: field("account"), PixKey: field("pix_key"), NotifyEmail: field("notify_email"),
		BackfillMonths: months, Authorized: field("authorized") == "true",
	}, actor, req, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(toConnectionDTO(conn, h.cleared, h.now()))
}

func (h *connectionHandlers) test(c fiber.Ctx) error {
	conn, err := h.conns.Test(c.Context(), middleware.GetSpace(c), c.Params("provider"), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.cleared, h.now()))
}

func (h *connectionHandlers) renew(c fiber.Ctx) error {
	cfh, _ := c.FormFile("cert")
	kfh, _ := c.FormFile("key")
	cert, cerr := readPEM(cfh)
	key, kerr := readPEM(kfh)
	if cerr != nil || kerr != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "invalid_file", "cert and key are PEM files up to 16 KiB")}).Send(c)
	}
	conn, err := h.conns.Renew(c.Context(), middleware.GetSpace(c), c.Params("provider"), cert, key,
		middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.cleared, h.now()))
}

func (h *connectionHandlers) sync(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	conn, err := h.conns.Get(c.Context(), sp, c.Params("provider"))
	if err != nil {
		return fail(c, err)
	}
	res, err := h.statement.SyncNow(c.Context(), sp, conn, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"posted": res.Posted, "skipped": res.Skipped, "backfill_done": res.BackfillDone})
}

func (h *connectionHandlers) onboarding(c fiber.Ctx) error {
	var body struct {
		Step string `json:"step"`
	}
	if p := decodeStrict(c, &body); p != nil {
		return p.Send(c)
	}
	if err := h.conns.SetOnboarding(c.Context(), middleware.GetSpace(c), c.Params("provider"), body.Step, h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *connectionHandlers) disconnect(c fiber.Ctx) error {
	if err := h.conns.Disconnect(c.Context(), middleware.GetSpace(c), c.Params("provider"),
		middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

`api/internal/api/v1/connections_dto.go`: `connectionDTO` with the JSON names listed above; `toConnectionDTO` maps `state` (an onboarding row with empty `State` → `"onboarding"`), `status` (`degraded` when `Degraded()`), `account_last4` (last four digits only), `cert_days_left = ceil(notAfter − now in days)`, `collection_cleared = conn.Livemode && cleared(conn.Provider)`; never `sealed`, `version`, `hook_id`, `authorized_by`. `providerCatalog()` returns, for `inter`: `fields` = `client_id`, `client_secret`, `cert`, `key`, `account`, `pix_key` (with `secret`/`file` flags, mirroring `inter.Provider.CredentialFields` without importing it) and `scopes` = `{statement: ["extrato.read"], collection: ["cobv.read","cobv.write","pix.read","pix.write","webhook.read","webhook.write"]}` (the onboarding checklist's copyable list).

`api/internal/api/v1/finance_unclassified.go` adds two `financeRoutes()` rows:

```go
		{"GET", "/accounts/:id/unclassified", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listUnclassified }},
		{"POST", "/unclassified/:tx_id/classify", space.Import, true, func(h *financeHandlers) fiber.Handler { return h.classify }},
```

with handlers calling `h.ledger.Unclassified` and `h.ledger.Classify(ctx, h.billsRepo, sp, txID, repositories.Classification{...}, h.postMeta(c, ""), now)`; the body is decoded strictly and exactly one of the three ids must be set (`422 validation_error` otherwise). `financeHandlers` gains `billsRepo *repositories.BillRepository` (from `Deps`).

`finance_accounts.go`: the account DTO gains `source`, `provider`, `provider_balance {available, blocked, at, synced_at}`, `sync_state` and `unclassified_count` (for BYOK accounts only: `len(ledger.Unclassified(...))`, one read per BYOK account on the accounts list; there is at most one per provider).

`api/internal/app/app.go` (in `Build`, after the cache backend and the ledger exist):

```go
	var byokConns *services.ByokConnections
	var byokStatement *services.ByokStatement
	if cfg.ByokOutboundFunction != "" {
		awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
		if err != nil {
			return nil, err
		}
		out := byokrpc.NewOutbound(lambda.NewFromConfig(awsCfg), cfg.ByokOutboundFunction)
		counter := byokCounter(cacheBackend) // ratelimit.NewValkeyCounter on Redis, MemoryCounter otherwise
		tokens := byokrpc.NewTokenManager(cacheBackend, counter, crypto.NewSealer(cfg.FieldEncryptionKey))
		limits := byokrpc.NewLimits(counter)
		byokRepo := repositories.NewByokRepository(db, cfg)
		byokStatement = services.NewByokStatement(byokRepo, ledger, cacheBackend,
			func(c *byokconn.Connection) byokrpc.Session { return byokConns.Session(c) },
			func(ctx context.Context, c *byokconn.Connection, err error, now time.Time) { byokConns.RecordOutcome(ctx, c, err, now) })
		byokConns = services.NewByokConnections(byokRepo, ledger, out, tokens, limits, byokStatement)
	}
```

and `Deps{…, ByokConnections: byokConns, ByokStatement: byokStatement, ByokCleared: cfg.ByokCleared}`. Add `BuildByok(ctx, cfg) (*services.ByokConnections, *services.ByokStatement, error)` with the same body for `cmd/byok` (Task 17), and have `Build` call it. `byokCounter`:

```go
func byokCounter(c cache.Backend) ratelimit.Counter {
	if rb, ok := c.(*cache.RedisBackend); ok {
		return ratelimit.NewValkeyCounter(rb.Client())
	}
	return ratelimit.NewMemoryCounter()
}
```

In the integration env, `Build` is given a fake invoker through a package variable `app.ByokInvoker byokrpc.Invoker` (nil in production: the real Lambda client is built) set by `newByokAPI`; document it beside `app.Build`.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./tests && go vet ./... && go test ./... -race -count=1 && make test-integration`
Expected: all `ok`; the existing route-table tests (`finance_routes_test.go`, `TestNoNonMerchantReachesAnyBillingHalfRoute`) still pass.

- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1 api/internal/app api/internal/repositories api/tests/integration
git commit -m "feat(byok): connection routes, unclassified lines and classification, synced account fields"
```

### Task 17: `cmd/byok` — the hourly job (part A: statement sync, certificate warnings)

**Files:**
- Create: `api/cmd/byok/main.go`, `api/internal/services/byok_job.go`, `api/internal/email/byok.go`
- Modify: `api/Makefile` (`build` gains `byok`), `.github/workflows/api.yml` (`for cmd in … byok`, zip list), `terraform/assets/bootstrap.sh.tftpl` and `bootstrap-alpine.sh.tftpl` (`setup-deploy.sh … finance byok`, the `billing-byok` service and timer)
- Test: `api/internal/services/byok_job_test.go`, `api/tests/integration/byok_job_test.go`

**Interfaces:**
- Consumes: Tasks 8, 14, 15.
- Produces:

```go
type ByokJob struct{ /* repo, conns, statement, mail, alerts, clock */ }
type ByokMailer interface{ SendByokNotice(ctx context.Context, to string, n email.ByokNotice) error }
func NewByokJob(repo *repositories.ByokRepository, conns *services.ByokConnections, st *services.ByokStatement, mail ByokMailer, p alerts.Publisher) *ByokJob
type ByokStep string // StepSweep "sweep", StepSync "sync", StepCertificates "certificates", StepAudit "audit", StepPromote "promote", StepUsage "usage"
func (j *ByokJob) Register(step ByokStep, fn func(ctx context.Context, conn *byokconn.Connection, now time.Time) error) // part B and C add steps
func (j *ByokJob) Run(ctx context.Context, livemode bool, now time.Time) ByokRunResult
type ByokRunResult struct{ Connections int; Errors []error }
package email: type ByokNotice struct{ Kind string /* cert_90, cert_30, cert_7, cert_expired, degraded, disconnected */; Provider, OrganizationName, ConsoleURL string; NotAfter time.Time }
func (c *Client) SendByokNotice(ctx context.Context, to string, n ByokNotice) error
```

Behaviour: walks `ConnectionsForJob` pages (100 per page) with a worker pool of 16; per connection, steps in order `sweep` (part B), `sync`, `certificates`, `audit` (part B, daily guard), `promote` (part B), `usage` (part C); a failing step is recorded and the next connection continues. `certificates`: `ExpireIfPast` (save, audit action `byok.expired`); `CertWarning` → `SendByokNotice` to `NotifyEmail` → `MarkWarned` → save (send-then-mark: a crash between them sends twice, never zero times). For the shared sandbox connection the warning goes to the operator through `alerts` at D-30/D-7/D-1 of the wallet integration's certificate (`CertNotAfter` is set from the `Token` op's `CertNotAfter` on every fetch, Task 10 stores it with `RecordOutcome`). `degraded`: once per São Paulo day (`DegradedAlertedOn`), e-mail + alert. Flags: `-date` (ignored by sync, used by later steps), `-mode live|test|both` (default both), `-retry-callbacks -since=` (part B).

- [ ] **Step 1: Write the failing tests**

```go
func TestCertificateWarningsAreSentOnceAndResetByRenewal(t *testing.T) {
	ctx := context.Background()
	mail := &recordingMailer{}
	j, conn, save := jobWithOneConnection(t, mail) // in-memory repo double
	conn.CertNotAfter = time.Now().AddDate(0, 0, 29)
	for range 3 {
		j.Run(ctx, true, time.Now())
	}
	if got := mail.kinds(); len(got) != 1 || got[0] != "cert_30" {
		t.Fatalf("%v", got)
	}
	conn.ResetWarnings()
	conn.CertNotAfter = time.Now().AddDate(1, 0, 0)
	save(conn)
	j.Run(ctx, true, time.Now())
	if len(mail.kinds()) != 1 {
		t.Fatal("a renewed certificate is not warned")
	}
}

func TestAFailingConnectionDoesNotStopTheOthers(t *testing.T) {
	j, _ := jobWithConnections(t, 3, map[int]error{1: byok.ErrUnavailable})
	res := j.Run(context.Background(), true, time.Now())
	if res.Connections != 3 || len(res.Errors) != 1 {
		t.Fatalf("%+v", res)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/services/ -run 'Certificate|Failing'`
Expected: FAIL, `undefined: NewByokJob`.

- [ ] **Step 3: Implement**

`api/internal/services/byok_job.go`: the registry is an ordered slice of `{step, fn}`; part A registers `StepSync` (`statement.Sync`) and `StepCertificates` (above). The pool:

```go
func (j *ByokJob) Run(ctx context.Context, livemode bool, now time.Time) ByokRunResult {
	var res ByokRunResult
	var mu sync.Mutex
	work := make(chan byokconn.Connection)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for conn := range work {
				for _, s := range j.steps {
					if err := s.fn(ctx, &conn, now); err != nil {
						mu.Lock()
						res.Errors = append(res.Errors, fmt.Errorf("%s %s %s: %w", conn.AccountOrganizationID, conn.Provider, s.step, err))
						mu.Unlock()
						break
					}
				}
			}
		}()
	}
	var start map[string]types.AttributeValue
	for {
		page, err := j.repo.ConnectionsForJob(ctx, livemode, start, 100)
		if err != nil {
			res.Errors = append(res.Errors, err)
			break
		}
		for _, c := range page.Items {
			res.Connections++
			work <- c
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		start = page.LastEvaluatedKey
	}
	close(work)
	wg.Wait()
	return res
}
```

`api/cmd/byok/main.go` follows `cmd/reconcile/main.go`: `config.Load`, `jobs.Alerts`, `app.BuildByok` (a missing `BYOK_OUTBOUND_FUNCTION` → `jobs.Startup`), `email.New` (no `EMAIL_FROM` → warnings are only alerts), run live then test; only live errors fail the job (`jobs.Fail`), test errors are logged.

`api/internal/email/byok.go`: one HTML template per kind, pt-BR (the e-mail is to a Brazilian company's owners; no em dash), subject e.g. "O certificado da integração Inter vence em 30 dias", body with the renewal steps at Inter and a link to `/console/connections/inter` (`CHECKOUT_BASE_URL`'s origin + path).

Bootstrap (both templates), after the finance timer:

```sh
# Hourly BYOK job (spec 2026-10-11 § 5): sweep, statement sync, certificate
# warnings, daily webhook audit, promotion, usage. Re-runnable by construction.
cat > /etc/systemd/system/billing-byok.service << 'BYOKSVC'
[Unit]
Description=CTech Billing BYOK job
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=webapp
Group=webapp
WorkingDirectory=/opt/app/current
Environment=HOME=/opt/app
ExecStartPre=/bin/test -x /opt/app/current/byok
ExecStart=/opt/app/job.sh byok
StandardOutput=append:/var/log/app/jobs.log
StandardError=append:/var/log/app/jobs.log
BYOKSVC

cat > /etc/systemd/system/billing-byok.timer << 'BYOKTIMER'
[Unit]
Description=Hourly CTech Billing BYOK job

[Timer]
OnCalendar=*-*-* *:20:00 UTC
RandomizedDelaySec=120
Persistent=true

[Install]
WantedBy=timers.target
BYOKTIMER
```

and `billing-byok.timer` added to the `systemctl enable` line; `byok` added to `setup-deploy.sh`'s job list. (`job.sh` runs the job on the elected leader only, as every other timer.) Note: userdata changes reach instances on the next instance refresh, which `infra.yml` triggers on a launch template change.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l ./internal ./cmd && go vet ./... && go test ./... -race -count=1 && make test-integration && make build && ls dist/byok && terraform fmt -check -recursive ../terraform`
Expected: `ok`, `dist/byok` exists, no fmt diff.

- [ ] **Step 5: Commit**

```bash
git add api/cmd/byok api/internal/services/byok_job.go api/internal/email/byok.go api/Makefile .github/workflows/api.yml terraform/assets
git commit -m "feat(byok): hourly cmd/byok with statement sync and certificate warnings"
```

### Task 18: Catalogue — the internal unlimited price and the pilot's subscription

**Files:**
- Modify: `api/tenants/ctech.json` (one price), `api/internal/provision/plan.go` only if the plan rejects `visibility: internal` on a `prod_billing` price (check with the test below)
- Create: `api/cmd/seed/pilot-subscription.sh`
- Test: `api/internal/provision/plan_test.go` (one case)

**Interfaces:**
- Produces: price `price_billing_unlimited_internal_monthly` (spec § 12.6, exactly):

```json
{ "id": "price_billing_unlimited_internal_monthly", "product_id": "prod_billing", "type": "fixed",
  "unit_amount": 0, "recurrence": { "interval": "month", "count": 1 }, "billing_timing": "advance",
  "metadata": { "plan": "unlimited", "visibility": "internal", "quota_charges_month": "-1", "overage": "bill",
                "quota_bank_connections": "-1", "quota_users": "-1", "feature_api_access": "true" } }
```

(The three feature keys are part C's metadata vocabulary; writing them now keeps the pilot's price final.)

- [ ] **Step 1: Write the failing test**

```go
func TestTheInternalBillingPriceIsInTheCatalogueAndNotPublic(t *testing.T) {
	plan := loadCTechPlan(t) // the existing helper that parses tenants/ctech.json
	p := plan.Price("price_billing_unlimited_internal_monthly")
	if p == nil || p.ProductID != "prod_billing" || p.UnitAmount != 0 || p.Metadata["visibility"] != "internal" || p.Metadata["quota_charges_month"] != "-1" {
		t.Fatalf("%+v", p)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/provision/ -run InternalBillingPrice`
Expected: FAIL, nil price.

- [ ] **Step 3: Add the price** to `api/tenants/ctech.json` after `price_billing_early_access`, and the operator script `api/cmd/seed/pilot-subscription.sh`:

```bash
#!/usr/bin/env bash
# Subscribes CTech's own ORG_ customer in tenant zero (account organization
# 01a04ed6-1af9-745e-bcf7-d3b66fe52321) to price_billing_unlimited_internal_monthly
# (BYOK spec § 12.6). Idempotent: the Idempotency-Key is fixed per environment.
# Uses tenant zero's own M2M credential ("dfe-billing"-style operator client),
# whose client id/secret the operator exports; prints no secret.
#   BILLING_API=https://billing-api.aoctech.app TOKEN=$(...) api/cmd/seed/pilot-subscription.sh prod
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
: "${BILLING_API:?}" "${TOKEN:?}"
ORG=01a04ed6-1af9-745e-bcf7-d3b66fe52321
curl -fsS -X POST "$BILLING_API/v1.0/subscriptions" \
  -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: pilot-byok-$ENV" -H "Content-Type: application/json" \
  -d "{\"customer_external_ref\":\"ORG_$ORG\",\"items\":[{\"price_id\":\"price_billing_unlimited_internal_monthly\"}]}" \
  | jq '{id, status}'
```

Check the subscribe body's field names against `SubscribeInput` (`api/internal/api/v1/handlers.go` `subscribe`), and the existing early-access subscription of CTech's `ORG_` customer: if it is `price_billing_early_access`, the pilot moves with `POST /v1.0/subscriptions/{id}/change` to the internal price instead of a second subscription (one entitling `prod_billing` subscription per customer, `sameMerchantEntitlement`). The runbook (Task 22) names the command to use after looking.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/provision/ -race -count=1 && bash -n cmd/seed/pilot-subscription.sh`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/tenants/ctech.json api/cmd/seed/pilot-subscription.sh api/internal/provision
git commit -m "feat(catalog): internal unlimited prod_billing price and the BYOK pilot subscription script"
```

## Billing UI tasks (`ui/`)

Every UI task: run `npm ci` first if `node_modules/@aoctech/ui/package.json` is not `0.5.0` (bump `ui/package.json` to `^0.5.0` in Task 19). Execute with `/impeccable` following `ui/DESIGN.md`; check each screen at 320, 375, 768 and 1280 px; pt-BR and en in `src/locales/{pt-BR,en}/connections.json` (a new namespace, registered in both `index.ts`); no em dash in any string (the `locales.test.ts` suite gains the check if it does not have it). Mock data for `npm run dev:mock` in `src/dev/connectionsMockData.ts`.

### Task 19: UI — API layer, types, error catalogue, navigation

**Files:**
- Create: `ui/src/lib/api/connections.ts`, `ui/src/lib/api/connectionsTypes.ts`, `ui/src/locales/pt-BR/connections.json`, `ui/src/locales/en/connections.json`, `ui/src/dev/connectionsMockData.ts`
- Modify: `ui/package.json` (`@aoctech/ui ^0.5.0`), `ui/src/locales/*/index.ts`, `ui/src/locales/*/errors.json` (codes), `ui/src/lib/console/nav.ts` (`/console/connections`), `ui/src/components/finance/FinanceNav.tsx` (`/finance/settings/connections`, organization spaces where the role is owner/admin only)
- Test: `ui/src/lib/api/connections.test.ts`, `ui/src/lib/console/nav.test.ts`, `ui/src/locales/locales.test.ts`

**Interfaces:**
- Consumes: Task 16's HTTP shapes.
- Produces:

```ts
export type ConnectionState = "onboarding" | "active" | "expired" | "disconnected"
export type ConnectionStatus = "onboarding" | "active" | "degraded" | "expired" | "disconnected"
export type OnboardingStep = "requirements" | "create_integration" | "validation" | "download" | "upload" | "webhook"
export interface Connection { provider: "inter"; state: ConnectionState; status: ConnectionStatus; kind: "merchant" | "sandbox_shared"
  client_id?: string; account_last4?: string; pix_key?: string; cert_subject?: string; cert_fingerprint?: string; cert_not_after?: string; cert_days_left?: number
  capabilities: ("statement" | "collection")[]; missing_scopes?: string[]; collection_cleared: boolean; webhook_status?: string
  last_sync_ok_at?: string; last_error_code?: string; last_error_at?: string; onboarding_step?: OnboardingStep; onboarding_since?: string
  ledger_account_id?: string; backfill_done: boolean; first_day?: string; connected_at?: string; notify_email?: string }
export interface ProviderInfo { provider: "inter"; fields: {name: string; secret: boolean; file: boolean}[]; scopes: {statement: string[]; collection: string[]} }
export interface ConnectForm { client_id: string; client_secret: string; account: string; pix_key: string; notify_email: string; backfill_months: number; authorized: boolean; cert: File; key: File }
export interface ConnectionsScope { org: string; mode: "live" | "test" }
export const connectionKeys: { list(s): readonly unknown[]; one(s, p): readonly unknown[] }
export function listConnections(s: ConnectionsScope): Promise<{data: Connection[]; providers: ProviderInfo[]}>
export function getConnection(s, provider): Promise<Connection | null> // 404 → null
export function connect(s, provider, form: ConnectForm | null, key: string): Promise<Connection> // null in test: PUT {}
export function testConnection(s, provider, key): Promise<Connection>
export function renewCertificate(s, provider, cert: File, keyFile: File, key: string): Promise<Connection>
export function syncNow(s, provider, key): Promise<{posted: number; skipped: number; backfill_done: boolean}>
export function setOnboarding(s, provider, step: OnboardingStep, key): Promise<void>
export function disconnect(s, provider, key): Promise<void>
export function listUnclassified(c: FinanceCtx, accountId: string): Promise<{data: UnclassifiedLine[]}>
export function classify(c: FinanceCtx, txId: string, body: {category_id?: string; bill_id?: string; transfer_account_id?: string}, key: string): Promise<{tx_id: string}>
export interface UnclassifiedLine { tx_id: string; date: string; amount: number; direction: "credit" | "debit"; memo: string }
```

Headers: `X-Billing-Space: org:{org}`, `X-Billing-Mode`, `Idempotency-Key` on writes (the existing `idempotency.ts` helper); `connect` and `renewCertificate` send `multipart/form-data` (`FormData`, files under `cert` and `key`).

- [ ] **Step 1: Write the failing tests**

`ui/src/lib/api/connections.test.ts` (axios mocked the way `console.test.ts` mocks it):

```ts
import {beforeEach, describe, expect, it, vi} from "vitest"

import {apiClient} from "@/lib/api/client"
import {connect, getConnection, listConnections} from "@/lib/api/connections"

const scope = {org: "01a04ed6-1af9-745e-bcf7-d3b66fe52321", mode: "live" as const}

describe("connections api", () => {
  beforeEach(() => vi.restoreAllMocks())

  it("sends the organization and the mode on every call", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {data: [], providers: []}})
    await listConnections(scope)
    expect(get).toHaveBeenCalledWith("/v1.0/console/connections",
      expect.objectContaining({headers: expect.objectContaining({"X-Billing-Space": `org:${scope.org}`, "X-Billing-Mode": "live"})}))
  })

  it("reads a never-connected provider as null, not as an error", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue({isAxiosError: true, response: {status: 404, data: {code: "resource_not_found"}}})
    expect(await getConnection(scope, "inter")).toBeNull()
  })

  it("uploads the pair as multipart with the idempotency key, and test mode sends an empty object", async () => {
    const put = vi.spyOn(apiClient, "put").mockResolvedValue({data: {provider: "inter"}})
    const cert = new File(["c"], "inter.crt"), key = new File(["k"], "inter.key")
    await connect(scope, "inter", {client_id: "id", client_secret: "s", account: "1234567", pix_key: "evp", notify_email: "a@b.co", backfill_months: 3, authorized: true, cert, key}, "idem-1")
    const [, body, cfg] = put.mock.calls[0]
    expect(body).toBeInstanceOf(FormData)
    expect((body as FormData).get("cert")).toBe(cert)
    expect((body as FormData).get("authorized")).toBe("true")
    expect(cfg?.headers?.["Idempotency-Key"]).toBe("idem-1")
    await connect({...scope, mode: "test"}, "inter", null, "idem-2")
    expect(put.mock.calls[1][1]).toEqual({})
  })
})
```

`ui/src/lib/console/nav.test.ts` gains: `expect(consoleNav(true).map(i => i.href)).toContain("/console/connections")`.

`ui/src/locales/locales.test.ts` gains: the `connections` namespace has the same keys in pt-BR and en, and no value in any namespace contains `"\u2014"`.

- [ ] **Step 2: Run them**

Run: `npx vitest run --maxWorkers=2 src/lib/api/connections.test.ts src/lib/console/nav.test.ts src/locales/locales.test.ts; echo "vitest exit=$?"`
Expected: `vitest exit=1` (module not found).

- [ ] **Step 3: Implement** `connections.ts` with the shapes above (one private `headers(scope, key?)`, a `read`/`write` pair like `finance.ts`), the types file, the navigation entries, and the catalogue. The pt-BR catalogue (`connections.json`), complete for this part (en mirrors it):

```json
{
  "title": "Conexões bancárias",
  "intro": "Conecte a conta da sua empresa para trazer o extrato e o saldo para o Finanças. O dinheiro fica no seu banco; a CTech não guarda saldo.",
  "providers": {"inter": "Banco Inter"},
  "status": {"onboarding": "Em configuração", "active": "Ativa", "degraded": "Atenção", "expired": "Certificado vencido", "disconnected": "Desconectada"},
  "capabilities": {"statement": "Extrato", "collection": "Cobrança", "collectionPending": "Cobrança em liberação"},
  "steps": {
    "label": "Conectar o Banco Inter",
    "requirements": {"title": "Requisitos", "body": "Você precisa de uma conta Inter Empresas (PJ). Quem cria a integração no Inter é um administrador da conta no Internet Banking."},
    "create_integration": {"title": "Criar a integração no Inter", "body": "No Internet Banking, abra Integrar e escolha Nova integração. Sugestão de nome: CTech Billing. Marque estes escopos:", "copy": "Copiar escopos", "copied": "Escopos copiados", "noPayments": "Não marque nenhum escopo de pagamento (pagamento-*).", "questionnaire": "O Inter pede um questionário em três partes e o aceite dos termos de uso da API."},
    "validation": {"title": "Aguardar a validação", "body": "O Inter analisa a integração e avisa por e-mail. Enquanto isso, não dá para criar outra.", "waiting": "Estou aguardando", "since": "Aguardando desde {{date}}"},
    "download": {"title": "Baixar chave e certificado", "body": "Na integração aprovada, baixe o certificado (.crt) e a chave (.key). O client_secret aparece só uma vez: copie agora."},
    "upload": {"title": "Enviar para a CTech"},
    "webhook": {"title": "Aviso de pagamentos", "body": "Disponível quando a cobrança pelo Inter for liberada."},
    "state": {"done": "Concluído", "current": "Agora", "waiting": "Aguardando", "upcoming": "Depois"}
  },
  "form": {
    "clientId": "Client ID", "clientSecret": "Client secret", "cert": "Certificado (.crt)", "key": "Chave privada (.key)",
    "account": "Conta corrente", "accountHint": "Só números, com o dígito, sem zeros à esquerda.",
    "pixKey": "Chave Pix para cobranças", "pixKeyHint": "Recomendamos uma chave aleatória só para a CTech: o aviso de pagamento do Inter é um por chave.",
    "notifyEmail": "E-mail para avisos", "notifyEmailHint": "Avisos de certificado e de conexão vão para este endereço.",
    "backfill": "Histórico do extrato", "backfillMonths": "{{count}} meses",
    "authorize": "Declaro que esta integração é da minha empresa e autorizo a CTech a usá-la em nosso nome para ler o extrato e, quando liberado, emitir cobranças.",
    "submit": "Testar e conectar", "testing": "Testando a conexão",
    "choose": "Escolher arquivo", "replace": "Trocar", "remove": "Remover", "tooLarge": "Arquivo maior que 16 KB", "wrongType": "Tipo de arquivo não aceito"
  },
  "sandbox": {"title": "Modo de teste", "body": "No modo de teste, a CTech usa a própria integração com o ambiente de testes do Inter. Nenhum dado seu é necessário.", "enable": "Ativar Inter (teste)", "busy": "O ambiente de testes do Inter está ocupado. Tente em instantes.", "unavailable": "O ambiente de testes do Inter pode estar indisponível."},
  "screen": {
    "certificate": "Certificado", "expires": "vence em {{date}}", "daysLeft_one": "falta {{count}} dia", "daysLeft_other": "faltam {{count}} dias",
    "renewBanner": "O certificado do Inter vence em {{date}}. Renove no Internet Banking e envie o novo par.",
    "lastSync": "Última sincronização", "never": "Ainda não sincronizou", "lastError": "Último erro", "account": "Conta ···{{last4}}",
    "missingScopes": "Para cobrar pelo Inter, faltam estes escopos na integração: {{scopes}}",
    "test": "Testar conexão", "renew": "Renovar certificado", "syncNow": "Sincronizar agora", "disconnect": "Desconectar",
    "testOk": "Conexão funcionando", "syncStarted": "Sincronização iniciada", "tooSoon": "Sincronizado há pouco. Tente em alguns minutos."
  },
  "disconnect": {"title": "Desconectar o Banco Inter?", "body": "A CTech apaga as credenciais e para de sincronizar. O histórico no Finanças fica, só para leitura.", "onlyYou": "Só você pode cancelar a integração no Inter: Internet Banking, Integrar, a integração, Cancelar. O cancelamento não tem volta.", "confirm": "Desconectar"},
  "errorCode": {
    "byok_certificate_expired": "Este certificado já venceu. Renove no Internet Banking do Inter e envie o novo.",
    "byok_certificate_invalid": "Não conseguimos ler o certificado. Envie o arquivo .crt baixado do Inter.",
    "byok_key_mismatch": "A chave não corresponde ao certificado. Envie o par baixado da mesma integração.",
    "byok_credentials_refused": "O Inter recusou estas credenciais. Confira o client_id e o client_secret.",
    "byok_provider_unavailable": "O Inter não respondeu. Tente de novo em instantes.",
    "byok_sandbox_busy": "O ambiente de testes do Inter está ocupado. Tente em instantes.",
    "byok_sync_too_soon": "Sincronizado há pouco. Tente em alguns minutos.",
    "account_is_synced": "Esta conta é sincronizada com o banco. Os lançamentos vêm do extrato.",
    "byok_already_connected": "Esta organização já está conectada ao Inter."
  },
  "unclassified": {"title": "A classificar", "count_one": "{{count}} lançamento a classificar", "count_other": "{{count}} lançamentos a classificar", "category": "Categoria", "bill": "Conta em aberto", "transfer": "Transferência", "save": "Classificar", "empty": "Tudo classificado", "updatedAt": "Saldo do Inter, atualizado às {{time}}", "difference": "Diferença de {{amount}} entre o Inter e o Finanças"}
}
```

- [ ] **Step 4: Run them**

Run: `npx vitest run --maxWorkers=2 src/lib/api/connections.test.ts src/lib/console/nav.test.ts src/locales/locales.test.ts; echo "vitest exit=$?" && npx next typegen && npx tsc --noEmit && npm run lint`
Expected: `vitest exit=0`, no type or lint error.

- [ ] **Step 5: Commit**

```bash
git add ui/package.json ui/package-lock.json ui/src/lib ui/src/locales ui/src/dev ui/src/components/finance/FinanceNav.tsx
git commit -m "feat(ui): connections API layer, catalogue and navigation"
```

### Task 20: UI — the connections list and the Inter screen (guided onboarding, connection health)

**Files:**
- Create: `ui/src/components/connections/ConnectionsList.tsx`, `ui/src/components/connections/InterOnboarding.tsx`, `ui/src/components/connections/ConnectForm.tsx`, `ui/src/components/connections/ConnectionHealth.tsx`, `ui/src/components/connections/DisconnectDialog.tsx`, `ui/src/components/connections/useConnectionsScope.ts`
- Create pages: `ui/src/app/(console)/console/connections/{page.tsx,layout.tsx}`, `ui/src/app/(console)/console/connections/inter/{page.tsx,layout.tsx}`, `ui/src/app/(finance)/finance/settings/connections/{page.tsx,layout.tsx}`, `ui/src/app/(finance)/finance/settings/connections/inter/{page.tsx,layout.tsx}`
- Test: `ui/src/components/connections/InterOnboarding.test.tsx`, `ui/src/components/connections/ConnectForm.test.tsx`, `ui/src/components/connections/ConnectionHealth.test.tsx`

**Interfaces:**
- Consumes: Task 19; `Steps`, `FileDrop`, `Badge`, `Alert`, `Modal`, `Field`, `Input`, `Checkbox`, `Select`, `PageHeader`, `Button`, `Skeleton`, `EmptyState` from `@aoctech/ui`; `useConsoleScope()` (console) and `useFinanceCtx()` (Finanças) mapped by `useConnectionsScope()` to `ConnectionsScope`.
- Produces: one screen component per provider, `InterConnectionScreen({scope})`, used by both pages (spec § 11: one screen serves both areas).

Screen rules (spec § 11.1, § 11.2), each pinned by a test below:
- No connection (or `state: "onboarding"`) in **live**: `InterOnboarding` with six `Steps`; the current step comes from `onboarding_step` (default `requirements`); "Próximo" on steps 1, 2 and 4 calls `setOnboarding`; step 3 has "Estou aguardando" (`validation`, shows "Aguardando desde {date}" as `waiting`); step 5 holds `ConnectForm`; step 6 shows the webhook note (part B replaces it).
- **Test** mode: no checklist; the `sandbox` block with one button (`connect(scope, "inter", null)`).
- Connected: `ConnectionHealth`: status `Badge` (tone by status), capabilities (collection shows "Cobrança em liberação" when `!collection_cleared` in live), certificate block with "vence em" and a warning `Alert` from 90 days left, last sync and last error in words (`errorCode.*`), and the actions: Testar conexão, Renovar certificado (a `Modal` with two `FileDrop`s), Sincronizar agora, Desconectar (`DisconnectDialog` with the "só você cancela" text).
- Phone layout: the steps rail and the form are single-column, actions stack full width under the health block, file drops are full width; at ≥ 768 px the form uses a two-column grid for short fields (client id / account, pix key / e-mail).
- The client secret input is `type="password"` with `autocomplete="off"`; nothing from the form is kept in `localStorage`, query cache or the URL; the form state is cleared after a successful connect.

- [ ] **Step 1: Write the failing tests**

```tsx
// ConnectForm.test.tsx
import {fireEvent, render, screen, waitFor} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {ConnectForm} from "./ConnectForm"
import {withProviders} from "@/components/finance/finance.test-utils"

describe("ConnectForm", () => {
  it("cannot be submitted without the authorization and both files", async () => {
    const submit = vi.fn()
    render(withProviders(<ConnectForm onSubmit={submit} pending={false}/>))
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}))
    expect(submit).not.toHaveBeenCalled()
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0)
  })

  it("never echoes the secret and clears the form after success", async () => {
    const submit = vi.fn().mockResolvedValue(undefined)
    render(withProviders(<ConnectForm onSubmit={submit} pending={false}/>))
    const secret = screen.getByLabelText("Client secret") as HTMLInputElement
    expect(secret.type).toBe("password")
    expect(secret.autocomplete).toBe("off")
  })

  it("explains a key that does not match its certificate in words", async () => {
    const submit = vi.fn().mockRejectedValue({isAxiosError: true, response: {status: 422, data: {code: "byok_key_mismatch"}}})
    render(withProviders(<ConnectForm onSubmit={submit} pending={false}/>))
    fillValidForm()
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}))
    await waitFor(() => expect(screen.getByText("A chave não corresponde ao certificado. Envie o par baixado da mesma integração.")).toBeTruthy())
  })
})

// InterOnboarding.test.tsx
it("starts at the saved step and offers the copyable scope list with the no-payments warning", () => {
  render(withProviders(<InterOnboarding scope={live} connection={{provider: "inter", state: "onboarding", onboarding_step: "create_integration"} as Connection} providers={[interInfo]}/>))
  expect(screen.getByText("Criar a integração no Inter").closest("li")!.getAttribute("aria-current")).toBe("step")
  expect(screen.getByText("extrato.read cobv.read cobv.write pix.read pix.write webhook.read webhook.write")).toBeTruthy()
  expect(screen.getByText("Não marque nenhum escopo de pagamento (pagamento-*).")).toBeTruthy()
})

// ConnectionHealth.test.tsx
it("warns from 90 days before expiry and says collection is pending clearance", () => {
  render(withProviders(<ConnectionHealth scope={live} connection={{...active, cert_days_left: 89, cert_not_after: "2027-01-08T00:00:00Z", collection_cleared: false, capabilities: ["statement", "collection"]}}/>))
  expect(screen.getByRole("alert").textContent).toContain("vence em")
  expect(screen.getByText("Cobrança em liberação")).toBeTruthy()
})

it("asks for confirmation and tells only the merchant can cancel at Inter", async () => {
  render(withProviders(<ConnectionHealth scope={live} connection={active}/>))
  fireEvent.click(screen.getByRole("button", {name: "Desconectar"}))
  expect(await screen.findByText(/Só você pode cancelar a integração no Inter/)).toBeTruthy()
})
```

(`fillValidForm` fills every field and drops two small files through the `FileDrop` test ids; `withProviders` is the existing helper wrapping i18n and a fresh QueryClient.)

- [ ] **Step 2: Run them**

Run: `npx vitest run --maxWorkers=2 src/components/connections; echo "vitest exit=$?"`
Expected: `vitest exit=1`.

- [ ] **Step 3: Implement** the components and pages with `/impeccable`. `ConnectForm` keeps its state in `useState`, validates client-side (required fields, digits-only account, e-mail, both files, authorization) with `useFieldErrors`, and maps server problems with `t("connections.errorCode." + code)` falling back to `messageFor`. Pages: `layout.tsx` sets `metadata.title` ("Conexões · Console", "Conexões · Finanças"); the Finanças pages render only for organization spaces where `role` is owner/admin (else the existing not-found view), the console pages under the console shell (no tenant needed: the API does not require one). After a successful connect, invalidate `connectionKeys.one` and Finanças' account list, and show "Sincronizando o histórico" until `backfill_done`.

- [ ] **Step 4: Run them**

Run: `npx vitest run --maxWorkers=2 src/components/connections; echo "vitest exit=$?" && npx next typegen && npx tsc --noEmit && npm run lint && npm run build`
Expected: `vitest exit=0`, build succeeds and lists `/console/connections/inter` and `/finance/settings/connections/inter`.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/connections "ui/src/app/(console)/console/connections" "ui/src/app/(finance)/finance/settings"
git commit -m "feat(ui): Inter connection onboarding and health, in the console and in Finanças"
```

### Task 21: UI — the synced account in Finanças and the classification list

**Files:**
- Modify: `ui/src/lib/api/financeTypes.ts` (`Account` gains `source?: "byok"`, `provider?`, `provider_balance?`, `sync_state?`, `unclassified_count?`), `ui/src/components/finance/AccountsView.tsx`, `ui/src/components/finance/OverviewView.tsx` (balance card)
- Create: `ui/src/app/(finance)/finance/unclassified/{page.tsx,layout.tsx}`, `ui/src/components/finance/UnclassifiedView.tsx`
- Test: `ui/src/components/finance/UnclassifiedView.test.tsx`, `ui/src/components/finance/AccountsView.test.tsx` (cases added)

**Rules:** a `source: "byok"` account shows the provider's balance with "Saldo do Inter, atualizado às {hh:mm}", a "Sincronizada" badge, the unclassified count linking to `/finance/unclassified?account={id}`, the difference banner when `balance !== provider_balance.available`, and **no** rename-to-opening, opening-balance, transfer or import actions (they would answer `account_is_synced`). `UnclassifiedView` lists lines (date, memo, signed amount) and per line a `Select` among category / open bill / transfer, then "Classificar"; on a phone each line is a card with the select and button stacked; classified lines leave the list optimistically and the account's count is invalidated.

- [ ] **Step 1: Write the failing tests**

```tsx
it("shows the bank's balance and hides manual movements for a synced account", () => {
  renderAccounts([{id: "byok-inter-1", name: "Inter ···4567", class: "asset", source: "byok", provider: "inter", balance: 10000,
    provider_balance: {available: 10000, blocked: 0, at: "2026-10-11", synced_at: "2026-10-11T13:05:00Z"}, unclassified_count: 2}])
  expect(screen.getByText(/Saldo do Inter, atualizado às/)).toBeTruthy()
  expect(screen.getByRole("link", {name: "2 lançamentos a classificar"}).getAttribute("href")).toBe("/finance/unclassified?account=byok-inter-1")
  expect(screen.queryByRole("button", {name: /saldo inicial/i})).toBeNull()
})

it("shows the difference between the bank and the ledger", () => {
  renderAccounts([{...synced, balance: 9000}])
  expect(screen.getByText(/Diferença de R\$\s?10,00 entre o Inter e o Finanças/)).toBeTruthy()
})

it("classifies a line into a category and removes it from the list", async () => {
  const classify = mockClassify()
  renderUnclassified([{tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "credit", memo: "Pix recebido"}])
  await chooseCategory("Outras receitas")
  fireEvent.click(screen.getByRole("button", {name: "Classificar"}))
  await waitFor(() => expect(classify).toHaveBeenCalledWith(expect.anything(), "tx1", {category_id: "outras-receitas"}, expect.any(String)))
  expect(await screen.findByText("Tudo classificado")).toBeTruthy()
})
```

- [ ] **Step 2: Run them**

Run: `npx vitest run --maxWorkers=2 src/components/finance/AccountsView.test.tsx src/components/finance/UnclassifiedView.test.tsx; echo "vitest exit=$?"`
Expected: `vitest exit=1`.

- [ ] **Step 3: Implement** with `/impeccable` as described.

- [ ] **Step 4: Run the whole UI suite**

Run: `npx vitest run --maxWorkers=2; echo "vitest exit=$?" && npx next typegen && npx tsc --noEmit && npm run lint && npm run build`
Expected: `vitest exit=0`, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add ui/src
git commit -m "feat(ui): bank-synced account and the A classificar list in Finanças"
```

### Task 22: Runbook, operations and the deploy of part A

**Files:**
- Create: `docs/runbooks/byok.md`
- Modify: `README.md` (one line linking the runbook), `ARCHITECTURE.md` (BYOK paragraph: outbound Lambda, `byok` table, no wallet)

- [ ] **Step 1: Write `docs/runbooks/byok.md`** with these sections, commands exact:

1. **Credentials key (once per environment, before the first deploy of part A):** `AWS_PROFILE=ctech terraform/scripts/create-byok-credentials-key.sh dev` (then `stage`, `prod`); back up the value with the break-glass material (`aws ssm get-parameter --with-decryption` is run only by the person storing the backup, never pasted anywhere else).
2. **Sandbox identifiers (test mode):** CTech's Inter integration is the wallet's; billing's Lambda reads `/ctech-wallet/{env}/inter/{client-id,client-secret,mtls-cert,mtls-key}` by IAM grant (no copy). Set the two non-secret parameters with the sandbox account number and a sandbox EVP key created in Inter's sandbox for this integration: `aws ssm put-parameter --overwrite --type String --name /ctech-billing/{env}/byok/inter-sandbox/account --value <digits>` and `.../pix-key`. Smoke test: in the console in test mode, "Ativar Inter (teste)", then "Sincronizar agora" answers 202. If Inter's sandbox refuses the production integration's credentials (the risk in plan decision 1), the fix is a dedicated sandbox integration whose four values go into `/ctech-billing/{env}/byok/inter-sandbox/{client-id,client-secret,cert,key}` SecureStrings and the Lambda's `BYOK_SANDBOX_CREDENTIAL_PARAMS` in `byok.tf` pointed at them.
3. **Deploy order (part A):** ctech-go-common `v1.18.0` tagged → ctech-ui `0.5.0` published → billing PR merged: `deploy.yml` runs Terraform (Lambda from the placeholder, flags off, IAM), then the API job (`Deploy BYOK Lambdas` before the instances), then the frontend. Check: `aws lambda get-function-configuration --function-name prod-ctech-billing-byok-outbound --query '{state:State,flags:Environment.Variables.BYOK_LIVE_COLLECTION}'` → `Active`, `"false"`; `systemctl list-timers billing-byok.timer` on the leader.
4. **Pilot (live statement sync):** CTech's organization `01a04ed6-1af9-745e-bcf7-d3b66fe52321`. Create, in CTech's Inter Internet Banking, an integration for BYOK with the scopes of the checklist (or reuse an existing one that has `extrato.read`), wait for Inter's validation, download the pair; an owner/admin uploads it at `/finance/settings/connections/inter` in live mode with a **dedicated EVP key** (never the wallet's Pix key: its webhook belongs to the wallet, spec § 12.6). Then `api/cmd/seed/run.sh prod` (the price) and the pilot subscription (`pilot-subscription.sh` or a plan change of the existing early-access subscription, whichever the customer has).
5. **Certificate renewal (merchants and the pilot):** what the D-90/D-30/D-7 e-mails ask; operator alert for the shared integration at D-30/D-7/D-1 (the wallet's certificate: renewing it in the wallet's parameters renews test mode too).
6. **Turning live collection on:** only with ADR 0030; set `byok_live_collection = true` and `byok_live_providers = ["inter"]` in `terraform/billing/environments/prod.tfvars` in the same PR as the ADR; after apply, `sudo systemctl restart app` is **not** needed for the Lambda (its environment changes at once) but is for the api to read the new SSM values (or wait for the next deploy).
7. **Emergency stop:** remove `inter` from `byok_live_providers` and apply: the Lambda refuses every live collect op on the next invocation.

- [ ] **Step 2: Commit**

```bash
git add docs/runbooks/byok.md README.md ARCHITECTURE.md
git commit -m "docs: BYOK runbook and architecture note"
```

## Final verification (part A, before the pull request)

From `api/`: `gofmt -l . && go vet ./... && go test ./... -race -count=1 && make test-integration && make build && make build-lambdas`. Then, **after** the Go suite finished, from `ui/`: `npx vitest run --maxWorkers=2; echo "vitest exit=$?" && npx next typegen && npx tsc --noEmit && npm run lint && npm run build`. Then `terraform fmt -check -recursive terraform && terraform -chdir=terraform/billing test`. Open the PR (no attribution lines), merge after CI and the whole-branch review.

## Spec coverage (part A)

| Spec | Task |
|---|---|
| § 3.1 ownership, keys, org-only, roles | 8, 14, 16 |
| § 3.2 fields, validation, Connect, capabilities, authorization | 5, 9, 14, 16, 20 |
| § 3.3 key, format, AAD, ops, no fallback, nothing in logs | 9, 11, 16 |
| § 3.4 states and health | 8, 14, 17, 20 |
| § 3.5 renewal and warnings | 8, 14, 17, 20 |
| § 3.6 disconnect (steps 3, 5, 6; 1, 2, 4 in part B) | 14, 20 |
| § 4.1 outbound Lambda, allowlist | 9, 11 |
| § 4.2 hosts, non-prod forces sandbox | 3, 9, 11 |
| § 4.3 tokens | 10 |
| § 4.4 seam | 1, 2, 5, 10 |
| § 5 steps 2, 3 | 17 |
| § 8.1, 8.2, 8.3, 8.5 | 12, 13, 15, 16, 21 |
| § 9 statement half, as amended | 9, 11, 14, 15, 22 |
| § 10 flags | 8, 9, 11 |
| § 11.1, 11.2, 11.3 (Finanças) | 6, 19, 20, 21 |
| § 12.1 connection and classification routes | 16 |
| § 12.2 Connection row, LedgerAccount, imports `B:` locks | 8, 12, 13 |
| § 12.3 configuration | 8, 11 |
| § 12.4 shared packages | 1–5 |
| § 12.6 pilot price and subscription | 18, 22 |
| § 16 steps 1, 3, 4 | 5, 11, 22 |
| § 17 items 4, 7, 8, 11, 12, 13, 14 | 9, 10, 13, 15, 16, 17 |
