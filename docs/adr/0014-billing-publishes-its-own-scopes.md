# ADR 0014 — Billing publishes its own OAuth scopes

Status: Accepted (2026-08-16) · Supersedes the "register 14 scopes in ctech-account" item that
PLAN.md Phase 0 carried

## Context

Every route in this service is gated on a scope (`billing:invoices:read`,
`billing:me:subscriptions:write`, and twelve more). A scope only works if two systems agree on it:
this service, which **verifies** it on the request, and ctech-account, which **issues** it in the
token. Nothing verifies a scope ctech-account never learned to issue — the token simply arrives
without it and every call 403s.

The obvious way to keep them in step is to write the list in ctech-account. That makes ctech-account
the place where every other service's vocabulary is maintained: adding a route here becomes a pull
request there, merged by somebody who cannot tell whether the scope is right, and the two lists drift
the first time a deploy order slips.

`ctech-dfe` had the same problem and solved it already: it keeps its scope manifest in its own
repository and publishes it from its own pipeline
(`ctech-dfe/.github/workflows/deploy.yml` calls the reusable
`artur-oliveira/ctech-account/.github/workflows/publish-resource-scopes.yml@main`).

## Decision

**Billing owns its scope manifest and publishes it as a deploy stage**, following the pattern
already in production for ctech-dfe.

- `api/internal/oauthresource/scope-manifest.json` is the manifest: 14 scopes, each with a pt-BR and
  an en description, a visibility and a status. It is `//go:embed`-ed into the binary.
- The same package mounts **RFC 9728** Protected Resource Metadata at
  `/.well-known/oauth-protected-resource`, so a client can discover the authorization server and the
  scopes this resource accepts without being told out of band.
- `deploy.yml` calls ctech-account's reusable publish workflow with
  `resource_server_id: billing`, before the API stage.

**A test asserts the manifest and the enforced list are the same set** — `middleware.AllScopes` is
what the running service checks, the JSON is what ctech-account is told to issue, and a divergence
fails in one direction only (tokens without the scope), silently, at runtime.

## Consequences

- **Adding a route is one repository's change.** A new scope is a line in the manifest and a line in
  `AllScopes`, and the pipeline tells ctech-account. No cross-repo pull request, no ordering
  agreement between two humans.
- **Scopes publish before the API deploys.** The order in `deploy.yml` is Terraform → scopes → API →
  frontend, and this is the reason for the second position: publishing after would leave a window in
  which a new route is live and no token can reach it.
- **The manifest is the API's public description of itself.** The RFC 9728 document is derived from
  the same file, so what a client discovers is what the pipeline published, not a second hand-written
  copy.
- **This service is the OAuth *resource server*, not an authorization server.** It publishes what it
  accepts; it issues nothing, stores no credential, and verifies every token against ctech-account's
  JWKS.

## Limits accepted

- **Billing can name a scope ctech-account will accept.** The publish credential is scoped to
  `resource_server_id: billing`, so it can only touch the `billing:*` namespace — but within that
  namespace this repository is authoritative, and a bad scope name shipped here is a bad scope name
  in the authorization server. The equality test catches drift, not judgement.
- **The pipeline depends on credentials another repository provisions**
  (`/ctech-account/{env}/scope-publishers/billing/{client-id,client-secret}`). Absent them the stage
  fails loudly, which is the correct failure and still a dependency.
- **The equality test compares sets, not meanings.** Renaming a scope in both files at once passes,
  and is exactly the change that invalidates every token already issued. That is a deploy-ordering
  concern, and no test in this repository can see it.

## Reopen if

A second resource server needs a scope in billing's namespace, or the manifest grows structure that
belongs to the authorization server rather than to the resource (consent copy, grant restrictions,
per-client defaults). Those are ctech-account's to own, and the seam moves.
