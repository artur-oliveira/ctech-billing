---
register: product
platform: web
theme: light
colors:
  brand-50: oklch(0.965 0.014 45)
  brand-100: oklch(0.930 0.028 45)
  brand-600: oklch(0.440 0.095 45)
  brand-700: oklch(0.380 0.085 45)
  background: oklch(1 0 0)
  surface: oklch(0.976 0.004 45)
  foreground: oklch(0.220 0.014 45)
  muted-foreground: oklch(0.540 0.018 45)
  border: oklch(0.910 0.006 45)
  ring: oklch(0.440 0.095 45)
  success: oklch(0.500 0.130 150)
  warning: oklch(0.500 0.120 58)
  danger: oklch(0.500 0.190 22)
  danger-strong: oklch(0.440 0.175 22)
typography:
  family: IBM Plex Sans (single family, all weights)
  body: 0.875rem / 1.5
  figure: 1.5rem / 600 / -0.015em
  hero: 1.875rem / 600 / -0.02em
  h1: 1.25rem / 600 / -0.01em
radius:
  sm: 0.375rem
  md: 0.5rem
  lg: 0.625rem
  xl: 0.75rem
components: "@aoctech/ui"
---

# Billing — visual system

The tokens live in `src/app/globals.css`. This file explains the ones that were
arguments, and records what was tried and rejected so it is not re-tried.

## The colour is sienna, and the chroma is the decision

`brand-600` is `oklch(0.440 0.095 45)` — `#7c3f22`. Burnt terracotta.

Hue 45 was chosen because it is one of three bands no other CTech app occupies.
The family holds 150–180 (dfe green, NFS-e teal), 258 (account cobalt), 296
(wallet violet) and 27 (poker `#af2a2f`). Warm-orange, olive and plum were free;
warm-orange was picked, and then it had to survive contact with a real screen.

**It did not, at first.** The palette started at `oklch(0.470 0.145 36)`. On the
overdue-invoice screen — an `urgent` badge above a pay button — that was simply a
red, and the page read as an error rather than a bill. The de-risking rule
("brand on fills, status on badges, never the same component") held structurally
and did not help: two reds are two reds whatever shape they are in.

The fix was **chroma, not hue**. Dropping to 0.095 leaves the brand warm and
unmistakably not-grey while making `danger` the only saturated colour on any
screen — which is what a Restrained palette is supposed to mean. Measured in Lab
the brand now sits at C≈39 against danger's C≈69, 21° apart in hue.

Rejected on the way, so they are not re-proposed:

| Candidate | Why not |
|---|---|
| `0.470 0.145 36` terracotta | Reads as red beside the urgent badge. The original. |
| `0.550 0.140 48` orange | Lands on `warning`'s amber. Two attention colours. |
| `0.420 0.075 50` umber | Reads as a disabled control, not a primary action. |
| `0.380 0.055 55` coffee | Dead. No identity left. |
| `0.430 0.110 330` plum | Distinct, but close to wallet's violet and reads consumer-fintech. |
| oxblood | Poker's `#af2a2f` / `#5b1218` already own it. |

## The canvas is pure white

`background` is `oklch(1 0 0)`, with no tint at all. With a warm brand, tinting
the canvas warm as well is what produces the cream/sand near-white that every
generated interface of 2026 shares. Warmth is carried by the brand and by the
ink — `foreground` holds 0.014 chroma toward hue 45 — and the page stays out of
it. `surface`, the second layer, carries 0.004: perceptible against white as a
change of plane, not as a colour.

## Contrast is measured, not estimated

Every pair below was computed from OKLCH through sRGB to WCAG relative
luminance, not judged by eye. All are in gamut.

| Pair | Ratio |
|---|---|
| `foreground` on `background` | 17.4 |
| `muted-foreground` on `background` | 5.1 |
| `muted-foreground` on `surface` | 4.8 |
| white on `brand-600` | 8.1 |
| white on `danger` | 6.6 |
| `success` on `background` | 5.7 |
| `warning` on `background` | 6.2 |
| `danger` on `background` | 6.6 |
| ring on `background` (non-text, needs 3.0) | 8.1 |

`muted-foreground` sits at L 0.540 and not the 0.58 that would look more
"muted": 0.58 measures 4.3 and fails. Light grey for elegance is the single
most common reason an interface is hard to read.

## Status colour is never the brand

The four tones — `neutral`, `positive`, `attention`, `urgent` — are the closed
set the API emits, and they are fixed across the whole CTech family. They are
never recoloured to match a surface's accent, and brand colour never appears on
a badge. `ctech-dfe/ui/DESIGN.md:179` already established this rule; billing is
the app that needed it most.

Every badge carries a glyph as well as a colour (`StatusBadge.tsx`). Colour
alone fails WCAG 2.2 1.4.1, and practically it fails first on the pair this
screen shows most: a green "Paga" beside an amber "Vence em 3 dias".

## The money is the heading

On the two screens that are *about* an amount — the home screen and the
invoice — the amount is the `h1` and there is no page title above it. This
inverts an earlier rule ("the invoice total must not outrank the page title")
that was wrong: "Suas cobranças" above a screen the nav already labels "Início"
put the largest type on the page onto the only line carrying no information.

The two list screens keep their titles. There the screen *is* the index, and
"Faturas" is a real heading rather than a restatement of the tab.

## One raised thing per screen, or none

Blocks are separated by rules, not by boxes. The home screen and the
subscriptions list have no cards at all: when every block is a bordered
`rounded-xl`, a border stops meaning "this is a unit" and the reader gets N
competing panels. `shadow-card` is still reserved for something genuinely above
the page — the open PIX charge — and now it is the only thing that has it.

## Status colour fills exactly one thing

The paid-invoice confirmation is a solid `success` panel with white text (5.7:1).
It is the single exception to "status lives on badges": having just transferred
money, a person should not have to look twice to be sure it landed, and a tinted
hairline note was not enough. No other status colour is used as a fill, and the
brand still never appears on a badge.

The brand colour gained a fourth home, the wordmark, alongside the primary
button, the active nav item and the selected row. A header that renders the
company name in body ink is a header that could belong to anybody.

## Density is an attribute, not a prop

The shell writes `data-density` on its root and every `@aoctech/ui` control
reads it: `comfortable` gives 44px targets (the portal, read on a phone),
`compact` gives 32px (the console, to come). It is the same trick
`data-dfe-theme` already proved in the family, and it is why `Button` has no
`size="console"` — height is decided by where a button is, not by each call site
remembering which screen it is on.

**The console is a `DensityScope`** (UX batch 5). Its root is
`<DensityScope density="compact">` from `@aoctech/ui`, not a bare attribute:
it writes `data-density="compact"` for the controls inside it **and** tells
the overlays opened from inside it (Drawer, Modal, Select, the row's "⋯" menu),
which render in a portal outside that element, to be compact as well. Decided
by the owner: a console drawer, modal, select list or menu is compact **on a
desk too** (32px controls at 1280px), the same density as the screen that
opened it. The portal and checkout stay `comfortable`. The bottom bar and its
Mais sheet are touch-sized whatever the density says.

**Touch keeps the compact look and grows the target** (UX batch 4; @aoctech/ui's
since batch 5). The console's 32px is for a mouse on a laptop. Under a coarse
pointer, or a viewport under `sm`, a control is **drawn** at 36px and **hit**
at 44 x 44px or more: an invisible `::after` centred on it, which only grows
past the drawing on an axis where the control is under 44px, and sideways by
at most 4px. Drawing every control 44px tall (batch 2) made a phone screen
mostly chrome; the finger needs the target, not the paint. The rule is
`@aoctech/ui`'s `touch.css`, keyed on each control's `data-slot` (`button`,
`select-trigger`, `date-picker-trigger`, `input` through its Field label,
`segmented-item`, `select-item`, `menu-item`) under any `[data-density=compact]`,
the overlays' portals included, and on the class `touch-target` for anything
else drawn as a control. `globals.css` imports `touch.css` alone, not
`styles.css`: that also brings `themes.css`, and the portals carry
`data-ctech-theme` (the account theme with no `ThemeProvider`), which would
repaint every drawer. Billing keeps only the `touch:` variant and
`scrollbar-none`. Rules that come with it:

- **Neighbours never share a target.** Keep 8px (`gap-2`) between neighbouring
  compact controls, in a row or a stack: two 4px extensions meet and never
  overlap; with less, the later control wins the shared edge. Segments touch,
  so theirs grow up and down only (`Segmented` draws that hit area itself). A
  segment billing draws by hand (the mode switch, Relatórios' and Importar's
  tabs) takes `components/ui/segmentHit` for the same 44px column; a tab row
  that wraps on a phone does not (Importar's tabs wrap their labels instead).
- **An input is reached through its label.** The Field's label's `::after`
  covers the field behind its content. Measured 36px drawn, 47px hit.
- **A clipped container clips the target.** The tab row scrolls sideways, so
  tabs stay 40px and their target grows 4px up into the row's padding. A list
  row clips only while it is swiped.
- Select options and menu items are 44px rows under touch: a list of targets
  with no gap needs its rows to be them.

Measured at 320 and 375px with touch emulation (scratchpad `b5/shoot.cjs`,
`b5/interact.cjs`): compact controls 36 drawn / 44 hit, segments 30 / 44, no
control's own drawing is reached by a neighbour's target, a swipe opens and
closes and the next tap goes through, the Mais sheet stays open while the
central action appears.

## Depth is hairlines

Structure comes from `border` at 1px. `shadow-card` marks a block that is
genuinely raised — the outstanding-invoice panel, the open PIX charge — and
`shadow-modal` marks the one thing above the page. Nothing else gets a shadow.

## Type

One family, IBM Plex Sans, at fixed rem sizes. Hierarchy is weight and size; there is no
second family and no fluid `clamp()`.

Money uses tabular figures via `[data-numeric]`, because proportional digits
make `R$ 1.199,00` and `R$ 89,90` impossible to compare down a column — which is
the one thing a list of invoices exists to let you do.

Three amount sizes, and the distinction is real: `body` inside a row, `figure`
for a headline amount inside a block (the home screen, where the invoice total
must not outrank the page title), `hero` for the amount a whole screen is about
(the invoice itself, where the title is just its number).

## Motion

150–250ms, ease-out, and only on state changes: skeleton to content, button to
PIX panel, the expiry countdown, the modal. There is no page-load choreography
and no scroll-reveal. Every animation has a `motion-reduce` alternative;
`Skeleton`'s pulse switches off entirely.

## Bans, specific to this project

- No chart on the portal. P1 is "one screen, no chart".
- No filter bar on the customer's invoice list. That is console furniture.
- No timeline, attempt list, charge id or metadata on a portal screen. They are
  not hidden — ADR 0012 keeps them out of the payload.
- No internal status string anywhere. The server sends a sentence; render it.
- No tenant or organization on any portal screen.
- No uppercase tracked eyebrow above sections, no numbered section markers, no
  gradient text, no glassmorphism, no side-stripe borders.

## Finance (console, phase 6.3b)

Shaped with `/impeccable shape` on 2026-10-08 and confirmed by the owner. The rules above still
hold; this section records what the finance screens add to them.

**Lane.** Restrained, as everywhere else: white canvas, sienna only on the primary action, the
active tab and the selected row; `danger` is the one saturated colour, and in finance it means
*vencida*. Scene: an operator — or a person managing their own money — on a laptop on a workday
morning, checking what is due this week. Light theme. References: Stripe Dashboard (dense tables
that compare down a column), Linear (tabs and in-place expansion instead of modals), Mercury
(balances as a ruled list, not cards).

**Navigation.** *Finanças* is one item of the console's top nav. Inside it, its sections are a
column on a laptop, a picker on a tablet and a bottom bar on a phone (see "Navigation and the
shell"). The header always shows the space and
the mode together — "Pessoal · Teste", "Acme LTDA · Produção" — because each can be mistaken for
the other and acting on the wrong one is the expensive mistake. A person with no organization sees
only *Finanças*, and the console opens there.

**F2, the working list.** One table per direction, grouped *Vencidas*, *Vence hoje*, *A vencer*,
with a subtotal per group in tabular figures. A row's actions expand **in place** — *Pagar* / *Receber*, edit,
cancel; the list never disappears behind a modal. The settle action is named by direction in plain
words, never "dar baixa" (ERP jargon) nor "lançar" (which means recording the bill, not paying it). Creating a bill opens a side panel. Settling for
a different amount requires the category for the gap and states it in words ("R$ 20,00 a mais —
registrado como juros"). Overdue is a badge with a glyph and text, never colour alone.

**F4, recurrences.** A list, and an editor in a side panel: a pattern (todo dia N, Nº dia útil,
Nª semana do mês, semanal, anual) plus exceptions (months, dates), with the next occurrences shown
beside it and refreshed while editing — the preview is the confirmation, before anything is saved.
When a rolled due date differs from the nominal day, both are shown ("31/01 → paga em 02/02").

**F8, accounts.** Three groups: Contas, Receitas, Despesas (by DRE group). Balances right-aligned
in tabular figures; archived items collapsed behind "Mostrar arquivadas", never deleted. System
accounts are never shown.

**F1, overview.** Three independent blocks, separated by rules: Saldos (a ruled list), Vencidas e
próximas, and Projeção. The projection is diverging bars per month — *a receber* above the axis,
*a pagar* below — where forecast bills are solid and recurrences not yet generated are outline
only: the difference is shape, not hue. The rows view shows the same numbers, in the same
Saldo / Entradas e saídas view as the chart (see "On a phone"). There is no
"resultado realizado" tile and no placeholder for one (it ships with the cash read in 6.4). This is
the first chart in billing; the portal ban on charts stands.

**States.** A skeleton per block, never a page spinner. Empty states teach the next step ("Crie
sua primeira conta para registrar contas a pagar e a receber"). Errors are per block, with
"Tentar de novo", so one failing request never blanks the screen. A role that lacks a verb sees
the control **absent**, not disabled; the server is the authority either way. Motion 150–250 ms on
state changes only (row expanding, panel opening), each with a `motion-reduce` alternative.

## Copy and controls (owner's rules, 2026-10-08)

**No em dash in interface copy.** A separation uses a bullet (`•`), a pause uses a semicolon (`;`),
and a paired aside becomes parentheses. The lone `—` standing for an empty value in a table cell or
a fact is not punctuation and stays. Code comments are not copy.

**Styled selects, never the native one.** Every choice from a list uses `@aoctech/ui`'s `Select` (billing's own until UX batch 5)
(the shadcn shape on `@base-ui/react`, sized by `data-density`). Once something is chosen the
trigger shows the option's **label**, never its value: an account shows "Conta corrente", not its
id, and the space shows "Pessoal", not `personal`. @aoctech/ui's tests pin this; actions run only from a press in the open list.
Every call site spreads `selectCopy()` first (`lib/selectCopy`): billing's placeholder ("Escolher…" / "Choose…") and the console's language.

## On a phone (finance, 2026-10-09)

A phone is its own layout, not the laptop's squeezed. Navigation is in "Navigation and the
shell" below.

**A chart becomes a list, and stays one tap away.** Under `sm`, a chart whose x-axis is
categories (the projection's months) opens as a list: one row per category, its headline
figure on the right, and a thin bar per series in the chart's colours (green in, red out), each
beside its amount. The bars are decoration; any split the chart draws by shade (the recurrences'
share) is said in words under the amount ("R$ 1.000,00 de recorrências"), never by opacity alone,
since a list row has no legend. The chart is behind an icon toggle
beside the period, never removed, so the two can be compared; on a laptop the chart is the
default and the same toggle shows a table. Rows and chart always show the same view (Saldo or
Entradas e saídas), and that switch never disappears with the chart. A chart that is shown
draws at its measured width, so its 11px labels stay 11px, and thins its month labels when a
slot is narrower than one; each bar's title still names its month.

**One row for every finance list.** `LedgerRow` (bills, recurrences, imported lines): from
`sm` a single line, description, aside, amount in a fixed column, actions; under `sm` the
description and the amount share the first line, the description wrapping to two lines rather
than being cut, then the meta, then the aside and the actions on lines of their own. An aside
that repeats a group heading (a bill's bucket badge under "Vencidas") is not shown on a phone;
one that is the only place a fact appears (a recurrence's direction) moves into the meta line.
Extrato and Contas are ledger rows too (UX batch 4): a statement line carries a second figure,
the running balance, in its own column from `sm` and under the amount as "Saldo R$ …" on a
phone; an account's name wraps beside its balance (it was cut to "Conta co…" at 375px), with
the card's mark before it. Extrato's account and period share one row on a phone, their labels
read only by a screen reader there (the chosen values say them).

**A row's secondary actions are a swipe and a "⋯"** (UX batch 4). On a phone a row keeps only
its primary action on its line (Pagar, Receber, Ver, Abrir). Edit, delete, end, reverse, undo a
payment, refund, advance, opening balance and archive are revealed by a **left swipe**:
neutral actions on `surface`, destructive ones on `danger` (the one saturated fill a list
gets, and only while uncovered). The gesture is never the only way: a visible **"⋯"** beside
the amount ("Mais ações: Aluguel") lists the same actions for a keyboard or a screen reader.
Choosing one, either way, opens the row's own step: a confirmation for anything destructive,
a form for an edit; nothing is done by the swipe itself. The gesture locks to an axis after
8px (a vertical drag stays the page's scroll, `touch-action: pan-y`), changes state past 35%
of the revealed width and snaps back otherwise, keeps one row open per page, closes on a
press elsewhere or Escape, and follows `prefers-reduced-motion`. A laptop row is unchanged:
the same actions are inline buttons. Since UX batch 5 the gesture and the menu are
`@aoctech/ui`'s `SwipeRow` and `RowMenu`; `LedgerRow` lays out the front. It stops a child's
`lostpointercapture` from reaching the front: @aoctech/ui 0.4.0 cancels the swipe on any of them,
and a finger's implicit capture moving from the title to the front sends one on every real swipe
(to be fixed in ctech-ui).

**An optional field can be emptied** (UX batch 4). An optional date has **Limpar** beside it
while it has a value, named for the date ("Limpar data de término"); an optional select lists
**Nenhuma** / **Nenhum** first (a card's brand, the default receiving account). A screen puts
`""` in an edit for "nothing" and never drops an emptied field; `lib/api/finance` sends it as
`null`, which the API reads as "clear" (absent is "keep").

**Segmented, for every two-to-four-way switch.** Direction, projection view, period, chart or
rows: `@aoctech/ui`'s `Segmented` (billing's own until batch 5), a group of pressed buttons, full width on a phone when it is
the row's only control. A shortened label carries its full name ("6 m" is "6 meses"), and the
name contains the visible text.

**Naming.** In finance, "conta" is a bank account and nothing else. Recording a bill is
**Adicionar**, and its panel is **Novo lançamento**. Once "Já foi pago" is on, the section that
held the account becomes **Pagamento** or **Recebimento**, with the date and the account it was
paid from or received into.

## Navigation and the shell (2026-10-09)

**Finanças on a phone is a bottom bar.** Under `md`, `@aoctech/ui`'s `BottomNav`
(`FinanceBottomNav`) is Finanças' navigation; the section column and the tablet picker are not
shown. Three tabs a person opens daily, **Resumo · Agenda · Extrato**, and **Mais**, a
sheet with the rest in two groups: *Lançamentos* (Recorrências, Cartões, Importar) and *Análise e
cadastro* (Relatórios, Contas). Mais reads as current while one of its sections is on screen. The
bar is touch-sized whatever `data-density` says. The console's own section row stays in the
header; the bar is Finanças only. From `md` to `lg` Finanças is a picker; from `lg` a column.

**The central action is always "create", and follows the screen.** It creates what the screen
lists, in that screen's own drawer: Agenda **Adicionar** (Novo lançamento), Cartões
**Nova compra** (or Novo cartão when there is no card yet), Recorrências **Recorrência**,
Extrato **Transferir**, Contas **Nova conta**. Resumo, Importar and Relatórios have nothing of
their own to create, so the action is **Adicionar** and it goes to Agenda to do it,
where the new line is on screen once saved. The shell asks through a one-shot request
(`lib/finance/createRequest`), never a URL parameter, so a reload or a shared link never reopens
a drawer. A role that cannot create there gets no slot, not a disabled button. On a phone the
screen's own top create button is hidden (`max-md:hidden`): it would be the same action twice.

**Nothing sits under the bar.** Finanças' layout ends its content with `BottomNavSpacer`, and
the viewport is `viewport-fit=cover`, so the bar pads itself by the home indicator's inset on an
iPhone instead of reading 0.

**Creating a space is the last entry of the space list.** "Novo espaço" sits after the spaces
behind a divider, with a plus icon, and choosing it starts the ctech-account handoff without
changing the current space (`Select`'s `actions`). There is no second button beside the select.
Gerenciar acesso stays beside it, only for the owner of a personal workspace. On a phone the
select fills the row next to the mode switch.

**The person is an avatar, in both shells.** `UserMenu` from `@aoctech/ui` holds the whole name
and the e-mail (never cut), the view switch **Portal / Console** with the current one checked,
and **Sair**. It is the only way between the shells: no "Console" link in the portal, no "Minhas
cobranças" link in the console. Console is offered to everyone signed in, since each has a
personal finance space; the operator probe only decides whether it opens on invoicing's overview
or on Finanças. On a phone it stays in the header; the bottom bar never repeats it.

**Shell controls under touch.** The space select (36px) and the mode switch (a segment, 30px)
keep the compact look with 44px targets; the section tabs stay 40px with a 4px target above
(see "Density"). The logo link, the language switch and the avatar, the header's icons, stay
44px drawn. On a desk with a mouse the console keeps 32px.

**The section is Agenda** (UX batch 4). "A pagar/receber" wrapped onto two lines in the bar.
Agenda is the section's name everywhere it is named: the bar, the column, the tablet picker,
the page title and the undo-payment confirmation ("A conta volta para a Agenda"); English says
**Schedule**. Its two sides stay *A pagar* and *A receber*, the switch at the top of the page.

## Timelines, card marks and statement colours (UX batch 3, 2026-10-09)

**A recurrence's dates are a rail.** F4's inline detail and the editor's *Próximas datas* share
`OccurrenceTimeline`: a hairline rail, one line per date (date, weekday, the state in words), and
under it, when a weekend or holiday moved it, "paga em 03/11/2026 (seg.), próximo dia útil". The
marker's shape says what exists: filled for paid (success) and overdue (danger), a firm outline for
a bill made and not yet due, a faint outline for a date the rule has not made yet (the projection's
"outline is not made yet"), a grey dot and a struck date for a skipped one. The words carry the
meaning; the marker never does alone. The detail opens under the row behind a **Ver** disclosure
(`aria-expanded`, `aria-controls`, focus stays on it), split *Próximas* | *Histórico* side by side
from `sm`, stacked on a phone. Still no chart for a recurrence.

**A card shows its mark.** `CardBrandMark` draws a simplified 32×20 mark per brand (no third-party
asset; trademarks of their owners, nominative use), always `aria-hidden` beside the brand's name.
"•••• 1234" is the only part of a number shown. A `Select` option may carry such an `icon`; the
option's name stays its label.

**A card statement is calm until it is late.** Aberta is `positive`, Fechada, Paga and Futura are
`neutral`, and `urgent` is kept for *Vencida*: closed with a statement bill, a positive total, past its
due date, unpaid (a zero or credit statement, or a month before the card's first, owes nothing). Each badge has a
glyph. No new tone: the four stay the family's closed set.

**A link to a row marks it.** Agenda reads `?direction=&bill=`, opens on that side, tints
the row with `brand-50` (the selected row) and scrolls it into view.

**Drawers follow the touch rule too.** A Drawer is portaled out of the console's root; since UX
batch 5 it follows the console's `DensityScope` (its portal carries `data-density="compact"`),
so @aoctech/ui's `touch.css` reaches it like any compact surface, and it is compact on a desk
as well. Batch 4's `ConsoleOverlay` marker is gone; the bottom bar's Mais sheet and the
portal's modals are not inside the scope and keep their own sizes (see "Density").
