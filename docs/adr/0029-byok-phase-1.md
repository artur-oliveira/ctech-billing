# ADR 0029 — BYOK phase 1: an organization's own bank account, reached through a dedicated outbound Lambda

Status: Accepted (2026-10-11) · Spec: [`2026-10-11-byok-inter-phase-1.md`](../specs/2026-10-11-byok-inter-phase-1.md)
(with its 2026-10-11 amendment) · Factual basis: [BYOK Inter parecer](../analysis/2026-10-11-byok-inter-parecer.md) ·
Extends [ADR 0004](0004-pix-on-invoice-via-wallet.md), [ADR 0005](0005-payout-gate.md),
[ADR 0017](0017-field-level-encryption.md), [ADR 0024](0024-billing-keeps-a-management-ledger.md),
[ADR 0025](0025-spaces-personal-and-organization.md)

## Context

Merchants want their invoices paid straight into their own bank account and their bank statement in Finanças without
retyping it. CTech's wallet rail (ADR 0004) collects into CTech's pooled account, which is custody and is gated by
ADR 0005. "Bring your own key" (BYOK) inverts that: the organization hands CTech its own bank API integration and the
money never passes through CTech. Banco Inter Empresas is the first provider (parecer §§ 2 to 7); the design must not
be Inter-shaped, because more providers will follow.

The constraints that shape it: billing's hosts have no public IPv4 and Inter has no IPv6; merchants' credentials can
create charges and read statements on real company accounts; the owner's cost rule forbids NAT gateways, per-call KMS
and Secrets Manager; and live collection on a merchant's account raises legal questions a lawyer has not answered yet
(parecer § 8).

## Decision

- **No wallet (D2, spec § 7.6).** BYOK never creates a wallet, a wallet balance or a wallet ledger entry. Billing keeps
  what was charged and whether it was paid; Finanças keeps what the bank holds, with the bank's own balance.
- **The organization owns the connection (D3, § 3.1).** One connection per ctech-account organization, mode and
  provider, keyed by the organization's Finanças space (`{account_org_id}#{mode}`, `CONNECTION#{provider}`) so the space
  resolver guards it (zero reads before membership). Personal spaces do not connect.
- **A dedicated credentials key, opened only by the outbound Lambda (D4, R1, § 3.3).** One AES-256 key per environment
  in the SSM SecureString `/ctech-billing/{env}/byok/credentials-key`, created by an operator script (never in Terraform
  state), read **once per cold start** by the outbound Lambda, which is the only principal allowed to read it. Bundles
  are sealed `b1.` + AES-256-GCM with the AAD `{account_organization_id}|{mode}|{provider}|{connection_id}|{version}`,
  so a bundle copied onto another row does not open. Plaintext touches the `api` only inside the upload request and is
  handed to the Lambda's `Connect` op (test, then seal); renewal uses `Reseal`. No KMS call per operation.
- **A non-VPC arm64 Lambda is the egress (D5, § 4.1).** `{env}-ctech-billing-byok-outbound` gives IPv4 egress with no
  NAT gateway and no public address per instance, and is the only billing code that opens a connection to a provider.
  It refuses ops by group (read, teardown, collect, sandbox).
- **Webhooks: one mTLS host, the secret in the path, re-query before paid (D8, D15, R6, § 6).** Built in part B.
  `/w/{provider}/{hook_id}/{secret}/{product}`, both suffix variants accepted, the parser chosen by the registered
  provider and product; nothing in a callback body is trusted; the provider's own client certificate is validated by
  a truststore, no merchant certificate is involved.
- **Test mode uses CTech's existing Inter integration against the sandbox host (D9 as amended 2026-10-11, § 9).** The
  outbound Lambda's role is granted read on the wallet's `/ctech-wallet/{env}/inter/{client-id,client-secret,mtls-cert,
  mtls-key}`; nothing is copied. The sandbox account and Pix key are billing's own non-secret parameters. A test space
  receives only statement lines of its own charges.
- **Shared code in ctech-go-common `v1.18.0` (D10, § 12.4).** `api-commons/byok` is the provider-neutral seam and the
  Lambda envelope; `api-commons/inter` is Inter's implementation with credentials passed per call. Billing's services
  depend on `byok.Provider` only; only the Lambda imports `inter`.
- **The legal gate (D11, § 10).** `BYOK_LIVE_COLLECTION` and `BYOK_LIVE_PROVIDERS` default to off and empty, set by
  Terraform variables that write both the SSM parameters (read by the `api` at start) and the Lambda's environment (an
  apply is the instant kill switch). A live `collect` op is refused inside the Lambda unless both allow it.
- **A new payout rail (D12, § 7.1).** `Organization.payout_rail` (`wallet` | `byok`), `payout_provider`, and the
  status `pending_clearance`; `AuthorizeCharge` stays the single gate. Built in part B.
- **The `api` owns the token lifecycle (D14, § 4.3).** Tokens are cached sealed in Valkey DB 3, fetched single-flight
  under a lock, at most 3 fetches per minute per connection, scoped to the capability in use.
- **D16 to D24** as the spec states them: statement sync is not plan-gated (D16); one due-date charge per payment
  attempt with a deterministic txid (D17); no CTech per-charge ceiling on BYOK (D18); excess payments are never refunded
  automatically (D19); the BYOK account is statement-driven with two "A classificar" system categories and refuses
  manual movements with `account_is_synced` (D20); Inter statement sync uses pagination, never scroll (D21); test spaces
  are filtered to their own charges (D22); the merchant ticks the full phase-1 scope list and a statement-only
  integration is accepted (D23); a webhook that is not CTech's is never overwritten without explicit confirmation
  (D24).

## Consequences

- Live collection stays off until ADR 0030 records the lawyer's clearance per provider and the same pull request turns
  on `BYOK_LIVE_COLLECTION` and lists the provider. Live statement sync may start with the pilot (CTech's own
  organization).
- The infrastructure budget is the plan's "Custos de infra" table
  ([part A plan](../plans/2026-10-11-byok-phase-1-a-connection-and-statement.md)): about US$ 1/month at 10 merchants,
  US$ 11 at 1.000 and US$ 105 at 10.000. Deviations must be reported with their cost delta.
- The wallet keeps its own Lambda, role and secrets; it migrates its `pix-gateway` to `api-commons/inter` in its own
  pull request. The only link is the read grant on its four Inter parameters for test mode, so the wallet's annual
  certificate renewal renews test mode too.
- Losing the credentials key makes every stored connection unreadable: merchants would have to upload their
  credentials again. The key is backed up with the account's break-glass material, like `field-encryption-key`.
