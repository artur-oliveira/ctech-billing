# Account Deletion — Billing as a Saga Participant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ctech-billing a participant in ctech-account's LGPD erasure saga. It answers the eligibility question, refuses a locked person, cancels their CTech subscriptions, and anonymizes their customer record while keeping the fiscal documents. It also purges their personal finance spaces and the finance spaces of the single-member organizations erased with them, and acks the purge.

**Architecture:**
- The participant side comes from `api-commons/erasure` (v1.13.1): the `Store` over `{prefix}_erasure_state`, the SQS `Consumer` and the `AckClient`. Billing writes three things:
  - `services.Eraser`: `Blockers` for the eligibility route and the purge re-check, and `Purge` as the consumer's `PurgeFunc`.
  - `repositories.SpacePurger`: deletes every row of one finance space, idempotent and resumable.
  - The lock: one check in `middleware.Verifier.Middleware`, which every route reached with a session token goes through.
- The consumer runs inside the API process, wired from `cmd/server`. That is where `space.ForJob` may be called.
- Infra is Terraform: the state table comes from `schema.json`; the queue, the DLQ, the subscription, the alarm and the IAM go in a new `terraform/billing/erasure.tf`.
- The userdata gets no new bytes. The queue URL, the ack URL and the revocation Valkey URL are all derived in Go.

**Tech Stack:** Go 1.27 (module `gopkg.aoctech.app/billing/api`), Fiber v3, DynamoDB through `api-commons/dynamo`, SQS (`aws-sdk-go-v2/service/sqs`, already in the module graph through api-commons), Terraform `hashicorp/aws ~> 6.60`, standard `testing` only.

**Spec:**
- ctech-account `docs/specs/2026-10-06-account-deletion-overview.md` (§ 5, § 11 D1–D14)
- `…-data-inventory.md` § 2, § 6, § 8
- `…-saga-protocol.md` § 3, § 4, § 5, § 7, § 9
- Wire contract: ctech-account `docs/plans/2026-10-07-account-deletion-phase3-participants.md` (Global Constraints, R10)
- This repo: [`docs/specs/2026-10-07-finance-erp-design.md`](../specs/2026-10-07-finance-erp-design.md) § 2, § 4, § 8, plus [ADR 0009](../adr/0009-retention-and-ttl.md) and [ADR 0026](../adr/0026-finance-retention-by-purge.md)

**Out of scope:**
- `service`-scoped unlink: ctech-account has not built it (its phase 3 "Out of scope").
- `Store.Clear` on re-consent: it belongs to service-scoped unlink.
- Organization customers (`CUSTOMER_ORG#`): not built yet (ADR 0025 amendment).
- The ctech-account side.

## Rulings (made while writing this plan; each with its cost if wrong)

- **R1 No payment-gateway erasure.** Billing never talks to a payment rail and never stores card data, PIX keys or gateway customers (ARCHITECTURE § 2, § 9). It asks ctech-wallet to collect a PIX charge. Inventory § 8's "payment gateway used by billing" is therefore *none*; the Asaas side is wallet's. *Cost if wrong:* a future card rail adds one step to `Eraser.Purge`.
- **R2 No NFS-e here.** Billing issues none (`ctech-dfe` is the suggested issuer). What stays is the invoice aggregate: invoice, lines, payment attempts, checkout sessions, credit notes, the S3 PDF, the audit rows and the canceled subscription. None has a TTL or all have ADR 0009's. *Cost if wrong:* none. The retained set only shrinks if legal says so.
- **R3 Three blockers:**
  - `billing.invoice_open`: a live tenant-zero invoice that is `OPEN` with an amount due. Overdue is a count in `detail`, not a separate code. `action_url` is the portal's invoice list.
  - `billing.invoice_uncollectible`: a live tenant-zero invoice written off as `UNCOLLECTIBLE` with an amount due. User decision 2026-10-08: a written-off debt still blocks. It gets a **separate code**, not a branch of `invoice_open`, because the remedy differs. The portal cannot collect it (`Invoice.Payable` needs `OPEN`), so the person settles it through support, and the blocker carries no `action_url`.
  - `billing.tenant_owner`: the person owns a live billing tenant (`Organization.OwnerUserID`). Deleting them would orphan its console.

  Not blockers:
  - *Subscriptions:* every live status (`INCOMPLETE`, `TRIALING`, `ACTIVE`, `PAST_DUE`, `PAUSED`) has an immediate `CANCELED` edge under `CauseCustomer`, so "cannot be cancelled now" cannot happen; the purge cancels them. Cancellation is immediate and credits nothing for the unused period (user decision 2026-10-08).
  - *Payment in dispute:* billing has no dispute state. A PIX MED/chargeback is wallet's blocker.

  *Cost if wrong:* `invoice_uncollectible` clears only when support records the payment (`CauseManualPayment`), so a person who cannot pay through the portal stays blocked until support acts.
- **R4 Only tenant zero is erased** (`PORTAL_ORGANIZATION_ID`, both modes). A customer in another tenant that carries a `user_id` is that merchant's record, and the merchant is the controller. It is reachable only through a per-tenant pointer: there is no cross-tenant index, and the IAM policy denies Scan. *Cost if wrong:* a `user_id` GSI and a loop over it.
- **R5 Anonymize in place, keep the pointer.** `CustomerRepository.Anonymize` is the existing ADR 0009 path. The `CUSTOMER_USER#{sub}` pointer and `user_id` stay: the sub is opaque, and the pointer is what makes the portal answer "conta encerrada". *Cost if wrong:* service-scoped re-consent must clear them, which is that plan's job.
- **R6 The lock is one choke point:** `Verifier.Middleware`, for every session token (non-empty `sid`).
  - It refuses **every method**, not only writes.
  - A failed state lookup is a 500 (fail closed).
  - Service tokens are not checked, and neither are the public checkout and wallet's webhook: inbound money is accepted (saga § 4.2).

  *Cost if wrong:* one consistent `GetItem` per user request.
- **R7 No `VerifyClaimsStrict` route.** Billing moves no money out; the only money that moves is a customer paying. The revocation check fails open as the shared default. *Cost if wrong:* while Valkey is down, a revoked token can still pay a bill for up to 20 min.
- **R8 The eligibility scope `internal:billing:erasure-eligibility` is not in `AllScopes` or `scope-manifest.json`.**
  - No client can be granted it. ctech-account mints it for itself as the issuer.
  - The route is not behind `ResolveTenant`.

  *Cost if wrong:* if ctech-account ever validates minted scopes against the published manifest, add it there.
- **R9 Zero new userdata bytes.** AL2023 is at ~15.9 KB of 16,384 (terraform/README). The values are derived instead:
  - the queue name from `ENVIRONMENT` (`{env}-ctech-billing-erasure`, resolved with `GetQueueUrl`);
  - the ack URL from `ACCOUNT_BASE_URL`;
  - the revocation Valkey URL from `VALKEY_URL` without its DB path;
  - the portal invoices URL from `CHECKOUT_BASE_URL`'s origin;
  - the erasure client's id and secret (R16): the consumer reads them at start from SSM paths derived from `ENVIRONMENT`, unless `ERASURE_CLIENT_ID`/`ERASURE_CLIENT_SECRET` are set.

  terraform/README says the next AL2023 template addition must first move the timer units to S3. Reading two parameters in Go avoids that migration.

  *Cost if wrong:* a Go↔Terraform naming coupling, the same kind `TABLE_PREFIX` already has, plus one new SDK module (`service/ssm`).
- **R10 The consumer runs in the API process.** Both processes on every instance run it; SQS hands each message to one.
  - It starts only when ctech-account's URLs are configured and DynamoDB is not local.
  - A missing queue, or an erasure client still holding Terraform's `SET-OUT-OF-BAND` placeholder, is logged, and the API keeps serving.

  *Cost if wrong:* a misconfigured environment never acks, and ctech-account alarms after 48 h.
- **R11 The purge is a new `SpacePurger`, not a `LedgerRepository` method.** `TestLedgerRepositoryHasNoEditPath` keeps meaning "no edit path". The space is built with `space.ForJob`, injected from `cmd/server`, so `TestForJobIsNotCalledFromInternal` stays untouched.
- **R12 What the purge removes:**
  - every row under the space key `S` in every finance table, plus `idempotency`;
  - every `S#ACCOUNT#{id}` entry partition;
  - the `audit` rows under `S` **for personal spaces only**.

  ADR 0026 keeps organization finance audit rows on ADR 0009's five-year TTL.
- **R13 Tombstones never expire** (`erasedTTL` 0). Billing keeps invoices with no TTL, so it may meet the sub forever.
- **R14 Valkey: no per-user deletion.** Billing's DB 3 holds the JWKS, service tokens and 60 s membership cache entries, nothing personal beyond a sub inside a 60 s key. *Cost if wrong:* 60 s.
- **R15 Blockers are live-mode only.** Test money is not owed. The purge still covers both modes.
- **R16 The acks use a dedicated confidential client** (user decision 2026-10-08), not billing's membership client. Its only grant is `internal:account:erasure-ack`. A leaked membership credential therefore cannot ack an erasure, and this client cannot read memberships.
  - Config: `ERASURE_CLIENT_ID`/`ERASURE_CLIENT_SECRET`, falling back to the SecureStrings `/ctech-billing/{env}/billing/erasure-client-id` and `…/erasure-client-secret`.
  - Terraform declares both SecureStrings with a `SET-OUT-OF-BAND` placeholder and `ignore_changes`, like the collection secrets.
  - The operator creates the client in ctech-account, grants it the scope, and writes both values with `aws ssm put-parameter --overwrite`.
  - ctech-account matches acks on `azp`, so this client's id is the `client_id` in its `ERASURE_PARTICIPANTS`.
  - The token URL is the existing `ACCOUNT_TOKEN_URL`.
- **R17 Queue visibility timeout 900 s, `maxReceiveCount` 5, DLQ alarm > 0.** The slowest purge reads tenant zero's whole invoice range once (`AllByCustomer`). 900 s is far above twice that today.

## Global Constraints

- **Repo conventions:**
  - Code lives under `api/`, and every Go command runs from `api/`.
  - Branch `feat/account-deletion-participant` from `main` after this plan merges.
  - Conventional Commits, with **no `Co-Authored-By` or any Claude/Anthropic attribution**.
  - `gofmt -l .` prints nothing, `go vet ./...` and `go vet -tags integration ./tests/...` are clean, and `go test ./... -race` passes before every commit.
  - Integration tests need `docker compose -f docker-compose.test.yml up -d`, then `make test-integration`.
- **Versions:** `gopkg.aoctech.app/api-commons` at exactly **v1.13.1**.
- **Wire contract:**
  - Participant id `billing`.
  - Eligibility: `GET /v1.0/internal/erasure/eligibility/:sub`, scope `internal:billing:erasure-eligibility`. ctech-account's `ERASURE_PARTICIPANTS` `url` for billing ends in `/v1.0`.
  - Ack: `POST {ACCOUNT_BASE_URL}/v1.0/internal/erasure/ack`, scope `internal:account:erasure-ack`, body `erasure.Ack`.
  - SNS topic: `{env}-account-user-erasure`, ARN in SSM `/ctech/{env}/account/erasure-topic-arn`.
  - Subscription: `FilterPolicy {"services":["billing"]}`, `filter_policy_scope = "MessageAttributes"`, raw delivery on.
- **State table:** `{prefix}_erasure_state`, partition key `pk` (S), TTL attribute `ttl`, declared in `schema.json` (so Terraform creates it).
- **Blocker codes** are stable and translated by the account UI, so they are never renamed: `billing.invoice_open`, `billing.invoice_uncollectible`, `billing.tenant_owner`.
- **Deployment check (user decision 2026-10-08):** ctech-account mints the eligibility token with `iss` = its issuer URL. That must equal billing's `CTECH_ISSUER_URL` (`/ctech-account/{env}/app-url`), and `aud` must equal billing's `SERVICE_AUDIENCE`. Verify both in dev before billing is added to `ERASURE_PARTICIPANTS` (Task 11, PLAN.md operator item).
- **DynamoDB:** no `Scan` anywhere (the IAM policy denies it). Batch deletes are at most 25 keys per `BatchWriteItem`.
- **Retained:** invoices and everything under `INVOICE#`, canceled subscriptions, audit rows (except personal-space finance audit), invoice PDFs. **Anonymized:** the tenant-zero customer. **Erased:** personal finance spaces and the finance spaces of `organizations[]`.
- **Rollout gate:** legal validation of the inventory (overview § 9 step 1) comes before ctech-account adds `billing` to `ERASURE_PARTICIPANTS` in prod. This code ships dark until then: no message reaches billing's queue.

## Review Focus

1. **A purge killed halfway, or run twice.** Expected: it ends in the same state, and the entry partitions are still found after the chart rows are gone. Tests:
   - Task 5 `TestPurgeSpaceErasesEveryRowAndIsSafeToRepeat`
   - Task 5 `TestPurgeFindsEntriesAfterTheChartIsGone`
2. **An invoice issued during the grace period (the daily sweep runs while the account is locked).** Expected: the purge's own re-check acks `blocked` and changes nothing. Test: Task 6 `TestPurgeIsRefusedWhileAnInvoiceIsOpenAndChangesNothing`.
3. **A locked person holding a token that has not expired yet.** Expected: refused on every session route, while service tokens keep working. Test: Task 3 `TestALockedPersonIsRefusedWithATokenThatIsStillValid`.
4. **The daily finance job holding a work item for a space that is being purged.** Expected: it skips the item instead of resurrecting rows. Test: Task 8 `TestTheJobLeavesAnErasedSpaceAlone`.
5. **An organization that survives, another customer's open invoice, an organization's audit rows.** Expected: all untouched. Test: Task 6 `TestPurgeErasesThePersonAndKeepsTheDocuments`.

---

### Task 1: api-commons v1.8.0 → v1.13.1, gated by the whole suite

The jump crosses `dynamo.IsConditionFailed`'s change (v1.10.x, "classify cancelled transactions by reason"). A `TransactionConflict` or a throttled transaction is no longer reported as "condition failed". At billing's 19 call sites it now surfaces as an error (500, retried by the caller) instead of being read as "somebody else won". That is the correct behavior. This task changes **no** call site. If a test fails, stop and report it; do not adapt the code inside this task.

**Files:**
- Modify: `api/go.mod`, `api/go.sum`

**Interfaces:**
- Produces: `gopkg.aoctech.app/api-commons/erasure` and `jwtverify.(*Verifier).WithRevocation` become importable.

- [ ] **Step 1: Bump**

Run: `go get gopkg.aoctech.app/api-commons@v1.13.1 && go mod tidy`
Expected: `grep api-commons go.mod` prints `gopkg.aoctech.app/api-commons v1.13.1`.

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./... && go vet -tags integration ./tests/...`
Expected: no output.

- [ ] **Step 3: Unit suite**

Run: `go test ./... -race -count=1`
Expected: every package `ok`, none `FAIL`.

- [ ] **Step 4: Integration suite**

Run: `docker compose -f docker-compose.test.yml up -d && make test-integration`
Expected: `PASS` and `ok  	gopkg.aoctech.app/billing/api/tests/integration`.

- [ ] **Step 5: Record the call sites reviewed**

Run: `grep -rn 'IsConditionFailed' internal --include=*.go | grep -v _test | wc -l`
Expected: `19`. Each one reads "my own condition failed", and none of them needs a conflict to be read that way.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(api): api-commons v1.13.1 (erasure package, jwt revocation)"
```

---

### Task 2: The erasure-state table

**Files:**
- Modify: `api/internal/repositories/keys.go` (table constants block)
- Modify: `api/internal/repositories/schema.json`
- Modify: `api/internal/repositories/schema_test.go` (`allTables`, one new test)

**Interfaces:**
- Produces: `repositories.TableErasureState` (`= erasure.TableSuffix = "erasure_state"`). `EnsureTables` creates it in tests. Terraform creates `{env}_billing_erasure_state` with TTL on `ttl`, PITR, deletion protection.

- [ ] **Step 1: Write the failing test**

In `schema_test.go`, add `TableErasureState,` as the last element of `allTables`, add the import `"gopkg.aoctech.app/api-commons/erasure"`, and append:

```go
// The saga's state store (api-commons/erasure.Store) addresses
// {prefix}_erasure_state by partition key "pk" alone. A range key here would
// make every one of its GetItem calls fail with a validation error.
func TestTheErasureStateTableIsTheOneTheSagaStoreAddresses(t *testing.T) {
	if TableErasureState != erasure.TableSuffix {
		t.Fatalf("TableErasureState = %q, the store uses %q", TableErasureState, erasure.TableSuffix)
	}
	s, ok := Schemas()[TableErasureState]
	if !ok {
		t.Fatal("erasure_state is not in schema.json, so Terraform would not create it")
	}
	if s.HashKey != "pk" || s.RangeKey != "" || len(s.Indexes) != 0 {
		t.Fatalf("erasure_state schema = %+v, want hash key pk only", s)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/repositories/ -run 'TestTheErasureStateTable|TestEveryTableConstantHasASchema'`
Expected: FAIL, `undefined: TableErasureState`.

- [ ] **Step 3: Add the constant and the schema**

In `keys.go`, add the import `"gopkg.aoctech.app/api-commons/erasure"` and, inside the table `const (...)` block after `TableRecurrences = "recurrences"`:

```go
	// TableErasureState holds ctech-account's erasure saga state for this
	// participant: one row per SUB#{sub} / ORG#{id} (lock, unlock, tombstone).
	// Its schema and transitions are api-commons/erasure.Store's; it is here so
	// schema.json, and therefore Terraform, declares it.
	TableErasureState = erasure.TableSuffix
```

In `schema.json`, replace the final

```json
  "recurrences": {
    "hash_key": "pk",
    "range_key": "sk",
    "attributes": { "pk": "S", "sk": "S", "schedule_pk": "S", "schedule_sk": "S" },
    "indexes": [
      { "name": "schedule-index", "hash_key": "schedule_pk", "range_key": "schedule_sk" }
    ]
  }
}
```

with

```json
  "recurrences": {
    "hash_key": "pk",
    "range_key": "sk",
    "attributes": { "pk": "S", "sk": "S", "schedule_pk": "S", "schedule_sk": "S" },
    "indexes": [
      { "name": "schedule-index", "hash_key": "schedule_pk", "range_key": "schedule_sk" }
    ]
  },
  "erasure_state": {
    "hash_key": "pk",
    "attributes": { "pk": "S" },
    "indexes": []
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/repositories/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/repositories/keys.go internal/repositories/schema.json internal/repositories/schema_test.go
git commit -m "feat(api): erasure_state table for the account-deletion saga"
```

---

### Task 3: Refuse a locked person; check the revocation list

**Files:**
- Modify: `api/internal/problem/problem.go` (type constants)
- Create: `api/internal/middleware/erasure.go`
- Create: `api/internal/middleware/erasure_test.go`
- Modify: `api/internal/middleware/auth.go`
- Create: `api/internal/app/erasure.go`
- Create: `api/internal/app/erasure_test.go`
- Modify: `api/internal/app/app.go` (`Build`)
- Create: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `erasure.NewStore(db, prefix, 0)`, `(*erasure.Store).Blocked(ctx, sub) (bool, error)` (api-commons v1.13.1); `jwtverify.(*Verifier).WithRevocation(cache.Backend)`.
- Produces:
  - `problem.TypeAccountLocked`
  - `middleware.RefuseLocked(blocked func(ctx context.Context, sub string) (bool, error)) fiber.Handler`
  - `(*middleware.Verifier).WithErasureLock(blocked func(ctx context.Context, sub string) (bool, error)) *Verifier`
  - `app.revocationURL(string) (string, error)` and `app.revocationBackend(*config.Config) cache.Backend` (unexported)
  - the integration helper `lockSubject(t, sub)`

- [ ] **Step 1: Write the failing unit tests**

`api/internal/middleware/erasure_test.go`:

```go
package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

func TestRefuseLockedStopsOnlyALockedPerson(t *testing.T) {
	calls := 0
	blocked := func(_ context.Context, sub string) (bool, error) {
		calls++
		switch sub {
		case "gone":
			return true, nil
		case "broken":
			return false, errors.New("dynamodb: boom")
		}
		return false, nil
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, &Claims{Sub: c.Get("X-Sub"), SID: c.Get("X-Sid")})
		return c.Next()
	})
	app.Use(RefuseLocked(blocked))
	app.All("/", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	for _, tc := range []struct {
		name, method, sub, sid string
		want                   int
	}{
		{"an active person", "GET", "alive", "s1", 200},
		{"a locked person reading", "GET", "gone", "s1", 403},
		{"a locked person writing", "POST", "gone", "s1", 403},
		{"a service token whose client id happens to be the sub", "POST", "gone", "", 200},
		{"the state cannot be read", "GET", "broken", "s1", 500},
	} {
		req := httptest.NewRequest(tc.method, "/", nil)
		req.Header.Set("X-Sub", tc.sub)
		req.Header.Set("X-Sid", tc.sid)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, resp.StatusCode, tc.want, body)
		}
		if tc.want == 403 && !strings.Contains(string(body), problem.TypeAccountLocked) {
			t.Errorf("%s: body %s does not name %s", tc.name, body, problem.TypeAccountLocked)
		}
	}
	if calls != 4 {
		t.Fatalf("store asked %d times, want 4: a service token must not cost a lookup", calls)
	}
}
```

`api/internal/app/erasure_test.go`:

```go
package app

import "testing"

func TestRevocationURLDropsTheLogicalDB(t *testing.T) {
	for in, want := range map[string]string{
		"redis://valkey.internal:6379/3":          "redis://valkey.internal:6379",
		"rediss://:secret@valkey.internal:6379/3": "rediss://:secret@valkey.internal:6379",
		"redis://valkey.internal:6379":            "redis://valkey.internal:6379",
	} {
		got, err := revocationURL(in)
		if err != nil || got != want {
			t.Errorf("revocationURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := revocationURL("valkey.internal"); err == nil {
		t.Error("a URL with no host must be refused, not turned into a DB 0 connection to nowhere")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/middleware/ ./internal/app/ -run 'TestRefuseLocked|TestRevocationURL'`
Expected: FAIL, `undefined: RefuseLocked`, `undefined: problem.TypeAccountLocked`, `undefined: revocationURL`.

- [ ] **Step 3: Implement**

`problem.go`, inside the type `const (...)` block after `TypeSpaceUnavailable`:

```go
	// TypeAccountLocked is a signed-in person whose CTech account is being
	// deleted (ctech-account's erasure saga). Every session request is refused
	// from the lock to the purge, even with a token that is still valid.
	TypeAccountLocked = "/problems/account-locked"
```

`api/internal/middleware/erasure.go`:

```go
package middleware

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
)

// RefuseLocked refuses every request made with a session token whose subject is
// locked or erased in the account-deletion saga (ctech-account
// docs/specs/2026-10-06-account-deletion-saga-protocol.md § 4.2, § 4.5).
//
// It runs inside Verifier.Middleware, which every user-facing group already
// mounts, so it is the one place where a person's `sub` enters billing.
// Service tokens (empty SID) pass: their subject is a client, not a person.
// The public checkout and wallet's notify-back carry no token at all. Inbound
// money is accepted, and the next eligibility check reports it (§ 4.2).
//
// Every method, not only writes: one rule is easier to prove than a list of
// safe reads. A failed lookup is a 500 and never a pass. The lock is the
// second layer behind the revocation list, and failing open would make it no
// layer at all.
func RefuseLocked(blocked func(ctx context.Context, sub string) (bool, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.SID == "" {
			return c.Next()
		}
		locked, err := blocked(c.Context(), cl.Sub)
		if err != nil {
			return problem.Internal("erro ao verificar a conta").WithCause(err).Send(c)
		}
		if locked {
			return problem.New(fiber.StatusForbidden, problem.TypeAccountLocked, "Account locked",
				"conta em processo de exclusão").Send(c)
		}
		return c.Next()
	}
}
```

`auth.go`:
- Add `"context"` to the imports.
- Replace the `Verifier` type, `NewVerifier`, and the tail of `Middleware`:

```go
type Verifier struct {
	*jwtverify.Verifier
	// lock is RefuseLocked. Nil when no erasure state is wired (unit tests that
	// build a bare verifier).
	lock fiber.Handler
}

func NewVerifier(jwksURL, audience, issuer string, backend cache.Backend) *Verifier {
	return &Verifier{Verifier: jwtverify.NewVerifier(jwksURL, audience, issuer, backend)}
}

// WithErasureLock refuses session tokens of a locked or erased subject on every
// route this verifier guards (RefuseLocked).
func (v *Verifier) WithErasureLock(blocked func(ctx context.Context, sub string) (bool, error)) *Verifier {
	v.lock = RefuseLocked(blocked)
	return v
}
```

In `Middleware`, replace

```go
		c.Locals(ClaimsKey, claims)
		return c.Next()
```

with

```go
		c.Locals(ClaimsKey, claims)
		if v.lock != nil {
			return v.lock(c)
		}
		return c.Next()
```

`api/internal/app/erasure.go`:

```go
package app

import (
	"fmt"
	"log/slog"
	"net/url"

	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/config"
)

// revocationURL is VALKEY_URL with its logical-DB path removed. ctech-account
// writes the token revocation list in DB 0 of the shared server (jwtverify,
// saga protocol § 5). Billing's own cache lives in DB 3, where those keys are
// invisible. Derived rather than configured: the userdata is at its 16 KiB
// ceiling (terraform/README).
func revocationURL(valkeyURL string) (string, error) {
	u, err := url.Parse(valkeyURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("valkey url %q has no host", valkeyURL)
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// revocationBackend is the DB 0 connection the verifier checks revoked subjects
// against. It is nil when there is no Valkey: the check is then off, and a
// locked person is still refused by the erasure lock.
func revocationBackend(cfg *config.Config) cache.Backend {
	if cfg.RedisURL == "" {
		return nil
	}
	raw, err := revocationURL(cfg.RedisURL)
	if err != nil {
		slog.Error("VALKEY_URL unusable for the revocation list — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	backend, err := cache.NewRedisBackend(raw)
	if err != nil {
		slog.Error("valkey DB 0 unavailable — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	return backend
}
```

`app.go`:
- Add the import `"gopkg.aoctech.app/api-commons/erasure"`.
- In `Build`, directly after `cacheBackend := newCache(cfg)`, add:

```go
	// The account-deletion lock (saga protocol § 4.2) and the token revocation
	// list (§ 5): two independent layers between a locked person and this API.
	erasureState := erasure.NewStore(db, cfg.TablePrefix, 0)
	verifier := middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechIssuerURL, cacheBackend).
		WithErasureLock(erasureState.Blocked)
	if rev := revocationBackend(cfg); rev != nil {
		verifier.WithRevocation(rev)
	}
```

and replace `Verifier:     middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechIssuerURL, cacheBackend),` in the `v1.Deps` literal with `Verifier:     verifier,`.

- [ ] **Step 4: Write the failing integration test**

`api/tests/integration/erasure_test.go`:

```go
//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// lockSubject applies a user.locked for sub, exactly as the saga consumer does.
func lockSubject(t *testing.T, sub string) {
	t.Helper()
	store := erasure.NewStore(testDB, testCfg.TablePrefix, 0)
	if _, err := store.Apply(ctxT(t), erasure.SubKey(sub), erasure.Message{
		Version: erasure.Version, Type: erasure.TypeLocked, RequestID: "req_" + id.New(), Sub: sub,
		Scope: erasure.ScopeAccount, Services: []string{"billing"}, IssuedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 3.
func TestALockedPersonIsRefusedWithATokenThatIsStillValid(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)
	if res := e.console(t, "/v1.0/portal/session", token, ""); res.status != http.StatusOK {
		t.Fatalf("before the lock: %d %s", res.status, res.body)
	}

	lockSubject(t, e.userID)

	res := e.console(t, "/v1.0/portal/session", token, "")
	if res.status != http.StatusForbidden || problemType(t, res.body) != problem.TypeAccountLocked {
		t.Fatalf("after the lock: %d %s, want 403 %s", res.status, res.body, problem.TypeAccountLocked)
	}
	// A service token is not a person: a user's erasure does not lock the M2M surface.
	m2m := e.token(t, e.client, "", middleware.ScopeProductsRead)
	if res := e.do(t, http.MethodGet, "/v1.0/products", m2m, "", ""); res.status != http.StatusOK {
		t.Fatalf("M2M after a user's lock: %d %s", res.status, res.body)
	}
}
```

- [ ] **Step 5: Run everything**

Run: `go test ./internal/middleware/ ./internal/app/ -count=1 && make test-integration`
Expected: `ok` for both packages, and `--- PASS: TestALockedPersonIsRefusedWithATokenThatIsStillValid` among a passing suite.

- [ ] **Step 6: Commit**

```bash
git add internal/problem internal/middleware internal/app tests/integration/erasure_test.go
git commit -m "feat(api): refuse a person locked by account deletion; check the jwt revocation list"
```

---

### Task 4: Every invoice and subscription of one customer; clearing subscription metadata

**Files:**
- Modify: `api/internal/repositories/ledger.go` (`queryPrefix` becomes a package function)
- Modify: `api/internal/repositories/recurrences.go` (its one caller)
- Modify: `api/internal/repositories/base.go` (`itemsWhere`)
- Modify: `api/internal/repositories/invoices.go` (`AllByCustomer`)
- Modify: `api/internal/repositories/subscriptions.go` (`AllByCustomer`, `ClearMetadata`)
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Produces:
  - `queryPrefix(ctx, b Base, pk, skPrefix string) ([]map[string]types.AttributeValue, error)`, package-level and unexported
  - `itemsWhere(items []map[string]types.AttributeValue, attr, want string) []map[string]types.AttributeValue`
  - `(*InvoiceRepository).AllByCustomer(ctx, organizationID string, livemode bool, customerID string) ([]billing.Invoice, error)`
  - `(*SubscriptionRepository).AllByCustomer(ctx, organizationID string, livemode bool, customerID string) ([]billing.Subscription, error)`
  - `(*SubscriptionRepository).ClearMetadata(ctx, s *billing.Subscription, now time.Time) error`
  - the integration helper `newSubscriptionWithMetadata(t, org, customerID, priceID string) *billing.Subscription`

- [ ] **Step 1: Write the failing test**

Append to `tests/integration/erasure_test.go` and extend its imports with `"gopkg.aoctech.app/billing/api/internal/domain/billing"`, `"gopkg.aoctech.app/billing/api/internal/domain/brcal"` and `"gopkg.aoctech.app/billing/api/internal/repositories"`:

```go
func newSubscriptionWithMetadata(t *testing.T, org *billing.Organization, customerID, priceID string) *billing.Subscription {
	t.Helper()
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: org.Livemode,
		CustomerID: customerID, Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
		Anchor:     brcal.New(2026, time.March, 1),
		Metadata:   billing.Metadata{"crm_contact": "pessoa@example.com"},
	}
	item := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: org.Livemode,
		SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1,
	}
	if err := repositories.NewSubscriptionRepository(testDB, testCfg).
		Create(ctxT(t), sub, []billing.SubscriptionItem{item}, now()); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestAllByCustomerReadsOneCustomerOnlyAndMetadataClears(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	other := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Outra Pessoa", Email: "outra@example.com",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, other, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	mine := newSubscriptionWithMetadata(t, e.org, e.customer.ID, e.priceID)
	newSubscriptionIn(t, e.org, other.ID, e.priceID, billing.SubscriptionActive)
	newSubscriptionInvoice(t, e.org, mine)
	newInvoiceFor(t, e.org, e.customer.ID)
	newInvoiceFor(t, e.org, other.ID)

	invoices, err := repositories.NewInvoiceRepository(testDB, testCfg).AllByCustomer(ctx, e.org.ID, true, e.customer.ID)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices = %d, %v; want this customer's 2 and none of the other's", len(invoices), err)
	}
	for _, inv := range invoices {
		if inv.CustomerID != e.customer.ID {
			t.Fatalf("invoice %s belongs to %s", inv.ID, inv.CustomerID)
		}
	}

	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	got, err := subs.AllByCustomer(ctx, e.org.ID, true, e.customer.ID)
	if err != nil || len(got) != 1 || got[0].ID != mine.ID || len(got[0].Metadata) == 0 {
		t.Fatalf("subscriptions = %+v, %v; want only %s, with its metadata", got, err, mine.ID)
	}
	if err := subs.ClearMetadata(ctx, &got[0], now()); err != nil {
		t.Fatal(err)
	}
	after, err := subs.Get(ctx, e.org.ID, true, mine.ID)
	if err != nil || len(after.Metadata) != 0 {
		t.Fatalf("metadata after clearing = %v, %v", after.Metadata, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go vet -tags integration ./tests/...`
Expected: FAIL, `AllByCustomer undefined` and `ClearMetadata undefined`.

- [ ] **Step 3: Implement**

Make `queryPrefix` a package function and repoint its callers:

```bash
sed -i 's/^func (r \*LedgerRepository) queryPrefix(/func queryPrefix(/; s/r\.queryPrefix(/queryPrefix(/g' internal/repositories/ledger.go
sed -i 's/r\.ledger\.queryPrefix(/queryPrefix(/' internal/repositories/recurrences.go
```

`base.go`: add the import `"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"` if it is not already there (it is, for `txItems`), and append:

```go
// itemsWhere keeps the raw items whose string attribute attr equals want. It
// filters before decoding, so rows of another shape that share a key prefix
// (an invoice's lines, a subscription's items) are dropped unread.
func itemsWhere(items []map[string]types.AttributeValue, attr, want string) []map[string]types.AttributeValue {
	out := make([]map[string]types.AttributeValue, 0, len(items))
	for _, it := range items {
		if v, ok := it[attr].(*types.AttributeValueMemberS); ok && v.Value == want {
			out = append(out, it)
		}
	}
	return out
}
```

`invoices.go`, after `ListByCustomer`:

```go
// AllByCustomer returns every invoice addressed to customerID, following
// continuation keys to the end. It serves account deletion, where missing one
// open invoice means erasing a debtor. ListByCustomer stays capped for the
// portal.
//
// ponytail: reads the tenant's whole INVOICE# range once per erasure; add a
// customer index if tenant zero's range ever takes more than seconds to read.
func (r *InvoiceRepository) AllByCustomer(ctx context.Context, organizationID string, livemode bool, customerID string) ([]billing.Invoice, error) {
	items, err := queryPrefix(ctx, r.base, TenantPK(organizationID, livemode), skInvoice)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[invoiceRow](itemsWhere(items, "customer_id", customerID))
	if err != nil {
		return nil, err
	}
	out := make([]billing.Invoice, 0, len(rows))
	for _, row := range rows {
		if row.Invoice.Status == "" {
			continue
		}
		out = append(out, row.Invoice)
	}
	return out, nil
}
```

`subscriptions.go`, after `ListByCustomer`:

```go
// AllByCustomer returns every subscription of customerID, following
// continuation keys to the end. It serves account deletion, which must cancel
// all of them, not the first page.
func (r *SubscriptionRepository) AllByCustomer(ctx context.Context, organizationID string, livemode bool, customerID string) ([]billing.Subscription, error) {
	items, err := queryPrefix(ctx, r.base, TenantPK(organizationID, livemode), skSubscription)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[subscriptionRow](itemsWhere(items, "customer_id", customerID))
	if err != nil {
		return nil, err
	}
	out := make([]billing.Subscription, 0, len(rows))
	for _, row := range rows {
		if row.Subscription.Status == "" {
			continue
		}
		out = append(out, row.entity())
	}
	return out, nil
}

// ClearMetadata removes the subscription's metadata. Account deletion erases
// what is not tied to an invoice (data inventory § 6). Metadata is free-form,
// so it is the one place undeclared personal data can sit on a subscription
// (ADR 0008). Invoices carry their own copy, which stays with the document.
func (r *SubscriptionRepository) ClearMetadata(ctx context.Context, s *billing.Subscription, now time.Time) error {
	if _, err := r.base.UpdateItem(ctx, TenantPK(s.OrganizationID, s.Livemode), new(SubscriptionSK(s.ID)), map[string]any{
		"metadata":   nil,
		"updated_at": now.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}
	s.Metadata = nil
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/repositories/ -count=1 && make test-integration`
Expected: `ok`, and `--- PASS: TestAllByCustomerReadsOneCustomerOnlyAndMetadataClears` among a passing suite (the ledger and recurrence tests still pass after the `queryPrefix` move).

- [ ] **Step 5: Commit**

```bash
git add internal/repositories tests/integration/erasure_test.go
git commit -m "feat(api): read every invoice and subscription of a customer; clear subscription metadata"
```

---

### Task 5: `SpacePurger` — every row of one finance space, resumable

**Files:**
- Create: `api/internal/repositories/finance_purge.go`
- Create: `api/internal/repositories/finance_purge_test.go`
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `queryPrefix`, `DecodeItems[txItem]`, `LedgerEntryPK(sp, account)` (Task 4 and existing code).
- Produces:
  - `repositories.NewSpacePurger(db *dynamodb.Client, cfg *config.Config) *SpacePurger`
  - `(*SpacePurger).PurgeSpace(ctx context.Context, sp space.ResolvedSpace) (map[string]int, error)`, which returns the rows deleted per logical table
  - the integration helpers `seedFinance(t, sp)`, `rowsUnder(t, logical, pk) int` and `assertSpaceGone(t, sp)`

- [ ] **Step 1: Write the failing unit tests**

`api/internal/repositories/finance_purge_test.go`:

```go
package repositories

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/space"
)

// notSpaceScoped is every table that PurgeSpace does not clear as a finance
// table, with the reason. ADR 0026: "a missing TTL or a missing purge path" is
// a review failure, so a new table must land in exactly one of the two lists.
// A table whose rows live in partitions other than S (cards: S#CARD#{id})
// also needs its own step in PurgeSpace, as the S#ACCOUNT# entries have.
var notSpaceScoped = map[string]string{
	TableOrganizations: "a merchant tenant (ADR 0007); its owner is an eligibility blocker",
	TableCredentials:   "integration credentials, no person",
	TableCustomers:     "the tenant-zero customer is anonymized in place by services.Eraser (ADR 0009)",
	TableProducts:      "catalogue, no person",
	TablePrices:        "catalogue, no person",
	TableSubscriptions: "canceled by services.Eraser and retained: it explains invoices (ADR 0009)",
	TableInvoices:      "fiscal documents, retained (ADR 0009)",
	TableUsage:         "24-month TTL, no personal data",
	TableWebhooks:      "90-day TTL; the payload is an id and a type",
	TableErasureState:  "the saga's own state and tombstones",
	TableAudit:         "purged under S for personal spaces only (ADR 0026); five-year TTL otherwise",
	TableIdempotency:   "purged under S by PurgeSpace, outside financeTables",
}

func TestEveryTableIsClassifiedForThePurge(t *testing.T) {
	for _, name := range TableNames() {
		_, other := notSpaceScoped[name]
		purged := slices.Contains(financeTables, name)
		if other == purged {
			t.Errorf("table %q must be in exactly one of financeTables (purged under the space) or notSpaceScoped (with the reason it is not)", name)
		}
	}
}

func TestPurgeSpaceRefusesTheZeroSpace(t *testing.T) {
	// A nil client would panic on any call, so reaching DynamoDB fails loudly.
	if _, err := (&SpacePurger{}).PurgeSpace(context.Background(), space.ResolvedSpace{}); !errors.Is(err, space.ErrNoSpace) {
		t.Fatalf("err = %v, want space.ErrNoSpace", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/repositories/ -run 'TestEveryTableIsClassifiedForThePurge|TestPurgeSpaceRefusesTheZeroSpace'`
Expected: FAIL, `undefined: financeTables`, `undefined: SpacePurger`.

- [ ] **Step 3: Implement**

`api/internal/repositories/finance_purge.go`:

```go
package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// financeTables are the tables whose every row lives under a finance space key
// S (spec § 4). PurgeSpace clears each one's S partition.
// TestEveryTableIsClassifiedForThePurge makes a new table choose a side.
var financeTables = []string{TableLedgerAccounts, TableLedgerTransactions, TableBills, TableRecurrences}

// batchDeleteAttempts bounds the retries of a BatchWriteItem's unprocessed
// keys. Running out is an error. The saga redelivers, and the purge restarts
// from a fresh read.
const batchDeleteAttempts = 5

// SpacePurger deletes a finance space (ADR 0026): a personal space when its
// owner's CTech account is deleted, an organization space when the organization
// is erased with its only member.
//
// It is not a LedgerRepository method on purpose. The ledger has no edit path
// (TestLedgerRepositoryHasNoEditPath), and erasure is not an edit. It is the
// end of the space.
type SpacePurger struct {
	bases map[string]Base
}

func NewSpacePurger(db *dynamodb.Client, cfg *config.Config) *SpacePurger {
	bases := map[string]Base{}
	for _, t := range append([]string{TableAudit, TableIdempotency}, financeTables...) {
		bases[t] = NewBase(db, cfg, t)
	}
	return &SpacePurger{bases: bases}
}

// PurgeSpace deletes every row of sp and returns how many it deleted per
// logical table. Idempotent and resumable: every run re-reads from scratch.
// Entry partitions are deleted before the chart and the transaction headers
// that name their accounts, so a run that dies anywhere still finds what is
// left.
func (p *SpacePurger) PurgeSpace(ctx context.Context, sp space.ResolvedSpace) (map[string]int, error) {
	if sp.IsZero() {
		return nil, space.ErrNoSpace
	}
	counts := map[string]int{}
	s := sp.PK()

	// 1. The per-account entry partitions S#ACCOUNT#{id}. The ids come from the
	// chart and from every transaction's legs, so entries are found even after
	// a previous run deleted the chart.
	accounts := map[string]bool{}
	chart, err := queryPrefix(ctx, p.bases[TableLedgerAccounts], s, "ACCOUNT#")
	if err != nil {
		return nil, err
	}
	for _, it := range chart {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			accounts[strings.TrimPrefix(sk.Value, "ACCOUNT#")] = true
		}
	}
	headers, err := queryPrefix(ctx, p.bases[TableLedgerTransactions], s, "TX#")
	if err != nil {
		return nil, err
	}
	txs, err := DecodeItems[txItem](headers)
	if err != nil {
		return nil, err
	}
	for _, tx := range txs {
		for _, leg := range tx.Legs {
			accounts[leg.Account] = true
		}
	}
	for account := range accounts {
		n, err := p.deletePartition(ctx, TableLedgerTransactions, LedgerEntryPK(sp, account))
		counts[TableLedgerTransactions] += n
		if err != nil {
			return counts, err
		}
	}

	// 2. The space partition S in every table that keys by it. Audit rows go
	// only with a personal space: an organization's finance audit rows keep
	// ADR 0009's five years (ADR 0026, "Finance audit rows").
	tables := append(append([]string(nil), financeTables...), TableIdempotency)
	if sp.Personal() {
		tables = append(tables, TableAudit)
	}
	for _, t := range tables {
		n, err := p.deletePartition(ctx, t, s)
		counts[t] += n
		if err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// deletePartition deletes every row under pk, 25 keys per BatchWriteItem,
// retrying unprocessed keys with a short backoff.
func (p *SpacePurger) deletePartition(ctx context.Context, logical, pk string) (int, error) {
	b := p.bases[logical]
	items, err := queryPrefix(ctx, b, pk, "")
	if err != nil {
		return 0, err
	}
	deleted := 0
	for start := 0; start < len(items); start += 25 {
		end := min(start+25, len(items))
		reqs := make([]types.WriteRequest, 0, end-start)
		for _, it := range items[start:end] {
			reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{
				Key: map[string]types.AttributeValue{"pk": it["pk"], "sk": it["sk"]},
			}})
		}
		pending := map[string][]types.WriteRequest{b.TableName: reqs}
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt == batchDeleteAttempts {
				return deleted, fmt.Errorf("purge %s %s: keys still unprocessed after %d attempts", logical, pk, attempt)
			}
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return deleted, ctx.Err()
				case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
				}
			}
			out, err := b.BatchWriteItemRaw(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
			if err != nil {
				return deleted, err
			}
			pending = out.UnprocessedItems
		}
		deleted += end - start
	}
	return deleted, nil
}
```

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `go test ./internal/repositories/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Write the integration tests**

Append to `tests/integration/erasure_test.go` and extend its imports with `"context"`, `"github.com/aws/aws-sdk-go-v2/aws"`, `"github.com/aws/aws-sdk-go-v2/service/dynamodb"`, `"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"`, `"gopkg.aoctech.app/billing/api/internal/domain/finance"` and `"gopkg.aoctech.app/billing/api/internal/space"`:

```go
var seedNow = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

// seedFinance writes a space as a person uses it: the chart, a transfer, a
// recognised bill, a recurrence and a cached idempotent response.
func seedFinance(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	ctx := context.Background()
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	if err := ledger.EnsureSpace(ctx, sp, seedNow); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "cash", Name: "Caixa", Class: finance.ClassAsset},
		{ID: "rent", Name: "Aluguel", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := ledger.CreateAccount(ctx, sp, a, seedNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ledger.Post(ctx, sp, transfer(t, "bank", "cash", 1000, brcal.New(2026, time.March, 2)),
		repositories.PostMeta{Actor: "u"}, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.NewBillRepository(testDB, testCfg).Create(ctx, sp, finance.Bill{
		Direction: finance.Payable, Amount: 5000, AccountID: "bank", CategoryID: "rent", Description: "Aluguel",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10), Origin: finance.OriginManual,
	}, repositories.PostMeta{Actor: "u"}, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.NewRecurrenceRepository(testDB, testCfg).Create(ctx, sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, seedNow); err != nil {
		t.Fatal(err)
	}
	if err := repositories.NewIdempotencyRepository(testDB, testCfg).Store(ctx, sp.Owner(), sp.Livemode(),
		repositories.IdempotencyRecord{Key: "k-" + id.New(), RequestHash: "h", Status: 201, Response: "{}", Route: "/x"}, seedNow); err != nil {
		t.Fatal(err)
	}
}

// rowsUnder counts the rows of one partition, read straight from the table.
func rowsUnder(t *testing.T, logical, pk string) int {
	t.Helper()
	out, err := testDB.Query(ctxT(t), &dynamodb.QueryInput{
		TableName:                 aws.String(repositories.PhysicalName(testCfg.TablePrefix, logical)),
		KeyConditionExpression:    aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}},
		Select:                    types.SelectCount,
		ConsistentRead:            aws.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	return int(out.Count)
}

// assertSpaceGone checks every partition seedFinance writes to, except audit,
// which depends on the kind of space.
func assertSpaceGone(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	for _, table := range []string{repositories.TableLedgerAccounts, repositories.TableLedgerTransactions,
		repositories.TableBills, repositories.TableRecurrences, repositories.TableIdempotency} {
		if n := rowsUnder(t, table, sp.PK()); n != 0 {
			t.Errorf("%s still holds %d rows under %s", table, n, sp.PK())
		}
	}
	for _, account := range []string{"bank", "cash", "rent"} {
		if n := rowsUnder(t, repositories.TableLedgerTransactions, sp.PK()+"#ACCOUNT#"+account); n != 0 {
			t.Errorf("entries of %s survive: %d rows", account, n)
		}
	}
}

// Review Focus 1.
func TestPurgeSpaceErasesEveryRowAndIsSafeToRepeat(t *testing.T) {
	ctx := ctxT(t)
	sp := jobSpace(t, "USER#usr_"+id.New(), true)
	seedFinance(t, sp)
	for _, table := range []string{repositories.TableLedgerAccounts, repositories.TableLedgerTransactions,
		repositories.TableBills, repositories.TableRecurrences, repositories.TableAudit, repositories.TableIdempotency} {
		if rowsUnder(t, table, sp.PK()) == 0 {
			t.Fatalf("the seed wrote nothing to %s, so this test would prove nothing", table)
		}
	}

	purger := repositories.NewSpacePurger(testDB, testCfg)
	counts, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	assertSpaceGone(t, sp)
	if n := rowsUnder(t, repositories.TableAudit, sp.PK()); n != 0 {
		t.Fatalf("a personal space's audit rows survive: %d (ADR 0026)", n)
	}
	if counts[repositories.TableLedgerTransactions] == 0 || counts[repositories.TableAudit] == 0 {
		t.Fatalf("counts = %v", counts)
	}

	again, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range again {
		if n != 0 {
			t.Errorf("second run deleted %d rows from %s; a finished purge has nothing left", n, table)
		}
	}
}

// Review Focus 1: a run that died after deleting the chart still finds the
// entries, through the legs of the transaction headers.
func TestPurgeFindsEntriesAfterTheChartIsGone(t *testing.T) {
	ctx := ctxT(t)
	sp := jobSpace(t, "USER#usr_"+id.New(), true)
	seedFinance(t, sp)
	out, err := testDB.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(repositories.PhysicalName(testCfg.TablePrefix, repositories.TableLedgerAccounts)),
		KeyConditionExpression:    aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: sp.PK()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range out.Items {
		if _, err := testDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(repositories.PhysicalName(testCfg.TablePrefix, repositories.TableLedgerAccounts)),
			Key:       map[string]types.AttributeValue{"pk": it["pk"], "sk": it["sk"]},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	assertSpaceGone(t, sp)
}

func TestAnOrganizationSpaceKeepsItsAuditRows(t *testing.T) {
	sp := jobSpace(t, newSpaceOrgID(), true)
	seedFinance(t, sp)
	if _, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(ctxT(t), sp); err != nil {
		t.Fatal(err)
	}
	assertSpaceGone(t, sp)
	if rowsUnder(t, repositories.TableAudit, sp.PK()) == 0 {
		t.Fatal("an organization's finance audit rows keep ADR 0009's five years (ADR 0026)")
	}
}
```

- [ ] **Step 6: Run the integration suite**

Run: `make test-integration`
Expected: PASS for `TestPurgeSpaceErasesEveryRowAndIsSafeToRepeat`, `TestPurgeFindsEntriesAfterTheChartIsGone` and `TestAnOrganizationSpaceKeepsItsAuditRows`, among a passing suite.

- [ ] **Step 7: Commit**

```bash
git add internal/repositories/finance_purge.go internal/repositories/finance_purge_test.go tests/integration/erasure_test.go
git commit -m "feat(api): SpacePurger erases a finance space, resumable (ADR 0026)"
```

---

### Task 6: `services.Eraser` — blockers and the purge

**Files:**
- Create: `api/internal/services/erasing.go`
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: the repository methods from Tasks 4–5, `CustomerRepository.GetByUser`/`Anonymize`, `OrganizationRepository.GetByOwner`, `SubscriptionRepository.Transition`, and `erasure.Blocker`/`Ack`/`Message`/`ResultDone`/`ResultBlocked`.
- Produces:
  - `services.BlockerInvoiceOpen = "billing.invoice_open"`, `services.BlockerInvoiceUncollectible = "billing.invoice_uncollectible"` and `services.BlockerTenantOwner = "billing.tenant_owner"`
  - `services.NewEraser(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository, invoices *repositories.InvoiceRepository, orgs *repositories.OrganizationRepository, portalOrganizationID, invoicesURL string) *Eraser`
  - `(*Eraser).WithPurge(p *repositories.SpacePurger, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)) *Eraser`
  - `(*Eraser).Blockers(ctx context.Context, sub string) ([]erasure.Blocker, error)`
  - `(*Eraser).Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error)`, matching `erasure.PurgeFunc`
  - Ack `Counts` keys: `customers_anonymized`, `subscriptions_canceled`, `invoices_retained`, `finance_{table}`

- [ ] **Step 1: Write the failing integration tests**

Append to `tests/integration/erasure_test.go` and extend its imports with `"strings"` and `"gopkg.aoctech.app/billing/api/internal/services"`:

```go
func eraserFor(e *portalEnv) *services.Eraser {
	return services.NewEraser(
		repositories.NewCustomerRepository(testDB, testCfg),
		repositories.NewSubscriptionRepository(testDB, testCfg),
		repositories.NewInvoiceRepository(testDB, testCfg),
		repositories.NewOrganizationRepository(testDB, testCfg),
		e.org.ID, "https://billing.test/invoices",
	).WithPurge(repositories.NewSpacePurger(testDB, testCfg), space.ForJob)
}

func eraseMessage(sub string, orgs ...string) erasure.Message {
	return erasure.Message{
		Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req_" + id.New(), Sub: sub,
		Scope: erasure.ScopeAccount, Services: []string{"billing"}, Organizations: orgs, Attempt: 1, IssuedAt: time.Now(),
	}
}

func finalizeInvoice(t *testing.T, inv *billing.Invoice) {
	t.Helper()
	due := brcal.New(2026, time.March, 20)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctxT(t), inv, due, due, billing.CauseScheduler, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 2: an invoice issued while the account was locked is caught by
// the purge's own re-check, and nothing is half-erased.
func TestPurgeIsRefusedWhileAnInvoiceIsOpenAndChangesNothing(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	sub := newSubscriptionIn(t, e.org, e.customer.ID, e.priceID, billing.SubscriptionActive)
	finalizeInvoice(t, newSubscriptionInvoice(t, e.org, sub))
	personal := jobSpace(t, "USER#"+e.userID, true)
	seedFinance(t, personal)

	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID))
	if err != nil || ack.Result != erasure.ResultBlocked || len(ack.Blockers) != 1 || ack.Blockers[0].Code != services.BlockerInvoiceOpen {
		t.Fatalf("ack = %+v, %v; want blocked by %s", ack, err, services.BlockerInvoiceOpen)
	}
	if ack.Blockers[0].ActionURL != "https://billing.test/invoices" || ack.Blockers[0].Detail["overdue"] != 1 {
		t.Fatalf("blocker = %+v", ack.Blockers[0])
	}
	c, err := repositories.NewCustomerRepository(testDB, testCfg).Get(ctx, e.org.ID, true, e.customer.ID)
	if err != nil || c.Anonymized {
		t.Fatalf("customer anonymized by a blocked purge: %+v, %v", c, err)
	}
	s, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID)
	if err != nil || s.Status != billing.SubscriptionActive {
		t.Fatalf("subscription touched by a blocked purge: %+v, %v", s, err)
	}
	if rowsUnder(t, repositories.TableBills, personal.PK()) == 0 {
		t.Fatal("the personal space was purged by a blocked purge")
	}
}

// Review Focus 5.
func TestPurgeErasesThePersonAndKeepsTheDocuments(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	sub := newSubscriptionWithMetadata(t, e.org, e.customer.ID, e.priceID)
	inv := newSubscriptionInvoice(t, e.org, sub) // a draft: issued to nobody yet, so not owed
	// Somebody else's open invoice is not this person's debt.
	other := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true, Name: "Outra", Email: "o@example.com"}
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	if err := customers.Create(ctx, other, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	finalizeInvoice(t, newInvoiceFor(t, e.org, other.ID))

	personal := jobSpace(t, "USER#"+e.userID, true)
	personalTest := jobSpace(t, "USER#"+e.userID, false)
	erasedOrg := jobSpace(t, newSpaceOrgID(), true)
	survivor := jobSpace(t, newSpaceOrgID(), true)
	for _, sp := range []space.ResolvedSpace{personal, personalTest, erasedOrg, survivor} {
		seedFinance(t, sp)
	}
	survivorRows := rowsUnder(t, repositories.TableLedgerAccounts, survivor.PK())

	m := eraseMessage(e.userID, erasedOrg.OrganizationID())
	eraser := eraserFor(e)
	ack, err := eraser.Purge(ctx, m)
	if err != nil || ack.Result != erasure.ResultDone {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	if ack.Counts["customers_anonymized"] != 1 || ack.Counts["subscriptions_canceled"] != 1 || ack.Counts["invoices_retained"] != 1 {
		t.Fatalf("counts = %v", ack.Counts)
	}

	c, err := customers.Get(ctx, e.org.ID, true, e.customer.ID)
	if err != nil || !c.Anonymized || c.Email != "" || c.Name != "[anonimizado]" {
		t.Fatalf("customer = %+v, %v; want anonymized in place", c, err)
	}
	s, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID)
	if err != nil || s.Status != billing.SubscriptionCanceled || len(s.Metadata) != 0 {
		t.Fatalf("subscription = %+v, %v; want canceled, metadata cleared, row kept", s, err)
	}
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctx, e.org.ID, true, inv.ID); err != nil {
		t.Fatalf("the invoice is a document and must survive: %v", err)
	}
	for _, sp := range []space.ResolvedSpace{personal, personalTest, erasedOrg} {
		assertSpaceGone(t, sp)
	}
	if rowsUnder(t, repositories.TableAudit, personal.PK()) != 0 {
		t.Fatal("personal-space audit rows survive (ADR 0026)")
	}
	if rowsUnder(t, repositories.TableAudit, erasedOrg.PK()) == 0 {
		t.Fatal("the erased organization's finance audit rows must keep their five years (ADR 0026)")
	}
	if got := rowsUnder(t, repositories.TableLedgerAccounts, survivor.PK()); got != survivorRows {
		t.Fatalf("an organization that survives lost rows: %d → %d", survivorRows, got)
	}

	again, err := eraser.Purge(ctx, m)
	if err != nil || again.Result != erasure.ResultDone || again.Counts["customers_anonymized"] != 0 || again.Counts["subscriptions_canceled"] != 0 {
		t.Fatalf("second purge = %+v, %v; want done with nothing left to do", again, err)
	}
	for k, v := range again.Counts {
		if strings.HasPrefix(k, "finance_") && v != 0 {
			t.Errorf("second purge deleted %d rows from %s", v, k)
		}
	}
}

func TestOwningATenantBlocksErasure(t *testing.T) {
	e := newPortal(t)
	owner := e.org.OwnerUserID
	blockers, err := eraserFor(e).Blockers(ctxT(t), owner)
	if err != nil || len(blockers) != 1 || blockers[0].Code != services.BlockerTenantOwner || blockers[0].Detail["organization_id"] != e.org.ID {
		t.Fatalf("blockers = %+v, %v; want %s for %s", blockers, err, services.BlockerTenantOwner, e.org.ID)
	}
}

// A written-off CTech debt still blocks deletion (user decision 2026-10-08).
// It gets its own code and no action_url, because the portal cannot collect
// it. The purge's re-check refuses too, and changes nothing.
func TestAWrittenOffInvoiceBlocksErasure(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	finalizeInvoice(t, inv)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Transition(ctx, inv, billing.InvoiceUncollectible,
		billing.CauseDunningExhausted, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	blockers, err := eraserFor(e).Blockers(ctx, e.userID)
	if err != nil || len(blockers) != 1 || blockers[0].Code != services.BlockerInvoiceUncollectible ||
		blockers[0].ActionURL != "" || blockers[0].Detail["amount_cents"] != int64(4990) || blockers[0].Detail["count"] != 1 {
		t.Fatalf("blockers = %+v, %v; want one %s for 4990 with no action_url", blockers, err, services.BlockerInvoiceUncollectible)
	}
	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID))
	if err != nil || ack.Result != erasure.ResultBlocked {
		t.Fatalf("purge = %+v, %v; want blocked", ack, err)
	}
	c, err := repositories.NewCustomerRepository(testDB, testCfg).Get(ctx, e.org.ID, true, e.customer.ID)
	if err != nil || c.Anonymized {
		t.Fatalf("a blocked purge anonymized the customer: %+v, %v", c, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go vet -tags integration ./tests/...`
Expected: FAIL, `undefined: services.NewEraser`.

- [ ] **Step 3: Implement**

`api/internal/services/erasing.go`:

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Blocker codes, stable and translated by ctech-account's UI. Never rename.
const (
	// BlockerInvoiceOpen is a live tenant-zero invoice still owed. The person
	// pays it in the portal (action_url) and asks again.
	BlockerInvoiceOpen = "billing.invoice_open"
	// BlockerInvoiceUncollectible is a live tenant-zero invoice that dunning
	// wrote off with an amount still due. A written-off debt still blocks (user
	// decision 2026-10-08). The portal cannot collect it, so support records the
	// payment (CauseManualPayment), and the blocker has no action_url.
	BlockerInvoiceUncollectible = "billing.invoice_uncollectible"
	// BlockerTenantOwner is a person who owns a billing tenant. Erasing them
	// would leave its console with nobody to reach it. Support moves the owner.
	BlockerTenantOwner = "billing.tenant_owner"
)

// erasureActor names the saga in the audit trail of what it changes.
const erasureActor = "account-erasure"

// Eraser is billing's side of the account-deletion saga (ctech-account
// docs/specs/2026-10-06-account-deletion-saga-protocol.md § 4; data inventory
// § 6). Blockers answers the eligibility question, and Purge is the consumer's
// PurgeFunc.
//
// What it keeps and why is ADR 0009 and ADR 0026: the invoice aggregate and the
// canceled subscription are documents; the tenant-zero customer is anonymized
// in place; the person's finance spaces, and those of the organizations erased
// with them, are deleted.
type Eraser struct {
	customers   *repositories.CustomerRepository
	subs        *repositories.SubscriptionRepository
	invoices    *repositories.InvoiceRepository
	orgs        *repositories.OrganizationRepository
	portalOrg   string
	invoicesURL string

	purger   *repositories.SpacePurger
	spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)
}

// NewEraser builds the eligibility side. portalOrganizationID is tenant zero
// (empty: there is no customer to look for). invoicesURL is the portal page a
// blocker points the person at (empty: no action_url).
func NewEraser(
	customers *repositories.CustomerRepository,
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	orgs *repositories.OrganizationRepository,
	portalOrganizationID, invoicesURL string,
) *Eraser {
	return &Eraser{customers: customers, subs: subs, invoices: invoices, orgs: orgs,
		portalOrg: portalOrganizationID, invoicesURL: invoicesURL}
}

// WithPurge adds what Purge needs. spaceFor is space.ForJob, passed in from a
// cmd/ binary: ForJob is forbidden under internal/.
func (e *Eraser) WithPurge(p *repositories.SpacePurger, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)) *Eraser {
	e.purger, e.spaceFor = p, spaceFor
	return e
}

// Blockers reports why sub cannot be erased yet. An unknown sub has none.
// Live mode only: test money is not owed (R15).
func (e *Eraser) Blockers(ctx context.Context, sub string) ([]erasure.Blocker, error) {
	var out []erasure.Blocker
	org, err := e.orgs.GetByOwner(ctx, sub, true)
	switch {
	case err == nil:
		out = append(out, erasure.Blocker{Code: BlockerTenantOwner, Detail: map[string]any{"organization_id": org.ID}})
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, err
	}
	if e.portalOrg == "" {
		return out, nil
	}
	customer, err := e.customers.GetByUser(ctx, e.portalOrg, true, sub)
	if errors.Is(err, repositories.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	invoices, err := e.invoices.AllByCustomer(ctx, e.portalOrg, true, customer.ID)
	if err != nil {
		return nil, err
	}
	var open, overdue, writtenOff int
	var openDue, writtenOffDue billing.Cents
	today := brcal.Today()
	for _, inv := range invoices {
		if inv.AmountDue() <= 0 {
			continue
		}
		switch inv.Status {
		case billing.InvoiceOpen:
			open++
			openDue += inv.AmountDue()
			if inv.IsOverdue(today) {
				overdue++
			}
		case billing.InvoiceUncollectible:
			writtenOff++
			writtenOffDue += inv.AmountDue()
		}
	}
	if open > 0 {
		out = append(out, erasure.Blocker{
			Code:      BlockerInvoiceOpen,
			Detail:    map[string]any{"count": open, "overdue": overdue, "amount_cents": int64(openDue)},
			ActionURL: e.invoicesURL,
		})
	}
	if writtenOff > 0 {
		// No action_url: the portal cannot collect a written-off invoice
		// (Invoice.Payable needs OPEN). Support records the payment.
		out = append(out, erasure.Blocker{
			Code:   BlockerInvoiceUncollectible,
			Detail: map[string]any{"count": writtenOff, "amount_cents": int64(writtenOffDue)},
		})
	}
	return out, nil
}

// Purge erases m.Sub's billing data and the finance spaces of
// m.Organizations. It re-checks the blockers first and never half-purges.
// Idempotent and resumable: every step re-reads and skips what is already done.
func (e *Eraser) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
	if e.purger == nil || e.spaceFor == nil {
		return erasure.Ack{}, errors.New("eraser: purge is not configured")
	}
	blockers, err := e.Blockers(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	if len(blockers) > 0 {
		return erasure.Ack{Result: erasure.ResultBlocked, Blockers: blockers}, nil
	}

	counts := map[string]int{}
	now := time.Now()
	if e.portalOrg != "" {
		for _, live := range []bool{true, false} {
			if err := e.eraseCustomer(ctx, live, m, counts, now); err != nil {
				return erasure.Ack{}, err
			}
		}
	}
	for _, owner := range append([]string{"USER#" + m.Sub}, m.Organizations...) {
		for _, live := range []bool{true, false} {
			sp, err := e.spaceFor(owner, live)
			if err != nil {
				// A sub or organization id that cannot name a space never had one.
				continue
			}
			n, err := e.purger.PurgeSpace(ctx, sp)
			for table, c := range n {
				counts["finance_"+table] += c
			}
			if err != nil {
				return erasure.Ack{}, fmt.Errorf("purging space %s: %w", sp.PK(), err)
			}
		}
	}
	return erasure.Ack{Result: erasure.ResultDone, Counts: counts}, nil
}

// eraseCustomer cancels the person's tenant-zero subscriptions, clears their
// metadata, and anonymizes the customer. Invoices are counted, never touched.
func (e *Eraser) eraseCustomer(ctx context.Context, live bool, m erasure.Message, counts map[string]int, now time.Time) error {
	c, err := e.customers.GetByUser(ctx, e.portalOrg, live, m.Sub)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	subs, err := e.subs.AllByCustomer(ctx, e.portalOrg, live, c.ID)
	if err != nil {
		return err
	}
	for i := range subs {
		s := &subs[i]
		if s.Status != billing.SubscriptionCanceled {
			// Immediate, not at period end: the person is leaving now. Every live
			// status has this edge under CauseCustomer (R3).
			if _, err := e.subs.Transition(ctx, s, billing.SubscriptionCanceled, billing.CauseCustomer, erasureActor, m.RequestID, now); err != nil {
				return fmt.Errorf("cancelling subscription %s: %w", s.ID, err)
			}
			counts["subscriptions_canceled"]++
		}
		if len(s.Metadata) > 0 {
			if err := e.subs.ClearMetadata(ctx, s, now); err != nil {
				return err
			}
		}
	}
	invoices, err := e.invoices.AllByCustomer(ctx, e.portalOrg, live, c.ID)
	if err != nil {
		return err
	}
	counts["invoices_retained"] += len(invoices)
	if !c.Anonymized {
		if err := e.customers.Anonymize(ctx, c, erasureActor, m.RequestID, now); err != nil {
			return err
		}
		counts["customers_anonymized"]++
	}
	return nil
}
```

- [ ] **Step 4: Run the suites**

Run: `go vet ./... && go test ./internal/services/ -count=1 && make test-integration`
Expected: `ok`, and PASS for `TestPurgeIsRefusedWhileAnInvoiceIsOpenAndChangesNothing`, `TestPurgeErasesThePersonAndKeepsTheDocuments`, `TestOwningATenantBlocksErasure` and `TestAWrittenOffInvoiceBlocksErasure`.

- [ ] **Step 5: Commit**

```bash
git add internal/services/erasing.go tests/integration/erasure_test.go
git commit -m "feat(api): billing's erasure purge and blockers (data inventory § 6)"
```

---

### Task 7: The eligibility route

**Files:**
- Modify: `api/internal/middleware/scope.go`
- Create: `api/internal/api/v1/erasure.go`
- Modify: `api/internal/api/v1/router.go` (`Deps`, `Register`)
- Modify: `api/internal/app/erasure.go`, `api/internal/app/erasure_test.go`, `api/internal/app/app.go`
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `services.NewEraser`, `(*Eraser).Blockers` (Task 6).
- Produces:
  - `middleware.ScopeErasureEligibility = "internal:billing:erasure-eligibility"`
  - `v1.Deps.Eraser *services.Eraser`
  - `GET /v1.0/internal/erasure/eligibility/:sub`, which returns `erasure.Eligibility`
  - `app.newEraser(db *dynamodb.Client, cfg *config.Config) *services.Eraser`
  - `app.portalInvoicesURL(checkoutBaseURL string) string`

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/app/erasure_test.go`:

```go
func TestPortalInvoicesURLIsTheCheckoutsOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://billing.aoctech.app/checkout":     "https://billing.aoctech.app/invoices",
		"https://billing-dev.aoctech.app/checkout": "https://billing-dev.aoctech.app/invoices",
		"": "",
	} {
		if got := portalInvoicesURL(in); got != want {
			t.Errorf("portalInvoicesURL(%q) = %q, want %q", in, got, want)
		}
	}
}
```

Append to `tests/integration/erasure_test.go`:

```go
func TestEligibilityAnswersCtechAccountAboutOnePerson(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	asAccount := e.token(t, "ctech-account", "", middleware.ScopeErasureEligibility)
	ask := func(sub, token string) (int, erasure.Eligibility) {
		t.Helper()
		res := e.do(t, http.MethodGet, "/v1.0/internal/erasure/eligibility/"+sub, token, "", "")
		var el erasure.Eligibility
		if res.status == http.StatusOK {
			res.decode(t, &el)
		}
		return res.status, el
	}

	if code, el := ask(e.userID, asAccount); code != http.StatusOK || !el.Eligible || el.Blockers == nil || len(el.Blockers) != 0 {
		t.Fatalf("before any invoice: %d %+v; want eligible with blockers []", code, el)
	}
	finalizeInvoice(t, newInvoiceFor(t, e.org, e.customer.ID))
	code, el := ask(e.userID, asAccount)
	if code != http.StatusOK || el.Eligible || len(el.Blockers) != 1 || el.Blockers[0].Code != services.BlockerInvoiceOpen ||
		el.Blockers[0].Detail["amount_cents"] != float64(4990) {
		t.Fatalf("with an open invoice: %d %+v", code, el)
	}
	if code, el := ask("usr_"+id.New(), asAccount); code != http.StatusOK || !el.Eligible {
		t.Fatalf("an unknown sub has nothing to delete: %d %+v", code, el)
	}
	if code, _ := ask(e.userID, e.portalToken(t, middleware.ScopeErasureEligibility)); code != http.StatusForbidden {
		t.Fatalf("a session token: %d, want 403", code)
	}
	if code, _ := ask(e.userID, e.token(t, "ctech-account", "", middleware.ScopeProductsRead)); code != http.StatusForbidden {
		t.Fatalf("a service token without the scope: %d, want 403", code)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/ -run TestPortalInvoicesURL; go vet -tags integration ./tests/...`
Expected: FAIL, `undefined: portalInvoicesURL` and `undefined: middleware.ScopeErasureEligibility`.

- [ ] **Step 3: Implement**

`scope.go`, after the `AllScopes` declaration:

```go
// ScopeErasureEligibility is carried only by the token ctech-account mints for
// itself to ask whether a person may be erased (saga protocol § 4.1). It is NOT
// in AllScopes or scope-manifest.json: those list what a client can be
// granted, and no client may be granted this one.
const ScopeErasureEligibility = "internal:billing:erasure-eligibility"
```

`api/internal/api/v1/erasure.go`:

```go
package v1

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// registerErasure mounts ctech-account's eligibility question (saga protocol
// § 4.1).
//
// It is not behind ResolveTenant: the caller is ctech-account itself, which is
// not a billing credential, and the question is about a person rather than a
// tenant. The scope is the gate, and nothing but ctech-account can hold it
// (middleware.ScopeErasureEligibility).
func registerErasure(v1 fiber.Router, d Deps, auth fiber.Handler) {
	if d.Eraser == nil {
		return
	}
	v1.Get("/internal/erasure/eligibility/:sub", auth,
		middleware.RequireM2MScope(middleware.ScopeErasureEligibility),
		func(c fiber.Ctx) error {
			blockers, err := d.Eraser.Blockers(c.Context(), c.Params("sub"))
			if err != nil {
				// Never "eligible": ctech-account reads any non-2xx as a transient blocker.
				return fail(c, err)
			}
			return c.JSON(erasure.NewEligibility(blockers...))
		})
}
```

`router.go`:
- In `Deps`, after `SettlementBus settlement.Bus`, add:

```go
	// Eraser answers ctech-account's erasure eligibility question. Nil leaves
	// the route unmounted.
	Eraser *services.Eraser
```

- In `Register`, directly before `registerConsole(v1, d, h, auth)`, add `registerErasure(v1, d, auth)`.

`app/erasure.go`:
- Extend the imports with `"github.com/aws/aws-sdk-go-v2/service/dynamodb"`, `"gopkg.aoctech.app/billing/api/internal/repositories"` and `"gopkg.aoctech.app/billing/api/internal/services"`.
- Append:

```go
// portalInvoicesURL is the portal's invoice list, where a person settles what
// blocks their erasure. Derived from CHECKOUT_BASE_URL's origin, because both
// pages are the same static site (ADR 0013), and so the userdata carries
// nothing new.
func portalInvoicesURL(checkoutBaseURL string) string {
	u, err := url.Parse(checkoutBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/invoices"
}

// newEraser is the one way the API and the consumer build the Eraser, so both
// look for the same customer in the same tenant.
func newEraser(db *dynamodb.Client, cfg *config.Config) *services.Eraser {
	return services.NewEraser(
		repositories.NewCustomerRepository(db, cfg),
		repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewOrganizationRepository(db, cfg),
		cfg.PortalOrganizationID,
		portalInvoicesURL(cfg.CheckoutBaseURL),
	)
}
```

`app.go`, in the `v1.Deps` literal after `SettlementBus:        bus,`: `Eraser:               newEraser(db, cfg),`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -count=1 && make test-integration`
Expected: `ok` everywhere, and `--- PASS: TestEligibilityAnswersCtechAccountAboutOnePerson`. `internal/oauthresource`'s manifest test still passes, because the scope is not in `AllScopes`.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/scope.go internal/api/v1 internal/app tests/integration/erasure_test.go
git commit -m "feat(api): GET /v1.0/internal/erasure/eligibility/:sub for ctech-account"
```

---

### Task 8: The daily finance job leaves an erased space alone

**Files:**
- Modify: `api/internal/services/finance_recurrences.go`
- Modify: `api/internal/services/erasing.go` (`ErasedSpace`)
- Modify: `api/internal/services/finance_test.go`
- Modify: `api/internal/app/app.go` (`BuildFinanceJobs`)

**Interfaces:**
- Consumes: `(*erasure.Store).Blocked`, `(*erasure.Store).OrgErased`.
- Produces:
  - `(*FinanceJobs).WithSpaceGuard(gone func(ctx context.Context, sp space.ResolvedSpace) (bool, error)) *FinanceJobs`
  - `services.ErasedSpace(store *erasure.Store) func(context.Context, space.ResolvedSpace) (bool, error)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/services/finance_test.go`:

```go
// Review Focus 4: a work item read before the purge must not write into the
// space after it.
func TestTheJobLeavesAnErasedSpaceAlone(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10))
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10))}
	asked := 0
	jobs := NewFinanceJobs(bills, recs).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) {
		asked++
		return true, nil
	})
	today, now := day(2026, time.March, 20), time.Now()

	m := jobs.Materialise(context.Background(), true, today, now)
	a := jobs.AutoSettle(context.Background(), true, today, now)
	if m.Done != 0 || m.Skipped != 1 || bills.created != 0 || recs.marks != 0 {
		t.Fatalf("materialise = %+v, created %d, cursor moves %d; want the erased space skipped", m, bills.created, recs.marks)
	}
	if a.Done != 0 || a.Skipped != 1 || len(bills.settled) != 0 {
		t.Fatalf("auto-settle = %+v, settled %v; want the erased space skipped", a, bills.settled)
	}
	if asked != 2 {
		t.Fatalf("guard asked %d times, want once per item", asked)
	}
}

func TestAGuardErrorFailsTheItemNotTheRun(t *testing.T) {
	bills, recs := newFakeBills(), newFakeRecs(monthly("r1", 10), monthly("r2", 12))
	jobs := NewFinanceJobs(bills, recs).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) {
		return false, errors.New("dynamodb: boom")
	})
	res := jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if res.Failed != 2 || res.Done != 0 || bills.created != 0 {
		t.Fatalf("res = %+v; an unknown erasure state is a failure, never a write", res)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/services/ -run 'TestTheJobLeavesAnErasedSpaceAlone|TestAGuardErrorFailsTheItemNotTheRun'`
Expected: FAIL, `jobs.WithSpaceGuard undefined`.

- [ ] **Step 3: Implement**

`finance_recurrences.go`. Replace the `FinanceJobs` struct with:

```go
type FinanceJobs struct {
	bills billStore
	recs  recurrenceStore
	// gone reports a space whose owner the account-deletion saga has locked or
	// erased. Nil means no guard (the console's projection read).
	gone func(ctx context.Context, sp space.ResolvedSpace) (bool, error)
}
```

and add after `NewFinanceJobs`:

```go
// WithSpaceGuard skips every work item in a space gone reports, so the daily
// job never writes into a space being or already purged (saga protocol § 4.5).
// A guard error fails that item; the job carries on with the rest.
func (j *FinanceJobs) WithSpaceGuard(gone func(ctx context.Context, sp space.ResolvedSpace) (bool, error)) *FinanceJobs {
	j.gone = gone
	return j
}

// skip reports whether sp must be left alone, recording why in res.
func (j *FinanceJobs) skip(ctx context.Context, sp space.ResolvedSpace, res *JobResult, what string) bool {
	if j.gone == nil {
		return false
	}
	gone, err := j.gone(ctx, sp)
	if err != nil {
		res.fail("%s: reading the space's erasure state: %v", what, err)
		return true
	}
	if gone {
		res.Skipped++
	}
	return gone
}
```

In `Materialise`, replace

```go
		res.Examined++
		rec := d.Recurrence
```

with

```go
		res.Examined++
		rec := d.Recurrence
		if j.skip(ctx, d.Space, &res, "recurrence "+rec.ID) {
			continue
		}
```

In `AutoSettle`, replace

```go
		res.Examined++
		_, err := j.bills.Settle(
```

with

```go
		res.Examined++
		if j.skip(ctx, d.Space, &res, "bill "+d.Bill.ID) {
			continue
		}
		_, err := j.bills.Settle(
```

`erasing.go`: add `"strings"` to the imports and append:

```go
// ErasedSpace reports a space whose owner is locked or erased in the
// account-deletion saga: a personal space by its user, an organization space
// by the organization's tombstone. It is the daily job's guard.
func ErasedSpace(store *erasure.Store) func(context.Context, space.ResolvedSpace) (bool, error) {
	return func(ctx context.Context, sp space.ResolvedSpace) (bool, error) {
		if sp.Personal() {
			return store.Blocked(ctx, strings.TrimPrefix(sp.Owner(), "USER#"))
		}
		return store.OrgErased(ctx, sp.OrganizationID())
	}
}
```

`app.go`, in `BuildFinanceJobs`, replace

```go
	return services.NewFinanceJobs(
		repositories.NewBillRepository(db, cfg),
		repositories.NewRecurrenceRepository(db, cfg),
	), nil
```

with

```go
	return services.NewFinanceJobs(
		repositories.NewBillRepository(db, cfg),
		repositories.NewRecurrenceRepository(db, cfg),
	).WithSpaceGuard(services.ErasedSpace(erasure.NewStore(db, cfg.TablePrefix, 0))), nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -count=1 && make test-integration`
Expected: `ok` everywhere, including the two new tests and every existing `FinanceJobs` test (they build no guard).

- [ ] **Step 5: Commit**

```bash
git add internal/services internal/app/app.go
git commit -m "feat(api): the finance job skips spaces locked or erased by account deletion"
```

---

### Task 9: Run the consumer beside the API, with its own client

**Files:**
- Modify: `api/internal/config/config.go` (`ErasureClientID`, `ErasureClientSecret`)
- Modify: `api/internal/app/erasure.go` (`BuildErasureConsumer`, `erasureQueueName`, `erasureClientParams`, `usableErasureClient`, `erasureCredentials`)
- Modify: `api/internal/app/erasure_test.go`
- Modify: `api/cmd/server/main.go`
- Modify: `api/go.mod`, `api/go.sum`:
  - `aws-sdk-go-v2/service/sqs` becomes a direct requirement;
  - `aws-sdk-go-v2/service/ssm` is new, from the AWS SDK family billing already pins.

**Interfaces:**
- Consumes: `erasure.NewConsumer`, `erasure.NewAckClient`, `erasure.NewStore`, `oauth2client.New`, `newEraser` (Task 7), `(*Eraser).WithPurge`/`Purge` (Task 6), `repositories.NewSpacePurger` (Task 5).
- Produces:
  - `config.Config.ErasureClientID` (`ERASURE_CLIENT_ID`) and `config.Config.ErasureClientSecret` (`ERASURE_CLIENT_SECRET`)
  - `app.BuildErasureConsumer(ctx context.Context, cfg *config.Config, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)) (*erasure.Consumer, error)`
  - `app.erasureQueueName(env string) string` = `{env}-ctech-billing-erasure`
  - `app.erasureClientParams(env string) (id, secret string)` = `/ctech-billing/{env}/billing/erasure-client-{id,secret}`

  Task 10's Terraform matches both names.

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/app/erasure_test.go` and add the imports `"context"` and `"gopkg.aoctech.app/billing/api/internal/config"`:

```go
// terraform/billing/erasure.tf names the queue "${local.name}-erasure" with
// local.name = "${var.environment}-ctech-billing", and the client parameters
// "${local.ssm_prefix}/erasure-client-{id,secret}" with
// local.ssm_prefix = "/ctech-billing/${var.environment}/billing".
// Change both sides or neither.
func TestErasureNamesMatchTerraform(t *testing.T) {
	if got := erasureQueueName("prod"); got != "prod-ctech-billing-erasure" {
		t.Fatalf("erasureQueueName(prod) = %q", got)
	}
	id, secret := erasureClientParams("prod")
	if id != "/ctech-billing/prod/billing/erasure-client-id" || secret != "/ctech-billing/prod/billing/erasure-client-secret" {
		t.Fatalf("erasureClientParams(prod) = %q, %q", id, secret)
	}
}

// A laptop with real AWS credentials and a local DynamoDB must never consume
// dev's queue into a local table.
func TestNoConsumerWithoutCtechAccountOrAgainstALocalTable(t *testing.T) {
	full := config.Config{Env: "dev", AccountBaseURL: "https://a", AccountTokenURL: "https://a/t",
		ErasureClientID: "billing-erasure", ErasureClientSecret: "s"}
	for name, cfg := range map[string]config.Config{
		"no ctech-account": {Env: "dev"},
		"local dynamodb":   func() config.Config { c := full; c.DynamoDBEndpoint = "http://localhost:8124"; return c }(),
	} {
		c, err := BuildErasureConsumer(context.Background(), &cfg, nil)
		if c != nil || err != nil {
			t.Errorf("%s: consumer %v, err %v; want neither", name, c, err)
		}
	}
}

func TestAnUnsetErasureClientIsRefused(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"SET-OUT-OF-BAND", "SET-OUT-OF-BAND"}, {"billing-erasure", ""}} {
		if err := usableErasureClient(pair[0], pair[1]); err == nil {
			t.Errorf("client %q/%q accepted; a placeholder must never mint a token", pair[0], pair[1])
		}
	}
	if err := usableErasureClient("billing-erasure", "s3cret"); err != nil {
		t.Fatalf("a real client was refused: %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/ -run 'TestErasureNames|TestNoConsumer|TestAnUnsetErasureClient'`
Expected: FAIL, `undefined: erasureQueueName`, `undefined: BuildErasureConsumer`, `unknown field ErasureClientID`.

- [ ] **Step 3: Implement**

`config.go`, after `AccountClientSecret`:

```go
	// The confidential client that acks account-deletion purges to ctech-account
	// (scope internal:account:erasure-ack and nothing else). It is dedicated so
	// that the membership credential above cannot ack an erasure, and this one
	// cannot read memberships.
	//
	// Empty means "read them from SSM": /ctech-billing/{env}/billing/erasure-client-{id,secret},
	// declared by terraform/billing/erasure.tf. The consumer reads them at start
	// rather than through the userdata, which is at its 16 KiB ceiling.
	ErasureClientID     string `env:"ERASURE_CLIENT_ID"`
	ErasureClientSecret string `env:"ERASURE_CLIENT_SECRET"`
```

`app/erasure.go`:
- Replace the import block with:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/oauth2client"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)
```

- Append:

```go
// erasureService is billing's participant id in the saga: the SNS filter
// value, the consumer's target check and the ack's service field.
const erasureService = "billing"

// erasureAckScope is what ctech-account's ack route requires.
const erasureAckScope = "internal:account:erasure-ack"

// erasureQueueName is terraform/billing/erasure.tf's queue. It is derived from
// ENVIRONMENT rather than configured, because the userdata is at its 16 KiB
// ceiling (terraform/README).
func erasureQueueName(env string) string { return env + "-ctech-billing-erasure" }

// erasureClientParams are terraform/billing/erasure.tf's SecureStrings for the
// dedicated ack client.
func erasureClientParams(env string) (id, secret string) {
	prefix := "/ctech-billing/" + env + "/billing/erasure-client-"
	return prefix + "id", prefix + "secret"
}

// usableErasureClient refuses an empty pair or Terraform's placeholder.
func usableErasureClient(id, secret string) error {
	if id == "" || secret == "" || id == "SET-OUT-OF-BAND" || secret == "SET-OUT-OF-BAND" {
		return errors.New("the erasure client is not set (ERASURE_CLIENT_ID/SECRET or its SSM parameters)")
	}
	return nil
}

// erasureCredentials returns the dedicated ack client: from the environment
// when set, otherwise from its two SSM SecureStrings.
func erasureCredentials(ctx context.Context, awsConf aws.Config, cfg *config.Config) (string, string, error) {
	if cfg.ErasureClientID != "" || cfg.ErasureClientSecret != "" {
		return cfg.ErasureClientID, cfg.ErasureClientSecret, usableErasureClient(cfg.ErasureClientID, cfg.ErasureClientSecret)
	}
	idName, secretName := erasureClientParams(cfg.Env)
	out, err := ssm.NewFromConfig(awsConf).GetParameters(ctx, &ssm.GetParametersInput{
		Names:          []string{idName, secretName},
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("reading the erasure client from SSM: %w", err)
	}
	values := map[string]string{}
	for _, p := range out.Parameters {
		values[aws.ToString(p.Name)] = aws.ToString(p.Value)
	}
	id, secret := values[idName], values[secretName]
	return id, secret, usableErasureClient(id, secret)
}

// BuildErasureConsumer wires billing's side of the account-deletion saga: the
// SQS consumer that locks, purges and acks (api-commons/erasure).
//
// spaceFor is space.ForJob, passed in from cmd/server. A finance space is built
// from the saga message, which is an operator-grade decision, and ForJob is
// forbidden under internal/ (TestForJobIsNotCalledFromInternal).
//
// It returns (nil, nil) where there is nothing to consume for: no ctech-account
// URLs (a laptop, the integration tests), or a local DynamoDB. It returns an
// error, which the caller logs, for a missing queue or an unset erasure client.
func BuildErasureConsumer(ctx context.Context, cfg *config.Config, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)) (*erasure.Consumer, error) {
	if cfg.AccountBaseURL == "" || cfg.AccountTokenURL == "" || cfg.DynamoDBEndpoint != "" {
		slog.Warn("account erasure consumer not started: no ctech-account URLs, or a local DynamoDB")
		return nil, nil
	}
	awsConf, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	clientID, clientSecret, err := erasureCredentials(ctx, awsConf, cfg)
	if err != nil {
		return nil, err
	}
	queues := sqs.NewFromConfig(awsConf)
	name := erasureQueueName(cfg.Env)
	q, err := queues.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return nil, fmt.Errorf("erasure queue %s: %w", name, err)
	}
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	acks := erasure.NewAckClient(hc,
		strings.TrimSuffix(cfg.AccountBaseURL, "/")+"/v1.0/internal/erasure/ack",
		oauth2client.New(hc, newCache(cfg), cfg.AccountTokenURL, clientID, clientSecret, erasureAckScope))
	eraser := newEraser(db, cfg).WithPurge(repositories.NewSpacePurger(db, cfg), spaceFor)
	return erasure.NewConsumer(queues, aws.ToString(q.QueueUrl), erasureService,
		erasure.NewStore(db, cfg.TablePrefix, 0), eraser.Purge, acks), nil
}
```

`cmd/server/main.go`, the whole file:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // billing decides "today" in America/Sao_Paulo, on any host

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(1)
	}

	server, err := app.Build(context.Background(), cfg, time.Now)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(1)
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The account-deletion consumer runs beside the API, in both processes of
	// every instance; SQS hands each message to one of them. A failure to start
	// it is logged, never fatal: the API keeps serving, and ctech-account alarms
	// on the missing ack after 48 h.
	consumer, err := app.BuildErasureConsumer(shutdownCtx, cfg, space.ForJob)
	switch {
	case err != nil:
		slog.Error("account erasure consumer not started", "error", err)
	case consumer != nil:
		go func() { _ = consumer.Run(shutdownCtx) }()
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("billing api listening", "addr", addr, "env", cfg.Env, "version", cfg.AppVersion)

	listenErr := make(chan error, 1)
	go func() { listenErr <- server.Listen(addr) }()

	select {
	case err := <-listenErr:
		if err == nil {
			return
		}
		slog.Error("listen", "error", err)
		os.Exit(1)
	case <-shutdownCtx.Done():
		slog.Info("billing api draining")
		if err := server.ShutdownWithTimeout(15 * time.Second); err != nil {
			slog.Error("shutdown", "error", err)
			os.Exit(1)
		}
	}
}
```

Then run `go get github.com/aws/aws-sdk-go-v2/service/ssm && go mod tidy` (from `api/`). That adds `service/ssm` and promotes `github.com/aws/aws-sdk-go-v2/service/sqs` from indirect to direct.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go get github.com/aws/aws-sdk-go-v2/service/ssm && go mod tidy && go build ./... && go test ./... -race -count=1 && go test ./internal/space/ -run TestForJobIsNotCalledFromInternal -count=1`
Expected: `ok` everywhere. The ForJob guard still passes, because the only new call is in `cmd/server`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/config/config.go internal/app/erasure.go internal/app/erasure_test.go cmd/server/main.go
git commit -m "feat(api): run the account-erasure consumer beside the API, with a dedicated ack client"
```

---

### Task 10: Terraform — queue, DLQ, subscription, alarm, IAM, ack-client parameters

The state table needs nothing here: `dynamodb.tf` already creates every key of `schema.json` (Task 2), and `table_access` already covers every table in that set. Run commands from the repo root.

**Files:**
- Create: `terraform/billing/erasure.tf`

**Interfaces:**
- Consumes: `local.name`, `local.alerts_topic_arn`, `aws_iam_role.billing`, SSM `/ctech/{env}/account/erasure-topic-arn` (written by ctech-account's `iam-stack.ts`).
- Produces: queue `{env}-ctech-billing-erasure` (the name `app.erasureQueueName` derives), DLQ `{env}-ctech-billing-erasure-dlq`, the SNS subscription, alarm `{env}-ctech-billing-erasure-dlq`, SecureStrings `/ctech-billing/{env}/billing/erasure-client-{id,secret}` (the paths `app.erasureClientParams` derives).

- [ ] **Step 1: Write the file**

`terraform/billing/erasure.tf`:

```hcl
# Billing as a participant in ctech-account's account-deletion saga
# (ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md § 3, § 7).
#
# ctech-account owns the topic and publishes the ARN in SSM; billing owns its
# queue, its DLQ and its subscription. The state table is not here: it is a key
# of api/internal/repositories/schema.json, so dynamodb.tf creates it.

data "aws_ssm_parameter" "erasure_topic_arn" {
  name = "/ctech/${var.environment}/account/erasure-topic-arn"
}

resource "aws_sqs_queue" "erasure_dlq" {
  name                      = "${local.name}-erasure-dlq"
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

# The Go service derives this name from ENVIRONMENT (internal/app/erasure.go,
# erasureQueueName) and resolves the URL with GetQueueUrl, so the userdata,
# which is at its 16 KiB ceiling, carries nothing new.
#
# Visibility 900 s is well over twice the slowest purge (one read of tenant
# zero's invoices plus a handful of finance spaces). After five receives a
# message is parked in the DLQ and the alarm below pages.
resource "aws_sqs_queue" "erasure" {
  name                       = "${local.name}-erasure"
  visibility_timeout_seconds = 900
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.erasure_dlq.arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "erasure_queue" {
  statement {
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.erasure.arn]
    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [data.aws_ssm_parameter.erasure_topic_arn.value]
    }
  }
}

resource "aws_sqs_queue_policy" "erasure" {
  queue_url = aws_sqs_queue.erasure.id
  policy    = data.aws_iam_policy_document.erasure_queue.json
}

# The filter is on the `services` MESSAGE ATTRIBUTE ctech-account sets on every
# publish, not on the body: a service-scoped request for another service never
# reaches this queue. Raw delivery, so the body is the message itself
# (erasure.Decode accepts the envelope too).
resource "aws_sns_topic_subscription" "erasure" {
  topic_arn            = data.aws_ssm_parameter.erasure_topic_arn.value
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.erasure.arn
  raw_message_delivery = true
  filter_policy_scope  = "MessageAttributes"
  filter_policy        = jsonencode({ services = ["billing"] })

  depends_on = [aws_sqs_queue_policy.erasure]
}

resource "aws_cloudwatch_metric_alarm" "erasure_dlq" {
  alarm_name          = "${local.name}-erasure-dlq"
  alarm_description   = "An account-deletion message failed five times in billing. Read the app log for 'erasure:', fix, then redrive the DLQ."
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.erasure_dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [local.alerts_topic_arn]
}

data "aws_iam_policy_document" "erasure_consume" {
  statement {
    effect = "Allow"
    actions = [
      "sqs:GetQueueUrl",
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:ChangeMessageVisibility",
      "sqs:GetQueueAttributes",
    ]
    resources = [aws_sqs_queue.erasure.arn]
  }
}

resource "aws_iam_role_policy" "erasure_consume" {
  name   = "${local.name}-erasure-queue"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.erasure_consume.json
}

# The dedicated confidential client that acks purges to ctech-account. Its only
# grant is internal:account:erasure-ack, kept apart from the membership client.
# The service reads both values at start (internal/app/erasure.go,
# erasureClientParams), not through the userdata.
#
# Same discipline as the collection secrets: Terraform creates the parameters
# and never their values. The operator creates the client in ctech-account and
# runs `aws ssm put-parameter --overwrite` for each value. Until then the
# consumer logs that the client is unset and does not start. The role already
# reads and decrypts everything under local.ssm_prefix (iam.tf, ssm_read).
resource "aws_ssm_parameter" "erasure_client" {
  for_each = {
    id     = "${local.ssm_prefix}/erasure-client-id"
    secret = "${local.ssm_prefix}/erasure-client-secret"
  }

  name  = each.value
  type  = "SecureString"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}
```

- [ ] **Step 2: Validate**

Run: `terraform -chdir=terraform/billing fmt -check && terraform -chdir=terraform/billing init -backend=false -input=false >/dev/null && terraform -chdir=terraform/billing validate`
Expected: no `fmt` output, then `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/billing/erasure.tf
git commit -m "feat(infra): erasure queue, DLQ, filtered subscription, alarm and ack-client parameters for billing"
```

---

### Task 11: Documentation (repo policy: every change documented)

**Files:**
- Modify: `README.md`, `ARCHITECTURE.md` § 9, `PLAN.md`, `docs/adr/0009-retention-and-ttl.md`, `docs/adr/0026-finance-retention-by-purge.md`, `docs/specs/2026-10-07-finance-erp-design.md` § 8, `terraform/README.md`, `.github/workflows/README.md`

- [ ] **Step 1: `README.md`** — insert this section directly before `### Telling other services what happened`:

```markdown
### Account deletion (LGPD)

Billing is a participant in ctech-account's erasure saga
(ctech-account `docs/specs/2026-10-06-account-deletion-saga-protocol.md`; plan
[`docs/plans/2026-10-07-account-deletion-participant.md`](docs/plans/2026-10-07-account-deletion-participant.md)).

| Piece | Where |
|---|---|
| Eligibility: `GET /v1.0/internal/erasure/eligibility/:sub`, scope `internal:billing:erasure-eligibility` (minted by ctech-account only; not in the scope manifest) | `internal/api/v1/erasure.go` |
| Blockers: `billing.invoice_open` (a live tenant-zero invoice still owed, with `count`, `overdue` and `amount_cents`, and the portal's `/invoices` as `action_url`), `billing.invoice_uncollectible` (a written-off invoice still owed, with `count` and `amount_cents` and no `action_url`, because support records that payment), `billing.tenant_owner` (the person owns a billing tenant) | `services.Eraser.Blockers` |
| Lock: every session token of a locked or erased `sub` gets 403 `/problems/account-locked`, on every route | `middleware.RefuseLocked`, inside `Verifier.Middleware` |
| Token cut-off: the jwtverify revocation list, read from Valkey **DB 0** (`VALKEY_URL` without its DB path) | `internal/app/erasure.go` |
| Purge: re-checks the blockers; cancels the person's tenant-zero subscriptions now, clears their metadata, anonymizes the customer in place; deletes their personal finance spaces (live and test) and those of every organization in `organizations[]` | `services.Eraser.Purge`, `repositories.SpacePurger` |
| Kept: invoices, lines, payment attempts, credit notes, invoice PDFs, the canceled subscriptions, audit rows (except a personal space's finance audit) | ADR 0009, ADR 0026 |
| Consumer: SQS `{env}-ctech-billing-erasure` (+ `-dlq`), run in the API process, acks to `{ACCOUNT_BASE_URL}/v1.0/internal/erasure/ack` with a **dedicated** client holding only `internal:account:erasure-ack` (`ERASURE_CLIENT_ID`/`SECRET`, or the SecureStrings `/ctech-billing/{env}/billing/erasure-client-{id,secret}`) | `app.BuildErasureConsumer`, `terraform/billing/erasure.tf` |
| State and tombstones: `{env}_billing_erasure_state` | `api-commons/erasure.Store` |

The daily finance job skips any space whose owner is locked or erased (`services.ErasedSpace`).
No payment gateway is involved: billing stores no card, PIX key or gateway customer, and the PIX
rail is ctech-wallet's.

**After restoring any billing table from PITR**, replay `user.erase` for every request purged after
the restore point (ctech-account README, "Account deletion" → backup-restore runbook). The purge is
idempotent.
```

- [ ] **Step 2: `ARCHITECTURE.md` § 9** — append this bullet after the "Minimum PII, not zero PII." bullet:

```markdown
- **Account deletion is a saga billing takes part in, not a delete button.** ctech-account locks the
  person (a 403 on every session route here, plus the jwtverify revocation list), asks billing for
  blockers, and sends `user.erase`. Billing cancels the person's subscriptions, anonymizes the customer
  in place, deletes their finance spaces, and acks. The invoice aggregate stays: it is a document. The
  details and the rulings are in [`docs/plans/2026-10-07-account-deletion-participant.md`](docs/plans/2026-10-07-account-deletion-participant.md).
```

- [ ] **Step 3: `PLAN.md`**:
- Replace the line `Cross-repo: ctech-account's account-deletion spec must emit the personal-space purge trigger` and the words `(ADR 0026).` that follow it on the next line with:

```markdown
Cross-repo: the personal-space purge trigger exists. Billing consumes ctech-account's `user.erase`
(see "Account deletion" below).
```

- Append at the end of Phase 6:

```markdown
## Account deletion (LGPD) — billing as a saga participant
Plan: [`docs/plans/2026-10-07-account-deletion-participant.md`](docs/plans/2026-10-07-account-deletion-participant.md).
- [x] api-commons v1.13.1; `erasure_state` table; lock on every session route; revocation list (DB 0)
- [x] Eligibility route and blockers (`billing.invoice_open`, `billing.invoice_uncollectible`, `billing.tenant_owner`)
- [x] Purge: subscriptions canceled, customer anonymized, personal and erased-organization finance
      spaces deleted (`SpacePurger`, every table classified by a test); the finance job skips erased spaces
- [x] Consumer in the API process; queue, DLQ, filtered subscription, alarm (`terraform/billing/erasure.tf`)
- [ ] Operator:
  - create billing's dedicated erasure client in ctech-account with only `internal:account:erasure-ack`;
  - write its id and secret to `/ctech-billing/{env}/billing/erasure-client-{id,secret}` (`aws ssm put-parameter --overwrite`);
  - check in dev that ctech-account's minted eligibility token has `iss` = billing's `CTECH_ISSUER_URL` and `aud` = `SERVICE_AUDIENCE`;
  - and add billing to
      ctech-account's `ERASURE_PARTICIPANTS`, **after** the legal validation of the data inventory
- [ ] Later: organization customers (`CUSTOMER_ORG#`, ADR 0025 amendment) once they exist; service-scoped
      unlink (`Store.Clear` on re-consent) once ctech-account builds it
```

- [ ] **Step 4: ADRs**
- `docs/adr/0026-finance-retention-by-purge.md`, append:

```markdown
## Amendment, 2026-10-07 — implemented

The trigger is ctech-account's `user.erase`. `repositories.SpacePurger.PurgeSpace` deletes every row
under the space key `S` in `ledger_accounts`, `ledger_transactions`, `bills`, `recurrences` and
`idempotency`, plus every `S#ACCOUNT#{id}` entry partition, plus, for a personal space only, the
`audit` rows under `S`. It runs for `USER#{sub}#live|test` and for every organization in the
message's `organizations[]` (organizations erased with their only member). Entry partitions go
first, so a run that dies anywhere is finished by the next. `TestEveryTableIsClassifiedForThePurge`
is the "a test lists the tables" consequence: every table in `schema.json` is either purged under the
space or listed with the reason it is not.
```

- `docs/adr/0009-retention-and-ttl.md`, append:

```markdown
## Amendment, 2026-10-07 — the soft-delete has a caller

`CustomerRepository.Anonymize` is now called by the account-deletion saga (`services.Eraser.Purge`)
for the tenant-zero customer of a deleted CTech account. The customer's subscriptions are canceled and
their metadata cleared; the invoice aggregate is untouched.
```

- [ ] **Step 5: `docs/specs/2026-10-07-finance-erp-design.md` § 8** — replace the bullet beginning `**Personal space** data is purged when the CTech account is deleted.` with:

```markdown
- **Personal space** data is purged when the CTech account is deleted: ctech-account's `user.erase`,
  consumed by billing (`repositories.SpacePurger`, ADR 0026 amendment).
```

- [ ] **Step 6: `terraform/README.md`** — append:

```markdown
## Account-deletion queue

`billing/erasure.tf` subscribes `{env}-ctech-billing-erasure` (+ `-dlq`) to ctech-account's
`{env}-account-user-erasure` topic. The ARN is read from `/ctech/{env}/account/erasure-topic-arn`, so a
plan fails until ctech-account's stack has created it. The subscription filters on the `services`
message attribute (`["billing"]`) with raw delivery. The service derives the queue name from
`ENVIRONMENT`, and the userdata gained nothing. An alarm on the DLQ pages through the alerts topic.
The state table is `schema.json`'s `erasure_state`.
```

- [ ] **Step 7: `.github/workflows/README.md`** — add these rows to the "Prerequisites this pipeline does not create" table:

```markdown
| `/ctech/{env}/account/erasure-topic-arn` (the account-deletion topic) | ctech-account (CDK) | `terraform/billing/erasure.tf` |
| A dedicated confidential client in ctech-account holding only `internal:account:erasure-ack`, its id and secret written to `/ctech-billing/{env}/billing/erasure-client-{id,secret}` (Terraform creates the placeholders), and a `billing` entry in ctech-account's `ERASURE_PARTICIPANTS` (`url` = billing's internal base URL + `/v1.0`, `audience` = billing's `SERVICE_AUDIENCE`, `client_id` = that client). **Deployment check:** the eligibility token ctech-account mints has `iss` = billing's `CTECH_ISSUER_URL` | ctech-account, operator, after legal validation | account deletion |
```

- [ ] **Step 8: Commit**

```bash
git add README.md ARCHITECTURE.md PLAN.md docs terraform/README.md .github/workflows/README.md
git commit -m "docs: billing as an account-deletion saga participant"
```

---

## Cross-project impact

- **ctech-account:**
  - Adds billing to `ERASURE_PARTICIPANTS`:
    - `service`: `billing`
    - `url`: `https://billing[-env].internal.aoctech.app/v1.0`, with the scheme account uses for its other internal base URLs
    - `audience`: billing's `SERVICE_AUDIENCE` (`https://billing[-env].aoctech.app`)
    - `client_id`: billing's **dedicated erasure client** (R16), created for this purpose
  - Grants that client `internal:account:erasure-ack` and nothing else. The operator writes its id and secret to billing's SSM placeholders.
  - Its minted eligibility token's `iss` must equal billing's `CTECH_ISSUER_URL` (`/ctech-account/{env}/app-url`), the check its phase-3 plan already asks for.
  - Its UI translates `billing.invoice_open`, `billing.invoice_uncollectible` and `billing.tenant_owner`.
  - The data inventory § 6 / § 8 should record:
    - no payment gateway in billing;
    - the table list (`{env}_billing_*`, `schema.json`);
    - the three blocker codes;
    - no subscription or dispute blocker (R3).
  - Gated on the legal validation of the matrix.
- **ctech-go-common:** none in code. Its `CLAUDE.md` consumer list should say `ctech-billing/api (v1.13.1)`.
- **ctech-dfe:** receives `subscription.canceled` webhooks for erased users through the existing routing (ADR 0016). No change.
- **ctech-wallet:** none. The PIX rail and Asaas are wallet's participant work.
- **ctech-cdk / ctech-lbalancer:** none. The topic exists, and the route already serves `/v1.0/internal/*`.
