# Spec — Shared and additional personal spaces

Status: **Implemented (6.8)** · 2026-10-09 · see the implementation amendment at the end ·
Decision: [ADR 0027](../adr/0027-personal-workspaces-in-account.md) (extends
[ADR 0025](../adr/0025-spaces-personal-and-organization.md)) ·
Counterpart in ctech-account: `ctech-account/docs/specs/2026-10-09-personal-workspaces.md` ·
Builds on the finance module: [`2026-10-07-finance-erp-design.md`](2026-10-07-finance-erp-design.md)

## 1. What this is

A person can create **more personal spaces** than the default one, and **share** any of them with
other people, at one of two access levels: **Leitura** (read) or **Acesso total** (full). The case
that motivates it: a couple keeping their finances together without creating an organization, which
carries a company, a CNPJ and an onboarding that a household does not need.

What it is **not**:
- **Not a consolidated view.** Each space is still read on its own (ADR 0025).
- **Not a migration path.** A new space starts empty. No code reads one space and writes another.
- **Not billed yet.** Limits per plan are the plans spec's business (§ 7).

## 2. The three kinds of space

| Kind | Key `S` | Shareable | Owner | Lives in |
|---|---|---|---|---|
| Default personal | `USER#{sub}#{mode}` | **never** | the user, implicitly | billing only |
| Additional personal | `{workspace_id}#{mode}` | yes | whoever created it | a ctech-account workspace, `kind: personal` |
| Organization | `{organization_id}#{mode}` | yes (as today) | the organization's owner | a ctech-account workspace, `kind: organization` |

**The default personal space stays private.** Its key is the person's identity. Sharing it would make
a partition named after one person reachable by another, and would leave no clear answer for the purge
when that person deletes their account. The CTech subscription expenses of finance 6.7 keep landing
there and never in a shared space.

**An additional personal space is a ctech-account workspace** — the same record an organization is,
with `kind: personal` and no company. Membership, invitations, role changes, leaving, ownership
transfer and account deletion are all ctech-account's existing machinery (ADR 0023). Billing gains no
membership table, no invitation flow and no second answer to "who is in this space".

The interface calls it **Espaço**. The word "organização" never appears for a `personal` workspace,
and "empresa" never does either.

## 3. Roles and verbs

ctech-account restricts a `personal` workspace to three roles; billing maps them:

| Shown as | ctech-account role | Billing verbs |
|---|---|---|
| Dono | `owner` | all; only the owner manages people (in ctech-account) |
| Acesso total | `member` | all finance verbs, `finance.configure` included |
| Leitura | `viewer` | `finance.read` |

`VerbsForRole(role)` becomes `VerbsFor(kind, role)`:
- `kind = organization`: today's table, unchanged (`owner`/`admin` all, `member` all but configure,
  `viewer` read).
- `kind = personal`: the table above. **`admin` grants nothing** — ctech-account refuses it on a
  personal workspace, so seeing it means something upstream is wrong, and the answer is the ordinary
  404.
- Any other kind grants nothing → 404. A new kind upstream must never inherit a grant by default.

## 4. Resolution — what changes and what does not

**Unchanged:** the selector (`X-Billing-Space: org:{id}`, the id being the workspace id whatever its
kind), the `Resolver`, the single 404, the zero table reads before membership, the 60 s cache keyed on
both ids, and failing closed when ctech-account is unreachable. A shared space adds **no new path**
through the resolver; it is a workspace like an organization. That is the main protection this design
buys.

**Changed:**
- `MembershipSource.Membership` returns `(kind, role, member, err)`. The ctech-account route adds
  `kind` to its answer (§ 8).
- The cache entry stores `kind` beside `role`, under the same key and TTL. An entry written before the
  deploy has no `kind` and is read as `organization` — for `member` that grants **fewer** verbs than
  `personal` would, never more.
- `ResolvedSpace` gains `Kind()` (`personal_default` for `USER#`, `personal`, `organization`), so the
  console can decide what to show. It is read-only, like every other field.

**Accepted limit, unchanged from ADR 0025:** a person removed from a space may keep reading for up to
60 s. They cannot write anything they could not already write in that minute.

## 5. Console

### 5.1 The switcher

`GET /v1.0/console/finance/spaces` lists, always for the token's own subject:
1. *Pessoal* (the default space);
2. additional personal spaces, alphabetically;
3. organizations, alphabetically.

Each item carries `selector`, `kind`, `display_name`, `role`, `verbs` and `manage_people` (true for
the owner of a `personal` workspace only). The list is information, never authority: every request
still resolves its space through the membership check.

In a `personal` workspace the console shows **Finanças only**: invoices, subscriptions, customers and
catalogue never appear. The **portal's** selector (ADR 0025, 2026-10-07 amendment) lists organizations
only, never personal workspaces: a space has no CTech subscription of its own.

### 5.2 Creating a space — handoff

ctech-account is the only writer of tenancy ("a product never writes tenancy; it redirects", from
ctech-account's organization handoff spec). Billing redirects:

```
Billing  "Novo espaço"
         state = 128 random bits → sessionStorage
         browser → {ACCOUNTS}/account/spaces/new?client_id=billing&return_to={BILLING}/console/finance/spaces/created&state=…

Account  validates (first-party client, registered origin), creates the workspace (kind personal,
         owner = the signed-in user)
         → return_to?organization_id=…&state=…        (ids only, never a token)
         → return_to?cancelled=1&state=…               (backed out)

Billing  state missing or different from the stored one → discard silently, show the switcher
         otherwise → reload the spaces list; select the new id ONLY if it is in that list
```

The id on the return URL is **never** used as a selector directly. If it is not in the list the user
gets back from the server, nothing is selected, and any request carrying it would get the 404 anyway.

### 5.3 Managing people — handoff

"Gerenciar acesso" shows only when `manage_people` is true and opens
`{ACCOUNTS}/account/spaces/people?id={id}&client_id=billing&return_to=…` (ctech-account's route as implemented;
see the amendment). Inviting, changing a
role, removing and transferring ownership all happen there. Billing does **not** show a member list: a
copy here would be a second source for the question ctech-account answers.

### 5.4 Deleting a space

**Not in v1.** ctech-account's workspaces have no deletion today (transfer and leave only), and
deleting one needs a per-workspace purge protocol across products, the way `user.erase` works for a
person. Until then the owner can remove everyone and stop using the space. Billing's side of the purge
is already designed (ADR 0026) and needs no change when the protocol arrives.

## 6. Account deletion

Nothing new in billing's design. ctech-account's deletion spec already decides:
- a workspace whose only member is the person being deleted is erased, and listed in
  `organizations[]` on `user.erase` — billing purges each listed workspace's finance partitions, of
  either kind, exactly as an organization's;
- a workspace with other members **blocks** the deletion until ownership is transferred; on a
  `personal` workspace only to someone with *Acesso total*.

Billing's purge itself is still unbuilt (see PLAN.md); this spec only adds that personal workspaces are
in its scope.

## 7. Plans (deferred)

The intended shape, for the plans spec to decide:

| Plan | Additional spaces the owner may create | People per space |
|---|---|---|
| Free | 1 | 1 invitee |
| Basic | 3 | 5 |
| Pro | 10 | 10 |
| Sob demanda | metered | metered |

**Where it will be enforced:** in ctech-account, at **creation** and at **invitation**, because those
are ctech-account's writes. ctech-account asks billing for the owner's entitlement at that moment.
Nothing is limited or charged in v1.

## 8. What ctech-account must provide

Specified in `ctech-account/docs/specs/2026-10-09-personal-workspaces.md`. In short:
1. `kind` on the workspace (`organization` default, `personal`); a `personal` workspace never links a
   company.
2. On `personal`: roles `owner`/`member`/`viewer` only; invitations and role changes by the owner only.
3. Handoffs `/account/spaces/new` and `/account/spaces/{id}/people`, under the existing handoff rules.
4. `kind` in the answers of `GET /internal/organizations/:id/members/:user_id` and
   `GET /internal/users/:user_id/organizations`.
5. Organization lists filter `kind = personal`. **The DF-e needs no filter** — it never lists
   workspaces, only its own companies, and a personal workspace has none; it checks the workspace kind
   in depth on its company reach instead (`ctech-dfe/docs/specs/2026-10-09-personal-workspaces-in-dfe.md`).
6. Account deletion: unchanged rules, applied to both kinds.

**Deploy order:** ctech-account first (the field, the routes answering `kind`, the handoffs), then
billing. Billing reading a route that does not yet send `kind` treats the space as `organization`,
which is the safe direction (§ 4).

## 9. Tests — no cross-space leak

Part of the definition of done, not a later hardening pass:

1. A member of space A selecting B → 404 and **zero DynamoDB calls**, with the counting client, now
   with `personal` fixtures as well as organization ones.
2. A `viewer` in a personal space is refused on **every** write route, driven by the route/verb table
   the gate tests already walk.
3. ctech-account answering `admin` on a `personal` workspace, or an unknown `kind` → 404.
4. A member removed upstream loses access when the cache expires; an outage is never cached.
5. A forged `organization_id` on the handoff return, for a workspace the person is not in → nothing
   is selected, and any request with it → 404.
6. The default personal space cannot be addressed by id: `org:USER#…` fails the UUID validation.
7. Ids of bills, accounts and cards from another space → 404, including between **two personal spaces
   of the same user**.
8. A cache entry without `kind` resolves `member` to the organization verbs, never the personal ones.
9. The spaces list only ever contains the token's own subject's workspaces.

## 10. Out of scope

- Deleting a space (§ 5.4).
- Moving or copying data between spaces.
- A member list inside billing.
- Plan limits (§ 7).
- Per-person permissions finer than the two levels.

## Amendment, implementation (2026-10-09, 6.8)

Settled by the implementation ([plan](../plans/2026-10-09-finance-6.8-shared-spaces.md)), against ctech-account
PR #46 as merged-to-be:

- **Upstream contract.** The membership route answers `{member, role, kind}`; a refusal is `{member:false}` with no
  kind. The user-organizations route lists workspaces of **both** kinds, each with `kind` — billing splits them,
  not ctech-account. Handoff validation reuses `GET /v1.0/organizations/handoff`.
- **People route:** `/account/spaces/people?id={id}` (a static export has no dynamic segments). Billing sends
  `client_id` and `return_to` and no `state`: nothing on that return is acted on.
- **Return path:** `/console/finance/spaces/created` (Finanças lives under `/console`). The stored `state` is
  single-use and is removed whatever the outcome. A missing stored state discards (stricter than ctech-dfe's
  company handoff, which completes without one): a person whose browser refuses `sessionStorage` sees the new
  space in the list, unselected.
- **Kinds on the wire:** `personal_default` (Pessoal), `personal`, `organization` — in the list and in
  `GET /console/finance/space`. `selector` is the only thing the console selects with; *Pessoal* is translated by
  the console from its kind.
- **Normalising the kind** happens once, in the resolver, after the cache: an absent kind (an old cache entry, a
  ctech-account without kinds) is `organization`; any other unknown value is kept and grants nothing.
- **Categories:** a personal workspace is seeded with the personal chart, like Pessoal.
- **The 6.7 CTech-invoice switch** shows in Pessoal only — the one space the payer side posts to (§ 2).
- **§ 5.1 "Finanças only"** needed no code: the invoicing sections follow the console session (ADR 0011), not the
  finance space. **The portal selector** is not built yet; when it is, it must filter `kind = personal`.
