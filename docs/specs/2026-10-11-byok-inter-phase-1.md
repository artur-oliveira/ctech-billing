# Spec — BYOK, phase 1 (provider: Inter): the connection, statement sync and Pix with due date

Status: **Draft for review** · 2026-10-11 (revised the same day with the owner's review decisions) ·
Factual basis: [BYOK Inter parecer](../analysis/2026-10-11-byok-inter-parecer.md) (cited below as "parecer § n") ·
Builds on: [`2026-10-07-finance-erp-design.md`](2026-10-07-finance-erp-design.md) (§ 1 tiers, § 3 accounts and
posting, § 3.8 billing integration, § 12 item 2), [`2026-10-10-console-self-service.md`](2026-10-10-console-self-service.md)
(merchant tenants, applications, `payout_status`, credentials, `prod_billing` approval),
[`2026-10-10-plans-design.md`](2026-10-10-plans-design.md) (level metering), [ADR 0003](../adr/0003-tenant-and-livemode-partition-key.md)
(mode isolation), [ADR 0004](../adr/0004-pix-on-invoice-via-wallet.md) (the wallet rail),
[ADR 0005](../adr/0005-payout-gate.md) (payout gate), [ADR 0017](../adr/0017-field-level-encryption.md) (field
encryption), [ADR 0024](../adr/0024-billing-keeps-a-management-ledger.md) (management ledger),
[ADR 0025](../adr/0025-spaces-personal-and-organization.md) (spaces) ·
Reused from ctech-wallet: `pix-gateway` (outbound Lambda, mTLS webhook stack), the Inter token manager spec ·
Decision records: **ADR 0029** (this spec's decisions, written when the spec is approved). ADR 0030 (legal clearance)
is no longer planned: the legal gate was withdrawn on 2026-10-11 (amendment at the end)

## 1. What this is

**BYOK** ("bring your own key"): an organization connects **its own bank or PSP account** to CTech, and CTech uses
that account to collect the organization's invoices and to read its statement. The money never passes through CTech.
BYOK is **provider-agnostic**: a connection has a `provider`, and **Inter is the first provider**, not the only one.
Everything named "Inter" below is Inter's implementation of the provider seam (§ 4.4); everything else is BYOK's.

An organization that holds an **Inter Empresas (PJ) account** connects its own Inter API integration. With that
connection:

1. **The connection.** The merchant's credentials (for Inter: client_id, client_secret and the mTLS certificate +
   key), sealed so that only the outbound Lambda can open them; a per-connection token cache that respects Inter's
   5 tokens/minute limit and 60-minute tokens; an IPv4 outbound path (billing hosts have no public IPv4, Inter has no
   IPv6); for Inter, the annual certificate renewal with warnings at 90/30/7 days; health and disconnect.
2. **Statement and balance sync into Finanças.** The organization's Finanças space gains a ledger account of a new
   kind, **BYOK** (shown with the provider's name, "Inter"), whose statement is synced from the provider and whose
   balance is the provider's.
3. **A due-date charge to collect billing invoices** — for Inter, **Pix with due date (`cobv`)**. The payer pays the
   merchant's account directly; billing marks the invoice paid after re-querying the provider.

The connection belongs to the **ctech-account organization** (Inter's APIs are PJ-only, parecer § 2.1). One
connection per organization, mode and provider serves both that organization's billing tenant (collection) and its
Finanças space (statement). Personal spaces do not connect in phase 1.

What it is **not**:
- **Not the wallet.** BYOK never creates a wallet, a wallet balance or a wallet ledger entry (D2).
- Not Boleto com Pix / Cobrança v3 (phase 2) and not Pix Automático for subscriptions (phase 3). § 13 names the seams
  this phase leaves for them.
- Not a second provider yet: the seam is defined (§ 4.4), Inter is the only implementation in phase 1.
- Not `lotecobv`, not any payment scope (`pagamento-*`).
- Not a change to tenant zero's default rail: CTech's own invoices keep the wallet rail (ADR 0004) unless the owner
  moves it during the pilot (§ 12.6).
- Not the pricing engine's new pieces: the plan table is decided (§ 12.5), but the Sob demanda automatic cap is a new
  invoicing rule deferred to the pricing plan.
- Not the wallet's migration to the shared Inter package (separate work).

## 2. Decisions

The owner's decisions are D1–D13 and the review decisions R1–R6 (2026-10-11); D14 onwards are this spec's, each
argued where it is specified.

| # | Decision |
|---|---|
| D1 | Phase 1 = the connection + statement/balance sync into Finanças + due-date charge (Inter `cobv`) collection of billing invoices. Phases 2 (Cobrança v3) and 3 (Pix Automático) are out, with named seams (§ 13). |
| D2 | **BYOK never touches the CTech Wallet.** No wallet, no wallet balance, no wallet ledger entry for BYOK money. Transaction control lives in billing (charges and their status per invoice) and Finanças (the bank account with synced statement, balance from the provider). A wallet would be a second balance that diverges from the bank, and would look like custody. |
| D3 | The connection belongs to an **ctech-account organization**; it serves the organization's billing tenant and its Finanças space. Personal spaces do not connect. |
| D4 | Credentials are stored encrypted in ADR 0017's format (AES-256-GCM, version prefix), under a dedicated key only the outbound Lambda holds (R1, § 3.3). |
| D5 | Per-connection token cache (Inter: 5 tokens/min, 60-minute tokens); IPv4 egress through a billing-owned **Lambda** modelled on the wallet's `pix-gateway` — confirmed as the cheap path (R2). |
| D6 | Inter: annual certificate renewal flow with warnings at **D-90, D-30, D-7**. |
| D7 | Disconnect deletes CTech's webhooks at the provider, then the credentials, and tells the merchant that **only they** can cancel the integration at the provider. |
| D8 | Webhooks: **one CTech host** for every merchant; per-connection opaque secret **in the URL path**; account check (Inter: `x-conta-corrente`); **re-query the provider before marking anything paid**; DNS-only (not proxied by Cloudflare); mTLS truststore holding the providers' CA(s); product paths handled (§ 6). Recovery through the provider's callbacks/resend endpoints; a reconciliation sweep is the safety net. |
| D9 | **Test mode** uses one CTech-owned integration per provider for all tenants, against the provider's sandbox host. For Inter this is **the integration CTech already has (the wallet's)**: Inter has no separate sandbox kind of integration, the same credentials are used and only the base URL changes (amended 2026-10-11, § 9). Live mode uses each merchant's integration. |
| D10 | Shared code: a new **`ctech-go-common/inter`** package (credentials passed per call) implementing the provider seam declared in **`ctech-go-common/byok`** (R3). Billing and wallet keep separate Lambdas and secrets. |
| D11 | **Withdrawn 2026-10-11 (amendment at the end): there is no legal gate.** Original text: **Legal gate:** live-mode BYOK collection cannot be enabled until the owner records the lawyer's clearance: `BYOK_LIVE_COLLECTION` defaults off in live, `BYOK_LIVE_PROVIDERS` lists the cleared providers, plus ADR 0030 (R3). Read-only statement sync may go live without it (§ 10 agrees, with conditions). |
| D12 | `payout_status` (ADR 0005) stays the per-merchant gate; BYOK is a **new rail** `payout_rail: byok` with `payout_provider: inter`, enabled per connection rather than by CTech custody (R3). The existing gate and its single function are kept. |
| D13 | Onboarding guides the merchant through creating the integration (Inter: scopes, "Em validação", upload), then shows health and certificate expiry, in the console and in Finanças. Routes in English, Portuguese only in i18n. |
| R1 | **No KMS per call.** A dedicated AES-256 key for bank credentials in an SSM SecureString, read **once per Lambda cold start**, readable only by the outbound Lambda; the `api` never holds it. Credentials reach the Lambda through a sealing op (`Connect`, which tests and seals; `Reseal` on renewal); the Lambda encrypts and returns the ciphertext the `api` stores (§ 3.3). Per-merchant KMS keys are a future option when cost allows; not now. |
| R3 | **Provider-agnostic:** connections, the rail, the Finanças account, flags and webhook paths carry a `provider`; Inter specifics stay in Inter-named sections. |
| R4 | **Pricing:** BYOK collection is sold through `prod_billing` plans priced by **one unit, "cobrança emitida"** (a charge cancelled within 24 h does not count): Grátis, Sob demanda, Básico, Crescimento, Pro, Empresas (§ 12.5 table). No per-subscription price and no separate subscription quota; active subscriptions are capped at the plan's charge quota; Pix Automático counts as a normal charge. Overage is charged automatically and never blocks, except Grátis. Upsell is by features (bank connections, API access, users). The early access price stays R$ 0. |
| R5 | **Pilot:** CTech's own CNPJ (account organization `01a04ed6-1af9-745e-bcf7-d3b66fe52321`) on a new internal unlimited price, `price_billing_unlimited_internal_monthly` (§ 12.6). |
| R6 | **Webhook mTLS uses no merchant certificate:** the provider presents its own client certificate, validated by CTech's truststore; CTech presents its own server certificate (§ 6.1). |
| D14 | The `api` owns the token lifecycle (single-flight in Valkey, sealed token), as the wallet's token manager does; the Lambda performs the token fetch (§ 4.3). |
| D15 | The webhook URL is `/w/{provider}/{hook_id}/{secret}/{product}`; the receiver accepts both the registered path and that path with the provider's documented suffix; the payload parser is chosen by the registered provider and product, never by the suffix (§ 6.2). |
| D16 | Connecting for **statement sync** is not gated by a plan in phase 1; **collection** is entitled by the `prod_billing` subscription that already approves a live merchant (R4). |
| D17 | A due-date charge is created when the invoice is **finalized** (and on demand at checkout if that failed), one per payment attempt, with a deterministic reference (Inter: txid). |
| D18 | No CTech per-charge ceiling on the BYOK rail; ADR 0004's ceiling stays on the wallet rail only. |
| D19 | **Excess and duplicate payments are never refunded automatically**: the money is the merchant's; the console flags it and the merchant decides (answers parecer § 11, owner question 3). |
| D20 | The BYOK account in Finanças is **statement-driven**: every synced line posts automatically; unclassified lines sit on two system categories until the person classifies them; manual cash movements into the account are refused. Its opening balance is the provider's balance on the day before the first synced day. |
| D21 | Inter statement sync uses **page pagination, never scroll**: Inter allows one active scroll per account, and the merchant's own ERP may be using it. |
| D22 | In test mode the shared sandbox statement is **filtered to lines carrying the tenant's own charge references**; no other line enters a test space. |
| D23 | Inter: the merchant is asked to tick the **full phase-1 scope list** at integration creation; CTech uses only what the gates allow. A statement-only integration (`extrato.read`) is accepted too. |
| D24 | Webhook conflict default: `GET` before `PUT`; a webhook URL that is not CTech's is **never overwritten without the merchant's explicit confirmation**; a daily check detects being overwritten later. |

## 3. The connection

### 3.1 Ownership and keys

- One connection per **(ctech-account organization, mode, provider)**. Its partition key is the organization's
  Finanças space key, `{account_organization_id}#{mode}` (finance spec § 2), sort key `CONNECTION#{provider}`, so the
  resolver that guards Finanças guards the connection (ADR 0025: zero reads before membership, one 404).
- A billing tenant collects through **one** provider at a time (`payout_provider`, § 7.1); an organization may hold
  connections to several providers for statement sync once more providers exist.
- Only `kind = organization` workspaces connect. `personal` and personal workspaces (ADR 0027) answer `404`, the
  same body as an unknown space.
- Connect, renew, re-register the webhook, change collection settings and disconnect need `owner` or `admin`
  (`finance.configure`). Members and viewers see the BYOK account in Finanças like any account, not the connection
  screen.
- The billing tenant is found from the organization by the existing lookup `{mode}#ACCOUNT_ORG#{id}` (console spec
  § 3). An organization with **no billing tenant** may connect for statement sync alone; collection additionally
  needs the tenant (and, in live, an approved one).

### 3.2 What the merchant gives (Inter)

| Field | Notes |
|---|---|
| `client_id`, `client_secret` | shown once by Inter at download (parecer § 2.1) |
| certificate (`.crt`) and private key (`.key`) | PEM; one pair per integration, valid 1 year (parecer § 2.2). Used **only on outbound calls** to Inter, never on webhooks (§ 6.1) |
| `account_number` | the conta corrente, digits with check digit, no leading zeros; sent as `x-conta-corrente` when the integration has several accounts, and compared on every webhook (§ 6.3) |
| `pix_key` | collection only: the key `cobv` charges are created on; the console recommends a **dedicated random (EVP) key** because Inter's Pix webhook is one per key (parecer § 6.5) |
| `backfill_months` | statement history to import, 3 (default) to 12 (§ 8.5) |

Each provider declares its own credential fields; the connection row keeps them only inside the sealed bundle.

Validation at upload, before anything is stored:
1. In the `api`, without secrets being kept: PEM parses; the key matches the certificate; `notAfter` is in the future
   (an expired pair is refused with `byok_certificate_expired` and a renewal hint).
2. **Connect** through the Lambda (§ 3.3): a token for the phase-1 scope set; if Inter refuses that set, a token for
   `extrato.read` alone (statement-only integration, D23); then `GET /saldo` (proves `extrato.read` and the account
   number).
3. The granted scopes become the connection's **capabilities**, which are provider-neutral: `statement` (Inter:
   `extrato.read`), `collection` (Inter: `cobv.read`, `cobv.write`, `pix.read`, `pix.write`, `webhook.read`,
   `webhook.write`, all present). Anything less than the collection set means `statement` only, and the console says
   which scopes are missing.

The merchant also confirms, in the form, that the integration is their own and that they authorize CTech to use it
on their behalf (stored as `authorized_by`, `authorized_at`, and the wording's version). § 10 explains why this is
required even for the read-only path.

### 3.3 Storage (D4, R1)

- **The key.** One AES-256 key for bank credentials per environment, in the SSM SecureString
  `/ctech-billing/{env}/byok/credentials-key`. Only the outbound Lambda's role may read it; the Lambda reads it **once
  at cold start** and keeps it in memory. There is no per-call KMS or SSM cost: the only decryption of the parameter is
  SSM's own, once per cold container. The `api` EC2 role, the jobs and the webhook Lambda have no access to it.
- **The format** is ADR 0017's: AES-256-GCM, random nonce, a version prefix (`b1.` + base64, distinct from the field
  sealer's `v1.`), no development fallback key (the Lambda refuses to start without the parameter), `Open` refuses an
  unprefixed value, and an integration test reads the raw DynamoDB item to prove it is not plaintext. The additional
  authenticated data binds the ciphertext to `{account_organization_id, mode, provider, connection_id, version}`, so a
  bundle copied onto another connection's row does not open. A second key is additive (`b2.`), as ADR 0017 says.
- **The bundle**: `{client_id, client_secret, cert_pem, key_pem}` (Inter's fields; each provider defines its own) as
  JSON, sealed whole.
- **The ops.** The upload request is the one place plaintext touches the `api`: it is held in memory for that
  request, never logged, and handed to the Lambda's **`Connect`** op, which runs the connection test of § 3.2 with the
  plaintext, **seals** the bundle and returns `{sealed, capabilities, scopes_granted, account_check}`; the `api` stores
  `sealed`. Renewal uses **`Reseal`** (the stored ciphertext plus the new certificate and key; the Lambda opens the
  old bundle, tests the combination, returns a new ciphertext), so the `api` never sees the client_secret again. Every
  later op carries the ciphertext; the Lambda opens it in memory for that invocation (AES-GCM, no network call) and may
  keep the opened bundle in its container for up to 15 minutes. The Lambda has no DynamoDB access.
- **The fallback** if these ops are judged too much: ADR 0017's sealer with `FIELD_ENCRYPTION_KEY`, sealed in the
  `api`. It is weaker: that key is in every billing binary that calls `config.Load`, on internet-facing instances, and
  it would also have to be given to the Lambda. A compromise of the `api` would then read every merchant's bank
  credentials, which can create charges and read statements on N companies' accounts; with the dedicated key, the same
  compromise can submit new credentials but cannot read stored ones. The two ops are small (one more case in the
  Lambda's dispatcher), so the spec keeps the dedicated key.
- Logs, errors, audit rows and API responses never carry the secret, the key or the token. The console shows
  client_id, the certificate's subject, fingerprint and `notAfter`.

### 3.4 States and health

```
connection.state: active | expired | disconnected      (pending states are UI-only, § 11.1)
webhook_status:   not_registered | registered | conflict | overwritten
```

Health fields on the row: `last_token_ok_at`, `last_sync_ok_at`, `last_call_error` (code and time),
`cert_not_after`, `cert_fingerprint`, `capabilities`, `scopes_granted`, `webhook_status`, `consecutive_failures`.
`active` with `consecutive_failures ≥ 3` (token refused after the forced refresh, or 5xx from the provider) is shown as
**degraded** and alerts the organization's owners/admins once per day; collection keeps trying, since a refused
call never marks anything paid.

### 3.5 Certificate renewal — Inter (D6)

- Inter opens renewal 90 days before expiry and allows it until 90 days after; renewal issues a **new `.crt` and
  `.key`** and keeps client_id, client_secret and scopes (parecer § 2.2). Only the merchant can renew, in Inter's
  Internet Banking.
- The hourly job (§ 5) sends warnings at **D-90, D-30 and D-7** (and on the day it expires): e-mail to the
  organization's owners and admins (SES, as dunning does), a banner in the console and in Finanças, each once,
  guarded by `warned_90`, `warned_30`, `warned_7`, `warned_expired` flags reset by a renewal.
- `POST …/connections/inter/certificate` with the new pair: the § 3.2 checks, then `Reseal` (§ 3.3). On success a new
  bundle `version` is written in one conditional update; the old version is discarded. No downtime: calls in flight
  finish with the old pair, the next ones use the new.
- Past `notAfter`, `state = expired`: collection is refused (`byok_certificate_expired`, § 7.1) and sync pauses.
  Upload of a renewed pair returns it to `active`.

Providers without certificates simply never set `cert_not_after`; the warnings key on it.

### 3.6 Disconnect (D7)

`DELETE …/connections/{provider}`, owner/admin, in this order:

1. **Open charges.** Live charges still payable would keep being payable at the provider with nobody to confirm them.
   Disconnect refuses with `409 byok_open_charges` (count included) unless the body has `cancel_open_charges: true`;
   then each is removed (Inter: `PATCH /cobv/{txid}` `status=REMOVIDA_PELO_USUARIO_RECEBEDOR`) and its attempt becomes
   `FAILED`.
2. **Webhooks.** Read the registered webhook (Inter: `GET /webhook/{pix_key}`); if the URL is CTech's, delete it. A
   URL that is not CTech's is left alone.
3. **Credentials.** The bundle and the Valkey token are deleted; the hook row is deleted, so any late callback gets
   `404`.
4. **Gate.** When this provider is the tenant's `payout_provider`, `payout_status` returns to `not_configured`,
   `payout_rail` and `payout_provider` cleared (audited).
5. **Finanças.** The BYOK account keeps its history and becomes read-only (`sync_state: disconnected`). Reconnecting
   the **same** provider account resumes on it from its last synced day; another account gets a new ledger account.
6. **The merchant is told** (console and e-mail): CTech no longer holds the credentials, but **only they can cancel
   the integration** at the provider (Inter: Internet Banking → Integrar → the integration → Cancelar, which is
   irreversible).

If the provider cannot be reached at step 1 or 2, the console offers "Desconectar mesmo assim", which skips them and
tells the merchant which webhook and charges to remove themselves. Closing the organization (ADR 0026 purge) runs
this path with `cancel_open_charges: true`.

## 4. Reaching the provider

### 4.1 The outbound Lambda (D5)

`{env}-ctech-billing-byok-outbound`: Go, arm64, **not VPC-attached** (cheap IPv4 egress without NAT or a public
address per instance, parecer § 9 row 12), invoked synchronously by the `api` and the jobs with
`lambda:InvokeFunction` — the wallet's `pix-gateway/cmd/outbound` shape. It is the only billing code that opens a
connection to a provider, and it dispatches each op to the provider named in the envelope. Separate from the wallet's
function, role and secrets: one handles CTech's custodied money and the other merchants' credentials, and they must
not mix (parecer § 10.3).

Envelope:

```json
{ "op": "CreateDueCharge", "provider": "inter", "mode": "live",
  "connection": { "id": "…", "version": 3, "sealed": "b1.…", "account": "…" },
  "oauth_token": "…", "payload": { … } }
```

In test mode `connection` is `{ "id": "sandbox" }` and the Lambda uses CTech's sandbox credentials for that provider
from SSM (§ 9).

Phase-1 ops (provider-neutral names, § 4.4): `Connect`, `Reseal`, `Token`, `Balance`, `StatementPage`,
`CreateDueCharge`, `GetCharge`, `UpdateCharge`, `CancelCharge`, `GetPayment`, `Refund`, `GetRefund`, `GetWebhook`,
`RegisterWebhook`, `DeleteWebhook`, `ListCallbacks`, `RetryCallbacks`, `SimulatePayment` (sandbox only).

**Op allowlist in the Lambda (defense in depth for D11).** Ops are grouped:
- `read`: `Connect`, `Reseal`, `Token`, `Balance`, `StatementPage`, `GetCharge`, `GetPayment`, `GetRefund`,
  `GetWebhook`, `ListCallbacks`, `RetryCallbacks` (asks the provider to resend; creates nothing);
- `teardown`: `CancelCharge`, `DeleteWebhook` — always allowed, so disconnecting and voiding work whatever the flag
  says;
- `collect`: `CreateDueCharge`, `UpdateCharge`, `Refund`, `RegisterWebhook`.

A live `collect` op is refused inside the Lambda unless `BYOK_LIVE_COLLECTION` is `true` **and** the envelope's
provider is in `BYOK_LIVE_PROVIDERS` — the same SSM parameters the `api` reads (§ 10). A bug in the `api`'s gate still
cannot create a live charge. `SimulatePayment` is refused outside sandbox.

### 4.2 Inter base URLs

Production `https://cdpj.partners.bancointer.com.br`, sandbox `https://cdpj-sandbox.partners.uatinter.co`
(parecer § 2.3). **Non-production environments point every mode at the provider's sandbox**; a live connection in
`dev` accepts only sandbox credentials. Merchants' real credentials exist only in `prod`.

### 4.3 Tokens (D14)

- The `api` owns the lifecycle, as the wallet's token manager spec does: it asks the Lambda for `Token`, caches it
  and passes it in `oauth_token` with every op.
- Cache: Valkey (billing's DB 3), key `byok:token:{provider}:{mode}:{connection_id}:{version}:{scopeset}`, value sealed
  with ADR 0017's field sealer (a bearer for a merchant's account is not stored in clear even for an hour), TTL
  `expires_in − 300 s`.
- **Single-flight:** a `ctech-go-common/lock` lock per key; the loser waits and reads the cache.
- **Local limiter (Inter):** at most **3 token fetches per minute per connection** (and for the shared sandbox
  integration), counted in Valkey, below Inter's 5/min. Whether Inter counts per integration, per account or per
  source IP is open (§ 15); 3/min per connection is safe for one integration per merchant, and the per-IP reading is
  mitigated by needing ~1 fetch per connection per hour. Each provider declares its limits to the limiter.
- On `401` from an op: one forced refresh and one retry; a second `401` counts as a failure (§ 3.4).
- A token is fetched with the scope set of the capability in use (Inter: `extrato.read` alone for statement sync, the
  collection set for charges), so a statement sync never holds a token that can create charges.

### 4.4 The provider seam (R3)

Declared in **`gopkg.aoctech.app/api-commons/byok`** (ctech-go-common), implemented first by
**`api-commons/inter`**. Provider-neutral types, money in centavos, dates as civil dates:

```go
type Provider interface {
    Name() string // "inter"
    CredentialFields() []Field // what the console asks for

    // Connect validates and tests plaintext credentials and reports capabilities.
    Connect(ctx context.Context, c Credentials) (Connected, error)
    Token(ctx context.Context, c Credentials, set ScopeSet) (Token, error)

    CreateDueCharge(ctx context.Context, c Credentials, t Token, in DueCharge) (Charge, error) // Inter: PUT /cobv/{txid}
    GetCharge(ctx context.Context, c Credentials, t Token, ref string) (Charge, error)       // status + payments
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

    // ParseCallback runs in the webhook Lambda, with no credentials. Its output only wakes billing:
    // charge references and the account the provider says it is for. Nothing in it is trusted.
    ParseCallback(product Product, headers map[string]string, body []byte) (Callback, error)
}
```

- `Charge` carries a provider-neutral status (`open`, `paid`, `expired`, `removed`) plus the payments
  (`payment_id` — Inter `endToEndId` —, amount, late charges, time) and the payable artefact (Inter: `pixCopiaECola`).
- `StatementLine` carries `id` (Inter `idTransacao`), date, direction, amount, title, description and a
  `charge_ref` when the provider links it to a charge (Inter `detalhes.txId`).
- **Webhook authentication is the edge's job** (mTLS at API Gateway, § 6.1) plus the path secret; a provider that
  authenticates callbacks differently (a signature header) adds a `VerifyCallback` and its own host (§ 13).
- The `api` uses a `LambdaProvider` that implements the same interface over the RPC envelope; the Lambda's dispatcher
  holds the registry `{"inter": inter.Provider}`. Billing's services depend only on `byok.Provider`.

## 5. Jobs

One new binary, **`cmd/byok`**, hourly on the leader (crontab with `@reboot` catch-up, like `cmd/reconcile`; the
userdata ceiling note in the finance spec § 5 applies). Cross-tenant by construction, so it has no HTTP surface
(ADR 0002, as `cmd/sweep`). It reads connections through a sparse `jobs-index` partition `{mode}#byok-connections`.
Steps, each idempotent and re-runnable with `-date`:

1. **Charge sweep (safety net, D8):** every BYOK attempt still `PENDING` is re-queried (`GetCharge`) when its
   `checked_at` is older than 1 hour (checkout opened in the last 48 hours) or 24 hours (otherwise). The result goes
   through the same confirmation path as a webhook (§ 7.4).
2. **Statement sync** (§ 8.5).
3. **Certificate warnings** (§ 3.5) and expiry transitions; sandbox certificate warnings for the operator (§ 9).
4. **Webhook audit (daily, first run after 06:00 São Paulo):** `GetWebhook` → `overwritten` when the URL is no longer
   CTech's (alert + banner; payments are still confirmed by step 1); `ListCallbacks` for the last 24 hours → any
   delivery with a non-2xx status raises an operator alert (the webhook path is broken somewhere) and its charges are
   re-queried at once.
5. **Promotion to `enabled`** of `pending_clearance` tenants whose provider is cleared (§ 7.1), registering their
   webhook (§ 6.4).
6. **Usage (live only):** each BYOK charge created 24–48 hours ago and **not cancelled within 24 hours of its
   creation** is reported once as one unit of `billing_charges` for the organization's `ORG_` customer in tenant zero
   (idempotency key: the attempt id), so a charge counts in the month it was issued. Test-mode charges are never
   reported.

`cmd/byok -retry-callbacks -since=…` is the **recovery tool** after an outage of the webhook host: it lists failed
callbacks per connection and asks the provider to resend them (Inter: `POST /webhook/callbacks/retry`, ≤ 50 txids per
call, ≤ 5 calls/min, history kept 1 month; parecer § 6.1). It is an operator command, not a cron step: in normal
operation step 1 already confirms everything the webhook missed.

## 6. Webhooks (D8)

### 6.1 Host, edge and which certificates are involved (R6)

- One host for every merchant and every mTLS-delivering provider: **`billing-byok-hooks.aoctech.app`** (prod),
  `billing-byok-hooks-{env}.aoctech.app` elsewhere. A single label under `aoctech.app` so the existing `*.aoctech.app`
  certificate covers it.
- **Which certificates take part in a webhook call, explicitly:**
  - **The merchant's integration certificate is not involved.** It authenticates CTech's *outbound* calls to Inter on
    the merchant's behalf (§ 3.2) and nothing else.
  - **Inter presents its own client certificate**, the same kind for every merchant, signed by Inter's CA. CTech's
    truststore holds Inter's CA (`ca.crt`, "a chave pública do Inter", parecer § 6.3) and API Gateway rejects any
    handshake that does not chain to it.
  - **CTech presents its own server certificate** (the `*.aoctech.app` one), as any HTTPS server does.
  - So one CTech domain serves every merchant with **no per-merchant certificate** anywhere on the inbound side.
    Tenant separation comes from the path secret (§ 6.2), never from the certificate. The only open point is whether
    Inter's `ca.crt` differs between integrations (§ 15 item 1); the truststore is a bundle, so either answer works.
- API Gateway **HTTP API**, **regional** custom domain with **mTLS**: truststore in a versioned S3 bucket holding the
  **CA bundle** (Inter's `ca.crt` downloaded from CTech's own Inter account, the sandbox one, and any further CA a pilot
  merchant's download turns out to contain). `disableExecuteApiEndpoint: true`. This is the wallet's
  `pix-gateway-stack.ts` pattern in Terraform (ADR 0010).
- The Cloudflare record is a **DNS-only** CNAME to the regional target: proxied, Cloudflare would terminate TLS and
  the provider's client certificate would never reach API Gateway (parecer § 6.4).

### 6.2 Paths (D15)

The URL registered at the provider is `https://{host}/w/{provider}/{hook_id}/{secret}/{product}`:

- `provider`: `inter`. It selects the parser and the expected suffixes; it is checked against the hook row (§ 6.3),
  so it is routing, not authority.
- `hook_id`: 26-character ULID identifying the connection's hook row; `secret`: 32 random bytes, base64url (≥ 128 bits,
  parecer § 6.4). The secret is in the **path**, never in the query.
- `product`: `pix` in phase 1; reserved `boleto` (phase 2, Cobrança v3), `rec` and `cobr` (phase 3).
- Accepted routes for phase 1: `POST /w/inter/{hook_id}/{secret}/pix` **and** `POST /w/inter/{hook_id}/{secret}/pix/pix`.
  Inter's documentation says it appends `/pix` (parecer § 6.1); production evidence from the wallet says Inter calls the
  Pix URL **exactly as registered** (parecer § 6.6, corrected 2026-10-11). Accepting both costs one route and holds
  whichever is true for a given account. For phase 3, `…/rec` and `…/rec/rec`, `…/cobr` and `…/cobr/cobr` will be
  accepted the same way; Cobrança v3 has no documented suffix.
- The payload parser is chosen by the **registered provider and product segments**, never by the suffix, so a Pix
  payload can never be read as another product's.
- Any other path: `404` at API Gateway (default route), never reaching the Lambda.

### 6.3 The webhook Lambda and the notify route

`{env}-ctech-billing-byok-webhook` holds **no provider credentials and no tenant secrets** (the wallet's split). It
runs the provider's `ParseCallback` (Inter: `pix[].txid`, `pix[].endToEndId`, header `x-conta-corrente`) and calls
billing's **`POST /v1.0/internal/byok/notify`** on the public API host with an M2M token (new ctech-account client
`billing-byok-webhook`, scope `internal:billing:byok-notify`, published in billing's manifest, not `m2m`-marked for
merchants):

```json
{ "provider": "inter", "hook_id": "…", "secret": "…", "product": "pix", "account": "…",
  "charge_refs": ["…"], "payment_ids": ["…"] }
```

The `api`:
1. Reads `HOOK#{hook_id}`; its `provider` must equal the path's; compares the SHA-256 of `secret` with the stored hash
   **in constant time**; a previous secret is also accepted for 24 hours after a rotation (the provider's retries carry
   the URL of the moment they fired). Unknown hook, provider mismatch or wrong secret → `404`, and an operator alert at
   most once per hour per hook.
2. Compares `account` with the connection's account (when present; the shared sandbox hook compares with CTech's
   sandbox account). Mismatch → the call is discarded with `200` (a retry cannot make it right) and an alert is raised.
3. For each charge reference: the pointer `CHARGEREF#{provider}#{mode}#{ref}` must exist **and belong to the hook's
   organization** (in live) — otherwise it is ignored and counted. Then **re-query the provider** (`GetCharge`) with
   the connection's credentials and run the confirmation (§ 7.4). The payload's amounts, status and payer are never
   used.
4. Answers `200` when every reference was confirmed, ignored or found still open; `503` when a re-query failed, so the
   provider retries (Inter: 4 attempts over ~4 hours, parecer § 6.1) while the hourly sweep also covers it.

### 6.4 Registration and conflict (D24)

On connect (collection capability; in live only once the provider is cleared, so `cmd/byok` step 5 registers it at
promotion) and on "Registrar aviso novamente":

1. `GetWebhook` (Inter: `GET /webhook/{pix_key}`).
2. None registered → `RegisterWebhook` with CTech's URL; `webhook_status = registered`.
3. CTech's URL already (this hook) → nothing to do.
4. Another URL → `webhook_status = conflict`; the console shows the existing URL's host (the merchant's own
   configuration) and explains that registering CTech's will **stop that system from receiving payment notices**
   (Inter: for this Pix key), recommending a dedicated EVP key instead. Only `POST …/connections/{provider}/webhook`
   with `replace_existing: true`, sent after that confirmation, overwrites it (audited with the replaced host).
5. Re-registering rotates the secret (new hash, previous kept 24 hours).

Without a registered webhook collection still works — the sweep confirms within the hour — and the console says
confirmation will be slower. The `conflict` state does not block collection.

Whether an Inter Pix webhook belongs to the integration or to the key/account is open (§ 15 item 4); the
check-before-register and the daily audit (§ 5 step 4) are correct under either answer.

## 7. Collection by due-date charge (Inter: Pix with due date, `cobv`)

### 7.1 The gate (D11, D12)

`Organization` gains `payout_rail: "" | wallet | byok` (`""` reads as `wallet`, so every existing tenant is unchanged),
`payout_provider` (`inter`; set only with `byok`), and `payout_status` gains one value:

```
payout_status: not_configured | pending_custody | pending_clearance | enabled
```

| Event | Live tenant | Test tenant |
|---|---|---|
| A connection becomes collection-capable and the merchant chooses it for collection | `byok` + provider + `pending_clearance` when the provider is not cleared; `enabled` when it is | — |
| Merchant turns on "Cobrança via Inter (teste)" (§ 9) | — | `byok` + `inter` + `enabled` |
| Provider cleared (flag on and provider listed) | `cmd/byok` step 5 promotes `pending_clearance` → `enabled` (audited) | — |
| Disconnect of the collecting provider | `not_configured`, rail and provider cleared | same |

`Organization.AuthorizeCharge` stays **the single function** every collection path calls. For `byok` it returns nil
only when **all** hold, each with its own reason (409, `problem` code):

- `payout_status == enabled` (`payout_not_enabled`, as today);
- the connection for `(mode, payout_provider)` is `active` with the `collection` capability (`byok_not_connected`,
  `byok_certificate_expired`, `byok_collection_scopes_missing`);
- in live: `BYOK_LIVE_COLLECTION` is `true` and `payout_provider` is in `BYOK_LIVE_PROVIDERS`, **at call time**
  (`byok_collection_not_cleared`) — checked even though the stored status already encodes it, so removing a provider
  from the list stops its collection at once without a migration;
- in live: the tenant is approved (an entitling `prod_billing` subscription, R4) and not suspended (console spec
  § 4.3, unchanged). The entitling price's `quota_charges_month` is read here (§ 12.5): only a plan whose overage is
  `refuse` (Grátis) answers `byok_quota_exceeded` past its quota, with the upgrade suggestion; every other plan's
  overage is billed, never refused.

The flag and the provider list are the ecosystem-wide capability, the status is per merchant — exactly ADR 0005's two
gates (`AsaasCustodyEnabled` / `payout_status`), with the wallet's custody replaced by the lawyer's clearance per
provider. `pending_custody` keeps its wallet meaning. Tenant zero is untouched (rail `wallet`) unless moved for the
pilot (§ 12.6).

### 7.2 The charge (D17, D18)

- A BYOK attempt is a `PaymentAttempt` with method **`byok`**, `provider: inter`, `product: pix_due` (Inter `cobv`);
  `charge_ref` = the provider's reference (Inter: the txid). One attempt per invoice at a time; a new attempt only
  after the previous one is `FAILED`.
- **Inter txid** = `ctb` + the attempt's ULID (29 characters of `[A-Za-z0-9]`, inside Bacen's 26–35 rule, unique per
  receiver because ULIDs are). Written with the attempt, in the same transaction, is the pointer
  `CHARGEREF#{provider}#{mode}#{ref}` → `{tenant, account_organization_id, invoice_id, attempt_id}`; it is how a
  webhook and a statement line find their invoice (and, in the shared sandbox, their tenant).
- **When:** on invoice **finalization**, when `AuthorizeCharge` passes for the BYOK rail. If creation fails, the
  invoice is still issued and the attempt stays `PENDING` without a payable artefact; checkout (`Collector.Pay`) and
  the hourly sweep retry `CreateDueCharge` (idempotent: Inter's `PUT /cobv/{txid}` on an existing txid is read back
  with `GET`). Zero-total invoices create nothing (ADR 0019).
- **Inter body** (`PUT /cobv/{txid}`, parecer § 3.2):
  - `calendario.dataDeVencimento` = the invoice's due date (already rolled forward, ADR 0006);
  - `calendario.validadeAposVencimento` = the length of the invoice's copied dunning schedule in days, at least 1,
    so the Pix stays payable for as long as billing keeps chasing;
  - `devedor` = the customer's name and CPF or CNPJ (decrypted tax id, ADR 0017). A customer without a tax id cannot
    get a `cobv`: the attempt is not created and the invoice shows `customer_tax_id_required`. The wallet rail's
    `ErrNoPayerAccount` does **not** apply: the payer needs no CTech account;
  - `valor.original` = `amount_due`; `valor.multa` and `valor.juros` from the tenant's **collection settings**
    (late-fee percentage, monthly interest percentage, calendar days; both optional, default none), **copied onto the
    invoice at finalization** like the dunning policy, so a later settings change never alters an issued charge.
    `desconto` and `abatimento` are not used in phase 1;
  - `chave` = the connection's `pix_key`; `solicitacaoPagador` = the invoice number.
- **No CTech ceiling (D18).** ADR 0004's ceiling bounds the damage of one forged request opening a charge into CTech's
  pooled account; a BYOK charge pays the merchant's own account, and `MaxChargeCents` mirrors the wallet's limit, which
  does not apply. The provider's own format bounds the value.
- The payable artefact (Inter: `pixCopiaECola`) is stored on the attempt; the QR image is rendered by billing (the
  wallet's `qr.go`, through the shared package). Checkout, the invoice e-mail and the invoice PDF show it with the due
  date and "pague até {due + validity}".

### 7.3 Changes to an open invoice

- **Void:** `CancelCharge` (Inter: `PATCH /cobv/{txid}` `status=REMOVIDA_PELO_USUARIO_RECEBEDOR`, no other change in
  the same request); a `409` because it was concluded meanwhile → the re-query decides, and a paid charge wins over the
  void (the invoice's existing paid-after-void handling applies).
- **Credit note on an open invoice:** `UpdateCharge` with the new `amount_due` while open; refused (already concluded)
  → re-query.
- **Validity ends unpaid** (Inter: expired / `REMOVIDA_PELO_PSP`): attempt `FAILED`; the invoice follows its dunning
  schedule's end as today. The console can open a new attempt ("Gerar novo Pix", new due date).

### 7.4 Confirmation

One function, called by the webhook route, the sweep and the statement sync (three wake-ups, one decision):

1. `GetCharge` through the Lambda (never the payload).
2. `paid` with payments: the first payment settles the attempt `SUCCEEDED` (`Collector.settleInvoice`, unchanged
   downstream: subscription activation, settlement bus, finance posting). Its amount must be ≥ `amount_due`; the
   difference is the late charge (Inter: `componentesValor` multa + juros) recorded on the payment and posted in
   Finanças to the seeded *Juros e multas* category. Less than `amount_due` → `ErrAmountMismatch` alarm (existing),
   never a partial payment.
3. Any further payment on the same charge, or an amount above `amount_due` + computed late charges → an **excess
   payment** row on the invoice (D19), a console notice ("Pagamento em duplicidade") and an e-mail to owners/admins.
   Nothing is refunded automatically.
4. Still `open` → nothing; `checked_at` updated.

Idempotent by attempt status (`PENDING → SUCCEEDED` is the existing guarded transition): three wake-ups for the same
payment settle it once.

### 7.5 Refunds (D19)

Merchant-initiated from the console (owner/admin), gated like collection (`Refund` is a `collect` op):
- an **excess payment**: "Devolver" refunds that payment (Inter: `PUT /pix/{e2eId}/devolucao/{id}`, `natureza
  ORIGINAL`);
- a **paid invoice**: a credit note may carry "devolver via Pix", refunding up to the credited amount from the
  invoice's payment.

The refund id is derived from the excess row or credit note (Inter: `[a-zA-Z0-9]{1,35}`, its idempotency key); status
(Inter: `EM_PROCESSAMENTO → DEVOLVIDO | NAO_REALIZADO`) is followed by `GetRefund` in the hourly sweep. Inter's 90-day
window is checked before the call (`refund_window_closed`). Finanças posts the refund as a cash-out of the BYOK
account against the original category (a reversal for the credited part), linked to the statement line when it
arrives.

### 7.6 Why there is no wallet in this (D2)

The money settles in the merchant's account and never in CTech's (parecer § 8). Recording it in a wallet would create
**a second balance for money the bank already holds**: it would drift from the bank's (fees, the merchant's own Pix
out, refunds made in the bank's app) with no way to reconcile except by trusting the bank — at which point the wallet
balance is noise. Worse, a CTech-held balance for a merchant's money is what custody looks like from outside, which is
the exact question ADR 0005 and the lawyer are about. So: the invoice and its attempts in billing say **what was
charged and whether it was paid**; the BYOK account in Finanças says **what the bank holds**, with the bank's own
balance. No code path on the BYOK rail calls the wallet client; a test asserts it (§ 17).

## 8. Statement and balance in Finanças

### 8.1 The account (D20)

- On connect, the organization's Finanças space (same mode) gets a ledger account `class: asset`, named after the
  provider and the account's last digits ("Inter ···1234"), with the new attributes **`source: byok`** (absent =
  `manual`) and **`provider: inter`**. The ledger has no account kind today (`repositories/finance_invoices.go`);
  `source` is the smallest addition and the one the read-only rules key on.
- Two system categories are seeded with the first BYOK account: `sys-unclassified-in` ("A classificar — entradas",
  income, group `other`) and `sys-unclassified-out` ("A classificar — saídas", expense, group `other`).
- **Statement-driven:** manual settlement into the account, transfers to or from it, opening-balance edits and
  OFX/CSV imports into it answer `409 account_is_synced`. It may be the space's default receiving account, and bills
  may expect it; they are settled by matching a line (§ 8.3).
- **Balance:** the account row stores the provider's last `Balance` (Inter `GET /saldo`: `disponivel`, the blocked
  amounts, `dataReferencia`) and `synced_at`. Screens show **the provider's balance** as the account's balance, with
  "atualizado às {hora}". The ledger's derived balance must equal it; a difference is shown ("Diferença de R$ X entre o
  Inter e o Finanças") and logged, and points at a bug or a line the parser could not read. No balance here authorizes
  anything (ADR 0024).

### 8.2 Posting

- **Opening balance:** `Balance(D0 − 1)` (Inter: `GET /saldo?dataSaldo=`), where D0 is the first synced day, through
  the ordinary opening-balance rule (`OPENING#` marker). Exact by construction: opening + every line since D0 = today's
  balance. (That Inter's `dataSaldo` is the end-of-day balance is to be confirmed, § 15.)
- **Each line** (Inter: `GET /banking/v2/extrato/completo`) posts one transaction: credit → BYOK account ↔
  `sys-unclassified-in`; debit → `sys-unclassified-out` ↔ BYOK account; dated by the transaction date, memo title +
  description, ref the line id. Idempotency: the existing lock row in `imports`, key
  `FITID#{account}#B:{provider}:{line_id}` (no TTL), written in the same `TransactWriteItems`.
- **Why automatic, when the finance spec says "nothing is settled without a confirmation":** that rule protects bills
  from a guess. Here no bill is settled; the bank says the money moved, so the cash leg is a fact and only its
  *category* is a guess — and that guess is explicit ("A classificar"). The balance stays equal to the bank's without
  waiting for the person.

### 8.3 Classification and matching

`GET …/finance/accounts/:id/unclassified` lists the account's lines still on the two system categories (one Query on
those categories' entries). For each, the person (verb `finance.import`) chooses:
- **a category** → replacement adjustment (reverse the line's transaction, post it to the category);
- **an open bill** (offered when amount and due date match within ±5 days, as import does) → replacement: reverse the
  line, settle the bill with the line's date and amount; the cash leg is unchanged;
- **a transfer** to another of the space's accounts → replacement into a transfer.

The balance never moves on classification. Auto-confirming matches stays out of scope, as in the finance spec.

### 8.4 Linking to billing (no double count)

A paid BYOK invoice posts in Finanças through the existing rule (§ 3.8), with two changes for the BYOK rail: the
issuer side's cash leg goes to **the collecting provider's BYOK account** (not the default receiving account), and its
ref is the payment id (Inter: `endToEndId`). Then:

- the sync meets a line whose `charge_ref` has a `CHARGEREF#` pointer: if the invoice's posting exists, the line posts
  **nothing** and its lock row is linked to that transaction;
- if the invoice is not paid yet, the line is a third wake-up for § 7.4 (re-query); after settlement it is linked in
  the same run;
- a line still unlinked after 24 hours (re-query keeps saying unpaid, which should not happen) is posted unclassified
  and an alert is raised;
- excess payments and refunds link the same way, through their payment id.

The finance § 3.8 durable retry queue keeps covering a failed write.

### 8.5 Sync schedule and limits

- **Backfill** on connect: `backfill_months` (3 default, up to 12) in windows of the provider's maximum (Inter: 90
  days per call), in the background, newest window first so the recent statement appears soonest. How far back Inter
  answers is open (§ 15); a window the provider refuses ends the backfill, and the screen says from which day the
  statement starts.
- **Incremental:** every hour in `cmd/byok`, re-reading the last 3 days by transaction date (late lines), deduplicated
  by line id; then `Balance`.
- **"Sincronizar agora"** in Finanças: at most once per 10 minutes per connection.
- **Inter: pagination, not scroll (D21):** `pagina`/`tamanhoPagina`; scroll allows one active scroll per account, and
  the merchant's ERP may hold it.
- **Inter limits:** banking is 10 calls/min (parecer § 2.4); the local limiter allows 8/min per connection, and the
  sync for one connection runs under a per-connection lock, so the hourly run and a button press never overlap.

## 9. Test mode (D9, D22)

- One **CTech-owned integration per provider**, used against the provider's sandbox host. For Inter it is the
  integration CTech already holds for ctech-wallet (amendment below): the outbound Lambda's role is granted read on
  `/ctech-wallet/{env}/inter/{client-id,client-secret,mtls-cert,mtls-key}` and reads them at cold start; nothing is
  copied. The sandbox account number and Pix key are billing's own non-secret parameters
  `/ctech-billing/{env}/byok/inter-sandbox/{account,pix-key}`. `cmd/byok` alerts the operator at D-30, D-7 and D-1 of
  that integration's certificate (the wallet's annual renewal renews test mode too).
- A tenant enables it in test mode with "Cobrança via Inter (teste)" — no credentials. Its test Finanças space gets a
  BYOK test account.
- **One webhook** for the sandbox Pix key: `…/w/inter/sandbox/{secret}/pix`, tenant resolved through
  `CHARGEREF#inter#test#{txid}`.
- **Statement filter:** the sandbox account is shared by every test tenant, and test data may carry real names and
  tax ids an integrator typed. A test space receives **only lines whose charge reference points to that tenant's own
  charges**; every other line is dropped. The opening balance of a test account is zero, and its balance is the sum
  of its own lines (the shared sandbox balance is never shown).
- **Shared limits:** Inter's sandbox allows 10 Pix calls/min and 5 tokens/min **for all test tenants together**. A
  global limiter (8 Pix calls/min, one token fetch per hour normally) answers `429 byok_sandbox_busy` ("tente em
  instantes") when exhausted. Sandbox failures alert at a lower severity than live ones, and the console says the
  sandbox may be unavailable (parecer § 2.6).
- "Simular pagamento" on a test invoice calls `SimulatePayment` (Inter: `POST /cobv/pagar/{txid}`, sandbox only); the
  confirmation then runs through the real webhook and re-query path.
- This makes test-mode BYOK invoices collectable, which ADR 0004's 2026-08-16 amendment forbids for the wallet rail
  only. The wallet rail keeps `ErrTestModeNotPayable`; the BYOK rail in test reaches the sandbox, never real money.
  No row crosses modes (ADR 0003).

## 10. The legal gate (D11)

- **`BYOK_LIVE_COLLECTION`** (SSM `/ctech-billing/{env}/byok/live-collection-enabled`) and **`BYOK_LIVE_PROVIDERS`**
  (SSM `/ctech-billing/{env}/byok/live-providers`, comma-separated, e.g. `inter`), both set by Terraform variables
  that **default to `false` and empty** (so turning them on is a reviewed commit). Read by the `api` and by the
  outbound Lambda. Off, or provider not listed: no live charge, refund or webhook registration for that provider; the
  console shows "Cobrança pelo Inter em liberação" on a connected live tenant, and statement sync works.
- **Clearing a provider requires, in the same pull request:** ADR 0030 (or a later ADR per new provider) recording the
  lawyer's answers to parecer § 8 questions 1–4 for that provider and the owner's decision, and the provider added to
  `BYOK_LIVE_PROVIDERS` for `prod`. Without the ADR the review refuses the change.
- **Do I agree that read-only sync can go live without the clearance? Yes, with two conditions.**
  - Questions 1 and 4 (is operating charges "facilitating a payment instruction", art. 6º III b; does BYOK escape
    ADR 0005) concern **collection** only. Reading a statement moves no money and instructs no payment; it is what an
    accountant, an ERP or an aggregator (Pluggy, parecer § 2.1) does with the same credentials.
  - Questions 2 and 3 (do Inter's API terms allow a third party to hold and use the certificate; what contract and
    LGPD role CTech has) apply to **both** paths: a read-only connection holds exactly the same credentials. So:
    (a) the connect form carries the merchant's explicit authorization and declaration (§ 3.2), placing on the
    merchant, in writing, the representation that their provider's terms allow it; (b) live sync starts with the
    **pilot** (§ 12.6), whose API terms CTech can read itself (parecer § 8 Q2), and general availability of live sync
    waits for someone to read that text. This is an operational gate (who gets invited), not a flag.
  - D23 interacts: a merchant who ticks the collection scopes hands CTech credentials able to create charges even
    while the provider is not cleared. The Lambda allowlist (§ 4.1) and read-scoped tokens (§ 4.3) make that
    capability unusable, not merely unused.

## 11. Console and Finanças

Built with `/impeccable` on `@aoctech/ui`, following `ui/DESIGN.md`, each screen with its phone layout. One screen per
provider serves both areas: **`/finance/settings/connections/inter`** in Finanças and **`/console/connections/inter`**
in the console, under a **`…/connections`** list that shows the available providers (only Inter in phase 1). The
console entry appears for merchant tenants, the Finanças entry for every organization space where the person is
owner/admin. Before building a stepper, file-drop and health card here, check `ctech-ui` for existing ones and propose
them there if absent (family rule).

### 11.1 Guided onboarding (Inter)

A checklist, each step with what to click at Inter (parecer § 2.1) and its own state, saved per organization and
provider (`onboarding_step`, display only):

1. **Requisitos:** conta Inter Empresas (PJ); who can do it at Inter.
2. **Criar a integração** no Internet Banking: Integrar → Nova integração; name and description suggested; the scopes
   to tick, as a copyable list: `extrato.read` (Finanças) and, for collection, `cobv.read`, `cobv.write`, `pix.read`,
   `pix.write`, `webhook.read`, `webhook.write`; and an explicit **"não marque nenhum escopo de pagamento
   (pagamento-*)"**. The questionnaire's three parts and the API terms acceptance are mentioned.
3. **Em validação:** "O Inter analisa a integração; enquanto isso não é possível criar outra. O Inter avisa por
   e-mail." The person can mark "estou aguardando" and come back; the step shows since when.
4. **Baixar chave e certificado:** client_id and client_secret are shown **once** — copy them now.
5. **Enviar para a CTech:** client_id, client_secret, `.crt`, `.key`, conta corrente, Pix key (collection; EVP
   recommended), history depth, the authorization checkbox (§ 3.2). The test runs on submit and answers in plain
   language (wrong secret, certificate from another integration, scope missing, expired).
6. **Aviso de pagamentos:** webhook registration, with the conflict dialog (§ 6.4).

### 11.2 Connection screen

Status (Ativa / Atenção / Certificado vencido / Desconectada), capabilities (Extrato, Cobrança — and "em liberação"
under the legal gate), certificate (subject, fingerprint, "vence em 12/10/2027", a countdown banner from D-90), last
sync and last error in words, webhook status, actions: Testar conexão, Renovar certificado, Registrar aviso
novamente, Sincronizar agora, Usar para cobrança, Configurar multa e juros (console), Desconectar (confirmation listing
the open charges that will be cancelled and the "só você cancela a integração no Inter" message).

### 11.3 Elsewhere

- Finanças: the BYOK account shows the provider's balance and "atualizado às", the unclassified count with a link to
  the classification list, and the difference banner (§ 8.1).
- Console invoices: the attempt's Pix (copy and QR), "Simular pagamento" in test, excess payment notice with
  "Devolver", the `customer_tax_id_required` and gate reasons in words.
- Tenant zero's console (applications, console spec § 4.2) shows each merchant's `payout_rail`, `payout_provider` and
  status.

## 12. Interfaces

### 12.1 Routes

Console routes require a user session (`RequireUserScope`), `X-Billing-Space: org:{id}` through `ResolveSpace`, and
`X-Billing-Mode`. Writes go through the idempotency middleware. `{provider}` is `inter` in phase 1; an unknown
provider is `404`.

| Route | Who / scope |
|---|---|
| `GET /v1.0/console/connections` | owner/admin; `billing:finance:read` |
| `GET /v1.0/console/connections/{provider}` | same |
| `PUT /v1.0/console/connections/{provider}` (multipart in live; `{}` in test turns the sandbox on) | owner/admin; `billing:finance:write` |
| `POST /v1.0/console/connections/{provider}/test` | same |
| `POST /v1.0/console/connections/{provider}/certificate` | same |
| `POST /v1.0/console/connections/{provider}/webhook` `{ "replace_existing": bool }` | same |
| `POST /v1.0/console/connections/{provider}/sync` | same |
| `PUT /v1.0/console/connections/{provider}/onboarding` `{ "step": "…" }` | same |
| `DELETE /v1.0/console/connections/{provider}` `{ "cancel_open_charges": bool }` | same |
| `PUT /v1.0/console/collection` `{ "provider", "late_fee_percent", "monthly_interest_percent" }` | owner/admin of the merchant tenant; `billing:organization:write` |
| `POST /v1.0/console/invoices/:id/charge` (new attempt) | same |
| `POST /v1.0/console/invoices/:id/simulate-payment` (test only) | same |
| `POST /v1.0/console/excess-payments/:id/refund` | same |
| `GET /v1.0/console/finance/accounts/:id/unclassified` | `finance.read` |
| `POST /v1.0/console/finance/unclassified/:tx_id/classify` `{ "category_id" \| "bill_id" \| "transfer_account_id" }` | `finance.import` |
| `POST /v1.0/internal/byok/notify` | M2M `internal:billing:byok-notify` |

New `problem` codes: `byok_not_connected`, `byok_certificate_expired`, `byok_certificate_invalid`,
`byok_key_mismatch`, `byok_credentials_refused`, `byok_collection_scopes_missing`, `byok_open_charges`,
`byok_webhook_conflict`, `byok_sandbox_busy`, `byok_provider_unavailable`, `byok_collection_not_cleared`,
`byok_quota_exceeded`, `subscription_limit_reached`, `customer_tax_id_required`, `account_is_synced`, `refund_window_closed`.

### 12.2 Data

New table **`{env}_billing_byok`** in `schema.json`:

| Row | pk → sk | Notes |
|---|---|---|
| Connection | `{account_org_id}#{mode}` → `CONNECTION#{provider}` | state, kind (`merchant` \| `sandbox_shared`), public identifiers (Inter: client_id, account_number, pix_key), `sealed` (`b1.`), version, cert fields, capabilities, scopes_granted, hook_id, webhook_status, health, warned flags, authorization, onboarding_step, sync cursor, backfill range, ledger_account_id; `jobs_pk = {mode}#byok-connections` (sparse) |
| Hook | `HOOK#{hook_id}` → `HOOK` | provider, account_org_id, mode, secret_hash, previous_secret_hash + valid_until |
| Charge pointer | `CHARGEREF#{provider}#{mode}#{ref}` → `CHARGEREF` | tenant, account_org_id, invoice_id, attempt_id; written with the attempt |

Changed rows: `Organization` gains `payout_rail`, `payout_provider`; `PaymentAttempt` gains method `byok`, `provider`,
`product`, the payable artefact, `checked_at`, late charges; a new `EXCESS#` row under the invoice; the invoice copies
the collection settings at finalization; `LedgerAccount` gains `source`, `provider` and the balance snapshot;
`imports` gains `B:` lock keys.

### 12.3 Configuration

| Name | Where |
|---|---|
| `BYOK_LIVE_COLLECTION` | SSM `/ctech-billing/{env}/byok/live-collection-enabled`, Terraform variable, default `false` |
| `BYOK_LIVE_PROVIDERS` | SSM `/ctech-billing/{env}/byok/live-providers`, Terraform variable, default empty |
| `BYOK_OUTBOUND_FUNCTION` | `api` env; `lambda:InvokeFunction` on it |
| Credentials key | SSM SecureString `/ctech-billing/{env}/byok/credentials-key` (32 random bytes, base64); **outbound Lambda only**, read at cold start |
| Sandbox credentials | the wallet's `/ctech-wallet/{env}/inter/{client-id,client-secret,mtls-cert,mtls-key}` by IAM grant, plus `/ctech-billing/{env}/byok/inter-sandbox/{account,pix-key}` (String); outbound Lambda only (amended 2026-10-11) |
| Webhook M2M client | SSM `/ctech-billing/{env}/byok-webhook/{client-id,client-secret}`; webhook Lambda only |

### 12.4 Shared packages (D10)

In ctech-go-common (module `gopkg.aoctech.app/api-commons`):

- **`byok`**: the provider interface and neutral types of § 4.4, plus the envelope types the Lambda and its callers
  share.
- **`inter`** (proposed by parecer § 10.2), Inter's implementation:
  - `inter.Credentials{ClientID, ClientSecret, CertPEM, KeyPEM, Account, Env}` passed **per call**; the package reads
    no SSM, no environment and no cache of its own;
  - an mTLS transport cached by certificate fingerprint (a renewal makes a new transport);
  - `FetchToken(ctx, creds, scopes)` and a `TokenCache` interface the caller implements (billing: Valkey + lock);
  - `pix`: `cobv` (put, get, patch), `pix` (get), devolução (put, get), webhook (get, put, delete), callbacks (list,
    retry), sandbox payment; `banking`: extrato completo (paginated), saldo;
  - Bacen RFC 7807 error types, money conversion, txid validation, the BR Code QR renderer (from the wallet's
    `qr.go`);
  - `inter.Provider`, the `byok.Provider` adapter over the above.

Phase 2 adds `cobranca`, phase 3 `pixauto`. The wallet migrates its `pix-gateway` to `inter` in its own pull request
(it does not use `byok`); its RPC contract does not change.

### 12.5 Catalogue and pricing (R4)

**The unit is the "cobrança emitida"**: one BYOK charge created for an invoice in live mode (a due-date Pix now; a
Boleto com Pix in phase 2; each Pix Automático cycle charge in phase 3 counts as a normal charge). A charge cancelled
within 24 hours of its creation does not count (§ 5 step 6). There is **no per-subscription price and no separate
subscription quota**.

Plans, sold through **`prod_billing`** (owner key `billing`, the product whose active subscription already approves a
live merchant, console spec D3). Decided catalogue values; prices go into `api/tenants/ctech.json` in centavos when
the catalogue is written:

| Plan | Monthly fee | Charges included | Past the quota |
|---|---|---|---|
| Grátis | R$ 0 | 50 | **refused** (`byok_quota_exceeded`, upgrade suggested); 1 bank connection, 1 user |
| Sob demanda | none | 0 | R$ 0,49 per charge, with an automatic cap: never more than the cheapest paid plan that would fit the month's volume |
| Básico | R$ 99,90 | 500 | R$ 0,25 each |
| Crescimento | R$ 299,90 | 2.000 | R$ 0,18 each |
| Pro | R$ 699,90 | 5.000 | R$ 0,15 each |
| Empresas | sob consulta | above 10.000 | negotiated; not in the public catalogue |

- **Overage is charged automatically and never blocks**, except Grátis. **Active subscriptions are capped at the
  plan's charge quota**: creating a subscription collected through BYOK beyond `quota_charges_month` active ones
  answers `409 subscription_limit_reached` with the upgrade suggestion; charges themselves are never refused on a paid
  plan. Sob demanda has no quota, so no cap.
- **Upsell is by features, not subscription limits:** more bank connections (`quota_bank_connections`), API access
  (`feature_api_access`) and users (`quota_users`), as price metadata. Grátis has 1 connection and 1 user; the other
  plans' feature values are set with the catalogue.
- **Annual billing** with about two months free is optional, later.
- **`price_billing_early_access` stays R$ 0** (`plan: early_access`, unlimited) until the merchant chooses a plan.
- Statement sync alone is not priced in phase 1 (D16); the Finanças "Custom (BYOK)" tier follows from this table.

**How it maps onto billing's existing catalogue features** (ADR 0018, plans spec):

- Each paid plan is a subscription with two prices: the **fixed monthly fee** (advance) and a **metered overage
  price**: `meter: billing_charges`, `aggregation: sum` (the default), `included_quantity` = the plan's included
  charges, `unit_amount` = the overage price, arrears. `billed = max(0, used − included_quantity) × unit_amount` is the
  plans spec's existing rule. Metadata carries `quota_charges_month` (equal to `included_quantity`; `-1` unlimited) and
  `overage: bill | refuse` (`refuse` only on Grátis). **No new invoicing feature is needed** for Grátis, Básico,
  Crescimento and Pro.
- **Sob demanda's automatic cap does need a new invoicing rule.** Its bill is `min(0,49 × n, min over the paid plans
  of fee + max(0, n − included) × overage)`: a metered item whose amount is capped by a function of other prices. No
  existing price type expresses that (`included_quantity` and `aggregation` cannot). **Deferred to the pricing plan**,
  not this phase: BYOK phase 1 does not depend on it, and until the rule ships Sob demanda is not offered (not in the
  public plan list).
- Empresas is a per-customer price created by the operator, never in the public catalogue.

### 12.6 The pilot (R5)

- **Who:** CTech's own CNPJ, the account organization `01a04ed6-1af9-745e-bcf7-d3b66fe52321`, which is already tenant
  zero's linked organization (`api/tenants/ctech.json`).
- **New catalogue price, not public:**

  ```json
  { "id": "price_billing_unlimited_internal_monthly", "product_id": "prod_billing", "type": "fixed",
    "unit_amount": 0, "recurrence": { "interval": "month", "count": 1 }, "billing_timing": "advance",
    "metadata": { "plan": "unlimited", "visibility": "internal",
                  "quota_charges_month": "-1", "overage": "bill" } }
  ```

  The same shape as DF-e's `price_dfe_unlimited_internal_monthly` (R$ 0, unlimited quotas, `visibility: internal`,
  never in a public plan list), but under `prod_billing` itself, so the console spec's entitlement hook (a subscription
  carrying a `prod_billing` price) treats it as approval with no special case. CTech's `ORG_` customer in tenant zero
  is subscribed to it.
- **What the pilot exercises:** live statement sync into CTech's Finanças space first; collection in test mode at
  once; live collection only after the provider is cleared (§ 10). Collecting **tenant zero's** live invoices through
  BYOK means moving tenant zero's `payout_rail` from `wallet` to `byok`, a deliberate, audited operator action the
  owner takes, never a side effect of connecting.
- **Inter account caution:** the wallet's `pix-gateway` already registers a Pix webhook on CTech's Inter Pix key. The
  pilot's BYOK integration must use a **dedicated EVP key**; on the wallet's key D24 would report `conflict`, and
  confirming the replacement would break the wallet's deposits.

## 13. Seams for later phases and other providers

| Seam | Phase 2 (Inter Cobrança v3) | Phase 3 (Inter Pix Automático) | Another provider |
|---|---|---|---|
| Provider interface (§ 4.4) | `CreateBoleto`-style methods added to `byok.Provider` as an optional interface | recurrence methods as an optional interface | implements `byok.Provider`, registered in the Lambda's dispatcher |
| Webhook path (§ 6.2) | `/w/inter/{hook}/{secret}/boleto` (no suffix documented) | `/rec`, `/cobr`, each also with its suffix variant | `/w/{provider}/…`; a provider that signs callbacks instead of mTLS gets its own host and `VerifyCallback` |
| Pointer rows | `CHARGEREF#inter#…` keyed by `codigoSolicitacao` | keyed by `idRec` / `cobr` txid | same row shape |
| `PaymentAttempt.product` | `boleto` | `pix_automatic` | the provider's products |
| Capabilities and scopes | `boleto` (`boleto-cobranca.*`); customers need a full address (billing's `Customer` gains it then) | `pix_automatic` (`rec.*`, `solicrec.*`, `cobr.*`, `payloadlocationrec.*`, `webhookrec.*`, `webhookcobr.*`); CNPJ ≥ 6 months | per provider |
| Gate | same `AuthorizeCharge` and provider clearance (the lawyer's question 1 explicitly includes `cobr`) | same | the provider needs its own clearance ADR before joining `BYOK_LIVE_PROVIDERS` |
| Subscriptions | — | a per-subscription collection method; journey 4 builds on this phase's `cobv` (parecer § 5.3) | — |
| Statement linking | `seuNumero` / `codigoSolicitacao` as `charge_ref` | `cobr` txid | the provider's `charge_ref` |
| Pricing | each boleto counts as a charge (R4) | each cycle charge counts as a normal charge (R4) | same unit |

`cobv` is Bacen-standard, so the `inter/pix` subpackage keeps Bacen types apart from Inter-specific ones; another
Bacen-API PSP can reuse them.

Whether adding scopes to an existing Inter integration needs a new integration (and a new validation wait) is open
(§ 15); if it does, phases 2 and 3 ask the merchant to tick their scopes in phase 1's integration in advance.

## 14. Failure behaviour

- **Provider down / 5xx:** reads show the last synced data with its time; charge creation retries via checkout and
  the sweep; confirmation is never inferred from a failure. `consecutive_failures` drives "Atenção" and alerts.
- **Lambda unreachable:** the same as the provider down; the console answers `503 byok_provider_unavailable` on
  actions.
- **Token limit hit (429 on token):** the local limiter makes this a bug; the op fails, retried by its caller, and an
  operator alert fires.
- **Webhook lost or webhook host down:** the hourly sweep confirms; the statement sync is a third path; after an
  outage the operator runs `-retry-callbacks`.
- **Forged webhook:** fails the mTLS handshake at the edge; with a valid CA but wrong secret, `404`; with a valid
  secret, nothing is trusted from the body.
- **Webhook overwritten by the merchant's ERP:** detected daily, banner and alert; confirmation continues by sweep.
- **Certificate expired:** collection refused with a reason, sync paused, console and e-mail say how to renew.
- **Merchant cancelled the integration at the provider:** tokens are refused → "Atenção" → after 24 hours of refusals
  the console asks to reconnect or disconnect; nothing is marked paid or unpaid on a refusal.
- **ctech-account unreachable:** console routes fail closed (ADR 0025); the notify route needs no membership.
- **Credentials key parameter unreadable at cold start:** the outbound Lambda fails to start and alerts; every op fails
  as "provider down". Warm containers keep working.
- **Concurrent confirmations** (webhook, sweep, sync): one guarded transition; the others see `SUCCEEDED`.
- **Disconnect racing a payment:** a payment that lands after the charge is removed is refused by the provider; one
  that lands before is confirmed by step 1's removal answer (`409` → re-query) before credentials are deleted.

## 15. Open items

To verify with Inter or the pilot (parecer § 11), each with the design's current answer:

1. **Does Inter's `ca.crt` differ between integrations?** Probably not (it is described as "Inter's public key"), but
   it is undocumented. Either way works: the truststore is a bundle, and tenant separation never depends on the CA
   (§ 6.1). Check: compare CTech's download with the pilot integration's.
2. **Is the 5 tokens/min limit per integration, per account or per source IP?** The limiter assumes per integration
   and keeps ~1 fetch per connection per hour, safe if it is per IP at today's scale; measure with the pilot and
   revisit before ~200 live connections.
3. **Statement history depth.** The API caps a call at 90 days and documents no lower bound. Backfill stops at the
   first refused window; measure with the pilot (ask 24 months).
4. **Does a Pix webhook belong to the integration or to the key/account?** Unknown; the safe default is
   check-before-register with explicit confirmation to overwrite and the daily audit (§ 6.4, D24).
5. **Pix Automático suffixes (`/rec`, `/cobr`) are unverified;** the Pix suffix is documented but not applied in
   production (parecer § 6.6). Phase 3 accepts both variants; verify with the pilot.
6. Whether the **old certificate stays valid** after renewal until its `notAfter` (affects only the swap's overlap).
7. Whether **scopes can be added** to an existing integration without a new integration and validation.
8. Whether a token request for scopes the integration lacks is **refused or narrowed** (§ 3.2 handles both).
9. Whether `cobv` creation needs `payloadlocation.write` (Bacen allows the PSP to create the location).
10. `GET /saldo?dataSaldo` semantics (end-of-day balance?) for the opening balance (§ 8.2).
11. `x-conta-corrente` vs `x-inter-conta-corrente` in the statement example (parecer § 2.5).
12. The text of Inter's API terms (lawyer question 2, readable from the pilot's own account) and the merchants'
    tariffs (their decision, not CTech's).
13. **For the lawyer:** parecer § 8 questions 1–4. Live collection stays off until answered (§ 10).

Decided, not open here: the price table (§ 12.5) and the pilot (§ 12.6). Deferred to the pricing plan: Sob demanda's automatic cap (§ 12.5).

## 16. Deploy

1. **ctech-go-common:** `byok` and `inter` packages released; billing pins the version.
2. **billing manifest + ctech-account:** billing publishes `internal:billing:byok-notify`; ctech-account creates the
   `billing-byok-webhook` client with that scope (`cmd/createclient`); its id/secret into SSM.
3. **billing infra (Terraform):** the credentials-key parameter (generated once, never in state as plaintext: created
   by an operator script in the repo, Terraform only grants read on its ARN); the two Lambdas and their CI workflow (the
   wallet's `pix-gateway.yml` shape, deployed ahead of the `api`); HTTP API with the regional mTLS domain, the
   versioned truststore bucket seeded with CTech's `ca.crt` and the sandbox CA; the DNS-only CNAME (operator,
   documented in `docs/runbooks/byok.md`); the Lambda's read grant on the wallet's Inter parameters (test mode); `BYOK_LIVE_COLLECTION=false`,
   `BYOK_LIVE_PROVIDERS` empty.
4. **billing part A:** connections, tokens, Finanças account and statement sync (live allowed, pilot first, § 10),
   test-mode sandbox toggle, `cmd/byok` steps 2–4; catalogue gains `price_billing_unlimited_internal_monthly` and the
   pilot subscription (`api/cmd/seed/run.sh prod`).
5. **billing part B:** due-date charge collection, webhook route, confirmation, refunds, sweep (step 1) and promotion
   (step 5); live stays `pending_clearance`.
6. **On the lawyer's answer:** ADR 0030 + `BYOK_LIVE_COLLECTION=true`, `BYOK_LIVE_PROVIDERS=inter` in one reviewed
   pull request; the pilot first.

Each part deploys on its own; part A without B changes nothing about money.

## 17. Tests and review focus

Part of done, not later hardening:

1. **No wallet on the BYOK rail:** a BYOK invoice issued, paid, refunded and voided with a wallet fake that fails on
   any call; zero calls.
2. **Legal gate:** with the flag off, or the provider not listed, a live BYOK tenant gets `byok_collection_not_cleared`
   from `AuthorizeCharge`, and a live `collect` op sent straight to the Lambda is refused there too; read and teardown
   ops still work; removing the provider from the list after `enabled` blocks the next charge at once.
3. **Single gate:** every collection path (finalization, checkout, new attempt, refund) goes through
   `AuthorizeCharge`; a table test over rail × provider × status × connection state × flag × list × mode × quota; Grátis refuses past 50 charges, a paid plan never refuses.
4. **Credentials:** the raw DynamoDB item holds only `b1.` ciphertext; responses, logs and audit rows never contain
   secret, key or token (a log-capture test); the `api` role has no read on the credentials-key parameter (a Terraform
   plan assertion); a bundle copied onto another connection fails to open (AAD); the Lambda refuses to start without
   the key.
5. **Webhook spoofing:** wrong secret, another connection's hook with a valid reference of this one, a reference of
   another organization, a provider segment that does not match the hook, an account mismatch, a payload claiming
   payment for an open charge — none settles anything; both path variants (`…/pix` and `…/pix/pix`) reach the handler;
   the secret compare is constant time.
6. **Re-query before paid:** settlement only from `GetCharge`; three concurrent wake-ups settle once.
7. **Tokens:** 50 concurrent calls on a cold cache → one fetch; a forced `401` → one refresh and one retry; the limiter
   never lets a fourth fetch out in a minute; the sandbox's shared limiter answers `byok_sandbox_busy`.
8. **Renewal:** swap under load with no failed call; warnings at D-90/D-30/D-7 sent once each, reset by a renewal.
9. **Disconnect:** order (charges, webhook, credentials, gate); refuses with open charges without the flag; never
   deletes a webhook that is not CTech's; a late callback afterwards is `404`.
10. **Webhook conflict:** an existing foreign URL is not overwritten without `replace_existing`; the daily audit marks
    `overwritten`.
11. **Statement:** syncing the same range twice adds nothing; after backfill on a fixture, the ledger balance equals the
    fixture's balance; a paid BYOK invoice and its statement line produce **one** cash entry whichever arrives first;
    classification never moves the balance; manual movements into the account answer `account_is_synced`.
12. **Test-mode isolation:** tenant B's test space never receives a line of tenant A's sandbox charges; no row crosses
    modes; a live connection in `dev` refuses production credentials.
13. **Spaces:** personal spaces, personal workspaces, members, viewers and other organizations get `404`/`403` from
    every connection route, with zero table reads before membership (ADR 0025).
14. **Provider seam:** the `api`'s services compile against `byok.Provider` only (no import of `api-commons/inter`
    outside the Lambda); a fake provider runs the whole connect → charge → confirm → statement flow.
15. **Usage:** a charge cancelled within 24 hours is never reported; one surviving 24 hours is reported once whatever
    the number of job runs; test-mode charges are never reported.

## Amendment 2026-10-11 — test mode uses CTech's existing Inter integration

Owner correction after review. Inter integrations do **not** come in a separate sandbox kind: the same integration's
credentials (client_id, client_secret, certificate and key) are used, and only the API base endpoint changes
(`https://cdpj-sandbox.partners.uatinter.co` instead of `https://cdpj.partners.bancointer.com.br`). So test mode does
not require creating a sandbox integration: it uses the integration CTech already has for ctech-wallet, against the
sandbox host.

- The outbound Lambda's IAM role is **granted read** on the wallet's four parameters
  (`/ctech-wallet/{env}/inter/client-id`, `client-secret`, `mtls-cert`, `mtls-key`), read once per cold start. The
  values are never copied, printed or read by a person or a script (an IAM grant was preferred over a copy: one source
  of truth, nothing to keep in step on the wallet's renewal).
- The 30-day sandbox-certificate assumption (parecer § 2.6) is dropped; operator warnings follow the real `notAfter`
  of that integration's certificate.
- Risk, recorded and not designed around: the parecer quotes Inter's portal describing a separate sandbox portal with
  30-day certificates. If Inter's sandbox refuses the production integration's credentials, test-mode ops fail with
  `byok_provider_unavailable`, and the fallback is a dedicated sandbox integration whose values go into billing's own
  SecureStrings (only the Lambda's parameter names change; see `docs/runbooks/byok.md`).
- D9, § 9 and § 12.3 above are edited accordingly. Plans: `docs/plans/2026-10-11-byok-phase-1-{a,b,c}-*.md`.

## Amendment 2026-10-11 (second) — no legal gate

Owner decision. **There is no legal gate on BYOK collection.** The merchant grants CTech access to its own account
through the bank's **official** API (Inter's PJ APIs, with the merchant's own integration, scopes and terms acceptance);
this is an authorized integration, not scraping, and needs no separate clearance before live collection.

What this removes, everywhere in this spec:

- **D11** is withdrawn. `BYOK_LIVE_COLLECTION` and `BYOK_LIVE_PROVIDERS` do not exist: no SSM parameter, no Terraform
  variable, no `api` or Lambda environment variable (§ 10, § 12.3, § 16 steps 3 and 6).
- **ADR 0030** is not planned. Parecer § 8's questions stay useful context for the terms and LGPD wording (the
  merchant's authorization in the connect form, § 3.2, is kept), but they gate nothing.
- **§ 4.1, the Lambda allowlist:** kept, as a **security control only**. The Lambda accepts exactly the known ops
  (`byok.GroupOf`), for a registered provider and mode; an unknown op or provider never reaches a bank, and
  `SimulatePayment` runs only against a sandbox. A live `collect` op is **not** refused for legal reasons.
- **§ 7.1, the gate:** `payout_status` gains **no** `pending_clearance` value. A live tenant that chooses a
  collection-capable connection goes straight to `byok` + provider + `enabled` (still subject to the entitling
  `prod_billing` subscription, R4, and to suspension). `AuthorizeCharge` keeps every other condition and loses
  `byok_collection_not_cleared`; `cmd/byok` step 5 (promotion) does not exist; webhook registration happens on
  connect / choosing collection, in live as in test (§ 6.4).
- **§ 11.2:** no "em liberação" state; the capabilities show "Cobrança" when the collection scopes are granted.
- **§ 12.6, the pilot:** live collection for the pilot no longer waits for a clearance; moving tenant zero's
  `payout_rail` to `byok` stays a deliberate, audited operator action.
- **§ 17 item 2** becomes: an unknown op, provider or mode sent straight to the Lambda is refused there;
  `SimulatePayment` is refused outside a sandbox.

Implemented in part A's pull request (config, Terraform and the Lambda); part B's plan is adjusted in the same pull
request. The owner also allowed loading the wallet's Inter SSM parameters into environment variables for scripts and
tests (never printed or inspected).

## Amendment 2026-10-13 — the paid plans' fee is billed in arrears

§ 12.5 put each paid plan on one subscription with an **advance** fixed fee and an **arrears** metered overage. ADR 0018
refuses that: one subscription has one billing timing. The paid plans (Básico, Crescimento, Pro) therefore bill their
fixed fee **in arrears**, on the same invoice as the period's overage. A paid plan is active as soon as it is chosen,
and the merchant pays fee and overage together at the end of the period; dunning applies as to any invoice. Grátis and
early access (R$ 0) stay advance. No new invoicing feature; a mixed-timing subscription stays out of scope.
