# Spec — Global search (⌘K) across Portal, Console and Finanças; Portal at full width

Status: **Draft for review** · 2026-10-14 ·
Builds on: the sidebar shell ([#127](https://github.com/artur-oliveira/ctech-billing/pull/127), [#128](https://github.com/artur-oliveira/ctech-billing/pull/128)),
`lib/finance/createRequest.ts` (the phone's create button), ctech-dfe's `GlobalSearch` (the reference behaviour) ·
Touches two repos: **ctech-ui** (new shared component, release 0.8.0) and **ctech-billing** (consumer).

## 1. Goals

1. The Portal uses the width of the page area like the Console and Finanças, instead of a fixed 2xl column.
2. A person can press ⌘K / Ctrl+K (or tap the search button in the top bar) from any screen, type part of a screen's
   name and go there. Each result shows its **subproduct** (Portal, Console, Finanças) and the screen's name.
   Choosing a result in another subproduct switches the current subproduct, because each area has its own layout.
3. Bonus: **create actions** are results too. Typing "recor" lists "Recorrências" and "Nova recorrência"; the second
   goes to the recurrences screen with the create form already open.

Non-goals: searching data (invoices, customers, bills), recent items, a cross-app search across the CTech family,
migrating ctech-dfe's own `GlobalSearch` to the shared component (a separate effort).

## 2. Portal width

`PortalLayout` passes `contentClassName="max-w-2xl"` to `BillingShell` (`ui/src/app/(portal)/layout.tsx`). Remove it.
Then, with the browser, review every Portal screen at ≥1280px (dashboard, invoices, invoice, subscriptions,
subscription, checkout-adjacent panels, terms gate, no-billing-account) and fix what stretches badly: text-only
panels keep a readable measure (`max-w-prose` on the paragraph, not the page), lists and cards use a grid or two
columns from `lg`, the way "Quero cobrar meus clientes" already does. No behaviour change.

## 3. ctech-ui: `CommandPalette` (release 0.8.0)

### 3.1 API

```ts
type CommandEntry = {
  id: string                 // stable, unique
  label: string              // "Recorrências", "Nova recorrência"
  group: string              // the subproduct, shown as context: "Finanças"
  kind: "page" | "action"    // actions are marked (a "+" glyph and an "Ação" hint), pages are not
  keywords?: string[]        // synonyms: ["recorrente", "assinatura"]
  icon?: ReactNode
  href?: string              // navigation, through renderLink
  onSelect?: () => void      // for actions; runs after the dialog closes
}
CommandPalette({entries, open, onOpenChange, renderLink, locale, labels?})
CommandPaletteTrigger({onClick, locale, labels?, className?})
```

`labels` (placeholder, empty state, "Esc", "Ação", dialog name) default per `locale` from `lib/i18n.ts`, in the same
shape as `BottomNavLabels`/`ModalLabels` (pt-BR, en). The component is **controlled**; the consuming app owns `open`.
`useCommandPaletteShortcut(onToggle)` binds ⌘K / Ctrl+K (it ignores the key when focus is in an editable field only if
the field is not the palette's own input; the shortcut works from inputs elsewhere, as in ctech-dfe).

### 3.2 Matching

Own matcher, **no new dependency** (no Fuse.js in the shared package):

- Normalize both sides: lowercase, strip diacritics (NFD), collapse whitespace. "cartao" matches "Cartões".
- Tokenize the query; every token must match (AND).
- Score per entry, best first: label starts with the query > a label word starts with a token > label contains the
  token > keyword starts with / contains the token > group contains the token. A label hit always outranks a keyword
  hit, as in ctech-dfe (weights 3/2/1).
- Ties keep the entries' given order, so the app controls the default order (sections as in the nav).
- Empty query: show the first 8 entries in given order. Non-empty: top 8 by score.
- A pure function `matchEntries(entries, query, limit)` exported for tests and for apps that want to reuse it.

### 3.3 Behaviour and accessibility

- Built on Base UI's Dialog directly, as `Drawer` is (`Modal` carries a title bar and a footer a search does not
  want): focus trap, Esc, scroll lock, portal. Top-anchored (≈10vh), full width minus gutters on a phone.
- Input is a combobox (`role="combobox"`, `aria-controls`, `aria-activedescendant`); results are a `listbox`
  of `option`s. ArrowDown/ArrowUp cycle, Enter selects, mouse hover moves the cursor. The active option is scrolled
  into view.
- Results are grouped under the group name when the query is empty and shown flat, with the group as secondary text,
  when searching (a flat ranking is the point of searching).
- Page results render through `renderLink` (a real `<a>`: middle-click and open-in-new-tab work); action results are
  buttons. Both close the dialog.
- Touch: rows ≥44px, input font ≥16px so iOS does not zoom. Respects the package's `DensityScope` tokens and has no
  colours of its own (themed by the consumer's CSS variables, as every component in the package).
- The trigger shows a search icon, the placeholder text and a `Ctrl K` / `⌘K` hint from `sm`; under `sm` the icon only
  (min 44px target on touch).

### 3.4 Package work

Add `src/components/command-palette.tsx`, `src/lib/match.ts`, export from `src/index.ts`, labels in `src/lib/i18n.ts`,
tests (§ 6), a Storybook story, a line in `docs/component-inventory.md`, bump `package.json` to 0.8.0. ctech-ui's local
checkout is 3 commits behind origin: pull first; the new work builds on whatever `main` is at that point.

## 4. ctech-billing

### 4.1 Entries come from the navigation's own lists

`ui/src/lib/search/entries.ts` builds `CommandEntry[]` from the lists that already draw the navigation, so a new
screen added to a nav is searchable with no second edit:

| Subproduct | Source | Visible when |
| --- | --- | --- |
| Portal | `NAV` in `(portal)/layout.tsx` (moved to `lib/portal/nav.ts` so both read it) | the portal session did not answer "no billing account" |
| Console | `consoleNav(active, reviewer)` | the console session lists an organization with a tenant in the current mode (`active`); Solicitações only for a reviewer |
| Finanças | `visibleFinanceSections(personal, canConfigure)` | always (everyone has a personal space) |

Access reuses the queries the `AccountMenu` already runs (`portalKeys.session`, `consoleKeys.session()`, same keys and
stale times, so no extra requests) and `useFinanceSpaces()`/`useSpace()` for the space. A subproduct whose access is
unknown yet (query pending) contributes no entries rather than entries that may fail; the person's current
subproduct always contributes (it is on screen).

Entry `group` is `t("common.account.<view>")` (the same strings the account menu and the wordmark use). Keywords are
added in `locales/{pt-BR,en}/common.json` under `search.keywords.<key>` for the screens whose name is not what people
type (Agenda ↔ "contas a pagar", "boletos"; Extrato ↔ "saldo"; Cartões ↔ "fatura"). Keywords are optional per entry.

### 4.2 Create actions

`lib/finance/createRequest.ts` moves to `lib/createRequest.ts` (it is no longer Finanças-only) and `CreateKind` gains
`"product"` and `"webhook"`. Everything else about it stays: held ≤3 s, taken once, dropped when the person lands
elsewhere, deliberately **not a URL parameter** (a `?novo=1` would reopen the form on every reload and shared link).
The bottom nav, the six `useCreateRequest` call sites and their tests are updated for the move.

| Action | Kind | Target | Permission shown to |
| --- | --- | --- | --- |
| Novo lançamento | `bill` | `/finance/bills` | `finance.write` |
| Nova compra | `purchase` | `/finance/cards` | `finance.write` |
| Nova recorrência | `recurrence` | `/finance/recurrences` | `finance.write` |
| Nova transferência | `transfer` | `/finance/statement` | `finance.write` |
| Nova conta | `account` | `/finance/accounts` | `finance.configure` |
| Novo produto | `product` | `/console/catalog` | Console `active` |
| Novo webhook | `webhook` | `/console/webhooks` | Console `active` |

Selecting an action runs `requestCreate(kind, target)` then `router.push(target)`. The screen opens its drawer on mount
through `useCreateRequest(kind, …)` and still applies its own checks (the webhooks screen's `atLimit`, the role).
`catalog/page.tsx` and `webhooks/page.tsx` gain a `useCreateRequest` call each. Already on the target screen: the
request is taken at once by the mounted listener, so it opens in place. The action's label comes from the existing
`finance.nav.create.<kind>` strings where they exist; `common.search.actions.*` for all seven, since the screens' own button texts ("Novo produto", "Novo endpoint") differ from the verbs a search wants ("Novo webhook").

### 4.3 Mounting

`BillingShell` owns the palette state, renders `<CommandPaletteTrigger/>` in the `actions` slot (next to the language
switch; on a phone the trigger stays and the language switch stays hidden as now) and `<CommandPalette/>`, and binds
the shortcut. Since all three areas render `BillingShell`, the search exists everywhere, including the Portal's
no-billing-account state. A hook `useSearchEntries(view)` returns the entries for the current person; the palette is
lazy-mounted on first open so the queries and the matcher cost nothing until used.

Choosing a result in another subproduct is a plain navigation: the destination layout (portal, console, finance)
takes over. The Console's guard already redirects a person without organization to Finanças, so a stale entry cannot
strand them.

### 4.4 i18n and docs

New keys in `locales/{pt-BR,en}/common.json` (`search.*`: trigger label, placeholder, empty state, keywords). Update
`ui/DESIGN.md` (the shell section: the search trigger and the palette) and `OVERVIEW.md`/`ARCHITECTURE.md` only if
they describe the top bar.

## 5. Delivery

1. **ctech-ui PR**: `CommandPalette`, matcher, labels, tests, story, inventory, 0.8.0. Merge, publish.
2. **ctech-billing PR**: bump `@aoctech/ui` to 0.8.0 (+ lockfile), `createRequest` move and new kinds, entries,
   shell mounting, locales, Portal width, DESIGN.md. Following the repo's habit, the lockfile bump may be its own
   commit.

No infrastructure, cost or API change in either.

## 6. Testing

- **ctech-ui** (vitest): `matchEntries` (diacritics, AND of tokens, ranking label > keyword > group, stable ties,
  limit, empty query); the palette (opens focused, typing filters, arrows/Enter/Esc, action runs `onSelect` after
  close, page result renders a link through `renderLink`, empty state, labels per locale); the shortcut hook.
- **ctech-billing** (vitest): `entries.ts` by situation: no organization (no Console entries), no billing account (no
  Portal entries), personal space (Plano yes, Conexões no), organization space with `finance.configure` (Conexões
  yes), viewer role (no create actions), reviewer (Solicitações); `createRequest` still holds, expires and drops by
  target after the move; the catalog and webhook screens open their drawer on a request; the shell opens the palette
  on Ctrl+K.
- **Browser check** (chrome-devtools) before the PR: Portal at 1440px and 390px; ⌘K from each area; "recor" →
  "Nova recorrência" lands with the drawer open; switching Portal → Finanças → Console through results; no
  horizontal scroll at 390px; keyboard-only run.

## 7. Decisions and why

- **Shared in ctech-ui (owner's choice)**, since the family wants one search and ctech-dfe's is local; the cost is one
  release cycle before Billing can consume it.
- **No Fuse.js in the shared package**: a ~60-line matcher with diacritic folding covers navigation search and keeps
  the package's dependency list at what it is.
- **`createRequest`, not a query parameter**, for the create actions (the owner suggested a query parameter): same
  outcome (the form is open on arrival) without a parameter that reopens it on reload and in shared links. The
  mechanism already exists and has tests.
- **Entries derived from the nav lists**, not a second registry that drifts.
- **Search inside `BillingShell`**, not per area, so a fourth area gets it for free.
