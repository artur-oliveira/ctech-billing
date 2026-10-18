# ctech-ui upstream proposals

Billing built these on top of `@aoctech/ui`. None is billing-specific. Candidates to move into
`ctech-ui` so the other CTech apps stop re-building them. Paths are under `ui/src/components/`.
Counts are files using the component today. Nothing here edits ctech-ui; this is the list.

| # | Component | Proposed props | Used in | Notes |
|---|---|---|---|---|
| 1 | `Checkbox` indeterminate dash | none new; fix `indeterminate` to draw a bar, not `CheckGlyph` | `ui/Checkbox.tsx` wrapper, 6 call sites (UnclassifiedBulk uses it) | Bug fix, not an addition. `checkbox.tsx` renders the check for `data-[indeterminate]` too. Wrapper hides the svg and draws an `after:` bar; delete the wrapper once fixed. Highest value, smallest change. |
| 2 | `Tabs` | `label`, `value`, `onValueChange`, `options: {value,label}[]`, `idBase`, `className` | Finanças reports + one more screen (2) | ARIA tablist with roving focus, panel ids derived from `idBase`. |
| 3 | `ContainerSwitch` | `min` (px), `className`, `children(narrow: boolean)` | 4 (table vs list, wide row vs stacked) | Chooses layout by the box width, not the window (sidebars make the viewport lie). Uses `ResizeObserver`, media query fallback for SSR/tests. Prefer Tailwind `@container` where CSS can do it; this is for when JS must pick a different tree. |
| 4 | `Section` | `title`, `action?`, `hideTitle?`, `tone?: "foreground"\|"muted"`, `className?` | 20 | Landmark named by its own `h2` (`useId`), heading row with optional action. |
| 5 | `Amount` / `AmountRow` | `Amount`: `tone`, `strong`, `struck`; `AmountRow`: `label`, `strong`, `as: "div"\|"li"` | 17 | Tabular figures via `data-numeric`, figure pinned right and never shrunk. Needs `[data-numeric]` rule in the shared stylesheet. |
| 6 | `MoneyInput` | `value` (masked string), `onValueChange`, `signed?`, rest of `Input` | 6 | Locale-masked BRL entry. Parsing lives in billing's `lib/money`; move both together. |
| 7 | `InlineConfirm` | `text`, `confirmLabel`, `cancelLabel?`, `tone: "danger"\|"brand"`, `pending?`, `error?`, `announce?`, `onCancel`, `onConfirm` | 7 (+ Plano cards) | In-place confirmation under the row it concerns, safe answer first. Alternative to a modal for non-blocking destructive steps. `cancelLabel` absent = retry offer. |
| 8 | `MonthField` | `id`, `value` (`YYYY-MM`), `onValueChange`, `min?`, `max?`, `placeholder?`, `disabled?`, `invalid?` | 1 | Month picker that follows the app locale; `<input type="month">` ignores `lang`. |

Also local but not proposed yet: `StatusBadge` (glyph plus word, wraps `Badge`; needs a shared glyph-per-tone
convention first), `DateField`, `FileDrop`-style import widgets.

## Shell / navigation (final pass)

- `BottomNav`: full-strength focus ring on the icon pill and the action button (lib draws `ring-ring/50`, faint on white). Billing overrides it in `globals.css`.
- `BottomNav` / `BottomNavSpacer`: a short-viewport variant (`@media (max-height: 32rem)`): 72px of a 360px landscape phone is 20%. Make `BAR_HEIGHT` a CSS variable so apps can tighten it; the spacer must follow.
- `SidebarNav`: 44px rows under touch (`touch:min-h-11`) built in; billing wraps the link renderer (`renderNavLink`).
- `SidebarNav` / `BottomNav` `More`: optional badge (pending count) for an item, for the action queue (Pendências lives under Mais on a phone).
- `AppShell`: header should shed the brand text under `md` itself (brand + 240px context + search label + language + avatar = 664px at 640).
- `AppShell`: honour `env(safe-area-inset-left/right)` in landscape.
