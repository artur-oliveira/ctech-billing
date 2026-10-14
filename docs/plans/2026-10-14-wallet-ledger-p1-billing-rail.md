# Wallet ledger — P1: Billing becomes CTech's Inter rail — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Billing can collect CTech's own invoices on its own Inter connection (tenant zero on the BYOK rail), keeps the payer of every Pix on the payment attempt, pays a Pix out to a user's CPF, and exposes one-off invoice and payout routes to CTech services.

**Architecture:** `api-commons` gains an optional payer on the Pix callback and an optional payout capability on the Inter provider. Billing writes the payer onto the `PaymentAttempt` once (first write wins) with a computed `payer_match`, adds a `Payout` entity driven by the same provider session as refunds, and adds two M2M surfaces: `POST /v1.0/invoices` (a one-off invoice collected now) and `POST /v1.0/payouts`. Wallet is not touched in this phase.

**Tech Stack:** Go (Fiber v3, DynamoDB single table), `gopkg.aoctech.app/api-commons` (bumped to v1.23.0), the BYOK provider pattern, Inter Pix cobv and Banking APIs.

**Spec:** `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (§3, §4, §5, §6, §8 P1)

Repos: `P_COMMONS=/home/artur-revgas/Documents/Projects/Ctech/ctech-go-common` (the repo of the module `gopkg.aoctech.app/api-commons`; it is already at v1.22.0 (the opt-in raw Inter response trace, the full scope vocabulary, the 50-per-page callback listing), so this plan's release is the next free tag, `v1.23.0`), `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing` (Go in `P_BILL/api`). **Task 0 (live proof) comes first and needs no new code.** Tasks 0b–3 run in `P_COMMONS` and end with a tagged `v1.23.0`; Billing tasks (4 onward) start only after the tag exists.

## Global Constraints

- Routes in English, Portuguese only in i18n text (project memory). Scopes follow `billing:<resource>:<action>`; internal ones `internal:billing:<name>`.
- The Pix webhook is never the source of truth (spec §9). The payer taken from it is **evidence only**; settlement still follows `Confirm`'s re-query.
- Payer data is **write-once**: a later webhook never overwrites it (spec §6).
- Payout **destination is never client-supplied**: it is the Pix key of the CPF the caller names, resolved from the user's KYC record by the caller, and Billing refuses a key type other than CPF (spec §5).
- Every mutating route is idempotent (`Idempotency-Key`), and every cross-service call carries one.
- Constants for every string key, scope, header and attribute name; RFC 7807 problems via the existing `problem` helpers.
- Money in integer centavos (`billing.Cents`); Inter reais strings only inside `api-commons/inter`.
- No attribution trailers in commits or PR bodies; specs and plans live under `docs/specs|plans/`.
- Billing keeps working on the Wallet rail for every tenant until P2 flips CTech's own deposits; this phase only adds. The one exception is Task 0: tenant zero is switched to the BYOK rail for a R$ 0,10 proof; if that switch must be undone, set the rail back to `wallet`.

## Review Focus

- A credit line with no payer detail (an old line, or Inter omits it) leaves the payer empty and `payer_match = unknown`; it must not fail the sync or the confirmation. Pinned in Task 2 and Task 4.
- Two payments on one charge: the payer stored is the first payment's (matched by `endToEndId`), the second is an excess and never rewrites it. Pinned in Task 4.
- A document that is masked, partial or in another shape (CNPJ for a CPF customer) never reads as `match`: digits that differ are `mismatch`, too few digits are `unknown`. Pinned in Task 4.
- A payout retried with the same `Idempotency-Key` returns the original payout and never pays twice; a key reused with another amount or CPF is `409`. Pinned in Task 6.
- A payout whose Inter call fails or times out stays `processing` and is resolved by the sweep from Inter's status, never marked failed on a transport error. Pinned in Task 1 and Task 6.
- A one-off invoice for a customer with no tax ID is refused before any charge opens. Pinned in Task 5.

---

## Part 0 — Prove the live BYOK path for CTech (`P_BILL`, no code)

Billing already has the BYOK Inter integration (`api/cmd/byok-outbound` running `inter.Provider` from `api-commons`; Inter's URLs live in `api-commons/inter/client.go`). Nothing in this plan should be built on top of it before it is shown to work **live** for CTech's own organization (tenant zero), because until now test mode borrowed Wallet's keypair for the sandbox (ADR 0029, D9).

### Task 0: Connect tenant zero live and collect R$ 0,10

**Files:**
- Create: `docs/runbooks/tenant-zero-inter-rail.md` (the record of what you did; Task 8 extends it)

- [x] **Step 1: Create CTech's Inter app** — done 2026-10-14. Account number **with its check digit, digits only, no leading zeros** (Inter's reference for `x-conta-corrente`; without the digit `GET /saldo` answered `401 Login/senha inválido`). A **dedicated Pix key** (never the Wallet's: Inter keeps one notice address per key).
- [x] **Step 2: Connect it live** — done 2026-10-14. Token granted all seven scopes; balance and statement sync work. (Found on the way and fixed: a bare 500 on connect became `422 byok_bearer_refused`; every Inter response is now traced in the Lambda log with secrets redacted; webhook URLs keep only scheme and host in that trace.)
- [x] **Step 3: Switch tenant zero to the BYOK rail** — done 2026-10-14 by `artur` with the audited operator command (the Console refuses it by design, spec § 12.6): `/opt/app/job.sh byok -tenant-rail=byok -mode=live -operator=artur` on the Billing instance (via `aws ssm send-command`, profile `ctech`); `-dry-run` first. Way back: `-tenant-rail=wallet`. No subscription with value existed in production, so no customer was affected. The Pix notice was already registered on the dedicated key and points at `billing-byok-hooks.aoctech.app`.
- [ ] **Step 4: Live proof, after asking the owner** — needs a way to issue a R$ 0,10 invoice: the Console cannot create a one-off invoice, so this step runs **after Task 5** (the one-off invoice route). Then: pay it by Pix from a personal account and verify the invoice goes `paid` without anyone calling the API by hand (the webhook woke `Confirm`, which re-queried Inter), the statement sync shows the line, and the byok-webhook Lambda log has the callback body (save it, masked, for Task 0b).
- [ ] **Step 5:** If Step 4 fails, **stop**: the failure is in the existing BYOK path and is fixed in its own PR before Part B continues. Record the outcome and the date in the runbook and commit it.

## Part A — api-commons (`P_COMMONS`)

Branch: `feat/byok-payer-and-payout`.

### Task 0b: Capture the real payloads — DONE 2026-10-14

Spec §11's open fact is closed by the live proof (spec §6 has the table). Keep as fixtures, with the document and name replaced by fake values of the same shape: the **statement line** of the received Pix (`inter/testdata/extrato_pix_received.json`: `detalhes` with `nomePagador`, `cpfCnpjPagador`, `nomeEmpresaPagador`, `endToEndId`, `txId`) and the **paid cobv** (`inter/testdata/cobv_paid.json`: `pix[]` with an empty `infoPagador`). The webhook body was not captured (the Lambda logs none) and is not needed.

### Task 1: Payout capability

**Files:**
- Modify: `byok/types.go`, `byok/provider.go`, `inter/client.go` (scopes), `inter/provider.go`, `inter/banking_calls.go`
- Create: `inter/payout.go`
- Test: `inter/payout_test.go`, `byok/byoktest/` fake gains the payout methods

**Interfaces:**
- Produces:
```go
// byok
// CapabilityPayout and inter.ScopesPayout (pagamento-pix.read/write + webhook-banking.read/write) exist since
// api-commons v1.22.0, and Connect already reports the capability when the integration ticks them. This task
// adds the calls.
type PayoutRequest struct {
	Ref      string // caller's idempotency id, <= 35 [A-Za-z0-9], the bank dedupes on it
	Amount   int64  // centavos
	PixKey   string // CPF only; the caller has already matched it to the user
	Message  string
}
type PayoutStatus string // "processing" | "done" | "failed" | "unknown"
type Payout struct { Ref, PaymentID string; Status PayoutStatus }
var ErrPixKeyNotFound = errors.New("byok: destination key not registered")
// Optional interface: a provider that can pay out also implements it.
type Payouts interface {
	Payout(ctx context.Context, c Credentials, t Token, in PayoutRequest) (Payout, error)
	GetPayout(ctx context.Context, c Credentials, t Token, ref string) (Payout, error)
}
```

- [ ] **Step 1: Write failing tests** in `inter/payout_test.go` against the package's existing httptest harness (see `pix_calls_test.go` for how it fakes Inter): (a) `Payout` POSTs `/banking/v2/pix` with `valor` in reais, `destinatario{tipo:"CHAVE",chave}` and the `x-id-idempotente` header equal to `Ref`; (b) a 404 maps to `byok.ErrPixKeyNotFound`; (c) `GetPayout` GETs `/banking/v2/pix/{Ref}` and maps Inter's `EFETIVADO` to `done`; (d) a transport error returns a plain error (never `failed`).
- [ ] **Step 2: Run** `go test ./inter/ -run Payout -v` — expect FAIL.
- [ ] **Step 3: Implement** by porting `ctech-wallet/pix-gateway/internal/inter/inter.go:235-275` (`Transfer`, `QueryTransfer`) onto `Client.Do`, the idempotency header the wallet gateway uses in `doIdem`, and `Reais()` for the amount. `Provider` implements `byok.Payouts`; `Token(ctx, c, CapabilityPayout)` already requests `ScopesPayout`. `Connect` (v1.23.0) already asks for every product at once and steps down on refusal, so only tenant zero's connection reports `CapabilityPayout`; no credentials flag is needed. A payout call on a connection without the capability fails with `ErrScopesRefused` from Inter; the Billing service checks `conn.Has(byok.CapabilityPayout)` first.
- [ ] **Step 4: Run** `go test ./...` — expect PASS.
- [ ] **Step 5: Commit** `feat(byok): optional payout capability, implemented for Inter`.

### Task 2: `Payer` on the statement line

**Files:**
- Modify: `byok/types.go` (`StatementLine`), `inter/banking_calls.go` (`ExtratoLine.Detalhes`), `inter/provider.go` (`StatementPage`)
- Test: `inter/provider_test.go` (or `banking_calls_test.go`)

**Interfaces:**
- Produces:
```go
// byok
type Payer struct {
	Name        string `json:"name,omitempty"`
	Document    string `json:"document,omitempty"`    // CPF or CNPJ as the bank gives it
	Institution string `json:"institution,omitempty"` // the payer's bank
}
type StatementLine struct { /* existing fields */ Payer *Payer `json:"payer,omitempty"` } // credit lines only, nil when the bank sends none
```

- [ ] **Step 1: Write the failing test** with the Task 0b fixture:

```go
func TestAReceivedPixLineCarriesItsPayer(t *testing.T) {
	c, cr, _ := recorder(t, `{"totalPaginas":1,"transacoes":[{"idTransacao":"T1","dataTransacao":"2026-10-14","tipoOperacao":"C","valor":"0.10","titulo":"Pix recebido","descricao":"Fulano de Tal","detalhes":{"txId":"ctb01JABCDEFGHJKMNPQRSTVWXYZ","endToEndId":"E1","nomePagador":"Fulano de Tal","cpfCnpjPagador":"12345678909","nomeEmpresaPagador":"BANCO EXEMPLO"}}]}`, 200)
	page, err := NewProvider(c).StatementPage(context.Background(), byok.Credentials{Values: credValues(cr)}, byok.Token{Value: "t"}, day("2026-10-14"), day("2026-10-14"), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := &byok.Payer{Name: "Fulano de Tal", Document: "12345678909", Institution: "BANCO EXEMPLO"}
	if got := page.Lines[0].Payer; got == nil || *got != *want {
		t.Fatalf("payer = %+v, want %+v", got, want)
	}
}

func TestADebitLineHasNoPayer(t *testing.T) { /* the same shape with tipoOperacao "D": Payer must be nil: the "payer" there is CTech itself */ }
func TestALineWithoutPayerDetailsHasNoPayer(t *testing.T) { /* detalhes without the three fields: Payer nil, the line is still returned */ }
```
  (`credValues` and `day` are two small helpers in the test file: copy the credential map and `byok.ParseDate` use from `TestAStatementLineWithAnUnreadableAmountIsSkippedNotPostedAsZero`.)
- [ ] **Step 2: Run** `go test ./inter/ -run Payer -v` — expect FAIL (`Payer` undefined).
- [ ] **Step 3: Implement.** Add `NomePagador`, `CPFCNPJPagador`, `NomeEmpresaPagador` (`json:"nomePagador"`, `cpfCnpjPagador`, `nomeEmpresaPagador`) to `ExtratoLine.Detalhes`; in `StatementPage`, for credit lines, set `Payer` when name or document is non-empty. Never for debits: on a Pix sent by CTech the "pagador" is CTech itself.
- [ ] **Step 4: Run** `go test ./...` — PASS. **Step 5: Commit** `feat(byok): the payer of a received Pix on its statement line`.

### Task 3: Tag

- [ ] **Step 1:** `go vet ./... && go test ./...`; update `README.md`/`doc.go` for the two additions.
- [ ] **Step 2:** Merge, then tag `v1.23.0` and push the tag. Verify `go list -m gopkg.aoctech.app/api-commons@v1.23.0` resolves.

---

## Part B — ctech-billing (`P_BILL/api`)

Branch: `feat/wallet-ledger-p1`. First step of Task 4: `go get gopkg.aoctech.app/api-commons@v1.23.0 && go mod tidy`.

### Task 4: Record the payer on the attempt, write-once, with `payer_match`

**Files:**
- Modify: `internal/domain/billing/payment.go` (`PaymentAttempt`), `internal/repositories/payments_byok.go`, `internal/repositories` (`BankLine` gains the payer), `internal/services/byok_statement.go` (map the line), `internal/services/byok_linker.go` (`Link`), `internal/services/byok_confirm.go` (the on-demand pull), `internal/api/v1/dto.go` (`invoiceResponse`)
- Create: `internal/domain/billing/payer.go`, `internal/domain/billing/payer_test.go`
- Test: `internal/services/byok_linker_payer_test.go`, a repository integration test beside the existing ones

**Interfaces:**
- Produces:
```go
// billing
type PayerMatch string
const ( PayerMatchYes PayerMatch = "match"; PayerMatchNo PayerMatch = "mismatch"; PayerMatchUnknown PayerMatch = "unknown" )
type Payer struct{ Name, Document, Institution string }
func MatchPayer(customerTaxID string, p Payer) PayerMatch
// PaymentAttempt gains:
//   Payer      *Payer     `dynamodbav:"payer,omitempty" json:"payer,omitempty"`
//   PayerMatch PayerMatch `dynamodbav:"payer_match,omitempty" json:"payer_match,omitempty"`
// repositories
func (r *PaymentRepository) SetPayerOnce(ctx context.Context, a *billing.PaymentAttempt, p billing.Payer, m billing.PayerMatch, now time.Time) (bool, error) // true when this call wrote it
```

- [ ] **Step 1: Failing domain test** (`payer_test.go`):

```go
func TestMatchPayer(t *testing.T) {
	cases := []struct {
		name, tax string
		p         Payer
		want      PayerMatch
	}{
		{"cpf equal", "12345678909", Payer{Document: "12345678909"}, PayerMatchYes},
		{"cpf equal with punctuation", "123.456.789-09", Payer{Document: "12345678909"}, PayerMatchYes},
		{"cpf different", "12345678909", Payer{Document: "98765432100"}, PayerMatchNo},
		{"cnpj payer, cpf customer", "12345678909", Payer{Document: "12345678000195"}, PayerMatchNo},
		{"masked, revealed digits equal", "12345678909", Payer{Document: "***456789**"}, PayerMatchYes},
		{"masked, a revealed digit differs", "12345678909", Payer{Document: "***456780**"}, PayerMatchNo},
		{"nothing revealed", "12345678909", Payer{Document: "***********"}, PayerMatchUnknown},
		{"no payer", "12345678909", Payer{}, PayerMatchUnknown},
		{"customer has no tax id", "", Payer{Document: "12345678909"}, PayerMatchUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchPayer(tc.tax, tc.p); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
```
- [ ] **Step 2: Run** `go test ./internal/domain/billing -run MatchPayer` — FAIL. **Step 3:** implement `payer.go`: digits-only normalisation of both sides; a document with no `*` compares whole; with `*` compare only the revealed positions and answer `unknown` when fewer than 3 digits are revealed. **Step 4:** PASS.
- [ ] **Step 5: Failing repository test** (follow the neighbouring `payments_byok` integration tests): `SetPayerOnce` returns `true` on the first call and `false` on the second with a different payer, and the stored payer is the first. Implement with condition `attribute_not_exists(payer)`; `ConditionalCheckFailed` is `(false, nil)`.
- [ ] **Step 6: Failing service test** (`byok_linker_payer_test.go`, helpers in `byok_testhelpers_test.go`): linking a credit line that carries a payer to its attempt stores the payer and `payer_match` against the customer's tax ID; a second line for the same attempt with another payer changes nothing; a line without a payer leaves `Payer == nil` and `PayerMatch == "unknown"`, and the link still succeeds.
- [ ] **Step 7: Implement.** Carry `Payer` through `byok_statement.go` into `repositories.BankLine`; in `Link`, after the attempt is found (and only for the line whose `PaymentID` matches the attempt's, or the first when none is recorded), call `SetPayerOnce`. A failure to store is logged and never blocks the link.
- [ ] **Step 8: The on-demand pull.** Failing test: after `Confirm` settles an attempt, one statement page for today is requested for that connection (a fake provider counts calls) and the payer is on the attempt without waiting for the hourly sync; a rate-limited or failing pull leaves the confirmation untouched. Implement by calling the existing statement sync entry point for a one-day window under its per-connection lock (`byok_statement.go`); never inside the confirmation's critical section, and never more than once a minute per connection.
- [ ] **Step 9:** add `payer` and `payer_match` to the M2M and console `invoiceResponse` (from the paid attempt). The document is returned as received; the console shows it masked.
- [ ] **Step 10:** `go test ./... && go test -tags integration ./tests/integration/` PASS. **Commit** `feat(billing): the payer of a Pix on its attempt, write-once, with payer_match`.

### Task 5: One-off invoice route

Wallet and Poker both need "invoice this user for X, give me the Pix code". Subscriptions are the wrong shape.

**Files:**
- Modify: `internal/middleware/scope.go` (reuse `ScopeInvoicesWrite`), `internal/api/v1/router.go`, `internal/api/v1/dto.go`
- Create: `internal/api/v1/oneoff_invoices.go`, `internal/services/oneoff_invoices.go`, `internal/services/oneoff_invoices_test.go`

**Interfaces:**
- Produces:
```
POST /v1.0/invoices     scope billing:invoices:write, Idempotency-Key required
Body:  { customer_id, description, amount_cents, metadata{...}, reference }
201 -> { id, status, amount_cents, pix_copia_e_cola, qr_code_base64, expires_at, payable_until }
GET  /v1.0/invoices/:id  (existing) gains payer, payer_match
GET  /v1.0/invoices/by-reference/:reference   scope billing:invoices:read -> same body as GET /invoices/:id plus the pix fields
service: OneOff.Create(ctx, tenant, in OneOffInput) (*billing.Invoice, *billing.CheckoutSession, error)
```

`reference` is the caller's own opaque label (Wallet sends its deposit id, Poker its purchase id). It is unique per calling credential: a lookup item `REF#<org>#<credential>#<reference>` → invoice id is written in the same transaction as the invoice (`attribute_not_exists`), which is also what makes `Idempotency-Key` replays safe. Route `by-reference` is registered **before** `/:id` so `"by-reference"` is never read as an invoice id (same ordering care as wallet's `/skus`).

- [ ] **Step 1: Failing service test:** creating with the same idempotency key twice returns the same invoice id; a customer with no tax ID returns `ErrNoTaxID` before any charge opens; `amount_cents <= 0` is invalid; the invoice carries `OwnerKey` from the calling credential so the existing webhook routing sends `invoice.paid` only to that product owner's endpoint (read `internal/domain/billing/webhook.go` for how `OwnerKey` is chosen and reuse it; do not invent a new routing field).
- [ ] **Step 1b: Failing test for the lookup:** `GetByReference(ctx, tenant, credentialID, ref)` returns the invoice created with that reference; another credential's same reference returns `ErrNotFound`; a second `Create` with the same reference and a **different** amount or customer returns `ErrIdempotencyConflict`.
- [ ] **Step 2: Implement** `OneOff.Create` by composing existing pieces: build a one-line `billing.Invoice` (no `SubscriptionID`), `Invoicer.Issue` (`internal/services/invoicing.go:338`, finalize + dunning arm), then the collector's `Checkout(ctx, org, inv, ...)` (`byok_collector.go:270`) which returns a `CheckoutSession` carrying `PixCode`. For a tenant on the BYOK rail this opens the cobv through the tenant's Inter connection.
- [ ] **Step 3: Route + handler** in the `m2m("/invoices")` group beside the existing GETs, behind `idem`. Problems: `422 no_tax_id`, `409 idempotency_conflict`.
- [ ] **Step 4:** tests PASS; **Step 5: Commit** `feat(billing): one-off invoice route for CTech services`.

### Task 5b: Refund a paid invoice to its payer (M2M)

Wallet refunds a quarantined or reversed deposit by asking Billing to give the money back. Billing already does this for the console as a credit note with "devolver via Pix" (`ByokCollector.RefundCredit`, `byok_refunds.go:101`); this task only exposes it to a service client.

**Files:**
- Modify: `internal/api/v1/router.go`, `internal/api/v1/console.go` (extract the shared body of `creditInvoice` if it is not already callable without the user session; read it first)
- Create: `internal/api/v1/m2m_credit_notes.go`, test beside it

**Interfaces:**
- Produces:
```
POST /v1.0/invoices/:id/credit-notes   scope billing:invoices:write, Idempotency-Key required
Body: { amount_cents, reason, refund_to_payer: true }
201 -> { id, amount_cents, refunded_externally, external_refund_ref }
409 refund_window_closed  (> billing.RefundWindow after the payment)
```

- [ ] **Step 1: Failing test:** with a paid BYOK invoice, the route issues a credit note and calls the provider `Refund` for the attempt's `PaymentID` exactly once on a replayed key; an amount above what was paid → 422; a payment older than `billing.RefundWindow` → 409.
- [ ] **Step 2: Implement** by calling the same service the console handler calls (`CheckRefundCredit` first, then create the note and `RefundCredit`), with `actor = "service:" + credential id`. Do not add a second refund implementation.
- [ ] **Step 3:** PASS; **Step 4: Commit** `feat(billing): credit-note route with refund to the payer for service clients`.

### Task 6: Payout entity and service

**Files:**
- Create: `internal/domain/billing/payout.go`, `internal/repositories/payouts.go`, `internal/services/payouts.go`, tests beside each
- Modify: `internal/repositories` key helpers (follow `payments_byok.go` for PK/SK shape)

**Interfaces:**
- Produces:
```go
type PayoutStatus string // "processing" | "done" | "failed"
type Payout struct {
	ID, OrganizationID string; Livemode bool
	IdempotencyKey, RequestHash string // hash of (amount, cpf, customer)
	Amount Cents; CPF string; Message string
	Status PayoutStatus; PaymentID, FailureReason string
	CreatedAt, UpdatedAt time.Time
}
func (s *Payouts) Create(ctx, tenant, in PayoutInput) (*billing.Payout, error)  // idempotent
func (s *Payouts) Get(ctx, tenant, id string) (*billing.Payout, error)
func (s *Payouts) Resolve(ctx, p *billing.Payout, now time.Time) error // sweep entry
```

- [ ] **Step 1: Failing tests:** replay with the same key and body returns the first payout and the fake provider's `Payout` is called once; same key, different amount → `ErrIdempotencyConflict`; a provider `ErrPixKeyNotFound` → payout `failed` with reason `key_not_found` (the only error that is terminal on the spot); any other provider error leaves `processing`; `Create` refuses a tenant whose connection lacks `CapabilityPayout`.
- [ ] **Step 2: Implement.** Reserve the row (`attribute_not_exists`) **before** calling the provider, with `Ref = "po" + id` (alphanumeric, ≤35); call `byokrpc.Call(ctx, sess, byok.CapabilityPayout, ...)` exactly as `confirmAttempt` does for collection; type-assert the provider to `byok.Payouts`.
- [ ] **Step 3:** `Resolve` calls `GetPayout`; `done` → `done`; provider says it does not exist after the key's reservation is older than 10 minutes → `failed`; otherwise unchanged.
- [ ] **Step 4:** PASS; **Step 5: Commit** `feat(billing): payouts to a CPF key through the tenant's Inter connection`.

### Task 7: Payout routes, scope and sweep

**Files:**
- Modify: `internal/middleware/scope.go` (+ `AllScopes`, and the scope-manifest/`oauthresource` test that pins it), `internal/api/v1/router.go`, `cmd/sweep/main.go`
- Create: `internal/api/v1/payouts.go`, `internal/api/v1/payouts_test.go`

- [ ] **Step 1:** Add `ScopePayoutsWrite = "billing:payouts:write"` and `ScopePayoutsRead = "billing:payouts:read"`; add to `AllScopes`; run the manifest test and let it tell you which other file pins the list (`internal/oauthresource`).
- [ ] **Step 2: Failing handler tests:** `POST /v1.0/payouts` without the scope → 403; without `Idempotency-Key` → 400; valid → 202 with `status: processing|done`; `GET /v1.0/payouts/:id` for another tenant's payout → 404.
- [ ] **Step 3: Implement** the routes in the `m2m("/payouts")` group behind `idem`; body `{ amount_cents, cpf, message, reference }`. The sweep job calls `Payouts.Resolve` for every `processing` payout older than one minute (mirror `byok_sweep.go`'s paging).
- [ ] **Step 3b: Lookup by the caller's key.** Add `GET /v1.0/payouts/by-reference/:reference` (scope `billing:payouts:read`, registered before `/:id`). The payout's `reference` is the caller's own idempotency id (Wallet sends its withdrawal's idempotency key); it is unique per credential and lets Wallet's reconcile ask "did this payout go through?" without having stored Billing's id. Failing test first: another credential's reference → 404.
- [ ] **Step 4:** PASS; **Step 5: Commit** `feat(billing): payout routes and sweep`.

### Task 8: Register the consumers' webhooks (runbook, not code)

The connection and the rail switch for tenant zero were done and proven live in Task 0. This task only wires the consumers.

**Files:**
- Create: `docs/runbooks/tenant-zero-inter-rail.md` (Task 0's steps plus these)

- [ ] **Step 1:** Extend the runbook: (1) the Task 0 steps as the record of how tenant zero was connected; (2) turn the connection's `payout` flag on (Task 1 added it) and reconnect so `Connected.Capabilities` shows `payout`; (3) register an `invoice.paid` webhook endpoint for the Wallet consumer and another for Poker, each with an `OwnerKey` (reuse the console's webhook screen); (4) rollback = switch the rail back to `wallet`.
- [ ] **Step 2:** Do (2) in sandbox first (Inter sandbox simulated payment), then ask the owner before any live payout; this phase moves no live traffic (P2 does).
- [ ] **Step 3: Commit** the runbook.

### Task 9: End-to-end check

- [ ] **Step 1:** In `tests/integration/` (see `byok_notify_test.go`): create a one-off invoice, `SimulatePayment`, notify with a payer, assert invoice `paid`, `payer_match`, and a payout to the same CPF reaching `done`.
- [ ] **Step 2:** `go test ./... && go vet ./...`; the UI is unchanged. Open the PR; merge per the repo's autonomy rule and report that no infra changed and no cost was added (one more DynamoDB item type; the sweep gains one scan).

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| §6 payer on the invoice, write-once, `payer_match`, recorded within a minute | 2, 4 |
| §6 open fact: where the payer comes from | 0b (closed: the statement line) |
| §5 payout through Billing, CPF only | 1, 6, 7 |
| §4 Billing issues the deposit invoice; lookup by the caller's reference | 5 |
| §4 refund the payer of a quarantined deposit | 5b |
| §3 tenant zero on the BYOK rail, proven live first | 0 |
| §3 `invoice.paid` consumers registered | 8 |
| §9 idempotency on every call, webhook not truth | 4, 6, 7 |
| §8 P1 shippable alone, old path untouched | 8 (no live traffic beyond Task 0's R$ 0,10) |
