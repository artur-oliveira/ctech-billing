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
  family: Geist Sans (single family, all weights)
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

## Depth is hairlines

Structure comes from `border` at 1px. `shadow-card` marks a block that is
genuinely raised — the outstanding-invoice panel, the open PIX charge — and
`shadow-modal` marks the one thing above the page. Nothing else gets a shadow.

## Type

One family, Geist, at fixed rem sizes. Hierarchy is weight and size; there is no
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
