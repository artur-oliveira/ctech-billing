# Spec — BYOK Inter, phase 1: the connection, statement sync and Pix with due date

Status: **Draft for review** · 2026-10-11 ·
Factual basis: [BYOK Inter parecer](../analysis/2026-10-11-byok-inter-parecer.md) (cited below as "parecer § n") ·
Builds on: [`2026-10-07-finance-erp-design.md`](2026-10-07-finance-erp-design.md) (§ 1 tiers, § 3 accounts and
posting, § 3.8 billing integration, § 12 item 2), [`2026-10-10-console-self-service.md`](2026-10-10-console-self-service.md)
(merchant tenants, applications, `payout_status`, credentials), [ADR 0003](../adr/0003-tenant-and-livemode-partition-key.md)
(mode isolation), [ADR 0004](../adr/0004-pix-on-invoice-via-wallet.md) (the wallet rail),
[ADR 0005](../adr/0005-payout-gate.md) (payout gate), [ADR 0017](../adr/0017-field-level-encryption.md) (field
encryption), [ADR 0024](../adr/0024-billing-keeps-a-management-ledger.md) (management ledger),
[ADR 0025](../adr/0025-spaces-personal-and-organization.md) (spaces) ·
Reused from ctech-wallet: `pix-gateway` (outbound Lambda, mTLS webhook stack), the Inter token manager spec ·
Decision records: **ADR 0029** (this spec's decisions, written when the spec is approved) and **ADR 0030** (legal
clearance of live BYOK collection, written only when the lawyer answers, § 10)

## 1. What this is

An organization that holds an **Inter Empresas (PJ) account** connects its own Inter API integration to CTech. With
that connection:

1. **The connection.** The merchant's client_id, client_secret and mTLS certificate + key, stored encrypted; a
   per-connection token cache that respects Inter's 5 tokens/minute limit and 60-minute tokens; an IPv4 outbound
   path (billing hosts have no public IPv4, Inter has no IPv6); the annual certificate renewal with warnings at
   90/30/7 days; health and disconnect.
2. **Statement and balance sync into Finanças.** The organization's Finanças space gains a ledger account of a new
   kind, **Inter (BYOK)**, whose statement is synced from Inter and whose balance is Inter's.
3. **Pix with due date (`cobv`) to collect billing invoices.** A merchant tenant's invoices get a `cobv` created on
   the merchant's own account; the payer pays the merchant's bank directly; billing marks the invoice paid after
   re-querying Inter.

The connection belongs to the **ctech-account organization** (Inter's APIs are PJ-only, parecer § 2.1). One
connection per organization per mode serves both that organization's billing tenant (collection) and its Finanças
space (statement). Personal spaces do not connect in phase 1.

What it is **not**:
- **Not the wallet.** BYOK never creates a wallet, a wallet balance or a wallet ledger entry (D2).
- Not Boleto com Pix / Cobrança v3 (phase 2) and not Pix Automático for subscriptions (phase 3). § 13 names the seams
  this phase leaves for them.
- Not `lotecobv`, not any payment scope (`pagamento-*`), not other banks or PSPs.
- Not a change to tenant zero: CTech's own invoices keep the wallet rail (ADR 0004).
- Not a pricing decision: the Finanças "Custom (BYOK)" tier has no price yet (D17).
- Not the wallet's migration to the shared Inter package (separate work, D15).

## 2. Decisions

The owner's decisions are D1–D13; D14 onwards are this spec's, each argued where it is specified.

| # | Decision |
|---|---|
| D1 | Phase 1 = the connection + statement/balance sync into Finanças + `cobv` collection of billing invoices. Phases 2 (Cobrança v3) and 3 (Pix Automático) are out, with named seams (§ 13). |
| D2 | **BYOK never touches the CTech Wallet.** No wallet, no wallet balance, no wallet ledger entry for BYOK money. Transaction control lives in billing (charges and their status per invoice) and Finanças (the bank account with synced statement, balance from Inter). A wallet would be a second balance that diverges from the bank, and would look like custody. |
| D3 | The connection belongs to an **ctech-account organization**; it serves the organization's billing tenant and its Finanças space. Personal spaces do not connect. |
| D4 | Credentials (client_id, client_secret, certificate, key) are stored encrypted in the ADR 0017 discipline; refined in D14. |
| D5 | Per-connection token cache honouring 5 tokens/min and 60-minute tokens; IPv4 egress through a billing-owned Lambda modelled on the wallet's `pix-gateway`. |
| D6 | Annual certificate renewal flow with warnings at **D-90, D-30, D-7**. |
| D7 | Disconnect deletes CTech's webhooks at Inter, then the credentials, and tells the merchant that **only they** can cancel the integration at Inter. |
| D8 | Webhooks: **one CTech host** for every merchant; per-connection opaque secret **in the URL path**; `x-conta-corrente` check; **re-query Inter before marking anything paid**; DNS-only (not proxied by Cloudflare); mTLS truststore holding Inter's CA(s); product paths handled (§ 6). Recovery through Inter's callbacks/resend endpoints; a reconciliation sweep is the safety net. |
| D9 | **Test mode** uses one CTech-owned Inter sandbox integration for all tenants (sandbox certificates last 30 days). Live mode uses each merchant's integration. |
| D10 | Shared code is a new **`ctech-go-common/inter`** package, credentials passed per call. Billing and wallet keep separate Lambdas and secrets. |
| D11 | **Legal gate:** live-mode BYOK collection cannot be enabled until the owner records the lawyer's clearance (a config flag that defaults off in live, plus ADR 0030). Read-only statement sync may go live without it (§ 10 agrees, with conditions). |
| D12 | `payout_status` (ADR 0005) stays the per-merchant gate; BYOK is a **new rail**, enabled per connection rather than by CTech custody. The existing gate and its single function are kept. |
| D13 | Onboarding guides the merchant through creating the Inter integration, waiting "Em validação", uploading, then shows health and certificate expiry, in the console and in Finanças. Routes in English, Portuguese only in i18n. |
| D14 | **Credentials are sealed under a dedicated KMS key that the internet-facing `api` can encrypt with but never decrypt**; only the outbound Lambda decrypts (§ 3.3). |
| D15 | The `api` owns the token lifecycle (single-flight in Valkey, sealed token), as the wallet's token manager does; the Lambda is a transport that also performs the token fetch (§ 4.3). |
| D16 | The webhook URL carries a **product segment chosen at registration** (`…/pix`), and the receiver accepts both the registered path and that path with Inter's documented suffix; the payload parser is chosen by the registered segment, never by the suffix (§ 6.2). |
| D17 | Connecting is **not gated by a plan** in phase 1; the connect route is the single place a future `finance_byok` entitlement check goes. |
| D18 | A `cobv` is created when the invoice is **finalized** (and on demand at checkout if that failed), one per payment attempt, with a deterministic txid. |
| D19 | No CTech per-charge ceiling on the BYOK rail; ADR 0004's ceiling stays on the wallet rail only. |
| D20 | **Excess and duplicate Pix are never refunded automatically**: the money is the merchant's; the console flags it and the merchant decides (answers parecer § 11, owner question 3). |
| D21 | The Inter (BYOK) account is **statement-driven**: every synced line posts automatically; unclassified lines sit on two system categories until the person classifies them; manual cash movements into the account are refused. Its opening balance is Inter's balance on the day before the first synced day. |
| D22 | Statement sync uses Inter's **page pagination, never scroll**: Inter allows one active scroll per account, and the merchant's own ERP may be using it. |
| D23 | In test mode the shared sandbox statement is **filtered to lines carrying the tenant's own txids**; no other line enters a test space. |
| D24 | The merchant is asked to tick the **full phase-1 scope list** at integration creation; CTech uses only what the gates allow. A statement-only integration (`extrato.read`) is accepted too. |
| D25 | Webhook conflict default: `GET` before `PUT`; a webhook URL that is not CTech's is **never overwritten without the merchant's explicit confirmation**; a daily check detects being overwritten later. |

## 3. The connection

### 3.1 Ownership and keys

- One connection per **(ctech-account organization, mode)**. Its partition key is the organization's Finanças space
  key, `{account_organization_id}#{mode}` (finance spec § 2), so the same resolver that guards Finanças guards the
  connection (ADR 0025: zero reads before membership, one 404).
- Only `kind = organization` workspaces connect. `personal` and personal workspaces (ADR 0027) answer `404`, the
  same body as an unknown space.
- Connect, renew, re-register the webhook, change collection settings and disconnect need `owner` or `admin`
  (`finance.configure`). Members and viewers see the Inter (BYOK) account in Finanças like any account, not the
  connection screen.
- The billing tenant is found from the organization by the existing lookup `{mode}#ACCOUNT_ORG#{id}` (console spec
  § 3). An organization with **no billing tenant** may connect for statement sync alone; collection additionally
  needs the tenant (and, in live, an approved one).

### 3.2 What the merchant gives

| Field | Notes |
|---|---|
| `client_id`, `client_secret` | shown once by Inter at download (parecer § 2.1) |
| certificate (`.crt`) and private key (`.key`) | PEM; one pair per integration, valid 1 year (parecer § 2.2) |
| `account_number` | the conta corrente, digits with check digit, no leading zeros; sent as `x-conta-corrente` when the integration has several accounts, and compared on every webhook (§ 6.3) |
| `pix_key` | collection only: the key `cobv` charges are created on; the console recommends a **dedicated random (EVP) key** because Inter's Pix webhook is one per key (parecer § 6.5) |
| `backfill_months` | statement history to import, 3 (default) to 12 (§ 8.5) |

Validation at upload, before anything is stored:
1. PEM parses; the key matches the certificate; `notAfter` is in the future (or within Inter's 90-day post-expiry
   renewal window, which is refused with `inter_certificate_expired` and a renewal hint).
2. **Connection test** through the Lambda with the uploaded bundle: a token for the phase-1 scope set; if Inter
   refuses that set, a token for `extrato.read` alone (statement-only integration, D24); then `GET /saldo` (proves
   `extrato.read` and the account number).
3. The granted scopes become the connection's **capabilities**: `statement` (`extrato.read`), `collection`
   (`cobv.read`, `cobv.write`, `pix.read`, `pix.write`, `webhook.read`, `webhook.write`, all present). Anything
   less than the collection set means `statement` only, and the console says which scopes are missing.

The merchant also confirms, in the form, that the integration is their own and that they authorize CTech to use it
on their behalf (stored as `authorized_by`, `authorized_at`, and the wording's version). § 10 explains why this is
required even for the read-only path.

### 3.3 Storage (D4, D14)

- One sealed **bundle** per connection: `{client_id, client_secret, cert_pem, key_pem}` as JSON, encrypted with
  AES-256-GCM under a data key from a **dedicated KMS key** (`alias/{env}-ctech-billing-inter-credentials`), with an
  encryption context `{tenant: account_organization_id, mode, connection_id, version}`. Stored as `k1.` + base64
  (ADR 0017's discipline: a version prefix, no development fallback key, `Open` refuses an unprefixed value, and an
  integration test reads the raw item to prove it is not plaintext).
- **IAM:** the `api` EC2 role may `kms:GenerateDataKey` (to seal at upload) and **not** `kms:Decrypt`. Only the
  outbound Lambda's role has `kms:Decrypt`, conditioned on the encryption context keys being present. The `api` never
  reads a merchant's credentials back after the request that uploaded them.
- **Why not ADR 0017's `FIELD_ENCRYPTION_KEY` as is.** That key is one per deployment and every binary holding it can
  decrypt every sealed field. These credentials can create charges and read statements on N companies' bank accounts;
  a key the internet-facing process cannot use to decrypt bounds a full `api` compromise to "can write new
  credentials", not "can read every merchant's". The format and the refusal rules are ADR 0017's; only the key's
  custody differs. ADR 0029 records this as an extension of ADR 0017. **If the owner prefers the plain sealer**, the
  fallback is the ADR 0017 sealer with the Lambda also holding `FIELD_ENCRYPTION_KEY`, at the cost above.
- The `api` passes the **ciphertext** in each Lambda invocation; the Lambda decrypts once per container per
  `(connection_id, version)` and keeps the plaintext in memory for at most 15 minutes. That is one KMS call per warm
  container per connection per 15 minutes, not one per Inter call (the family's KMS-cost concern, parecer § 10.4).
  The Lambda has no DynamoDB access.
- Logs, errors, audit rows and API responses never carry the secret, the key or the token. The console shows
  client_id, the certificate's subject, fingerprint and `notAfter`.

### 3.4 States and health

```
connection.state: active | expired | disconnected      (pending states are UI-only, § 11.1)
webhook_status:   not_registered | registered | conflict | overwritten
```

Health fields on the row: `last_token_ok_at`, `last_sync_ok_at`, `last_call_error` (code and time),
`cert_not_after`, `cert_fingerprint`, `capabilities`, `scopes_granted`, `webhook_status`, `consecutive_failures`.
`active` with `consecutive_failures ≥ 3` (token refused after the forced refresh, or 5xx from Inter) is shown as
**degraded** and alerts the organization's owners/admins once per day; collection keeps trying, since a refused
call never marks anything paid.

### 3.5 Certificate renewal (D6)

- Inter opens renewal 90 days before expiry and allows it until 90 days after; renewal issues a **new `.crt` and
  `.key`** and keeps client_id, client_secret and scopes (parecer § 2.2). Only the merchant can renew, in Inter's
  Internet Banking.
- The hourly job (§ 5) sends warnings at **D-90, D-30 and D-7** (and on the day it expires): e-mail to the
  organization's owners and admins (SES, as dunning does), a banner in the console and in Finanças, each once,
  guarded by `warned_90`, `warned_30`, `warned_7`, `warned_expired` flags reset by a renewal.
- `POST …/connection/certificate` with the new pair: same validation as § 3.2 with the stored client_id/secret (the
  Lambda decrypts the current bundle and combines it with the new pair; the `api` never sees the secret). On success
  a new bundle `version` is written in one conditional update; the old version is discarded. No downtime: calls in
  flight finish with the old pair, the next ones use the new.
- Past `notAfter`, `state = expired`: collection is refused (`inter_certificate_expired`, § 7.1) and sync pauses.
  Upload of a renewed pair returns it to `active`.

### 3.6 Disconnect (D7)

`DELETE …/connection`, owner/admin, in this order:

1. **Open charges.** Live `cobv` charges still `ATIVA` would keep being payable at Inter with nobody to confirm them.
   Disconnect refuses with `409 inter_open_charges` (count included) unless the body has `cancel_open_charges: true`;
   then each is removed (`PATCH status=REMOVIDA_PELO_USUARIO_RECEBEDOR`) and its attempt becomes `FAILED`.
2. **Webhooks.** `GET /webhook/{pix_key}`; if the registered URL is CTech's, `DELETE` it. A URL that is not CTech's is
   left alone.
3. **Credentials.** The bundle and the Valkey token are deleted; the hook row is deleted, so any late callback gets
   `404`.
4. **Gate.** The tenant's `payout_status` returns to `not_configured`, `payout_rail` cleared (audited).
5. **Finanças.** The Inter (BYOK) account keeps its history and becomes read-only (`sync_state: disconnected`).
   Reconnecting the **same** account number resumes on it from its last synced day; another account number gets a
   new ledger account.
6. **The merchant is told** (console and e-mail): CTech no longer holds the credentials, but **only they can cancel
   the integration**, in Inter's Internet Banking (Integrar → the integration → Cancelar), and cancelling is
   irreversible.

If Inter cannot be reached at step 1 or 2, the console offers "Desconectar mesmo assim", which skips them and tells
the merchant which webhook and charges to remove themselves. Closing the organization (ADR 0026 purge) runs this
path with `cancel_open_charges: true`.

## 4. Reaching Inter

### 4.1 The outbound Lambda (D5)

`{env}-ctech-billing-inter-outbound`: Go, arm64, **not VPC-attached** (cheap IPv4 egress, parecer § 9 row 12), invoked
synchronously by the `api` and the jobs with `lambda:InvokeFunction` — the wallet's `pix-gateway/cmd/outbound`
shape. It is the only billing code that opens a connection to Inter. Separate from the wallet's function, role and
secrets: one mixes CTech's custodied money with merchants' credentials, and the other must not (parecer § 10.3).

Envelope:

```json
{ "op": "CreateCobV", "mode": "live",
  "connection": { "id": "…", "version": 3, "sealed": "k1.…", "account_number": "…" },
  "oauth_token": "…", "payload": { … } }
```

In test mode `connection` is `{ "id": "sandbox" }` and the Lambda uses CTech's sandbox credentials from SSM (§ 9).

Phase-1 ops: `Token`, `Balance`, `Statement` (one page), `CreateCobV`, `GetCobV`, `PatchCobV`, `GetPix`, `Refund`,
`GetRefund`, `GetWebhook`, `PutWebhook`, `DeleteWebhook`, `ListCallbacks`, `RetryCallbacks`, `SimulateCobVPayment`
(sandbox only).

**Op allowlist in the Lambda (defense in depth for D11).** Ops are grouped:
- `read`: `Token`, `Balance`, `Statement`, `GetCobV`, `GetPix`, `GetRefund`, `GetWebhook`, `ListCallbacks`,
  `RetryCallbacks` (asks Inter to resend; creates nothing);
- `teardown`: `PatchCobV` when it only removes a charge, `DeleteWebhook` — always allowed, so disconnecting and voiding
  work whatever the flag says;
- `collect`: `CreateCobV`, `PatchCobV` changing a value, `Refund`, `PutWebhook`.

A live `collect` op is refused inside the Lambda unless its own `LIVE_COLLECTION_ENABLED` is `true` — the same SSM
parameter the `api` reads (§ 10). A bug in the `api`'s gate still cannot create a live charge. `SimulateCobVPayment`
is refused outside sandbox.

### 4.2 Base URLs

Production `https://cdpj.partners.bancointer.com.br`, sandbox `https://cdpj-sandbox.partners.uatinter.co`
(parecer § 2.3). **Non-production environments point every mode at the sandbox**; a live connection in `dev` accepts
only sandbox credentials. Merchants' real credentials exist only in `prod`.

### 4.3 Tokens (D15)

- The `api` owns the lifecycle, as the wallet's token manager spec does: it asks the Lambda for `Token`, caches it
  and passes it in `oauth_token` with every op.
- Cache: Valkey (billing's DB 3), key `inter:token:{mode}:{connection_id}:{version}:{scopeset}`, value sealed with
  ADR 0017's sealer (a bearer for a merchant's account is not stored in clear even for an hour), TTL
  `expires_in − 300 s`.
- **Single-flight:** a `ctech-go-common/lock` lock per key; the loser waits and reads the cache.
- **Local limiter:** at most **3 token fetches per minute per connection** (and for the shared sandbox integration),
  counted in Valkey, below Inter's 5/min. Whether Inter counts per integration, per account or per source IP is open
  (§ 15); 3/min per connection is safe for one integration per merchant, and the per-IP reading is mitigated by
  needing ~1 fetch per connection per hour.
- On `401` from an op: one forced refresh and one retry; a second `401` counts as a failure (§ 3.4).
- A token is fetched with the scope set of the capability in use (`extrato.read` alone for statement sync, the
  collection set for charges), so a statement sync never holds a token that can create charges.

## 5. Jobs

One new binary, **`cmd/inter`**, hourly on the leader (crontab with `@reboot` catch-up, like `cmd/reconcile`; the
userdata ceiling note in the finance spec § 5 applies). Cross-tenant by construction, so it has no HTTP surface
(ADR 0002, as `cmd/sweep`). It reads connections through a sparse `jobs-index` partition `{mode}#inter-connections`.
Steps, each idempotent and re-runnable with `-date`:

1. **Charge sweep (safety net, D8):** every BYOK attempt still `PENDING` is re-queried (`GET /cobv/{txid}`) when its
   `checked_at` is older than 1 hour (checkout opened in the last 48 hours) or 24 hours (otherwise). The result goes
   through the same confirmation path as a webhook (§ 7.4).
2. **Statement sync** (§ 8.4).
3. **Certificate warnings** (§ 3.5) and expiry transitions.
4. **Webhook audit (daily, first run after 06:00 São Paulo):** `GET /webhook/{pix_key}` → `overwritten` when the URL is
   no longer CTech's (alert + banner; payments are still confirmed by step 1); `GET /webhook/callbacks` for the last
   24 hours → any delivery with a non-2xx `httpStatus` raises an operator alert (the webhook path is broken
   somewhere) and its txids are re-queried at once.
5. **Promotion to `enabled`** of `pending_clearance` tenants when the live flag is on (§ 7.1), registering their webhook (§ 6.4).

`cmd/inter -retry-callbacks -since=…` is the **recovery tool** after an outage of the webhook host: it lists failed
callbacks per connection and asks Inter to resend them (`POST /webhook/callbacks/retry`, ≤ 50 txids per call,
≤ 5 calls/min, history kept 1 month; parecer § 6.1). It is an operator command, not a cron step: in normal operation
step 1 already confirms everything the webhook missed.

## 6. Webhooks (D8)

### 6.1 Host and edge

- One host for every merchant: **`billing-inter-hooks.aoctech.app`** (prod), `billing-inter-hooks-{env}.aoctech.app`
  elsewhere. A single label under `aoctech.app` so the existing `*.aoctech.app` certificate covers it.
- API Gateway **HTTP API**, **regional** custom domain with **mTLS**: truststore in a versioned S3 bucket holding
  **Inter's CA bundle** (the `ca.crt` downloaded from CTech's own Inter account, the sandbox one, and any further CA a
  pilot merchant's download turns out to contain — § 15 item 1). `disableExecuteApiEndpoint: true`. This is the
  wallet's `pix-gateway-stack.ts` pattern in Terraform (ADR 0010).
- The Cloudflare record is a **DNS-only** CNAME to the regional target: proxied, Cloudflare would terminate TLS and
  Inter's client certificate would never reach API Gateway (parecer § 6.4).

### 6.2 Paths (D16)

The URL registered at Inter is `https://{host}/w/{hook_id}/{secret}/{product}`:

- `hook_id`: 26-character ULID identifying the connection's hook row; `secret`: 32 random bytes, base64url (≥ 128 bits,
  parecer § 6.4). The secret is in the **path**, never in the query.
- `product`: `pix` in phase 1; reserved `boleto` (phase 2, Cobrança v3), `rec` and `cobr` (phase 3).
- Accepted routes for phase 1: `POST /w/{hook_id}/{secret}/pix` **and** `POST /w/{hook_id}/{secret}/pix/pix`.
  Inter's documentation says it appends `/pix` (parecer § 6.1); production evidence from the wallet says Inter calls the
  Pix URL **exactly as registered** (parecer § 6.6, corrected 2026-10-11). Accepting both costs one route and holds
  whichever is true for a given account. For phase 3, `…/rec` and `…/rec/rec`, `…/cobr` and `…/cobr/cobr` will be
  accepted the same way; Cobrança v3 has no documented suffix.
- The payload parser is chosen by the **registered product segment**, never by the suffix, so a Pix payload can never
  be read as another product's.
- Any other path: `404` at API Gateway (default route), never reaching the Lambda.

### 6.3 The webhook Lambda and the notify route

`{env}-ctech-billing-inter-webhook` holds **no Inter credentials and no tenant secrets** (the wallet's split). It
parses the path and the minimal body (`pix[].txid`, `pix[].endToEndId`), reads `x-conta-corrente`, and calls billing's
**`POST /v1.0/internal/inter/notify`** on the public API host with an M2M token (new ctech-account client
`billing-inter-webhook`, scope `internal:billing:inter-notify`, published in billing's manifest, not `m2m`-marked for
merchants):

```json
{ "hook_id": "…", "secret": "…", "product": "pix", "account_header": "…", "txids": ["…"], "e2e_ids": ["…"] }
```

The `api`:
1. Reads `HOOK#{hook_id}`; compares the SHA-256 of `secret` with the stored hash **in constant time**; a previous
   secret is also accepted for 24 hours after a rotation (Inter's retries carry the URL of the moment they fired).
   Unknown hook or wrong secret → `404`, and an operator alert at most once per hour per hook.
2. Compares `account_header` with the connection's `account_number` (when the header is present; the shared sandbox
   hook compares with CTech's sandbox account). Mismatch → the call is discarded with `200` (a retry cannot make it
   right) and an alert is raised.
3. For each txid: the pointer `TXID#{mode}#{txid}` must exist **and belong to the hook's organization** (in live) —
   otherwise it is ignored and counted. Then **re-query Inter** (`GET /cobv/{txid}`) with the connection's
   credentials and run the confirmation (§ 7.4). The payload's amounts, status and payer are never used.
4. Answers `200` when every txid was confirmed, ignored or found still open; `503` when a re-query failed, so Inter
   retries (4 attempts over ~4 hours, parecer § 6.1) while the hourly sweep also covers it.

### 6.4 Registration and conflict (D25)

On connect (collection capability; in live only once the flag is on, so `cmd/inter` step 5 registers it at promotion) and on "Registrar aviso novamente":

1. `GET /webhook/{pix_key}`.
2. None registered → `PUT` CTech's URL; `webhook_status = registered`.
3. CTech's URL already (this hook) → nothing to do.
4. Another URL → `webhook_status = conflict`; the console shows the existing URL's host (the merchant's own
   configuration) and explains that registering CTech's will **stop that system from receiving Pix notices for this
   key**, recommending a dedicated EVP key instead. Only `POST …/connection/webhook` with `replace_existing: true`,
   sent after that confirmation, overwrites it (audited with the replaced host).
5. Re-registering rotates the secret (new hash, previous kept 24 hours).

Without a registered webhook collection still works — the sweep confirms within the hour — and the console says
confirmation will be slower. The `conflict` state does not block collection.

Whether a Pix webhook belongs to the integration or to the key/account is open (§ 15 item 4); the check-before-put and
the daily audit (§ 5 step 4) are correct under either answer.

## 7. Collection by Pix with due date (`cobv`)

### 7.1 The gate (D11, D12)

`Organization` gains `payout_rail: "" | wallet | inter_byok` (`""` reads as `wallet`, so every existing tenant is
unchanged) and `payout_status` gains one value:

```
payout_status: not_configured | pending_custody | pending_clearance | enabled
```

| Event | Live tenant | Test tenant |
|---|---|---|
| Connection becomes collection-capable (the webhook is not required, § 6.4) | `inter_byok` + `pending_clearance` when the live flag is off; `enabled` when on | — |
| Merchant turns on "Cobrança via Inter (teste)" (§ 9) | — | `inter_byok` + `enabled` |
| Live flag turned on | `cmd/inter` step 5 promotes `pending_clearance` → `enabled` (audited) | — |
| Disconnect | `not_configured`, rail cleared | same |

`Organization.AuthorizeCharge` stays **the single function** every collection path calls. For `inter_byok` it
returns nil only when **all** hold, each with its own reason (409, `problem` code):

- `payout_status == enabled` (`payout_not_enabled`, as today);
- the connection for the mode is `active` with the `collection` capability (`inter_not_connected`,
  `inter_certificate_expired`, `inter_collection_scopes_missing`);
- in live: the flag `INTER_BYOK_LIVE_COLLECTION` is `true` **at call time** (`byok_collection_not_cleared`) — checked
  even though the stored status already encodes it, so turning the flag off stops collection at once without a
  migration;
- in live: the tenant is approved and not suspended (console spec § 4.3, unchanged).

The flag is the ecosystem-wide capability, the status is per merchant — exactly ADR 0005's two gates
(`AsaasCustodyEnabled` / `payout_status`), with the wallet's custody replaced by the lawyer's clearance. `pending_custody`
keeps its wallet meaning. Tenant zero is untouched (rail `wallet`).

### 7.2 The charge (D18, D19)

- A BYOK attempt is a `PaymentAttempt` with the new method **`inter_cobv`**; `charge_id` = the txid. One attempt per
  invoice at a time; a new attempt only after the previous one is `FAILED`.
- **txid** = `ctb` + the attempt's ULID (29 characters of `[A-Za-z0-9]`, inside Bacen's 26–35 rule, unique per
  receiver because ULIDs are). Written with the attempt, in the same transaction, is the pointer
  `TXID#{mode}#{txid}` → `{tenant, account_organization_id, invoice_id, attempt_id}`; it is how a webhook and a
  statement line find their invoice (and, in the shared sandbox, their tenant).
- **When:** on invoice **finalization**, when `AuthorizeCharge` passes for the BYOK rail. If creation fails, the
  invoice is still issued and the attempt stays `PENDING` without a `pix_copia_e_cola`; checkout (`Collector.Pay`)
  and the hourly sweep retry `PUT /cobv/{txid}` (idempotent: an existing txid is read back with `GET`). Zero-total
  invoices create nothing (ADR 0019).
- **Body** (`PUT /cobv/{txid}`, parecer § 3.2):
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
- **No CTech ceiling (D19).** ADR 0004's ceiling bounds the damage of one forged request opening a charge into CTech's
  pooled account; a BYOK charge pays the merchant's own account, and `MaxChargeCents` mirrors the wallet's limit, which
  does not apply. Inter's own format bounds the value.
- The response's `pixCopiaECola` is stored on the attempt; the QR image is rendered by billing (the wallet's `qr.go`,
  through the shared package). Checkout, the invoice e-mail and the invoice PDF show it with the due date and "pague
  até {due + validity}".

### 7.3 Changes to an open invoice

- **Void:** `PATCH /cobv/{txid}` `status=REMOVIDA_PELO_USUARIO_RECEBEDOR` (no other change in the same request); a
  `409` because it was concluded meanwhile → the re-query decides, and a paid charge wins over the void (the
  invoice's existing paid-after-void handling applies).
- **Credit note on an open invoice:** `PATCH` `valor.original` to the new `amount_due` while `ATIVA`; refused by Inter
  (already concluded) → re-query.
- **Validity ends unpaid** (`GET` shows expired / `REMOVIDA_PELO_PSP`): attempt `FAILED`; the invoice follows its dunning
  schedule's end as today. The console can open a new attempt ("Gerar novo Pix", new due date).

### 7.4 Confirmation

One function, called by the webhook route, the sweep and the statement sync (three wake-ups, one decision):

1. `GET /cobv/{txid}` through the Lambda (never the payload).
2. `CONCLUIDA` with `pix[]`: the first Pix settles the attempt `SUCCEEDED` (`Collector.settleInvoice`, unchanged
   downstream: subscription activation, settlement bus, finance posting). Its amount must be ≥ `amount_due`; the
   difference is the late charge (`componentesValor` multa + juros) recorded on the payment and posted in Finanças to
   the seeded *Juros e multas* category. Less than `amount_due` → `ErrAmountMismatch` alarm (existing), never a
   partial payment.
3. Any further Pix on the same txid, or an amount above `amount_due` + computed late charges → an **excess payment**
   row on the invoice (D20), a console notice ("Pagamento em duplicidade") and an e-mail to owners/admins. Nothing is
   refunded automatically.
4. Still `ATIVA` → nothing; `checked_at` updated.

Idempotent by attempt status (`PENDING → SUCCEEDED` is the existing guarded transition): three wake-ups for the same
payment settle it once.

### 7.5 Refunds (D20)

Merchant-initiated from the console (owner/admin), gated like collection (`Refund` is a `collect` op):
- an **excess payment**: "Devolver" refunds that Pix (`PUT /pix/{e2eId}/devolucao/{id}`, `natureza ORIGINAL`);
- a **paid invoice**: a credit note may carry "devolver via Pix", refunding up to the credited amount from the
  invoice's Pix.

The devolution `id` is derived from the excess row or credit note (`[a-zA-Z0-9]{1,35}`, Inter's idempotency key); status
(`EM_PROCESSAMENTO → DEVOLVIDO | NAO_REALIZADO`) is followed by `GetRefund` in the hourly sweep. The 90-day window is
checked before the call (`refund_window_closed`). Finanças posts the refund as a cash-out of the Inter (BYOK) account
against the original category (a reversal for the credited part), linked to the statement line when it arrives.

### 7.6 Why there is no wallet in this (D2)

The money settles in the merchant's Inter account and never in CTech's (parecer § 8). Recording it in a wallet would
create **a second balance for money the bank already holds**: it would drift from the bank's (fees, the merchant's own
Pix out, refunds made in Inter's app) with no way to reconcile except by trusting the bank — at which point the wallet
balance is noise. Worse, a CTech-held balance for a merchant's money is what custody looks like from outside, which is
the exact question ADR 0005 and the lawyer are about. So: the invoice and its attempts in billing say **what was
charged and whether it was paid**; the Inter (BYOK) account in Finanças says **what the bank holds**, with the bank's
own balance. No code path on the BYOK rail calls the wallet client; a test asserts it (§ 17).

## 8. Statement and balance in Finanças

### 8.1 The account (D21)

- On connect, the organization's Finanças space (same mode) gets a ledger account `class: asset`, name "Inter" + the
  account's last digits, with the new attribute **`source: inter_byok`** (absent = `manual`). The ledger has no
  account kind today (`repositories/finance_invoices.go`); `source` is the smallest addition and the one the
  read-only rules key on.
- Two system categories are seeded with the first BYOK account: `sys-unclassified-in` ("A classificar — entradas",
  income, group `other`) and `sys-unclassified-out` ("A classificar — saídas", expense, group `other`).
- **Statement-driven:** manual settlement into the account, transfers to or from it, opening-balance edits and
  OFX/CSV imports into it answer `409 account_is_synced`. It may be the space's default receiving account, and bills
  may expect it; they are settled by matching a line (§ 8.3).
- **Balance:** the account row stores Inter's last `GET /saldo` (`disponivel`, the blocked amounts, `dataReferencia`,
  `synced_at`). Screens show **Inter's balance** as the account's balance, with "atualizado às {hora}". The ledger's
  derived balance must equal it; a difference is shown ("Diferença de R$ X entre o Inter e o Finanças") and logged,
  and points at a bug or a line the parser could not read. No balance here authorizes anything (ADR 0024).

### 8.2 Posting

- **Opening balance:** `GET /saldo?dataSaldo={D0 − 1}`, where D0 is the first synced day, through the ordinary
  opening-balance rule (`OPENING#` marker). Exact by construction: opening + every line since D0 = today's balance.
  (That `dataSaldo` is the end-of-day balance is to be confirmed, § 15.)
- **Each line** of `GET /banking/v2/extrato/completo` posts one transaction: credit → Inter account ↔
  `sys-unclassified-in`; debit → `sys-unclassified-out` ↔ Inter account; dated `dataTransacao`, memo `titulo` +
  `descricao`, ref `idTransacao`. Idempotency: the existing lock row in `imports`, key
  `FITID#{account}#I:{idTransacao}` (no TTL), written in the same `TransactWriteItems`.
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
issuer side's cash leg goes to **the Inter (BYOK) account** (not the default receiving account), and its ref is the
Pix `endToEndId`. Then:

- the sync meets a line whose `detalhes.txId` has a `TXID#` pointer: if the invoice's posting exists, the line posts
  **nothing** and its lock row is linked to that transaction;
- if the invoice is not paid yet, the line is a third wake-up for § 7.4 (re-query); after settlement it is linked in
  the same run;
- a line still unlinked after 24 hours (re-query keeps saying unpaid, which should not happen) is posted unclassified
  and an alert is raised;
- excess Pix and refunds link the same way, through their `endToEndId`.

The finance § 3.8 durable retry queue keeps covering a failed write.

### 8.5 Sync schedule and limits

- **Backfill** on connect: `backfill_months` (3 default, up to 12) in 90-day windows (the API's maximum per call), in
  the background, newest window first so the recent statement appears soonest. How far back Inter answers is open
  (§ 15); a window Inter refuses ends the backfill, and the screen says from which day the statement starts.
- **Incremental:** every hour in `cmd/inter`, re-reading the last 3 days by transaction date (late lines), deduplicated
  by `idTransacao`; then `GET /saldo`.
- **"Sincronizar agora"** in Finanças: at most once per 10 minutes per connection.
- **Pagination, not scroll (D22):** `pagina`/`tamanhoPagina`; scroll allows one active scroll per account, and the
  merchant's ERP may hold it.
- **Limits:** banking is 10 calls/min (parecer § 2.4); the local limiter allows 8/min per connection, and the sync for
  one connection runs under a per-connection lock, so the hourly run and a button press never overlap.

## 9. Test mode (D9, D23)

- One **CTech-owned sandbox integration** (client_id, secret, certificate, key, account, a Pix key) in SSM
  SecureStrings `/ctech-billing/{env}/inter-sandbox/*`, readable only by the outbound Lambda. Its certificate lasts
  **30 days**; `cmd/inter` alerts the operator at D-7, D-3 and D-1; renewal is an operator runbook step.
- A tenant enables it in test mode with "Cobrança via Inter (teste)" — no credentials. Its test Finanças space gets an
  Inter (BYOK) test account.
- **One webhook** for the sandbox Pix key: `…/w/sandbox/{secret}/pix`, tenant resolved through `TXID#test#{txid}`.
- **Statement filter:** the sandbox account is shared by every test tenant, and test data may carry real names and
  tax ids an integrator typed. A test space receives **only lines whose `txId` points to that tenant's own charges**;
  every other line is dropped. The opening balance of a test account is zero, and its "Inter balance" is the sum of
  its own lines (the shared sandbox `saldo` is never shown).
- **Shared limits:** the sandbox allows 10 Pix calls/min and 5 tokens/min **for all test tenants together**. A global
  limiter (8 Pix calls/min, one token fetch per hour normally) answers `429 inter_sandbox_busy` ("tente em instantes")
  when exhausted. Sandbox failures alert at a lower severity than live ones, and the console says the sandbox may be
  unavailable (parecer § 2.6).
- "Simular pagamento" on a test invoice calls `POST /cobv/pagar/{txid}` (sandbox only); the confirmation then runs
  through the real webhook and re-query path.
- This makes test-mode BYOK invoices collectable, which ADR 0004's 2026-08-16 amendment forbids for the wallet rail
  only. The wallet rail keeps `ErrTestModeNotPayable`; the BYOK rail in test reaches the sandbox, never real money.
  No row crosses modes (ADR 0003).

## 10. The legal gate (D11)

- **`INTER_BYOK_LIVE_COLLECTION`**, from SSM `/ctech-billing/{env}/inter/live-collection-enabled`, set by a Terraform
  variable that **defaults to `false`** (so turning it on is a reviewed commit). Read by the `api` and by the outbound
  Lambda. Off: no live charge, refund or webhook registration; the console shows "Cobrança pelo Inter em liberação"
  on a connected live tenant, and statement sync works.
- **Turning it on requires, in the same pull request:** ADR 0030 recording the lawyer's answers to parecer § 8
  questions 1–4 and the owner's decision, and the variable set to `true` for `prod`. Without the ADR the review
  refuses the change.
- **Do I agree that read-only sync can go live without the clearance? Yes, with two conditions.**
  - Questions 1 and 4 (is operating charges "facilitating a payment instruction", art. 6º III b; does BYOK escape
    ADR 0005) concern **collection** only. Reading a statement moves no money and instructs no payment; it is what an
    accountant, an ERP or an aggregator (Pluggy, parecer § 2.1) does with the same credentials.
  - Questions 2 and 3 (do Inter's API terms allow a third party to hold and use the certificate; what contract and
    LGPD role CTech has) apply to **both** paths: a read-only connection holds exactly the same credentials. So:
    (a) the connect form carries the merchant's explicit authorization and declaration (§ 3.2), placing on the
    merchant, in writing, the representation that their Inter terms allow it; (b) the first live connections are a
    **pilot** with merchants who send a copy of the API terms they accepted (parecer § 8 Q2), and general availability
    of live sync waits for someone to read that text. This is an operational gate (who gets invited), not a flag.
  - D24 interacts: a merchant who ticks the collection scopes hands CTech credentials able to create charges even
    while the flag is off. The Lambda allowlist (§ 4.1) and read-scoped tokens (§ 4.3) make that capability unusable,
    not merely unused.

## 11. Console and Finanças

Built with `/impeccable` on `@aoctech/ui`, following `ui/DESIGN.md`, each screen with its phone layout. One screen
serves both areas: **`/finance/settings/inter`** in Finanças and **`/console/inter`** in the console (organization
spaces only; the console entry appears for merchant tenants, the Finanças entry for every organization space where
the person is owner/admin). Before building a stepper, file-drop and health card here, check `ctech-ui` for existing
ones and propose them there if absent (family rule).

### 11.1 Guided onboarding

A checklist, each step with what to click at Inter (parecer § 2.1) and its own state, saved per organization
(`onboarding_step`, display only):

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
novamente, Sincronizar agora, Configurar multa e juros (console), Desconectar (confirmation listing the open charges
that will be cancelled and the "só você cancela a integração no Inter" message).

### 11.3 Elsewhere

- Finanças: the Inter account shows Inter's balance and "atualizado às", the unclassified count with a link to the
  classification list, and the difference banner (§ 8.1).
- Console invoices: the attempt's Pix (copy and QR), "Simular pagamento" in test, excess payment notice with
  "Devolver", the `customer_tax_id_required` and gate reasons in words.
- Tenant zero's console (applications, console spec § 4.2) shows each merchant's `payout_rail` and status.

## 12. Interfaces

### 12.1 Routes

Console routes require a user session (`RequireUserScope`), `X-Billing-Space: org:{id}` through `ResolveSpace`, and
`X-Billing-Mode`. Writes go through the idempotency middleware.

| Route | Who / scope |
|---|---|
| `GET /v1.0/console/inter/connection` | owner/admin; `billing:finance:read` |
| `PUT /v1.0/console/inter/connection` (multipart in live; `{}` in test turns the sandbox on) | owner/admin; `billing:finance:write` |
| `POST /v1.0/console/inter/connection/test` | same |
| `POST /v1.0/console/inter/connection/certificate` | same |
| `POST /v1.0/console/inter/connection/webhook` `{ "replace_existing": bool }` | same |
| `POST /v1.0/console/inter/connection/sync` | same |
| `PUT /v1.0/console/inter/onboarding` `{ "step": "…" }` | same |
| `DELETE /v1.0/console/inter/connection` `{ "cancel_open_charges": bool }` | same |
| `PUT /v1.0/console/inter/collection-settings` `{ "late_fee_percent", "monthly_interest_percent" }` | owner/admin of the merchant tenant; `billing:organization:write` |
| `POST /v1.0/console/invoices/:id/pix` (new attempt) | same |
| `POST /v1.0/console/invoices/:id/simulate-payment` (test only) | same |
| `POST /v1.0/console/excess-payments/:id/refund` | same |
| `GET /v1.0/console/finance/accounts/:id/unclassified` | `finance.read` |
| `POST /v1.0/console/finance/unclassified/:tx_id/classify` `{ "category_id" \| "bill_id" \| "transfer_account_id" }` | `finance.import` |
| `POST /v1.0/internal/inter/notify` | M2M `internal:billing:inter-notify` |

New `problem` codes: `inter_not_connected`, `inter_certificate_expired`, `inter_certificate_invalid`,
`inter_key_mismatch`, `inter_credentials_refused`, `inter_collection_scopes_missing`, `inter_open_charges`,
`inter_webhook_conflict`, `inter_sandbox_busy`, `inter_unavailable`, `byok_collection_not_cleared`,
`customer_tax_id_required`, `account_is_synced`, `refund_window_closed`.

### 12.2 Data

New table **`{env}_billing_inter`** in `schema.json`:

| Row | pk → sk | Notes |
|---|---|---|
| Connection | `{account_org_id}#{mode}` → `CONNECTION` | state, kind (`merchant` \| `sandbox_shared`), client_id, account_number, pix_key, `sealed` (`k1.`), version, cert fields, capabilities, scopes_granted, hook_id, webhook_status, health, warned flags, authorization, onboarding_step, sync cursor, backfill range, ledger_account_id; `jobs_pk = {mode}#inter-connections` (sparse) |
| Hook | `HOOK#{hook_id}` → `HOOK` | account_org_id, mode, secret_hash, previous_secret_hash + valid_until |
| Txid pointer | `TXID#{mode}#{txid}` → `TXID` | tenant, account_org_id, invoice_id, attempt_id; written with the attempt |

Changed rows: `Organization` gains `payout_rail`; `PaymentAttempt` gains method `inter_cobv`, `pix_copia_e_cola`,
`checked_at`, late charges; a new `EXCESS#` row under the invoice; the invoice copies the collection settings at
finalization; `LedgerAccount` gains `source` and the Inter balance snapshot; `imports` gains `I:` lock keys.

### 12.3 Configuration

| Name | Where |
|---|---|
| `INTER_BYOK_LIVE_COLLECTION` | SSM `/ctech-billing/{env}/inter/live-collection-enabled`, Terraform variable, default `false` |
| `INTER_OUTBOUND_FUNCTION` | `api` env; `lambda:InvokeFunction` on it |
| `INTER_CREDENTIALS_KMS_KEY` | key ARN; `api`: `GenerateDataKey` only; outbound Lambda: `Decrypt` with context condition |
| Sandbox credentials | SSM SecureString `/ctech-billing/{env}/inter-sandbox/{client-id,client-secret,cert,key,account,pix-key}`; outbound Lambda only |
| Webhook M2M client | SSM `/ctech-billing/{env}/inter-webhook/{client-id,client-secret}`; webhook Lambda only |

### 12.4 `ctech-go-common/inter` (D10)

New package `gopkg.aoctech.app/api-commons/inter` (the module path of ctech-go-common), proposed by parecer § 10.2:

- `inter.Credentials{ClientID, ClientSecret, CertPEM, KeyPEM, Account, Env}` passed **per call**; the package reads
  no SSM, no environment and no cache of its own;
- an mTLS transport cached by certificate fingerprint (a renewal makes a new transport);
- `FetchToken(ctx, creds, scopes)` and a `TokenCache` interface the caller implements (billing: Valkey + lock);
- `pix`: `cobv` (put, get, patch), `pix` (get), devolução (put, get), webhook (get, put, delete), callbacks (list,
  retry), sandbox payment; `banking`: extrato completo (paginated), saldo;
- Bacen RFC 7807 error types, money conversion, txid validation, the BR Code QR renderer (from the wallet's `qr.go`).

Phase 2 adds `cobranca`, phase 3 `pixauto`. The wallet migrates its `pix-gateway` to the package in its own pull
request; its RPC contract does not change.

## 13. Seams for phases 2 and 3

| Seam | Phase 2 (Cobrança v3) | Phase 3 (Pix Automático) |
|---|---|---|
| Lambda ops grouped by product | `CreateBoleto`, `GetBoleto`, `CancelBoleto`, `BoletoPDF`, boleto webhook ops | `rec`, `solicrec`, `cobr`, `locrec`, `webhookrec`, `webhookcobr` ops |
| Webhook product segment (§ 6.2) | `/w/{hook}/{secret}/boleto` (no suffix documented) | `/rec`, `/cobr`, each also with its suffix variant |
| Pointer rows | `CODSOL#{mode}#{codigoSolicitacao}` beside `TXID#` | `IDREC#{mode}#{idRec}` |
| `PaymentMethod` | `inter_boleto` | `inter_pix_automatico` |
| Capabilities and scopes | `boleto` (`boleto-cobranca.*`); customers need a full address (billing's `Customer` gains it then) | `pix_automatico` (`rec.*`, `solicrec.*`, `cobr.*`, `payloadlocationrec.*`, `webhookrec.*`, `webhookcobr.*`); CNPJ ≥ 6 months |
| Gate | same `AuthorizeCharge`, same flag (the lawyer's question 1 explicitly includes `cobr`) | same |
| Subscriptions | — | a per-subscription collection method; journey 4 builds on this phase's `cobv` (parecer § 5.3) |
| Statement linking | `seuNumero` / `codigoSolicitacao` in `detalhes` | `txid` of each `cobr` |
| Other PSPs | `cobv` is Bacen-standard: the `pix` subpackage keeps Bacen types apart from Inter-specific ones, and the rail is named `inter_byok`, so another PSP is a new rail, not a rename |

Whether adding scopes to an existing Inter integration needs a new integration (and a new validation wait) is open
(§ 15); if it does, phases 2 and 3 ask the merchant to tick their scopes in phase 1's integration in advance.

## 14. Failure behaviour

- **Inter down / 5xx:** reads show the last synced data with its time; charge creation retries via checkout and the
  sweep; confirmation is never inferred from a failure. `consecutive_failures` drives "Atenção" and alerts.
- **Lambda unreachable:** the same as Inter down; the console answers `503 inter_unavailable` on actions.
- **Token limit hit (429 on token):** the local limiter makes this a bug; the op fails, retried by its caller, and an
  operator alert fires.
- **Webhook lost or webhook host down:** the hourly sweep confirms; the statement sync is a third path; after an outage
  the operator runs `-retry-callbacks`.
- **Forged webhook:** fails the mTLS handshake at the edge; with a valid CA but wrong secret, `404`; with a valid
  secret, nothing is trusted from the body.
- **Webhook overwritten by the merchant's ERP:** detected daily, banner and alert; confirmation continues by sweep.
- **Certificate expired:** collection refused with a reason, sync paused, console and e-mail say how to renew.
- **Merchant cancelled the integration at Inter:** tokens are refused → "Atenção" → after 24 hours of refusals the
  console asks to reconnect or disconnect; nothing is marked paid or unpaid on a refusal.
- **ctech-account unreachable:** console routes fail closed (ADR 0025); the notify route needs no membership.
- **KMS unavailable:** the Lambda's in-memory cache serves warm connections up to 15 minutes; then calls fail as
  "Inter down".
- **Concurrent confirmations** (webhook, sweep, sync): one guarded transition; the others see `SUCCEEDED`.
- **Disconnect racing a payment:** a payment that lands after the charge is removed is refused by Inter; one that
  lands before is confirmed by step 1's removal answer (`409` → re-query) before credentials are deleted.

## 15. Open items

To verify with Inter or the pilot (parecer § 11), each with the design's current answer:

1. **`ca.crt` per merchant or global?** Probably global, undocumented. The truststore is a bundle, so a merchant whose
   download differs adds a CA; tenant separation never depends on the CA (§ 6.1). Check: compare CTech's download with
   the pilot's.
2. **Is the 5 tokens/min limit per integration, per account or per source IP?** The limiter assumes per integration
   and keeps ~1 fetch per connection per hour, safe if it is per IP at today's scale; measure with the pilot and
   revisit before ~200 live connections.
3. **Statement history depth.** The API caps a call at 90 days and documents no lower bound. Backfill stops at the
   first refused window; measure with the pilot (ask 24 months).
4. **Does a Pix webhook belong to the integration or to the key/account?** Unknown; safe default is check-before-put
   with explicit confirmation to overwrite and the daily audit (§ 6.4, D25).
5. **Pix Automático suffixes (`/rec`, `/cobr`) are unverified;** the Pix suffix is documented but not applied in
   production (parecer § 6.6). Phase 3 accepts both variants; verify with the pilot.
6. Whether the **old certificate stays valid** after renewal until its `notAfter` (affects only the swap's overlap).
7. Whether **scopes can be added** to an existing integration without a new integration and validation.
8. Whether a token request for scopes the integration lacks is **refused or narrowed** (§ 3.2 handles both).
9. Whether `cobv` creation needs `payloadlocation.write` (Bacen allows the PSP to create the location).
10. `GET /saldo?dataSaldo` semantics (end-of-day balance?) for the opening balance (§ 8.2).
11. `x-conta-corrente` vs `x-inter-conta-corrente` in the statement example (parecer § 2.5).
12. The text of Inter's API terms (lawyer question 2) and the merchant's tariffs (their decision, not CTech's).
13. **For the lawyer:** parecer § 8 questions 1–4. Live collection stays off until answered (§ 10).
14. **For the owner:** the price of the Finanças "Custom (BYOK)" tier (plans spec), and the pilot merchant (CNPJ,
    ideally without an ERP webhook on the chosen key).

## 16. Deploy

1. **ctech-go-common:** `inter` package released; billing pins the version.
2. **billing manifest + ctech-account:** billing publishes `internal:billing:inter-notify`; ctech-account creates the
   `billing-inter-webhook` client with that scope (`cmd/createclient`); its id/secret into SSM.
3. **billing infra (Terraform):** KMS key and policies; the two Lambdas and their CI workflow (the wallet's
   `pix-gateway.yml` shape, deployed ahead of the `api`); HTTP API with the regional mTLS domain, the versioned
   truststore bucket seeded with CTech's `ca.crt` and the sandbox CA; the DNS-only CNAME (operator, documented in
   `OPERATIONS.md`, as the wallet's); sandbox credentials in SSM; the live-collection flag `false`.
4. **billing part A:** connection, tokens, Finanças account and statement sync (live allowed, pilot only, § 10),
   test-mode sandbox toggle, `cmd/inter` steps 2–4.
5. **billing part B:** `cobv` collection, webhook route, confirmation, refunds, sweep (step 1) and promotion (step 5);
   live stays `pending_clearance`.
6. **On the lawyer's answer:** ADR 0030 + flag `true` in one reviewed pull request; the pilot first.

Each part deploys on its own; part A without B changes nothing about money.

## 17. Tests and review focus

Part of done, not later hardening:

1. **No wallet on the BYOK rail:** a BYOK invoice issued, paid, refunded and voided with a wallet fake that fails on
   any call; zero calls.
2. **Legal gate:** with the flag off, a live BYOK tenant gets `byok_collection_not_cleared` from `AuthorizeCharge`,
   and a live `collect` op sent straight to the Lambda is refused there too; read ops still work; flipping the flag
   off after `enabled` blocks the next charge at once.
3. **Single gate:** every collection path (finalization, checkout, new attempt, refund) goes through
   `AuthorizeCharge`; a table test over rail × status × connection state × flag × mode.
4. **Credentials:** the raw DynamoDB item holds only `k1.` ciphertext; responses, logs and audit rows never contain
   secret, key or token (a log-capture test); the `api` role's policy has no `kms:Decrypt` on the key (a Terraform
   plan assertion); a wrong encryption context fails to decrypt.
5. **Webhook spoofing:** wrong secret, another connection's hook with a valid txid of this one, a txid of another
   organization, an account-header mismatch, a payload claiming payment for an `ATIVA` charge — none settles anything;
   both path variants (`…/pix` and `…/pix/pix`) reach the handler; the secret compare is constant time.
6. **Re-query before paid:** settlement only from `GET /cobv`; three concurrent wake-ups settle once.
7. **Tokens:** 50 concurrent calls on a cold cache → one fetch; a forced `401` → one refresh and one retry; the limiter
   never lets a fourth fetch out in a minute; the sandbox's shared limiter answers `inter_sandbox_busy`.
8. **Renewal:** swap under load with no failed call; warnings at D-90/D-30/D-7 sent once each, reset by a renewal.
9. **Disconnect:** order (charges, webhook, credentials, gate); refuses with open charges without the flag; never
   deletes a webhook that is not CTech's; a late callback afterwards is `404`.
10. **Webhook conflict:** an existing foreign URL is not overwritten without `replace_existing`; the daily audit marks
    `overwritten`.
11. **Statement:** syncing the same range twice adds nothing; after backfill on a fixture, the ledger balance equals the
    fixture's `saldo`; a paid BYOK invoice and its statement line produce **one** cash entry whichever arrives first;
    classification never moves the balance; manual movements into the account answer `account_is_synced`.
12. **Test-mode isolation:** tenant B's test space never receives a line of tenant A's sandbox charges; no row crosses
    modes; a live connection in `dev` refuses production credentials.
13. **Spaces:** personal spaces, personal workspaces, members, viewers and other organizations get `404`/`403` from
    every connection route, with zero table reads before membership (ADR 0025).
