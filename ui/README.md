# ui

The billing front end. Two shells in one Next app: the **portal**, which a CTech customer opens to
see what they owe, and the **console**, which is not built yet. `PRODUCT.md` says who they are for,
`DESIGN.md` says what they look like and why.

## Running it

```sh
npm run dev:mock   # port 3004, fixtures, no backend needed
npm run dev        # port 3004, proxies /v1 to the API on :8004
```

`dev:mock` is the one to reach for. Most of the states these screens exist to handle cannot be
produced against a real backend on demand — an overdue invoice needs a due date in the past, an
expired PIX needs thirty minutes of waiting, "pendente de acordo" needs a dunning run that is not
built. The switcher in the bottom-left corner moves between ten of them, and `?scenario=vencida`
works as a link.

Mock mode is off unless `NEXT_PUBLIC_MOCK_API=true` **and** `NODE_ENV !== "production"`, and
`next.config.ts` aliases `src/dev/` to empty stubs in a production build so the fixtures are absent
from the bundle rather than merely unreachable.

## Environment

Four `NEXT_PUBLIC_*` variables are baked into the bundle at build time. CI sets
them per environment (`.github/workflows/frontend.yml`); `dev:mock` needs none
of them, because mock mode never authenticates and never leaves the process.

| Variable | Deployed value | Why |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | **empty** | CloudFront forwards `/v1.0/*` to the API from the same distribution, so the browser is same-origin and CORS never applies. |
| `NEXT_PUBLIC_SITE_URL` | `https://billing.aoctech.app` | `metadataBase`. Without it every Open Graph image resolves to localhost. |
| `NEXT_PUBLIC_CTECH_URL` | `https://accounts-api.aoctech.app` | ctech-account's API. The OAuth client talks to it directly, so it is also in the CSP's `connect-src`. |
| `NEXT_PUBLIC_CTECH_CLIENT_ID` | `billing` | The OAuth client registered at ctech-account. |

## Checks

```sh
npm test           # vitest
npm run lint
npm run build      # also type-checks
```

## Layout

| Path | What's in it |
|---|---|
| `src/app/page.tsx` | The public landing. The only route a stranger can reach and the only one that is indexed. |
| `src/app/(portal)/` | The four signed-in screens (`/dashboard`, `/invoices`, `/invoice`, `/subscriptions`). The route group is the shell **and** the auth gate. |
| `src/app/checkout/` | X1, the payment link. No session, no nav: the signed token in the URL is the whole of the authentication. |
| `src/app/{login,callback}/` | The OAuth round trip. `/login` is not a form — ctech-account owns every credential and this app never sees one. |
| `src/app/{not-found,error}.tsx`, `src/app/maintenance/` | The whole-page states. Outside the group on purpose — all three mean the nav is useless or misleading. A 503 from any request sends the reader to `/maintenance`, which probes `/v1.0/health` every 15s and carries them back. |
| `src/lib/auth/` | `@aoctech/auth-client` wiring: scopes, the client, the provider. No tokens are stored here — the refresh token is ctech-account's HttpOnly cookie and the access token is in memory. |
| `src/lib/api/` | The wire contract (`types.ts` mirrors `portal_dto.go`), the axios client, the fetchers and the query keys. |
| `src/lib/hooks/usePaymentStream.ts` | Waits for a PIX charge to settle, over SSE. |
| `src/components/portal/` | Pieces specific to billing: status badge, money, invoice row, PIX panel. |
| `src/dev/` | Mock fixtures and the scenario switcher. Not shipped. |

Anything not specific to billing belongs in [`@aoctech/ui`](../../ctech-ui), not here.

## Two things that will trip you up

**`@aoctech/ui` is a `file:` link** to a sibling checkout while it is unpublished. That is why
`next.config.ts` widens the Turbopack root to the repositories' parent — Turbopack will not resolve
a module outside its root. Narrow it back when the package comes from npm.

**Routes are English, labels are Portuguese.** `/invoices`, `/subscriptions`; "Faturas",
"Assinaturas". The URL is part of the API surface and matches `/v1.0/portal/invoices`; the text on
screen is what a customer reads.

**There are no dynamic segments, and there cannot be.** A production build is `output: "export"`
and ships as objects in S3 behind CloudFront, so every route is one prerendered file. An invoice is
`/invoice?id=inv_…`, not `/invoices/[id]` — the latter would need `generateStaticParams` to
enumerate every invoice that will ever exist. Anything that needs a subject reads it from the query
string, inside a `<Suspense>` boundary because `useSearchParams` suspends during prerender.

## One route is indexed, and it is not one of yours

`/` — the public landing — is the only page search engines are invited to. It is
a server component for exactly that reason: it has to export its own `metadata`
to override the root layout, and a client component cannot.

Everything else is `noindex, nofollow`, said twice on purpose — in
`src/app/layout.tsx` and in `public/robots.txt`, which fail differently. What is
being protected is one customer's invoices appearing in a search result. Open
Graph tags are still on every page and still work: preview scrapers ignore
robots, which is what makes a checkout link pasted into WhatsApp unfurl
properly, and WhatsApp is the channel these links actually travel through.

`app/robots.ts` was tried and removed: a static export refuses a route handler
without `export const dynamic = "force-static"`, which is three lines of
ceremony to emit four lines of text.

Titles come from a `layout.tsx` per route, because every screen is a client
component and a client component cannot export `metadata`. The invoice number is
not knowable at build time — one static file serves every invoice — so
`useDocumentTitle` replaces it once the data lands.

## What the front end does not decide

The API sends `state` as a finished sentence — "Vence em 3 dias", "Vencida há 4 dias" — with a
`tone` from a closed set of four. The internal status is not in the payload at all (ADR 0012), and
neither is metadata, an audit trail or an organization. Any code here that maps a status to a
phrase is a bug: the translation lives in `api/internal/api/v1/portal_dto.go`, once.
