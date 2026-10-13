# Account Deletion — Billing as a Saga Participant Implementation Plan

> **Rebaselined 2026-10-13.** The first version (2026-10-07) was written against billing at #43; `main` is at #122. This text
> replaces it, folding in the review `ctech-account/docs/2026-10-13-account-deletion-plan-review.md` and the decisions taken that day.
> Where the review says "billing", this file is the answer.
>
> **Decisions (2026-10-13):**
> 1. A sole-member workspace that has a billing (merchant) tenant **blocks** the deletion with `billing.merchant_tenant`.
> 2. Audit rows of an erased workspace are **deleted**, whatever its kind.
> 3. The `sub` left in audit actor fields and in the ledger of a surviving shared space is **retained as an opaque id**.
> 4. `usage` level rows (`LEVEL#`, `LEVEL_LATEST#`) of `USER_{sub}` and `ORG_{id}` are **deleted**.
> 5. `user.erase` carries `workspaces: [{id, kind}]` (api-commons ≥ v1.19.0, `Message.WorkspaceIDs()` / `KindOf`).

**Goal:** Make ctech-billing a participant in ctech-account's LGPD erasure saga. It answers the eligibility question, refuses a locked person, cancels the CTech subscriptions of the person **and of every workspace erased with them**, and anonymizes their tenant-zero customers while keeping the fiscal documents. It purges their personal finance spaces and the finance spaces (every table) of the erased workspaces, disconnects their bank connections, and acks the purge. A workspace that has a merchant tenant is never erased silently: it blocks.

**Architecture:**
- The participant side comes from `api-commons/erasure` (≥ v1.19.0): the `Store` over `{prefix}_erasure_state`, the SQS `Consumer` and the `AckClient`. Billing writes:
  - `services.Eraser`: `Blockers` for the eligibility route and the purge re-check, and `Purge` as the consumer's `PurgeFunc`.
  - `repositories.SpacePurger`: deletes every row of one finance space across **all** space-keyed tables, including the per-card and per-import partitions and the BYOK connection rows. Idempotent and resumable.
  - `repositories.LevelRepository.EraseRef`: deletes the level rows of one `customer_ref`.
  - The lock: one check in `middleware.Verifier.Middleware`, which every route reached with a session token goes through.
- The consumer runs inside the API process, wired from `cmd/server`. That is where `space.ForJob` may be called.
- Every asynchronous path that can write into a space (the daily finance job, the card close, the BYOK sweeps and webhooks, the level reporter) drops work for a locked or erased owner.
- Infra is Terraform: the state table comes from `schema.json`; the queue, the DLQ, the subscription, the alarm and the IAM go in a new `terraform/billing/erasure.tf`.
- The userdata gets no new bytes. The queue URL, the ack URL and the revocation Valkey URL are all derived in Go.

**Tech Stack:** Go 1.27 (module `gopkg.aoctech.app/billing/api`), Fiber v3, DynamoDB through `api-commons/dynamo`, SQS (`aws-sdk-go-v2/service/sqs`), Terraform `hashicorp/aws ~> 6.60`, standard `testing` only.

**Spec:**
- ctech-account `docs/specs/2026-10-06-account-deletion-overview.md` (§ 5, § 11 D1–D14), `…-data-inventory.md` § 2, § 6, § 8, `…-saga-protocol.md` § 3, § 4, § 5, § 7, § 9, and the review above.
- Wire contract: ctech-account `docs/plans/2026-10-07-account-deletion-phase3-participants.md` (Global Constraints, R10, Task 3 for the `workspaces` query parameter).
- This repo: [`docs/specs/2026-10-07-finance-erp-design.md`](../specs/2026-10-07-finance-erp-design.md) § 2, § 4, § 8; ADR [0009](../adr/0009-retention-and-ttl.md), [0025](../adr/0025-spaces-personal-and-organization.md), [0026](../adr/0026-finance-retention-by-purge.md), [0027](../adr/0027-personal-workspaces-in-account.md), [0028](../adr/0028-console-tenant-from-membership.md), [0029](../adr/0029-byok-phase-1.md); [`docs/specs/2026-10-09-shared-spaces-design.md`](../specs/2026-10-09-shared-spaces-design.md) § 6; [`docs/specs/2026-10-10-plans-design.md`](../specs/2026-10-10-plans-design.md) § 6.

**Out of scope:**
- `service`-scoped unlink: ctech-account has not built it.
- `Store.Clear` on re-consent: it belongs to service-scoped unlink.
- **Deleting a merchant tenant** (its credentials, webhook endpoints, customers, invoices, BYOK collection). It is a feature with its own deletion flow. Until it exists the tenant blocks (R3). When it ships it plugs into `Eraser.Purge`.
- The ctech-account side, except the contract it must honor (eligibility `workspaces` parameter, `workspaces[]` on the message).

## Rulings (each with its cost if wrong)

- **R1 No payment-gateway erasure.** Billing never talks to a payment rail and never stores card data of a payment instrument, PIX keys of a payer or gateway customers (ARCHITECTURE § 2, § 9). The `cards` table is the person's own **expense-tracking** cards (brand, last digits), not a payment rail. It asks ctech-wallet to collect a PIX charge. *Cost if wrong:* a future card rail adds one step to `Eraser.Purge`.
- **R2 No NFS-e here.** What stays is the invoice aggregate: invoice, lines, payment attempts, checkout sessions, credit notes, the S3 PDF, the audit rows and the canceled subscription. *Cost if wrong:* none; the retained set only shrinks if legal says so.
- **R3 Four blockers**, live mode only (R15):
  - `billing.invoice_open`: a live tenant-zero invoice that is `OPEN` with an amount due, of the person **or of a customer of an erased workspace**. Overdue is a count in `detail`. `action_url` is the portal's invoice list.
  - `billing.invoice_uncollectible`: same scope, written off (`UNCOLLECTIBLE`) with an amount due. A written-off debt still blocks (user decision 2026-10-08). It has no `action_url`: the portal cannot collect it and support records the payment.
  - `billing.merchant_tenant` (decision 2026-10-13): a workspace being erased has a billing tenant in either mode (`OrganizationRepository.GetByAccountOrganization`). `detail.workspace_ids` lists them. It replaces `billing.tenant_owner`, whose premise (the console's tenant is the person's `owner_user_id`) ended with ADR 0028.
  - Not blockers: *subscriptions* (every live status has an immediate `CANCELED` edge under `CauseCustomer`; the purge cancels them, crediting nothing for the unused period), and *payment in dispute* (billing has no dispute state; a PIX MED is wallet's).

  *Cost if wrong:* a person whose only member workspace ever opened the billing console (even a test tenant) cannot delete the account until support removes the tenant by hand. This is the accepted price of not hiding a tenant deletion inside the purge.
- **R4 Tenant zero is erased; other tenants are untouched, except the tenant of an erased workspace, which blocks (R3).** A customer in a merchant's tenant that carries a `user_id` is that merchant's record, and the merchant is the controller.
- **R5 Anonymize in place, keep the pointers.** `CustomerRepository.Anonymize` is the ADR 0009 path. `CUSTOMER_USER#{sub}` and `CUSTOMER_ORG#{id}` pointers and `user_id` stay: the ids are opaque, and the pointer is what makes the portal answer "conta encerrada". *Cost if wrong:* service-scoped re-consent must clear them, which is that plan's job.
- **R6 The lock is one choke point:** `Verifier.Middleware`, for every session token (non-empty `sid`). It refuses every method; a failed state lookup is a 500 (fail closed). Service tokens, the public checkout, wallet's webhook and the BYOK webhook receivers are not checked: inbound money is accepted (saga § 4.2). *Cost if wrong:* one consistent `GetItem` per user request.
- **R7 No `VerifyClaimsStrict` route.** Billing moves no money out. *Cost if wrong:* while Valkey is down, a revoked token can still pay a bill for up to 20 min.
- **R8 The eligibility scope `internal:billing:erasure-eligibility` is not in `AllScopes` or `scope-manifest.json`.** No client can be granted it; ctech-account mints it for itself. The route is not behind `ResolveTenant`.
- **R9 Zero new userdata bytes** (AL2023 is near its ceiling). Derive the queue name, ack URL, revocation Valkey URL and portal invoices URL in Go; read the erasure client from SSM at start.
- **R10 The consumer runs in the API process.** It starts only when ctech-account's URLs are configured and DynamoDB is not local; a missing queue or a placeholder client is logged and the API keeps serving.
- **R11 The purge is a new `SpacePurger`, not a `LedgerRepository` method.** `TestLedgerRepositoryHasNoEditPath` keeps meaning "no edit path". The space is built with `space.ForJob`, injected from `cmd/server`.
- **R12 What the purge removes, per erased owner** (`USER#{sub}` and every id of `m.WorkspaceIDs()`), in both modes:
  - every row under the space key `S` in `ledger_accounts`, `ledger_transactions`, `bills`, `recurrences`, `cards`, `imports`, `byok`, `idempotency` **and `audit`** (decision 2: audit for every workspace, personal or organization, because the whole workspace is gone);
  - every `S#ACCOUNT#{id}` entry partition, every `S#CARD#{id}` partition, every `S#IMPORT#{id}` partition, and the `HOOK#{id}` rows the connections name;
  - the level rows of `USER_{sub}` and `ORG_{id}` (R21);
  - the customers, subscriptions and applications of R5, R19.
- **R13 Tombstones never expire** (`erasedTTL` 0).
- **R14 Valkey: no per-user deletion.** Billing's DB 3 holds the JWKS, service tokens and 60 s membership cache entries.
- **R15 Blockers are live-mode only.** Test money is not owed. The purge still covers both modes.
- **R16 The acks use a dedicated confidential client** (user decision 2026-10-08), not billing's membership client. Config: `ERASURE_CLIENT_ID`/`ERASURE_CLIENT_SECRET`, falling back to the SecureStrings `/ctech-billing/{env}/billing/erasure-client-{id,secret}`. The operator creates the client in ctech-account, grants it `internal:account:erasure-ack`, and writes both values with `aws ssm put-parameter --overwrite`. ctech-account matches acks on `azp`.
- **R17 Queue visibility timeout 900 s, `maxReceiveCount` 5, DLQ alarm > 0.**
- **R18 The `sub` stays opaque where a surviving space keeps it** (decision 3): the `actor` of audit rows and ledger rows of a **surviving** shared space, `applied_by`, `connected_by`, `authorized_by` of surviving organizations. The `sub` is already the key the tombstone keeps; the ledger is immutable. *Cost if wrong:* a later decision to anonymize needs a rewrite of history in surviving spaces.
- **R19 Organization customers.** For each erased workspace the tenant-zero `ORG_{id}` customer (pointer `CUSTOMER_ORG#{id}`, `CustomerRepository.GetByOrganization`) gets the same treatment as the person's: subscriptions canceled now (this includes the DF-e organization subscription), metadata cleared, customer anonymized (D12: an MEI's CNPJ and name are personal data), invoices kept. Open or written-off invoices of that customer block like the person's. *Cost if wrong:* arrears metered usage of the current period (for example DF-e companies) is not billed after the immediate cancellation.
- **R20 The eligibility call carries the workspaces.** ctech-account passes `?workspaces=id1,id2` (the workspaces where the person is the only member) on `GET /v1.0/internal/erasure/eligibility/:sub`; billing cannot compute it (ADR 0023: membership lives in ctech-account). Absent parameter: only the person is checked. The purge re-check uses the message's `WorkspaceIDs()`, which is authoritative. *Cost if wrong:* a `merchant_tenant` block is discovered at LOCKED instead of at request; the purge re-check still acks `blocked`, never a half purge.
- **R21 Level rows (`usage` table).** Meter names are free-form (`ValidMeter`) and part of the partition key, so rows cannot be listed by `customer_ref`. The purge derives the candidate meters from tenant zero's own catalogue (`CatalogRepository.ListPrices`, prices with a `Meter`) in both modes and deletes `LEVEL#`, `LEVEL_LATEST#` partitions for `USER_{sub}` and `ORG_{id}`. `LEVEL_KEY#{key}` markers hold only a hash and expire in 13 months; they stay. *Cost if wrong:* a meter that is no longer in the catalogue leaves a latest-level row of an opaque ref until its history TTL; the reporter test (Task 8b) stops new ones.
- **R22 Customer reads go through the indexes, behind a backfill gate.** `InvoiceRepository.ListByCustomer` (`customer-index`) and `SubscriptionRepository.ListByCustomer` (`lookup-index`) replace the old whole-partition scans. Both indexes are sparse and populated at create; rows written before the index need `cmd/backfill-invoice-lookup` and `cmd/backfill-subscription-lookup`. An unfilled index hides an open invoice, so each environment must be confirmed backfilled before billing joins `ERASURE_PARTICIPANTS` (Task 11 operator list). *Cost if wrong:* a missed open invoice lets a debtor delete the account.
- **R23 BYOK.** Connections of an erased workspace are disconnected through the existing `ByokConnections.Disconnect` (drops cached tokens, deletes the connection) and the purger deletes what is left (`SEALED#`, `HOOK#`). Disconnect does not unregister the webhook at the bank; the receiver drops events for an unknown hook or an erased workspace. Unregistering at the bank is a BYOK decision, recorded as a limit. A BYOK *merchant* connection lives with a tenant, which blocks (R3). *Cost if wrong:* the bank keeps calling a dead webhook until its own retry policy gives up.
- **R24 `ForJob` cannot know a workspace's kind and reports `organization`.** That is fine now: nothing in the purge depends on the kind (decision 2). The message's `KindOf` is for logs and counts only.

## Global Constraints

- **Repo conventions:**
  - Code lives under `api/`, and every Go command runs from `api/`.
  - Branch `feat/account-deletion-participant` from `main`. Conventional Commits, **no trailer** (`Co-Authored-By` or any attribution), authored by the repo's configured user.
  - `gofmt -l .` prints nothing, `go vet ./...` and `go vet -tags integration ./tests/...` are clean, and `go test ./... -race` passes before every commit.
  - Integration tests need `docker compose -f docker-compose.test.yml up -d`, then `make test-integration`.
- **Versions:** `gopkg.aoctech.app/api-commons` at **v1.19.0 or later** (the first tag with `Message.Workspaces`). Never pin lower than the current `go.mod`.
- **Wire contract:**
  - Participant id `billing`.
  - Eligibility: `GET /v1.0/internal/erasure/eligibility/:sub[?workspaces=a,b]`, scope `internal:billing:erasure-eligibility`. ctech-account's `ERASURE_PARTICIPANTS` `url` for billing ends in `/v1.0`.
  - Ack: `POST {ACCOUNT_BASE_URL}/v1.0/internal/erasure/ack`, scope `internal:account:erasure-ack`, body `erasure.Ack`.
  - SNS topic: `{env}-account-user-erasure`, ARN in SSM `/ctech/{env}/account/erasure-topic-arn`.
  - Subscription: `FilterPolicy {"services":["billing"]}`, `filter_policy_scope = "MessageAttributes"`, raw delivery on.
- **State table:** `{prefix}_erasure_state`, partition key `pk` (S), TTL attribute `ttl`, declared in `schema.json`.
- **Blocker codes** are stable and translated by the account UI: `billing.invoice_open`, `billing.invoice_uncollectible`, `billing.merchant_tenant`.
- **Deployment check (user decision 2026-10-08):** ctech-account mints the eligibility token with `iss` = its issuer URL, equal to billing's `CTECH_ISSUER_URL`, and `aud` equal to billing's `SERVICE_AUDIENCE`. Verify both in dev before billing is added to `ERASURE_PARTICIPANTS`.
- **DynamoDB:** no `Scan` anywhere (the IAM policy denies it). Batch deletes are at most 25 keys per `BatchWriteItem`.
- **Retained:** invoices and everything under `INVOICE#`, canceled subscriptions, audit rows of surviving spaces and tenants, invoice PDFs, tenant-zero customers (anonymized). **Erased:** personal finance spaces and the finance spaces of erased workspaces, with their cards, imports and BYOK connections; level rows of the erased refs.
- **Every new table needs a purge classification** (Task 5's test). This is repository policy; see the README rule "every new feature ships its erasure path".
- **Rollout gate:** legal validation of the inventory comes before ctech-account adds `billing` to `ERASURE_PARTICIPANTS` in prod. The code ships dark until then.

## Review Focus

1. **A purge killed halfway, or run twice.** It ends in the same state; entry, card and import partitions are still found after their parents are gone. Tests: Task 5 `TestPurgeSpaceErasesEveryTableAndIsSafeToRepeat`, `TestPurgeFindsPartitionsAfterTheirParentsAreGone`.
2. **An invoice issued during the grace period.** The purge's own re-check acks `blocked` and changes nothing. Test: Task 6 `TestPurgeIsRefusedWhileAnInvoiceIsOpenAndChangesNothing`.
3. **A locked person holding a token that has not expired.** Refused on every session route; service tokens keep working. Test: Task 3 `TestALockedPersonIsRefusedWithATokenThatIsStillValid`.
4. **Async work for a space being purged.** The finance job, the card close, the BYOK sweeps and the level reporter skip it instead of resurrecting rows. Tests: Task 8 and Task 8b.
5. **A surviving organization, another customer's open invoice, a surviving space's audit rows, a surviving tenant.** All untouched. Test: Task 6 `TestPurgeErasesThePersonAndKeepsTheDocuments`.
6. **An erased workspace with a merchant tenant, or with an unpaid organization invoice.** Blocks; nothing is touched. Tests: Task 6 `TestAWorkspaceWithAMerchantTenantBlocksErasure`, `TestAnOrganizationInvoiceBlocksErasure`.
7. **A sparse index not backfilled.** The gate (Task 11) is the control; Task 4's test documents what a missing key hides.

---

### Task 1: api-commons ≥ v1.19.0, gated by the whole suite

Billing is on `v1.18.0` (the `erasure` package and `jwtverify.WithRevocation` are already importable). This task moves to the first tag that carries `erasure.Message.Workspaces` (`WorkspaceIDs`, `KindOf`). **Prerequisite:** ctech-go-common has tagged `v1.19.0` (or later). It changes **no** call site; if a test fails, stop and report it.

**Files:**
- Modify: `api/go.mod`, `api/go.sum`

**Interfaces:**
- Produces: `erasure.Message.WorkspaceIDs()` and `erasure.Message.KindOf(id)`.

- [ ] **Step 1: Bump**

Run: `go get gopkg.aoctech.app/api-commons@v1.19.0 && go mod tidy`
Expected: `grep api-commons go.mod` prints `gopkg.aoctech.app/api-commons v1.19.0` (or the later tag you used). It must never print a version lower than before.

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./... && go vet -tags integration ./tests/...`
Expected: no output.

- [ ] **Step 3: Unit and integration suites**

Run: `go test ./... -race -count=1 && docker compose -f docker-compose.test.yml up -d && make test-integration`
Expected: every package `ok`, then `PASS`.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(api): api-commons v1.19.0 (erasure workspaces)"
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
- Consumes: `erasure.NewStore(db, prefix, 0)`, `(*erasure.Store).Blocked(ctx, sub) (bool, error)` (api-commons ≥ v1.13.0); `jwtverify.(*Verifier).WithRevocation(cache.Backend)`.
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

### Task 4: Every invoice and subscription of one customer, through the indexes; clearing subscription metadata

`InvoiceRepository.ListByCustomer` (`customer-index`) and `SubscriptionRepository.ListByCustomer` (`lookup-index`) already exist (#88, #92). With `limit <= 0` they follow continuation keys to the end, which is what deletion needs. This task adds the one missing write, `ClearMetadata`, and tests the two reads for erasure's needs, including what an unfilled index hides (R22).

**Files:**
- Modify: `api/internal/repositories/subscriptions.go` (`ClearMetadata`)
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `(*InvoiceRepository).ListByCustomer(ctx, organizationID, livemode, customerID, 0)`, `(*SubscriptionRepository).ListByCustomer(ctx, organizationID, livemode, customerID, 0)`.
- Produces:
  - `(*SubscriptionRepository).ClearMetadata(ctx, s *billing.Subscription, now time.Time) error`
  - the integration helper `newSubscriptionWithMetadata(t, org, customerID, priceID string) *billing.Subscription`

- [ ] **Step 1: Write the failing test**

Append to `tests/integration/erasure_test.go` (imports: `billing`, `brcal`, `repositories`, `id`):

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

func TestListByCustomerReadsOneCustomerOnlyAndMetadataClears(t *testing.T) {
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

	invoices, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices = %d, %v; want this customer's 2 and none of the other's", len(invoices), err)
	}
	for _, inv := range invoices {
		if inv.CustomerID != e.customer.ID {
			t.Fatalf("invoice %s belongs to %s", inv.ID, inv.CustomerID)
		}
	}

	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	got, err := subs.ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
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

// R22: the indexes are sparse. A row written before customer-index existed and
// not backfilled is invisible to ListByCustomer, so an open invoice on it would
// not block an erasure. This test pins that fact; the control is the backfill
// gate in Task 11, not code.
func TestAnInvoiceWithoutItsCustomerKeyIsInvisibleToTheIndex(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	removeAttr(t, "invoices", repositories.TenantPK(e.org.ID, true), repositories.InvoiceSK(inv.ID), "customer_pk")

	got, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("invoices = %d, %v; a row without customer_pk must not appear (that is what the backfill fixes)", len(got), err)
	}
}
```

`removeAttr(t, logicalTable, pk, sk, attr)` is a small helper in the same file: an `UpdateItem ... REMOVE #a` on `repositories.TableName(testCfg, logicalTable)`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go vet -tags integration ./tests/...`
Expected: FAIL, `ClearMetadata undefined`.

- [ ] **Step 3: Implement**

`subscriptions.go`, after `ListByCustomer`:

```go
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
Expected: `ok`, and both new tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repositories tests/integration/erasure_test.go
git commit -m "feat(api): clear a subscription's metadata; pin what the customer indexes hide"
```

---

### Task 5: `SpacePurger` — every row of one finance space, resumable

**Files:**
- Modify: `api/internal/repositories/ledger.go`, `api/internal/repositories/recurrences.go` (`queryPrefix` becomes a package function)
- Create: `api/internal/repositories/finance_purge.go`
- Create: `api/internal/repositories/finance_purge_test.go`
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `queryPrefix`, `DecodeItems[txItem]`, `LedgerEntryPK(sp, account)`, `CardPK(sp, id)`, `ImportPK(sp, id)`, `hookPK(id)`, `CardSK`, `ImportSK`.
- Produces:
  - `repositories.NewSpacePurger(db *dynamodb.Client, cfg *config.Config) *SpacePurger`
  - `(*SpacePurger).PurgeSpace(ctx context.Context, sp space.ResolvedSpace) (map[string]int, error)`, which returns the rows deleted per logical table
  - the integration helpers `seedFinance(t, sp)`, `rowsUnder(t, logical, pk) int` and `assertSpaceGone(t, sp)`

- [ ] **Step 1: Make `queryPrefix` a package function**

```bash
sed -i 's/^func (r \*LedgerRepository) queryPrefix(/func queryPrefix(/; s/r\.queryPrefix(/queryPrefix(/g' internal/repositories/ledger.go
sed -i 's/r\.ledger\.queryPrefix(/queryPrefix(/' internal/repositories/recurrences.go internal/repositories/cards.go internal/repositories/imports.go
go build ./... 
```
Expected: builds. (`cards.go` and `imports.go` call it as `r.ledger.queryPrefix(ctx, r.cards, …)`; fix any other caller the compiler names the same way.)

- [ ] **Step 2: Write the failing unit tests**

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
// A table whose rows live in partitions other than S (cards: S#CARD#{id},
// imports: S#IMPORT#{id}, byok: HOOK#{id}) also needs its own step in
// PurgeSpace, as the S#ACCOUNT# entries have.
var notSpaceScoped = map[string]string{
	TableOrganizations: "merchant tenants (ADR 0007) and applications; a workspace with a tenant blocks erasure (billing.merchant_tenant), so none is purged until a tenant deletion flow exists",
	TableCredentials:   "belongs to a merchant tenant, which blocks erasure",
	TableCustomers:     "tenant-zero customers are anonymized in place by services.Eraser (ADR 0009); merchants' customers are their controller's",
	TableProducts:      "catalogue, no person",
	TablePrices:        "catalogue, no person",
	TableSubscriptions: "canceled by services.Eraser and retained: it explains invoices (ADR 0009)",
	TableInvoices:      "fiscal documents, retained (ADR 0009)",
	TableUsage:         "level rows of USER_/ORG_ refs are deleted by LevelRepository.EraseRef; raw usage records expire in 24 months and carry no person",
	TableWebhooks:      "tenant endpoints belong to a merchant tenant (blocks erasure); events and deliveries expire in 90 days",
	TableErasureState:  "the saga's own state and tombstones",
	TableAudit:         "purged under S by PurgeSpace, outside financeTables; a surviving space's audit keeps ADR 0009's five-year TTL",
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
	for _, name := range financeTables {
		if !slices.Contains(TableNames(), name) {
			t.Errorf("financeTables names %q, which is not a table", name)
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

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/repositories/ -run 'TestEveryTableIsClassifiedForThePurge|TestPurgeSpaceRefusesTheZeroSpace'`
Expected: FAIL, `undefined: financeTables`, `undefined: SpacePurger`.

- [ ] **Step 4: Implement**

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

// financeTables are the tables whose rows live under a finance space key S
// (spec § 4, 6.5, 6.6, BYOK § 12.2). PurgeSpace clears each one's S partition,
// and the child partitions below. TestEveryTableIsClassifiedForThePurge makes a
// new table choose a side.
var financeTables = []string{
	TableLedgerAccounts, TableLedgerTransactions, TableBills, TableRecurrences,
	TableCards, TableImports, TableByok,
}

// batchDeleteAttempts bounds the retries of a BatchWriteItem's unprocessed
// keys. Running out is an error. The saga redelivers, and the purge restarts
// from a fresh read.
const batchDeleteAttempts = 5

// attrHookID is the connection row's pointer to its HOOK#{id} partition.
const attrHookID = "hook_id"

// SpacePurger deletes a finance space (ADR 0026): a personal space when its
// owner's CTech account is deleted, any workspace's space when the workspace
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
// Child partitions (entries, cards, imports, hooks) are deleted before the
// rows that name them, so a run that dies anywhere still finds what is left.
//
// The space's audit rows go too, whatever the space's kind (decision
// 2026-10-13): the workspace itself is gone, so the five-year trail that ADR
// 0009 keeps for a surviving organization has nothing left to explain.
func (p *SpacePurger) PurgeSpace(ctx context.Context, sp space.ResolvedSpace) (map[string]int, error) {
	if sp.IsZero() {
		return nil, space.ErrNoSpace
	}
	counts := map[string]int{}
	s := sp.PK()
	child := func(table, pk string) error {
		n, err := p.deletePartition(ctx, table, pk)
		counts[table] += n
		return err
	}

	// 1. Ledger entries S#ACCOUNT#{id}. The ids come from the chart and from
	// every transaction's legs, so entries are found even after a previous run
	// deleted the chart.
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
		if err := child(TableLedgerTransactions, LedgerEntryPK(sp, account)); err != nil {
			return counts, err
		}
	}

	// 2. Card partitions S#CARD#{id}: purchases, statements, statement items.
	// The card ids are the S partition's CARD# rows.
	if err := p.eachID(ctx, TableCards, s, "CARD#", func(id string) error {
		return child(TableCards, CardPK(sp, id))
	}); err != nil {
		return counts, err
	}

	// 3. Import partitions S#IMPORT#{id}: the parsed lines. The FITID# locks and
	// CSVMAP# rows are in S itself.
	if err := p.eachID(ctx, TableImports, s, "IMPORT#", func(id string) error {
		return child(TableImports, ImportPK(sp, id))
	}); err != nil {
		return counts, err
	}

	// 4. BYOK hooks HOOK#{id}, named by the connection rows' hook_id.
	conns, err := queryPrefix(ctx, p.bases[TableByok], s, "")
	if err != nil {
		return counts, err
	}
	for _, it := range conns {
		if v, ok := it[attrHookID].(*types.AttributeValueMemberS); ok && v.Value != "" {
			if err := child(TableByok, hookPK(v.Value)); err != nil {
				return counts, err
			}
		}
	}

	// 5. The space partition S in every table that keys by it, then audit and
	// idempotency.
	tables := append(append([]string(nil), financeTables...), TableIdempotency, TableAudit)
	for _, t := range tables {
		if err := child(t, s); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// eachID calls fn with the id of every row under pk whose sort key starts with
// prefix (the sort key is prefix + id).
func (p *SpacePurger) eachID(ctx context.Context, table, pk, prefix string, fn func(id string) error) error {
	rows, err := queryPrefix(ctx, p.bases[table], pk, prefix)
	if err != nil {
		return err
	}
	for _, it := range rows {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if err := fn(strings.TrimPrefix(sk.Value, prefix)); err != nil {
				return err
			}
		}
	}
	return nil
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

- [ ] **Step 5: Run the unit tests to verify they pass**

Run: `go test ./internal/repositories/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Write the integration tests**

Append to `tests/integration/erasure_test.go`:

```go
var seedNow = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

// seedFinance writes a space as a person uses it, across EVERY space-keyed
// table: the chart, a transfer, a bill, a recurrence, a card with a purchase,
// an import with its lines and locks, a BYOK connection with a hook, an audit
// row and a cached idempotent response. A new space-keyed table must be added
// here, which assertSpaceGone then checks.
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
	seedCardWithPurchase(t, sp)  // cards: S and S#CARD#{id}
	seedImportWithLines(t, sp)   // imports: S (IMPORT#, FITID#, CSVMAP#) and S#IMPORT#{id}
	seedByokConnectionWithHook(t, sp) // byok: S (CONNECTION#, SEALED#) and HOOK#{id}
	if err := repositories.NewIdempotencyRepository(testDB, testCfg).Store(ctx, sp.Owner(), sp.Livemode(),
		repositories.IdempotencyRecord{Key: "k-" + id.New(), RequestHash: "h", Status: 201, Response: "{}", Route: "/x"}, seedNow); err != nil {
		t.Fatal(err)
	}
}
```

Each `seedX` helper uses the repository's own public constructor path (`CardRepository.CreateCard` and its purchase method, `ImportRepository` parse and stage, `ByokRepository.PutConnection`/`PutHook` or `ByokConnections.EnableSandbox` for the shared sandbox), so a change to those key layouts breaks the seed instead of silently desynchronizing it. `rowsUnder(t, logical, pk)` queries the table by partition key and returns the count; `assertSpaceGone(t, sp)` asserts zero rows under `sp.PK()` in every `financeTables` entry plus `idempotency` and `audit`, zero under each of the seeded `S#ACCOUNT#`, `S#CARD#`, `S#IMPORT#` and `HOOK#` partitions.

```go
func TestPurgeSpaceErasesEveryTableAndIsSafeToRepeat(t *testing.T) {
	ctx := context.Background()
	purger := repositories.NewSpacePurger(testDB, testCfg)
	sp := jobSpace(t, "USER#"+id.New(), true)
	other := jobSpace(t, "USER#"+id.New(), true)
	seedFinance(t, sp)
	seedFinance(t, other)

	first, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ledger_accounts", "ledger_transactions", "bills", "recurrences", "cards", "imports", "byok", "audit", "idempotency"} {
		if first[table] == 0 {
			t.Errorf("first run deleted nothing from %s; the seed or the purge skips it", table)
		}
	}
	assertSpaceGone(t, sp)

	second, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range second {
		if n != 0 {
			t.Errorf("second run deleted %d rows from %s; the purge is not idempotent", n, table)
		}
	}
	assertSpaceUntouched(t, other) // a different person's space is exactly as seeded
}

// A run that died after deleting the parent rows must still find and delete the
// child partitions. Here the chart, the card rows and the import rows are gone
// first, as an interrupted earlier run would leave them.
func TestPurgeFindsPartitionsAfterTheirParentsAreGone(t *testing.T) {
	ctx := context.Background()
	sp := jobSpace(t, "USER#"+id.New(), true)
	seedFinance(t, sp)
	deleteRows(t, "ledger_accounts", sp.PK())
	deleteRowsWithPrefix(t, "cards", sp.PK(), "CARD#")
	deleteRowsWithPrefix(t, "imports", sp.PK(), "IMPORT#")
	deleteRowsWithPrefix(t, "byok", sp.PK(), "SEALED#")

	if _, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	assertSpaceGone(t, sp) // includes the S#ACCOUNT#, S#CARD#, S#IMPORT# and HOOK# partitions
}
```

The connection row stays in this test on purpose: it is what names the `HOOK#{id}` partition, so the purge deletes hooks **before** the `S` partition (Step 4 order). Everything else a dead run could have removed first is removed.

- [ ] **Step 7: Run all suites**

Run: `go test ./internal/... -count=1 && make test-integration`
Expected: `ok`, and PASS for both tests.

- [ ] **Step 8: Commit**

```bash
git add internal/repositories tests/integration/erasure_test.go
git commit -m "feat(api): SpacePurger deletes a finance space across every table, with its cards, imports and bank hooks"
```

---

### Task 5b: Delete the level rows of one `customer_ref`

Level reports (`usage` table) are keyed `{tenant}#LEVEL#{customer_ref}#{meter}` (history, 13 months) and `{tenant}#LEVEL_LATEST#{customer_ref}#{meter}` (newest, no TTL). Tenant zero's refs are `USER_{sub}` (Finanças plans) and `ORG_{id}` (DF-e companies). Meters are free-form, so they cannot be listed by ref (R21); the caller passes them.

**Files:**
- Modify: `api/internal/repositories/levels.go` (`EraseRef`)
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes: `LevelPK`, `LevelLatestPK`, `queryPrefix` (Task 5), the batch delete of Task 5 (extract `deleteKeys(ctx, base, items)` from `SpacePurger.deletePartition` as a package function so both use it).
- Produces: `(*LevelRepository).EraseRef(ctx, organizationID string, livemode bool, customerRef string, meters []string) (int, error)`: rows deleted; idempotent.

- [ ] **Step 1: Write the failing test**

```go
func TestEraseRefDeletesHistoryAndLatestOfThatRefOnly(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	mine, other := "USER_"+id.New(), "USER_"+id.New()
	for _, ref := range []string{mine, other} {
		for _, meter := range []string{"finance_spaces", "finance_people"} {
			if err := levels.Append(ctx, &billing.LevelRecord{
				OrganizationID: e.org.ID, Livemode: true, CustomerRef: ref, Meter: meter, Value: 2,
				OccurredAt: seedNow, IdempotencyKey: "k-" + id.New(),
			}, seedNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	n, err := levels.EraseRef(ctx, e.org.ID, true, mine, []string{"finance_spaces", "finance_people", "never_reported"})
	if err != nil || n != 4 { // one history + one latest row per reported meter
		t.Fatalf("EraseRef = %d, %v; want 4", n, err)
	}
	if l, err := levels.Latest(ctx, e.org.ID, true, mine, "finance_spaces"); err == nil && l != nil {
		t.Fatalf("latest of the erased ref survives: %+v", l)
	}
	if l, err := levels.Latest(ctx, e.org.ID, true, other, "finance_spaces"); err != nil || l == nil {
		t.Fatalf("another ref's latest was touched: %+v, %v", l, err)
	}
	if n, err := levels.EraseRef(ctx, e.org.ID, true, mine, []string{"finance_spaces"}); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0", n, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go vet -tags integration ./tests/...`
Expected: FAIL, `EraseRef undefined`.

- [ ] **Step 3: Implement**

In `finance_purge.go`, extract the batch loop of `deletePartition` into `deleteKeys(ctx context.Context, b Base, items []map[string]types.AttributeValue) (int, error)` and make `deletePartition` call it after `queryPrefix`. In `levels.go`:

```go
// EraseRef deletes the level history and the latest level of customerRef for
// each meter (account deletion, R21). The LEVEL_KEY# idempotency markers hold a
// hash only and expire with the 13-month TTL, so they stay.
func (r *LevelRepository) EraseRef(ctx context.Context, organizationID string, livemode bool, customerRef string, meters []string) (int, error) {
	total := 0
	for _, meter := range meters {
		for _, pk := range []string{
			LevelPK(organizationID, livemode, customerRef, meter),
			LevelLatestPK(organizationID, livemode, customerRef, meter),
		} {
			items, err := queryPrefix(ctx, r.base, pk, "")
			if err != nil {
				return total, err
			}
			n, err := deleteKeys(ctx, r.base, items)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/repositories/ -count=1 && make test-integration`
Expected: `ok`, and `--- PASS: TestEraseRefDeletesHistoryAndLatestOfThatRefOnly`.

- [ ] **Step 5: Commit**

```bash
git add internal/repositories tests/integration/erasure_test.go
git commit -m "feat(api): delete the level rows of one customer ref"
```

---

### Task 6: `services.Eraser` — blockers and the purge

**Files:**
- Create: `api/internal/services/erasing.go`
- Test: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Consumes:
  - Task 4 and 5/5b: `ListByCustomer(…, 0)`, `ClearMetadata`, `SpacePurger.PurgeSpace`, `LevelRepository.EraseRef`
  - existing: `CustomerRepository.GetByUser`/`GetByOrganization`/`Anonymize`, `OrganizationRepository.GetByAccountOrganization`, `SubscriptionRepository.Transition`, `CatalogRepository.ListPrices`, `ByokConnections.List`/`Disconnect`
  - `erasure.Blocker`/`Ack`/`Message`/`ResultDone`/`ResultBlocked`
- Produces:
  - `services.BlockerInvoiceOpen = "billing.invoice_open"`, `services.BlockerInvoiceUncollectible = "billing.invoice_uncollectible"` and `services.BlockerMerchantTenant = "billing.merchant_tenant"`
  - `services.NewEraser(customers, subs, invoices, orgs, catalog, levels, portalOrganizationID, invoicesURL) *Eraser`
  - `(*Eraser).WithPurge(p *repositories.SpacePurger, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks BankConnections) *Eraser`
  - `services.BankConnections` (interface: `List(ctx, sp) ([]byokconn.Connection, error)` and `Disconnect(ctx, sp, provider, actor, requestID string, now time.Time) error`), satisfied by `*ByokConnections`
  - `(*Eraser).Blockers(ctx context.Context, sub string, workspaces []string) ([]erasure.Blocker, error)`
  - `(*Eraser).Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error)`, matching `erasure.PurgeFunc`
  - Ack `Counts` keys: `customers_anonymized`, `subscriptions_canceled`, `invoices_retained`, `bank_connections_disconnected`, `level_rows`, `finance_{table}`

- [ ] **Step 1: Write the failing integration tests**

Append to `tests/integration/erasure_test.go` (imports: `strings`, `services`, `erasure`, `space`, `repositories`, `billing`, `brcal`, `id`):

```go
func eraserFor(e *portalEnv) *services.Eraser {
	return services.NewEraser(
		repositories.NewCustomerRepository(testDB, testCfg),
		repositories.NewSubscriptionRepository(testDB, testCfg),
		repositories.NewInvoiceRepository(testDB, testCfg),
		repositories.NewOrganizationRepository(testDB, testCfg),
		repositories.NewCatalogRepository(testDB, testCfg),
		repositories.NewLevelRepository(testDB, testCfg),
		e.org.ID, "https://billing.test/invoices",
	).WithPurge(repositories.NewSpacePurger(testDB, testCfg), space.ForJob, noBanks{})
}

// noBanks is a BankConnections with no connections; the BYOK path has its own
// test (TestPurgeDisconnectsBankConnections).
type noBanks struct{}

func (noBanks) List(context.Context, space.ResolvedSpace) ([]byokconn.Connection, error) { return nil, nil }
func (noBanks) Disconnect(context.Context, space.ResolvedSpace, string, string, string, time.Time) error {
	return nil
}

func eraseMessage(sub string, workspaces ...string) erasure.Message {
	m := erasure.Message{
		Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req_" + id.New(), Sub: sub,
		Scope: erasure.ScopeAccount, Services: []string{"billing"}, Attempt: 1, IssuedAt: time.Now(),
	}
	for _, w := range workspaces {
		m.Workspaces = append(m.Workspaces, erasure.Workspace{ID: w, Kind: erasure.WorkspaceKindPersonal})
	}
	return m
}

// newOrgCustomer is the tenant-zero customer of a ctech-account workspace: the
// ORG_{id} reference and its CUSTOMER_ORG# pointer (ADR 0025 amendment).
func newOrgCustomer(t *testing.T, e *portalEnv, workspaceID string) *billing.Customer {
	t.Helper()
	c := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Empresa MEI de Fulano", Email: "mei@example.com", TaxID: "12345678000199",
		ExternalRef: repositories.OrgRefPrefix + workspaceID,
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), c, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return c
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
	if c, err := repositories.NewCustomerRepository(testDB, testCfg).Get(ctx, e.org.ID, true, e.customer.ID); err != nil || c.Anonymized {
		t.Fatalf("customer anonymized by a blocked purge: %+v, %v", c, err)
	}
	if s, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID); err != nil || s.Status != billing.SubscriptionActive {
		t.Fatalf("subscription touched by a blocked purge: %+v, %v", s, err)
	}
	assertSpaceUntouched(t, personal)
}

// Review Focus 5: the person goes; the documents, another customer's debt and a
// surviving organization's space stay.
func TestPurgeErasesThePersonAndKeepsTheDocuments(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	sub := newSubscriptionWithMetadata(t, e.org, e.customer.ID, e.priceID)
	inv := newSubscriptionInvoice(t, e.org, sub)
	paid(t, inv) // a settled invoice does not block
	personal := jobSpace(t, "USER#"+e.userID, true)
	seedFinance(t, personal)
	surviving := jobSpace(t, "org_"+id.New(), true)
	seedFinance(t, surviving)

	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID))
	if err != nil || ack.Result != erasure.ResultDone {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	if ack.Counts["subscriptions_canceled"] != 1 || ack.Counts["customers_anonymized"] != 1 || ack.Counts["invoices_retained"] != 1 {
		t.Fatalf("counts = %v", ack.Counts)
	}
	c, _ := repositories.NewCustomerRepository(testDB, testCfg).Get(ctx, e.org.ID, true, e.customer.ID)
	if !c.Anonymized || c.Email != "" || c.TaxID != "" {
		t.Fatalf("customer = %+v; want anonymized", c)
	}
	s, _ := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID)
	if s.Status != billing.SubscriptionCanceled || len(s.Metadata) != 0 {
		t.Fatalf("subscription = %+v; want canceled, no metadata", s)
	}
	if got, _ := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctx, e.org.ID, true, inv.ID); got == nil {
		t.Fatal("the invoice is a document and must stay")
	}
	assertSpaceGone(t, personal)
	assertSpaceUntouched(t, surviving)

	again, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID)) // idempotent
	if err != nil || again.Result != erasure.ResultDone || again.Counts["subscriptions_canceled"] != 0 || again.Counts["customers_anonymized"] != 0 {
		t.Fatalf("second purge = %+v, %v", again, err)
	}
}

// R19, R12: an erased workspace takes its organization customer, that
// customer's DF-e subscription and its whole finance space with it.
func TestPurgeErasesAnErasedWorkspaceCustomerSubscriptionAndSpace(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	ws := "org_" + id.New()
	c := newOrgCustomer(t, e, ws)
	sub := newSubscriptionIn(t, e.org, c.ID, e.priceID, billing.SubscriptionActive)
	space := jobSpace(t, ws, true)
	seedFinance(t, space)

	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID, ws))
	if err != nil || ack.Result != erasure.ResultDone {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	got, _ := repositories.NewCustomerRepository(testDB, testCfg).Get(ctx, e.org.ID, true, c.ID)
	if !got.Anonymized || got.TaxID != "" || got.ExternalRef != "" {
		t.Fatalf("organization customer = %+v; want anonymized", got)
	}
	if s, _ := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctx, e.org.ID, true, sub.ID); s.Status != billing.SubscriptionCanceled {
		t.Fatalf("organization subscription = %s; want canceled", s.Status)
	}
	assertSpaceGone(t, space) // including its audit rows, whatever its kind (decision 2)
}

// R3, Review Focus 6: a workspace with a billing tenant is never erased
// silently. Nothing is touched, in either mode.
func TestAWorkspaceWithAMerchantTenantBlocksErasure(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	ws := "org_" + id.New()
	newLinkedOrg(t, false, ws) // a test-mode tenant is enough
	space := jobSpace(t, ws, true)
	seedFinance(t, space)

	blockers, err := eraserFor(e).Blockers(ctx, e.userID, []string{ws})
	if err != nil || len(blockers) != 1 || blockers[0].Code != services.BlockerMerchantTenant {
		t.Fatalf("blockers = %+v, %v; want %s", blockers, err, services.BlockerMerchantTenant)
	}
	if ids, _ := blockers[0].Detail["workspace_ids"].([]string); len(ids) != 1 || ids[0] != ws {
		t.Fatalf("detail = %v", blockers[0].Detail)
	}
	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID, ws))
	if err != nil || ack.Result != erasure.ResultBlocked {
		t.Fatalf("purge = %+v, %v; want blocked", ack, err)
	}
	assertSpaceUntouched(t, space)
}

// An unpaid invoice of an erased workspace's customer blocks like the person's.
func TestAnOrganizationInvoiceBlocksErasure(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	ws := "org_" + id.New()
	c := newOrgCustomer(t, e, ws)
	finalizeInvoice(t, newInvoiceFor(t, e.org, c.ID))

	blockers, err := eraserFor(e).Blockers(ctx, e.userID, []string{ws})
	if err != nil || len(blockers) != 1 || blockers[0].Code != services.BlockerInvoiceOpen ||
		blockers[0].Detail["amount_cents"] != int64(4990) {
		t.Fatalf("blockers = %+v, %v", blockers, err)
	}
	if got, _ := eraserFor(e).Blockers(ctx, e.userID, nil); len(got) != 0 {
		t.Fatalf("without the workspace in the question the person is clear: %+v", got)
	}
}

// A written-off CTech debt still blocks (user decision 2026-10-08). It has its
// own code and no action_url, because the portal cannot collect it.
func TestAWrittenOffInvoiceBlocksErasure(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	finalizeInvoice(t, inv)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Transition(ctx, inv, billing.InvoiceUncollectible,
		billing.CauseDunningExhausted, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	blockers, err := eraserFor(e).Blockers(ctx, e.userID, nil)
	if err != nil || len(blockers) != 1 || blockers[0].Code != services.BlockerInvoiceUncollectible ||
		blockers[0].ActionURL != "" || blockers[0].Detail["amount_cents"] != int64(4990) || blockers[0].Detail["count"] != 1 {
		t.Fatalf("blockers = %+v, %v; want one %s for 4990 with no action_url", blockers, err, services.BlockerInvoiceUncollectible)
	}
	if ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID)); err != nil || ack.Result != erasure.ResultBlocked {
		t.Fatalf("purge = %+v, %v; want blocked", ack, err)
	}
}

// R21: the person's level rows go with them.
func TestPurgeErasesTheLevelRowsOfTheErasedRefs(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	ref := "USER_" + e.userID
	addMeteredPrice(t, e.org, "finance_spaces")
	if err := levels.Append(ctx, &billing.LevelRecord{OrganizationID: e.org.ID, Livemode: true, CustomerRef: ref,
		Meter: "finance_spaces", Value: 3, OccurredAt: seedNow, IdempotencyKey: "k-" + id.New()}, seedNow); err != nil {
		t.Fatal(err)
	}
	ack, err := eraserFor(e).Purge(ctx, eraseMessage(e.userID))
	if err != nil || ack.Result != erasure.ResultDone || ack.Counts["level_rows"] != 2 {
		t.Fatalf("ack = %+v, %v; want done with 2 level rows", ack, err)
	}
	if l, err := levels.Latest(ctx, e.org.ID, true, ref, "finance_spaces"); err == nil && l != nil {
		t.Fatalf("latest level survives: %+v", l)
	}
}

// R23: a workspace's bank connections are disconnected before its space goes.
func TestPurgeDisconnectsBankConnections(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	ws := "org_" + id.New()
	banks := &recordingBanks{conns: map[string][]byokconn.Connection{ws + "#live": {{Provider: "inter", AccountOrganizationID: ws, Livemode: true}}}}
	er := eraserFor(e).WithPurge(repositories.NewSpacePurger(testDB, testCfg), space.ForJob, banks)
	ack, err := er.Purge(ctx, eraseMessage(e.userID, ws))
	if err != nil || ack.Result != erasure.ResultDone || ack.Counts["bank_connections_disconnected"] != 1 || len(banks.disconnected) != 1 {
		t.Fatalf("ack = %+v, %v, disconnected %v", ack, err, banks.disconnected)
	}
}
```

`recordingBanks` implements `services.BankConnections`, returning `conns[sp.PK()]` and recording `Disconnect` calls. `paid`, `addMeteredPrice`, `assertSpaceUntouched` are small helpers beside `seedFinance`.

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
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Blocker codes, stable and translated by ctech-account's UI. Never rename.
const (
	// BlockerInvoiceOpen is a live tenant-zero invoice still owed by the person
	// or by a customer of a workspace being erased. The person pays it in the
	// portal (action_url) and asks again.
	BlockerInvoiceOpen = "billing.invoice_open"
	// BlockerInvoiceUncollectible is a live tenant-zero invoice that dunning
	// wrote off with an amount still due. A written-off debt still blocks (user
	// decision 2026-10-08). The portal cannot collect it, so support records the
	// payment (CauseManualPayment), and the blocker has no action_url.
	BlockerInvoiceUncollectible = "billing.invoice_uncollectible"
	// BlockerMerchantTenant is a workspace being erased that has a billing
	// tenant (decision 2026-10-13). Deleting a tenant is its own flow, which does
	// not exist yet, so the person asks support. detail.workspace_ids names them.
	BlockerMerchantTenant = "billing.merchant_tenant"
)

// erasureActor names the saga in the audit trail of what it changes.
const erasureActor = "account-erasure"

// BankConnections is the BYOK surface the purge needs (R23).
type BankConnections interface {
	List(ctx context.Context, sp space.ResolvedSpace) ([]byokconn.Connection, error)
	Disconnect(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) error
}

// Eraser is billing's side of the account-deletion saga (ctech-account
// docs/specs/2026-10-06-account-deletion-saga-protocol.md § 4; data inventory
// § 6). Blockers answers the eligibility question, and Purge is the consumer's
// PurgeFunc.
//
// What it keeps and why is ADR 0009 and ADR 0026: the invoice aggregate and the
// canceled subscriptions are documents; tenant-zero customers are anonymized in
// place; the finance spaces of the person and of the workspaces erased with
// them are deleted.
type Eraser struct {
	customers   *repositories.CustomerRepository
	subs        *repositories.SubscriptionRepository
	invoices    *repositories.InvoiceRepository
	orgs        *repositories.OrganizationRepository
	catalog     *repositories.CatalogRepository
	levels      *repositories.LevelRepository
	portalOrg   string
	invoicesURL string

	purger   *repositories.SpacePurger
	spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)
	banks    BankConnections
}

// NewEraser builds the eligibility side. portalOrganizationID is tenant zero
// (empty: there are no customers to look for). invoicesURL is the portal page a
// blocker points the person at (empty: no action_url).
func NewEraser(
	customers *repositories.CustomerRepository,
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	orgs *repositories.OrganizationRepository,
	catalog *repositories.CatalogRepository,
	levels *repositories.LevelRepository,
	portalOrganizationID, invoicesURL string,
) *Eraser {
	return &Eraser{customers: customers, subs: subs, invoices: invoices, orgs: orgs, catalog: catalog, levels: levels,
		portalOrg: portalOrganizationID, invoicesURL: invoicesURL}
}

// WithPurge adds what Purge needs. spaceFor is space.ForJob, passed in from a
// cmd/ binary: ForJob is forbidden under internal/.
func (e *Eraser) WithPurge(p *repositories.SpacePurger, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks BankConnections) *Eraser {
	e.purger, e.spaceFor, e.banks = p, spaceFor, banks
	return e
}

// debt is what a customer still owes CTech, live mode.
type debt struct {
	open, overdue, writtenOff int
	openDue, writtenOffDue    billing.Cents
}

func (d *debt) add(o debt) {
	d.open += o.open
	d.overdue += o.overdue
	d.writtenOff += o.writtenOff
	d.openDue += o.openDue
	d.writtenOffDue += o.writtenOffDue
}

func (e *Eraser) debtOf(ctx context.Context, customerID string) (debt, error) {
	var d debt
	invoices, err := e.invoices.ListByCustomer(ctx, e.portalOrg, true, customerID, 0)
	if err != nil {
		return d, err
	}
	today := brcal.Today()
	for _, inv := range invoices {
		if inv.AmountDue() <= 0 {
			continue
		}
		switch inv.Status {
		case billing.InvoiceOpen:
			d.open++
			d.openDue += inv.AmountDue()
			if inv.IsOverdue(today) {
				d.overdue++
			}
		case billing.InvoiceUncollectible:
			d.writtenOff++
			d.writtenOffDue += inv.AmountDue()
		}
	}
	return d, nil
}

// Blockers reports why sub, together with the workspaces erased with them,
// cannot be erased yet. An unknown sub and no workspaces have none. Live mode
// only: test money is not owed (R15).
func (e *Eraser) Blockers(ctx context.Context, sub string, workspaces []string) ([]erasure.Blocker, error) {
	var out []erasure.Blocker

	var tenants []string
	for _, ws := range workspaces {
		has, err := e.hasTenant(ctx, ws)
		if err != nil {
			return nil, err
		}
		if has {
			tenants = append(tenants, ws)
		}
	}
	if len(tenants) > 0 {
		out = append(out, erasure.Blocker{Code: BlockerMerchantTenant, Detail: map[string]any{"workspace_ids": tenants}})
	}
	if e.portalOrg == "" {
		return out, nil
	}

	var total debt
	var owing []string
	if c, err := e.customers.GetByUser(ctx, e.portalOrg, true, sub); err == nil {
		d, err := e.debtOf(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		total.add(d)
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	for _, ws := range workspaces {
		c, err := e.customers.GetByOrganization(ctx, e.portalOrg, true, ws)
		if errors.Is(err, repositories.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d, err := e.debtOf(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if d.open+d.writtenOff > 0 {
			owing = append(owing, ws)
		}
		total.add(d)
	}
	if total.open > 0 {
		detail := map[string]any{"count": total.open, "overdue": total.overdue, "amount_cents": int64(total.openDue)}
		if len(owing) > 0 {
			detail["workspace_ids"] = owing
		}
		out = append(out, erasure.Blocker{Code: BlockerInvoiceOpen, Detail: detail, ActionURL: e.invoicesURL})
	}
	if total.writtenOff > 0 {
		// No action_url: the portal cannot collect a written-off invoice
		// (Invoice.Payable needs OPEN). Support records the payment.
		out = append(out, erasure.Blocker{
			Code:   BlockerInvoiceUncollectible,
			Detail: map[string]any{"count": total.writtenOff, "amount_cents": int64(total.writtenOffDue)},
		})
	}
	return out, nil
}

// hasTenant reports a billing tenant linked to the ctech-account workspace in
// either mode (a test-mode tenant counts: R3).
func (e *Eraser) hasTenant(ctx context.Context, workspaceID string) (bool, error) {
	for _, live := range []bool{true, false} {
		_, err := e.orgs.GetByAccountOrganization(ctx, workspaceID, live)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, repositories.ErrNotFound):
		default:
			return false, err
		}
	}
	return false, nil
}

// Purge erases m.Sub's billing data and the data of every workspace in
// m.WorkspaceIDs(). It re-checks the blockers first and never half-purges.
// Idempotent and resumable: every step re-reads and skips what is already done.
//
// Order matters. Customers are anonymized first, so the finance replay queue's
// payer side skips them (payer_anonymized) instead of writing into a space
// about to be deleted; levels and bank connections go next; the spaces last.
func (e *Eraser) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
	if e.purger == nil || e.spaceFor == nil || e.banks == nil {
		return erasure.Ack{}, errors.New("eraser: purge is not configured")
	}
	workspaces := m.WorkspaceIDs()
	blockers, err := e.Blockers(ctx, m.Sub, workspaces)
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
			if c, err := e.customers.GetByUser(ctx, e.portalOrg, live, m.Sub); err == nil {
				if err := e.eraseCustomer(ctx, c, m, counts, now); err != nil {
					return erasure.Ack{}, err
				}
			} else if !errors.Is(err, repositories.ErrNotFound) {
				return erasure.Ack{}, err
			}
			for _, ws := range workspaces {
				c, err := e.customers.GetByOrganization(ctx, e.portalOrg, live, ws)
				if errors.Is(err, repositories.ErrNotFound) {
					continue
				}
				if err != nil {
					return erasure.Ack{}, err
				}
				if err := e.eraseCustomer(ctx, c, m, counts, now); err != nil {
					return erasure.Ack{}, err
				}
			}
			if err := e.eraseLevels(ctx, live, m.Sub, workspaces, counts); err != nil {
				return erasure.Ack{}, err
			}
		}
	}
	for _, owner := range append([]string{"USER#" + m.Sub}, workspaces...) {
		for _, live := range []bool{true, false} {
			sp, err := e.spaceFor(owner, live)
			if err != nil {
				// A sub or workspace id that cannot name a space never had one.
				continue
			}
			if err := e.disconnectBanks(ctx, sp, m, counts, now); err != nil {
				return erasure.Ack{}, err
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

// eraseCustomer cancels a tenant-zero customer's subscriptions, clears their
// metadata, and anonymizes the customer. Invoices are counted, never touched.
func (e *Eraser) eraseCustomer(ctx context.Context, c *billing.Customer, m erasure.Message, counts map[string]int, now time.Time) error {
	subs, err := e.subs.ListByCustomer(ctx, e.portalOrg, c.Livemode, c.ID, 0)
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
	invoices, err := e.invoices.ListByCustomer(ctx, e.portalOrg, c.Livemode, c.ID, 0)
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

// eraseLevels deletes the level rows of USER_{sub} and ORG_{id} for every meter
// tenant zero's catalogue declares (R21).
func (e *Eraser) eraseLevels(ctx context.Context, live bool, sub string, workspaces []string, counts map[string]int) error {
	prices, err := e.catalog.ListPrices(ctx, e.portalOrg, live, 0)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var meters []string
	for _, p := range prices {
		if p.Meter != "" && !seen[p.Meter] {
			seen[p.Meter] = true
			meters = append(meters, p.Meter)
		}
	}
	refs := []string{"USER_" + sub}
	for _, ws := range workspaces {
		refs = append(refs, repositories.OrgRefPrefix+ws)
	}
	for _, ref := range refs {
		n, err := e.levels.EraseRef(ctx, e.portalOrg, live, ref, meters)
		counts["level_rows"] += n
		if err != nil {
			return err
		}
	}
	return nil
}

// disconnectBanks disconnects every BYOK connection of the space (R23).
func (e *Eraser) disconnectBanks(ctx context.Context, sp space.ResolvedSpace, m erasure.Message, counts map[string]int, now time.Time) error {
	conns, err := e.banks.List(ctx, sp)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if err := e.banks.Disconnect(ctx, sp, c.Provider, erasureActor, m.RequestID, now); err != nil {
			return fmt.Errorf("disconnecting %s in %s: %w", c.Provider, sp.PK(), err)
		}
		counts["bank_connections_disconnected"]++
	}
	return nil
}
```

- [ ] **Step 4: Run the suites**

Run: `go vet ./... && go test ./internal/services/ -count=1 && make test-integration`
Expected: `ok`, and PASS for every test in this task.

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
  - the optional query parameter `workspaces=id1,id2` (R20): the workspaces where the person is the only member, sent by ctech-account
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
	ws := "org_" + id.New()
	newLinkedOrg(t, true, ws)
	if code, el := askAbout(e, e.userID, asAccount, ws); code != http.StatusOK || el.Eligible || el.Blockers[0].Code != services.BlockerMerchantTenant {
		t.Fatalf("a workspace with a tenant: %d %+v; want %s", code, el, services.BlockerMerchantTenant)
	}
	if res := e.do(t, http.MethodGet, "/v1.0/internal/erasure/eligibility/"+e.userID+"?workspaces=not-a-workspace", asAccount, "", ""); res.status != http.StatusBadRequest {
		t.Fatalf("a malformed workspace id: %d, want 400", res.status)
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
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// maxWorkspaces bounds the eligibility question; a person who is the only member
// of more workspaces than this has a plan limit to explain first.
const maxWorkspaces = 100

// workspacesParam parses ?workspaces=a,b. Empty is no workspaces. An id that is
// not shaped like a workspace is a 400, because it will become a partition key.
func workspacesParam(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	ids := strings.Split(raw, ",")
	if len(ids) > maxWorkspaces {
		return nil, fmt.Errorf("at most %d workspaces", maxWorkspaces)
	}
	for _, id := range ids {
		if !space.IsOrganizationID(id) {
			return nil, fmt.Errorf("workspaces: %q is not a workspace id", id)
		}
	}
	return ids, nil
}

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
			workspaces, err := workspacesParam(c.Query("workspaces"))
			if err != nil {
				return problem.BadRequest(err.Error()).Send(c)
			}
			blockers, err := d.Eraser.Blockers(c.Context(), c.Params("sub"), workspaces)
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
		repositories.NewCatalogRepository(db, cfg),
		repositories.NewLevelRepository(db, cfg),
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

The job has three steps that write into a space from a work item read earlier: `Materialise` (recurrences), `AutoSettle` (bills) and `CloseStatements` (cards). Each skips an item whose space owner is locked or erased.

**Files:**
- Modify: `api/internal/services/finance_recurrences.go`, `api/internal/services/finance_cards.go`
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
	bills, recs, cards := newFakeBills(), newFakeRecs(monthly("r1", 10)), newFakeCards(dueCard("c1"))
	bills.autoDue = []repositories.DueBill{dueBill("b1", day(2026, time.March, 10))}
	asked := 0
	jobs := NewFinanceJobs(bills, recs).WithCards(cards).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) {
		asked++
		return true, nil
	})
	today, now := day(2026, time.March, 20), time.Now()

	m := jobs.Materialise(context.Background(), true, today, now)
	a := jobs.AutoSettle(context.Background(), true, today, now)
	c := jobs.CloseStatements(context.Background(), true, today, now)
	if m.Done != 0 || m.Skipped != 1 || bills.created != 0 || recs.marks != 0 {
		t.Fatalf("materialise = %+v, created %d, cursor moves %d; want the erased space skipped", m, bills.created, recs.marks)
	}
	if a.Done != 0 || a.Skipped != 1 || len(bills.settled) != 0 {
		t.Fatalf("auto-settle = %+v, settled %v; want the erased space skipped", a, bills.settled)
	}
	if c.Done != 0 || c.Skipped != 1 || cards.closed != 0 {
		t.Fatalf("close = %+v, closed %d; want the erased space skipped", c, cards.closed)
	}
	if asked != 3 {
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

`newFakeCards`, `dueCard` and the `closed` counter extend the existing card fake in the same file (`cardStore` already has a fake for `CloseStatements` tests; reuse it and add the counter if it lacks one).

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/services/ -run 'TestTheJobLeavesAnErasedSpaceAlone|TestAGuardErrorFailsTheItemNotTheRun'`
Expected: FAIL, `jobs.WithSpaceGuard undefined`.

- [ ] **Step 3: Implement**

`finance_recurrences.go`. Add the field to `FinanceJobs`:

```go
	// gone reports a space whose owner the account-deletion saga has locked or
	// erased. Nil means no guard (the console's projection read).
	gone func(ctx context.Context, sp space.ResolvedSpace) (bool, error)
```

and after `NewFinanceJobs`:

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

In `Materialise`, directly after `rec := d.Recurrence` (before the `rec.IsCard()` branch):

```go
		if j.skip(ctx, d.Space, &res, "recurrence "+rec.ID) {
			continue
		}
```

In `AutoSettle`, directly after `res.Examined++`:

```go
		if j.skip(ctx, d.Space, &res, "bill "+d.Bill.ID) {
			continue
		}
```

In `CloseStatements` (`finance_cards.go`), directly after `res.Examined++`:

```go
		if j.skip(ctx, d.Space, &res, "card "+d.CardID) {
			continue
		}
```

`erasing.go`: add `"strings"` to the imports and append:

```go
// ErasedSpace reports a space whose owner is locked or erased in the
// account-deletion saga: a personal space by its user, a workspace's space by
// the workspace's tombstone. It is the daily job's guard.
func ErasedSpace(store *erasure.Store) func(context.Context, space.ResolvedSpace) (bool, error) {
	return func(ctx context.Context, sp space.ResolvedSpace) (bool, error) {
		if sp.Personal() {
			return store.Blocked(ctx, strings.TrimPrefix(sp.Owner(), "USER#"))
		}
		return store.OrgErased(ctx, sp.OrganizationID())
	}
}
```

`app.go`, in `BuildFinanceJobs`, chain `.WithSpaceGuard(services.ErasedSpace(erasure.NewStore(db, cfg.TablePrefix, 0)))` after the existing `.WithCards(...)` wiring (keep whatever builders are already chained).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -count=1 && make test-integration`
Expected: `ok` everywhere, including every existing `FinanceJobs` test (they build no guard).

- [ ] **Step 5: Commit**

```bash
git add internal/services internal/app/app.go
git commit -m "feat(api): the finance job skips spaces locked or erased by account deletion"
```

---

### Task 8b: BYOK and level reports leave an erased owner alone

Two more asynchronous writers act on an owner the saga may have erased: the BYOK job (one connection → statement sync, sweeps, certificate mail) and the level reports ctech-account and ctech-dfe send for `USER_{sub}` / `ORG_{id}` refs. The BYOK webhook receiver and the finance replay queue are already safe by construction (below).

**Safe by construction (pinned by tests, no code):**
- The BYOK webhook receiver finds its connection through the `HOOK#{id}` row the purge deletes, so an event for a purged workspace is an unknown hook: dropped.
- The finance replay queue (`schedule-index` of `invoices`) posts the payer side only for a non-anonymized customer (`payer_anonymized`), and the purge anonymizes before it deletes the space (Task 6 order).

**Files:**
- Modify: `api/internal/services/byok_job.go` (`runOne`), `api/internal/services/erasing.go` (`ErasedWorkspace`)
- Modify: `api/internal/api/v1/levels.go`, `api/internal/api/v1/router.go` (`Deps`)
- Test: `api/internal/services/byok_job_test.go`, `api/tests/integration/erasure_test.go`

**Interfaces:**
- Produces:
  - `(*ByokJob).WithSpaceGuard(gone func(ctx context.Context, sp space.ResolvedSpace) (bool, error)) *ByokJob`
  - `services.ErasedRef(store *erasure.Store) func(ctx context.Context, customerRef string) (bool, error)`: true for `USER_{sub}` when `Blocked(sub)` and for `ORG_{id}` when `OrgErased(id)`; false for any other ref
  - `v1.Deps.ErasedRef func(ctx context.Context, customerRef string) (bool, error)`

- [ ] **Step 1: Write the failing tests**

`byok_job_test.go` (reuse the file's existing fakes):

```go
func TestTheByokJobLeavesAnErasedWorkspaceAlone(t *testing.T) {
	steps := 0
	job := newTestByokJob(t, oneConnection("org_gone")).WithSpaceGuard(func(context.Context, space.ResolvedSpace) (bool, error) { return true, nil })
	job.Register(StepStatement, func(context.Context, space.ResolvedSpace, *byokconn.Connection, time.Time) error { steps++; return nil })
	job.Run(context.Background(), true, time.Now())
	if steps != 0 {
		t.Fatalf("a step ran %d times for an erased workspace", steps)
	}
}
```

`tests/integration/erasure_test.go`:

```go
// A level report for an erased ref is accepted and dropped, so the reporter
// stops retrying, and it writes nothing (R21).
func TestALevelReportForAnErasedRefWritesNothing(t *testing.T) {
	e := newPortal(t)
	lockSubject(t, e.userID) // the Task 3 helper: Store.Apply(lock) for the sub
	res := e.postLevel(t, "USER_"+e.userID, "finance_spaces", 3)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (accepted and dropped)", res.status)
	}
	if l, err := repositories.NewLevelRepository(testDB, testCfg).Latest(ctxT(t), e.org.ID, true, "USER_"+e.userID, "finance_spaces"); err == nil && l != nil {
		t.Fatalf("a level was written for an erased ref: %+v", l)
	}
}
```

`e.postLevel` is a small helper over the existing levels route test helper (`levels_test.go`).

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/services/ -run TestTheByokJobLeavesAnErasedWorkspaceAlone; go vet -tags integration ./tests/...`
Expected: FAIL, `WithSpaceGuard undefined` / `postLevel` undefined.

- [ ] **Step 3: Implement**

`byok_job.go`: add a `gone` field like `FinanceJobs`, `WithSpaceGuard` identical in shape, and in `runOne`, right after `sp, err := j.spaceFor(conn)` succeeds:

```go
	if j.gone != nil {
		gone, err := j.gone(ctx, sp)
		if err != nil {
			return fmt.Errorf("%s %s: erasure state: %w", conn.AccountOrganizationID, conn.Provider, err)
		}
		if gone {
			return nil
		}
	}
```

Wire it where the job is built (`cmd/byok`, `cmd/byok-outbound` via `app`): `.WithSpaceGuard(services.ErasedSpace(erasure.NewStore(db, cfg.TablePrefix, 0)))`.

`erasing.go`:

```go
// ErasedRef reports a level-report customer ref whose person or workspace the
// saga has locked or erased: USER_{sub} by sub, ORG_{id} by workspace id.
func ErasedRef(store *erasure.Store) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, ref string) (bool, error) {
		switch {
		case strings.HasPrefix(ref, "USER_"):
			return store.Blocked(ctx, strings.TrimPrefix(ref, "USER_"))
		case strings.HasPrefix(ref, repositories.OrgRefPrefix):
			return store.OrgErased(ctx, strings.TrimPrefix(ref, repositories.OrgRefPrefix))
		}
		return false, nil
	}
}
```

`levels.go`, in the handler right before `rec := &billing.LevelRecord{`:

```go
	if h.erasedRef != nil && t.OrganizationID == h.tenantZero {
		gone, err := h.erasedRef(c.Context(), req.CustomerRef)
		if err != nil {
			return fail(c, err) // unknown erasure state: never write
		}
		if gone {
			// Accepted and dropped: the reporter must not retry forever for a
			// person who no longer exists here.
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"recorded": false, "dropped": "account_erased"})
		}
	}
```

`router.go` `Deps` gains `ErasedRef`; `handlers` gains `erasedRef` and `tenantZero` from `Deps` (use the field the portal handlers already read for tenant zero); `app.go` sets `ErasedRef: services.ErasedRef(erasure.NewStore(db, cfg.TablePrefix, 0))`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/... -count=1 && make test-integration`
Expected: `ok`, and both new tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/services internal/api/v1 internal/app tests/integration/erasure_test.go
git commit -m "feat(api): BYOK jobs and level reports leave erased owners alone"
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
  - `app.BuildErasureConsumer(ctx context.Context, cfg *config.Config, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks services.BankConnections) (*erasure.Consumer, error)`
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
		c, err := BuildErasureConsumer(context.Background(), &cfg, nil, nil)
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
func BuildErasureConsumer(ctx context.Context, cfg *config.Config, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks services.BankConnections) (*erasure.Consumer, error) {
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
	eraser := newEraser(db, cfg).WithPurge(repositories.NewSpacePurger(db, cfg), spaceFor, banks)
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
	consumer, err := app.BuildErasureConsumer(shutdownCtx, cfg, space.ForJob, built.ByokConnections())
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

`built.ByokConnections()` is the `*services.ByokConnections` that `app.Build` already constructs for the console; expose it through an accessor on the Build result if it is not reachable from `cmd/server` yet. The consumer and the console then share one connection service, so a disconnect from the purge drops the same cached tokens the console would.

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
| Blockers: `billing.invoice_open` (a live tenant-zero invoice still owed, with `count`, `overdue` and `amount_cents`, and the portal's `/invoices` as `action_url`), `billing.invoice_uncollectible` (a written-off invoice still owed, with `count` and `amount_cents` and no `action_url`, because support records that payment), `billing.merchant_tenant` (a workspace being erased has a billing tenant; `detail.workspace_ids`) | `services.Eraser.Blockers` |
| Lock: every session token of a locked or erased `sub` gets 403 `/problems/account-locked`, on every route | `middleware.RefuseLocked`, inside `Verifier.Middleware` |
| Token cut-off: the jwtverify revocation list, read from Valkey **DB 0** (`VALKEY_URL` without its DB path) | `internal/app/erasure.go` |
| Purge: re-checks the blockers; cancels the tenant-zero subscriptions of the person and of every erased workspace now, clears their metadata, anonymizes those customers in place; deletes the level rows of `USER_{sub}` / `ORG_{id}`; disconnects bank connections; deletes the personal finance space and those of every workspace in `workspaces[]`, across every space-keyed table (ledger, bills, recurrences, cards, imports, byok, idempotency, audit) | `services.Eraser.Purge`, `repositories.SpacePurger` |
| Kept: invoices, lines, payment attempts, credit notes, invoice PDFs, the canceled subscriptions, audit rows (except a personal space's finance audit) | ADR 0009, ADR 0026 |
| Consumer: SQS `{env}-ctech-billing-erasure` (+ `-dlq`), run in the API process, acks to `{ACCOUNT_BASE_URL}/v1.0/internal/erasure/ack` with a **dedicated** client holding only `internal:account:erasure-ack` (`ERASURE_CLIENT_ID`/`SECRET`, or the SecureStrings `/ctech-billing/{env}/billing/erasure-client-{id,secret}`) | `app.BuildErasureConsumer`, `terraform/billing/erasure.tf` |
| State and tombstones: `{env}_billing_erasure_state` | `api-commons/erasure.Store` |

The daily finance job (recurrences, auto-settle, card close), the BYOK job and level reports skip any owner that is locked or erased (`services.ErasedSpace`, `ErasedRef`).
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
- [x] api-commons ≥ v1.19.0; `erasure_state` table; lock on every session route; revocation list (DB 0)
- [x] Eligibility route and blockers (`billing.invoice_open`, `billing.invoice_uncollectible`, `billing.merchant_tenant`), with the `workspaces` query parameter
- [x] Purge: subscriptions canceled, customers (person and erased workspaces) anonymized, personal and erased-workspace
      finance spaces deleted across every table (`SpacePurger`, every table classified by a test), level rows and bank
      connections removed; the finance, BYOK and level paths skip erased owners
- [x] Consumer in the API process; queue, DLQ, filtered subscription, alarm (`terraform/billing/erasure.tf`)
- [ ] Operator:
  - create billing's dedicated erasure client in ctech-account with only `internal:account:erasure-ack`;
  - write its id and secret to `/ctech-billing/{env}/billing/erasure-client-{id,secret}` (`aws ssm put-parameter --overwrite`);
  - confirm in each environment that `cmd/backfill-invoice-lookup` and `cmd/backfill-subscription-lookup` have run (R22: an unfilled customer index hides an open invoice);
  - check in dev that ctech-account's minted eligibility token has `iss` = billing's `CTECH_ISSUER_URL` and `aud` = `SERVICE_AUDIENCE`;
  - and add billing to
      ctech-account's `ERASURE_PARTICIPANTS`, **after** the legal validation of the data inventory
- [ ] Later: deleting a merchant tenant (credentials, webhook endpoints, customers, BYOK collection), which lifts `billing.merchant_tenant`;
      service-scoped unlink (`Store.Clear` on re-consent) once ctech-account builds it
```

- [ ] **Step 4: ADRs**
- `docs/adr/0026-finance-retention-by-purge.md`, append:

```markdown
## Amendment, 2026-10-07 — implemented

The trigger is ctech-account's `user.erase`. `repositories.SpacePurger.PurgeSpace` deletes every row
under the space key `S` in `ledger_accounts`, `ledger_transactions`, `bills`, `recurrences`, `cards`,
`imports`, `byok`, `idempotency` and `audit`, plus every `S#ACCOUNT#{id}`, `S#CARD#{id}` and
`S#IMPORT#{id}` partition and the `HOOK#{id}` rows the connections name. Audit rows go for every space
it purges, personal or workspace: the workspace itself is gone (decision 2026-10-13). It runs for
`USER#{sub}#live|test` and for every workspace in the message's `workspaces[]` (erased with their only
member). Child partitions go first, so a run that dies anywhere is finished by the next.
`TestEveryTableIsClassifiedForThePurge` is the "a test lists the tables" consequence: every table in
`schema.json` is either purged under the space or listed with the reason it is not. A workspace that
has a billing tenant blocks the deletion (`billing.merchant_tenant`) until a tenant deletion flow exists.
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
  - **Eligibility call:** adds `?workspaces=id1,id2` (R20) to `GET …/eligibility/:sub`, listing the workspaces where the person is the only member; its phase 3 plan (Task 3) and the saga protocol § 4.1 carry the change. Publishes `workspaces[{id,kind}]` on `user.locked`/`user.erase` (api-commons ≥ v1.19.0).
  - Its minted eligibility token's `iss` must equal billing's `CTECH_ISSUER_URL` (`/ctech-account/{env}/app-url`).
  - Its UI translates `billing.invoice_open`, `billing.invoice_uncollectible` and `billing.merchant_tenant` (a workspace with a billing account: contact support).
  - Its level worker (plan limits) skips a tombstoned owner; billing also drops reports for erased refs (Task 8b), so a race is harmless.
  - Gated on the legal validation of the matrix.
- **ctech-dfe:** the organization's DF-e subscription is an `ORG_{id}` customer in billing. Billing cancels it when the workspace is erased (R19); dfe's own purge erases its side (`account_billing` keyed by organization). Receives `subscription.canceled` through the existing routing (ADR 0016).
- **ctech-wallet:** none. The PIX rail and its refunds are wallet's participant work.
- **ctech-go-common:** `v1.19.0` carries `Message.Workspaces`. Its `CLAUDE.md` consumer list should say `ctech-billing/api (v1.19.0)`.
- **ctech-cdk / ctech-lbalancer:** none. The topic exists, and the route already serves `/v1.0/internal/*`.
- **Data inventory (ctech-account § 6):** carries the BYOK, cards/imports, organization-customer and level rows (added 2026-10-13); this plan implements them.
