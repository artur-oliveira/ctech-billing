# ADR 0013 — The portal is a static export, and the API is same-origin behind it

Status: Accepted (2026-08-16) · **Half amended 2026-08-16 — the API is no longer same-origin** ·
**Hosting superseded by [0020](0020-portal-on-cloudflare-workers.md) (2026-08-20)** ·
Implements the front end proposed in ARCHITECTURE.md § 1 · Screens P1–P4, X1

> Only the static-export half of this decision still stands. The same-origin half was reversed the
> same day (see the amendment at the end), and the S3 + CloudFront hosting it described was replaced
> by Cloudflare Workers Static Assets in [0020](0020-portal-on-cloudflare-workers.md). Everything
> below is left as written — it is the record of why the decision was made, not a description of the
> system today.

## Context

The portal is a Next 16 app. Next offers three deployment shapes, and the choice is not a
preference — each one implies a different runtime, a different failure mode and a different
threat model.

**A Node server** (`next start`, or an adapter on Lambda) gives SSR, dynamic route segments and
route handlers. It also gives this service a second long-lived process to deploy, patch, scale
and observe, in a language nothing else in the company runs in production, to render pages whose
entire content arrives from an API the browser can call itself.

**A static export** (`output: "export"`) is a directory of files. There is no server, so there is
nothing to exploit, nothing to keep warm and nothing to roll back except objects in a bucket.
What it costs is every Next feature that runs at request time.

There is a second question stacked on the first: where the API lives relative to the app. A front
end on `billing.aoctech.app` calling `billing-api.aoctech.app` is cross-origin, which means CORS,
which means a list of allowed origins maintained in the API for the benefit of a browser — and a
preflight on every mutating call.

## Decision

**The portal ships as a static export to S3, served by CloudFront, and `/v1.0/*` is forwarded to the
API from the same distribution.**

The distribution has two origins: the private bucket (through an Origin Access Control) and the
HAProxy edge. `/v1.0/*` and `/.well-known/*` are ordered cache behaviours pointed at the second,
with caching disabled and `AllViewerExceptHostHeader`. Everything else is the bucket.

Pretty URLs come from a CloudFront Function reading a **KeyValueStore route manifest**: `/invoices`
becomes `/invoices.html` if that route exists in the manifest, and `/404.html` if it does not. The
manifest is written by the deploy from the export's own output, so it cannot drift from what was
shipped.

`NEXT_PUBLIC_API_URL` is **empty** in a deployed build. The browser calls `/v1.0/portal/invoices` as
a same-origin relative path.

## Consequences

- **There are no dynamic route segments, and there cannot be.** `output: "export"` prerenders one
  file per route; `/invoices/[id]` would require `generateStaticParams` to enumerate every invoice
  that will ever exist. An invoice is `/invoice?id=…`, read from the query string inside a
  `<Suspense>` boundary, because `useSearchParams` suspends during prerender. This is written in
  `ui/README.md` as a rule rather than a detail, because it is the one constraint that silently
  reappears every time somebody adds a screen.
- **CORS never applies, and the API needs no origin list.** A same-origin request has no preflight
  and no `Access-Control-Allow-Origin` to get wrong. It also means a browser-side CSP can be
  strict: `connect-src 'self'` plus ctech-account, and nothing else.
- **No `custom_error_response` on the distribution.** They are distribution-wide, so mapping 404 to
  `/404.html` would also replace the API's RFC 7807 problem bodies on the `/v1.0/*` behaviour. The
  function handles the miss instead, on the bucket's behaviour only.
- **No image optimizer** (`images.unoptimized`). The bucket serves bytes. The two images this app
  has are the logo, already sized.
- **`rewrites()` and `output: "export"` are mutually exclusive by construction.** `next.config.ts`
  puts them in the two arms of one conditional rather than leaving both present and relying on
  somebody remembering that rewrites only ever ran in `next dev`.
- **A deploy is a sync plus a manifest write plus an invalidation.** Immutable assets and mutable
  documents are synced in two passes with different cache headers, because one pass cannot express
  both.

## Limits accepted

- **No server-side rendering, so no server-side authorization.** Every gate in this app is a
  client-side gate, which is a UX affordance and not a security boundary. That is only safe because
  the security boundary is the API: every portal route verifies a JWT and a scope, and the static
  files themselves are public by design (they contain no customer data — the data arrives from an
  authenticated call).
- **Environment is baked at build time.** The four `NEXT_PUBLIC_*` variables are compiled into the
  bundle, so changing the OAuth client id means a rebuild, not a restart. That is the honest cost of
  having no runtime.
- **One distribution serves two very different things.** A cache or header policy change made for
  the app can affect the API path. The behaviours are separated and documented for exactly that
  reason, and it stays a place to be careful.
- **A route removed from the manifest 404s before its file is deleted, and a file uploaded before
  its manifest entry 404s too.** The deploy syncs first and writes the manifest second, so the
  window favours "new file not yet routed" over "route pointing at nothing".

## Reopen if

A screen genuinely needs to be rendered before it reaches the browser — a public invoice page that
must unfurl its own content to a scraper, or an SEO surface where client-side rendering measurably
costs ranking. That is an argument for one prerendered surface, not for putting the whole portal
behind a server.


## Amendment (2026-08-16) — the portal calls the API directly

The same-origin half is reversed. `NEXT_PUBLIC_API_URL` is the API's own hostname in every deployed
environment, and the browser goes to `billing-api[-env].aoctech.app` rather than through the
portal's distribution.

**What the Context above underweighted is what same-origin actually cost at run time.** The
`/v1.0/*` behaviour does not shorten the path to the API — it lengthens it. A request from the
portal went to CloudFront, which forwarded to the origin, which is Cloudflare in front of HAProxy,
which finally reached the instance: three hops and three TLS terminations, on every call, for a
request that had one destination the whole time. The price of cross-origin is a preflight per
(method, header-set), cached for an hour, and an allowlist. That is one round trip amortised over
a session against two extra ones on every request.

**The CORS posture, and why it is safe:**

- Exact origins, never a wildcard and never a suffix match.
  `strings.HasSuffix(origin, ".aoctech.app")` is the shape that looks careful and admits
  `evil-aoctech.app`.
- `AllowCredentials` is on, paired with those explicit origins — the same configuration
  `ctech-wallet` and `ctech-poker` ship. The spec forbids credentials alongside a wildcard and
  Fiber refuses the combination, so the two settings can only move together.
- `config.Load` refuses to boot production without `CORS_ALLOWED_ORIGINS`. Outside production an
  empty list means wildcard *without* credentials, which is the siblings' laptop default and cannot
  reach a customer because of that guard.
- The origin list is `terraform/billing/locals.tf`'s `cors_allowed_origins`, and it holds the
  portal's domain and nothing else. The checkout is the same origin as the portal.

**What did not change:** the static export, the route manifest, the bucket, the CSP. The
`/v1.0/*` behaviour is kept rather than deleted — it is the rollback, and taking it takes a
Terraform apply while restoring same-origin otherwise takes one environment variable. `/.well-known/*`
is not a rollback at all: a client discovering billing from the portal's hostname still resolves it
there.

**Reopen if** the preflight cost turns out to matter — it is one request per unique (method,
header) combination per hour per origin, and if that is ever measurable the answer is a longer
`MaxAge`, not a return to three hops.
