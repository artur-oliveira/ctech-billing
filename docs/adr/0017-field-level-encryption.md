# ADR 0017 — Field-level encryption for stored personal data

Status: Accepted (2026-08-16) · Makes true a claim ARCHITECTURE.md § 9 already
made · Extends [ADR 0009](0009-retention-and-ttl.md)

## Context

`billing.Customer.TaxID` is a CPF or CNPJ. The comment on that field said, and
had said since it was written:

> Encrypted at rest by the repository layer

It was not. `grep TaxID internal/repositories/` returned one function —
`RecordTaxIDAccess`, the audit entry for *revealing* it — and nothing that
encrypted anything. The DynamoDB table has `server_side_encryption`, which
protects the disk and protects the value from nobody: any role holding
`dynamodb:Query` on the table read every CPF in plaintext.

That is worse than never having claimed it. A comment asserting a safeguard is
exactly what makes the next reviewer skip checking for one, and this is personal
data under the LGPD with a documented control that did not exist.

## Decision

**A `crypto.Sealer` in `internal/crypto`, applied by the repository on the way
in and out.** AES-256-GCM, random nonce, stored as `v1.` + base64. The domain
struct holds a readable value; only the row does not.

Three choices inside that, each a departure worth naming:

**No development fallback key.** ctech-account's equivalent package encrypts with
a constant when `SECRET_ENC_KEY` is unset. That constant is in the repository, so
anything encrypted with it is encrypted with a published key — and the deployment
that forgot to set the real one is indistinguishable from the one that did not.
`config.Load` refuses to start without a valid key.

**A version prefix.** Without it, telling v1 ciphertext from v2 means trying one
and seeing whether the tag verifies, which is indistinguishable from tampering.
With it, a second key is additive.

**`Open` refuses an unprefixed value** rather than passing it through. A
pass-through would make a deployment that silently stopped sealing look
completely healthy, and the discovery would be made by whoever exports the table.

## Consequences

- **The claim in ARCHITECTURE.md § 9 is now true**, and an integration test reads
  the raw DynamoDB item to prove it — going through the repository would prove
  only that it round-trips, which a no-op "encryptor" also does.
- **`FIELD_ENCRYPTION_KEY` is required to boot.** Every binary that calls
  `config.Load` refuses without it. That is the intended failure: writing tax ids
  in the clear is invisible from every screen.
- **Nothing can be queried by tax id**, which is fine because nothing does.
  Making it searchable would need deterministic encryption, which leaks equality —
  and equality on a CPF is "these two records are the same person".
- **The webhook endpoint signing secret uses the same sealer**
  ([ADR 0016](0016-webhook-routing-by-product-owner.md)): a leaked signing secret
  lets somebody forge a delivery to a consumer that trusts this service, which is
  worse than reading one.

## Limits accepted

- **Rotation is not implemented.** The `v1.` marker makes it possible; nothing
  reads a second key today. Changing the key makes existing tax ids unreadable,
  so the key belongs wherever the account's break-glass material lives, not only
  in SSM.
- **This is the third AES-GCM helper in the company** (ctech-account's
  `internal/crypto`, ctech-wallet's `internal/asaas/crypto.go`, and now this).
  It is a duplication that belongs in `ctech-go-common`, and it was written here
  rather than moved because moving it is a change to two other repositories on
  the day a third needed it.
- **One key for the whole deployment**, not per-tenant. Per-tenant keys would
  make one tenant's exposure independent of another's, and they need key
  management this service does not have.

## Reopen if

A second field needs sealing and turns out to need searching, or a merchant-facing
requirement arrives for tenant-held keys. Both change the shape rather than the
decision, and both are worth being explicit about rather than growing this
package quietly.
