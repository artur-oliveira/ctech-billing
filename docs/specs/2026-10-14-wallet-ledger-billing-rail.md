# Spec — Wallet as a pure ledger; Billing as the only Inter rail

Status: **Draft for review** · 2026-10-14 · Repositories that change: `ctech-billing`, `ctech-wallet`,
`ctech-poker`, `api-commons` (shared) · Plans: one per phase (P1–P5), written after this spec is approved.

## 1. Intent

CTech is a technology company, not a gaming company and not a payment institution. Today `ctech-wallet`
knows Pix, charges, gambling limits and product catalogues. After this work:

- **Billing** is the only service that talks to Inter. CTech is tenant zero, with its own BYOK Inter
  connection, so CTech's collection uses the same code as every customer's.
- **Wallet** is a ledger of balances and transactions. It has no Inter code, no catalogues and no gambling rules.
- **Poker** (and future games) own catalogues, prices, personal limits, self-exclusion and consent.
- Every deposit becomes a Billing **invoice**, which is what makes NF issuance straightforward.

What the owner said, verbatim in meaning: Real balance is spendable **and** withdrawable by Pix to the user's own
CPF (option 2); Sandbox credits are sold by Pix **only inside the game**; the "Dinheiro de Jogos" (`game`) wallet
goes away; Wallet's product and Sandbox SKUs move to Poker; the Inter webhook's payer data must be saved on the
Billing invoice; delivery is a strangler in phases (option A), not a big bang.

Success means: no Inter or Pix code left in `ctech-wallet`; deposits and withdrawals working end to end through
Billing; no user balance lost or changed by the migration; the ledger invariants in §9 still enforced by tests.

## 2. What exists today (verified in source)

| Fact | Where |
|---|---|
| Wallet has three balances: `real`, `game` (real money, limited), `sandbox` (virtual) | `ctech-wallet/CLAUDE.md` |
| Wallet owns the Inter rail: Pix gateway Lambda, deposits, withdrawals, CPF match, reconcile | `ctech-wallet/pix-gateway`, `api/internal/pix`, `services/reconcile.go` |
| Billing collects through Wallet today: `POST /internal/wallet/charge` (ADR 0004), notify-back by webhook | `ctech-wallet/api/internal/api/v1/router.go:153`, `docs/adr/0004-pix-on-invoice-via-wallet.md` |
| Wallet sells Sandbox and product SKUs from a Go catalogue | `domain/wallet/product_sku.go`, `services/sandbox_purchase.go`, `services/product_purchase.go` |
| Wallet holds responsible-gambling state: limits, self-exclusion, consent addendum, `wallet_audit` | `services/responsible.go`, `repositories/audit.go` |
| Poker real-money mode exists, off by default; holds and cashout call Wallet | `ctech-poker/OVERVIEW.md`, `api/internal/api/v1/rooms.go` |
| The shared Inter provider drops payer data from the Pix webhook on purpose | `api-commons@v1.18.0/inter/callback.go:11-12` |
| Billing's `Confirm` settles from a re-query of the charge, never from the webhook body | `api/internal/services/byok_confirm.go` |

## 3. Target architecture

```
User ─▶ Wallet ──(OAuth client, invoice API)──▶ Billing ──(BYOK Inter provider)──▶ Inter
          ▲                                        │
          └──── signed invoice.paid event ◀────────┘      (Inter ─webhook─▶ Billing)

Poker ──(OAuth client)──▶ Billing   (sells Sandbox packs by Pix)
Poker ──(M2M scopes)────▶ Wallet    (credit Sandbox; hold/cashout on Real)
```

| Service | Owns | Does not own |
|---|---|---|
| Billing | Inter connection, invoices, charges, payer data, payouts, refunds, NF | user balances |
| Wallet | `real` and `sandbox` ledgers, deposit and withdrawal state, KYC gates, step-up MFA | Pix, charges, catalogues, gambling rules |
| Poker | catalogues and prices, personal limits, self-exclusion, consent, buy-in metering | money custody |

## 4. Deposit (Real balance)

1. The user calls `POST /wallet/deposits`. Wallet checks KYC and the deposit range **before** touching Billing.
2. Wallet writes a pending deposit and asks Billing for an invoice for that user's customer. The idempotency key
   is the deposit id.
3. The user pays by Pix. Inter calls Billing's webhook.
4. Billing re-queries the charge (existing `Confirm`), stores the payer data on the payment attempt (§6) and
   computes `payer_match`.
5. Billing sends a signed `invoice.paid` event to Wallet. The event is a wake-up signal only.
6. Wallet fetches the invoice from Billing and credits **only if** it is paid, the amount equals the deposit's,
   and `payer_match = match`. Otherwise the deposit is **quarantined** (never credited) and Wallet asks Billing
   to refund the payer.
7. A sweep over pending deposits re-runs step 6, so a lost event delays a credit and never loses one.

## 5. Withdrawal (Real balance)

1. The user calls `POST /wallet/withdrawals`: KYC `enhanced`, fresh MFA, as today.
2. Wallet debits (conditional write) and enters `processing`.
3. Wallet asks Billing for a payout to the CPF on the user's KYC record. The client never supplies a destination.
4. Billing pays through the CTech tenant's Inter connection. Only that connection holds the Pix/bill-payment
   permissions (the BYOK guide deliberately leaves them off for other tenants).
5. The reconcile job resolves `processing`: complete, or reverse the debit. A failed payout alarms; nothing is
   silent.

**Accepted risk.** A self-service withdrawal of a stored balance is the feature that most resembles a payment
account, and the "credit structure outside BACEN" position rests partly on it. This stays the owner's accepted
risk, recorded as Wallet invariant 12 is today. It is named here so it is never forgotten, not to reopen it.

## 6. Payer data on the invoice

Verified on 2026-10-14 with a live R$ 0,10 charge on CTech's Inter account (invoice 9, paid by Pix):

| Source | Carries the payer? |
|---|---|
| The charge re-query (`GET /pix/v2/cobv/{txid}`, what `Confirm` trusts) | **No.** `pix[]` has `endToEndId`, `valor`, `horario`, and an empty `infoPagador`. |
| The Pix webhook body | Not observed: the webhook Lambda logs no body. Wallet's old notes say it carries the CPF **masked**. Not relied on. |
| The statement line (`GET /banking/v2/extrato/completo`, what the statement sync reads) | **Yes, complete:** `detalhes.nomePagador`, `detalhes.cpfCnpjPagador` (**unmasked**), `detalhes.nomeEmpresaPagador` (the payer's institution), `detalhes.endToEndId`, `detalhes.txId`. The line was readable about a minute after the payment. |

So the payer comes from the **statement line**, joined to the payment by `txId` / `endToEndId` (which Billing's statement mapping already extracts as `ChargeRef` / `PaymentID`).

- **`api-commons`** (`byok.StatementLine`): an optional `Payer` (name, document, institution), filled by the Inter mapping for credit lines. Additive. The callback is left as it is.
- **Billing**: when the statement linker matches a credit line to a payment attempt (`ByokLinker.Link`), it persists the payer on the attempt, **write-once** (first write wins), and computes `payer_match` ∈ `match | mismatch | unknown` once, against the customer's registered tax ID, digit for digit (the document is not masked; a masked one, if Inter ever sends it, compares on the revealed digits and `unknown` when too few are shown).
- **Not waiting for the hourly sync.** Right after `Confirm` settles an attempt, Billing pulls the current day's statement page for that connection (one request, under the existing per-connection sync lock and rate limits) so the payer is normally recorded within a minute. The hourly sync and the sweep remain the safety net.
- **Consequence for the credit decision (§4):** a deposit is credited when the invoice is paid **and** `payer_match = match`. While the payer is not yet known, the deposit stays *pending*, never credited and never refunded; Wallet's sweep re-reads it. If no payer is known after a bounded wait (default 30 minutes), the deposit is raised to the operator, not credited.
- **Not used for NF.** The NF tomador is the customer's registered tax ID. A third party paying someone else's invoice must not become the tomador.

## 7. Sandbox and the games

- Wallet keeps a `sandbox` ledger. It gets a `credit` route (already scoped `internal:wallet:sandbox-credit`) and
  no purchase logic.
- Poker opens a Billing invoice for a Sandbox pack with its own OAuth client. On `invoice.paid` it credits
  Sandbox, idempotent on the invoice id.
- The **SKU catalogues (product and Sandbox) move to Poker.** Wallet's `product_sku.go`, `sandbox_purchase*`,
  `product_purchase*`, and `/internal/wallet/{sandbox,product}-purchases` routes are removed in P5, after Poker
  stops calling them.
- **No route converts Sandbox into anything** and there is no Real → Sandbox route. Both are enforced in the
  ledger (the handler refuses), with regression tests, not only in docs.
- Poker's real-money buy-ins keep using holds and cashout on the Real balance. Poker meters its own inflow
  limits, self-exclusion and consent; Wallet keeps no game concept.

## 8. Migration (strangler, five phases)

| Phase | Repo | Content | Shippable on its own |
|---|---|---|---|
| P1 | billing, api-commons | CTech tenant's Inter collection and payout; payer data and `payer_match`; payout and refund API; `invoice.paid` event with signature | yes |
| P2 | wallet | Billing client; deposits and withdrawals go through Billing behind a flag; old Pix path stays as fallback | yes |
| P3 | wallet | merge `game` into `real` (one-off, idempotent, ledger-preserving; before/after total check) | yes |
| P4 | poker, wallet | limits, consent, self-exclusion, SKUs and Sandbox sales move to Poker | yes |
| P5 | wallet, cdk | delete the Pix gateway, charge routes, SKU catalogues and `game` routes | yes |

- Rollback in P2 is turning the flag off. Nothing is deleted until P5.
- P3 is the only phase that rewrites balances. It runs per user under the existing ordered wallet lock, writes a
  ledger entry on both sides, and aborts the whole run if the before and after totals differ.
- Billing's own subscription collection stops depending on Wallet's charge route at P1; ADR 0004 is superseded
  by this spec and gets an amendment pointing here.
- `docs/specs/` and `docs/plans/` hold this spec and its plans in `ctech-billing`; each other repo's phase plan
  links back here.

## 9. Invariants and tests

Kept from Wallet (and enforced by tests each phase): balance never negative (conditional debit); ledger append-only;
every operation idempotent; one operation per wallet at a time; Sandbox never becomes real; the Pix webhook is
never the source of truth; no money left in limbo.

Retired: the ring-fence rules (`real → game` only, gross-inflow limits, `game` is real money), which leave with
the `game` wallet.

New: a deposit credits only after Wallet re-checks the Billing invoice and `payer_match = match`; a quarantined
deposit is never credited and is refunded; payer data is write-once; every cross-service call carries an
idempotency key.

End-to-end tests in each money-touching phase use Inter's sandbox simulated payment.

## 10. Cost

Net infra cost is neutral to lower. P5 deletes the Pix gateway Lambda and its SNS alerts; Billing's Inter
provider already runs. Billing gains a payout path and a few DynamoDB writes per deposit. Exact numbers go in the
P5 plan, as the owner asked for infra costs to be reported.

## 11. Open items

- Whether payout needs its own scope set on CTech's Inter app beyond what Wallet's gateway used. Verify in P1.
- NF timing for deposits (on the invoice at payment, as intended). Fiscal treatment of credit deposits belongs to
  the accountant, not to this spec.
