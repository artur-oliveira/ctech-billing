# Architecture Decision Records

One file per decision. Format: Context / Decision / Consequences / Limits accepted / Reopen if.

These records exist to preserve the **why** and the **limits we knowingly accepted**, not to
re-litigate settled questions. Every ADR here is already decided — the analysis behind them is in
[`../analysis/2026-08-15-product-architecture-assessment.md`](../analysis/2026-08-15-product-architecture-assessment.md)
(section 0, decisions D1–D12). Each ADR names the decision id it records.

| ADR | Title | Records |
|-----|-------|---------|
| [0001](0001-product-scope-a-and-b.md) | Product scope: CTech billing its own customers **and** third-party merchants | D1, D4, D6 |
| [0002](0002-datastore-dynamodb.md) | Datastore: DynamoDB, one table per entity, period GSI for analytics | D7 |
| [0003](0003-tenant-and-livemode-partition-key.md) | Partition key `{organization_id}#{livemode}` | D7, §12.4 |
| [0004](0004-pix-on-invoice-via-wallet.md) | Collection rail: PIX on the invoice, through wallet | D2, D11 |
| [0005](0005-payout-gate.md) | External merchants built but gated by `payout_status` | D3, D9 |
| [0006](0006-due-date-roll-forward.md) | Weekend/holiday due dates roll **forward** | D5 |
| [0007](0007-minimal-organization.md) | Minimal `Organization`, no company registry, no CNPJ | D10, D4 |
| [0008](0008-opaque-metadata.md) | `metadata` is an opaque key/value map | D8 |
| [0009](0009-retention-and-ttl.md) | Retention periods and DynamoDB TTL | D12 |
| [0010](0010-infrastructure-as-terraform.md) | Infrastructure as Terraform, not CDK | — (2026-08-15) |
| [0011](0011-console-session.md) | Console sessions: tenant from the owner, mode from the request | extends 0003 |
| [0012](0012-portal-serves-tenant-zero.md) | The portal serves tenant zero; other merchants' customers get the checkout | scopes 0011 |
| [0013](0013-static-portal-same-origin-api.md) | The portal is a static export, and the API is same-origin behind it | — (2026-08-16) |
| [0014](0014-billing-publishes-its-own-scopes.md) | Billing publishes its own OAuth scopes | — (2026-08-16) |
| [0015](0015-four-deploy-identities.md) | Four deploy identities, none of which trusts a pull request | extends 0010 |
| [0016](0016-webhook-routing-by-product-owner.md) | Outbound webhooks route by product owner, not by tenant or by caller | OVERVIEW § 9.3 |
| [0017](0017-field-level-encryption.md) | Field-level encryption for stored personal data | extends 0009 |
| [0018](0018-subscriptions-bill-several-prices.md) | A subscription bills one or more prices | replaces the one-item MVP rule |
| [0019](0019-zero-total-invoices.md) | A zero-total invoice is issued, then settled on issue | — (2026-08-16) |
| [0020](0020-portal-on-cloudflare-workers.md) | The portal is served by Cloudflare Workers Static Assets, not S3 + CloudFront | supersedes the hosting half of 0013 (2026-08-20) |

## Status of the record itself

All ADRs here are **Accepted** as of the date each file carries. Superseding an ADR
means adding a new file and marking the old one `Superseded by NNNN` — never editing the decision
of an existing one. Editing an ADR to say something else destroys the only artifact that explains
why the code looks the way it does.
