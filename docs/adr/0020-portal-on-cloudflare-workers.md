# ADR 0020 — The portal is served by Cloudflare Workers Static Assets, not S3 + CloudFront

Status: Accepted (2026-08-20) · Supersedes the hosting half of
[0013](0013-static-portal-same-origin-api.md) · Extends [0010](0010-infrastructure-as-terraform.md)

> [0013](0013-static-portal-same-origin-api.md) decided two things: the portal is a static export,
> and the API is same-origin behind it. The same-origin half was already reversed by 0013's own
> amendment. This ADR replaces the other half — where the files are served from. The static-export
> half of 0013 still stands and is not in question here.

## Context

DNS for `aoctech.app` is already on Cloudflare. Every request to `billing.aoctech.app` therefore
resolved at Cloudflare, was passed to CloudFront, and only then reached the S3 bucket. CloudFront
was a paid hop between two parties that were already talking.

That hop also carried real machinery: an Origin Access Control, a KeyValueStore route manifest, a
CloudFront Function to read it, and a response-headers policy. All of it existed to serve a
directory of files that never changes between deploys.

The `/v1.0/*` behaviour that made 0013's same-origin decision work was already dead by then — the
browser calls `billing-api[-env].aoctech.app` directly, and the behaviour was kept only as a
rollback for a decision nobody intends to roll back.

## Decision

**The export is uploaded to Cloudflare Workers Static Assets by
`ctech-cdk`'s reusable workflow `.github/workflows/frontend-cloudflare.yml`, and
`billing.aoctech.app` points at it. CloudFront and the bucket are removed.**

The decision is not billing-specific: all five CTech front ends move the same way, from one
workflow, so the security headers, the export guards and the CSP are written once. The migration
plan of record is `ctech-cdk/docs/plans/2026-08-20-frontend-cloudflare-migration.md`.

Pretty URLs need no manifest and no function — Workers Static Assets resolves `/invoices` to
`invoices.html` itself, which is what the CloudFront Function was hand-rolling.

Security headers come from a generated `_headers` file. The CSP's `connect-src` is **derived from
the build environment**: every `https://` and `wss://` literal in `build-env-*` becomes an allowed
origin, plus anything in `extra-connect-src`.

## Consequences

- **One less hop and one less bill.** Cloudflare terminates and serves; nothing else is in the path.
- **`connect-src` is generated, so an origin absent from `build-env-*` is an origin the browser
  refuses.** Adding a third-party endpoint to the portal is now a workflow change, not only a code
  change. It is also scheme-exact: `https://host` does not permit `wss://host`.
- **CORS stays.** Nothing about this ADR revisits 0013's amendment — the browser was already calling
  the API host directly, and the API still needs its exact origin list with credentials on.
- **The rollback that 0013's amendment described is gone.** Restoring same-origin was one
  environment variable while the `/v1.0/*` behaviour existed; with the distribution removed, it is a
  redeploy of the distribution first. This is accepted: the portal carries no traffic that would
  make that reversal urgent.
- **`terraform/billing/frontend.tf` goes away**, and with it the HCL port of `ctech-cdk`'s
  `nextjs-static-frontend`. The deploy identity that wrote to the bucket is no longer needed
  ([0015](0015-four-deploy-identities.md)).
