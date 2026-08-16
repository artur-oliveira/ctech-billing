# ADR 0011 — Console sessions: tenant from the owner, mode from the request

Status: Accepted (2026-08-15) · Extends [ADR 0003](0003-tenant-and-livemode-partition-key.md)
· Precondition for the console screens (assessment § 15, C1–C9 and C17)

## Context

[ADR 0003](0003-tenant-and-livemode-partition-key.md) settled both halves of tenancy on the M2M
surface: the organization comes from the credential, and so does test-vs-live. There is no
`organization_id` parameter and no `livemode` flag on any route, because a parameter can be changed
by a client and a credential cannot.

The console breaks the second half, and it has to. **A person is not an integration.** The same
human legitimately works in test and in live within one session — that is what a test mode is for —
while an integration provisioned for test must never reach live data even by accident. Applying the
M2M rule literally would mean issuing a browser two OAuth clients and asking the user to sign in
twice to see their own sandbox.

There is also no credential to resolve. `middleware.ResolveTenant` maps an OAuth client id to an
`APICredential`; a browser token's subject is a person, and billing stores no membership table —
[ADR 0007](0007-minimal-organization.md) keeps `Organization` at one owner and explicitly refuses to
grow a second RBAC model.

## Decision

A separate route group, `/v1/console/*`, with its own resolver
(`middleware.ResolveConsoleTenant`):

- **The organization comes from the token's subject**, resolved through a sparse lookup key
  `{mode}#OWNER#{user_id}` on the organization row. Nothing the browser sends names an organization
  — the M2M rule survives intact on the half that matters.
- **The mode comes from the request**, in a required `X-Billing-Mode: live | test` header. Required,
  not defaulted: a console that forgets it fails loudly rather than quietly showing the wrong world.
- **`RequireUserScope` rejects service tokens** on every console route, exactly as `RequireM2MScope`
  rejects session tokens on every M2M route. The two are mirrors, and that is what stops the mode
  header from becoming a way for an integration to cross modes: a client-credentials token cannot
  reach a route that reads the header at all.
- **The scopes are the same manifest**, `billing:invoices:read` and friends, plus
  `billing:products:read` and `billing:organization:read`. Not a parallel `billing:console:*` set:
  the resource is the same resource, and a second naming scheme for the same data is a second thing
  to keep in agreement.
- **The console surface is read-only** in this ADR. Every mutation a merchant performs already
  exists on the M2M surface or does not exist yet; a second write path to the same entities is a
  second place for the audit cause to be wrong. Console writes arrive with the screens that need
  them, each carrying its own `Cause`.

## Consequences

- The mode is attacker-controlled input on the console surface, and that is acceptable **because it
  cannot widen access**: the owner lookup is performed per mode, so asking for `live` when you own
  only a test organization returns the same 403 as owning nothing. Both partitions are yours or
  neither is.
- One owner, one organization per mode. When `Organization` becomes multi-member, `GetByOwner`
  returns a list and the console grows an organization switcher. Until then a switcher is UI for a
  state that cannot exist.
- The organization row now carries a `lookup_pk`. It is sparse: an organization provisioned without
  an owner is simply not reachable from a browser, which is the safe direction to fail in.
- An operator's actions will appear in the audit trail as a person rather than a client id. That is
  the reason `Actor` was never typed as a client — see `AuditLog.Actor`.

## Limits accepted

- No per-organization roles. Owner-only means the first merchant with a finance assistant will ask
  for one, and the answer is a membership model **in `ctech-account`**, consumed by billing — never
  a second one grown here (assessment § 12.2, ADR 0007).
- The lookup is one row per (owner, mode). A user who owns two organizations gets the first one
  DynamoDB returns; that is unreachable today by construction (provisioning is manual) and is the
  first thing to fix when membership arrives.

## Reopen if

`Organization` becomes multi-member, or `ctech-account` starts issuing an organization claim in the
access token. Either makes the owner lookup redundant, and the second one moves the decision out of
billing entirely — which is where it belongs.
